package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"github.com/j/insightlayer/internal/backend"
	anthropicbe "github.com/j/insightlayer/internal/backend/anthropic"
	openaibe "github.com/j/insightlayer/internal/backend/openai"
	"github.com/j/insightlayer/internal/config"
	"github.com/j/insightlayer/internal/hooks"
	"github.com/j/insightlayer/internal/observability"
	"github.com/j/insightlayer/internal/pipeline"
	anthropiccodec "github.com/j/insightlayer/internal/protocol/anthropic"
	openaicodec "github.com/j/insightlayer/internal/protocol/openai"
	"github.com/j/insightlayer/internal/router"
	"github.com/j/insightlayer/internal/stream"
)

type Handler struct {
	router   *router.Router
	backends *backend.Registry
	hooks    *hooks.Manager
	logger   *slog.Logger
}

func NewHandler(cfg *config.Config, logger *slog.Logger) (*Handler, error) {
	reg := backend.NewRegistry()
	for _, bcfg := range cfg.Backends {
		var be backend.Backend
		switch pipeline.Protocol(bcfg.Protocol) {
		case pipeline.ProtocolOpenAI:
			be = openaibe.New(bcfg, nil)
		case pipeline.ProtocolAnthropic:
			be = anthropicbe.New(bcfg, nil)
		default:
			return nil, fmt.Errorf("unknown backend protocol: %s", bcfg.Protocol)
		}
		reg.Register(be)
	}

	r, err := router.New(cfg.Routing, cfg.Backends)
	if err != nil {
		return nil, fmt.Errorf("router init: %w", err)
	}

	var hm *hooks.Manager
	if len(cfg.Hooks) > 0 {
		hm, err = hooks.BuildFromConfig(cfg.Hooks)
		if err != nil {
			return nil, fmt.Errorf("hooks init: %w", err)
		}
	} else {
		hm = hooks.NewManager()
	}

	return &Handler{
		router:   r,
		backends: reg,
		hooks:    hm,
		logger:   logger,
	}, nil
}

func (h *Handler) HookManager() *hooks.Manager {
	return h.hooks
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	requestID := r.Header.Get("X-Request-Id")
	if requestID == "" {
		requestID = generateRequestID()
	}

	ctx := observability.WithRequestID(r.Context(), requestID)
	logger := observability.LoggerFrom(ctx, h.logger)

	resolved, err := h.router.Resolve(r)
	if err != nil {
		h.writeErrorWithContext(ctx, w, err, logger)
		return
	}

	be, err := h.backends.Get(resolved.Route.BackendName)
	if err != nil {
		h.writeErrorWithContext(ctx, w, err, logger)
		return
	}

	w.Header().Set("X-Request-Id", requestID)
	if tp := r.Header.Get("Traceparent"); tp != "" {
		w.Header().Set("Traceparent", tp)
	}
	if ts := r.Header.Get("Tracestate"); ts != "" {
		w.Header().Set("Tracestate", ts)
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		h.writeErrorWithContext(ctx, w, &pipeline.PipelineError{
			StatusCode: http.StatusBadRequest,
			Message:    "failed to read request body",
		}, logger)
		return
	}

	if isPassthroughEndpoint(resolved) {
		if resolved.Route.InboundProtocol != be.Protocol() {
			h.writeErrorWithContext(ctx, w, &pipeline.PipelineError{
				StatusCode: http.StatusBadRequest,
				Message: fmt.Sprintf(
					"cross-protocol translation not supported for %s endpoints",
					resolved.EndpointKind,
				),
			}, logger)
			return
		}

		req := &pipeline.NormalizedRequest{
			ID:             requestID,
			ClientProtocol: resolved.Route.InboundProtocol,
			EndpointKind:   resolved.EndpointKind,
			Headers:        r.Header.Clone(),
			Model:          extractModelFromBody(body),
		}

		if err := h.hooks.RunPreRequest(ctx, req); err != nil {
			h.writeErrorWithContext(ctx, w, err, logger)
			return
		}

		h.handlePassthrough(ctx, w, r.Method, body, req.Headers, be, resolved, logger)
		return
	}

	req, err := h.decodeRequest(resolved, body)
	if err != nil {
		h.writeErrorWithContext(ctx, w, &pipeline.PipelineError{
			StatusCode: http.StatusBadRequest,
			Message:    err.Error(),
		}, logger)
		return
	}

	req.ID = requestID
	req.Headers = r.Header.Clone()

	if !be.Supports(req.EndpointKind) {
		h.writeErrorWithContext(ctx, w, &pipeline.PipelineError{
			StatusCode: http.StatusBadRequest,
			Message: fmt.Sprintf(
				"backend %q does not support %s endpoints",
				be.Name(), req.EndpointKind,
			),
		}, logger)
		return
	}

	if err := h.hooks.RunPreRequest(ctx, req); err != nil {
		h.writeErrorWithContext(ctx, w, err, logger)
		return
	}

	if req.Stream {
		h.handleStream(ctx, w, req, be, resolved, logger)
		return
	}

	h.handleNonStream(ctx, w, req, be, resolved, logger)
}

