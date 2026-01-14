package stream

import (
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/j/insightlayer/internal/pipeline"
)

func TestOpenAIStreamDecoder(t *testing.T) {
	input := `data: {"id":"chatcmpl-abc","object":"chat.completion.chunk","created":1700000000,"model":"gpt-5.4","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}

data: {"id":"chatcmpl-abc","object":"chat.completion.chunk","created":1700000000,"model":"gpt-5.4","choices":[{"index":0,"delta":{"content":"Hello"},"finish_reason":null}]}

data: {"id":"chatcmpl-abc","object":"chat.completion.chunk","created":1700000000,"model":"gpt-5.4","choices":[{"index":0,"delta":{"content":"!"},"finish_reason":null}]}

data: {"id":"chatcmpl-abc","object":"chat.completion.chunk","created":1700000000,"model":"gpt-5.4","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12}}

data: [DONE]

`
	decoder := NewOpenAIStreamDecoder(strings.NewReader(input))

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
		t.Errorf("event 2 = %+v, want text_delta Hello", ev)
	}

	// text_delta "!"
	ev, err = decoder.Next()
	if err != nil {
		t.Fatalf("event 3: %v", err)
	}
	if ev.Type != pipeline.StreamEventTextDelta || ev.Text != "!" {
		t.Errorf("event 3 = %+v, want text_delta !", ev)
	}

	// stop
	ev, err = decoder.Next()
	if err != nil {
		t.Fatalf("event 4: %v", err)
	}
	if ev.Type != pipeline.StreamEventStop {
		t.Errorf("event 4 type = %q, want stop", ev.Type)
	}
	if ev.FinishReason != "stop" {
		t.Errorf("finish_reason = %q", ev.FinishReason)
	}
	if ev.Usage == nil || ev.Usage.TotalTokens != 12 {
		t.Errorf("usage = %+v", ev.Usage)
	}

	// EOF
	_, err = decoder.Next()
	if err != io.EOF {
		t.Errorf("expected EOF, got %v", err)
	}
}

func TestOpenAIStreamDecoderFirstChunkWithContent(t *testing.T) {
	input := `data: {"id":"chatcmpl-abc","object":"chat.completion.chunk","created":1700000000,"model":"gpt-5.4","choices":[{"index":0,"delta":{"role":"assistant","content":"Hello"},"finish_reason":null}]}

data: {"id":"chatcmpl-abc","object":"chat.completion.chunk","created":1700000000,"model":"gpt-5.4","choices":[{"index":0,"delta":{"content":" world"},"finish_reason":null}]}

data: {"id":"chatcmpl-abc","object":"chat.completion.chunk","created":1700000000,"model":"gpt-5.4","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}

data: [DONE]

`
	decoder := NewOpenAIStreamDecoder(strings.NewReader(input))

	// stream_start
	ev, err := decoder.Next()
	if err != nil {
		t.Fatalf("event 1: %v", err)
	}
	if ev.Type != pipeline.StreamEventStart {
		t.Errorf("event 1 type = %q, want stream_start", ev.Type)
	}

	// text_delta "Hello" (carried on the first chunk alongside role)
	ev, err = decoder.Next()
	if err != nil {
		t.Fatalf("event 2: %v", err)
	}
	if ev.Type != pipeline.StreamEventTextDelta {
		t.Errorf("event 2 type = %q, want text_delta", ev.Type)
	}
	if ev.Text != "Hello" {
		t.Errorf("event 2 text = %q, want %q", ev.Text, "Hello")
	}

	// text_delta " world"
	ev, err = decoder.Next()
	if err != nil {
		t.Fatalf("event 3: %v", err)
	}
	if ev.Type != pipeline.StreamEventTextDelta || ev.Text != " world" {
		t.Errorf("event 3 = %+v", ev)
	}

	// stop
	ev, err = decoder.Next()
	if err != nil {
		t.Fatalf("event 4: %v", err)
	}
	if ev.Type != pipeline.StreamEventStop {
		t.Errorf("event 4 type = %q, want stop", ev.Type)
	}

	// EOF
	_, err = decoder.Next()
	if err != io.EOF {
		t.Errorf("expected EOF, got %v", err)
	}
}

func TestOpenAIStreamEncoder(t *testing.T) {
	enc := NewOpenAIStreamEncoder("chatcmpl-test", "gpt-5.4")

	// stream_start
	sse, err := enc.Encode(&pipeline.StreamEvent{Type: pipeline.StreamEventStart})
	if err != nil {
		t.Fatalf("encode start: %v", err)
	}
	if sse == nil {
		t.Fatal("start event should produce SSE")
	} else if !strings.Contains(sse.Data, `"role":"assistant"`) {
		t.Errorf("start event should contain role, got %s", sse.Data)
	}

	// text_delta
	sse, err = enc.Encode(&pipeline.StreamEvent{Type: pipeline.StreamEventTextDelta, Text: "hi"})
	if err != nil {
		t.Fatalf("encode delta: %v", err)
	}
	if !strings.Contains(sse.Data, `"content":"hi"`) {
		t.Errorf("delta should contain content, got %s", sse.Data)
	}

	// stop
	sse, err = enc.Encode(&pipeline.StreamEvent{Type: pipeline.StreamEventStop, FinishReason: "stop"})
	if err != nil {
		t.Fatalf("encode stop: %v", err)
	}
	if !strings.Contains(sse.Data, `"finish_reason":"stop"`) {
		t.Errorf("stop should contain finish_reason, got %s", sse.Data)
	}

	// done
	done := enc.Done()
	if done.Data != "[DONE]" {
		t.Errorf("done data = %q", done.Data)
	}

	// error event produces SSE payload instead of returning an error
	sse, err = enc.Encode(&pipeline.StreamEvent{
		Type:  pipeline.StreamEventError,
		Error: fmt.Errorf("upstream failure"),
	})
	if err != nil {
		t.Fatalf("encode error: %v", err)
	}
	if sse == nil {
		t.Fatal("error event should produce an SSE payload, not nil")
	} else if !strings.Contains(sse.Data, `"message":"upstream failure"`) {
		t.Errorf("error event should contain message, got %s", sse.Data)
	}
	if !strings.Contains(sse.Data, `"type":"server_error"`) {
		t.Errorf("error event should contain type, got %s", sse.Data)
	}
}

