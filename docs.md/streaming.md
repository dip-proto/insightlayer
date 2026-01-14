# Streaming

Streaming works transparently for both same-protocol and
[cross-protocol](cross-protocol.md) requests. When the client sets
`"stream": true`, InsightLayer opens a streaming connection to the
[backend](backends.md), translates each event into the client's protocol on the
fly, and forwards it as a server-sent event.

## Trying it out

The simplest way to see streaming in action is with curl. The `-N` flag disables
output buffering so you see tokens as they arrive:

```sh
curl -N http://localhost:9090/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "qwen3.5-9b-unsloth-mlx",
    "messages": [{"role": "user", "content": "Count from 1 to 5."}],
    "stream": true
  }'
```

## Wire formats

The two protocols have quite different streaming formats, and InsightLayer
translates between them in both directions.

OpenAI streaming uses `chat.completion.chunk` events with delta objects, ending
with a `data: [DONE]` sentinel:

```text
data: {"id":"...","object":"chat.completion.chunk","choices":[{"delta":{"content":"Hello"},"finish_reason":null}]}

data: {"id":"...","object":"chat.completion.chunk","choices":[{"delta":{},"finish_reason":"stop"}]}

data: [DONE]
```

Anthropic streaming uses a sequence of typed events --
`message_start`, `content_block_start`, `content_block_delta`,
`content_block_stop`, `message_delta`, and `message_stop`:

```text
event: message_start
data: {"type":"message_start","message":{"id":"...","role":"assistant",...}}

event: content_block_delta
data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"Hello"}}

event: message_stop
data: {"type":"message_stop"}
```

When a client speaks OpenAI and the backend speaks Anthropic (or vice versa),
InsightLayer decodes the backend's stream format, passes each event through any
configured [stream hooks](hooks.md), and re-encodes it into the client's format.
The client only ever sees events in its own protocol.

## Stream hooks

Stream event hooks run once per event, between decoding from the backend and
encoding for the client. Only `text_delta` events are passed to text modifier
hooks; start, stop, and usage events pass through unchanged. See
[Hooks](hooks.md) for configuration.

## Error handling

If an error occurs mid-stream -- the backend drops the connection, a hook
fails, or something else goes wrong -- InsightLayer sends the error as an event
in the client's format and closes the connection. This is better than silently
hanging, because the client can detect the error and decide what to do.

## HTTP headers

Streaming responses are sent with these headers:

- `Content-Type: text/event-stream`
- `Cache-Control: no-cache`
- `Connection: keep-alive`

The status code is 200 and is sent before the first event, since HTTP does not
allow changing the status code once headers are written.

Note that the [server write timeout](configuration.md) should be set to `0s`
when streaming is in use, otherwise long-running streams will be killed
mid-response.