func (h *Handler) handleNonStream(ctx context.Context, w http.ResponseWriter, req *pipeline.NormalizedRequest, be backend.Backend, resolved *router.ResolvedRoute, logger *slog.Logger) {
	resp, err := be.Do(ctx, req)
	if err != nil {
		h.writeErrorWithContext(ctx, w, err, logger)
		return
	}

	if h.hooks.HasPostResponseHooks() {
		preHookContent, preHookChoices := snapshotResponseTexts(resp)

		if err := h.hooks.RunPostResponse(ctx, resp); err != nil {
			h.writeErrorWithContext(ctx, w, err, logger)
			return
		}

		syncResponseContent(resp, preHookContent, preHookChoices)
	}

	data, err := h.encodeResponse(resolved, resp)
	if err != nil {
		h.writeErrorWithContext(ctx, w, err, logger)
		return
	}

	if resp.Headers != nil {
		for k, vv := range resp.Headers {
			for _, v := range vv {
				w.Header().Add(k, v)
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

type clientStreamEncoder struct {
	openai    *stream.OpenAIStreamEncoder
	anthropic *stream.AnthropicStreamEncoder
}

func newClientStreamEncoder(proto pipeline.Protocol, id, model string) clientStreamEncoder {
	switch proto {
	case pipeline.ProtocolOpenAI:
		return clientStreamEncoder{openai: stream.NewOpenAIStreamEncoder(id, model)}
	default:
		return clientStreamEncoder{anthropic: stream.NewAnthropicStreamEncoder(id, model)}
	}
}

func (e *clientStreamEncoder) writeEvent(w *stream.SSEWriter, event *pipeline.StreamEvent) error {
	if e.openai != nil {
		sse, err := e.openai.Encode(event)
		if err != nil {
			return err
		}
		if sse != nil {
			return w.WriteEvent(*sse)
		}
		return nil
	}
	events, err := e.anthropic.Encode(event)
	if err != nil {
		return err
	}
	for _, sse := range events {
		if err := w.WriteEvent(sse); err != nil {
			return err
		}
	}
	return nil
}

func (e *clientStreamEncoder) writeDone(w *stream.SSEWriter) {
	if e.openai != nil {
		_ = w.WriteEvent(*e.openai.Done())
	}
}

func (h *Handler) handleStream(ctx context.Context, w http.ResponseWriter, req *pipeline.NormalizedRequest, be backend.Backend, resolved *router.ResolvedRoute, logger *slog.Logger) {
	sr, err := be.DoStream(ctx, req)
	if err != nil {
		h.writeErrorWithContext(ctx, w, err, logger)
		return
	}
	defer func() { _ = sr.Close() }()

	sseW, err := stream.NewSSEWriter(w)
	if err != nil {
		h.writeErrorWithContext(ctx, w, err, logger)
		return
	}

	enc := newClientStreamEncoder(resolved.Route.InboundProtocol, req.ID, req.Model)

	for {
		event, err := sr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			h.handleStreamError(ctx, sseW, &enc, err, logger)
			return
		}

		if hookErr := h.hooks.RunStreamEvent(ctx, event); hookErr != nil {
			h.handleStreamError(ctx, sseW, &enc, hookErr, logger)
			return
		}

		if err := enc.writeEvent(sseW, event); err != nil {
			h.handleStreamError(ctx, sseW, &enc, err, logger)
			return
		}
	}

	enc.writeDone(sseW)
}

func (h *Handler) handleStreamError(ctx context.Context, sseW *stream.SSEWriter, enc *clientStreamEncoder, err error, logger *slog.Logger) {
	pErr, ok := err.(*pipeline.PipelineError)
	if !ok {
		pErr = &pipeline.PipelineError{
			Cause:      err,
			StatusCode: http.StatusInternalServerError,
			Message:    err.Error(),
		}
	}

	_ = h.hooks.RunError(ctx, pErr)

	logger.Error("stream error", "status", pErr.StatusCode, "message", pErr.Message)

	errEvent := &pipeline.StreamEvent{
		Type:  pipeline.StreamEventError,
		Error: pErr,
	}
	_ = enc.writeEvent(sseW, errEvent)
	enc.writeDone(sseW)
}

func isPassthroughEndpoint(resolved *router.ResolvedRoute) bool {
	switch resolved.EndpointKind {
	case pipeline.EndpointEmbedding, pipeline.EndpointModelList:
		return true
	}
	return false
}

func (h *Handler) handlePassthrough(ctx context.Context, w http.ResponseWriter, method string, body []byte, headers http.Header, be backend.Backend, resolved *router.ResolvedRoute, logger *slog.Logger) {
	upstreamPath := pipeline.OpenAIEndpointPath(resolved.EndpointKind)

	upstreamResp, err := be.RawProxy(ctx, method, upstreamPath, bytes.NewReader(body), headers)
	if err != nil {
		h.writeErrorWithContext(ctx, w, err, logger)
		return
	}
	defer func() { _ = upstreamResp.Body.Close() }()

	if upstreamResp.StatusCode >= 400 {
		errBody, _ := io.ReadAll(io.LimitReader(upstreamResp.Body, 4096))
		errHeaders := upstreamResp.Header.Clone()
		errHeaders.Del("Content-Length")
		errHeaders.Del("Content-Encoding")
		errHeaders.Del("Content-Type")
		errHeaders.Del("Transfer-Encoding")
		h.writeErrorWithContext(ctx, w, &pipeline.PipelineError{
			StatusCode: upstreamResp.StatusCode,
			Message:    string(errBody),
			Headers:    errHeaders,
		}, logger)
		return
	}

	respBody, err := io.ReadAll(upstreamResp.Body)
	if err != nil {
		h.writeErrorWithContext(ctx, w, &pipeline.PipelineError{
			StatusCode: http.StatusBadGateway,
			Message:    "failed to read upstream response",
			Cause:      err,
		}, logger)
		return
	}

	nResp := &pipeline.NormalizedResponse{
		Headers: upstreamResp.Header.Clone(),
	}

	if err := h.hooks.RunPostResponse(ctx, nResp); err != nil {
		h.writeErrorWithContext(ctx, w, err, logger)
		return
	}

	for k, vv := range nResp.Headers {
		w.Header()[k] = vv
	}

	w.WriteHeader(upstreamResp.StatusCode)
	_, _ = w.Write(respBody)
}

func (h *Handler) decodeRequest(resolved *router.ResolvedRoute, body []byte) (*pipeline.NormalizedRequest, error) {
	switch resolved.Route.InboundProtocol {
	case pipeline.ProtocolOpenAI:
		switch resolved.EndpointKind {
		case pipeline.EndpointCompletion:
			return openaicodec.DecodeCompletionRequest(body)
		default:
			return openaicodec.DecodeRequest(body)
		}
	case pipeline.ProtocolAnthropic:
		return anthropiccodec.DecodeRequest(body)
	default:
		return nil, fmt.Errorf("unsupported inbound protocol: %s", resolved.Route.InboundProtocol)
	}
}

func (h *Handler) encodeResponse(resolved *router.ResolvedRoute, resp *pipeline.NormalizedResponse) ([]byte, error) {
	switch resolved.Route.InboundProtocol {
	case pipeline.ProtocolOpenAI:
		switch resolved.EndpointKind {
		case pipeline.EndpointCompletion:
			return openaicodec.EncodeCompletionResponse(resp)
		default:
			return openaicodec.EncodeResponse(resp)
		}
	case pipeline.ProtocolAnthropic:
		return anthropiccodec.EncodeResponse(resp)
	default:
		return nil, fmt.Errorf("unsupported inbound protocol: %s", resolved.Route.InboundProtocol)
	}
}

func (h *Handler) writeErrorWithContext(ctx context.Context, w http.ResponseWriter, err error, logger *slog.Logger) {
	pErr, ok := err.(*pipeline.PipelineError)
	if !ok {
		pErr = &pipeline.PipelineError{
			StatusCode: http.StatusInternalServerError,
			Message:    err.Error(),
		}
	}

	_ = h.hooks.RunError(ctx, pErr)

	logger.Error("request error", "status", pErr.StatusCode, "message", pErr.Message)

	for k, vv := range pErr.Headers {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(pErr.StatusCode)

	errBody := map[string]any{
		"message": pErr.Message,
		"type":    "proxy_error",
	}
	if len(pErr.Extensions) > 0 {
		for k, v := range pErr.Extensions {
			errBody[k] = v
		}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"error": errBody})
}

func snapshotResponseTexts(resp *pipeline.NormalizedResponse) (string, []string) {
	texts := make([]string, len(resp.Choices))
	for i, c := range resp.Choices {
		texts[i] = c.Text
	}
	return resp.Content, texts
}

func syncResponseContent(resp *pipeline.NormalizedResponse, preContent string, preChoices []string) {
	if len(resp.Choices) == 0 {
		return
	}

	contentChanged := resp.Content != preContent
	choicesChanged := false
	for i, c := range resp.Choices {
		if i < len(preChoices) && c.Text != preChoices[i] {
			choicesChanged = true
			break
		}
	}

	switch {
	case contentChanged && !choicesChanged:
		resp.Choices[0].Text = resp.Content
	case choicesChanged:
		resp.Content = resp.Choices[0].Text
	}
}

func extractModelFromBody(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	var partial struct {
		Model string `json:"model"`
	}
	_ = json.Unmarshal(body, &partial)
	return partial.Model
}

func generateRequestID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return "req-" + hex.EncodeToString(b)
}
