package hooks

import (
	"context"
	"errors"
	"testing"

	"github.com/j/insightlayer/internal/pipeline"
)

type testPreHook struct {
	name string
	fn   func(*pipeline.NormalizedRequest) error
}

func (h *testPreHook) Name() string { return h.name }
func (h *testPreHook) Execute(_ context.Context, req *pipeline.NormalizedRequest) error {
	return h.fn(req)
}

type testPostHook struct {
	name string
	fn   func(*pipeline.NormalizedResponse) error
}

func (h *testPostHook) Name() string { return h.name }
func (h *testPostHook) Execute(_ context.Context, resp *pipeline.NormalizedResponse) error {
	return h.fn(resp)
}

type testStreamHook struct {
	name string
	fn   func(*pipeline.StreamEvent) error
}

func (h *testStreamHook) Name() string { return h.name }
func (h *testStreamHook) Execute(_ context.Context, event *pipeline.StreamEvent) error {
	return h.fn(event)
}

func TestPreRequestHookOrder(t *testing.T) {
	m := NewManager()
	var order []string

	m.AddPreRequest(&testPreHook{name: "first", fn: func(req *pipeline.NormalizedRequest) error {
		order = append(order, "first")
		return nil
	}})
	m.AddPreRequest(&testPreHook{name: "second", fn: func(req *pipeline.NormalizedRequest) error {
		order = append(order, "second")
		return nil
	}})

	req := &pipeline.NormalizedRequest{Model: "test"}
	if err := m.RunPreRequest(context.Background(), req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(order) != 2 || order[0] != "first" || order[1] != "second" {
		t.Errorf("execution order = %v, want [first second]", order)
	}
}

func TestPreRequestMutationPropagates(t *testing.T) {
	m := NewManager()

	m.AddPreRequest(&testPreHook{name: "upper", fn: func(req *pipeline.NormalizedRequest) error {
		req.Model = "modified-model"
		return nil
	}})
	m.AddPreRequest(&testPreHook{name: "check", fn: func(req *pipeline.NormalizedRequest) error {
		if req.Model != "modified-model" {
			t.Errorf("mutation not visible: model = %q", req.Model)
		}
		return nil
	}})

	req := &pipeline.NormalizedRequest{Model: "original"}
	if err := m.RunPreRequest(context.Background(), req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if req.Model != "modified-model" {
		t.Errorf("final model = %q, want %q", req.Model, "modified-model")
	}
}

func TestStopPipeline(t *testing.T) {
	m := NewManager()
	secondRan := false

	m.AddPreRequest(&testPreHook{name: "stopper", fn: func(req *pipeline.NormalizedRequest) error {
		return pipeline.ErrStopPipeline
	}})
	m.AddPreRequest(&testPreHook{name: "after", fn: func(req *pipeline.NormalizedRequest) error {
		secondRan = true
		return nil
	}})

	req := &pipeline.NormalizedRequest{}
	err := m.RunPreRequest(context.Background(), req)
	if !errors.Is(err, pipeline.ErrStopPipeline) {
		t.Errorf("error = %v, want ErrStopPipeline", err)
	}
	if secondRan {
		t.Error("second hook should not have run after stop")
	}
}

func TestHookError(t *testing.T) {
	m := NewManager()

	m.AddPreRequest(&testPreHook{name: "fail", fn: func(req *pipeline.NormalizedRequest) error {
		return errors.New("hook failed")
	}})

	req := &pipeline.NormalizedRequest{}
	err := m.RunPreRequest(context.Background(), req)
	if err == nil {
		t.Fatal("expected error")
	}

	var pErr *pipeline.PipelineError
	if !errors.As(err, &pErr) {
		t.Fatalf("expected PipelineError, got %T", err)
	}
	if pErr.StatusCode != 500 {
		t.Errorf("status = %d, want 500", pErr.StatusCode)
	}
}

func TestPostResponseMutation(t *testing.T) {
	m := NewManager()

	m.AddPostResponse(&testPostHook{name: "modify", fn: func(resp *pipeline.NormalizedResponse) error {
		resp.Content = "modified"
		return nil
	}})

	resp := &pipeline.NormalizedResponse{Content: "original"}
	if err := m.RunPostResponse(context.Background(), resp); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "modified" {
		t.Errorf("content = %q, want %q", resp.Content, "modified")
	}
}

func TestStreamEventMutation(t *testing.T) {
	m := NewManager()

	m.AddStreamEvent(&testStreamHook{name: "modify", fn: func(event *pipeline.StreamEvent) error {
		event.Text = "replaced"
		return nil
	}})

	event := &pipeline.StreamEvent{Type: pipeline.StreamEventTextDelta, Text: "original"}
	if err := m.RunStreamEvent(context.Background(), event); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if event.Text != "replaced" {
		t.Errorf("text = %q, want %q", event.Text, "replaced")
	}
}
