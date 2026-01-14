package openai

import (
	"encoding/json"
	"testing"

	"github.com/j/insightlayer/internal/pipeline"
)

func TestDecodeRequest(t *testing.T) {
	input := `{
		"model": "gpt-5.4",
		"messages": [
			{"role": "system", "content": "You are helpful."},
			{"role": "user", "content": "Hi"}
		],
		"stream": true,
		"temperature": 0.7,
		"max_tokens": 100
	}`

	req, err := DecodeRequest([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if req.Model != "gpt-5.4" {
		t.Errorf("model = %q, want %q", req.Model, "gpt-5.4")
	}
	if req.SystemPrompt != "You are helpful." {
		t.Errorf("system = %q", req.SystemPrompt)
	}
	if len(req.Messages) != 1 {
		t.Fatalf("messages count = %d, want 1 (system extracted)", len(req.Messages))
	}
	if req.Messages[0].Role != "user" || req.Messages[0].Content != "Hi" {
		t.Errorf("message = %+v", req.Messages[0])
	}
	if !req.Stream {
		t.Error("stream should be true")
	}
	if req.ClientProtocol != pipeline.ProtocolOpenAI {
		t.Errorf("protocol = %q", req.ClientProtocol)
	}
	if *req.InferenceParams.Temperature != 0.7 {
		t.Errorf("temperature = %v", *req.InferenceParams.Temperature)
	}
	if *req.InferenceParams.MaxTokens != 100 {
		t.Errorf("max_tokens = %v", *req.InferenceParams.MaxTokens)
	}
}

func TestEncodeResponse(t *testing.T) {
	resp := &pipeline.NormalizedResponse{
		ID:           "chatcmpl-123",
		Model:        "gpt-5.4",
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

	var raw ChatResponse
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("invalid json: %v", err)
	}

	if raw.ID != "chatcmpl-123" {
		t.Errorf("id = %q", raw.ID)
	}
	if raw.Object != "chat.completion" {
		t.Errorf("object = %q", raw.Object)
	}
	if len(raw.Choices) != 1 {
		t.Fatalf("choices count = %d", len(raw.Choices))
	}
	if raw.Choices[0].Message.Content == nil || *raw.Choices[0].Message.Content != "Hello there!" {
		t.Errorf("content = %v", raw.Choices[0].Message.Content)
	}
	if *raw.Choices[0].FinishReason != "stop" {
		t.Errorf("finish_reason = %q", *raw.Choices[0].FinishReason)
	}
}

func TestRoundTrip(t *testing.T) {
	input := `{
		"model": "gpt-5.4",
		"messages": [
			{"role": "user", "content": "test"}
		],
		"max_tokens": 50
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
	if len(req2.Messages) != len(req.Messages) {
		t.Errorf("message count mismatch: %d vs %d", len(req2.Messages), len(req.Messages))
	}
}

func TestDecodeResponseFromFixture(t *testing.T) {
	fixture := `{
		"id": "chatcmpl-abc123",
		"object": "chat.completion",
		"created": 1700000000,
		"model": "gpt-5.4",
		"choices": [{
			"index": 0,
			"message": {"role": "assistant", "content": "Hello!"},
			"finish_reason": "stop"
		}],
		"usage": {"prompt_tokens": 20, "completion_tokens": 5, "total_tokens": 25}
	}`

	resp, err := DecodeResponse([]byte(fixture))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	if resp.Content != "Hello!" {
		t.Errorf("content = %q", resp.Content)
	}
	if resp.FinishReason != "stop" {
		t.Errorf("finish_reason = %q", resp.FinishReason)
	}
	if resp.Usage.TotalTokens != 25 {
		t.Errorf("total_tokens = %d", resp.Usage.TotalTokens)
	}
}

func TestDecodeRequestWithTools(t *testing.T) {
	input := `{
		"model": "gpt-5.4",
		"messages": [{"role": "user", "content": "What's the weather?"}],
		"tools": [{
			"type": "function",
			"function": {
				"name": "get_weather",
				"description": "Get current weather",
				"parameters": {"type": "object", "properties": {"city": {"type": "string"}}, "required": ["city"]}
			}
		}],
		"tool_choice": "auto"
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
	if req.Tools[0].Description != "Get current weather" {
		t.Errorf("tool description = %q", req.Tools[0].Description)
	}
	if req.ToolChoice == nil || req.ToolChoice.Mode != "auto" {
		t.Errorf("tool_choice = %+v", req.ToolChoice)
	}
}

func TestDecodeRequestWithToolCallHistory(t *testing.T) {
	input := `{
		"model": "gpt-5.4",
		"messages": [
			{"role": "user", "content": "What's the weather?"},
			{"role": "assistant", "content": "Let me check.", "tool_calls": [
				{"id": "call_abc", "type": "function", "function": {"name": "get_weather", "arguments": "{\"city\":\"SF\"}"}}
			]},
			{"role": "tool", "content": "72F sunny", "tool_call_id": "call_abc"},
			{"role": "assistant", "content": "It's 72F and sunny in SF."}
		]
	}`

	req, err := DecodeRequest([]byte(input))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	if len(req.Messages) != 4 {
		t.Fatalf("messages count = %d, want 4", len(req.Messages))
	}

	m := req.Messages[1]
	if m.Role != "assistant" || m.Content != "Let me check." {
		t.Errorf("assistant message = %+v", m)
	}
	if len(m.ToolCalls) != 1 {
		t.Fatalf("tool_calls count = %d", len(m.ToolCalls))
	}
	if m.ToolCalls[0].ID != "call_abc" || m.ToolCalls[0].Name != "get_weather" {
		t.Errorf("tool_call = %+v", m.ToolCalls[0])
	}

	m = req.Messages[2]
	if m.Role != "tool" || m.Content != "72F sunny" || m.ToolCallID != "call_abc" {
		t.Errorf("tool message = %+v", m)
	}
}

func TestEncodeResponseWithToolCalls(t *testing.T) {
	resp := &pipeline.NormalizedResponse{
		ID:    "chatcmpl-123",
		Model: "gpt-5.4",
		ToolCalls: []pipeline.ToolCall{
			{ID: "call_abc", Name: "get_weather", Arguments: `{"city":"SF"}`},
		},
		Usage: pipeline.Usage{PromptTokens: 10, CompletionTokens: 20, TotalTokens: 30},
	}

	data, err := EncodeResponse(resp)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	var raw ChatResponse
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("invalid json: %v", err)
	}

	if *raw.Choices[0].FinishReason != "tool_calls" {
		t.Errorf("finish_reason = %q, want tool_calls", *raw.Choices[0].FinishReason)
	}
	if raw.Choices[0].Message.Content != nil {
		t.Errorf("content should be nil for tool-call-only response, got %v", raw.Choices[0].Message.Content)
	}
	if len(raw.Choices[0].Message.ToolCalls) != 1 {
		t.Fatalf("tool_calls count = %d", len(raw.Choices[0].Message.ToolCalls))
	}
	tc := raw.Choices[0].Message.ToolCalls[0]
	if tc.ID != "call_abc" || tc.Type != "function" || tc.Function.Name != "get_weather" {
		t.Errorf("tool_call = %+v", tc)
	}
}

func TestDecodeResponseWithToolCalls(t *testing.T) {
	fixture := `{
		"id": "chatcmpl-abc",
		"object": "chat.completion",
		"created": 1700000000,
		"model": "gpt-5.4",
		"choices": [{
			"index": 0,
			"message": {
				"role": "assistant",
				"content": null,
				"tool_calls": [{
					"id": "call_abc",
					"type": "function",
					"function": {"name": "get_weather", "arguments": "{\"city\":\"SF\"}"}
				}]
			},
			"finish_reason": "tool_calls"
		}],
		"usage": {"prompt_tokens": 10, "completion_tokens": 20, "total_tokens": 30}
	}`

	resp, err := DecodeResponse([]byte(fixture))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	if resp.Content != "" {
		t.Errorf("content = %q, want empty", resp.Content)
	}
	if resp.FinishReason != "tool_calls" {
		t.Errorf("finish_reason = %q", resp.FinishReason)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("tool_calls count = %d", len(resp.ToolCalls))
	}
	if resp.ToolCalls[0].Name != "get_weather" {
		t.Errorf("tool_call name = %q", resp.ToolCalls[0].Name)
	}
}

func TestEncodeRequestWithTools(t *testing.T) {
	req := &pipeline.NormalizedRequest{
		Model: "gpt-5.4",
		Messages: []pipeline.Message{
			{Role: "user", Content: "What's the weather?"},
		},
		Tools: []pipeline.ToolDefinition{
			{
				Name:        "get_weather",
				Description: "Get current weather",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}}}`),
			},
		},
		ToolChoice: &pipeline.ToolChoice{Mode: "auto"},
	}

	data, err := EncodeRequest(req)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	var raw ChatRequest
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("invalid json: %v", err)
	}

	if len(raw.Tools) != 1 {
		t.Fatalf("tools count = %d", len(raw.Tools))
	}
	if raw.Tools[0].Function.Name != "get_weather" {
		t.Errorf("tool name = %q", raw.Tools[0].Function.Name)
	}

	var tc string
	if err := json.Unmarshal(raw.ToolChoice, &tc); err != nil {
		t.Fatalf("unmarshal tool_choice: %v", err)
	}
	if tc != "auto" {
		t.Errorf("tool_choice = %q", string(raw.ToolChoice))
	}
}
