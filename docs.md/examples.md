# Examples

This page collects practical, end-to-end configurations that you can copy and
adapt. Each example is a complete [config](configuration.md) file that works on
its own.

## Local model proxy

The simplest setup. Forward OpenAI-format chat requests to a local model server
(LM Studio, ollama, llama.cpp, vLLM, or anything else that speaks the OpenAI
protocol). No [authentication](authentication.md) needed.

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

Test it:

```sh
curl http://localhost:9090/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "qwen3.5-9b-unsloth-mlx",
    "messages": [{"role": "user", "content": "Hello!"}]
  }'
```

## Local model as Anthropic endpoint

Same local server, but exposed as an Anthropic-protocol endpoint via
[cross-protocol translation](cross-protocol.md). Useful if you have tooling that
only speaks the Anthropic API and you want to point it at a local model.

```yaml
server:
  listen: ":9090"

routing:
  routes:
    - name: messages
      priority: 100
      inbound_protocol: anthropic
      path_prefix: /v1/messages
      endpoint_kinds: [chat]
      backend: local

backends:
  - name: local
    protocol: openai
    base_url: http://127.0.0.1:8080
    auth:
      mode: passthrough
```

Anthropic-format requests to `http://localhost:9090/v1/messages` are translated
to OpenAI format, forwarded to the local model, and the response is translated
back to Anthropic format.

## OpenAI client to Anthropic backend

Your application speaks OpenAI, and you want to use Claude as the
[backend](backends.md). The API key is
[injected](authentication.md#inject) from an environment variable.

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
      backend: anthropic

backends:
  - name: anthropic
    protocol: anthropic
    base_url: https://api.anthropic.com
    auth:
      mode: inject
      header: X-Api-Key
      value: ${ANTHROPIC_API_KEY}
    default_headers:
      anthropic-version: "2023-06-01"
```

Test it:

```sh
curl http://localhost:9090/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "claude-sonnet-4-6",
    "messages": [
      {"role": "system", "content": "You are a helpful assistant."},
      {"role": "user", "content": "Explain what a proxy server does."}
    ]
  }'
```

## Multi-backend with hooks

A more complete setup with [routing](routing.md), [hooks](hooks.md), and
[cross-protocol translation](cross-protocol.md). Chat goes to Anthropic.
Embeddings and model listing go directly to OpenAI. Request logging is on, and
a project codename is redacted from all traffic.

```yaml
server:
  listen: ":9090"
  read_timeout: 60s
  write_timeout: 0s
  idle_timeout: 120s

routing:
  routes:
    - name: chat
      priority: 200
      inbound_protocol: openai
      path_prefix: /v1/chat/completions
      endpoint_kinds: [chat]
      backend: anthropic

    - name: embeddings
      priority: 100
      inbound_protocol: openai
      path_prefix: /v1/embeddings
      endpoint_kinds: [embedding]
      backend: openai

    - name: models
      priority: 100
      inbound_protocol: openai
      path_prefix: /v1/models
      endpoint_kinds: [model_list]
      backend: openai

backends:
  - name: anthropic
    protocol: anthropic
    base_url: https://api.anthropic.com
    auth:
      mode: prefer_client
      header: X-Api-Key
      value: ${ANTHROPIC_API_KEY}
    default_headers:
      anthropic-version: "2023-06-01"

  - name: openai
    protocol: openai
    base_url: https://api.openai.com
    auth:
      mode: inject
      header: Authorization
      value: Bearer ${OPENAI_API_KEY}

hooks:
  - name: logger
    type: logging
    stage: pre_request
    enabled: true
    config:
      verbose: true

  - name: redact-request
    type: request_text_modifier
    stage: pre_request
    enabled: true
    config:
      target: all
      replacements:
        "Project Falcon": "[internal project]"

  - name: redact-response
    type: response_text_modifier
    stage: post_response
    enabled: true
    config:
      replacements:
        "Project Falcon": "[internal project]"

  - name: redact-stream
    type: stream_text_modifier
    stage: stream_event
    enabled: true
    config:
      replacements:
        "Project Falcon": "[internal project]"

  - name: custom-headers
    type: header_modifier
    stage: post_response
    enabled: true
    config:
      phase: response
      add:
        X-Powered-By: insightlayer
      remove:
        - Server
```

Test a chat request (goes to Anthropic):

```sh
curl http://localhost:9090/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer sk-ant-your-key-here" \
  -d '{
    "model": "claude-sonnet-4-6",
    "messages": [
      {"role": "user", "content": "Summarize Project Falcon in one sentence."}
    ]
  }'
```

What happens:

1. InsightLayer receives the OpenAI-format request.
2. The logging hook prints the request metadata.
3. The text modifier replaces "Project Falcon" with "[internal project]" before
   the request leaves the proxy.
4. The `Authorization: Bearer sk-ant-your-key-here` header is
   [translated](authentication.md#cross-protocol-credential-translation) to
   `X-Api-Key: sk-ant-your-key-here` for the Anthropic backend. Since
   `prefer_client` mode is set, the client's key takes precedence over the
   configured fallback.
5. The request is encoded as an Anthropic `/v1/messages` call and sent upstream.
6. The response is decoded, the text modifier redacts any mentions of
   "Project Falcon", the header modifier adds `X-Powered-By` and strips
   `Server`, and the result is encoded back as an OpenAI chat completion.

## Request tracing

InsightLayer generates a unique request ID for every request and includes it in
the `X-Request-Id` response header. If the client sends its own, that value is
used instead. The request ID appears in all [log lines](configuration.md#logging)
for that request, which makes tracing straightforward.

You can combine this with the [header modifier hook](hooks.md#header_modifier) to
inject tracing headers:

```yaml
hooks:
  - name: tracing
    type: header_modifier
    stage: pre_request
    enabled: true
    config:
      phase: request
      add:
        X-Tenant: production
```

OpenTelemetry headers (`Traceparent` and `Tracestate`) are
[propagated automatically](backends.md#header-propagation) from the client to the
backend and back, without any hook configuration.
