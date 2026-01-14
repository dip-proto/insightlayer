package httpserver

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/j/insightlayer/internal/config"
)

func TestHealthEndpoint(t *testing.T) {
	srv := New(config.ServerConfig{Listen: ":0"}, slog.Default())

	ts := httptest.NewServer(srv.Mux())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/health")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	body, _ := io.ReadAll(resp.Body)
	var result map[string]string
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if result["status"] != "ok" {
		t.Errorf("status = %q, want %q", result["status"], "ok")
	}
}

func TestHealthMethodNotAllowed(t *testing.T) {
	srv := New(config.ServerConfig{Listen: ":0"}, slog.Default())

	ts := httptest.NewServer(srv.Mux())
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/health", "application/json", nil)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusOK {
		t.Error("POST /health should not return 200")
	}
}

func TestGracefulShutdown(t *testing.T) {
	srv := New(config.ServerConfig{
		Listen:      ":0",
		ReadTimeout: 5 * time.Second,
		IdleTimeout: 5 * time.Second,
	}, slog.Default())

	go func() { _ = srv.ListenAndServe() }()

	if err := srv.Shutdown(2 * time.Second); err != nil {
		t.Errorf("shutdown error: %v", err)
	}
}
