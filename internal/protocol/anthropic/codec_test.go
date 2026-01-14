package anthropic

import (
	"encoding/json"
	"testing"

	"github.com/j/insightlayer/internal/pipeline"
)

func TestDecodeRequest(t *testing.T) {
	input := `{
		"model": "claude-sonnet-4-6",
		"max_tokens": 256,
		"system": "You are helpful.",
		"messages": [
			{"role": "user", "content": "Hi"}
		],
		"stream": false
	}`

	req, err := DecodeRequest([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if req.Model != "claude-sonnet-4-6" {
		t.Errorf("model = %q", req.Model)
	}
	if req.SystemPrompt != "You are helpful." {
		t.Errorf("system = %q", req.SystemPrompt)
	}
	if len(req.Messages) != 1 {
		t.Fatalf("messages count = %d", len(req.Messages))
	}
	if req.Messages[0].Content != "Hi" {
		t.Errorf("content = %q", req.Messages[0].Content)
	}
	if req.ClientProtocol != pipeline.ProtocolAnthropic {
		t.Errorf("protocol = %q", req.ClientProtocol)
	}
	if *req.InferenceParams.MaxTokens != 256 {
		t.Errorf("max_tokens = %v", *req.InferenceParams.MaxTokens)
	}
}

func TestEncodeResponse(t *testing.T) {
	resp := &pipeline.NormalizedResponse{
		ID:           "msg_123",
		Model:        "claude-sonnet-4-6",
		Content:      "Hello there!",
		FinishReason: "stop",
		Usage: pipeline.Usage{
			PromptTokens:     10,
			CompletionTokens: 5,
			TotalTokens:      15,
		},
	}

	data, err := EncodeResponse(resp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var raw MessagesResponse
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("invalid json: %v", err)
	}

	if raw.ID != "msg_123" {
		t.Errorf("id = %q", raw.ID)
	}
	if raw.Type != "message" {
		t.Errorf("type = %q", raw.Type)
	}
	if len(raw.Content) != 1 || raw.Content[0].Text != "Hello there!" {
		t.Errorf("content = %+v", raw.Content)
	}
	if *raw.StopReason != "end_turn" {
		t.Errorf("stop_reason = %q (stop should map to end_turn)", *raw.StopReason)
	}
	if raw.Usage.InputTokens != 10 {
		t.Errorf("input_tokens = %d", raw.Usage.InputTokens)
	}
}

func TestDecodeResponse(t *testing.T) {
	fixture := `{
		"id": "msg_abc123",
		"type": "message",
		"role": "assistant",
		"model": "claude-sonnet-4-6",
		"content": [{"type": "text", "text": "Hello!"}],
		"stop_reason": "end_turn",
		"usage": {"input_tokens": 20, "output_tokens": 5}
	}`

	resp, err := DecodeResponse([]byte(fixture))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	if resp.Content != "Hello!" {
		t.Errorf("content = %q", resp.Content)
	}
	if resp.FinishReason != "stop" {
		t.Errorf("finish_reason = %q (end_turn should map to stop)", resp.FinishReason)
	}
	if resp.Usage.TotalTokens != 25 {
		t.Errorf("total_tokens = %d (should be input + output)", resp.Usage.TotalTokens)
	}
}

func TestRoundTrip(t *testing.T) {
	input := `{
		"model": "claude-sonnet-4-6",
		"max_tokens": 100,
		"system": "Be brief.",
		"messages": [{"role": "user", "content": "test"}]
	}`

	req, err := DecodeRequest([]byte(input))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	encoded, err := EncodeRequest(req)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	req2, err := DecodeRequest(encoded)
	if err != nil {
		t.Fatalf("decode2: %v", err)
	}

	if req2.Model != req.Model {
		t.Errorf("model mismatch: %q vs %q", req2.Model, req.Model)
	}
	if req2.SystemPrompt != req.SystemPrompt {
		t.Errorf("system mismatch: %q vs %q", req2.SystemPrompt, req.SystemPrompt)
	}
}

func TestStopReasonMapping(t *testing.T) {
	tests := []struct {
		anthropic string
		internal  string
	}{
		{"end_turn", "stop"},
		{"max_tokens", "length"},
		{"tool_use", "tool_calls"},
		{"other", "other"},
	}

	for _, tt := range tests {
		got := MapStopReason(tt.anthropic)
		if got != tt.internal {
			t.Errorf("MapStopReason(%q) = %q, want %q", tt.anthropic, got, tt.internal)
		}
	}
}

func TestFinishReasonMapping(t *testing.T) {
	tests := []struct {
		internal  string
		anthropic string
	}{
		{"stop", "end_turn"},
		{"length", "max_tokens"},
		{"tool_calls", "tool_use"},
		{"other", "other"},
	}

	for _, tt := range tests {
		got := MapFinishReason(tt.internal)
		if got != tt.anthropic {
			t.Errorf("MapFinishReason(%q) = %q, want %q", tt.internal, got, tt.anthropic)
		}
	}
}

func TestDecodeRequestWithTools(t *testing.T) {
	input := `{
		"model": "claude-sonnet-4-6",
		"max_tokens": 1024,
		"messages": [{"role": "user", "content": "What's the weather?"}],
		"tools": [{
			"name": "get_weather",
			"description": "Get current weather",
			"input_schema": {"type": "object", "properties": {"city": {"type": "string"}}, "required": ["city"]}
		}],
		"tool_choice": {"type": "auto"}
	}`

	req, err := DecodeRequest([]byte(input))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	if len(req.Tools) != 1 {
		t.Fatalf("tools count = %d, want 1", len(req.Tools))
	}
	if req.Tools[0].Name != "get_weather" {
		t.Errorf("tool name = %q", req.Tools[0].Name)
	}
	if req.ToolChoice == nil || req.ToolChoice.Mode != "auto" {
		t.Errorf("tool_choice = %+v", req.ToolChoice)
	}
}

func TestDecodeRequestArrayContent(t *testing.T) {
	input := `{
		"model": "claude-sonnet-4-6",
		"max_tokens": 1024,
		"messages": [
			{"role": "user", "content": "What's the weather?"},
			{"role": "assistant", "content": [
				{"type": "text", "text": "Let me check."},
				{"type": "tool_use", "id": "toolu_abc", "name": "get_weather", "input": {"city": "SF"}}
			]},
			{"role": "user", "content": [
				{"type": "tool_result", "tool_use_id": "toolu_abc", "content": "72F sunny"}
			]}
		]
	}`

	req, err := DecodeRequest([]byte(input))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	if len(req.Messages) != 3 {
		t.Fatalf("messages count = %d, want 3", len(req.Messages))
	}

	// Assistant with tool calls
	m := req.Messages[1]
	if m.Role != "assistant" || m.Content != "Let me check." {
		t.Errorf("assistant = %+v", m)
	}
	if len(m.ToolCalls) != 1 || m.ToolCalls[0].ID != "toolu_abc" || m.ToolCalls[0].Name != "get_weather" {
		t.Errorf("tool_calls = %+v", m.ToolCalls)
	}

	// Tool result (converted to role="tool")
	m = req.Messages[2]
	if m.Role != "tool" || m.Content != "72F sunny" || m.ToolCallID != "toolu_abc" {
		t.Errorf("tool result = %+v", m)
	}
}

func TestEncodeRequestToolResultGrouping(t *testing.T) {
	req := &pipeline.NormalizedRequest{
		Model: "claude-sonnet-4-6",
		Messages: []pipeline.Message{
			{Role: "user", Content: "Check weather in 2 cities"},
			{Role: "assistant", Content: "", ToolCalls: []pipeline.ToolCall{
				{ID: "tc1", Name: "get_weather", Arguments: `{"city":"SF"}`},
				{ID: "tc2", Name: "get_weather", Arguments: `{"city":"NYC"}`},
			}},
			{Role: "tool", Content: "72F", ToolCallID: "tc1"},
			{Role: "tool", Content: "55F", ToolCallID: "tc2"},
		},
		InferenceParams: pipeline.InferenceParams{MaxTokens: intPtr(1024)},
	}

	data, err := EncodeRequest(req)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	var raw MessagesRequest
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// User, assistant (with tool_use blocks), user (with 2 tool_result blocks)
	if len(raw.Messages) != 3 {
		t.Fatalf("messages = %d, want 3 (2 tool results grouped into 1 user msg)", len(raw.Messages))
	}

	// The grouped tool result message
	last := raw.Messages[2]
	if last.Role != "user" {
		t.Errorf("last role = %q, want user", last.Role)
	}
	_, blocks := parseMessageContent(last.Content)
	if len(blocks) != 2 {
		t.Fatalf("tool_result blocks = %d, want 2", len(blocks))
	}
	if blocks[0].Type != "tool_result" || blocks[0].ToolUseID != "tc1" {
		t.Errorf("block[0] = %+v", blocks[0])
	}
	if blocks[1].Type != "tool_result" || blocks[1].ToolUseID != "tc2" {
		t.Errorf("block[1] = %+v", blocks[1])
	}
}

func TestDecodeResponseWithToolUse(t *testing.T) {
	fixture := `{
		"id": "msg_abc",
		"type": "message",
		"role": "assistant",
		"model": "claude-sonnet-4-6",
		"content": [
			{"type": "text", "text": "Let me check."},
			{"type": "tool_use", "id": "toolu_abc", "name": "get_weather", "input": {"city": "SF"}}
		],
		"stop_reason": "tool_use",
		"usage": {"input_tokens": 20, "output_tokens": 50}
	}`

	resp, err := DecodeResponse([]byte(fixture))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	if resp.Content != "Let me check." {
		t.Errorf("content = %q", resp.Content)
	}
	if resp.FinishReason != "tool_calls" {
		t.Errorf("finish_reason = %q (tool_use should map to tool_calls)", resp.FinishReason)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("tool_calls = %d", len(resp.ToolCalls))
	}
	tc := resp.ToolCalls[0]
	if tc.ID != "toolu_abc" || tc.Name != "get_weather" {
		t.Errorf("tool_call = %+v", tc)
	}
}

func TestEncodeResponseWithToolUse(t *testing.T) {
	resp := &pipeline.NormalizedResponse{
		ID:           "msg_abc",
		Model:        "claude-sonnet-4-6",
		Content:      "Checking weather.",
		FinishReason: "tool_calls",
		ToolCalls: []pipeline.ToolCall{
			{ID: "toolu_abc", Name: "get_weather", Arguments: `{"city":"SF"}`},
		},
		Usage: pipeline.Usage{PromptTokens: 20, CompletionTokens: 50, TotalTokens: 70},
	}

	data, err := EncodeResponse(resp)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	var raw MessagesResponse
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if *raw.StopReason != "tool_use" {
		t.Errorf("stop_reason = %q, want tool_use", *raw.StopReason)
	}
	if len(raw.Content) != 2 {
		t.Fatalf("content blocks = %d, want 2", len(raw.Content))
	}
	if raw.Content[0].Type != "text" || raw.Content[0].Text != "Checking weather." {
		t.Errorf("block[0] = %+v", raw.Content[0])
	}
	if raw.Content[1].Type != "tool_use" || raw.Content[1].ID != "toolu_abc" {
		t.Errorf("block[1] = %+v", raw.Content[1])
	}
}

func intPtr(n int) *int { return &n }
