# Getting Started

## Installation

InsightLayer is a single Go binary. Build it from source:

```sh
go build -o bin/insightlayer ./cmd/insightlayer
```

Or use `go install` to place it on your `PATH`:

```sh
go install ./cmd/insightlayer
```

There is also a Makefile if you prefer:

```sh
make build          # builds to bin/insightlayer
make test           # runs the test suite
make lint           # runs golangci-lint
make fmt            # formats with gofumpt
```

The only runtime dependency is a YAML parser. There are no frameworks, no
container requirements, and no external services needed beyond whatever LLM
backends you want to proxy to.

## Your first config

InsightLayer reads its configuration from a YAML file. By default it looks for
`config.yaml` in the current directory, but you can point it at any path:

```sh
insightlayer -config /path/to/config.yaml
```

Here is the simplest useful config. It proxies OpenAI-format chat requests to a
local server running on port 8080, which is what you get with LM Studio, ollama,
llama.cpp, or similar tools:

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

Start the server and try a request:

```sh
insightlayer -config config.yaml
```

```sh
curl http://localhost:9090/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "qwen3.5-9b-unsloth-mlx",
    "messages": [
      {"role": "user", "content": "What is 2+2? Reply with just the number."}
    ]
  }'
```

You should get back a standard OpenAI chat completion response with the model's
answer. If you add `"stream": true` to the request body, the response comes back
as [server-sent events](streaming.md) instead.

## What happens internally

When a request arrives, InsightLayer matches it against the configured
[routes](routing.md) by path prefix and priority. Once a route is matched, the
request body is decoded from the client's wire format (OpenAI or Anthropic) into
a protocol-neutral representation. Any [pre-request hooks](hooks.md) run on this
normalized form. Then the request is re-encoded into the
[backend's](backends.md) wire format and sent upstream. The response takes the
reverse path: decode from backend format, run post-response hooks, encode into
client format, send back.

This is what makes [cross-protocol translation](cross-protocol.md) possible. A
client speaking OpenAI can talk to an Anthropic backend, or vice versa, because
the normalization layer sits in between.

## Where to go next

If you want to understand the full configuration surface, continue with
[Configuration](configuration.md). If you want to set up cross-protocol
translation, read [Cross-Protocol Translation](cross-protocol.md). If you want
to inspect or modify traffic with hooks, see [Hooks](hooks.md). For practical
end-to-end setups, check out [Examples](examples.md).
