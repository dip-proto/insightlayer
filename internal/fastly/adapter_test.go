package fastly

import (
	"bytes"
	"net/url"
	"testing"

	"github.com/j/insightlayer/internal/pipeline"
	"github.com/j/insightlayer/internal/stream"
)

func TestBuildTransportFromBackendURLs(t *testing.T) {
	backends := map[string]*url.URL{
		"openai":    mustURL("https://api.openai.com"),
		"anthropic": mustURL("https://api.anthropic.com"),
	}

	transport := BuildTransport(backends)
	if transport == nil {
		t.Fatal("BuildTransport returned nil")
	}
}

func TestBuildTransportEmptyBackends(t *testing.T) {
	transport := BuildTransport(map[string]*url.URL{})
	if transport == nil {
		t.Fatal("BuildTransport with empty map returned nil")
	}
}

func TestStreamEmitterOpenAIFrames(t *testing.T) {
	var buf bytes.Buffer
	fw := stream.NewSSEFrameWriter(&buf)
	enc := stream.NewOpenAIStreamEncoder("id-1", "gpt-4")

	events := []*pipeline.StreamEvent{
		{Type: pipeline.StreamEventStart},
		{Type: pipeline.StreamEventTextDelta, Text: "Hello"},
		{Type: pipeline.StreamEventStop, FinishReason: "stop"},
	}

	for _, ev := range events {
		sse, err := enc.Encode(ev)
		if err != nil {
			t.Fatalf("encode %s: %v", ev.Type, err)
		}
		if sse != nil {
			if err := fw.WriteEvent(*sse); err != nil {
				t.Fatalf("write %s: %v", ev.Type, err)
			}
		}
	}
	if err := fw.WriteEvent(*enc.Done()); err != nil {
		t.Fatalf("write done: %v", err)
	}

	output := buf.String()

	if !bytes.Contains([]byte(output), []byte("data: ")) {
		t.Error("output should contain SSE data frames")
	}
	if !bytes.Contains([]byte(output), []byte("Hello")) {
		t.Error("output should contain streamed text")
	}
	if !bytes.Contains([]byte(output), []byte("[DONE]")) {
		t.Error("output should end with [DONE]")
	}
}

func TestStreamEmitterAnthropicFrames(t *testing.T) {
	var buf bytes.Buffer
	fw := stream.NewSSEFrameWriter(&buf)
	enc := stream.NewAnthropicStreamEncoder("id-1", "claude-3")

	events := []*pipeline.StreamEvent{
		{Type: pipeline.StreamEventStart},
		{Type: pipeline.StreamEventTextDelta, Text: "Hi"},
		{Type: pipeline.StreamEventStop, FinishReason: "end_turn"},
	}

	for _, ev := range events {
		sseEvents, err := enc.Encode(ev)
		if err != nil {
			t.Fatalf("encode %s: %v", ev.Type, err)
		}
		for _, sse := range sseEvents {
			if err := fw.WriteEvent(sse); err != nil {
				t.Fatalf("write %s: %v", ev.Type, err)
			}
		}
	}

	output := buf.String()

	if !bytes.Contains([]byte(output), []byte("event: message_start")) {
		t.Error("should contain message_start event")
	}
	if !bytes.Contains([]byte(output), []byte("Hi")) {
		t.Error("should contain streamed text")
	}
	if !bytes.Contains([]byte(output), []byte("event: message_stop")) {
		t.Error("should end with message_stop")
	}
}

func mustURL(raw string) *url.URL {
	u, err := url.Parse(raw)
	if err != nil {
		panic(err)
	}
	return u
}
