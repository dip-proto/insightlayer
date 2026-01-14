package hooks

import (
	"context"
	"errors"

	"github.com/j/insightlayer/internal/pipeline"
)

type Manager struct {
	preRequest   []pipeline.PreRequestHook
	postResponse []pipeline.PostResponseHook
	streamEvent  []pipeline.StreamEventHook
	errorHooks   []pipeline.ErrorHook
}

func NewManager() *Manager {
	return &Manager{}
}

func (m *Manager) HasPostResponseHooks() bool {
	return len(m.postResponse) > 0
}

func (m *Manager) AddPreRequest(h pipeline.PreRequestHook) {
	m.preRequest = append(m.preRequest, h)
}

func (m *Manager) AddPostResponse(h pipeline.PostResponseHook) {
	m.postResponse = append(m.postResponse, h)
}

func (m *Manager) AddStreamEvent(h pipeline.StreamEventHook) {
	m.streamEvent = append(m.streamEvent, h)
}

func (m *Manager) AddError(h pipeline.ErrorHook) {
	m.errorHooks = append(m.errorHooks, h)
}

func (m *Manager) RunPreRequest(ctx context.Context, req *pipeline.NormalizedRequest) error {
	for _, h := range m.preRequest {
		if err := h.Execute(ctx, req); err != nil {
			return wrapHookError(err, h.Name(), "pre-request")
		}
	}
	return nil
}

func (m *Manager) RunPostResponse(ctx context.Context, resp *pipeline.NormalizedResponse) error {
	for _, h := range m.postResponse {
		if err := h.Execute(ctx, resp); err != nil {
			return wrapHookError(err, h.Name(), "post-response")
		}
	}
	return nil
}

func (m *Manager) RunStreamEvent(ctx context.Context, event *pipeline.StreamEvent) error {
	for _, h := range m.streamEvent {
		if err := h.Execute(ctx, event); err != nil {
			return wrapHookError(err, h.Name(), "stream-event")
		}
	}
	return nil
}

func wrapHookError(err error, hookName, phase string) error {
	if errors.Is(err, pipeline.ErrStopPipeline) {
		return err
	}
	var pErr *pipeline.PipelineError
	if errors.As(err, &pErr) {
		return pErr
	}
	return &pipeline.PipelineError{
		Cause:      err,
		StatusCode: 500,
		Message:    phase + " hook " + hookName + " failed",
	}
}

func (m *Manager) RunError(ctx context.Context, pErr *pipeline.PipelineError) error {
	for _, h := range m.errorHooks {
		if err := h.Execute(ctx, pErr); err != nil {
			return err
		}
	}
	return nil
}
