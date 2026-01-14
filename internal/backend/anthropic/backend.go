package anthropic

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"

	"github.com/j/insightlayer/internal/backend"
	"github.com/j/insightlayer/internal/config"
	"github.com/j/insightlayer/internal/pipeline"
	anthropiccodec "github.com/j/insightlayer/internal/protocol/anthropic"
	"github.com/j/insightlayer/internal/stream"
)

type Backend struct {
	name    string
	baseURL string
	authCfg config.AuthConfig
	headers map[string]string
	client  *http.Client
}

func New(cfg config.BackendConfig, client *http.Client) *Backend {
	if client == nil {
		client = http.DefaultClient
	}
	return &Backend{
		name:    cfg.Name,
		baseURL: cfg.BaseURL,
		authCfg: cfg.Auth,
		headers: cfg.DefaultHeaders,
		client:  client,
	}
}

func (b *Backend) Name() string                             { return b.name }
func (b *Backend) Protocol() pipeline.Protocol              { return pipeline.ProtocolAnthropic }
func (b *Backend) Supports(kind pipeline.EndpointKind) bool { return kind == pipeline.EndpointChat }

func (b *Backend) Do(ctx context.Context, req *pipeline.NormalizedRequest) (*pipeline.NormalizedResponse, error) {
	httpReq, err := b.buildRequest(ctx, req)
	if err != nil {
		return nil, err
	}

	body, err := backend.DoHTTP(b.client, httpReq, "anthropic")
	if err != nil {
		return nil, err
	}

	return anthropiccodec.DecodeResponse(body)
}

func (b *Backend) DoStream(ctx context.Context, req *pipeline.NormalizedRequest) (backend.StreamReader, error) {
	req.Stream = true
	httpReq, err := b.buildRequest(ctx, req)
	if err != nil {
		return nil, err
	}

	resp, err := backend.DoHTTPStream(b.client, httpReq, "anthropic")
	if err != nil {
		return nil, err
	}

	return &backend.GenericStreamReader{
		Decoder: stream.NewAnthropicStreamDecoder(resp.Body),
		Body:    resp.Body,
	}, nil
}

func (b *Backend) buildRequest(ctx context.Context, req *pipeline.NormalizedRequest) (*http.Request, error) {
	body, err := anthropiccodec.EncodeRequest(req)
	if err != nil {
		return nil, fmt.Errorf("encode anthropic request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", b.baseURL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	httpReq.Header.Set("Content-Type", "application/json")
	for k, v := range b.headers {
		httpReq.Header.Set(k, v)
	}

	if req.Headers != nil {
		backend.PropagateHeaders(req.Headers, httpReq.Header)
	}

	cred := backend.ResolveCredential(b.authCfg, req.Headers)
	cred = backend.MapCredentialToProtocol(cred, pipeline.ProtocolAnthropic)
	if cred.Value != "" {
		httpReq.Header.Set(cred.Header, cred.Value)
	}

	return httpReq, nil
}

func (b *Backend) RawProxy(ctx context.Context, method, path string, body io.Reader, headers http.Header) (*http.Response, error) {
	return backend.DoRawProxy(ctx, b.client, b.baseURL, method, path, body, headers, b.headers, b.authCfg, pipeline.ProtocolAnthropic)
}
