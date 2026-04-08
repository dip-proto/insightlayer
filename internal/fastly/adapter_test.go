package fastly

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/fastly/compute-sdk-go/fsthttp"

	"github.com/j/insightlayer/internal/app"
	"github.com/j/insightlayer/internal/config"
	"github.com/j/insightlayer/internal/pipeline"
	"github.com/j/insightlayer/internal/stream"
	"github.com/j/insightlayer/internal/testkit"
)

type mockResponseWriter struct {
	header     fsthttp.Header
	body       bytes.Buffer
	statusCode int
}

func newMockResponseWriter() *mockResponseWriter {
	return &mockResponseWriter{header: fsthttp.NewHeader()}
}

func (m *mockResponseWriter) Header() fsthttp.Header         { return m.header }
func (m *mockResponseWriter) WriteHeader(code int)            { m.statusCode = code }
func (m *mockResponseWriter) Write(p []byte) (int, error)     { return m.body.Write(p) }
func (m *mockResponseWriter) Close() error                    { return nil }
func (m *mockResponseWriter) SetManualFramingMode(bool)       {}
func (m *mockResponseWriter) Append(io.ReadCloser) error      { return nil }

func mockOpenAIBackend() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":      "chatcmpl-mock",
			"object":  "chat.completion",
			"created": 1700000000,
			"model":   "gpt-5.4",
			"choices": []map[string]any{
				{
					"index":         0,
					"message":       map[string]string{"role": "assistant", "content": "Hello"},
					"finish_reason": "stop",
				},
			},
			"usage": map[string]int{"prompt_tokens": 10, "completion_tokens": 3, "total_tokens": 13},
		})
	}))
}

func buildTestHandler(backendURL string) *app.Handler {
	cfg := &config.Config{
		Server: config.ServerConfig{Listen: ":0"},
		Routing: config.RoutingConfig{
			Routes: []config.RouteConfig{
				{
					Name:            "openai",
					Priority:        100,
					InboundProtocol: "openai",
					PathPrefix:      "/v1/chat/completions",
					EndpointKinds:   []string{"chat"},
					Backend:         "openai-backend",
				},
			},
		},
		Backends: []config.BackendConfig{
			{
				Name:     "openai-backend",
				Protocol: "openai",
				BaseURL:  backendURL,
				Auth:     config.AuthConfig{Mode: config.AuthModeInject, Header: "Authorization", Value: "Bearer test"},
			},
		},
	}

	handler, err := app.NewHandler(cfg, slog.Default())
	if err != nil {
		panic(fmt.Sprintf("test handler init: %v", err))
	}
	return handler
}

func newFastlyRequest(method, path string, body string) *fsthttp.Request {
	u, _ := url.Parse("http://localhost" + path)
	r := &fsthttp.Request{
		Method: method,
		URL:    u,
		Header: fsthttp.NewHeader(),
		Body:   io.NopCloser(strings.NewReader(body)),
	}
	return r
}

func TestHealthEndpointGET(t *testing.T) {
	adapter := NewAdapter(buildTestHandler("http://unused"))
	w := newMockResponseWriter()
	r := newFastlyRequest("GET", "/health", "")

	adapter.ServeHTTP(context.Background(), w, r)

	if w.statusCode != http.StatusOK {
		t.Errorf("GET /health status = %d, want %d", w.statusCode, http.StatusOK)
	}
	if got := w.header.Get("content-type"); got != "application/json" {
		t.Errorf("GET /health content-type = %q, want %q", got, "application/json")
	}

	var result map[string]string
	if err := json.Unmarshal(w.body.Bytes(), &result); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if result["status"] != "ok" {
		t.Errorf("status = %q, want %q", result["status"], "ok")
	}
}

func TestHealthEndpointHEAD(t *testing.T) {
	adapter := NewAdapter(buildTestHandler("http://unused"))
	w := newMockResponseWriter()
	r := newFastlyRequest("HEAD", "/health", "")

	adapter.ServeHTTP(context.Background(), w, r)

	if w.statusCode != http.StatusOK {
		t.Errorf("HEAD /health status = %d, want %d", w.statusCode, http.StatusOK)
	}
	if got := w.header.Get("content-type"); got != "application/json" {
		t.Errorf("HEAD /health content-type = %q, want %q", got, "application/json")
	}
	if w.body.Len() != 0 {
		t.Errorf("HEAD /health body should be empty, got %q", w.body.String())
	}
}

func TestHealthEndpointPOST(t *testing.T) {
	adapter := NewAdapter(buildTestHandler("http://unused"))
	w := newMockResponseWriter()
	r := newFastlyRequest("POST", "/health", "")

	adapter.ServeHTTP(context.Background(), w, r)

	if w.statusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST /health status = %d, want %d", w.statusCode, http.StatusMethodNotAllowed)
	}
}

