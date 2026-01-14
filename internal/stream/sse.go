package stream

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type SSEEvent struct {
	Event string
	Data  string
}

type SSEReader struct {
	scanner *bufio.Scanner
}

func NewSSEReader(r io.Reader) *SSEReader {
	return &SSEReader{scanner: bufio.NewScanner(r)}
}

func (r *SSEReader) Next() (*SSEEvent, error) {
	var event SSEEvent
	var hasData bool

	for r.scanner.Scan() {
		line := r.scanner.Text()

		if line == "" {
			if hasData {
				return &event, nil
			}
			continue
		}

		switch {
		case strings.HasPrefix(line, "event: "):
			event.Event = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			if hasData {
				event.Data += "\n"
			}
			event.Data += strings.TrimPrefix(line, "data: ")
			hasData = true
		case line == "data:":
			if hasData {
				event.Data += "\n"
			}
			hasData = true
		}
	}

	if err := r.scanner.Err(); err != nil {
		return nil, err
	}

	if hasData {
		return &event, nil
	}
	return nil, io.EOF
}

type SSEWriter struct {
	w       http.ResponseWriter
	flusher http.Flusher
}

func NewSSEWriter(w http.ResponseWriter) (*SSEWriter, error) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil, fmt.Errorf("response writer does not support flushing")
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	return &SSEWriter{w: w, flusher: flusher}, nil
}

func (w *SSEWriter) WriteEvent(event SSEEvent) error {
	if event.Event != "" {
		if _, err := fmt.Fprintf(w.w, "event: %s\n", event.Event); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(w.w, "data: %s\n\n", event.Data); err != nil {
		return err
	}
	w.flusher.Flush()
	return nil
}

func (w *SSEWriter) WriteData(data string) error {
	return w.WriteEvent(SSEEvent{Data: data})
}
