package app

import (
	"context"
	"io"
	"net/http"

	"github.com/j/insightlayer/internal/observability"
	"github.com/j/insightlayer/internal/pipeline"
	"github.com/j/insightlayer/internal/stream"
)

const maxRequestBodyBytes = 10 * 1024 * 1024

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	requestID := NormalizeRequestID(r.Header)
	ctx := observability.WithRequestID(r.Context(), requestID)

	w.Header().Set("X-Request-Id", requestID)
	setTraceHeaders(w, r.Header)

	body, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBodyBytes+1))
	if err != nil {
		resp := h.BuildErrorResponse(ctx, &pipeline.PipelineError{
			StatusCode: http.StatusBadRequest,
			Message:    "failed to read request body",
		})
		writeOutboundResponse(w, resp)
		return
	}
	if len(body) > maxRequestBodyBytes {
		_, _ = io.Copy(io.Discard, r.Body)
		resp := h.BuildErrorResponse(ctx, &pipeline.PipelineError{
			StatusCode: http.StatusRequestEntityTooLarge,
			Message:    "request body too large",
		})
		writeOutboundResponse(w, resp)
		return
	}

	inbound := &InboundRequest{
		RequestID: requestID,
		Method:    r.Method,
		Path:      r.URL.Path,
		Headers:   r.Header.Clone(),
		Body:      body,
	}

	result := h.Handle(ctx, inbound)

	if result.Stream != nil {
		h.handleHTTPStream(ctx, w, result.Stream)
		return
	}

	writeOutboundResponse(w, result.Response)
}

func setTraceHeaders(w http.ResponseWriter, headers http.Header) {
	if tp := headers.Get("Traceparent"); tp != "" {
		w.Header().Set("Traceparent", tp)
	}
	if ts := headers.Get("Tracestate"); ts != "" {
		w.Header().Set("Tracestate", ts)
	}
}

func writeOutboundResponse(w http.ResponseWriter, resp *OutboundResponse) {
	for k, vv := range resp.Headers {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(resp.Body)
}

type sseStreamEmitter struct {
	sseW      *stream.SSEWriter
	openai    *stream.OpenAIStreamEncoder
	anthropic *stream.AnthropicStreamEncoder
}

func newSSEStreamEmitter(w http.ResponseWriter, proto pipeline.Protocol, id, model string) (*sseStreamEmitter, error) {
	sseW, err := stream.NewSSEWriter(w)
	if err != nil {
		return nil, err
	}
	e := &sseStreamEmitter{sseW: sseW}
	switch proto {
	case pipeline.ProtocolOpenAI:
		e.openai = stream.NewOpenAIStreamEncoder(id, model)
	default:
		e.anthropic = stream.NewAnthropicStreamEncoder(id, model)
	}
	return e, nil
}

func (e *sseStreamEmitter) WriteEvent(event *pipeline.StreamEvent) error {
	if e.openai != nil {
		sse, err := e.openai.Encode(event)
		if err != nil {
			return err
		}
		if sse != nil {
			return e.sseW.WriteEvent(*sse)
		}
		return nil
	}
	events, err := e.anthropic.Encode(event)
	if err != nil {
		return err
	}
	for _, sse := range events {
		if err := e.sseW.WriteEvent(sse); err != nil {
			return err
		}
	}
	return nil
}

func (e *sseStreamEmitter) WriteDone() error {
	if e.openai != nil {
		return e.sseW.WriteEvent(*e.openai.Done())
	}
	return nil
}

func (h *Handler) handleHTTPStream(ctx context.Context, w http.ResponseWriter, sr *StreamRequest) {
	reader, err := sr.Backend.DoStream(ctx, sr.NormalizedReq)
	if err != nil {
		resp := h.BuildErrorResponse(ctx, err)
		writeOutboundResponse(w, resp)
		return
	}
	defer func() { _ = reader.Close() }()

	emitter, err := newSSEStreamEmitter(w, sr.Resolved.Route.InboundProtocol, sr.NormalizedReq.ID, sr.NormalizedReq.Model)
	if err != nil {
		resp := h.BuildErrorResponse(ctx, err)
		writeOutboundResponse(w, resp)
		return
	}

	if err := h.HandleStream(ctx, reader, emitter); err != nil {
		h.handleHTTPStreamError(ctx, emitter, err)
	}
}

func (h *Handler) handleHTTPStreamError(ctx context.Context, emitter StreamEmitter, err error) {
	pErr := h.HandleError(ctx, err)
	_ = emitter.WriteEvent(&pipeline.StreamEvent{
		Type:  pipeline.StreamEventError,
		Error: pErr,
	})
	_ = emitter.WriteDone()
}
