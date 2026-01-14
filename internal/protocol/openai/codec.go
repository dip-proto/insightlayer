package openai

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/j/insightlayer/internal/pipeline"
)

type ChatRequest struct {
	Model       string          `json:"model"`
	Messages    []ChatMessage   `json:"messages"`
	Stream      bool            `json:"stream,omitempty"`
	Temperature *float64        `json:"temperature,omitempty"`
	MaxTokens   *int            `json:"max_tokens,omitempty"`
	TopP        *float64        `json:"top_p,omitempty"`
	Stop        []string        `json:"stop,omitempty"`
	Tools       []ChatTool      `json:"tools,omitempty"`
	ToolChoice  json.RawMessage `json:"tool_choice,omitempty"`
}

type ChatTool struct {
	Type     string       `json:"type"`
	Function ChatFunction `json:"function"`
}

type ChatFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type ChatToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function ChatFunctionCall `json:"function"`
}

type ChatFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type ChatMessage struct {
	Role       string         `json:"role"`
	Content    *string        `json:"content"`
	ToolCalls  []ChatToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
}

type ChatResponse struct {
	ID      string       `json:"id"`
	Object  string       `json:"object"`
	Created int64        `json:"created"`
	Model   string       `json:"model"`
	Choices []ChatChoice `json:"choices"`
	Usage   *ChatUsage   `json:"usage,omitempty"`
}

type ChatChoice struct {
	Index        int          `json:"index"`
	Message      *ChatMessage `json:"message,omitempty"`
	Delta        *ChatMessage `json:"delta,omitempty"`
	FinishReason *string      `json:"finish_reason"`
}

type ChatUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

func strPtr(s string) *string { return &s }

func DecodeRequest(data []byte) (*pipeline.NormalizedRequest, error) {
	var raw ChatRequest
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("decode openai request: %w", err)
	}

	var system string
	msgs := make([]pipeline.Message, 0, len(raw.Messages))
	for _, m := range raw.Messages {
		if m.Role == "system" {
			if m.Content != nil {
				system = *m.Content
			}
			continue
		}

		pm := pipeline.Message{Role: m.Role}
		if m.Content != nil {
			pm.Content = *m.Content
		}

		if len(m.ToolCalls) > 0 {
			pm.ToolCalls = make([]pipeline.ToolCall, len(m.ToolCalls))
			for i, tc := range m.ToolCalls {
				pm.ToolCalls[i] = pipeline.ToolCall{
					ID:        tc.ID,
					Name:      tc.Function.Name,
					Arguments: tc.Function.Arguments,
				}
			}
		}

		if m.Role == "tool" && m.ToolCallID != "" {
			pm.ToolCallID = m.ToolCallID
		}

		msgs = append(msgs, pm)
	}

	req := &pipeline.NormalizedRequest{
		ClientProtocol: pipeline.ProtocolOpenAI,
		EndpointKind:   pipeline.EndpointChat,
		Model:          raw.Model,
		SystemPrompt:   system,
		Messages:       msgs,
		InferenceParams: pipeline.InferenceParams{
			Temperature: raw.Temperature,
			MaxTokens:   raw.MaxTokens,
			TopP:        raw.TopP,
			Stop:        raw.Stop,
		},
		Stream: raw.Stream,
	}

	if len(raw.Tools) > 0 {
		req.Tools = make([]pipeline.ToolDefinition, len(raw.Tools))
		for i, t := range raw.Tools {
			req.Tools[i] = pipeline.ToolDefinition{
				Name:        t.Function.Name,
				Description: t.Function.Description,
				Parameters:  t.Function.Parameters,
			}
		}
	}

	if len(raw.ToolChoice) > 0 {
		req.ToolChoice = decodeOpenAIToolChoice(raw.ToolChoice)
	}

	return req, nil
}

func EncodeResponse(resp *pipeline.NormalizedResponse) ([]byte, error) {
	finish := "stop"
	if resp.FinishReason != "" {
		finish = resp.FinishReason
	}

	msg := &ChatMessage{Role: "assistant"}

	if len(resp.ToolCalls) > 0 {
		if resp.Content != "" {
			msg.Content = &resp.Content
		}
		msg.ToolCalls = make([]ChatToolCall, len(resp.ToolCalls))
		for i, tc := range resp.ToolCalls {
			msg.ToolCalls[i] = ChatToolCall{
				ID:   tc.ID,
				Type: "function",
				Function: ChatFunctionCall{
					Name:      tc.Name,
					Arguments: tc.Arguments,
				},
			}
		}
		if finish == "stop" {
			finish = "tool_calls"
		}
	} else {
		msg.Content = &resp.Content
	}

	out := ChatResponse{
		ID:      resp.ID,
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   resp.Model,
		Choices: []ChatChoice{
			{
				Index:        0,
				Message:      msg,
				FinishReason: &finish,
			},
		},
		Usage: &ChatUsage{
			PromptTokens:     resp.Usage.PromptTokens,
			CompletionTokens: resp.Usage.CompletionTokens,
			TotalTokens:      resp.Usage.TotalTokens,
		},
	}
	return json.Marshal(out)
}

