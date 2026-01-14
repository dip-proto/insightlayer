package stream

import (
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/j/insightlayer/internal/pipeline"
)

func TestAnthropicStreamDecoder(t *testing.T) {
	input := `event: message_start
data: {"type":"message_start","message":{"id":"msg_abc","type":"message","role":"assistant","model":"claude-sonnet-4-6","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":15,"output_tokens":0}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: ping
data: {"type":"ping"}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"!"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":2}}

event: message_stop
data: {"type":"message_stop"}

`
	decoder := NewAnthropicStreamDecoder(strings.NewReader(input))

	// stream_start
	ev, err := decoder.Next()
	if err != nil {
		t.Fatalf("event 1: %v", err)
	}
	if ev.Type != pipeline.StreamEventStart {
		t.Errorf("event 1 type = %q, want stream_start", ev.Type)
	}

	// text_delta "Hello"
	ev, err = decoder.Next()
	if err != nil {
		t.Fatalf("event 2: %v", err)
	}
	if ev.Type != pipeline.StreamEventTextDelta || ev.Text != "Hello" {
		t.Errorf("event 2 = %+v", ev)
	}

	// text_delta "!"
	ev, err = decoder.Next()
	if err != nil {
		t.Fatalf("event 3: %v", err)
	}
	if ev.Type != pipeline.StreamEventTextDelta || ev.Text != "!" {
		t.Errorf("event 3 = %+v", ev)
	}

	// stop with usage
	ev, err = decoder.Next()
	if err != nil {
		t.Fatalf("event 4: %v", err)
	}
	if ev.Type != pipeline.StreamEventStop {
		t.Errorf("event 4 type = %q, want stop", ev.Type)
	}
	if ev.FinishReason != "stop" {
		t.Errorf("finish_reason = %q (end_turn should map to stop)", ev.FinishReason)
	}
	if ev.Usage == nil || ev.Usage.PromptTokens != 15 || ev.Usage.CompletionTokens != 2 {
		t.Errorf("usage = %+v", ev.Usage)
	}

	// EOF after message_stop
	_, err = decoder.Next()
	if err != io.EOF {
		t.Errorf("expected EOF, got %v", err)
	}
}

