package stream

import (
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/j/insightlayer/internal/pipeline"
)

type openaiChunk struct {
	ID      string         `json:"id"`
	Object  string         `json:"object"`
	Created int64          `json:"created"`
	Model   string         `json:"model"`
	Choices []openaiChoice `json:"choices"`
	Usage   *openaiUsage   `json:"usage,omitempty"`
}

type openaiChoice struct {
	Index        int          `json:"index"`
	Delta        *openaiDelta `json:"delta,omitempty"`
	FinishReason *string      `json:"finish_reason"`
}

type openaiDelta struct {
	Role      string                `json:"role,omitempty"`
	Content   string                `json:"content,omitempty"`
	ToolCalls []openaiDeltaToolCall `json:"tool_calls,omitempty"`
}

type openaiDeltaToolCall struct {
	Index    int                      `json:"index"`
	ID       string                   `json:"id,omitempty"`
	Type     string                   `json:"type,omitempty"`
	Function *openaiDeltaFunctionCall `json:"function,omitempty"`
}

type openaiDeltaFunctionCall struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

type openaiUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type OpenAIStreamDecoder struct {
	sse     *SSEReader
	started bool
	pending []*pipeline.StreamEvent
}

func NewOpenAIStreamDecoder(r io.Reader) *OpenAIStreamDecoder {
	return &OpenAIStreamDecoder{sse: NewSSEReader(r)}
}

func (d *OpenAIStreamDecoder) Next() (*pipeline.StreamEvent, error) {
	if len(d.pending) > 0 {
		ev := d.pending[0]
		d.pending = d.pending[1:]
		return ev, nil
	}

	for {
		sse, err := d.sse.Next()
		if err != nil {
			return nil, err
		}

		if sse.Data == "[DONE]" {
			return nil, io.EOF
		}

		var chunk openaiChunk
		if err := json.Unmarshal([]byte(sse.Data), &chunk); err != nil {
			return nil, fmt.Errorf("decode openai chunk: %w", err)
		}

		if !d.started {
			d.started = true
			// Queue any content or tool calls from the first chunk
			if len(chunk.Choices) > 0 && chunk.Choices[0].Delta != nil {
				delta := chunk.Choices[0].Delta
				if delta.Content != "" {
					d.pending = append(d.pending, &pipeline.StreamEvent{
						Type: pipeline.StreamEventTextDelta,
						Text: delta.Content,
					})
				}
				d.queueToolCallEvents(delta)
			}
			return &pipeline.StreamEvent{Type: pipeline.StreamEventStart}, nil
		}

		if len(chunk.Choices) == 0 {
			if chunk.Usage != nil {
				return &pipeline.StreamEvent{
					Type: pipeline.StreamEventUsage,
					Usage: &pipeline.Usage{
						PromptTokens:     chunk.Usage.PromptTokens,
						CompletionTokens: chunk.Usage.CompletionTokens,
						TotalTokens:      chunk.Usage.TotalTokens,
					},
				}, nil
			}
			continue
		}

		choice := chunk.Choices[0]

		if choice.FinishReason != nil && *choice.FinishReason != "" {
			event := &pipeline.StreamEvent{
				Type:         pipeline.StreamEventStop,
				FinishReason: *choice.FinishReason,
			}
			if chunk.Usage != nil {
				event.Usage = &pipeline.Usage{
					PromptTokens:     chunk.Usage.PromptTokens,
					CompletionTokens: chunk.Usage.CompletionTokens,
					TotalTokens:      chunk.Usage.TotalTokens,
				}
			}
			return event, nil
		}

		if choice.Delta != nil {
			if choice.Delta.Content != "" {
				return &pipeline.StreamEvent{
					Type: pipeline.StreamEventTextDelta,
					Text: choice.Delta.Content,
				}, nil
			}

			if len(choice.Delta.ToolCalls) > 0 {
				events := d.buildToolCallEvents(choice.Delta)
				if len(events) > 1 {
					d.pending = append(d.pending, events[1:]...)
				}
				if len(events) > 0 {
					return events[0], nil
				}
			}
		}
	}
}

func (d *OpenAIStreamDecoder) queueToolCallEvents(delta *openaiDelta) {
	events := d.buildToolCallEvents(delta)
	d.pending = append(d.pending, events...)
}

