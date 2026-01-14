package testkit

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestLoadJSONFixtures(t *testing.T) {
	fixtures := []string{
		"openai_chat_request.json",
		"openai_chat_response.json",
		"anthropic_messages_request.json",
		"anthropic_messages_response.json",
	}

	for _, name := range fixtures {
		t.Run(name, func(t *testing.T) {
			data := LoadFixture(t, name)
			if !json.Valid(data) {
				t.Errorf("fixture %s is not valid JSON", name)
			}
		})
	}
}

func TestLoadSSEFixtures(t *testing.T) {
	fixtures := []string{
		"openai_chat_stream.txt",
		"anthropic_messages_stream.txt",
	}

	for _, name := range fixtures {
		t.Run(name, func(t *testing.T) {
			lines := LoadSSELines(t, name)
			if len(lines) == 0 {
				t.Errorf("fixture %s has no lines", name)
			}
			hasData := false
			for _, line := range lines {
				if strings.HasPrefix(line, "data: ") {
					hasData = true
					break
				}
			}
			if !hasData {
				t.Errorf("fixture %s has no data lines", name)
			}
		})
	}
}

func TestOpenAIChatRequestFields(t *testing.T) {
	var req map[string]any
	data := LoadFixture(t, "openai_chat_request.json")
	if err := json.Unmarshal(data, &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if req["model"] != "gpt-5.4" {
		t.Errorf("model = %v, want gpt-4", req["model"])
	}
	msgs, ok := req["messages"].([]any)
	if !ok || len(msgs) != 2 {
		t.Fatalf("messages = %v", req["messages"])
	}
}

func TestAnthropicMessagesRequestFields(t *testing.T) {
	var req map[string]any
	data := LoadFixture(t, "anthropic_messages_request.json")
	if err := json.Unmarshal(data, &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if req["model"] != "claude-sonnet-4-6" {
		t.Errorf("model = %v", req["model"])
	}
	if req["system"] != "You are a helpful assistant." {
		t.Errorf("system = %v", req["system"])
	}
}
