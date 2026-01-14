# Hooks

Hooks let you inspect and modify requests and responses as they pass through the
[pipeline](cross-protocol.md). Each hook runs at a specific stage, operates on
the protocol-neutral normalized representation, and can be toggled on or off
without removing it from the config.

```yaml
hooks:
  - name: my-hook
    type: logging
    stage: pre_request
    enabled: true
    config:
      verbose: false
```

Hooks execute in the order they appear in the config file. If a hook returns an
error, the pipeline stops and the error is sent back to the client. You can set
`enabled: false` to disable a hook without deleting its configuration, which is
useful for toggling things like verbose logging during debugging.

## Stages

There are four stages where hooks can run.

**pre_request** runs after the request is decoded but before it is sent to the
backend. This is where you log, redact, or rewrite request content.

**post_response** runs after the backend response is decoded but before it is
sent to the client. This is where you redact or transform response content.

**stream_event** runs once per [streaming](streaming.md) event, between decoding
from the backend and encoding for the client.

**error** runs whenever an error occurs anywhere in the pipeline. Error hooks
receive the error object and can inspect or modify it before it reaches the
client.

Because hooks operate on the normalized representation rather than the raw wire
format, the same hook works regardless of which protocols are involved. A text
redaction hook does the same thing whether traffic is OpenAI-to-OpenAI,
OpenAI-to-Anthropic, or Anthropic-to-OpenAI. See
[Cross-Protocol Translation](cross-protocol.md) for how the pipeline works.

## logging

The logging hook writes structured log lines for each request. It logs the
model, endpoint kind, whether streaming is enabled, and the number of messages.
Set `verbose: true` to also include the system prompt in the log output.

```yaml
hooks:
  - name: request-logger
    type: logging
    stage: pre_request
    enabled: true
    config:
      verbose: false
```

This hook is stage `pre_request` only. It is read-only and does not modify the
request.

## request_text_modifier

Performs find-and-replace text substitutions on request content before it
reaches the backend. You can target all message content, or limit it to specific
roles.

```yaml
hooks:
  - name: redact-secrets
    type: request_text_modifier
    stage: pre_request
    enabled: true
    config:
      target: all
      replacements:
        "sk-live-abc123": "[REDACTED]"
        "internal.corp.net": "api.example.com"
```

The `target` field accepts `all`, `system`, `user`, or `assistant`. When set to
`all`, replacements are applied to the system prompt and to every message
regardless of role. When set to a specific role, only messages with that role
are modified.

This is useful for redacting secrets that might appear in user messages,
rewriting internal hostnames before they leave your network, or enforcing naming
conventions.

### Modifying the system prompt

Since `target: system` applies replacements directly to the system prompt field
of the normalized request, you can use `request_text_modifier` to inject,
rewrite, or prepend content to system prompts before they reach the backend.

For example, to prepend a safety preamble to every request's system prompt:

```yaml
hooks:
  - name: system-prompt-preamble
    type: request_text_modifier
    stage: pre_request
    enabled: true
    config:
      target: system
      replacements:
        "": "Always respond in a safe and helpful manner.\n"
```

Or to enforce a specific instruction across all routes:

```yaml
hooks:
  - name: enforce-language
    type: request_text_modifier
    stage: pre_request
    enabled: true
    config:
      target: system
      replacements:
        "": "Respond in French.\n"
```

For more complex logic — like conditionally setting the system prompt or
replacing it entirely based on request metadata — write a custom hook that
implements the `PreRequestHook` interface. Pre-request hooks receive a pointer
to the `NormalizedRequest`, so `SystemPrompt`, `Messages`, and all other fields
are directly mutable.

## response_text_modifier

The same idea as `request_text_modifier`, but for responses. Replacements are
applied to the response content and to every choice in multi-choice completions.

```yaml
hooks:
  - name: response-redaction
    type: response_text_modifier
    stage: post_response
    enabled: true
    config:
      replacements:
        "SECRET_PROJECT": "[redacted]"
```

## stream_text_modifier

Applies text replacements to each streaming text delta event. Only `text_delta`
events are affected; start, stop, and usage events pass through unchanged.

```yaml
hooks:
  - name: stream-redaction
    type: stream_text_modifier
    stage: stream_event
    enabled: true
    config:
      replacements:
        "SECRET_PROJECT": "[redacted]"
```

If you are redacting text in both streaming and non-streaming responses, you
typically want both a `response_text_modifier` and a `stream_text_modifier`
with the same replacements, since they cover different code paths.

## header_modifier

Adds or removes HTTP headers on requests or responses. The `phase` field
determines which set of headers the hook operates on.

For request headers (modifying what gets sent to the backend):

```yaml
hooks:
  - name: add-tracing-headers
    type: header_modifier
    stage: pre_request
    enabled: true
    config:
      phase: request
      add:
        X-Tenant: production
        X-Source: insightlayer
      remove:
        - X-Internal-Debug
```

For response headers (modifying what gets sent back to the client):

```yaml
hooks:
  - name: response-headers
    type: header_modifier
    stage: post_response
    enabled: true
    config:
      phase: response
      add:
        Cache-Control: no-store
      remove:
        - Server
```

The `add` map sets headers to the specified values. The `remove` list deletes
headers by name. Removals are applied after additions, so you can replace a
header by adding a new value and then removing the old name if needed.

## Combining hooks

Hooks run in the order they appear in the config. You can chain multiple hooks
at the same stage. For example, you might log first, then redact:

```yaml
hooks:
  - name: logger
    type: logging
    stage: pre_request
    enabled: true
    config:
      verbose: true

  - name: redact
    type: request_text_modifier
    stage: pre_request
    enabled: true
    config:
      target: all
      replacements:
        "SECRET_KEY": "[REDACTED]"
```

In this setup, the logger sees the original request content (before redaction),
which can be useful for debugging. If you want the logger to see the redacted
version instead, swap the order.