func EncodeRequest(req *pipeline.NormalizedRequest) ([]byte, error) {
	msgs := make([]ChatMessage, 0, len(req.Messages)+1)
	if req.SystemPrompt != "" {
		msgs = append(msgs, ChatMessage{Role: "system", Content: strPtr(req.SystemPrompt)})
	}
	for _, m := range req.Messages {
		cm := ChatMessage{Role: m.Role}

		switch {
		case m.Role == "tool" && m.ToolCallID != "":
			cm.Content = &m.Content
			cm.ToolCallID = m.ToolCallID
		case len(m.ToolCalls) > 0:
			if m.Content != "" {
				cm.Content = &m.Content
			}
			cm.ToolCalls = make([]ChatToolCall, len(m.ToolCalls))
			for i, tc := range m.ToolCalls {
				cm.ToolCalls[i] = ChatToolCall{
					ID:   tc.ID,
					Type: "function",
					Function: ChatFunctionCall{
						Name:      tc.Name,
						Arguments: tc.Arguments,
					},
				}
			}
		default:
			cm.Content = &m.Content
		}

		msgs = append(msgs, cm)
	}

	out := ChatRequest{
		Model:       req.Model,
		Messages:    msgs,
		Stream:      req.Stream,
		Temperature: req.InferenceParams.Temperature,
		MaxTokens:   req.InferenceParams.MaxTokens,
		TopP:        req.InferenceParams.TopP,
		Stop:        req.InferenceParams.Stop,
	}

	if len(req.Tools) > 0 {
		out.Tools = make([]ChatTool, len(req.Tools))
		for i, t := range req.Tools {
			out.Tools[i] = ChatTool{
				Type: "function",
				Function: ChatFunction{
					Name:        t.Name,
					Description: t.Description,
					Parameters:  t.Parameters,
				},
			}
		}
	}

	if req.ToolChoice != nil {
		out.ToolChoice = encodeOpenAIToolChoice(req.ToolChoice)
	}

	return json.Marshal(out)
}

func DecodeResponse(data []byte) (*pipeline.NormalizedResponse, error) {
	var raw ChatResponse
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("decode openai response: %w", err)
	}

	resp := &pipeline.NormalizedResponse{
		ID:    raw.ID,
		Model: raw.Model,
	}

	if len(raw.Choices) > 0 {
		c := raw.Choices[0]
		if c.Message != nil {
			if c.Message.Content != nil {
				resp.Content = *c.Message.Content
			}
			if len(c.Message.ToolCalls) > 0 {
				resp.ToolCalls = make([]pipeline.ToolCall, len(c.Message.ToolCalls))
				for i, tc := range c.Message.ToolCalls {
					resp.ToolCalls[i] = pipeline.ToolCall{
						ID:        tc.ID,
						Name:      tc.Function.Name,
						Arguments: tc.Function.Arguments,
					}
				}
			}
		}
		if c.FinishReason != nil {
			resp.FinishReason = *c.FinishReason
		}
	}

	if raw.Usage != nil {
		resp.Usage = pipeline.Usage{
			PromptTokens:     raw.Usage.PromptTokens,
			CompletionTokens: raw.Usage.CompletionTokens,
			TotalTokens:      raw.Usage.TotalTokens,
		}
	}

	return resp, nil
}

func decodeOpenAIToolChoice(raw json.RawMessage) *pipeline.ToolChoice {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		switch s {
		case "auto":
			return &pipeline.ToolChoice{Mode: "auto"}
		case "none":
			return &pipeline.ToolChoice{Mode: "none"}
		case "required":
			return &pipeline.ToolChoice{Mode: "required"}
		}
		return nil
	}
	var obj struct {
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	}
	if json.Unmarshal(raw, &obj) == nil && obj.Function.Name != "" {
		return &pipeline.ToolChoice{Mode: "specific", Name: obj.Function.Name}
	}
	return nil
}

func encodeOpenAIToolChoice(tc *pipeline.ToolChoice) json.RawMessage {
	switch tc.Mode {
	case "auto":
		return json.RawMessage(`"auto"`)
	case "none":
		return json.RawMessage(`"none"`)
	case "required":
		return json.RawMessage(`"required"`)
	case "specific":
		data, _ := json.Marshal(map[string]any{
			"type":     "function",
			"function": map[string]string{"name": tc.Name},
		})
		return data
	}
	return nil
}
