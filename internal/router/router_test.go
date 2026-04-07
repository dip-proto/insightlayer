package router

import (
	"testing"

	"github.com/j/insightlayer/internal/config"
	"github.com/j/insightlayer/internal/pipeline"
)

func backends() []config.BackendConfig {
	return []config.BackendConfig{
		{Name: "openai-main", Protocol: "openai"},
		{Name: "anthropic-main", Protocol: "anthropic"},
	}
}

func TestBasicRouteResolution(t *testing.T) {
	cfg := config.RoutingConfig{
		Routes: []config.RouteConfig{
			{
				Name:            "openai-chat",
				Priority:        100,
				InboundProtocol: "openai",
				PathPrefix:      "/v1/chat/completions",
				EndpointKinds:   []string{"chat"},
				Backend:         "openai-main",
			},
			{
				Name:            "openai-default",
				Priority:        10,
				InboundProtocol: "openai",
				PathPrefix:      "/v1",
				EndpointKinds:   []string{"completion", "embedding", "model_list"},
				Backend:         "openai-main",
			},
		},
	}

	r, err := New(cfg, backends())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	tests := []struct {
		path     string
		wantName string
		wantKind pipeline.EndpointKind
	}{
		{"/v1/chat/completions", "openai-chat", pipeline.EndpointChat},
		{"/v1/completions", "openai-default", pipeline.EndpointCompletion},
		{"/v1/embeddings", "openai-default", pipeline.EndpointEmbedding},
		{"/v1/models", "openai-default", pipeline.EndpointModelList},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			resolved, err := r.Resolve(tt.path)
			if err != nil {
				t.Fatalf("resolve error: %v", err)
			}
			if resolved.Route.Name != tt.wantName {
				t.Errorf("route = %q, want %q", resolved.Route.Name, tt.wantName)
			}
			if resolved.EndpointKind != tt.wantKind {
				t.Errorf("kind = %q, want %q", resolved.EndpointKind, tt.wantKind)
			}
		})
	}
}

func TestAnthropicRouteResolution(t *testing.T) {
	cfg := config.RoutingConfig{
		Routes: []config.RouteConfig{
			{
				Name:            "anthropic-messages",
				Priority:        100,
				InboundProtocol: "anthropic",
				PathPrefix:      "/v1/messages",
				EndpointKinds:   []string{"chat"},
				Backend:         "anthropic-main",
			},
		},
	}

	r, err := New(cfg, backends())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	resolved, err := r.Resolve("/v1/messages")
	if err != nil {
		t.Fatalf("resolve error: %v", err)
	}
	if resolved.Route.Name != "anthropic-messages" {
		t.Errorf("route = %q, want %q", resolved.Route.Name, "anthropic-messages")
	}
}

func TestNoMatchReturnsError(t *testing.T) {
	cfg := config.RoutingConfig{
		Routes: []config.RouteConfig{
			{
				Name:            "openai-chat",
				Priority:        100,
				InboundProtocol: "openai",
				PathPrefix:      "/v1/chat/completions",
				EndpointKinds:   []string{"chat"},
				Backend:         "openai-main",
			},
		},
	}

	r, err := New(cfg, backends())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, err = r.Resolve("/v2/something")
	if err == nil {
		t.Fatal("expected error for unmatched route")
	}
}

func TestAmbiguousRoutesRejected(t *testing.T) {
	cfg := config.RoutingConfig{
		Routes: []config.RouteConfig{
			{
				Name:            "route-a",
				Priority:        100,
				InboundProtocol: "openai",
				PathPrefix:      "/v1/chat/completions",
				EndpointKinds:   []string{"chat"},
				Backend:         "openai-main",
			},
			{
				Name:            "route-b",
				Priority:        100,
				InboundProtocol: "openai",
				PathPrefix:      "/v1/chat/completions",
				EndpointKinds:   []string{"chat"},
				Backend:         "anthropic-main",
			},
		},
	}

	_, err := New(cfg, backends())
	if err == nil {
		t.Fatal("expected ambiguity error")
	}
}

func TestDifferentPrioritiesNotAmbiguous(t *testing.T) {
	cfg := config.RoutingConfig{
		Routes: []config.RouteConfig{
			{
				Name:            "route-a",
				Priority:        100,
				InboundProtocol: "openai",
				PathPrefix:      "/v1/chat/completions",
				EndpointKinds:   []string{"chat"},
				Backend:         "openai-main",
			},
			{
				Name:            "route-b",
				Priority:        50,
				InboundProtocol: "openai",
				PathPrefix:      "/v1/chat/completions",
				EndpointKinds:   []string{"chat"},
				Backend:         "anthropic-main",
			},
		},
	}

	r, err := New(cfg, backends())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	resolved, err := r.Resolve("/v1/chat/completions")
	if err != nil {
		t.Fatalf("resolve error: %v", err)
	}
	if resolved.Route.Name != "route-a" {
		t.Errorf("route = %q, want %q (higher priority)", resolved.Route.Name, "route-a")
	}
}

func TestUnknownBackendRejected(t *testing.T) {
	cfg := config.RoutingConfig{
		Routes: []config.RouteConfig{
			{
				Name:    "bad-route",
				Backend: "nonexistent",
			},
		},
	}

	_, err := New(cfg, backends())
	if err == nil {
		t.Fatal("expected error for unknown backend")
	}
}

func TestPriorityThenPrefixLength(t *testing.T) {
	cfg := config.RoutingConfig{
		Routes: []config.RouteConfig{
			{
				Name:            "broad",
				Priority:        100,
				InboundProtocol: "openai",
				PathPrefix:      "/v1",
				EndpointKinds:   []string{"chat", "completion"},
				Backend:         "openai-main",
			},
			{
				Name:            "specific",
				Priority:        100,
				InboundProtocol: "openai",
				PathPrefix:      "/v1/chat/completions",
				EndpointKinds:   []string{"chat"},
				Backend:         "anthropic-main",
			},
		},
	}

	r, err := New(cfg, backends())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	resolved, err := r.Resolve("/v1/chat/completions")
	if err != nil {
		t.Fatalf("resolve error: %v", err)
	}
	if resolved.Route.Name != "specific" {
		t.Errorf("route = %q, want %q (longer prefix)", resolved.Route.Name, "specific")
	}
}

func TestCustomPrefixRoutes(t *testing.T) {
	cfg := config.RoutingConfig{
		Routes: []config.RouteConfig{
			{
				Name:            "custom-route",
				Priority:        100,
				InboundProtocol: "openai",
				PathPrefix:      "/custom/v1/chat/completions",
				EndpointKinds:   []string{"chat"},
				Backend:         "openai-main",
			},
		},
	}

	r, err := New(cfg, backends())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	resolved, err := r.Resolve("/custom/v1/chat/completions")
	if err != nil {
		t.Fatalf("resolve error: %v", err)
	}
	if resolved.Route.Name != "custom-route" {
		t.Errorf("route = %q, want %q", resolved.Route.Name, "custom-route")
	}
	if resolved.EndpointKind != pipeline.EndpointChat {
		t.Errorf("kind = %q, want chat", resolved.EndpointKind)
	}
}
