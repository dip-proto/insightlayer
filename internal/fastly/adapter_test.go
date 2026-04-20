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
)

type mockResponseWriter struct {
	header     fsthttp.Header
	body       bytes.Buffer
	statusCode int
}

func newMockResponseWriter() *mockResponseWriter {
	return &mockResponseWriter{header: fsthttp.NewHeader()}
}

func (m *mockResponseWriter) Header() fsthttp.Header      { return m.header }
func (m *mockResponseWriter) WriteHeader(code int)         { m.statusCode = code }
func (m *mockResponseWriter) Write(p []byte) (int, error)  { return m.body.Write(p) }
func (m *mockResponseWriter) Close() error                 { return nil }
func (m *mockResponseWriter) SetManualFramingMode(bool)    {}
func (m *mockResponseWriter) Append(io.ReadCloser) error   { return nil }

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
	return &fsthttp.Request{
		Method: method,
		URL:    u,
		Header: fsthttp.NewHeader(),
		Body:   io.NopCloser(strings.NewReader(body)),
	}
}

func TestHealthEndpoint(t *testing.T) {
	adapter := NewAdapter(buildTestHandler("http://unused"))

	t.Run("GET", func(t *testing.T) {
		w := newMockResponseWriter()
		adapter.ServeHTTP(context.Background(), w, newFastlyRequest("GET", "/health", ""))

		if w.statusCode != http.StatusOK {
			t.Errorf("status = %d, want %d", w.statusCode, http.StatusOK)
		}
		var result map[string]string
		if err := json.Unmarshal(w.body.Bytes(), &result); err != nil {
			t.Fatalf("invalid json: %v", err)
		}
		if result["status"] != "ok" {
			t.Errorf("status = %q, want %q", result["status"], "ok")
		}
	})

	t.Run("HEAD", func(t *testing.T) {
		w := newMockResponseWriter()
		adapter.ServeHTTP(context.Background(), w, newFastlyRequest("HEAD", "/health", ""))

		if w.statusCode != http.StatusOK {
			t.Errorf("status = %d, want %d", w.statusCode, http.StatusOK)
		}
		if w.body.Len() != 0 {
			t.Errorf("body should be empty, got %q", w.body.String())
		}
	})

	t.Run("POST", func(t *testing.T) {
		w := newMockResponseWriter()
		adapter.ServeHTTP(context.Background(), w, newFastlyRequest("POST", "/health", ""))

		if w.statusCode != http.StatusMethodNotAllowed {
			t.Errorf("status = %d, want %d", w.statusCode, http.StatusMethodNotAllowed)
		}
	})
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
		t.Fatalf("status = %d, want %d", w.statusCode, http.StatusOK)
	}

	var resp map[string]any
	if err := json.Unmarshal(w.body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	choices, ok := resp["choices"].([]any)
	if !ok || len(choices) == 0 {
		t.Fatal("expected non-empty choices array")
	}
}

func TestBuildTransport(t *testing.T) {
	backends := map[string]*url.URL{
		"openai":    {Scheme: "https", Host: "api.openai.com"},
		"anthropic": {Scheme: "https", Host: "api.anthropic.com"},
	}
	if BuildTransport(backends) == nil {
		t.Fatal("BuildTransport returned nil")
	}
	if BuildTransport(map[string]*url.URL{}) == nil {
		t.Fatal("BuildTransport with empty map returned nil")
	}
}