func TestRequestIDGenerated(t *testing.T) {
	mock := mockOpenAIBackend()
	defer mock.Close()

	adapter := NewAdapter(buildTestHandler(mock.URL))
	w := newMockResponseWriter()
	r := newFastlyRequest("POST", "/v1/chat/completions",
		`{"model":"gpt-5.4","messages":[{"role":"user","content":"hi"}]}`)

	adapter.ServeHTTP(context.Background(), w, r)

	id := w.header.Get("x-request-id")
	if id == "" {
		t.Error("should generate request ID when none provided")
	}
	if !strings.HasPrefix(id, "req-") {
		t.Errorf("generated ID should start with req-, got %q", id)
	}
}

func TestRequestIDPreserved(t *testing.T) {
	mock := mockOpenAIBackend()
	defer mock.Close()

	adapter := NewAdapter(buildTestHandler(mock.URL))
	w := newMockResponseWriter()
	r := newFastlyRequest("POST", "/v1/chat/completions",
		`{"model":"gpt-5.4","messages":[{"role":"user","content":"hi"}]}`)
	r.Header.Set("x-request-id", "my-custom-id")

	adapter.ServeHTTP(context.Background(), w, r)

	if got := w.header.Get("x-request-id"); got != "my-custom-id" {
		t.Errorf("X-Request-Id = %q, want %q", got, "my-custom-id")
	}
}

func TestTraceHeadersPropagated(t *testing.T) {
	mock := mockOpenAIBackend()
	defer mock.Close()

	adapter := NewAdapter(buildTestHandler(mock.URL))
	w := newMockResponseWriter()
	r := newFastlyRequest("POST", "/v1/chat/completions",
		`{"model":"gpt-5.4","messages":[{"role":"user","content":"hi"}]}`)
	r.Header.Set("Traceparent", "00-abc-def-01")
	r.Header.Set("Tracestate", "vendor=value")

	adapter.ServeHTTP(context.Background(), w, r)

	if got := w.header.Get("traceparent"); got != "00-abc-def-01" {
		t.Errorf("Traceparent = %q, want %q", got, "00-abc-def-01")
	}
	if got := w.header.Get("tracestate"); got != "vendor=value" {
		t.Errorf("Tracestate = %q, want %q", got, "vendor=value")
	}
}

func TestTraceHeadersOnError(t *testing.T) {
	adapter := NewAdapter(buildTestHandler("http://unused"))
	w := newMockResponseWriter()
	r := newFastlyRequest("POST", "/nonexistent/path", `{}`)
	r.Header.Set("Traceparent", "00-err-trace-01")

	adapter.ServeHTTP(context.Background(), w, r)

	if w.statusCode < 400 {
		t.Errorf("expected error status, got %d", w.statusCode)
	}
	if got := w.header.Get("traceparent"); got != "00-err-trace-01" {
		t.Errorf("error Traceparent = %q, want %q", got, "00-err-trace-01")
	}
}

func TestNoTraceHeadersWhenAbsent(t *testing.T) {
	mock := mockOpenAIBackend()
	defer mock.Close()

	adapter := NewAdapter(buildTestHandler(mock.URL))
	w := newMockResponseWriter()
	r := newFastlyRequest("POST", "/v1/chat/completions",
		`{"model":"gpt-5.4","messages":[{"role":"user","content":"hi"}]}`)

	adapter.ServeHTTP(context.Background(), w, r)

	if got := w.header.Get("traceparent"); got != "" {
		t.Errorf("Traceparent should be absent, got %q", got)
	}
	if got := w.header.Get("tracestate"); got != "" {
		t.Errorf("Tracestate should be absent, got %q", got)
	}
}

func TestNonStreamingResponse(t *testing.T) {
	mock := mockOpenAIBackend()
	defer mock.Close()

	adapter := NewAdapter(buildTestHandler(mock.URL))
	w := newMockResponseWriter()
	r := newFastlyRequest("POST", "/v1/chat/completions",
		`{"model":"gpt-5.4","messages":[{"role":"user","content":"hi"}]}`)

	adapter.ServeHTTP(context.Background(), w, r)

	if w.statusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", w.statusCode, http.StatusOK)
	}

	var resp map[string]any
	if err := json.Unmarshal(w.body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid json response: %v", err)
	}
	choices, ok := resp["choices"].([]any)
	if !ok || len(choices) == 0 {
		t.Fatal("expected non-empty choices array")
	}
}

