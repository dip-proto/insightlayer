package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadYAML(t *testing.T) {
	path := writeTemp(t, "config.yaml", `
server:
  listen: ":9090"
  read_timeout: 30s
  idle_timeout: 60s
routing:
  routes:
    - name: test-route
      priority: 100
      inbound_protocol: openai
      path_prefix: /v1/chat/completions
      endpoint_kinds: [chat]
      backend: my-backend
backends:
  - name: my-backend
    protocol: openai
    base_url: https://api.example.com
    auth:
      mode: inject
      header: Authorization
      value: Bearer sk-test
hooks:
  - name: logger
    type: logging
    stage: pre_request
    enabled: true
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Server.Listen != ":9090" {
		t.Errorf("listen = %q, want %q", cfg.Server.Listen, ":9090")
	}
	if cfg.Server.ReadTimeout != 30*time.Second {
		t.Errorf("read_timeout = %v, want %v", cfg.Server.ReadTimeout, 30*time.Second)
	}
	if cfg.Server.WriteTimeout != 0 {
		t.Errorf("write_timeout = %v, want 0", cfg.Server.WriteTimeout)
	}
	if len(cfg.Routing.Routes) != 1 {
		t.Fatalf("routes count = %d, want 1", len(cfg.Routing.Routes))
	}
	r := cfg.Routing.Routes[0]
	if r.Name != "test-route" || r.Priority != 100 || r.Backend != "my-backend" {
		t.Errorf("route = %+v", r)
	}
	if len(cfg.Backends) != 1 {
		t.Fatalf("backends count = %d, want 1", len(cfg.Backends))
	}
	b := cfg.Backends[0]
	if b.Auth.Mode != AuthModeInject {
		t.Errorf("auth mode = %q, want %q", b.Auth.Mode, AuthModeInject)
	}
	if len(cfg.Hooks) != 1 || cfg.Hooks[0].Type != "logging" {
		t.Errorf("hooks = %+v", cfg.Hooks)
	}
}

func TestLoadJSON(t *testing.T) {
	path := writeTemp(t, "config.json", `{
		"server": {"listen": ":7070"},
		"backends": [{"name": "b", "protocol": "anthropic", "base_url": "https://api.anthropic.com"}]
	}`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Server.Listen != ":7070" {
		t.Errorf("listen = %q, want %q", cfg.Server.Listen, ":7070")
	}
	if cfg.Backends[0].Auth.Mode != AuthModeInject {
		t.Errorf("default auth mode = %q, want %q", cfg.Backends[0].Auth.Mode, AuthModeInject)
	}
}

func TestEnvExpansion(t *testing.T) {
	t.Setenv("TEST_API_KEY", "sk-secret-123")
	path := writeTemp(t, "config.yaml", `
server:
  listen: ":8080"
backends:
  - name: b
    protocol: openai
    base_url: https://api.example.com
    auth:
      mode: inject
      header: Authorization
      value: Bearer ${TEST_API_KEY}
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Backends[0].Auth.Value != "Bearer sk-secret-123" {
		t.Errorf("auth value = %q, want %q", cfg.Backends[0].Auth.Value, "Bearer sk-secret-123")
	}
}

func TestEnvExpansionUnsetVar(t *testing.T) {
	path := writeTemp(t, "config.yaml", `
server:
  listen: ":8080"
backends:
  - name: b
    protocol: openai
    base_url: https://api.example.com
    auth:
      value: ${UNSET_VAR_12345}
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Backends[0].Auth.Value != "${UNSET_VAR_12345}" {
		t.Errorf("unset var should be preserved, got %q", cfg.Backends[0].Auth.Value)
	}
}

func TestDefaults(t *testing.T) {
	path := writeTemp(t, "config.yaml", `{}`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Server.Listen != ":8080" {
		t.Errorf("default listen = %q, want %q", cfg.Server.Listen, ":8080")
	}
	if cfg.Server.ReadTimeout != 60*time.Second {
		t.Errorf("default read_timeout = %v, want %v", cfg.Server.ReadTimeout, 60*time.Second)
	}
	if cfg.Server.IdleTimeout != 120*time.Second {
		t.Errorf("default idle_timeout = %v, want %v", cfg.Server.IdleTimeout, 120*time.Second)
	}
}

func TestValidationDuplicateBackend(t *testing.T) {
	path := writeTemp(t, "config.yaml", `
backends:
  - name: same
    protocol: openai
    base_url: https://a.com
  - name: same
    protocol: openai
    base_url: https://b.com
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for duplicate backend name")
	}
}

func TestValidationDuplicateRoute(t *testing.T) {
	path := writeTemp(t, "config.yaml", `
backends:
  - name: b
    protocol: openai
    base_url: https://a.com
routing:
  routes:
    - name: same
      path_prefix: /v1
      backend: b
    - name: same
      path_prefix: /v1
      backend: b
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for duplicate route name")
	}
}

func TestValidationMissingBackendURL(t *testing.T) {
	path := writeTemp(t, "config.yaml", `
backends:
  - name: b
    protocol: openai
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for missing base_url")
	}
}

func TestValidationRouteReferencesUnknownBackend(t *testing.T) {
	path := writeTemp(t, "config.yaml", `
backends:
  - name: b
    protocol: openai
    base_url: https://a.com
routing:
  routes:
    - name: r
      path_prefix: /v1
      backend: nonexistent
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for unknown backend reference")
	}
}

func TestValidationInvalidAuthMode(t *testing.T) {
	path := writeTemp(t, "config.yaml", `
backends:
  - name: b
    protocol: openai
    base_url: https://a.com
    auth:
      mode: bad_mode
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for invalid auth mode")
	}
}

func TestUnsupportedFormat(t *testing.T) {
	path := writeTemp(t, "config.toml", `key = "value"`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for unsupported format")
	}
}

func writeTemp(t *testing.T, name, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
