package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/j/insightlayer/internal/config"
	"github.com/j/insightlayer/internal/pipeline"
	"github.com/j/insightlayer/internal/testkit"
)

func mockOpenAIBackend() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)

		if stream, ok := req["stream"].(bool); ok && stream {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprint(w, `data: {"id":"chatcmpl-mock","object":"chat.completion.chunk","created":1700000000,"model":"gpt-5.4","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}

data: {"id":"chatcmpl-mock","object":"chat.completion.chunk","created":1700000000,"model":"gpt-5.4","choices":[{"index":0,"delta":{"content":"Hi from OpenAI"},"finish_reason":null}]}

data: {"id":"chatcmpl-mock","object":"chat.completion.chunk","created":1700000000,"model":"gpt-5.4","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":3,"total_tokens":13}}

data: [DONE]

`)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":      "chatcmpl-mock",
			"object":  "chat.completion",
			"created": 1700000000,
			"model":   "gpt-5.4",
			"choices": []map[string]any{
				{
					"index":         0,
					"message":       map[string]string{"role": "assistant", "content": "Hi from OpenAI"},
					"finish_reason": "stop",
				},
			},
			"usage": map[string]int{"prompt_tokens": 10, "completion_tokens": 3, "total_tokens": 13},
		})
	}))
}

func mockAnthropicBackend() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)

		if stream, ok := req["stream"].(bool); ok && stream {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprint(w, `event: message_start
data: {"type":"message_start","message":{"id":"msg_mock","type":"message","role":"assistant","model":"claude-sonnet-4-6","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":10,"output_tokens":0}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hi from Anthropic"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":4}}

event: message_stop
data: {"type":"message_stop"}

`)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":    "msg_mock",
			"type":  "message",
			"role":  "assistant",
			"model": "claude-sonnet-4-6",
			"content": []map[string]string{
				{"type": "text", "text": "Hi from Anthropic"},
			},
			"stop_reason": "end_turn",
			"usage":       map[string]int{"input_tokens": 10, "output_tokens": 4},
		})
	}))
}

func buildConfig(openaiURL, anthropicURL string) *config.Config {
	return &config.Config{
		Server: config.ServerConfig{Listen: ":0"},
		Routing: config.RoutingConfig{
			Routes: []config.RouteConfig{
				{
					Name:            "openai-to-openai",
					Priority:        100,
					InboundProtocol: "openai",
					PathPrefix:      "/oai-oai/v1/chat/completions",
					EndpointKinds:   []string{"chat"},
					Backend:         "openai-backend",
				},
				{
					Name:            "openai-to-anthropic",
					Priority:        100,
					InboundProtocol: "openai",
					PathPrefix:      "/oai-ant/v1/chat/completions",
					EndpointKinds:   []string{"chat"},
					Backend:         "anthropic-backend",
				},
				{
					Name:            "anthropic-to-anthropic",
					Priority:        100,
					InboundProtocol: "anthropic",
					PathPrefix:      "/ant-ant/v1/messages",
					EndpointKinds:   []string{"chat"},
					Backend:         "anthropic-backend",
				},
				{
					Name:            "anthropic-to-openai",
					Priority:        100,
					InboundProtocol: "anthropic",
					PathPrefix:      "/ant-oai/v1/messages",
					EndpointKinds:   []string{"chat"},
					Backend:         "openai-backend",
				},
			},
		},
		Backends: []config.BackendConfig{
			{
				Name:     "openai-backend",
				Protocol: "openai",
				BaseURL:  openaiURL,
				Auth:     config.AuthConfig{Mode: config.AuthModeInject, Header: "Authorization", Value: "Bearer test"},
			},
			{
				Name:     "anthropic-backend",
				Protocol: "anthropic",
				BaseURL:  anthropicURL,
				Auth:     config.AuthConfig{Mode: config.AuthModeInject, Header: "x-api-key", Value: "test-key"},
				DefaultHeaders: map[string]string{
					"anthropic-version": "2023-06-01",
				},
			},
		},
	}
}

func TestNonStreamingCombinations(t *testing.T) {
	openaiMock := mockOpenAIBackend()
	defer openaiMock.Close()
	anthropicMock := mockAnthropicBackend()
	defer anthropicMock.Close()

	cfg := buildConfig(openaiMock.URL, anthropicMock.URL)
	handler, err := NewHandler(cfg, slog.Default())
	if err != nil {
		t.Fatalf("init: %v", err)
	}

	cases := []struct {
		name        string
		requestPath string
		requestBody string
		wantContent string
	}{
		{
			name:        "OpenAI client to OpenAI upstream",
			requestPath: "/oai-oai/v1/chat/completions",
			requestBody: `{"model":"gpt-5.4","messages":[{"role":"user","content":"test"}]}`,
			wantContent: "Hi from OpenAI",
		},
		{
			name:        "OpenAI client to Anthropic upstream",
			requestPath: "/oai-ant/v1/chat/completions",
			requestBody: `{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"test"}]}`,
			wantContent: "Hi from Anthropic",
		},
		{
			name:        "Anthropic client to Anthropic upstream",
			requestPath: "/ant-ant/v1/messages",
			requestBody: `{"model":"claude-sonnet-4-6","max_tokens":100,"messages":[{"role":"user","content":"test"}]}`,
			wantContent: "Hi from Anthropic",
		},
		{
			name:        "Anthropic client to OpenAI upstream",
			requestPath: "/ant-oai/v1/messages",
			requestBody: `{"model":"gpt-5.4","max_tokens":100,"messages":[{"role":"user","content":"test"}]}`,
			wantContent: "Hi from OpenAI",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", tc.requestPath, strings.NewReader(tc.requestBody))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}

			body := rec.Body.String()
			if !strings.Contains(body, tc.wantContent) {
				t.Errorf("response does not contain %q:\n%s", tc.wantContent, body)
			}
		})
	}
}

func TestStreamingCombinations(t *testing.T) {
	openaiMock := mockOpenAIBackend()
	defer openaiMock.Close()
	anthropicMock := mockAnthropicBackend()
	defer anthropicMock.Close()

	cfg := buildConfig(openaiMock.URL, anthropicMock.URL)
	handler, err := NewHandler(cfg, slog.Default())
	if err != nil {
		t.Fatalf("init: %v", err)
	}

	cases := []struct {
		name        string
		requestPath string
		requestBody string
		wantContent string
	}{
		{
			name:        "Streaming: OpenAI to OpenAI",
			requestPath: "/oai-oai/v1/chat/completions",
			requestBody: `{"model":"gpt-5.4","messages":[{"role":"user","content":"test"}],"stream":true}`,
			wantContent: "Hi from OpenAI",
		},
		{
			name:        "Streaming: OpenAI to Anthropic",
			requestPath: "/oai-ant/v1/chat/completions",
			requestBody: `{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"test"}],"stream":true}`,
			wantContent: "Hi from Anthropic",
		},
		{
			name:        "Streaming: Anthropic to Anthropic",
			requestPath: "/ant-ant/v1/messages",
			requestBody: `{"model":"claude-sonnet-4-6","max_tokens":100,"messages":[{"role":"user","content":"test"}],"stream":true}`,
			wantContent: "Hi from Anthropic",
		},
		{
			name:        "Streaming: Anthropic to OpenAI",
			requestPath: "/ant-oai/v1/messages",
			requestBody: `{"model":"gpt-5.4","max_tokens":100,"messages":[{"role":"user","content":"test"}],"stream":true}`,
			wantContent: "Hi from OpenAI",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", tc.requestPath, strings.NewReader(tc.requestBody))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}

			ct := rec.Header().Get("Content-Type")
			if ct != "text/event-stream" {
				t.Errorf("content-type = %q, want text/event-stream", ct)
			}

			body := rec.Body.String()
			if !strings.Contains(body, tc.wantContent) {
				t.Errorf("stream does not contain %q:\n%s", tc.wantContent, body)
			}
		})
	}
}