func (d *OpenAIStreamDecoder) buildToolCallEvents(delta *openaiDelta) []*pipeline.StreamEvent {
	var events []*pipeline.StreamEvent
	for _, tc := range delta.ToolCalls {
		if tc.ID != "" {
			events = append(events, &pipeline.StreamEvent{
				Type:          pipeline.StreamEventToolCallStart,
				ToolCallID:    tc.ID,
				ToolCallName:  tc.Function.Name,
				ToolCallIndex: tc.Index,
			})
		} else if tc.Function != nil && tc.Function.Arguments != "" {
			events = append(events, &pipeline.StreamEvent{
				Type:          pipeline.StreamEventToolCallDelta,
				Text:          tc.Function.Arguments,
				ToolCallIndex: tc.Index,
			})
		}
	}
	return events
}

type OpenAIStreamEncoder struct {
	id      string
	model   string
	created int64
}

func NewOpenAIStreamEncoder(id, model string) *OpenAIStreamEncoder {
	return &OpenAIStreamEncoder{id: id, model: model, created: time.Now().Unix()}
}

func (e *OpenAIStreamEncoder) Encode(event *pipeline.StreamEvent) (*SSEEvent, error) {
	switch event.Type {
	case pipeline.StreamEventStart:
		chunk := openaiChunk{
			ID:      e.id,
			Object:  "chat.completion.chunk",
			Created: e.created,
			Model:   e.model,
			Choices: []openaiChoice{{
				Index:        0,
				Delta:        &openaiDelta{Role: "assistant", Content: ""},
				FinishReason: nil,
			}},
		}
		return e.marshalChunk(chunk)

	case pipeline.StreamEventTextDelta:
		chunk := openaiChunk{
			ID:      e.id,
			Object:  "chat.completion.chunk",
			Created: e.created,
			Model:   e.model,
			Choices: []openaiChoice{{
				Index:        0,
				Delta:        &openaiDelta{Content: event.Text},
				FinishReason: nil,
			}},
		}
		return e.marshalChunk(chunk)

	case pipeline.StreamEventToolCallStart:
		chunk := openaiChunk{
			ID:      e.id,
			Object:  "chat.completion.chunk",
			Created: e.created,
			Model:   e.model,
			Choices: []openaiChoice{{
				Index: 0,
				Delta: &openaiDelta{
					ToolCalls: []openaiDeltaToolCall{{
						Index: event.ToolCallIndex,
						ID:    event.ToolCallID,
						Type:  "function",
						Function: &openaiDeltaFunctionCall{
							Name: event.ToolCallName,
						},
					}},
				},
				FinishReason: nil,
			}},
		}
		return e.marshalChunk(chunk)

	case pipeline.StreamEventToolCallDelta:
		chunk := openaiChunk{
			ID:      e.id,
			Object:  "chat.completion.chunk",
			Created: e.created,
			Model:   e.model,
			Choices: []openaiChoice{{
				Index: 0,
				Delta: &openaiDelta{
					ToolCalls: []openaiDeltaToolCall{{
						Index: event.ToolCallIndex,
						Function: &openaiDeltaFunctionCall{
							Arguments: event.Text,
						},
					}},
				},
				FinishReason: nil,
			}},
		}
		return e.marshalChunk(chunk)

	case pipeline.StreamEventStop:
		reason := "stop"
		if event.FinishReason != "" {
			reason = event.FinishReason
		}
		chunk := openaiChunk{
			ID:      e.id,
			Object:  "chat.completion.chunk",
			Created: e.created,
			Model:   e.model,
			Choices: []openaiChoice{{
				Index:        0,
				Delta:        &openaiDelta{},
				FinishReason: &reason,
			}},
		}
		if event.Usage != nil {
			chunk.Usage = &openaiUsage{
				PromptTokens:     event.Usage.PromptTokens,
				CompletionTokens: event.Usage.CompletionTokens,
				TotalTokens:      event.Usage.TotalTokens,
			}
		}
		return e.marshalChunk(chunk)

	case pipeline.StreamEventUsage:
		return nil, nil

	case pipeline.StreamEventError:
		msg := "internal error"
		if event.Error != nil {
			msg = event.Error.Error()
		}
		data, _ := json.Marshal(map[string]any{
			"error": map[string]any{
				"message": msg,
				"type":    "server_error",
			},
		})
		return &SSEEvent{Data: string(data)}, nil
	}

	return nil, nil
}

func (e *OpenAIStreamEncoder) Done() *SSEEvent {
	return &SSEEvent{Data: "[DONE]"}
}

func (e *OpenAIStreamEncoder) marshalChunk(chunk openaiChunk) (*SSEEvent, error) {
	data, err := json.Marshal(chunk)
	if err != nil {
		return nil, err
	}
	return &SSEEvent{Data: string(data)}, nil
}
