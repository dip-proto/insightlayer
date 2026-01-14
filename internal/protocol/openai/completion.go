package openai

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/j/insightlayer/internal/pipeline"
)

type CompletionRequest struct {
	Model       string          `json:"model"`
	Prompt      json.RawMessage `json:"prompt,omitempty"`
	Stream      bool            `json:"stream,omitempty"`
	MaxTokens   *int            `json:"max_tokens,omitempty"`
	Temperature *float64        `json:"temperature,omitempty"`
	TopP        *float64        `json:"top_p,omitempty"`
	Stop        json.RawMessage `json:"stop,omitempty"`
	N           *int            `json:"n,omitempty"`
}

type CompletionResponse struct {
	ID      string             `json:"id"`
	Object  string             `json:"object"`
	Created int64              `json:"created"`
	Model   string             `json:"model"`
	Choices []CompletionChoice `json:"choices"`
	Usage   *ChatUsage         `json:"usage,omitempty"`
}

type CompletionChoice struct {
	Index        int     `json:"index"`
	Text         string  `json:"text"`
	FinishReason *string `json:"finish_reason"`
}

func DecodeCompletionRequest(data []byte) (*pipeline.NormalizedRequest, error) {
	var raw CompletionRequest
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("decode openai completion request: %w", err)
	}

	req := &pipeline.NormalizedRequest{
		ClientProtocol: pipeline.ProtocolOpenAI,
		EndpointKind:   pipeline.EndpointCompletion,
		Model:          raw.Model,
		InferenceParams: pipeline.InferenceParams{
			Temperature: raw.Temperature,
			MaxTokens:   raw.MaxTokens,
			TopP:        raw.TopP,
			N:           raw.N,
		},
		Stream: raw.Stream,
	}

	if len(raw.Stop) > 0 {
		var stopStr string
		if json.Unmarshal(raw.Stop, &stopStr) == nil {
			req.InferenceParams.Stop = []string{stopStr}
		} else {
			var stopArr []string
			if json.Unmarshal(raw.Stop, &stopArr) == nil {
				req.InferenceParams.Stop = stopArr
			}
		}
	}

	req.Messages, req.RawPrompt = decodePrompt(raw.Prompt)

	return req, nil
}

// decodePrompt attempts to decode the prompt into string Messages.
// If the prompt is a string, it becomes a single message.
// If the prompt is an array of strings, each becomes a message.
// If the prompt is anything else (token arrays, nested arrays),
// the raw bytes are returned for lossless round-trip.
func decodePrompt(raw json.RawMessage) ([]pipeline.Message, []byte) {
	if len(raw) == 0 {
		return nil, nil
	}

	var s string
	if json.Unmarshal(raw, &s) == nil {
		return []pipeline.Message{{Role: "user", Content: s}}, nil
	}

	var arr []json.RawMessage
	if json.Unmarshal(raw, &arr) != nil {
		return nil, raw
	}

	var messages []pipeline.Message
	for _, elem := range arr {
		var str string
		if json.Unmarshal(elem, &str) == nil {
			messages = append(messages, pipeline.Message{Role: "user", Content: str})
		} else {
			// Mixed or non-string array (token IDs). Preserve raw.
			return nil, raw
		}
	}
	return messages, nil
}

func EncodeCompletionRequest(req *pipeline.NormalizedRequest) ([]byte, error) {
	out := CompletionRequest{
		Model:       req.Model,
		Stream:      req.Stream,
		Temperature: req.InferenceParams.Temperature,
		MaxTokens:   req.InferenceParams.MaxTokens,
		TopP:        req.InferenceParams.TopP,
		N:           req.InferenceParams.N,
	}

	if len(req.InferenceParams.Stop) > 0 {
		out.Stop, _ = json.Marshal(req.InferenceParams.Stop)
	}

	if req.RawPrompt != nil {
		out.Prompt = req.RawPrompt
	} else if len(req.Messages) > 0 {
		out.Prompt = encodePrompt(req.Messages)
	}

	return json.Marshal(out)
}

func encodePrompt(messages []pipeline.Message) json.RawMessage {
	if len(messages) == 1 {
		data, _ := json.Marshal(messages[0].Content)
		return data
	}
	strs := make([]string, len(messages))
	for i, m := range messages {
		strs[i] = m.Content
	}
	data, _ := json.Marshal(strs)
	return data
}

func DecodeCompletionResponse(data []byte) (*pipeline.NormalizedResponse, error) {
	var raw CompletionResponse
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("decode openai completion response: %w", err)
	}

	resp := &pipeline.NormalizedResponse{
		ID:    raw.ID,
		Model: raw.Model,
	}

	for _, c := range raw.Choices {
		fr := ""
		if c.FinishReason != nil {
			fr = *c.FinishReason
		}
		resp.Choices = append(resp.Choices, pipeline.Choice{
			Text:         c.Text,
			FinishReason: fr,
		})
	}

	if len(resp.Choices) > 0 {
		resp.Content = resp.Choices[0].Text
		resp.FinishReason = resp.Choices[0].FinishReason
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

func EncodeCompletionResponse(resp *pipeline.NormalizedResponse) ([]byte, error) {
	choices := make([]CompletionChoice, 0, len(resp.Choices))
	if len(resp.Choices) > 0 {
		for i, c := range resp.Choices {
			fr := c.FinishReason
			if fr == "" {
				fr = "stop"
			}
			choices = append(choices, CompletionChoice{
				Index:        i,
				Text:         c.Text,
				FinishReason: &fr,
			})
		}
	} else {
		finish := "stop"
		if resp.FinishReason != "" {
			finish = resp.FinishReason
		}
		choices = append(choices, CompletionChoice{
			Index:        0,
			Text:         resp.Content,
			FinishReason: &finish,
		})
	}

	out := CompletionResponse{
		ID:      resp.ID,
		Object:  "text_completion",
		Created: time.Now().Unix(),
		Model:   resp.Model,
		Choices: choices,
	}
	if resp.Usage.TotalTokens > 0 {
		out.Usage = &ChatUsage{
			PromptTokens:     resp.Usage.PromptTokens,
			CompletionTokens: resp.Usage.CompletionTokens,
			TotalTokens:      resp.Usage.TotalTokens,
		}
	}
	return json.Marshal(out)
}
