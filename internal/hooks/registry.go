package hooks

import (
	"fmt"

	"github.com/j/insightlayer/internal/config"
	"github.com/j/insightlayer/internal/pipeline"
)

type HookFactory func(cfg config.HookConfig) (any, error)

var factories = map[string]HookFactory{}

func RegisterFactory(typeName string, f HookFactory) {
	factories[typeName] = f
}

var validStageInterfaces = map[string]func(any) bool{
	"pre_request": func(h any) bool {
		_, ok := h.(pipeline.PreRequestHook)
		return ok
	},
	"post_response": func(h any) bool {
		_, ok := h.(pipeline.PostResponseHook)
		return ok
	},
	"stream_event": func(h any) bool {
		_, ok := h.(pipeline.StreamEventHook)
		return ok
	},
	"error": func(h any) bool {
		_, ok := h.(pipeline.ErrorHook)
		return ok
	},
}

func BuildFromConfig(cfgs []config.HookConfig) (*Manager, error) {
	m := NewManager()

	for _, cfg := range cfgs {
		if !cfg.Enabled {
			continue
		}

		factory, ok := factories[cfg.Type]
		if !ok {
			return nil, fmt.Errorf("unknown hook type: %q", cfg.Type)
		}

		hook, err := factory(cfg)
		if err != nil {
			return nil, fmt.Errorf("build hook %q: %w", cfg.Name, err)
		}

		if cfg.Stage != "" {
			check, known := validStageInterfaces[cfg.Stage]
			if !known {
				return nil, fmt.Errorf("hook %q: unknown stage %q", cfg.Name, cfg.Stage)
			}
			if !check(hook) {
				return nil, fmt.Errorf("hook %q (type %q) does not implement the %s interface", cfg.Name, cfg.Type, cfg.Stage)
			}
		}

		registered := false
		if cfg.Stage == "" || cfg.Stage == "pre_request" {
			if h, ok := hook.(pipeline.PreRequestHook); ok {
				m.AddPreRequest(h)
				registered = true
			}
		}
		if cfg.Stage == "" || cfg.Stage == "post_response" {
			if h, ok := hook.(pipeline.PostResponseHook); ok {
				m.AddPostResponse(h)
				registered = true
			}
		}
		if cfg.Stage == "" || cfg.Stage == "stream_event" {
			if h, ok := hook.(pipeline.StreamEventHook); ok {
				m.AddStreamEvent(h)
				registered = true
			}
		}
		if cfg.Stage == "" || cfg.Stage == "error" {
			if h, ok := hook.(pipeline.ErrorHook); ok {
				m.AddError(h)
				registered = true
			}
		}

		if !registered {
			return nil, fmt.Errorf("hook %q does not implement any hook interface", cfg.Name)
		}
	}

	return m, nil
}
