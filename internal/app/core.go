package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/j/insightlayer/internal/backend"
	"github.com/j/insightlayer/internal/observability"
	"github.com/j/insightlayer/internal/pipeline"
	"github.com/j/insightlayer/internal/router"
)

type InboundRequest struct {
	RequestID string
	Method    string
	Path      string
	Headers   map[string][]string
	Body      []byte
}

type OutboundResponse struct {
	StatusCode int
	Headers    map[string][]string
	Body       []byte
}

type HandleResult struct {
	Response *OutboundResponse
	Stream   *StreamRequest
}

type StreamRequest struct {
	NormalizedReq *pipeline.NormalizedRequest
	Backend       backend.Backend
	Resolved      *router.ResolvedRoute
}

type StreamEmitter interface {
	WriteEvent(event *pipeline.StreamEvent) error
	WriteDone() error
}

// Handle processes a transport-neutral request and returns a result that is
// either a complete response (HandleResult.Response) or a streaming handoff
// (HandleResult.Stream). Pipeline and backend errors are always folded into
// HandleResult.Response via error hooks and structured logging — callers
// should inspect the result, not check for a returned error.
func (h *Handler) Handle(ctx context.Context, req *InboundRequest) *HandleResult {
	resolved, err := h.router.Resolve(req.Path)
	if err != nil {
		return errorResult(ctx, h, err)
	}

	be, err := h.backends.Get(resolved.Route.BackendName)
	if err != nil {
		return errorResult(ctx, h, err)
	}

	if isPassthroughEndpoint(resolved) {
		resp, err := h.handlePassthroughCore(ctx, req, be, resolved)
		if err != nil {
			return errorResult(ctx, h, err)
		}
		return &HandleResult{Response: resp}
	}

	nReq, err := h.decodeRequest(resolved, req.Body)
	if err != nil {
		return errorResult(ctx, h, &pipeline.PipelineError{
			StatusCode: http.StatusBadRequest,
			Message:    err.Error(),
		})
	}

	nReq.ID = req.RequestID
	nReq.Headers = http.Header(req.Headers)

	if !be.Supports(nReq.EndpointKind) {
		return errorResult(ctx, h, &pipeline.PipelineError{
			StatusCode: http.StatusBadRequest,
			Message: fmt.Sprintf(
				"backend %q does not support %s endpoints",
				be.Name(), nReq.EndpointKind,
			),
		})
	}

	if err := h.hooks.RunPreRequest(ctx, nReq); err != nil {
		return errorResult(ctx, h, err)
	}

	if nReq.Stream {
		return &HandleResult{
			Stream: &StreamRequest{
				NormalizedReq: nReq,
				Backend:       be,
				Resolved:      resolved,
			},
		}
	}

	resp, err := h.handleNonStreamCore(ctx, nReq, be, resolved)
	if err != nil {
		return errorResult(ctx, h, err)
	}
	return &HandleResult{Response: resp}
}

func (h *Handler) handleNonStreamCore(ctx context.Context, req *pipeline.NormalizedRequest, be backend.Backend, resolved *router.ResolvedRoute) (*OutboundResponse, error) {
	resp, err := be.Do(ctx, req)
	if err != nil {
		return nil, err
	}

	if h.hooks.HasPostResponseHooks() {
		preHookContent, preHookChoices := snapshotResponseTexts(resp)

		if err := h.hooks.RunPostResponse(ctx, resp); err != nil {
			return nil, err
		}

		syncResponseContent(resp, preHookContent, preHookChoices)
	}

	data, err := h.encodeResponse(resolved, resp)
	if err != nil {
		return nil, err
	}

	out := &OutboundResponse{
		StatusCode: http.StatusOK,
		Headers:    make(map[string][]string),
		Body:       data,
	}

	if resp.Headers != nil {
		for k, vv := range resp.Headers {
			out.Headers[k] = vv
		}
	}
	out.Headers["Content-Type"] = []string{"application/json"}

	return out, nil
}

