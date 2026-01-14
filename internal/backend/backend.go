package backend

import (
	"context"
	"fmt"
	"io"
	"net/http"

	"github.com/j/insightlayer/internal/config"
	"github.com/j/insightlayer/internal/pipeline"
)

type Backend interface {
	Name() string
	Protocol() pipeline.Protocol
	Supports(kind pipeline.EndpointKind) bool
	Do(ctx context.Context, req *pipeline.NormalizedRequest) (*pipeline.NormalizedResponse, error)
	DoStream(ctx context.Context, req *pipeline.NormalizedRequest) (StreamReader, error)
	RawProxy(ctx context.Context, method, path string, body io.Reader, headers http.Header) (*http.Response, error)
}

type StreamReader interface {
	Next() (*pipeline.StreamEvent, error)
	io.Closer
}

type Credential struct {
	Header string
	Value  string
}

func ResolveCredential(cfg config.AuthConfig, clientHeaders http.Header) Credential {
	clientCred := extractClientCredential(clientHeaders)

	switch cfg.Mode {
	case config.AuthModePassthrough:
		if clientCred.Value != "" {
			return clientCred
		}
		return Credential{}
	case config.AuthModePreferClient:
		if clientCred.Value != "" {
			return clientCred
		}
		return Credential{Header: cfg.Header, Value: cfg.Value}
	default:
		return Credential{Header: cfg.Header, Value: cfg.Value}
	}
}

func extractClientCredential(h http.Header) Credential {
	if bearer := h.Get("Authorization"); bearer != "" {
		return Credential{Header: "Authorization", Value: bearer}
	}
	if key := h.Get("X-Api-Key"); key != "" {
		return Credential{Header: "X-Api-Key", Value: key}
	}
	return Credential{}
}

func MapCredentialToProtocol(cred Credential, target pipeline.Protocol) Credential {
	if cred.Value == "" {
		return cred
	}

	value := cred.Value
	// Strip "Bearer " prefix if present, for cross-protocol mapping
	if len(value) > 7 && value[:7] == "Bearer " {
		value = value[7:]
	}

	switch target {
	case pipeline.ProtocolOpenAI:
		return Credential{Header: "Authorization", Value: "Bearer " + value}
	case pipeline.ProtocolAnthropic:
		return Credential{Header: "X-Api-Key", Value: value}
	default:
		return cred
	}
}

var propagatedHeaders = []string{
	"X-Request-Id",
	"Traceparent",
	"Tracestate",
}

func PropagateHeaders(src http.Header, dst http.Header) {
	for _, key := range propagatedHeaders {
		if v := src.Get(key); v != "" {
			dst.Set(key, v)
		}
	}
	for k, vv := range src {
		if len(k) > 2 && k[:2] == "X-" {
			if dst.Get(k) == "" {
				for _, v := range vv {
					dst.Add(k, v)
				}
			}
		}
	}
}

func DoRawProxy(ctx context.Context, client *http.Client, baseURL, method, path string, body io.Reader, clientHeaders http.Header, defaultHeaders map[string]string, authCfg config.AuthConfig, proto pipeline.Protocol) (*http.Response, error) {
	httpReq, err := http.NewRequestWithContext(ctx, method, baseURL+path, body)
	if err != nil {
		return nil, err
	}

	httpReq.Header.Set("Content-Type", "application/json")
	for k, v := range defaultHeaders {
		httpReq.Header.Set(k, v)
	}

	if clientHeaders != nil {
		PropagateHeaders(clientHeaders, httpReq.Header)
	}

	cred := ResolveCredential(authCfg, clientHeaders)
	cred = MapCredentialToProtocol(cred, proto)
	if cred.Value != "" {
		httpReq.Header.Set(cred.Header, cred.Value)
	}

	return client.Do(httpReq)
}

func DoHTTP(client *http.Client, req *http.Request, label string) ([]byte, error) {
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s request: %w", label, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, &pipeline.PipelineError{
			StatusCode: resp.StatusCode,
			Message:    fmt.Sprintf("%s upstream error: %s", label, string(body)),
		}
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read %s response: %w", label, err)
	}
	return body, nil
}

func DoHTTPStream(client *http.Client, req *http.Request, label string) (*http.Response, error) {
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s stream request: %w", label, err)
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
		return nil, &pipeline.PipelineError{
			StatusCode: resp.StatusCode,
			Message:    fmt.Sprintf("%s upstream error: %s", label, string(body)),
		}
	}

	return resp, nil
}

type GenericStreamReader struct {
	Decoder interface {
		Next() (*pipeline.StreamEvent, error)
	}
	Body io.ReadCloser
}

func (r *GenericStreamReader) Next() (*pipeline.StreamEvent, error) {
	return r.Decoder.Next()
}

func (r *GenericStreamReader) Close() error {
	return r.Body.Close()
}

type Registry struct {
	backends map[string]Backend
}

func NewRegistry() *Registry {
	return &Registry{backends: make(map[string]Backend)}
}

func (r *Registry) Register(b Backend) {
	r.backends[b.Name()] = b
}

func (r *Registry) Get(name string) (Backend, error) {
	b, ok := r.backends[name]
	if !ok {
		return nil, fmt.Errorf("unknown backend: %s", name)
	}
	return b, nil
}
