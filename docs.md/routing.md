# Routing

Routes tell InsightLayer how to match incoming requests to
[backends](backends.md). Each route has a name, a priority, an inbound protocol,
a path prefix, the endpoint kinds it handles, and the name of the backend it
forwards to.

```yaml
routing:
  routes:
    - name: chat
      priority: 100
      inbound_protocol: openai
      path_prefix: /v1/chat/completions
      endpoint_kinds: [chat]
      backend: my-backend
```

## How matching works

When a request comes in, InsightLayer walks the routes in order of priority
(highest first). If two routes share the same priority, the one with the longer
path prefix wins. The first route whose prefix matches the request path, and
whose endpoint kinds include the detected endpoint type, handles the request.

The endpoint kind is inferred from the path automatically. For OpenAI-protocol
requests:

- `/chat/completions` in the path means `chat`
- `/completions` (without `/chat/`) means `completion`
- `/embeddings` means `embedding`
- `/models` means `model_list`

For Anthropic-protocol requests, `/messages` in the path means `chat`. The
Anthropic API only has one endpoint type.

If a route has only one endpoint kind and the path does not clearly map to a
known kind, the route's single kind is used as a fallback. This lets you write
simpler configs for common single-purpose routes.

## Ambiguity detection

If you define routes that would be ambiguous -- same priority, same prefix
length, same protocol, and overlapping endpoint kinds -- InsightLayer rejects
the config at startup rather than picking one silently. This prevents surprises
in production. You can resolve ambiguity by giving one route a higher priority,
using a more specific path prefix, or splitting the endpoint kinds.

## Multiple routes

You can define as many routes as you need. A common pattern is to have a
high-priority route for chat that does cross-protocol translation, and
lower-priority routes for passthrough endpoints like embeddings and model
listing:

```yaml
routing:
  routes:
    - name: chat-to-anthropic
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
      backend: openai-direct

    - name: models
      priority: 100
      inbound_protocol: openai
      path_prefix: /v1/models
      endpoint_kinds: [model_list]
      backend: openai-direct
```

In this setup, chat requests go to an Anthropic backend with
[protocol translation](cross-protocol.md), while embedding and model listing
requests go directly to an OpenAI-compatible backend.

## Catch-all routes

You can set a low-priority route with a broad path prefix to catch anything
that does not match a more specific route:

```yaml
routing:
  routes:
    - name: chat-bridge
      priority: 200
      inbound_protocol: openai
      path_prefix: /v1/chat/completions
      endpoint_kinds: [chat]
      backend: anthropic

    - name: everything-else
      priority: 50
      inbound_protocol: openai
      path_prefix: /v1
      endpoint_kinds: [chat, completion, embedding, model_list]
      backend: openai-fallback
```

The `everything-else` route handles completions, embeddings, and model listing.
Chat requests still go to the higher-priority `chat-bridge` route.

## Namespaced routes

If you want to expose multiple backends from the same InsightLayer instance, you
can use different path prefixes as namespaces:

```yaml
routing:
  routes:
    - name: openai-chat
      priority: 100
      inbound_protocol: openai
      path_prefix: /openai/v1/chat/completions
      endpoint_kinds: [chat]
      backend: openai-backend

    - name: anthropic-chat
      priority: 100
      inbound_protocol: openai
      path_prefix: /v1/chat/completions
      endpoint_kinds: [chat]
      backend: anthropic-backend
```

Clients that hit `/openai/v1/chat/completions` go to OpenAI. Clients that hit
`/v1/chat/completions` go to Anthropic. Both routes can have the same priority
because they have different prefix lengths, so there is no ambiguity.