func TestRequestIDPropagation(t *testing.T) {
	openaiMock := mockOpenAIBackend()
	defer openaiMock.Close()

	cfg := buildConfig(openaiMock.URL, openaiMock.URL)
	handler, err := NewHandler(cfg, slog.Default())
	if err != nil {
		t.Fatalf("init: %v", err)
	}

	t.Run("provided request ID is echoed", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/oai-oai/v1/chat/completions",
			strings.NewReader(`{"model":"gpt-5.4","messages":[{"role":"user","content":"test"}]}`))
		req.Header.Set("X-Request-Id", "my-trace-123")
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if rec.Header().Get("X-Request-Id") != "my-trace-123" {
			t.Errorf("X-Request-Id = %q, want %q", rec.Header().Get("X-Request-Id"), "my-trace-123")
		}
	})

	t.Run("generated request ID when none provided", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/oai-oai/v1/chat/completions",
			strings.NewReader(`{"model":"gpt-5.4","messages":[{"role":"user","content":"test"}]}`))
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		id := rec.Header().Get("X-Request-Id")
		if id == "" {
			t.Error("should generate request ID when none provided")
		}
		if !strings.HasPrefix(id, "req-") {
			t.Errorf("generated ID should start with req-, got %q", id)
		}
	})
}

func TestRequestIDFromHeadersCaseInsensitive(t *testing.T) {
	cases := []struct {
		name    string
		headers map[string][]string
		wantID  string
	}{
		{
			name:    "canonical key",
			headers: map[string][]string{"X-Request-Id": {"canonical-123"}},
			wantID:  "canonical-123",
		},
		{
			name:    "lowercase key",
			headers: map[string][]string{"x-request-id": {"lower-456"}},
			wantID:  "lower-456",
		},
		{
			name:    "mixed case key",
			headers: map[string][]string{"X-REQUEST-ID": {"upper-789"}},
			wantID:  "upper-789",
		},
		{
			name:    "empty value generates new ID",
			headers: map[string][]string{"X-Request-Id": {""}},
			wantID:  "",
		},
		{
			name:    "missing key generates new ID",
			headers: map[string][]string{},
			wantID:  "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := NormalizeRequestID(tc.headers)
			if tc.wantID != "" {
				if got != tc.wantID {
					t.Errorf("NormalizeRequestID = %q, want %q", got, tc.wantID)
				}
			} else {
				if !strings.HasPrefix(got, "req-") {
					t.Errorf("expected generated ID starting with req-, got %q", got)
				}
			}
		})
	}
}

func TestTraceHeaderPropagation(t *testing.T) {
	openaiMock := mockOpenAIBackend()
	defer openaiMock.Close()

	cfg := buildConfig(openaiMock.URL, openaiMock.URL)
	handler, err := NewHandler(cfg, slog.Default())
	if err != nil {
		t.Fatalf("init: %v", err)
	}

	t.Run("success response", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/oai-oai/v1/chat/completions",
			strings.NewReader(`{"model":"gpt-5.4","messages":[{"role":"user","content":"test"}]}`))
		req.Header.Set("Traceparent", "00-abc-def-01")
		req.Header.Set("Tracestate", "vendor=value")
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if got := rec.Header().Get("Traceparent"); got != "00-abc-def-01" {
			t.Errorf("Traceparent = %q, want %q", got, "00-abc-def-01")
		}
		if got := rec.Header().Get("Tracestate"); got != "vendor=value" {
			t.Errorf("Tracestate = %q, want %q", got, "vendor=value")
		}
	})

	t.Run("error response", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/nonexistent/path",
			strings.NewReader(`{}`))
		req.Header.Set("Traceparent", "00-err-trace-01")
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if got := rec.Header().Get("Traceparent"); got != "00-err-trace-01" {
			t.Errorf("Traceparent = %q, want %q", got, "00-err-trace-01")
		}
	})

	t.Run("absent when not sent", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/oai-oai/v1/chat/completions",
			strings.NewReader(`{"model":"gpt-5.4","messages":[{"role":"user","content":"test"}]}`))
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if got := rec.Header().Get("Traceparent"); got != "" {
			t.Errorf("Traceparent should be absent, got %q", got)
		}
	})

	t.Run("body read error", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/oai-oai/v1/chat/completions", &testkit.BrokenReader{})
		req.Header.Set("Traceparent", "00-body-err-01")
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if got := rec.Header().Get("X-Request-Id"); got == "" {
			t.Error("should have X-Request-Id on body read error")
		}
		if got := rec.Header().Get("Traceparent"); got != "00-body-err-01" {
			t.Errorf("Traceparent = %q, want %q on body read error", got, "00-body-err-01")
		}
	})
}

func TestHookIntegration(t *testing.T) {
	openaiMock := mockOpenAIBackend()
	defer openaiMock.Close()

	cfg := buildConfig(openaiMock.URL, openaiMock.URL)
	handler, err := NewHandler(cfg, slog.Default())
	if err != nil {
		t.Fatalf("init: %v", err)
	}

	hookRan := false
	handler.HookManager().AddPreRequest(&testPreRequestHook{
		name: "test-hook",
		fn: func(req *pipeline.NormalizedRequest) error {
			hookRan = true
			return nil
		},
	})

	req := httptest.NewRequest("POST", "/oai-oai/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-5.4","messages":[{"role":"user","content":"test"}]}`))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if !hookRan {
		t.Error("pre-request hook should have run")
	}
}

func TestStreamHookMutation(t *testing.T) {
	openaiMock := mockOpenAIBackend()
	defer openaiMock.Close()

	cfg := buildConfig(openaiMock.URL, openaiMock.URL)
	handler, err := NewHandler(cfg, slog.Default())
	if err != nil {
		t.Fatalf("init: %v", err)
	}

	handler.HookManager().AddStreamEvent(&testStreamEventHook{
		name: "replace-text",
		fn: func(event *pipeline.StreamEvent) error {
			if event.Type == pipeline.StreamEventTextDelta {
				event.Text = "REPLACED"
			}
			return nil
		},
	})

	req := httptest.NewRequest("POST", "/oai-oai/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-5.4","messages":[{"role":"user","content":"test"}],"stream":true}`))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, "REPLACED") {
		t.Errorf("stream hook mutation should be visible in output:\n%s", body)
	}
	if strings.Contains(body, "Hi from OpenAI") {
		t.Error("original text should have been replaced")
	}
}

