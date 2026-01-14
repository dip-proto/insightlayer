package openai

import (
	"encoding/json"
	"testing"

	"github.com/j/insightlayer/internal/pipeline"
)

func TestDecodeCompletionRequestStringPrompt(t *testing.T) {
	input := `{
		"model": "gpt-5.4",
		"prompt": "Once upon a time",
		"max_tokens": 50,
		"temperature": 0.8
	}`

	req, err := DecodeCompletionRequest([]byte(input))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	if req.Model != "gpt-5.4" {
		t.Errorf("model = %q", req.Model)
	}
	if req.EndpointKind != pipeline.EndpointCompletion {
		t.Errorf("kind = %q", req.EndpointKind)
	}
	if len(req.Messages) != 1 {
		t.Fatalf("messages count = %d, want 1", len(req.Messages))
	}
	if req.Messages[0].Content != "Once upon a time" {
		t.Errorf("content = %q", req.Messages[0].Content)
	}
	if req.RawPrompt != nil {
		t.Error("string prompt should not set RawPrompt")
	}
}

func TestDecodeCompletionRequestStringArrayPrompt(t *testing.T) {
	input := `{
		"model": "gpt-5.4",
		"prompt": ["Hello", "World", "Foo"]
	}`

	req, err := DecodeCompletionRequest([]byte(input))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	if len(req.Messages) != 3 {
		t.Fatalf("messages count = %d, want 3", len(req.Messages))
	}
	if req.Messages[0].Content != "Hello" {
		t.Errorf("messages[0] = %q", req.Messages[0].Content)
	}
	if req.Messages[1].Content != "World" {
		t.Errorf("messages[1] = %q", req.Messages[1].Content)
	}
	if req.Messages[2].Content != "Foo" {
		t.Errorf("messages[2] = %q", req.Messages[2].Content)
	}
	if req.RawPrompt != nil {
		t.Error("string array prompt should not set RawPrompt")
	}
}

func TestDecodeCompletionRequestTokenArrayPrompt(t *testing.T) {
	input := `{
		"model": "gpt-5.4",
		"prompt": [1234, 5678, 9012]
	}`

	req, err := DecodeCompletionRequest([]byte(input))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	if len(req.Messages) != 0 {
		t.Errorf("token prompts should not produce messages, got %d", len(req.Messages))
	}
	if req.RawPrompt == nil {
		t.Fatal("token prompt should set RawPrompt")
	}

	// Round-trip: encode should preserve the token array
	data, err := EncodeCompletionRequest(req)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	var raw map[string]json.RawMessage
	_ = json.Unmarshal(data, &raw)

	var tokens []int
	if err := json.Unmarshal(raw["prompt"], &tokens); err != nil {
		t.Fatalf("prompt should be a token array: %v", err)
	}
	if len(tokens) != 3 || tokens[0] != 1234 {
		t.Errorf("tokens = %v", tokens)
	}
}

func TestDecodeCompletionRequestNestedTokenArrayPrompt(t *testing.T) {
	input := `{
		"model": "gpt-5.4",
		"prompt": [[1234, 5678], [9012]]
	}`

	req, err := DecodeCompletionRequest([]byte(input))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	if len(req.Messages) != 0 {
		t.Errorf("nested token prompts should not produce messages")
	}
	if req.RawPrompt == nil {
		t.Fatal("nested token prompt should set RawPrompt")
	}
}

func TestEncodeCompletionRequestSingleString(t *testing.T) {
	req := &pipeline.NormalizedRequest{
		Model: "gpt-5.4",
		Messages: []pipeline.Message{
			{Role: "user", Content: "Hello world"},
		},
	}

	data, err := EncodeCompletionRequest(req)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	var raw CompletionRequest
	_ = json.Unmarshal(data, &raw)

	var prompt string
	if err := json.Unmarshal(raw.Prompt, &prompt); err != nil {
		t.Fatalf("prompt should be a string: %v", err)
	}
	if prompt != "Hello world" {
		t.Errorf("prompt = %q", prompt)
	}
}

func TestEncodeCompletionRequestMultipleStrings(t *testing.T) {
	req := &pipeline.NormalizedRequest{
		Model: "gpt-5.4",
		Messages: []pipeline.Message{
			{Role: "user", Content: "Hello"},
			{Role: "user", Content: "World"},
		},
	}

	data, err := EncodeCompletionRequest(req)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	var raw CompletionRequest
	_ = json.Unmarshal(data, &raw)

	var prompts []string
	if err := json.Unmarshal(raw.Prompt, &prompts); err != nil {
		t.Fatalf("prompt should be a string array: %v", err)
	}
	if len(prompts) != 2 || prompts[0] != "Hello" || prompts[1] != "World" {
		t.Errorf("prompts = %v", prompts)
	}
}

