package stream

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/j/insightlayer/internal/pipeline"
	anthropiccodec "github.com/j/insightlayer/internal/protocol/anthropic"
)

type anthropicMessageStart struct {
	Type    string `json:"type"`
	Message struct {
		ID    string `json:"id"`
		Model string `json:"model"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	} `json:"message"`
}

type anthropicContentBlockStart struct {
	Type         string `json:"type"`
	Index        int    `json:"index"`
	ContentBlock struct {
		Type  string          `json:"type"`
		Text  string          `json:"text,omitempty"`
		ID    string          `json:"id,omitempty"`
		Name  string          `json:"name,omitempty"`
		Input json.RawMessage `json:"input,omitempty"`
	} `json:"content_block"`
}

type anthropicContentDelta struct {
	Type  string `json:"type"`
	Index int    `json:"index"`
	Delta struct {
		Type        string `json:"type"`
		Text        string `json:"text,omitempty"`
		PartialJSON string `json:"partial_json,omitempty"`
	} `json:"delta"`
}

type anthropicMessageDelta struct {
	Type  string `json:"type"`
	Delta struct {
		StopReason string `json:"stop_reason"`
	} `json:"delta"`
	Usage struct {
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

type AnthropicStreamDecoder struct {
	sse         *SSEReader
	inputTokens int
}

func NewAnthropicStreamDecoder(r io.Reader) *AnthropicStreamDecoder {
	return &AnthropicStreamDecoder{sse: NewSSEReader(r)}
}

func (d *AnthropicStreamDecoder) Next() (*pipeline.StreamEvent, error) {
	for {
		sse, err := d.sse.Next()
		if err != nil {
			return nil, err
		}

		switch sse.Event {
		case "message_start":
			var msg anthropicMessageStart
			if err := json.Unmarshal([]byte(sse.Data), &msg); err != nil {
				return nil, fmt.Errorf("decode message_start: %w", err)
			}
			d.inputTokens = msg.Message.Usage.InputTokens
			return &pipeline.StreamEvent{Type: pipeline.StreamEventStart}, nil

		case "content_block_start":
			var block anthropicContentBlockStart
			if err := json.Unmarshal([]byte(sse.Data), &block); err != nil {
				return nil, fmt.Errorf("decode content_block_start: %w", err)
			}
			if block.ContentBlock.Type == "tool_use" {
				return &pipeline.StreamEvent{
					Type:          pipeline.StreamEventToolCallStart,
					ToolCallID:    block.ContentBlock.ID,
					ToolCallName:  block.ContentBlock.Name,
					ToolCallIndex: block.Index,
				}, nil
			}
			continue

		case "content_block_delta":
			var delta anthropicContentDelta
			if err := json.Unmarshal([]byte(sse.Data), &delta); err != nil {
				return nil, fmt.Errorf("decode content_block_delta: %w", err)
			}
			switch delta.Delta.Type {
			case "text_delta":
				return &pipeline.StreamEvent{
					Type: pipeline.StreamEventTextDelta,
					Text: delta.Delta.Text,
				}, nil
			case "input_json_delta":
				return &pipeline.StreamEvent{
					Type:          pipeline.StreamEventToolCallDelta,
					Text:          delta.Delta.PartialJSON,
					ToolCallIndex: delta.Index,
				}, nil
			}

		case "message_delta":
			var delta anthropicMessageDelta
			if err := json.Unmarshal([]byte(sse.Data), &delta); err != nil {
				return nil, fmt.Errorf("decode message_delta: %w", err)
			}
			outputTokens := delta.Usage.OutputTokens
			return &pipeline.StreamEvent{
				Type:         pipeline.StreamEventStop,
				FinishReason: anthropiccodec.MapStopReason(delta.Delta.StopReason),
				Usage: &pipeline.Usage{
					PromptTokens:     d.inputTokens,
					CompletionTokens: outputTokens,
					TotalTokens:      d.inputTokens + outputTokens,
				},
			}, nil

		case "message_stop":
			return nil, io.EOF

		case "ping", "content_block_stop":
			continue
		}
	}
}

type AnthropicStreamEncoder struct {
	id           string
	model        string
	blockIndex   int
	hasTextBlock bool
	hasToolBlock bool
}

func NewAnthropicStreamEncoder(id, model string) *AnthropicStreamEncoder {
	return &AnthropicStreamEncoder{id: id, model: model}
}

func (e *AnthropicStreamEncoder) Encode(event *pipeline.StreamEvent) ([]SSEEvent, error) {
	switch event.Type {
	case pipeline.StreamEventStart:
		data, _ := json.Marshal(map[string]any{
			"type": "message_start",
			"message": map[string]any{
				"id":            e.id,
				"type":          "message",
				"role":          "assistant",
				"model":         e.model,
				"content":       []any{},
				"stop_reason":   nil,
				"stop_sequence": nil,
				"usage":         map[string]int{"input_tokens": 0, "output_tokens": 0},
			},
		})
		return []SSEEvent{
			{Event: "message_start", Data: string(data)},
		}, nil

	case pipeline.StreamEventTextDelta:
		var events []SSEEvent
		if !e.hasTextBlock {
			e.hasTextBlock = true
			blockStart, _ := json.Marshal(map[string]any{
				"type":          "content_block_start",
				"index":         e.blockIndex,
				"content_block": map[string]string{"type": "text", "text": ""},
			})
			events = append(events, SSEEvent{Event: "content_block_start", Data: string(blockStart)})
		}
		delta, _ := json.Marshal(map[string]any{
			"type":  "content_block_delta",
			"index": e.blockIndex,
			"delta": map[string]string{"type": "text_delta", "text": event.Text},
		})
		events = append(events, SSEEvent{Event: "content_block_delta", Data: string(delta)})
		return events, nil

	case pipeline.StreamEventToolCallStart:
		var events []SSEEvent
		if e.hasTextBlock || e.hasToolBlock {
			blockStop, _ := json.Marshal(map[string]any{
				"type":  "content_block_stop",
				"index": e.blockIndex,
			})
			events = append(events, SSEEvent{Event: "content_block_stop", Data: string(blockStop)})
			e.blockIndex++
			e.hasTextBlock = false
			e.hasToolBlock = false
		}
		blockStart, _ := json.Marshal(map[string]any{
			"type":  "content_block_start",
			"index": e.blockIndex,
			"content_block": map[string]any{
				"type":  "tool_use",
				"id":    event.ToolCallID,
				"name":  event.ToolCallName,
				"input": map[string]any{},
			},
		})
		events = append(events, SSEEvent{Event: "content_block_start", Data: string(blockStart)})
		e.hasToolBlock = true
		return events, nil

	case pipeline.StreamEventToolCallDelta:
		delta, _ := json.Marshal(map[string]any{
			"type":  "content_block_delta",
			"index": e.blockIndex,
			"delta": map[string]any{"type": "input_json_delta", "partial_json": event.Text},
		})
		return []SSEEvent{{Event: "content_block_delta", Data: string(delta)}}, nil

	case pipeline.StreamEventStop:
		stopReason := anthropiccodec.MapFinishReason(event.FinishReason)
		var events []SSEEvent

		if e.hasTextBlock || e.hasToolBlock {
			blockStop, _ := json.Marshal(map[string]any{
				"type":  "content_block_stop",
				"index": e.blockIndex,
			})
			events = append(events, SSEEvent{Event: "content_block_stop", Data: string(blockStop)})
		}

		usage := map[string]int{"output_tokens": 0}
		if event.Usage != nil {
			usage["output_tokens"] = event.Usage.CompletionTokens
		}
		msgDelta, _ := json.Marshal(map[string]any{
			"type":  "message_delta",
			"delta": map[string]any{"stop_reason": stopReason, "stop_sequence": nil},
			"usage": usage,
		})
		events = append(events, SSEEvent{Event: "message_delta", Data: string(msgDelta)})

		stop, _ := json.Marshal(map[string]string{"type": "message_stop"})
		events = append(events, SSEEvent{Event: "message_stop", Data: string(stop)})

		return events, nil

	case pipeline.StreamEventUsage:
		return nil, nil

	case pipeline.StreamEventError:
		msg := "internal error"
		if event.Error != nil {
			msg = event.Error.Error()
		}
		data, _ := json.Marshal(map[string]any{
			"type": "error",
			"error": map[string]any{
				"type":    "server_error",
				"message": msg,
			},
		})
		return []SSEEvent{{Event: "error", Data: string(data)}}, nil
	}

	return nil, nil
}