func mockOpenAIFullBackend() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "/completions"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":      "cmpl-mock",
				"object":  "text_completion",
				"choices": []map[string]any{{"text": "completed text", "finish_reason": "stop"}},
			})
		case strings.Contains(r.URL.Path, "/embeddings"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"object": "list",
				"data":   []map[string]any{{"object": "embedding", "embedding": []float64{0.1, 0.2}}},
			})
		case strings.Contains(r.URL.Path, "/models"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"object": "list",
				"data":   []map[string]any{{"id": "gpt-5.4", "object": "model"}},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func buildPassthroughConfig(openaiURL string) *config.Config {
	return &config.Config{
		Server: config.ServerConfig{Listen: ":0"},
		Routing: config.RoutingConfig{
			Routes: []config.RouteConfig{
				{
					Name:            "openai-completions",
					Priority:        100,
					InboundProtocol: "openai",
					PathPrefix:      "/v1/completions",
					EndpointKinds:   []string{"completion"},
					Backend:         "openai-backend",
				},
				{
					Name:            "openai-embeddings",
					Priority:        100,
					InboundProtocol: "openai",
					PathPrefix:      "/v1/embeddings",
					EndpointKinds:   []string{"embedding"},
					Backend:         "openai-backend",
				},
				{
					Name:            "openai-models",
					Priority:        100,
					InboundProtocol: "openai",
					PathPrefix:      "/v1/models",
					EndpointKinds:   []string{"model_list"},
					Backend:         "openai-backend",
				},
			},
		},
		Backends: []config.BackendConfig{
			{
				Name:     "openai-backend",
				Protocol: "openai",
				BaseURL:  openaiURL,
				Auth:     config.AuthConfig{Mode: config.AuthModeInject, Header: "Authorization", Value: "Bearer test"},
			},
		},
	}
}

func TestPassthroughEndpoints(t *testing.T) {
	mock := mockOpenAIFullBackend()
	defer mock.Close()

	cfg := buildPassthroughConfig(mock.URL)
	handler, err := NewHandler(cfg, slog.Default())
	if err != nil {
		t.Fatalf("init: %v", err)
	}

	tests := []struct {
		name        string
		path        string
		method      string
		body        string
		wantContent string
	}{
		{
			name:        "completions",
			path:        "/v1/completions",
			method:      "POST",
			body:        `{"model":"gpt-5.4","prompt":"Hello","max_tokens":10}`,
			wantContent: "completed text",
		},
		{
			name:        "embeddings",
			path:        "/v1/embeddings",
			method:      "POST",
			body:        `{"model":"text-embedding-3-small","input":"test"}`,
			wantContent: "embedding",
		},
		{
			name:        "models",
			path:        "/v1/models",
			method:      "GET",
			body:        "",
			wantContent: "gpt-5.4",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var req *http.Request
			if tc.body != "" {
				req = httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			} else {
				req = httptest.NewRequest(tc.method, tc.path, nil)
			}
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tc.wantContent) {
				t.Errorf("body does not contain %q:\n%s", tc.wantContent, rec.Body.String())
			}
		})
	}
}

func TestCrossProtocolNonChatRejected(t *testing.T) {
	mock := mockOpenAIFullBackend()
	defer mock.Close()

	cfg := &config.Config{
		Server: config.ServerConfig{Listen: ":0"},
		Routing: config.RoutingConfig{
			Routes: []config.RouteConfig{
				{
					Name:            "completions-to-anthropic",
					Priority:        100,
					InboundProtocol: "openai",
					PathPrefix:      "/v1/completions",
					EndpointKinds:   []string{"completion"},
					Backend:         "anthropic-backend",
				},
			},
		},
		Backends: []config.BackendConfig{
			{
				Name:     "anthropic-backend",
				Protocol: "anthropic",
				BaseURL:  "http://unused",
				Auth:     config.AuthConfig{Mode: config.AuthModeInject},
			},
		},
	}

	handler, err := NewHandler(cfg, slog.Default())
	if err != nil {
		t.Fatalf("init: %v", err)
	}

	req := httptest.NewRequest("POST", "/v1/completions",
		strings.NewReader(`{"model":"gpt-5.4","prompt":"test"}`))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code == http.StatusOK {
		t.Error("cross-protocol non-chat should not succeed")
	}
}

func TestRouteNotFound(t *testing.T) {
	cfg := buildConfig("http://unused", "http://unused")
	handler, err := NewHandler(cfg, slog.Default())
	if err != nil {
		t.Fatalf("init: %v", err)
	}

	req := httptest.NewRequest("POST", "/v2/unknown", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code == http.StatusOK {
		t.Error("unmatched route should not return 200")
	}
}

func TestErrorHooksRunOnFailure(t *testing.T) {
	openaiMock := mockOpenAIBackend()
	defer openaiMock.Close()

	cfg := buildConfig(openaiMock.URL, openaiMock.URL)
	handler, err := NewHandler(cfg, slog.Default())
	if err != nil {
		t.Fatalf("init: %v", err)
	}

	errorHookRan := false
	handler.HookManager().AddError(&testErrorHook{
		name: "test-error-hook",
		fn: func(pErr *pipeline.PipelineError) error {
			errorHookRan = true
			pErr.Headers = make(http.Header)
			pErr.Headers.Set("X-Error-Hook", "ran")
			pErr.Extensions = map[string]any{"code": "CUSTOM_ERROR"}
			return nil
		},
	})

	handler.HookManager().AddPreRequest(&testPreRequestHook{
		name: "force-error",
		fn: func(req *pipeline.NormalizedRequest) error {
			return &pipeline.PipelineError{
				StatusCode: 422,
				Message:    "forced error",
			}
		},
	})

	req := httptest.NewRequest("POST", "/oai-oai/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-5.4","messages":[{"role":"user","content":"test"}]}`))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if !errorHookRan {
		t.Error("error hook should have run")
	}
	if rec.Code != 422 {
		t.Errorf("status = %d, want 422", rec.Code)
	}
	if rec.Header().Get("X-Error-Hook") != "ran" {
		t.Errorf("PipelineError.Headers not propagated to response, X-Error-Hook = %q", rec.Header().Get("X-Error-Hook"))
	}

	var body map[string]any
	_ = json.NewDecoder(rec.Body).Decode(&body)
	errObj, _ := body["error"].(map[string]any)
	if errObj["code"] != "CUSTOM_ERROR" {
		t.Errorf("PipelineError.Extensions not in response body: %v", errObj)
	}
}

func TestPassthroughRunsPreRequestHooks(t *testing.T) {
	mock := mockOpenAIFullBackend()
	defer mock.Close()

	cfg := buildPassthroughConfig(mock.URL)
	handler, err := NewHandler(cfg, slog.Default())
	if err != nil {
		t.Fatalf("init: %v", err)
	}

	hookRan := false
	handler.HookManager().AddPreRequest(&testPreRequestHook{
		name: "passthrough-hook",
		fn: func(req *pipeline.NormalizedRequest) error {
			hookRan = true
			if req.EndpointKind != pipeline.EndpointCompletion {
				t.Errorf("endpoint kind = %q, want completion", req.EndpointKind)
			}
			return nil
		},
	})

	req := httptest.NewRequest("POST", "/v1/completions",
		strings.NewReader(`{"model":"gpt-5.4","prompt":"Hello"}`))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if !hookRan {
		t.Error("pre-request hook should run for passthrough endpoints")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

func TestPassthroughHookCanReject(t *testing.T) {
	mock := mockOpenAIFullBackend()
	defer mock.Close()

	cfg := buildPassthroughConfig(mock.URL)
	handler, err := NewHandler(cfg, slog.Default())
	if err != nil {
		t.Fatalf("init: %v", err)
	}

	handler.HookManager().AddPreRequest(&testPreRequestHook{
		name: "reject-hook",
		fn: func(req *pipeline.NormalizedRequest) error {
			return &pipeline.PipelineError{
				StatusCode: 403,
				Message:    "rejected by hook",
			}
		},
	})

	req := httptest.NewRequest("POST", "/v1/completions",
		strings.NewReader(`{"model":"gpt-5.4","prompt":"Hello"}`))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != 403 {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

func TestHeaderModifierPropagatesUpstream(t *testing.T) {
	var receivedHeader string
	upstreamMock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHeader = r.Header.Get("X-Custom-Upstream")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "chatcmpl-mock", "object": "chat.completion", "created": 1700000000,
			"model": "gpt-5.4",
			"choices": []map[string]any{
				{"index": 0, "message": map[string]string{"role": "assistant", "content": "ok"}, "finish_reason": "stop"},
			},
			"usage": map[string]int{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2},
		})
	}))
	defer upstreamMock.Close()

	cfg := buildConfig(upstreamMock.URL, upstreamMock.URL)
	handler, err := NewHandler(cfg, slog.Default())
	if err != nil {
		t.Fatalf("init: %v", err)
	}

	handler.HookManager().AddPreRequest(&testPreRequestHook{
		name: "add-header",
		fn: func(req *pipeline.NormalizedRequest) error {
			if req.Headers == nil {
				req.Headers = make(http.Header)
			}
			req.Headers.Set("X-Custom-Upstream", "hook-value")
			return nil
		},
	})

	req := httptest.NewRequest("POST", "/oai-oai/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-5.4","messages":[{"role":"user","content":"test"}]}`))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if receivedHeader != "hook-value" {
		t.Errorf("upstream did not receive X-Custom-Upstream, got %q", receivedHeader)
	}
}

