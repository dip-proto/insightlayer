package pipeline

import (
	"encoding/json"
	"net/http"
)

type Protocol string

const (
	ProtocolOpenAI    Protocol = "openai"
	ProtocolAnthropic Protocol = "anthropic"
)

type EndpointKind string

const (
	EndpointChat       EndpointKind = "chat"
	EndpointCompletion EndpointKind = "completion"
	EndpointEmbedding  EndpointKind = "embedding"
	EndpointModelList  EndpointKind = "model_list"
)

func OpenAIEndpointPath(kind EndpointKind) string {
	switch kind {
	case EndpointChat:
		return "/v1/chat/completions"
	case EndpointCompletion:
		return "/v1/completions"
	case EndpointEmbedding:
		return "/v1/embeddings"
	case EndpointModelList:
		return "/v1/models"
	default:
		return "/v1/chat/completions"
	}
}

type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type ToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type ToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type ToolChoice struct {
	Mode string `json:"mode"`
	Name string `json:"name,omitempty"`
}

type InferenceParams struct {
	Temperature *float64 `json:"temperature,omitempty"`
	MaxTokens   *int     `json:"max_tokens,omitempty"`
	TopP        *float64 `json:"top_p,omitempty"`
	Stop        []string `json:"stop,omitempty"`
	N           *int     `json:"n,omitempty"`
}

type NormalizedRequest struct {
	ID              string
	ClientProtocol  Protocol
	EndpointKind    EndpointKind
	Model           string
	SystemPrompt    string
	Messages        []Message
	InferenceParams InferenceParams
	Stream          bool
	Headers         http.Header
	Tools           []ToolDefinition
	ToolChoice      *ToolChoice

	// Set instead of Messages when the prompt cannot be decoded to
	// strings (token-ID arrays). Takes priority over Messages on encode.
	RawPrompt []byte
}

type Choice struct {
	Text         string
	FinishReason string
}

type NormalizedResponse struct {
	ID           string
	Model        string
	Content      string
	FinishReason string
	Usage        Usage
	Headers      http.Header
	ToolCalls    []ToolCall

	// Set for multi-choice responses (completions n > 1).
	// Content mirrors Choices[0].Text; both are synced after hooks run.
	Choices []Choice
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type StreamEventType string

const (
	StreamEventStart         StreamEventType = "stream_start"
	StreamEventTextDelta     StreamEventType = "text_delta"
	StreamEventUsage         StreamEventType = "usage"
	StreamEventStop          StreamEventType = "stop"
	StreamEventError         StreamEventType = "error"
	StreamEventToolCallStart StreamEventType = "tool_call_start"
	StreamEventToolCallDelta StreamEventType = "tool_call_delta"
)

type StreamEvent struct {
	Type          StreamEventType
	Text          string
	FinishReason  string
	Usage         *Usage
	Error         error
	ToolCallID    string
	ToolCallName  string
	ToolCallIndex int
}

type PipelineError struct {
	Cause      error
	StatusCode int
	Message    string
	Headers    http.Header
	Extensions map[string]any
}

func (e *PipelineError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	if e.Cause != nil {
		return e.Cause.Error()
	}
	return "pipeline error"
}

func (e *PipelineError) Unwrap() error {
	return e.Cause
}
