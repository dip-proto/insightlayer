package fastly

import (
	"context"
	"io"
	"net/http"
	"net/url"

	"github.com/fastly/compute-sdk-go/fsthttp"

	"github.com/j/insightlayer/internal/app"
	"github.com/j/insightlayer/internal/observability"
	"github.com/j/insightlayer/internal/pipeline"
	"github.com/j/insightlayer/internal/stream"
)

type Adapter struct {
	handler *app.Handler
}

func NewAdapter(handler *app.Handler) *Adapter {
	return &Adapter{handler: handler}
}

func (a *Adapter) ServeHTTP(ctx context.Context, w fsthttp.ResponseWriter, r *fsthttp.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read request body")
		return
	}

	requestID := requestIDFromHeaders(r.Header)
	ctx = observability.WithRequestID(ctx, requestID)

	inbound := &app.InboundRequest{
		RequestID: requestID,
		Method:    r.Method,
		Path:      r.URL.Path,
		Headers:   map[string][]string(r.Header),
		Body:      body,
	}

	result := a.handler.Handle(ctx, inbound)

	w.Header().Set("X-Request-Id", requestID)

	if result.Stream != nil {
		a.handleStream(ctx, w, result.Stream)
		return
	}

	writeOutboundResponse(w, result.Response)
}

func writeOutboundResponse(w fsthttp.ResponseWriter, resp *app.OutboundResponse) {
	for k, vv := range resp.Headers {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	w.Write(resp.Body)
}

func writeError(w fsthttp.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write([]byte(`{"error":{"message":"` + msg + `","type":"proxy_error"}}`))
}

func (a *Adapter) handleStream(ctx context.Context, w fsthttp.ResponseWriter, sr *app.StreamRequest) {
	reader, err := sr.Backend.DoStream(ctx, sr.NormalizedReq)
	if err != nil {
		resp := a.handler.BuildErrorResponse(ctx, err)
		writeOutboundResponse(w, resp)
		return
	}
	defer func() { _ = reader.Close() }()

	emitter := newFastlyStreamEmitter(w, sr.Resolved.Route.InboundProtocol, sr.NormalizedReq.ID, sr.NormalizedReq.Model)

	if err := a.handler.HandleStream(ctx, reader, emitter); err != nil {
		pErr := a.handler.HandleError(ctx, err)
		emitter.WriteEvent(&pipeline.StreamEvent{
			Type:  pipeline.StreamEventError,
			Error: pErr,
		})
		emitter.WriteDone()
	}
}

func requestIDFromHeaders(h fsthttp.Header) string {
	if v := h.Get("X-Request-Id"); v != "" {
		return v
	}
	return ""
}

func BuildTransport(backends map[string]*url.URL) *fsthttp.Transport {
	t := fsthttp.NewTransport("default")
	for name, u := range backends {
		t.AddHostBackend(u.Host, name)
	}
	return t
}

type fastlyStreamEmitter struct {
	fw        *stream.SSEFrameWriter
	openai    *stream.OpenAIStreamEncoder
	anthropic *stream.AnthropicStreamEncoder
}

func newFastlyStreamEmitter(w fsthttp.ResponseWriter, proto pipeline.Protocol, id, model string) *fastlyStreamEmitter {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)

	e := &fastlyStreamEmitter{fw: stream.NewSSEFrameWriter(w)}
	switch proto {
	case pipeline.ProtocolOpenAI:
		e.openai = stream.NewOpenAIStreamEncoder(id, model)
	default:
		e.anthropic = stream.NewAnthropicStreamEncoder(id, model)
	}
	return e
}

func (e *fastlyStreamEmitter) WriteEvent(event *pipeline.StreamEvent) error {
	if e.openai != nil {
		sse, err := e.openai.Encode(event)
		if err != nil {
			return err
		}
		if sse != nil {
			return e.fw.WriteEvent(*sse)
		}
		return nil
	}
	events, err := e.anthropic.Encode(event)
	if err != nil {
		return err
	}
	for _, sse := range events {
		if err := e.fw.WriteEvent(sse); err != nil {
			return err
		}
	}
	return nil
}

func (e *fastlyStreamEmitter) WriteDone() error {
	if e.openai != nil {
		return e.fw.WriteEvent(*e.openai.Done())
	}
	return nil
}