func TestDecodeCompletionResponseMultiChoice(t *testing.T) {
	input := `{
		"id": "cmpl-abc123",
		"object": "text_completion",
		"model": "gpt-5.4",
		"choices": [
			{"index": 0, "text": "first choice", "finish_reason": "stop"},
			{"index": 1, "text": "second choice", "finish_reason": "stop"},
			{"index": 2, "text": "third choice", "finish_reason": "length"}
		],
		"usage": {"prompt_tokens": 5, "completion_tokens": 15, "total_tokens": 20}
	}`

	resp, err := DecodeCompletionResponse([]byte(input))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	if len(resp.Choices) != 3 {
		t.Fatalf("choices count = %d, want 3", len(resp.Choices))
	}
	if resp.Content != "first choice" {
		t.Errorf("Content (first choice) = %q", resp.Content)
	}
	if resp.Choices[1].Text != "second choice" {
		t.Errorf("choices[1] = %q", resp.Choices[1].Text)
	}
	if resp.Choices[2].FinishReason != "length" {
		t.Errorf("choices[2] finish = %q", resp.Choices[2].FinishReason)
	}
}

func TestEncodeCompletionResponseMultiChoice(t *testing.T) {
	resp := &pipeline.NormalizedResponse{
		ID:    "cmpl-123",
		Model: "gpt-5.4",
		Choices: []pipeline.Choice{
			{Text: "first", FinishReason: "stop"},
			{Text: "second", FinishReason: "stop"},
		},
	}

	data, err := EncodeCompletionResponse(resp)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	var raw CompletionResponse
	_ = json.Unmarshal(data, &raw)

	if len(raw.Choices) != 2 {
		t.Fatalf("choices = %d", len(raw.Choices))
	}
	if raw.Choices[0].Text != "first" || raw.Choices[1].Text != "second" {
		t.Errorf("choices = %+v", raw.Choices)
	}
}

func TestCompletionRoundTrip(t *testing.T) {
	input := `{
		"model": "gpt-5.4",
		"prompt": "test prompt",
		"max_tokens": 20
	}`

	req, err := DecodeCompletionRequest([]byte(input))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	encoded, err := EncodeCompletionRequest(req)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	req2, err := DecodeCompletionRequest(encoded)
	if err != nil {
		t.Fatalf("decode2: %v", err)
	}

	if req2.Model != req.Model {
		t.Errorf("model mismatch")
	}
	if len(req2.Messages) != 1 || req2.Messages[0].Content != "test prompt" {
		t.Errorf("prompt mismatch: %v", req2.Messages)
	}
}

func TestCompletionNParamRoundTrip(t *testing.T) {
	input := `{
		"model": "gpt-5.4",
		"prompt": "test",
		"n": 3,
		"max_tokens": 50
	}`

	req, err := DecodeCompletionRequest([]byte(input))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	if req.InferenceParams.N == nil || *req.InferenceParams.N != 3 {
		t.Fatalf("n should be 3, got %v", req.InferenceParams.N)
	}

	encoded, err := EncodeCompletionRequest(req)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	var raw map[string]any
	_ = json.Unmarshal(encoded, &raw)
	if n, ok := raw["n"].(float64); !ok || int(n) != 3 {
		t.Errorf("encoded n = %v, want 3", raw["n"])
	}
}

func TestCompletionNParamOmittedWhenNil(t *testing.T) {
	input := `{"model": "gpt-5.4", "prompt": "test"}`

	req, err := DecodeCompletionRequest([]byte(input))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	if req.InferenceParams.N != nil {
		t.Fatalf("n should be nil when omitted, got %v", *req.InferenceParams.N)
	}

	encoded, err := EncodeCompletionRequest(req)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	var raw map[string]any
	_ = json.Unmarshal(encoded, &raw)
	if _, exists := raw["n"]; exists {
		t.Errorf("n should be omitted from encoded output, got %v", raw["n"])
	}
}

func TestDecodeCompletionRequestNoPrompt(t *testing.T) {
	input := `{"model": "gpt-5.4"}`

	req, err := DecodeCompletionRequest([]byte(input))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	if len(req.Messages) != 0 {
		t.Errorf("no prompt should produce 0 messages, got %d", len(req.Messages))
	}
	if req.RawPrompt != nil {
		t.Error("no prompt should not set RawPrompt")
	}

	encoded, err := EncodeCompletionRequest(req)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	var raw map[string]json.RawMessage
	_ = json.Unmarshal(encoded, &raw)
	if _, exists := raw["prompt"]; exists {
		t.Errorf("omitted prompt should stay omitted, got prompt=%s", raw["prompt"])
	}
}
