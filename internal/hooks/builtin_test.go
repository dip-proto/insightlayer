package hooks

import (
	"context"
	"testing"

	"github.com/j/insightlayer/internal/config"
	"github.com/j/insightlayer/internal/pipeline"
)

func TestLoggingHook(t *testing.T) {
	cfg := config.HookConfig{
		Name:    "test-logger",
		Type:    "logging",
		Stage:   "pre_request",
		Enabled: true,
		Config:  map[string]any{"verbose": true},
	}

	hook, err := newLoggingHook(cfg)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	h := hook.(*loggingHook)
	req := &pipeline.NormalizedRequest{
		Model:        "gpt-5.4",
		EndpointKind: pipeline.EndpointChat,
		SystemPrompt: "test system",
		Messages:     []pipeline.Message{{Role: "user", Content: "hi"}},
	}

	if err := h.Execute(context.Background(), req); err != nil {
		t.Fatalf("execute: %v", err)
	}
}

func TestRequestTextModifier(t *testing.T) {
	cfg := config.HookConfig{
		Name: "text-mod",
		Type: "request_text_modifier",
		Config: map[string]any{
			"target":       "all",
			"replacements": map[string]any{"bad": "good", "old": "new"},
		},
	}

	hook, err := newRequestTextModifier(cfg)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	h := hook.(*requestTextModifier)
	req := &pipeline.NormalizedRequest{
		SystemPrompt: "the bad old system",
		Messages: []pipeline.Message{
			{Role: "user", Content: "this is bad and old"},
		},
	}

	if err := h.Execute(context.Background(), req); err != nil {
		t.Fatalf("execute: %v", err)
	}

	if req.SystemPrompt != "the good new system" {
		t.Errorf("system = %q", req.SystemPrompt)
	}
	if req.Messages[0].Content != "this is good and new" {
		t.Errorf("content = %q", req.Messages[0].Content)
	}
}

func TestRequestTextModifierTargetUser(t *testing.T) {
	cfg := config.HookConfig{
		Name: "user-only",
		Type: "request_text_modifier",
		Config: map[string]any{
			"target":       "user",
			"replacements": map[string]any{"foo": "bar"},
		},
	}

	hook, err := newRequestTextModifier(cfg)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	h := hook.(*requestTextModifier)
	req := &pipeline.NormalizedRequest{
		Messages: []pipeline.Message{
			{Role: "user", Content: "foo message"},
			{Role: "assistant", Content: "foo response"},
		},
	}

	_ = h.Execute(context.Background(), req)

	if req.Messages[0].Content != "bar message" {
		t.Errorf("user content = %q", req.Messages[0].Content)
	}
	if req.Messages[1].Content != "foo response" {
		t.Errorf("assistant content should be unchanged, got %q", req.Messages[1].Content)
	}
}

func TestResponseTextModifier(t *testing.T) {
	cfg := config.HookConfig{
		Name: "resp-mod",
		Type: "response_text_modifier",
		Config: map[string]any{
			"replacements": map[string]any{"secret": "[REDACTED]"},
		},
	}

	hook, err := newResponseTextModifier(cfg)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	h := hook.(*responseTextModifier)
	resp := &pipeline.NormalizedResponse{Content: "the secret is here"}

	_ = h.Execute(context.Background(), resp)

	if resp.Content != "the [REDACTED] is here" {
		t.Errorf("content = %q", resp.Content)
	}
}

func TestStreamTextModifier(t *testing.T) {
	cfg := config.HookConfig{
		Name: "stream-mod",
		Type: "stream_text_modifier",
		Config: map[string]any{
			"replacements": map[string]any{"old": "new"},
		},
	}

	hook, err := newStreamTextModifier(cfg)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	h := hook.(*streamTextModifier)
	event := &pipeline.StreamEvent{Type: pipeline.StreamEventTextDelta, Text: "the old way"}

	_ = h.Execute(context.Background(), event)

	if event.Text != "the new way" {
		t.Errorf("text = %q", event.Text)
	}
}

func TestStreamTextModifierIgnoresNonDelta(t *testing.T) {
	cfg := config.HookConfig{
		Name:   "stream-mod",
		Type:   "stream_text_modifier",
		Config: map[string]any{"replacements": map[string]any{"x": "y"}},
	}

	hook, _ := newStreamTextModifier(cfg)
	h := hook.(*streamTextModifier)
	event := &pipeline.StreamEvent{Type: pipeline.StreamEventStart}

	_ = h.Execute(context.Background(), event)
	// Should not panic or modify non-delta events
}

