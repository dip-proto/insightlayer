# Using Claude Code with local models

Claude Code speaks the Anthropic API protocol. Local model servers like
LM Studio, ollama, and llama.cpp speak OpenAI. InsightLayer sits between the
two and translates on the fly.

```
Claude Code  --(Anthropic)-->  InsightLayer  --(OpenAI)-->  LM Studio
```

## Configuration

Save this as `config.lmstudio.yaml` (or any name you like):

```yaml
server:
  listen: ":8080"
  read_timeout: 120s
  idle_timeout: 300s
  write_timeout: 0s

routing:
  routes:
    - name: claude-code-to-lmstudio
      priority: 200
      inbound_protocol: anthropic
      path_prefix: /v1/messages
      endpoint_kinds: [chat]
      backend: lmstudio

backends:
  - name: lmstudio
    protocol: openai
    base_url: http://127.0.0.1:1234
    auth:
      mode: inject
      header: Authorization
      value: Bearer lm-studio
```

Adjust `base_url` if your model server listens on a different port. The auth
value does not matter for LM Studio, but the field is required.

## Running it

Start InsightLayer in one terminal:

```sh
insightlayer -config config.lmstudio.yaml
```

Then start Claude Code in another, pointing it at InsightLayer:

```sh
ANTHROPIC_BASE_URL=http://127.0.0.1:8080 ANTHROPIC_API_KEY=dummy claude
```

`ANTHROPIC_API_KEY` can be any non-empty string. InsightLayer ignores the
client's key and injects its own for the backend (see the `inject` auth mode
in [authentication](authentication.md)).

## Making it persistent

If you don't want to set environment variables every time, create a
`.claude/settings.local.json` file in whichever project you want to use this
from:

```json
{
  "env": {
    "ANTHROPIC_BASE_URL": "http://127.0.0.1:8080",
    "ANTHROPIC_API_KEY": "dummy"
  }
}
```

This file is gitignored by convention, so it stays local to your machine.

## Model names

Claude Code sends model names like `claude-opus-4-6` or `claude-sonnet-4-6`.
LM Studio receives them as-is and routes to whichever model is currently
loaded. Most local servers ignore the model name entirely when only one model
is loaded, so this usually just works.

If you need to rewrite the model name, add a [pre-request hook](hooks.md)
that modifies the normalized request before it hits the backend.

## Streaming

Streaming works out of the box. Claude Code sends `"stream": true` in its
requests. InsightLayer translates between Anthropic SSE events and OpenAI SSE
chunks transparently. See [Streaming](streaming.md) for details on how the
translation works.

## Adding request logging

To see what's going through the proxy, enable the logging hook:

```yaml
hooks:
  - name: logger
    type: logging
    stage: pre_request
    enabled: true
    config:
      verbose: false
```

Set `verbose: true` to also log system prompts, which is useful for debugging
but noisy in normal use.

## Other local servers

The same setup works for any OpenAI-compatible server. Just change the
`base_url`:

| Server    | Default URL              |
| --------- | ------------------------ |
| LM Studio | `http://127.0.0.1:1234`  |
| ollama    | `http://127.0.0.1:11434` |
| llama.cpp | `http://127.0.0.1:8080`  |
| vLLM      | `http://127.0.0.1:8000`  |
| LocalAI   | `http://127.0.0.1:8080`  |

When using ollama, the OpenAI-compatible endpoint is at `/v1`, so
`base_url: http://127.0.0.1:11434` works directly.