func TestErrorResponse(t *testing.T) {
	adapter := NewAdapter(buildTestHandler("http://unused"))
	w := newMockResponseWriter()
	r := newFastlyRequest("POST", "/nonexistent/path", `{}`)

	adapter.ServeHTTP(context.Background(), w, r)

	if w.statusCode < 400 {
		t.Errorf("expected error status, got %d", w.statusCode)
	}

	var resp map[string]any
	if err := json.Unmarshal(w.body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid json error response: %v", err)
	}
	if _, ok := resp["error"]; !ok {
		t.Error("error response should contain 'error' key")
	}
}

func TestBodyReadErrorPreservesHeaders(t *testing.T) {
	adapter := NewAdapter(buildTestHandler("http://unused"))
	w := newMockResponseWriter()

	u, _ := url.Parse("http://localhost/v1/chat/completions")
	r := &fsthttp.Request{
		Method: "POST",
		URL:    u,
		Header: fsthttp.NewHeader(),
		Body:   io.NopCloser(&testkit.BrokenReader{}),
	}
	r.Header.Set("Traceparent", "00-body-err-01")

	adapter.ServeHTTP(context.Background(), w, r)

	if got := w.header.Get("x-request-id"); got == "" {
		t.Error("should have X-Request-Id on body read error")
	}
	if !strings.HasPrefix(w.header.Get("x-request-id"), "req-") {
		t.Errorf("X-Request-Id should start with req-, got %q", w.header.Get("x-request-id"))
	}
	if got := w.header.Get("traceparent"); got != "00-body-err-01" {
		t.Errorf("Traceparent = %q, want %q on body read error", got, "00-body-err-01")
	}
}

func TestBuildTransportFromBackendURLs(t *testing.T) {
	backends := map[string]*url.URL{
		"openai":    mustURL("https://api.openai.com"),
		"anthropic": mustURL("https://api.anthropic.com"),
	}

	transport := BuildTransport(backends)
	if transport == nil {
		t.Fatal("BuildTransport returned nil")
	}
}

func TestBuildTransportEmptyBackends(t *testing.T) {
	transport := BuildTransport(map[string]*url.URL{})
	if transport == nil {
		t.Fatal("BuildTransport with empty map returned nil")
	}
}

func TestStreamEmitterOpenAIFrames(t *testing.T) {
	var buf bytes.Buffer
	fw := stream.NewSSEFrameWriter(&buf)
	enc := stream.NewOpenAIStreamEncoder("id-1", "gpt-4")

	events := []*pipeline.StreamEvent{
		{Type: pipeline.StreamEventStart},
		{Type: pipeline.StreamEventTextDelta, Text: "Hello"},
		{Type: pipeline.StreamEventStop, FinishReason: "stop"},
	}

	for _, ev := range events {
		sse, err := enc.Encode(ev)
		if err != nil {
			t.Fatalf("encode %s: %v", ev.Type, err)
		}
		if sse != nil {
			if err := fw.WriteEvent(*sse); err != nil {
				t.Fatalf("write %s: %v", ev.Type, err)
			}
		}
	}
	if err := fw.WriteEvent(*enc.Done()); err != nil {
		t.Fatalf("write done: %v", err)
	}

	output := buf.String()

	if !bytes.Contains([]byte(output), []byte("data: ")) {
		t.Error("output should contain SSE data frames")
	}
	if !bytes.Contains([]byte(output), []byte("Hello")) {
		t.Error("output should contain streamed text")
	}
	if !bytes.Contains([]byte(output), []byte("[DONE]")) {
		t.Error("output should end with [DONE]")
	}
}

func TestStreamEmitterAnthropicFrames(t *testing.T) {
	var buf bytes.Buffer
	fw := stream.NewSSEFrameWriter(&buf)
	enc := stream.NewAnthropicStreamEncoder("id-1", "claude-3")

	events := []*pipeline.StreamEvent{
		{Type: pipeline.StreamEventStart},
		{Type: pipeline.StreamEventTextDelta, Text: "Hi"},
		{Type: pipeline.StreamEventStop, FinishReason: "end_turn"},
	}

	for _, ev := range events {
		sseEvents, err := enc.Encode(ev)
		if err != nil {
			t.Fatalf("encode %s: %v", ev.Type, err)
		}
		for _, sse := range sseEvents {
			if err := fw.WriteEvent(sse); err != nil {
				t.Fatalf("write %s: %v", ev.Type, err)
			}
		}
	}

	output := buf.String()

	if !bytes.Contains([]byte(output), []byte("event: message_start")) {
		t.Error("should contain message_start event")
	}
	if !bytes.Contains([]byte(output), []byte("Hi")) {
		t.Error("should contain streamed text")
	}
	if !bytes.Contains([]byte(output), []byte("event: message_stop")) {
		t.Error("should end with message_stop")
	}
}

func mustURL(raw string) *url.URL {
	u, err := url.Parse(raw)
	if err != nil {
		panic(err)
	}
	return u
}