func TestResponseHeaderModifierPropagatesDownstream(t *testing.T) {
	openaiMock := mockOpenAIBackend()
	defer openaiMock.Close()

	cfg := buildConfig(openaiMock.URL, openaiMock.URL)
	handler, err := NewHandler(cfg, slog.Default())
	if err != nil {
		t.Fatalf("init: %v", err)
	}

	handler.HookManager().AddPostResponse(&testPostResponseHook{
		name: "add-resp-header",
		fn: func(resp *pipeline.NormalizedResponse) error {
			if resp.Headers == nil {
				resp.Headers = make(http.Header)
			}
			resp.Headers.Set("X-Downstream-Custom", "from-hook")
			return nil
		},
	})

	req := httptest.NewRequest("POST", "/oai-oai/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-5.4","messages":[{"role":"user","content":"test"}]}`))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if rec.Header().Get("X-Downstream-Custom") != "from-hook" {
		t.Errorf("downstream did not receive X-Downstream-Custom, got %q", rec.Header().Get("X-Downstream-Custom"))
	}
}

func TestCompletionsTextModifierHookWorks(t *testing.T) {
	var receivedPrompt string
	upstreamMock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		receivedPrompt, _ = req["prompt"].(string)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "cmpl-mock", "object": "text_completion", "model": "gpt-5.4",
			"choices": []map[string]any{{"index": 0, "text": "response with SECRET", "finish_reason": "stop"}},
			"usage":   map[string]int{"prompt_tokens": 5, "completion_tokens": 5, "total_tokens": 10},
		})
	}))
	defer upstreamMock.Close()

	cfg := buildPassthroughConfig(upstreamMock.URL)
	handler, err := NewHandler(cfg, slog.Default())
	if err != nil {
		t.Fatalf("init: %v", err)
	}

	handler.HookManager().AddPreRequest(&testPreRequestHook{
		name: "modify-prompt",
		fn: func(req *pipeline.NormalizedRequest) error {
			if len(req.Messages) > 0 {
				req.Messages[0].Content = "MODIFIED prompt"
			}
			return nil
		},
	})
	handler.HookManager().AddPostResponse(&testPostResponseHook{
		name: "modify-response",
		fn: func(resp *pipeline.NormalizedResponse) error {
			resp.Content = strings.ReplaceAll(resp.Content, "SECRET", "[REDACTED]")
			return nil
		},
	})

	req := httptest.NewRequest("POST", "/v1/completions",
		strings.NewReader(`{"model":"gpt-5.4","prompt":"original prompt","max_tokens":10}`))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	if receivedPrompt != "MODIFIED prompt" {
		t.Errorf("upstream received prompt = %q, want %q", receivedPrompt, "MODIFIED prompt")
	}

	body := rec.Body.String()
	if !strings.Contains(body, "[REDACTED]") {
		t.Errorf("response text modifier should have replaced SECRET, got:\n%s", body)
	}
	if strings.Contains(body, "SECRET") {
		t.Error("SECRET should have been redacted from response")
	}
}

func TestCompletionsLoggingHookGetsModel(t *testing.T) {
	mock := mockOpenAIFullBackend()
	defer mock.Close()

	cfg := buildPassthroughConfig(mock.URL)
	handler, err := NewHandler(cfg, slog.Default())
	if err != nil {
		t.Fatalf("init: %v", err)
	}

	var seenModel string
	var seenKind pipeline.EndpointKind
	handler.HookManager().AddPreRequest(&testPreRequestHook{
		name: "inspect",
		fn: func(req *pipeline.NormalizedRequest) error {
			seenModel = req.Model
			seenKind = req.EndpointKind
			return nil
		},
	})

	req := httptest.NewRequest("POST", "/v1/completions",
		strings.NewReader(`{"model":"gpt-5.4","prompt":"test"}`))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if seenModel != "gpt-5.4" {
		t.Errorf("model = %q, want %q", seenModel, "gpt-5.4")
	}
	if seenKind != pipeline.EndpointCompletion {
		t.Errorf("kind = %q, want completion", seenKind)
	}
}

func TestEmbeddingsPassthroughLoggingGetsModel(t *testing.T) {
	mock := mockOpenAIFullBackend()
	defer mock.Close()

	cfg := buildPassthroughConfig(mock.URL)
	handler, err := NewHandler(cfg, slog.Default())
	if err != nil {
		t.Fatalf("init: %v", err)
	}

	var seenModel string
	handler.HookManager().AddPreRequest(&testPreRequestHook{
		name: "inspect-model",
		fn: func(req *pipeline.NormalizedRequest) error {
			seenModel = req.Model
			return nil
		},
	})

	req := httptest.NewRequest("POST", "/v1/embeddings",
		strings.NewReader(`{"model":"text-embedding-3-small","input":"test"}`))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if seenModel != "text-embedding-3-small" {
		t.Errorf("model = %q, want %q", seenModel, "text-embedding-3-small")
	}
}

func TestPassthroughUpstreamErrorRunsErrorHooks(t *testing.T) {
	failingMock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = fmt.Fprint(w, `{"error":{"message":"rate limited","type":"rate_limit_error"}}`)
	}))
	defer failingMock.Close()

	cfg := buildPassthroughConfig(failingMock.URL)
	handler, err := NewHandler(cfg, slog.Default())
	if err != nil {
		t.Fatalf("init: %v", err)
	}

	errorHookRan := false
	var seenStatus int
	handler.HookManager().AddError(&testErrorHook{
		name: "catch-upstream-error",
		fn: func(pErr *pipeline.PipelineError) error {
			errorHookRan = true
			seenStatus = pErr.StatusCode
			return nil
		},
	})

	req := httptest.NewRequest("POST", "/v1/embeddings",
		strings.NewReader(`{"model":"text-embedding-3-small","input":"test"}`))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if !errorHookRan {
		t.Error("error hook should run on upstream 429")
	}
	if seenStatus != http.StatusTooManyRequests {
		t.Errorf("error hook saw status %d, want 429", seenStatus)
	}
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("downstream status = %d, want 429", rec.Code)
	}
}

func TestPassthroughUpstreamErrorStripsBodyHeaders(t *testing.T) {
	failingMock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Content-Length", "999")
		w.Header().Set("Transfer-Encoding", "chunked")
		w.Header().Set("X-Ratelimit-Remaining", "0")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = fmt.Fprint(w, `rate limited`)
	}))
	defer failingMock.Close()

	cfg := buildPassthroughConfig(failingMock.URL)
	handler, err := NewHandler(cfg, slog.Default())
	if err != nil {
		t.Fatalf("init: %v", err)
	}

	req := httptest.NewRequest("POST", "/v1/embeddings",
		strings.NewReader(`{"model":"text-embedding-3-small","input":"test"}`))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", rec.Code)
	}
	if rec.Header().Get("Content-Encoding") != "" {
		t.Error("Content-Encoding from upstream error should be stripped")
	}
	if rec.Header().Get("Transfer-Encoding") != "" {
		t.Error("Transfer-Encoding from upstream error should be stripped")
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type should be application/json (rewritten body), got %q", ct)
	}
	if rec.Header().Get("X-Ratelimit-Remaining") != "0" {
		t.Error("non-body upstream headers should be preserved")
	}
}

func TestPassthroughUpstream500RunsErrorHooks(t *testing.T) {
	failingMock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = fmt.Fprint(w, `internal server error`)
	}))
	defer failingMock.Close()

	cfg := buildPassthroughConfig(failingMock.URL)
	handler, err := NewHandler(cfg, slog.Default())
	if err != nil {
		t.Fatalf("init: %v", err)
	}

	errorHookRan := false
	handler.HookManager().AddError(&testErrorHook{
		name: "catch-500",
		fn: func(pErr *pipeline.PipelineError) error {
			errorHookRan = true
			return nil
		},
	})

	req := httptest.NewRequest("GET", "/v1/models", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if !errorHookRan {
		t.Error("error hook should run on upstream 500")
	}
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}

func TestCompletionsMultiChoiceResponseTextModifier(t *testing.T) {
	upstreamMock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "cmpl-mock", "object": "text_completion", "model": "gpt-5.4",
			"choices": []map[string]any{
				{"index": 0, "text": "first SECRET answer", "finish_reason": "stop"},
				{"index": 1, "text": "second SECRET answer", "finish_reason": "stop"},
			},
			"usage": map[string]int{"prompt_tokens": 5, "completion_tokens": 10, "total_tokens": 15},
		})
	}))
	defer upstreamMock.Close()

	cfg := buildPassthroughConfig(upstreamMock.URL)
	handler, err := NewHandler(cfg, slog.Default())
	if err != nil {
		t.Fatalf("init: %v", err)
	}

	handler.HookManager().AddPostResponse(&testPostResponseHook{
		name: "redact-all-choices",
		fn: func(resp *pipeline.NormalizedResponse) error {
			resp.Content = strings.ReplaceAll(resp.Content, "SECRET", "[REDACTED]")
			for i := range resp.Choices {
				resp.Choices[i].Text = strings.ReplaceAll(resp.Choices[i].Text, "SECRET", "[REDACTED]")
			}
			return nil
		},
	})

	req := httptest.NewRequest("POST", "/v1/completions",
		strings.NewReader(`{"model":"gpt-5.4","prompt":"test"}`))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	body := rec.Body.String()
	if strings.Contains(body, "SECRET") {
		t.Errorf("SECRET should be redacted from all choices:\n%s", body)
	}
	if !strings.Contains(body, "[REDACTED]") {
		t.Error("response should contain [REDACTED]")
	}

	var raw map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &raw)
	choices, _ := raw["choices"].([]any)
	if len(choices) != 2 {
		t.Fatalf("choices count = %d, want 2 (multi-choice preserved)", len(choices))
	}
}

func TestCompletionsHookEditsChoicesOnly(t *testing.T) {
	upstreamMock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "cmpl-mock", "object": "text_completion", "model": "gpt-5.4",
			"choices": []map[string]any{
				{"index": 0, "text": "original-0", "finish_reason": "stop"},
				{"index": 1, "text": "original-1", "finish_reason": "stop"},
			},
		})
	}))
	defer upstreamMock.Close()

	cfg := buildPassthroughConfig(upstreamMock.URL)
	handler, err := NewHandler(cfg, slog.Default())
	if err != nil {
		t.Fatalf("init: %v", err)
	}

	handler.HookManager().AddPostResponse(&testPostResponseHook{
		name: "edit-choices-only",
		fn: func(resp *pipeline.NormalizedResponse) error {
			for i := range resp.Choices {
				resp.Choices[i].Text = "modified-" + fmt.Sprintf("%d", i)
			}
			// Deliberately does NOT touch resp.Content
			return nil
		},
	})

	req := httptest.NewRequest("POST", "/v1/completions",
		strings.NewReader(`{"model":"gpt-5.4","prompt":"test"}`))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var raw CompletionResponseShape
	_ = json.Unmarshal(rec.Body.Bytes(), &raw)

	if len(raw.Choices) != 2 {
		t.Fatalf("choices = %d, want 2", len(raw.Choices))
	}
	if raw.Choices[0].Text != "modified-0" {
		t.Errorf("choices[0].text = %q, want modified-0", raw.Choices[0].Text)
	}
	if raw.Choices[1].Text != "modified-1" {
		t.Errorf("choices[1].text = %q, want modified-1", raw.Choices[1].Text)
	}
}

type CompletionResponseShape struct {
	Choices []struct {
		Text string `json:"text"`
	} `json:"choices"`
}

func TestCompletionsArrayPromptRoundTrip(t *testing.T) {
	var receivedPrompt json.RawMessage
	upstreamMock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]json.RawMessage
		_ = json.NewDecoder(r.Body).Decode(&req)
		receivedPrompt = req["prompt"]

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "cmpl-mock", "object": "text_completion", "model": "gpt-5.4",
			"choices": []map[string]any{
				{"index": 0, "text": "done", "finish_reason": "stop"},
			},
		})
	}))
	defer upstreamMock.Close()

	cfg := buildPassthroughConfig(upstreamMock.URL)
	handler, err := NewHandler(cfg, slog.Default())
	if err != nil {
		t.Fatalf("init: %v", err)
	}

	req := httptest.NewRequest("POST", "/v1/completions",
		strings.NewReader(`{"model":"gpt-5.4","prompt":["Hello","World"]}`))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var arr []string
	if err := json.Unmarshal(receivedPrompt, &arr); err != nil {
		t.Fatalf("upstream prompt should be a string array: %v (raw: %s)", err, receivedPrompt)
	}
	if len(arr) != 2 || arr[0] != "Hello" || arr[1] != "World" {
		t.Errorf("upstream prompt = %v, want [Hello World]", arr)
	}
}

func TestCompletionsTokenPromptRoundTrip(t *testing.T) {
	var receivedPrompt json.RawMessage
	upstreamMock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]json.RawMessage
		_ = json.NewDecoder(r.Body).Decode(&req)
		receivedPrompt = req["prompt"]

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "cmpl-mock", "object": "text_completion", "model": "gpt-5.4",
			"choices": []map[string]any{
				{"index": 0, "text": "done", "finish_reason": "stop"},
			},
		})
	}))
	defer upstreamMock.Close()

	cfg := buildPassthroughConfig(upstreamMock.URL)
	handler, err := NewHandler(cfg, slog.Default())
	if err != nil {
		t.Fatalf("init: %v", err)
	}

	req := httptest.NewRequest("POST", "/v1/completions",
		strings.NewReader(`{"model":"gpt-5.4","prompt":[1234,5678]}`))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var tokens []int
	if err := json.Unmarshal(receivedPrompt, &tokens); err != nil {
		t.Fatalf("upstream prompt should be a token array: %v (raw: %s)", err, receivedPrompt)
	}
	if len(tokens) != 2 || tokens[0] != 1234 {
		t.Errorf("upstream tokens = %v, want [1234 5678]", tokens)
	}
}

func TestPassthroughHeaderPropagation(t *testing.T) {
	var receivedHeader string
	upstreamMock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHeader = r.Header.Get("X-Hook-Added")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "cmpl-mock", "object": "text_completion",
			"choices": []map[string]any{{"text": "done", "finish_reason": "stop"}},
		})
	}))
	defer upstreamMock.Close()

	cfg := buildPassthroughConfig(upstreamMock.URL)
	handler, err := NewHandler(cfg, slog.Default())
	if err != nil {
		t.Fatalf("init: %v", err)
	}

	handler.HookManager().AddPreRequest(&testPreRequestHook{
		name: "add-header-passthrough",
		fn: func(req *pipeline.NormalizedRequest) error {
			req.Headers.Set("X-Hook-Added", "from-hook")
			return nil
		},
	})

	req := httptest.NewRequest("POST", "/v1/completions",
		strings.NewReader(`{"model":"gpt-5.4","prompt":"test"}`))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if receivedHeader != "from-hook" {
		t.Errorf("upstream did not receive hook-added header, got %q", receivedHeader)
	}
}

func TestPassthroughRequestIDPropagation(t *testing.T) {
	var receivedRequestID string
	upstreamMock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedRequestID = r.Header.Get("X-Request-Id")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "cmpl-mock", "object": "text_completion",
			"choices": []map[string]any{{"text": "done", "finish_reason": "stop"}},
		})
	}))
	defer upstreamMock.Close()

	cfg := buildPassthroughConfig(upstreamMock.URL)
	handler, err := NewHandler(cfg, slog.Default())
	if err != nil {
		t.Fatalf("init: %v", err)
	}

	req := httptest.NewRequest("POST", "/v1/completions",
		strings.NewReader(`{"model":"gpt-5.4","prompt":"test"}`))
	req.Header.Set("X-Request-Id", "trace-999")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if receivedRequestID != "trace-999" {
		t.Errorf("upstream did not receive X-Request-Id, got %q", receivedRequestID)
	}
}

func TestPassthroughPostResponseHooks(t *testing.T) {
	mock := mockOpenAIFullBackend()
	defer mock.Close()

	cfg := buildPassthroughConfig(mock.URL)
	handler, err := NewHandler(cfg, slog.Default())
	if err != nil {
		t.Fatalf("init: %v", err)
	}

	postHookRan := false
	handler.HookManager().AddPostResponse(&testPostResponseHook{
		name: "passthrough-post",
		fn: func(resp *pipeline.NormalizedResponse) error {
			postHookRan = true
			if resp.Headers == nil {
				resp.Headers = make(http.Header)
			}
			resp.Headers.Set("X-Post-Hook", "applied")
			return nil
		},
	})

	req := httptest.NewRequest("POST", "/v1/completions",
		strings.NewReader(`{"model":"gpt-5.4","prompt":"test"}`))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if !postHookRan {
		t.Error("post-response hook should run for passthrough endpoints")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if rec.Header().Get("X-Post-Hook") != "applied" {
		t.Errorf("response header from post-response hook not applied, got %q", rec.Header().Get("X-Post-Hook"))
	}
}

func TestPassthroughResponseHeaderRemoval(t *testing.T) {
	upstreamMock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Upstream-Secret", "should-be-removed")
		w.Header().Set("X-Upstream-Keep", "should-stay")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"data":   []map[string]any{{"id": "gpt-5.4", "object": "model"}},
		})
	}))
	defer upstreamMock.Close()

	cfg := buildPassthroughConfig(upstreamMock.URL)
	handler, err := NewHandler(cfg, slog.Default())
	if err != nil {
		t.Fatalf("init: %v", err)
	}

	handler.HookManager().AddPostResponse(&testPostResponseHook{
		name: "remove-header",
		fn: func(resp *pipeline.NormalizedResponse) error {
			resp.Headers.Del("X-Upstream-Secret")
			return nil
		},
	})

	req := httptest.NewRequest("GET", "/v1/models", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if rec.Header().Get("X-Upstream-Secret") != "" {
		t.Error("X-Upstream-Secret should have been removed by the hook")
	}
	if rec.Header().Get("X-Upstream-Keep") != "should-stay" {
		t.Errorf("X-Upstream-Keep = %q, want should-stay", rec.Header().Get("X-Upstream-Keep"))
	}
}

func TestStreamErrorHooksRun(t *testing.T) {
	brokenStreamMock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		// Send a valid start chunk then abruptly send malformed data
		_, _ = fmt.Fprint(w, `data: {"id":"chatcmpl-err","object":"chat.completion.chunk","created":1700000000,"model":"gpt-5.4","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}

data: THIS_IS_NOT_JSON

`)
		flusher.Flush()
	}))
	defer brokenStreamMock.Close()

	cfg := buildConfig(brokenStreamMock.URL, brokenStreamMock.URL)
	handler, err := NewHandler(cfg, slog.Default())
	if err != nil {
		t.Fatalf("init: %v", err)
	}

	errorHookRan := false
	handler.HookManager().AddError(&testErrorHook{
		name: "stream-error-hook",
		fn: func(pErr *pipeline.PipelineError) error {
			errorHookRan = true
			return nil
		},
	})

	req := httptest.NewRequest("POST", "/oai-oai/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-5.4","messages":[{"role":"user","content":"test"}],"stream":true}`))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if !errorHookRan {
		t.Error("error hook should run on mid-stream decode failure")
	}

	body := rec.Body.String()
	if !strings.Contains(body, `"server_error"`) {
		t.Errorf("stream output should contain an error event, got:\n%s", body)
	}
	if !strings.Contains(body, "[DONE]") {
		t.Error("stream should terminate with [DONE] after error")
	}
}

