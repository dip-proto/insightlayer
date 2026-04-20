package anthropic

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/j/insightlayer/internal/pipeline"
)

type MessagesRequest struct {
	Model      string          `json:"model"`
	MaxTokens  int             `json:"max_tokens"`
	System     json.RawMessage `json:"system,omitempty"`
	Messages   []Message       `json:"messages"`
	Stream     bool            `json:"stream,omitempty"`
	Tools      []AnthropicTool `json:"tools,omitempty"`
	ToolChoice json.RawMessage `json:"tool_choice,omitempty"`
}

type AnthropicTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema,omitempty"`
}

type Message struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type ContentBlock struct {
	Type string `json:"type"`

	// text block
	Text string `json:"text,omitempty"`

	// tool_use block
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`

	// tool_result block
	ToolUseID     string `json:"tool_use_id,omitempty"`
	ResultContent string `json:"content,omitempty"`
}

type MessagesResponse struct {
	ID           string         `json:"id"`
	Type         string         `json:"type"`
	Role         string         `json:"role"`
	Model        string         `json:"model"`
	Content      []ContentBlock `json:"content"`
	StopReason   *string        `json:"stop_reason"`
	StopSequence *string        `json:"stop_sequence"`
	Usage        *MessagesUsage `json:"usage,omitempty"`
}

type MessagesUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

func marshalStringContent(s string) json.RawMessage {
	data, _ := json.Marshal(s)
	return data
}

func marshalBlocksContent(blocks []ContentBlock) json.RawMessage {
	data, _ := json.Marshal(blocks)
	return data
}

func validatedToolArguments(args string) (json.RawMessage, error) {
	raw := json.RawMessage(args)
	if !json.Valid(raw) {
		return nil, &pipeline.PipelineError{
			StatusCode: 400,
			Message:    "tool call arguments must be valid JSON",
		}
	}
	return raw, nil
}

func parseMessageContent(raw json.RawMessage) (string, []ContentBlock) {
	if len(raw) == 0 {
		return "", nil
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s, nil
	}
	var blocks []ContentBlock
	if json.Unmarshal(raw, &blocks) == nil {
		return "", blocks
	}
	return "", nil
}

func parseSystemPrompt(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []ContentBlock
	if json.Unmarshal(raw, &blocks) == nil {
		var parts []string
		for _, b := range blocks {
			if b.Type == "text" && b.Text != "" {
				parts = append(parts, b.Text)
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

func DecodeRequest(data []byte) (*pipeline.NormalizedRequest, error) {
	var raw MessagesRequest
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("decode anthropic request: %w", err)
	}

	msgs := make([]pipeline.Message, 0, len(raw.Messages))
	for _, m := range raw.Messages {
		text, blocks := parseMessageContent(m.Content)

		if len(blocks) == 0 {
			msgs = append(msgs, pipeline.Message{Role: m.Role, Content: text})
			continue
		}

		if m.Role == "assistant" {
			pm := pipeline.Message{Role: "assistant"}
			var textParts []string
			for _, block := range blocks {
				switch block.Type {
				case "text":
					textParts = append(textParts, block.Text)
				case "tool_use":
					pm.ToolCalls = append(pm.ToolCalls, pipeline.ToolCall{
						ID:        block.ID,
						Name:      block.Name,
						Arguments: string(block.Input),
					})
				}
			}
			pm.Content = strings.Join(textParts, "")
			msgs = append(msgs, pm)
			continue
		}

		if m.Role == "user" {
			var textParts []string
			for _, block := range blocks {
				switch block.Type {
				case "text":
					textParts = append(textParts, block.Text)
				case "tool_result":
					msgs = append(msgs, pipeline.Message{
						Role:       "tool",
						Content:    block.ResultContent,
						ToolCallID: block.ToolUseID,
					})
				}
			}
			if len(textParts) > 0 {
				msgs = append(msgs, pipeline.Message{Role: "user", Content: strings.Join(textParts, "")})
			}
			continue
		}
	}

	maxTokens := raw.MaxTokens
	req := &pipeline.NormalizedRequest{
		ClientProtocol: pipeline.ProtocolAnthropic,
		EndpointKind:   pipeline.EndpointChat,
		Model:          raw.Model,
		SystemPrompt:   parseSystemPrompt(raw.System),
		Messages:       msgs,
		InferenceParams: pipeline.InferenceParams{
			MaxTokens: &maxTokens,
		},
		Stream: raw.Stream,
	}

	if len(raw.Tools) > 0 {
		req.Tools = make([]pipeline.ToolDefinition, len(raw.Tools))
		for i, t := range raw.Tools {
			req.Tools[i] = pipeline.ToolDefinition{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.InputSchema,
			}
		}
	}

	if len(raw.ToolChoice) > 0 {
		toolChoice, err := decodeAnthropicToolChoice(raw.ToolChoice)
		if err != nil {
			return nil, fmt.Errorf("decode anthropic tool_choice: %w", err)
		}
		req.ToolChoice = toolChoice
	}

	return req, nil
}

func EncodeResponse(resp *pipeline.NormalizedResponse) ([]byte, error) {
	stopReason := "end_turn"
	if resp.FinishReason != "" {
		stopReason = MapFinishReason(resp.FinishReason)
	}

	var blocks []ContentBlock
	if resp.Content != "" {
		blocks = append(blocks, ContentBlock{Type: "text", Text: resp.Content})
	}
	for _, tc := range resp.ToolCalls {
		input, err := validatedToolArguments(tc.Arguments)
		if err != nil {
			return nil, err
		}
		blocks = append(blocks, ContentBlock{
			Type:  "tool_use",
			ID:    tc.ID,
			Name:  tc.Name,
			Input: input,
		})
	}
	if len(blocks) == 0 {
		blocks = []ContentBlock{{Type: "text", Text: ""}}
	}

	out := MessagesResponse{
		ID:         resp.ID,
		Type:       "message",
		Role:       "assistant",
		Model:      resp.Model,
		Content:    blocks,
		StopReason: &stopReason,
		Usage: &MessagesUsage{
			InputTokens:  resp.Usage.PromptTokens,
			OutputTokens: resp.Usage.CompletionTokens,
		},
	}
	return json.Marshal(out)
}

func EncodeRequest(req *pipeline.NormalizedRequest) ([]byte, error) {
	msgs := make([]Message, 0, len(req.Messages))

	i := 0
	for i < len(req.Messages) {
		m := req.Messages[i]

		if m.Role == "tool" && m.ToolCallID != "" {
			var resultBlocks []ContentBlock
			for i < len(req.Messages) && req.Messages[i].Role == "tool" && req.Messages[i].ToolCallID != "" {
				resultBlocks = append(resultBlocks, ContentBlock{
					Type:          "tool_result",
					ToolUseID:     req.Messages[i].ToolCallID,
					ResultContent: req.Messages[i].Content,
				})
				i++
			}
			msgs = append(msgs, Message{
				Role:    "user",
				Content: marshalBlocksContent(resultBlocks),
			})
			continue
		}

		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			var blocks []ContentBlock
			if m.Content != "" {
				blocks = append(blocks, ContentBlock{Type: "text", Text: m.Content})
			}
			for _, tc := range m.ToolCalls {
				input, err := validatedToolArguments(tc.Arguments)
				if err != nil {
					return nil, err
				}
				blocks = append(blocks, ContentBlock{
					Type:  "tool_use",
					ID:    tc.ID,
					Name:  tc.Name,
					Input: input,
				})
			}
			msgs = append(msgs, Message{
				Role:    "assistant",
				Content: marshalBlocksContent(blocks),
			})
			i++
			continue
		}

		msgs = append(msgs, Message{
			Role:    m.Role,
			Content: marshalStringContent(m.Content),
		})
		i++
	}

	maxTokens := 1024
	if req.InferenceParams.MaxTokens != nil {
		maxTokens = *req.InferenceParams.MaxTokens
	}

	var systemRaw json.RawMessage
	if req.SystemPrompt != "" {
		systemRaw, _ = json.Marshal(req.SystemPrompt)
	}

	out := MessagesRequest{
		Model:     req.Model,
		MaxTokens: maxTokens,
		System:    systemRaw,
		Messages:  msgs,
		Stream:    req.Stream,
	}

	if len(req.Tools) > 0 {
		out.Tools = make([]AnthropicTool, len(req.Tools))
		for i, t := range req.Tools {
			out.Tools[i] = AnthropicTool{
				Name:        t.Name,
				Description: t.Description,
				InputSchema: t.Parameters,
			}
		}
	}

	if req.ToolChoice != nil {
		tc, err := encodeAnthropicToolChoice(req.ToolChoice)
		if err != nil {
			return nil, err
		}
		out.ToolChoice = tc
	}

	return json.Marshal(out)
}

func DecodeResponse(data []byte) (*pipeline.NormalizedResponse, error) {
	var raw MessagesResponse
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("decode anthropic response: %w", err)
	}

	var b strings.Builder
	var toolCalls []pipeline.ToolCall

	for _, block := range raw.Content {
		switch block.Type {
		case "text":
			b.WriteString(block.Text)
		case "tool_use":
			toolCalls = append(toolCalls, pipeline.ToolCall{
				ID:        block.ID,
				Name:      block.Name,
				Arguments: string(block.Input),
			})
		}
	}

	resp := &pipeline.NormalizedResponse{
		ID:        raw.ID,
		Model:     raw.Model,
		Content:   b.String(),
		ToolCalls: toolCalls,
	}

	if raw.StopReason != nil {
		resp.FinishReason = MapStopReason(*raw.StopReason)
	}

	if raw.Usage != nil {
		resp.Usage = pipeline.Usage{
			PromptTokens:     raw.Usage.InputTokens,
			CompletionTokens: raw.Usage.OutputTokens,
			TotalTokens:      raw.Usage.InputTokens + raw.Usage.OutputTokens,
		}
	}

	return resp, nil
}

func MapFinishReason(reason string) string {
	switch reason {
	case "stop":
		return "end_turn"
	case "length":
		return "max_tokens"
	case "tool_calls":
		return "tool_use"
	default:
		return reason
	}
}

func MapStopReason(reason string) string {
	switch reason {
	case "end_turn":
		return "stop"
	case "max_tokens":
		return "length"
	case "tool_use":
		return "tool_calls"
	default:
		return reason
	}
}

func decodeAnthropicToolChoice(raw json.RawMessage) (*pipeline.ToolChoice, error) {
	var obj struct {
		Type string `json:"type"`
		Name string `json:"name,omitempty"`
	}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, err
	}
	switch obj.Type {
	case "auto":
		return &pipeline.ToolChoice{Mode: "auto"}, nil
	case "any":
		return &pipeline.ToolChoice{Mode: "required"}, nil
	case "tool":
		return &pipeline.ToolChoice{Mode: "specific", Name: obj.Name}, nil
	}
	return nil, nil
}

func encodeAnthropicToolChoice(tc *pipeline.ToolChoice) (json.RawMessage, error) {
	switch tc.Mode {
	case "auto":
		return json.RawMessage(`{"type":"auto"}`), nil
	case "none":
		return nil, &pipeline.PipelineError{
			StatusCode: 400,
			Message:    `tool_choice "none" cannot be translated to Anthropic: the Anthropic API has no equivalent for disabling tool use entirely`,
		}
	case "required":
		return json.RawMessage(`{"type":"any"}`), nil
	case "specific":
		data, err := json.Marshal(map[string]string{"type": "tool", "name": tc.Name})
		return data, err
	}
	return nil, nil
}
