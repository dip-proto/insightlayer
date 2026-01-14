package pipeline

import (
	"context"
	"errors"
)

var ErrStopPipeline = errors.New("stop pipeline")

type PreRequestHook interface {
	Name() string
	Execute(ctx context.Context, req *NormalizedRequest) error
}

type PostResponseHook interface {
	Name() string
	Execute(ctx context.Context, resp *NormalizedResponse) error
}

type StreamEventHook interface {
	Name() string
	Execute(ctx context.Context, event *StreamEvent) error
}

type ErrorHook interface {
	Name() string
	Execute(ctx context.Context, err *PipelineError) error
}