func TestStreamHookErrorRunsErrorHook(t *testing.T) {
	openaiMock := mockOpenAIBackend()
	defer openaiMock.Close()

	cfg := buildConfig(openaiMock.URL, openaiMock.URL)
	handler, err := NewHandler(cfg, slog.Default())
	if err != nil {
		t.Fatalf("init: %v", err)
	}

	errorHookRan := false
	handler.HookManager().AddStreamEvent(&testStreamEventHook{
		name: "fail-on-delta",
		fn: func(event *pipeline.StreamEvent) error {
			if event.Type == pipeline.StreamEventTextDelta {
				return &pipeline.PipelineError{
					StatusCode: 500,
					Message:    "stream hook failure",
				}
			}
			return nil
		},
	})
	handler.HookManager().AddError(&testErrorHook{
		name: "catch-stream-hook-error",
		fn: func(pErr *pipeline.PipelineError) error {
			errorHookRan = true
			if pErr.Message != "stream hook failure" {
				t.Errorf("error message = %q", pErr.Message)
			}
			return nil
		},
	})

	req := httptest.NewRequest("POST", "/oai-oai/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-5.4","messages":[{"role":"user","content":"test"}],"stream":true}`))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if !errorHookRan {
		t.Error("error hook should run when a stream hook fails")
	}
}

func TestToolsForwardedToBackend(t *testing.T) {
	var receivedBody map[string]json.RawMessage
	upstreamMock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&receivedBody)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "chatcmpl-mock", "object": "chat.completion", "created": 1700000000,
			"model": "gpt-5.4",
			"choices": []map[string]any{
				{
					"index": 0,
					"message": map[string]any{
						"role":    "assistant",
						"content": nil,
						"tool_calls": []map[string]any{
							{
								"id":       "call_abc",
								"type":     "function",
								"function": map[string]string{"name": "get_weather", "arguments": `{"city":"SF"}`},
							},
						},
					},
					"finish_reason": "tool_calls",
				},
			},
			"usage": map[string]int{"prompt_tokens": 10, "completion_tokens": 20, "total_tokens": 30},
		})
	}))
	defer upstreamMock.Close()

	cases := []struct {
		name          string
		requestPath   string
		requestBody   string
		backendProto  string
		checkToolsKey string // "tools" for both, but field name differs
	}{
		{
			name:        "OpenAI-to-OpenAI tools forwarded",
			requestPath: "/oai-oai/v1/chat/completions",
			requestBody: `{
				"model": "gpt-5.4",
				"messages": [{"role": "user", "content": "What's the weather?"}],
				"tools": [{
					"type": "function",
					"function": {
						"name": "get_weather",
						"description": "Get weather",
						"parameters": {"type": "object", "properties": {"city": {"type": "string"}}}
					}
				}],
				"tool_choice": "auto"
			}`,
			backendProto:  "openai",
			checkToolsKey: "tools",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			receivedBody = nil

			cfg := &config.Config{
				Server: config.ServerConfig{Listen: ":0"},
				Routing: config.RoutingConfig{
					Routes: []config.RouteConfig{
						{
							Name:            "test-route",
							Priority:        100,
							InboundProtocol: "openai",
							PathPrefix:      "/oai-oai/v1/chat/completions",
							EndpointKinds:   []string{"chat"},
							Backend:         "test-backend",
						},
					},
				},
				Backends: []config.BackendConfig{
					{
						Name:     "test-backend",
						Protocol: tc.backendProto,
						BaseURL:  upstreamMock.URL,
						Auth:     config.AuthConfig{Mode: config.AuthModeInject, Header: "Authorization", Value: "Bearer test"},
					},
				},
			}

			handler, err := NewHandler(cfg, slog.Default())
			if err != nil {
				t.Fatalf("init: %v", err)
			}

			req := httptest.NewRequest("POST", tc.requestPath, strings.NewReader(tc.requestBody))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}

			// Verify tools were forwarded to backend
			if receivedBody == nil {
				t.Fatal("backend received no request body")
			}
			toolsRaw, ok := receivedBody[tc.checkToolsKey]
			if !ok {
				t.Fatalf("backend request missing %q field. Keys: %v", tc.checkToolsKey, mapKeys(receivedBody))
			}
			var tools []json.RawMessage
			if err := json.Unmarshal(toolsRaw, &tools); err != nil {
				t.Fatalf("could not parse tools: %v", err)
			}
			if len(tools) == 0 {
				t.Error("tools array is empty")
			}

			// Verify tool_choice was forwarded
			if _, ok := receivedBody["tool_choice"]; !ok {
				t.Error("backend request missing tool_choice")
			}

			// Verify response has tool_calls
			var respBody map[string]json.RawMessage
			_ = json.Unmarshal(rec.Body.Bytes(), &respBody)
			var respObj struct {
				Choices []struct {
					FinishReason string `json:"finish_reason"`
					Message      struct {
						ToolCalls []json.RawMessage `json:"tool_calls"`
					} `json:"message"`
				} `json:"choices"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &respObj); err != nil {
				t.Fatalf("unmarshal response: %v", err)
			}
			if len(respObj.Choices) == 0 {
				t.Fatal("no choices in response")
			}
			if respObj.Choices[0].FinishReason != "tool_calls" {
				t.Errorf("finish_reason = %q, want tool_calls", respObj.Choices[0].FinishReason)
			}
			if len(respObj.Choices[0].Message.ToolCalls) == 0 {
				t.Error("response has no tool_calls")
			}
		})
	}
}