func (h *Handler) handlePassthroughCore(ctx context.Context, req *InboundRequest, be backend.Backend, resolved *router.ResolvedRoute) (*OutboundResponse, error) {
	if resolved.Route.InboundProtocol != be.Protocol() {
		return nil, &pipeline.PipelineError{
			StatusCode: http.StatusBadRequest,
			Message: fmt.Sprintf(
				"cross-protocol translation not supported for %s endpoints",
				resolved.EndpointKind,
			),
		}
	}

	nReq := &pipeline.NormalizedRequest{
		ID:             req.RequestID,
		ClientProtocol: resolved.Route.InboundProtocol,
		EndpointKind:   resolved.EndpointKind,
		Headers:        http.Header(req.Headers),
		Model:          extractModelFromBody(req.Body),
	}

	if err := h.hooks.RunPreRequest(ctx, nReq); err != nil {
		return nil, err
	}

	upstreamPath := pipeline.OpenAIEndpointPath(resolved.EndpointKind)

	upstreamResp, err := be.RawProxy(ctx, req.Method, upstreamPath, bytes.NewReader(req.Body), nReq.Headers)
	if err != nil {
		return nil, err
	}
	defer func() { _ = upstreamResp.Body.Close() }()

	if upstreamResp.StatusCode >= 400 {
		errBody, _ := io.ReadAll(io.LimitReader(upstreamResp.Body, 4096))
		errHeaders := upstreamResp.Header.Clone()
		errHeaders.Del("Content-Length")
		errHeaders.Del("Content-Encoding")
		errHeaders.Del("Content-Type")
		errHeaders.Del("Transfer-Encoding")
		return nil, &pipeline.PipelineError{
			StatusCode: upstreamResp.StatusCode,
			Message:    string(errBody),
			Headers:    errHeaders,
		}
	}

	respBody, err := io.ReadAll(upstreamResp.Body)
	if err != nil {
		return nil, &pipeline.PipelineError{
			StatusCode: http.StatusBadGateway,
			Message:    "failed to read upstream response",
			Cause:      err,
		}
	}

	nResp := &pipeline.NormalizedResponse{
		Headers: upstreamResp.Header.Clone(),
	}

	if err := h.hooks.RunPostResponse(ctx, nResp); err != nil {
		return nil, err
	}

	out := &OutboundResponse{
		StatusCode: upstreamResp.StatusCode,
		Headers:    map[string][]string(nResp.Headers),
		Body:       respBody,
	}
	return out, nil
}

func errorResult(ctx context.Context, h *Handler, err error) *HandleResult {
	return &HandleResult{Response: h.buildErrorResponse(ctx, err)}
}

func (h *Handler) handleError(ctx context.Context, err error) *pipeline.PipelineError {
	pErr, ok := err.(*pipeline.PipelineError)
	if !ok {
		pErr = &pipeline.PipelineError{
			StatusCode: http.StatusInternalServerError,
			Message:    err.Error(),
		}
	}
	_ = h.hooks.RunError(ctx, pErr)
	logger := observability.LoggerFrom(ctx, h.logger)
	logger.Error("request error", "status", pErr.StatusCode, "message", pErr.Message)
	return pErr
}

func (h *Handler) buildErrorResponse(ctx context.Context, err error) *OutboundResponse {
	pErr := h.handleError(ctx, err)

	headers := make(map[string][]string)
	for k, vv := range pErr.Headers {
		headers[k] = vv
	}
	headers["Content-Type"] = []string{"application/json"}

	errBody := map[string]any{
		"message": pErr.Message,
		"type":    "proxy_error",
	}
	for k, v := range pErr.Extensions {
		errBody[k] = v
	}
	body, _ := json.Marshal(map[string]any{"error": errBody})

	return &OutboundResponse{
		StatusCode: pErr.StatusCode,
		Headers:    headers,
		Body:       body,
	}
}

// HandleStream reads events from reader, runs stream hooks, and writes them
// to emitter. The caller is responsible for obtaining the reader (typically
// via Backend.DoStream) and closing it when HandleStream returns.
func (h *Handler) HandleStream(ctx context.Context, reader backend.StreamReader, emitter StreamEmitter) error {
	for {
		event, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		if hookErr := h.hooks.RunStreamEvent(ctx, event); hookErr != nil {
			return hookErr
		}

		if err := emitter.WriteEvent(event); err != nil {
			return err
		}
	}

	return emitter.WriteDone()
}

func requestIDFromHeaders(headers map[string][]string) string {
	if v := headerGet(headers, "X-Request-Id"); v != "" {
		return v
	}
	return generateRequestID()
}

func headerGet(headers map[string][]string, key string) string {
	if vals, ok := headers[key]; ok && len(vals) > 0 {
		return vals[0]
	}
	for k, vals := range headers {
		if strings.EqualFold(k, key) && len(vals) > 0 {
			return vals[0]
		}
	}
	return ""
}
