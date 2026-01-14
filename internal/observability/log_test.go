package observability

import (
	"context"
	"log/slog"
	"testing"
)

func TestRequestIDRoundTrip(t *testing.T) {
	ctx := context.Background()
	if id := RequestID(ctx); id != "" {
		t.Errorf("empty context should have no request ID, got %q", id)
	}

	ctx = WithRequestID(ctx, "req-123")
	if id := RequestID(ctx); id != "req-123" {
		t.Errorf("request ID = %q, want %q", id, "req-123")
	}
}

func TestLoggerFrom(t *testing.T) {
	base := NewLogger(slog.LevelInfo)

	plain := LoggerFrom(context.Background(), base)
	if plain != base {
		t.Error("LoggerFrom with no request ID should return the base logger")
	}

	ctx := WithRequestID(context.Background(), "req-456")
	enriched := LoggerFrom(ctx, base)
	if enriched == base {
		t.Error("LoggerFrom with request ID should return a new logger")
	}
}