func TestToolsCrossProtocolOpenAIToAnthropic(t *testing.T) {
	var receivedBody map[string]json.RawMessage
	anthropicMock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&receivedBody)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":    "msg_mock",
			"type":  "message",
			"role":  "assistant",
			"model": "claude-sonnet-4-6",
			"content": []map[string]any{
				{"type": "text", "text": "Checking weather."},
				{"type": "tool_use", "id": "toolu_abc", "name": "get_weather", "input": map[string]string{"city": "SF"}},
			},
			"stop_reason": "tool_use",
			"usage":       map[string]int{"input_tokens": 20, "output_tokens": 50},
		})
	}))
	defer anthropicMock.Close()

	cfg := &config.Config{
		Server: config.ServerConfig{Listen: ":0"},
		Routing: config.RoutingConfig{
			Routes: []config.RouteConfig{
				{
					Name:            "oai-to-ant",
					Priority:        100,
					InboundProtocol: "openai",
					PathPrefix:      "/v1/chat/completions",
					EndpointKinds:   []string{"chat"},
					Backend:         "anthropic-backend",
				},
			},
		},
		Backends: []config.BackendConfig{
			{
				Name:     "anthropic-backend",
				Protocol: "anthropic",
				BaseURL:  anthropicMock.URL,
				Auth:     config.AuthConfig{Mode: config.AuthModeInject, Header: "X-Api-Key", Value: "test-key"},
				DefaultHeaders: map[string]string{
					"anthropic-version": "2023-06-01",
				},
			},
		},
	}

	handler, err := NewHandler(cfg, slog.Default())
	if err != nil {
		t.Fatalf("init: %v", err)
	}

	// Send OpenAI request with tools
	reqBody := `{
		"model": "claude-sonnet-4-6",
		"messages": [{"role": "user", "content": "What's the weather?"}],
		"tools": [{
			"type": "function",
			"function": {
				"name": "get_weather",
				"description": "Get weather",
				"parameters": {"type": "object", "properties": {"city": {"type": "string"}}}
			}
		}],
		"tool_choice": "auto"
	}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	// Verify Anthropic backend received tools (translated format)
	if receivedBody == nil {
		t.Fatal("backend received no body")
	}
	toolsRaw, ok := receivedBody["tools"]
	if !ok {
		t.Fatalf("Anthropic backend missing 'tools'. Keys: %v", mapKeys(receivedBody))
	}
	var tools []struct {
		Name        string `json:"name"`
		InputSchema any    `json:"input_schema"`
	}
	if err := json.Unmarshal(toolsRaw, &tools); err != nil {
		t.Fatalf("parse tools: %v (raw: %s)", err, toolsRaw)
	}
	if len(tools) != 1 || tools[0].Name != "get_weather" {
		t.Errorf("tools = %+v", tools)
	}
	if tools[0].InputSchema == nil {
		t.Error("input_schema should be set (translated from parameters)")
	}

	// Verify tool_choice was translated
	if _, ok := receivedBody["tool_choice"]; !ok {
		t.Error("Anthropic backend missing 'tool_choice'")
	}

	// Verify OpenAI response has tool_calls (translated from Anthropic tool_use)
	var respObj struct {
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct {
				Content   *string `json:"content"`
				ToolCalls []struct {
					ID       string `json:"id"`
					Type     string `json:"type"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &respObj); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(respObj.Choices) == 0 {
		t.Fatal("no choices")
	}
	c := respObj.Choices[0]
	if c.FinishReason != "tool_calls" {
		t.Errorf("finish_reason = %q, want tool_calls", c.FinishReason)
	}
	if len(c.Message.ToolCalls) != 1 {
		t.Fatalf("tool_calls = %d, want 1", len(c.Message.ToolCalls))
	}
	tc := c.Message.ToolCalls[0]
	if tc.ID != "toolu_abc" || tc.Function.Name != "get_weather" {
		t.Errorf("tool_call = %+v", tc)
	}
	if tc.Type != "function" {
		t.Errorf("tool_call type = %q, want function", tc.Type)
	}
}

