package hooks

import (
	"context"
	"log/slog"
	"net/http"
	"strings"

	"github.com/j/insightlayer/internal/config"
	"github.com/j/insightlayer/internal/observability"
	"github.com/j/insightlayer/internal/pipeline"
)

func parseReplacements(cfg map[string]any) map[string]string {
	out := map[string]string{}
	reps, ok := cfg["replacements"].(map[string]any)
	if !ok {
		return out
	}
	for k, v := range reps {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}
	return out
}

func init() {
	RegisterFactory("logging", newLoggingHook)
	RegisterFactory("request_text_modifier", newRequestTextModifier)
	RegisterFactory("response_text_modifier", newResponseTextModifier)
	RegisterFactory("stream_text_modifier", newStreamTextModifier)
	RegisterFactory("header_modifier", newHeaderModifier)
}

// logging hook

type loggingHook struct {
	name    string
	verbose bool
	logger  *slog.Logger
}

func newLoggingHook(cfg config.HookConfig) (any, error) {
	verbose, _ := cfg.Config["verbose"].(bool)
	return &loggingHook{
		name:    cfg.Name,
		verbose: verbose,
		logger:  slog.Default(),
	}, nil
}

func (h *loggingHook) Name() string { return h.name }

func (h *loggingHook) Execute(ctx context.Context, req *pipeline.NormalizedRequest) error {
	logger := observability.LoggerFrom(ctx, h.logger)
	logger.Info("request",
		"model", req.Model,
		"endpoint", req.EndpointKind,
		"stream", req.Stream,
		"messages", len(req.Messages),
	)
	if h.verbose && req.SystemPrompt != "" {
		logger.Info("system prompt", "text", req.SystemPrompt)
	}
	return nil
}

// request text modifier

type requestTextModifier struct {
	name         string
	target       string
	replacements map[string]string
}

func newRequestTextModifier(cfg config.HookConfig) (any, error) {
	target, _ := cfg.Config["target"].(string)
	if target == "" {
		target = "all"
	}

	return &requestTextModifier{
		name:         cfg.Name,
		target:       target,
		replacements: parseReplacements(cfg.Config),
	}, nil
}

func (h *requestTextModifier) Name() string { return h.name }

func (h *requestTextModifier) Execute(_ context.Context, req *pipeline.NormalizedRequest) error {
	if h.target == "system" || h.target == "all" {
		req.SystemPrompt = h.applyReplacements(req.SystemPrompt)
	}

	for i := range req.Messages {
		msg := &req.Messages[i]
		switch h.target {
		case "all":
			msg.Content = h.applyReplacements(msg.Content)
		case "user":
			if msg.Role == "user" {
				msg.Content = h.applyReplacements(msg.Content)
			}
		case "assistant":
			if msg.Role == "assistant" {
				msg.Content = h.applyReplacements(msg.Content)
			}
		}
	}
	return nil
}

func (h *requestTextModifier) applyReplacements(s string) string {
	for old, new := range h.replacements {
		s = strings.ReplaceAll(s, old, new)
	}
	return s
}

// response text modifier

type responseTextModifier struct {
	name         string
	replacements map[string]string
}

func newResponseTextModifier(cfg config.HookConfig) (any, error) {
	return &responseTextModifier{
		name:         cfg.Name,
		replacements: parseReplacements(cfg.Config),
	}, nil
}

func (h *responseTextModifier) Name() string { return h.name }

func (h *responseTextModifier) Execute(_ context.Context, resp *pipeline.NormalizedResponse) error {
	for old, new := range h.replacements {
		resp.Content = strings.ReplaceAll(resp.Content, old, new)
		for i := range resp.Choices {
			resp.Choices[i].Text = strings.ReplaceAll(resp.Choices[i].Text, old, new)
		}
	}
	return nil
}

// stream text modifier

type streamTextModifier struct {
	name         string
	replacements map[string]string
}

func newStreamTextModifier(cfg config.HookConfig) (any, error) {
	return &streamTextModifier{
		name:         cfg.Name,
		replacements: parseReplacements(cfg.Config),
	}, nil
}

func (h *streamTextModifier) Name() string { return h.name }

func (h *streamTextModifier) Execute(_ context.Context, event *pipeline.StreamEvent) error {
	if event.Type == pipeline.StreamEventTextDelta {
		for old, new := range h.replacements {
			event.Text = strings.ReplaceAll(event.Text, old, new)
		}
	}
	return nil
}

// header modifier

func newHeaderModifier(cfg config.HookConfig) (any, error) {
	add := map[string]string{}
	if a, ok := cfg.Config["add"].(map[string]any); ok {
		for k, v := range a {
			if s, ok := v.(string); ok {
				add[k] = s
			}
		}
	}

	var remove []string
	if r, ok := cfg.Config["remove"].([]any); ok {
		for _, v := range r {
			if s, ok := v.(string); ok {
				remove = append(remove, s)
			}
		}
	}

	phase, _ := cfg.Config["phase"].(string)
	if phase == "" {
		phase = "request"
	}

	switch phase {
	case "response":
		return &responseHeaderModifier{
			name:   cfg.Name,
			add:    add,
			remove: remove,
		}, nil
	default:
		return &requestHeaderModifier{
			name:   cfg.Name,
			add:    add,
			remove: remove,
		}, nil
	}
}

type requestHeaderModifier struct {
	name   string
	add    map[string]string
	remove []string
}

func (h *requestHeaderModifier) Name() string { return h.name }

func (h *requestHeaderModifier) Execute(_ context.Context, req *pipeline.NormalizedRequest) error {
	if req.Headers == nil {
		req.Headers = make(http.Header)
	}
	for k, v := range h.add {
		req.Headers.Set(k, v)
	}
	for _, k := range h.remove {
		req.Headers.Del(k)
	}
	return nil
}

type responseHeaderModifier struct {
	name   string
	add    map[string]string
	remove []string
}

func (h *responseHeaderModifier) Name() string { return h.name }

func (h *responseHeaderModifier) Execute(_ context.Context, resp *pipeline.NormalizedResponse) error {
	if resp.Headers == nil {
		resp.Headers = make(http.Header)
	}
	for k, v := range h.add {
		resp.Headers.Set(k, v)
	}
	for _, k := range h.remove {
		resp.Headers.Del(k)
	}
	return nil
}
