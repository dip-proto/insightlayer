# Backends

A backend is an upstream LLM service that InsightLayer forwards requests to.
Each backend has a name, a protocol, a base URL, authentication settings, and
optional default headers.

```yaml
backends:
  - name: openai
    protocol: openai
    base_url: https://api.openai.com
    auth:
      mode: inject
      header: Authorization
      value: Bearer ${OPENAI_API_KEY}
    default_headers:
      X-Proxy-Name: insightlayer
```

## Protocol

The `protocol` field tells InsightLayer how to encode requests for this backend
and how to decode its responses. The two options are `openai` and `anthropic`.
This is independent of the client's protocol -- you can route OpenAI-format
requests to an Anthropic backend, or Anthropic-format requests to an OpenAI
backend, and InsightLayer handles the
[translation](cross-protocol.md) automatically.

OpenAI backends support chat, completion, embedding, and model listing
endpoints. Anthropic backends only support chat (the `/v1/messages` endpoint).
If you try to route an embedding or model listing request to an Anthropic
backend, InsightLayer returns an error.

## Base URL

The `base_url` is the root of the upstream API. InsightLayer appends the
appropriate path based on the endpoint type:

- Chat on OpenAI: `/v1/chat/completions`
- Completion on OpenAI: `/v1/completions`
- Embedding on OpenAI: `/v1/embeddings`
- Model list on OpenAI: `/v1/models`
- Chat on Anthropic: `/v1/messages`

For a local server, this might be `http://127.0.0.1:8080`. For OpenAI's API,
it is `https://api.openai.com`. For Anthropic's API, it is
`https://api.anthropic.com`.

## Default headers

Default headers are added to every request sent to this backend. They are useful
for things like project identifiers, version headers, or tracing metadata.

```yaml
default_headers:
  anthropic-version: "2023-06-01"
  X-Proxy-Name: insightlayer
  OpenAI-Project: ${OPENAI_PROJECT_ID}
```

Environment variables in header values are expanded at startup, just like
everywhere else in the [config](configuration.md).

## Authentication

Authentication is covered in detail on its own page. See
[Authentication](authentication.md) for the three auth modes and how
cross-protocol credential translation works.

## Header propagation

Beyond default headers, InsightLayer automatically forwards certain headers from
the client to the backend:

- `X-Request-Id` for request tracing
- `Traceparent` and `Tracestate` for OpenTelemetry distributed tracing
- All `X-` prefixed headers that are not already set by the backend config

This means you can pass custom metadata from the client through the proxy
without any configuration.
