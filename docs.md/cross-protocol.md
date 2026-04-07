# Cross-Protocol Translation

This is the main feature of InsightLayer. It translates between the OpenAI and
Anthropic wire formats in both directions, for both streaming and non-streaming
requests.

## How it works

Every request goes through a normalize-then-encode pipeline:

1. The incoming request is decoded from its wire format (OpenAI or Anthropic)
   into a protocol-neutral representation.
2. [Pre-request hooks](hooks.md) run on the normalized request.
3. The normalized request is re-encoded into the [backend's](backends.md) wire
   format and sent upstream.
4. The response is decoded from the backend's format into a normalized
   representation.
5. [Post-response hooks](hooks.md) (or [stream hooks](hooks.md), for
   [streaming](streaming.md)) run on the normalized response.
6. The normalized response is re-encoded into the client's wire format and sent
   back.

This means [hooks](hooks.md) always operate on the same normalized types
regardless of which protocols are involved. A hook that redacts text works the
same whether the traffic is OpenAI-to-OpenAI, OpenAI-to-Anthropic, or
Anthropic-to-OpenAI.

## OpenAI client to Anthropic backend

This is probably the most common cross-protocol setup. Your application speaks
OpenAI, and you want to use Claude as the backend.

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

With this config, you can use any OpenAI-compatible client library and it will
talk to Claude:

```sh
curl http://localhost:9090/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "claude-sonnet-4-6",
    "messages": [
      {"role": "system", "content": "You are a helpful assistant."},
      {"role": "user", "content": "Explain what a proxy server does."}
    ],
    "stream": false
  }'
```

The response comes back as a standard OpenAI `chat.completion` object, even
though the actual work was done by Claude.

### What gets translated

When going from OpenAI to Anthropic:

- The system message (role `"system"`) is extracted from the messages array and
  sent as Anthropic's separate `system` parameter.
- Remaining messages are mapped to Anthropic's format.
- If `max_tokens` is not set, InsightLayer defaults it to 1024, since the
  Anthropic API requires that field.
- Inference parameters like `temperature`, `top_p`, and `stop` are passed
  through when present.
- The finish reason is mapped: `stop` becomes `end_turn`, `length` becomes
  `max_tokens`, and vice versa on the way back.

Authentication credentials are also translated automatically between
`Authorization: Bearer` and `X-Api-Key` headers. See
[Authentication](authentication.md) for details.

### What does not get translated

InsightLayer does not perform template conversion. Tool definitions, tool calls,
and tool results are mapped between protocol field names (for example,
`parameters` becomes `input_schema` and vice versa), but the schemas themselves
are passed through as-is with no structural transformation.

This matters most for tool calling. Models differ in how they expect tool
definitions to be described, and some backends (local inference servers in
particular) rely on chat templates to reshape tool schemas into a format the
model was trained on. InsightLayer does not apply or convert these templates, so
if your backend doesn't handle that step itself, tool calling may not work
as expected.

## Anthropic client to OpenAI backend

The reverse direction works just as well. If your client speaks the Anthropic
protocol but you want to hit an OpenAI-compatible backend:

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

Anthropic-format requests to `/v1/messages` are translated to OpenAI format,
sent to the local server, and the response is translated back to Anthropic
format before reaching the client.

This is useful if you have tooling that only speaks the Anthropic API and you
want to point it at a local model or an OpenAI-compatible server.

## Passthrough endpoints

Embedding and model listing endpoints skip the decode/encode pipeline entirely.
The request body is forwarded as-is to the backend, and the response comes back
unchanged. This only works when the client and backend share the same protocol.
If they differ, InsightLayer returns an error explaining that cross-protocol
translation is not supported for that endpoint type.

Pre-request and post-response [hooks](hooks.md) still run on passthrough
endpoints, but they operate on a minimal normalized representation (just headers
and model name) rather than the full decoded request.

## Streaming

Cross-protocol translation works for streaming requests too. When the client
sets `"stream": true`, InsightLayer opens a streaming connection to the backend,
decodes each event from the backend's format, translates it into the client's
format, and sends it as a server-sent event.

For more details on how streaming works, see [Streaming](streaming.md).