func TestOpenAIStreamDecoderToolCalls(t *testing.T) {
	input := `data: {"id":"chatcmpl-abc","object":"chat.completion.chunk","created":1700000000,"model":"gpt-5.4","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}

data: {"id":"chatcmpl-abc","object":"chat.completion.chunk","created":1700000000,"model":"gpt-5.4","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_abc","type":"function","function":{"name":"get_weather","arguments":""}}]},"finish_reason":null}]}

data: {"id":"chatcmpl-abc","object":"chat.completion.chunk","created":1700000000,"model":"gpt-5.4","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"city\""}}]},"finish_reason":null}]}

data: {"id":"chatcmpl-abc","object":"chat.completion.chunk","created":1700000000,"model":"gpt-5.4","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":":\"SF\"}"}}]},"finish_reason":null}]}

data: {"id":"chatcmpl-abc","object":"chat.completion.chunk","created":1700000000,"model":"gpt-5.4","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}

data: [DONE]

`
	decoder := NewOpenAIStreamDecoder(strings.NewReader(input))

	// stream_start
	ev, err := decoder.Next()
	if err != nil {
		t.Fatalf("event 1: %v", err)
	}
	if ev.Type != pipeline.StreamEventStart {
		t.Errorf("event 1 type = %q", ev.Type)
	}

	// tool_call_start
	ev, err = decoder.Next()
	if err != nil {
		t.Fatalf("event 2: %v", err)
	}
	if ev.Type != pipeline.StreamEventToolCallStart {
		t.Errorf("event 2 type = %q, want tool_call_start", ev.Type)
	}
	if ev.ToolCallID != "call_abc" || ev.ToolCallName != "get_weather" {
		t.Errorf("tool_call_start = id=%q name=%q", ev.ToolCallID, ev.ToolCallName)
	}

	// tool_call_delta (first fragment)
	ev, err = decoder.Next()
	if err != nil {
		t.Fatalf("event 3: %v", err)
	}
	if ev.Type != pipeline.StreamEventToolCallDelta || ev.Text != `{"city"` {
		t.Errorf("event 3 = %+v", ev)
	}

	// tool_call_delta (second fragment)
	ev, err = decoder.Next()
	if err != nil {
		t.Fatalf("event 4: %v", err)
	}
	if ev.Type != pipeline.StreamEventToolCallDelta || ev.Text != `:"SF"}` {
		t.Errorf("event 4 = %+v", ev)
	}

	// stop with tool_calls
	ev, err = decoder.Next()
	if err != nil {
		t.Fatalf("event 5: %v", err)
	}
	if ev.Type != pipeline.StreamEventStop || ev.FinishReason != "tool_calls" {
		t.Errorf("event 5 = %+v", ev)
	}

	// EOF
	_, err = decoder.Next()
	if err != io.EOF {
		t.Errorf("expected EOF, got %v", err)
	}
}

func TestOpenAIStreamEncoderToolCalls(t *testing.T) {
	enc := NewOpenAIStreamEncoder("chatcmpl-test", "gpt-5.4")

	// stream_start
	sse, err := enc.Encode(&pipeline.StreamEvent{Type: pipeline.StreamEventStart})
	if err != nil || sse == nil {
		t.Fatalf("start: err=%v sse=%v", err, sse)
	}

	// tool_call_start
	sse, err = enc.Encode(&pipeline.StreamEvent{
		Type:          pipeline.StreamEventToolCallStart,
		ToolCallID:    "call_abc",
		ToolCallName:  "get_weather",
		ToolCallIndex: 0,
	})
	if err != nil || sse == nil {
		t.Fatalf("tool_call_start: err=%v sse=%v", err, sse)
	}
	if !strings.Contains(sse.Data, `"id":"call_abc"`) {
		t.Errorf("should contain id, got %s", sse.Data)
	}
	if !strings.Contains(sse.Data, `"name":"get_weather"`) {
		t.Errorf("should contain name, got %s", sse.Data)
	}

	// tool_call_delta
	sse, err = enc.Encode(&pipeline.StreamEvent{
		Type:          pipeline.StreamEventToolCallDelta,
		Text:          `{"city":"SF"}`,
		ToolCallIndex: 0,
	})
	if err != nil || sse == nil {
		t.Fatalf("tool_call_delta: err=%v sse=%v", err, sse)
	}
	if !strings.Contains(sse.Data, `"arguments":"{\"city\":\"SF\"}"`) {
		t.Errorf("should contain arguments, got %s", sse.Data)
	}

	// stop with tool_calls
	sse, err = enc.Encode(&pipeline.StreamEvent{
		Type:         pipeline.StreamEventStop,
		FinishReason: "tool_calls",
	})
	if err != nil || sse == nil {
		t.Fatalf("stop: err=%v sse=%v", err, sse)
	}
	if !strings.Contains(sse.Data, `"finish_reason":"tool_calls"`) {
		t.Errorf("should contain finish_reason, got %s", sse.Data)
	}
}