func TestRequestHeaderModifier(t *testing.T) {
	cfg := config.HookConfig{
		Name: "header-mod",
		Type: "header_modifier",
		Config: map[string]any{
			"add":    map[string]any{"X-Custom": "value"},
			"remove": []any{"X-Remove-Me"},
			"phase":  "request",
		},
	}

	hook, err := newHeaderModifier(cfg)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	h := hook.(*requestHeaderModifier)
	req := &pipeline.NormalizedRequest{}
	req.Headers = make(map[string][]string)
	req.Headers.Set("X-Remove-Me", "gone")

	_ = h.Execute(context.Background(), req)

	if req.Headers.Get("X-Custom") != "value" {
		t.Errorf("X-Custom = %q", req.Headers.Get("X-Custom"))
	}
	if req.Headers.Get("X-Remove-Me") != "" {
		t.Error("X-Remove-Me should have been removed")
	}
}

func TestResponseHeaderModifier(t *testing.T) {
	cfg := config.HookConfig{
		Name: "resp-header-mod",
		Type: "header_modifier",
		Config: map[string]any{
			"add":   map[string]any{"X-Response-Custom": "added"},
			"phase": "response",
		},
	}

	hook, err := newHeaderModifier(cfg)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	h := hook.(*responseHeaderModifier)
	resp := &pipeline.NormalizedResponse{}

	_ = h.Execute(context.Background(), resp)

	if resp.Headers.Get("X-Response-Custom") != "added" {
		t.Errorf("X-Response-Custom = %q", resp.Headers.Get("X-Response-Custom"))
	}
}

func TestBuildFromConfig(t *testing.T) {
	cfgs := []config.HookConfig{
		{
			Name:    "logger",
			Type:    "logging",
			Stage:   "pre_request",
			Enabled: true,
			Config:  map[string]any{},
		},
		{
			Name:    "disabled-hook",
			Type:    "logging",
			Stage:   "pre_request",
			Enabled: false,
		},
		{
			Name:    "text-mod",
			Type:    "request_text_modifier",
			Stage:   "pre_request",
			Enabled: true,
			Config: map[string]any{
				"replacements": map[string]any{"a": "b"},
			},
		},
	}

	m, err := BuildFromConfig(cfgs)
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	req := &pipeline.NormalizedRequest{
		Model:    "gpt-5.4",
		Messages: []pipeline.Message{{Role: "user", Content: "aaa"}},
	}

	if err := m.RunPreRequest(context.Background(), req); err != nil {
		t.Fatalf("run: %v", err)
	}

	if req.Messages[0].Content != "bbb" {
		t.Errorf("content = %q, want %q", req.Messages[0].Content, "bbb")
	}
}

func TestBuildFromConfigUnknownType(t *testing.T) {
	cfgs := []config.HookConfig{
		{Name: "bad", Type: "nonexistent", Enabled: true},
	}

	_, err := BuildFromConfig(cfgs)
	if err == nil {
		t.Fatal("expected error for unknown hook type")
	}
}

func TestBuildFromConfigStageMismatch(t *testing.T) {
	cfgs := []config.HookConfig{
		{
			Name:    "wrong-stage",
			Type:    "logging",
			Stage:   "post_response",
			Enabled: true,
			Config:  map[string]any{},
		},
	}

	_, err := BuildFromConfig(cfgs)
	if err == nil {
		t.Fatal("expected error for stage mismatch (logging is pre_request, not post_response)")
	}
}

func TestBuildFromConfigUnknownStage(t *testing.T) {
	cfgs := []config.HookConfig{
		{
			Name:    "bad-stage",
			Type:    "logging",
			Stage:   "nonexistent_stage",
			Enabled: true,
			Config:  map[string]any{},
		},
	}

	_, err := BuildFromConfig(cfgs)
	if err == nil {
		t.Fatal("expected error for unknown stage")
	}
}

func TestBuildFromConfigCorrectStageAccepted(t *testing.T) {
	cfgs := []config.HookConfig{
		{
			Name:    "correct-stage",
			Type:    "logging",
			Stage:   "pre_request",
			Enabled: true,
			Config:  map[string]any{},
		},
	}

	_, err := BuildFromConfig(cfgs)
	if err != nil {
		t.Fatalf("correct stage should be accepted: %v", err)
	}
}

func TestBuildFromConfigEmptyStageAccepted(t *testing.T) {
	cfgs := []config.HookConfig{
		{
			Name:    "no-stage",
			Type:    "logging",
			Enabled: true,
			Config:  map[string]any{},
		},
	}

	_, err := BuildFromConfig(cfgs)
	if err != nil {
		t.Fatalf("empty stage should be accepted: %v", err)
	}
}

// Stage enforcement is also covered structurally: all hook interfaces share
// the Execute method name with different parameter types, so a single Go type
// cannot implement more than one hook interface. The stage validation in
// BuildFromConfig catches mismatches at config load time.