func TestAnthropicStreamEncoder(t *testing.T) {
	enc := NewAnthropicStreamEncoder("msg_test", "claude-sonnet-4-6")

	// stream_start produces only message_start (content_block_start is deferred)
	events, err := enc.Encode(&pipeline.StreamEvent{Type: pipeline.StreamEventStart})
	if err != nil {
		t.Fatalf("encode start: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("start should produce 1 SSE event, got %d", len(events))
	}
	if events[0].Event != "message_start" {
		t.Errorf("first event = %q", events[0].Event)
	}

	// first text_delta produces content_block_start + content_block_delta
	events, err = enc.Encode(&pipeline.StreamEvent{Type: pipeline.StreamEventTextDelta, Text: "hi"})
	if err != nil {
		t.Fatalf("encode delta: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("first text delta should produce 2 events (block_start + delta), got %d", len(events))
	}
	if events[0].Event != "content_block_start" {
		t.Errorf("events[0] = %q", events[0].Event)
	}
	if events[1].Event != "content_block_delta" {
		t.Errorf("events[1] = %q", events[1].Event)
	}
	if !strings.Contains(events[1].Data, `"text":"hi"`) {
		t.Errorf("delta data = %s", events[1].Data)
	}

	// second text_delta produces only content_block_delta
	events, err = enc.Encode(&pipeline.StreamEvent{Type: pipeline.StreamEventTextDelta, Text: " there"})
	if err != nil {
		t.Fatalf("encode delta 2: %v", err)
	}
	if len(events) != 1 || events[0].Event != "content_block_delta" {
		t.Errorf("second delta event = %+v", events)
	}

	// stop produces content_block_stop + message_delta + message_stop
	events, err = enc.Encode(&pipeline.StreamEvent{
		Type:         pipeline.StreamEventStop,
		FinishReason: "stop",
		Usage:        &pipeline.Usage{CompletionTokens: 5},
	})
	if err != nil {
		t.Fatalf("encode stop: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("stop should produce 3 SSE events, got %d", len(events))
	}
	if events[0].Event != "content_block_stop" {
		t.Errorf("events[0] = %q", events[0].Event)
	}
	if events[1].Event != "message_delta" {
		t.Errorf("events[1] = %q", events[1].Event)
	}
	if !strings.Contains(events[1].Data, `"stop_reason":"end_turn"`) {
		t.Errorf("stop should map to end_turn, got %s", events[1].Data)
	}
	if events[2].Event != "message_stop" {
		t.Errorf("events[2] = %q", events[2].Event)
	}

	// error event produces an SSE error frame instead of returning an error
	events, err = enc.Encode(&pipeline.StreamEvent{
		Type:  pipeline.StreamEventError,
		Error: fmt.Errorf("upstream failure"),
	})
	if err != nil {
		t.Fatalf("encode error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("error should produce 1 event, got %d", len(events))
	}
	if events[0].Event != "error" {
		t.Errorf("event type = %q, want error", events[0].Event)
	}
	if !strings.Contains(events[0].Data, `"message":"upstream failure"`) {
		t.Errorf("error data should contain message, got %s", events[0].Data)
	}
}

func TestAnthropicStreamDecoderToolUse(t *testing.T) {
	input := `event: message_start
data: {"type":"message_start","message":{"id":"msg_abc","type":"message","role":"assistant","model":"claude-sonnet-4-6","content":[],"stop_reason":null,"usage":{"input_tokens":15,"output_tokens":0}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Let me check."}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: content_block_start
data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_abc","name":"get_weather","input":{}}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"city\""}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":":\"SF\"}"}}

event: content_block_stop
data: {"type":"content_block_stop","index":1}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":50}}

event: message_stop
data: {"type":"message_stop"}

`
	decoder := NewAnthropicStreamDecoder(strings.NewReader(input))

	// stream_start
	ev, err := decoder.Next()
	if err != nil {
		t.Fatalf("event 1: %v", err)
	}
	if ev.Type != pipeline.StreamEventStart {
		t.Errorf("event 1 type = %q", ev.Type)
	}

	// text_delta "Let me check."
	ev, err = decoder.Next()
	if err != nil {
		t.Fatalf("event 2: %v", err)
	}
	if ev.Type != pipeline.StreamEventTextDelta || ev.Text != "Let me check." {
		t.Errorf("event 2 = %+v", ev)
	}

	// tool_call_start
	ev, err = decoder.Next()
	if err != nil {
		t.Fatalf("event 3: %v", err)
	}
	if ev.Type != pipeline.StreamEventToolCallStart {
		t.Errorf("event 3 type = %q, want tool_call_start", ev.Type)
	}
	if ev.ToolCallID != "toolu_abc" || ev.ToolCallName != "get_weather" || ev.ToolCallIndex != 1 {
		t.Errorf("tool_call_start = id=%q name=%q index=%d", ev.ToolCallID, ev.ToolCallName, ev.ToolCallIndex)
	}

	// tool_call_delta (first fragment)
	ev, err = decoder.Next()
	if err != nil {
		t.Fatalf("event 4: %v", err)
	}
	if ev.Type != pipeline.StreamEventToolCallDelta || ev.Text != `{"city"` {
		t.Errorf("event 4 = %+v", ev)
	}

	// tool_call_delta (second fragment)
	ev, err = decoder.Next()
	if err != nil {
		t.Fatalf("event 5: %v", err)
	}
	if ev.Type != pipeline.StreamEventToolCallDelta || ev.Text != `:"SF"}` {
		t.Errorf("event 5 = %+v", ev)
	}

	// stop with tool_use reason
	ev, err = decoder.Next()
	if err != nil {
		t.Fatalf("event 6: %v", err)
	}
	if ev.Type != pipeline.StreamEventStop || ev.FinishReason != "tool_calls" {
		t.Errorf("event 6 = %+v (want stop with tool_calls)", ev)
	}

	// EOF
	_, err = decoder.Next()
	if err != io.EOF {
		t.Errorf("expected EOF, got %v", err)
	}
}

func TestAnthropicStreamEncoderToolUse(t *testing.T) {
	enc := NewAnthropicStreamEncoder("msg_test", "claude-sonnet-4-6")

	// stream_start
	events, err := enc.Encode(&pipeline.StreamEvent{Type: pipeline.StreamEventStart})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if len(events) != 1 || events[0].Event != "message_start" {
		t.Errorf("start events = %+v", events)
	}

	// text delta (opens text block)
	events, err = enc.Encode(&pipeline.StreamEvent{Type: pipeline.StreamEventTextDelta, Text: "Checking."})
	if err != nil {
		t.Fatalf("text: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("first text should produce 2 events, got %d", len(events))
	}
	if events[0].Event != "content_block_start" {
		t.Errorf("events[0] = %q", events[0].Event)
	}

	// tool_call_start (closes text block, opens tool_use block)
	events, err = enc.Encode(&pipeline.StreamEvent{
		Type:         pipeline.StreamEventToolCallStart,
		ToolCallID:   "toolu_abc",
		ToolCallName: "get_weather",
	})
	if err != nil {
		t.Fatalf("tool_call_start: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("tool_call_start should produce 2 events (block_stop + block_start), got %d", len(events))
	}
	if events[0].Event != "content_block_stop" {
		t.Errorf("events[0] = %q", events[0].Event)
	}
	if events[1].Event != "content_block_start" {
		t.Errorf("events[1] = %q", events[1].Event)
	}
	if !strings.Contains(events[1].Data, `"tool_use"`) {
		t.Errorf("block_start should contain tool_use, got %s", events[1].Data)
	}
	if !strings.Contains(events[1].Data, `"id":"toolu_abc"`) {
		t.Errorf("block_start should contain id, got %s", events[1].Data)
	}

	// tool_call_delta
	events, err = enc.Encode(&pipeline.StreamEvent{
		Type: pipeline.StreamEventToolCallDelta,
		Text: `{"city":"SF"}`,
	})
	if err != nil {
		t.Fatalf("tool_call_delta: %v", err)
	}
	if len(events) != 1 || events[0].Event != "content_block_delta" {
		t.Errorf("tool_call_delta events = %+v", events)
	}
	if !strings.Contains(events[0].Data, `"input_json_delta"`) {
		t.Errorf("delta should contain input_json_delta, got %s", events[0].Data)
	}

	// stop (closes tool block)
	events, err = enc.Encode(&pipeline.StreamEvent{
		Type:         pipeline.StreamEventStop,
		FinishReason: "tool_calls",
		Usage:        &pipeline.Usage{CompletionTokens: 50},
	})
	if err != nil {
		t.Fatalf("stop: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("stop should produce 3 events, got %d", len(events))
	}
	if events[0].Event != "content_block_stop" {
		t.Errorf("events[0] = %q", events[0].Event)
	}
	if events[1].Event != "message_delta" {
		t.Errorf("events[1] = %q", events[1].Event)
	}
	if !strings.Contains(events[1].Data, `"stop_reason":"tool_use"`) {
		t.Errorf("tool_calls should map to tool_use, got %s", events[1].Data)
	}
}
