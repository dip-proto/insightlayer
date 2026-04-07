package app

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/j/insightlayer/internal/backend"
	anthropicbe "github.com/j/insightlayer/internal/backend/anthropic"
	openaibe "github.com/j/insightlayer/internal/backend/openai"
	"github.com/j/insightlayer/internal/config"
	"github.com/j/insightlayer/internal/hooks"
	"github.com/j/insightlayer/internal/pipeline"
	anthropiccodec "github.com/j/insightlayer/internal/protocol/anthropic"
	openaicodec "github.com/j/insightlayer/internal/protocol/openai"
	"github.com/j/insightlayer/internal/router"
)

type Handler struct {
	router   *router.Router
	backends *backend.Registry
	hooks    *hooks.Manager
	logger   *slog.Logger
}

type HandlerOptions struct {
	HTTPClient *http.Client
}

func NewHandler(cfg *config.Config, logger *slog.Logger) (*Handler, error) {
	return NewHandlerWithOptions(cfg, logger, HandlerOptions{})
}

func NewHandlerWithOptions(cfg *config.Config, logger *slog.Logger, opts HandlerOptions) (*Handler, error) {
	reg := backend.NewRegistry()
	for _, bcfg := range cfg.Backends {
		var be backend.Backend
		switch pipeline.Protocol(bcfg.Protocol) {
		case pipeline.ProtocolOpenAI:
			be = openaibe.New(bcfg, opts.HTTPClient)
		case pipeline.ProtocolAnthropic:
			be = anthropicbe.New(bcfg, opts.HTTPClient)
		default:
			return nil, fmt.Errorf("unknown backend protocol: %s", bcfg.Protocol)
		}
		reg.Register(be)
	}

	r, err := router.New(cfg.Routing, cfg.Backends)
	if err != nil {
		return nil, fmt.Errorf("router init: %w", err)
	}

	var hm *hooks.Manager
	if len(cfg.Hooks) > 0 {
		hm, err = hooks.BuildFromConfig(cfg.Hooks)
		if err != nil {
			return nil, fmt.Errorf("hooks init: %w", err)
		}
	} else {
		hm = hooks.NewManager()
	}

	return &Handler{
		router:   r,
		backends: reg,
		hooks:    hm,
		logger:   logger,
	}, nil
}

func (h *Handler) HookManager() *hooks.Manager {
	return h.hooks
}

func isPassthroughEndpoint(resolved *router.ResolvedRoute) bool {
	switch resolved.EndpointKind {
	case pipeline.EndpointEmbedding, pipeline.EndpointModelList:
		return true
	}
	return false
}

func (h *Handler) decodeRequest(resolved *router.ResolvedRoute, body []byte) (*pipeline.NormalizedRequest, error) {
	switch resolved.Route.InboundProtocol {
	case pipeline.ProtocolOpenAI:
		switch resolved.EndpointKind {
		case pipeline.EndpointCompletion:
			return openaicodec.DecodeCompletionRequest(body)
		default:
			return openaicodec.DecodeRequest(body)
		}
	case pipeline.ProtocolAnthropic:
		return anthropiccodec.DecodeRequest(body)
	default:
		return nil, fmt.Errorf("unsupported inbound protocol: %s", resolved.Route.InboundProtocol)
	}
}

func (h *Handler) encodeResponse(resolved *router.ResolvedRoute, resp *pipeline.NormalizedResponse) ([]byte, error) {
	switch resolved.Route.InboundProtocol {
	case pipeline.ProtocolOpenAI:
		switch resolved.EndpointKind {
		case pipeline.EndpointCompletion:
			return openaicodec.EncodeCompletionResponse(resp)
		default:
			return openaicodec.EncodeResponse(resp)
		}
	case pipeline.ProtocolAnthropic:
		return anthropiccodec.EncodeResponse(resp)
	default:
		return nil, fmt.Errorf("unsupported inbound protocol: %s", resolved.Route.InboundProtocol)
	}
}

func snapshotResponseTexts(resp *pipeline.NormalizedResponse) (string, []string) {
	texts := make([]string, len(resp.Choices))
	for i, c := range resp.Choices {
		texts[i] = c.Text
	}
	return resp.Content, texts
}

func syncResponseContent(resp *pipeline.NormalizedResponse, preContent string, preChoices []string) {
	if len(resp.Choices) == 0 {
		return
	}

	contentChanged := resp.Content != preContent
	choicesChanged := false
	for i, c := range resp.Choices {
		if i < len(preChoices) && c.Text != preChoices[i] {
			choicesChanged = true
			break
		}
	}

	switch {
	case contentChanged && !choicesChanged:
		resp.Choices[0].Text = resp.Content
	case choicesChanged:
		resp.Content = resp.Choices[0].Text
	}
}

func extractModelFromBody(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	var partial struct {
		Model string `json:"model"`
	}
	_ = json.Unmarshal(body, &partial)
	return partial.Model
}

func generateRequestID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return "req-" + hex.EncodeToString(b)
}