func mapKeys(m map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func TestStreamDoStreamFailureReturnsHTTPError(t *testing.T) {
	failingMock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = fmt.Fprint(w, `{"error":{"message":"backend overloaded","type":"overloaded_error"}}`)
	}))
	defer failingMock.Close()

	cfg := buildConfig(failingMock.URL, failingMock.URL)
	handler, err := NewHandler(cfg, slog.Default())
	if err != nil {
		t.Fatalf("init: %v", err)
	}

	errorHookRan := false
	handler.HookManager().AddError(&testErrorHook{
		name: "catch-dostream-failure",
		fn: func(pErr *pipeline.PipelineError) error {
			errorHookRan = true
			return nil
		},
	})

	req := httptest.NewRequest("POST", "/oai-oai/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-5.4","messages":[{"role":"user","content":"test"}],"stream":true}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code == http.StatusOK {
		t.Errorf("DoStream failure should not produce 200, got %d", rec.Code)
	}
	ct := rec.Header().Get("Content-Type")
	if ct == "text/event-stream" {
		t.Error("DoStream failure should not produce text/event-stream response")
	}
	if !errorHookRan {
		t.Error("error hook should run on DoStream failure")
	}
	body := rec.Body.String()
	if !strings.Contains(body, "backend overloaded") {
		t.Errorf("response should contain upstream error message, got:\n%s", body)
	}
}

