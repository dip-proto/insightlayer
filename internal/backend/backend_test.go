package backend

import (
	"net/http"
	"testing"

	"github.com/j/insightlayer/internal/config"
	"github.com/j/insightlayer/internal/pipeline"
)

func TestResolveCredentialInject(t *testing.T) {
	cfg := config.AuthConfig{
		Mode:   config.AuthModeInject,
		Header: "Authorization",
		Value:  "Bearer sk-configured",
	}
	clientHeaders := http.Header{"Authorization": {"Bearer sk-client"}}

	cred := ResolveCredential(cfg, clientHeaders)
	if cred.Value != "Bearer sk-configured" {
		t.Errorf("inject should use configured value, got %q", cred.Value)
	}
}

func TestResolveCredentialPassthrough(t *testing.T) {
	cfg := config.AuthConfig{
		Mode:   config.AuthModePassthrough,
		Header: "Authorization",
		Value:  "Bearer sk-configured",
	}
	clientHeaders := http.Header{"Authorization": {"Bearer sk-client"}}

	cred := ResolveCredential(cfg, clientHeaders)
	if cred.Value != "Bearer sk-client" {
		t.Errorf("passthrough should use client value, got %q", cred.Value)
	}
}

func TestResolveCredentialPassthroughNoClient(t *testing.T) {
	cfg := config.AuthConfig{
		Mode: config.AuthModePassthrough,
	}

	cred := ResolveCredential(cfg, http.Header{})
	if cred.Value != "" {
		t.Errorf("passthrough with no client cred should be empty, got %q", cred.Value)
	}
}

func TestResolveCredentialPreferClient(t *testing.T) {
	cfg := config.AuthConfig{
		Mode:   config.AuthModePreferClient,
		Header: "Authorization",
		Value:  "Bearer sk-configured",
	}

	t.Run("with client", func(t *testing.T) {
		clientHeaders := http.Header{"Authorization": {"Bearer sk-client"}}
		cred := ResolveCredential(cfg, clientHeaders)
		if cred.Value != "Bearer sk-client" {
			t.Errorf("prefer_client should use client value when present, got %q", cred.Value)
		}
	})

	t.Run("without client", func(t *testing.T) {
		cred := ResolveCredential(cfg, http.Header{})
		if cred.Value != "Bearer sk-configured" {
			t.Errorf("prefer_client should fall back to configured, got %q", cred.Value)
		}
	})
}

func TestMapCredentialToOpenAI(t *testing.T) {
	cred := Credential{Header: "X-Api-Key", Value: "sk-123"}
	mapped := MapCredentialToProtocol(cred, pipeline.ProtocolOpenAI)
	if mapped.Header != "Authorization" {
		t.Errorf("header = %q, want Authorization", mapped.Header)
	}
	if mapped.Value != "Bearer sk-123" {
		t.Errorf("value = %q, want %q", mapped.Value, "Bearer sk-123")
	}
}

func TestMapCredentialToAnthropic(t *testing.T) {
	cred := Credential{Header: "Authorization", Value: "Bearer sk-123"}
	mapped := MapCredentialToProtocol(cred, pipeline.ProtocolAnthropic)
	if mapped.Header != "X-Api-Key" {
		t.Errorf("header = %q, want X-Api-Key", mapped.Header)
	}
	if mapped.Value != "sk-123" {
		t.Errorf("value = %q, want %q (Bearer stripped)", mapped.Value, "sk-123")
	}
}

func TestRegistryGetUnknown(t *testing.T) {
	reg := NewRegistry()
	_, err := reg.Get("nope")
	if err == nil {
		t.Fatal("expected error for unknown backend")
	}
}
