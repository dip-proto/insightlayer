# InsightLayer

A cross-protocol LLM gateway proxy.

InsightLayer sits between your applications and LLM providers. It translates
requests and responses between the OpenAI and Anthropic API formats, so a client
built for one protocol can talk to a backend that speaks the other. It handles
both streaming and non-streaming requests, rewrites authentication headers
across protocols, and gives you a hook system to inspect or modify traffic as it
flows through.

The whole thing is a single Go binary with one dependency (a YAML parser). You
write a config file, start the server, and point your clients at it.

## Quickstart

Build and install:

```sh
go install ./cmd/insightlayer
```

Or with make:

```sh
make build
```

Create a config file. This minimal example proxies OpenAI-format chat requests
to a local LM Studio server:

```yaml
server:
  listen: ":9090"

routing:
  routes:
    - name: chat
      priority: 100
      inbound_protocol: openai
      path_prefix: /v1/chat/completions
      endpoint_kinds: [chat]
      backend: local

backends:
  - name: local
    protocol: openai
    base_url: http://127.0.0.1:8080
    auth:
      mode: passthrough
```

Start the server:

```sh
insightlayer -config config.yaml
```

Send a request:

```sh
curl http://localhost:9090/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "qwen3.5-9b-unsloth-mlx",
    "messages": [{"role": "user", "content": "Hello!"}]
  }'
```

That request goes through InsightLayer to LM Studio and comes back as a
standard OpenAI chat completion response. Streaming works the same way -- just
add `"stream": true` to the request body.

## What it does

The core idea is protocol translation. Every incoming request is decoded into a
protocol-neutral representation, passed through any configured hooks, re-encoded
into the backend's wire format, and sent upstream. The response takes the reverse
path. This means you can point an OpenAI client at an Anthropic backend, or an
Anthropic client at an OpenAI-compatible server, and everything just works.

Routes are matched by path prefix and priority, so you can set up different
backends for different endpoints or namespaces. Authentication credentials are
translated automatically between protocols (Bearer tokens and X-Api-Key headers).

Hooks let you log requests, redact sensitive text, or modify headers at each
stage of the pipeline. They run in the order you define them and operate on the
normalized representation, not the raw wire format.

## Configuration

Configuration is YAML-based. Environment variables can be referenced with
`${VAR}` syntax and are expanded at startup. See `config.example.yaml` for a
verbose example that demonstrates every feature, or read the
[configuration guide](docs.md/configuration.md) for a walkthrough of all
options.

## Documentation

- [Getting Started](docs.md/getting-started.md) -- installation, first config,
  first request
- [Configuration](docs.md/configuration.md) -- server settings, environment
  variables, health check, logging
- [Routing](docs.md/routing.md) -- route matching, priorities, namespaces,
  catch-all routes
- [Backends](docs.md/backends.md) -- upstream services, protocols, default
  headers
- [Authentication](docs.md/authentication.md) -- inject, passthrough,
  prefer_client, cross-protocol credential translation
- [Cross-Protocol Translation](docs.md/cross-protocol.md) -- the
  normalize-then-encode pipeline, OpenAI-to-Anthropic and back
- [Streaming](docs.md/streaming.md) -- SSE, cross-protocol streaming, error
  handling
- [Hooks](docs.md/hooks.md) -- logging, text redaction, header modification
- [Examples](docs.md/examples.md) -- complete working configs you can copy
