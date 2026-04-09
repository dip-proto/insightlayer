package openai

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"

	"github.com/j/insightlayer/internal/backend"
	"github.com/j/insightlayer/internal/config"
	"github.com/j/insightlayer/internal/pipeline"
	openaicodec "github.com/j/insightlayer/internal/protocol/openai"
	"github.com/j/insightlayer/internal/stream"
)

type Backend struct {
	name      string
	baseURL   string
	authCfg   config.AuthConfig
	headers   map[string]string
	client    *http.Client
	supported map[pipeline.EndpointKind]bool
}

func New(cfg config.BackendConfig, client *http.Client) *Backend {
	if client == nil {
		client = backend.DefaultHTTPClient()
	}
	return &Backend{
		name:    cfg.Name,
		baseURL: cfg.BaseURL,
		authCfg: cfg.Auth,
		headers: cfg.DefaultHeaders,
		client:  client,
		supported: map[pipeline.EndpointKind]bool{
			pipeline.EndpointChat:       true,
			pipeline.EndpointCompletion: true,
			pipeline.EndpointEmbedding:  true,
			pipeline.EndpointModelList:  true,
		},
	}
}

func (b *Backend) Name() string                             { return b.name }
func (b *Backend) Protocol() pipeline.Protocol              { return pipeline.ProtocolOpenAI }
func (b *Backend) Supports(kind pipeline.EndpointKind) bool { return b.supported[kind] }

func (b *Backend) Do(ctx context.Context, req *pipeline.NormalizedRequest) (*pipeline.NormalizedResponse, error) {
	httpReq, err := b.buildRequest(ctx, req)
	if err != nil {
		return nil, err
	}

	body, err := backend.DoHTTP(b.client, httpReq, "openai")
	if err != nil {
		return nil, err
	}

	return b.decodeResponse(req.EndpointKind, body)
}

func (b *Backend) DoStream(ctx context.Context, req *pipeline.NormalizedRequest) (backend.StreamReader, error) {
	req.Stream = true
	httpReq, err := b.buildRequest(ctx, req)
	if err != nil {
		return nil, err
	}

	resp, err := backend.DoHTTPStream(b.client, httpReq, "openai")
	if err != nil {
		return nil, err
	}

	return &backend.GenericStreamReader{
		Decoder: stream.NewOpenAIStreamDecoder(resp.Body),
		Body:    resp.Body,
	}, nil
}

func (b *Backend) decodeResponse(kind pipeline.EndpointKind, body []byte) (*pipeline.NormalizedResponse, error) {
	switch kind {
	case pipeline.EndpointCompletion:
		return openaicodec.DecodeCompletionResponse(body)
	default:
		return openaicodec.DecodeResponse(body)
	}
}

func (b *Backend) buildRequest(ctx context.Context, req *pipeline.NormalizedRequest) (*http.Request, error) {
	var body []byte
	var err error
	switch req.EndpointKind {
	case pipeline.EndpointCompletion:
		body, err = openaicodec.EncodeCompletionRequest(req)
	default:
		body, err = openaicodec.EncodeRequest(req)
	}
	if err != nil {
		return nil, fmt.Errorf("encode openai request: %w", err)
	}

	path := pipeline.OpenAIEndpointPath(req.EndpointKind)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", b.baseURL+path, bytes.NewReader(body))
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
	cred = backend.MapCredentialToProtocol(cred, pipeline.ProtocolOpenAI)
	if cred.Value != "" {
		httpReq.Header.Set(cred.Header, cred.Value)
	}

	return httpReq, nil
}

func (b *Backend) RawProxy(ctx context.Context, method, path string, body io.Reader, headers http.Header) (*http.Response, error) {
	return backend.DoRawProxy(ctx, b.client, b.baseURL, method, path, body, headers, b.headers, b.authCfg, pipeline.ProtocolOpenAI)
}
