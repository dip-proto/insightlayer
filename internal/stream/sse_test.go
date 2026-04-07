package stream

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSSEFrameWriterEvent(t *testing.T) {
	var buf bytes.Buffer
	fw := NewSSEFrameWriter(&buf)

	if err := fw.WriteEvent(SSEEvent{Event: "message", Data: `{"text":"hello"}`}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := "event: message\ndata: {\"text\":\"hello\"}\n\n"
	if got := buf.String(); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestSSEFrameWriterDataOnly(t *testing.T) {
	var buf bytes.Buffer
	fw := NewSSEFrameWriter(&buf)

	if err := fw.WriteData(`{"id":"1"}`); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := "data: {\"id\":\"1\"}\n\n"
	if got := buf.String(); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestSSEFrameWriterMultipleEvents(t *testing.T) {
	var buf bytes.Buffer
	fw := NewSSEFrameWriter(&buf)

	events := []SSEEvent{
		{Event: "start", Data: `{"type":"start"}`},
		{Data: `{"text":"hello"}`},
		{Data: "[DONE]"},
	}
	for _, e := range events {
		if err := fw.WriteEvent(e); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	want := "event: start\ndata: {\"type\":\"start\"}\n\n" +
		"data: {\"text\":\"hello\"}\n\n" +
		"data: [DONE]\n\n"
	if got := buf.String(); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestSSEWriterFlushes(t *testing.T) {
	rec := httptest.NewRecorder()
	sw, err := NewSSEWriter(rec)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if ct := rec.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}

	if err := sw.WriteEvent(SSEEvent{Event: "delta", Data: `{"text":"hi"}`}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	body := rec.Body.String()
	want := "event: delta\ndata: {\"text\":\"hi\"}\n\n"
	if body != want {
		t.Errorf("body = %q, want %q", body, want)
	}
	if !rec.Flushed {
		t.Error("SSEWriter should flush after each event")
	}
}