func TestNewHandlerWithCustomClient(t *testing.T) {
	openaiMock := mockOpenAIBackend()
	defer openaiMock.Close()

	transportUsed := false
	customTransport := &roundTripRecorder{
		wrapped: http.DefaultTransport,
		onTrip: func() {
			transportUsed = true
		},
	}
	customClient := &http.Client{Transport: customTransport}

	cfg := buildConfig(openaiMock.URL, openaiMock.URL)
	handler, err := NewHandlerWithOptions(cfg, slog.Default(), HandlerOptions{
		HTTPClient: customClient,
	})
	if err != nil {
		t.Fatalf("init: %v", err)
	}

	req := httptest.NewRequest("POST", "/oai-oai/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-5.4","messages":[{"role":"user","content":"test"}]}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !transportUsed {
		t.Error("custom HTTP client transport was not used by the backend")
	}
}

type roundTripRecorder struct {
	wrapped http.RoundTripper
	onTrip  func()
}

func (r *roundTripRecorder) RoundTrip(req *http.Request) (*http.Response, error) {
	r.onTrip()
	return r.wrapped.RoundTrip(req)
}

type testPreRequestHook struct {
	name string
	fn   func(*pipeline.NormalizedRequest) error
}

func (h *testPreRequestHook) Name() string { return h.name }
func (h *testPreRequestHook) Execute(_ context.Context, req *pipeline.NormalizedRequest) error {
	return h.fn(req)
}

func TestRequestBodySizeLimit(t *testing.T) {
	openaiMock := mockOpenAIBackend()
	defer openaiMock.Close()
	anthropicMock := mockAnthropicBackend()
	defer anthropicMock.Close()

	cfg := buildConfig(openaiMock.URL, anthropicMock.URL)
	handler, err := NewHandler(cfg, slog.Default())
	if err != nil {
		t.Fatalf("init: %v", err)
	}

	atLimit := bytes.Repeat([]byte("x"), maxRequestBodyBytes)
	req := httptest.NewRequest("POST", "/oai-oai/v1/chat/completions", bytes.NewReader(atLimit))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code == http.StatusRequestEntityTooLarge {
		t.Error("body at limit should not return 413")
	}

	overLimit := bytes.Repeat([]byte("x"), maxRequestBodyBytes+1)
	req = httptest.NewRequest("POST", "/oai-oai/v1/chat/completions", bytes.NewReader(overLimit))
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("body over limit should return 413, got %d", rec.Code)
	}
}

func TestPassthroughResponseTooLarge(t *testing.T) {
	upstreamMock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		chunk := make([]byte, 4096)
		for remaining := maxResponseBodyBytes + 1; remaining > 0; {
			n := remaining
			if n > len(chunk) {
				n = len(chunk)
			}
			_, _ = w.Write(chunk[:n])
			remaining -= n
		}
	}))
	defer upstreamMock.Close()

	cfg := buildPassthroughConfig(upstreamMock.URL)
	handler, err := NewHandler(cfg, slog.Default())
	if err != nil {
		t.Fatalf("init: %v", err)
	}

	req := httptest.NewRequest("POST", "/v1/completions",
		strings.NewReader(`{"model":"gpt-5.4","prompt":"test"}`))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Errorf("oversized passthrough response should return 502, got %d", rec.Code)
	}
}

func TestAnthropicToolChoiceNoneReturns400(t *testing.T) {
	anthropicMock := mockAnthropicBackend()
	defer anthropicMock.Close()
	openaiMock := mockOpenAIBackend()
	defer openaiMock.Close()

	cfg := buildConfig(openaiMock.URL, anthropicMock.URL)
	handler, err := NewHandler(cfg, slog.Default())
	if err != nil {
		t.Fatalf("init: %v", err)
	}

	body := `{
		"model": "claude-sonnet-4-6",
		"messages": [{"role": "user", "content": "test"}],
		"tools": [{"type": "function", "function": {"name": "f", "description": "d", "parameters": {"type": "object", "properties": {}}}}],
		"tool_choice": "none"
	}`
	req := httptest.NewRequest("POST", "/oai-ant/v1/chat/completions", strings.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("tool_choice none to Anthropic should return 400, got %d", rec.Code)
	}
	var resp map[string]any
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	msg, _ := resp["error"].(map[string]any)["message"].(string)
	if !strings.Contains(msg, "none") {
		t.Errorf("error should mention \"none\", got: %q", msg)
	}
}

func TestMethodGate(t *testing.T) {
	upstreamMock := mockOpenAIFullBackend()
	defer upstreamMock.Close()

	cfg := buildPassthroughConfig(upstreamMock.URL)
	handler, err := NewHandler(cfg, slog.Default())
	if err != nil {
		t.Fatalf("init: %v", err)
	}

	req := httptest.NewRequest("GET", "/v1/models", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("GET /v1/models should succeed, got %d", rec.Code)
	}

	req = httptest.NewRequest("POST", "/v1/models", strings.NewReader("{}"))
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /v1/models should return 405, got %d", rec.Code)
	}

	openaiMock := mockOpenAIBackend()
	defer openaiMock.Close()
	anthropicMock := mockAnthropicBackend()
	defer anthropicMock.Close()

	chatCfg := buildConfig(openaiMock.URL, anthropicMock.URL)
	chatHandler, err := NewHandler(chatCfg, slog.Default())
	if err != nil {
		t.Fatalf("init: %v", err)
	}

	req = httptest.NewRequest("POST", "/oai-oai/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-5.4","messages":[{"role":"user","content":"test"}]}`))
	rec = httptest.NewRecorder()
	chatHandler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("POST /v1/chat/completions should succeed, got %d", rec.Code)
	}

	req = httptest.NewRequest("GET", "/oai-oai/v1/chat/completions", nil)
	rec = httptest.NewRecorder()
	chatHandler.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET /v1/chat/completions should return 405, got %d", rec.Code)
	}
}

type testPostResponseHook struct {
	name string
	fn   func(*pipeline.NormalizedResponse) error
}

func (h *testPostResponseHook) Name() string { return h.name }
func (h *testPostResponseHook) Execute(_ context.Context, resp *pipeline.NormalizedResponse) error {
	return h.fn(resp)
}

type testStreamEventHook struct {
	name string
	fn   func(*pipeline.StreamEvent) error
}

func (h *testStreamEventHook) Name() string { return h.name }
func (h *testStreamEventHook) Execute(_ context.Context, event *pipeline.StreamEvent) error {
	return h.fn(event)
}

type testErrorHook struct {
	name string
	fn   func(*pipeline.PipelineError) error
}

func (h *testErrorHook) Name() string { return h.name }
func (h *testErrorHook) Execute(_ context.Context, pErr *pipeline.PipelineError) error {
	return h.fn(pErr)
}
