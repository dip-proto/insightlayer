# Configuration

InsightLayer uses a single YAML file for all its settings. You can also use
JSON if you prefer. The config file path defaults to `config.yaml` in the
current directory, but you can override it with the `-config` flag:

```sh
insightlayer -config /etc/insightlayer/production.yaml
```

## Server settings

The `server` section controls the HTTP listener:

```yaml
server:
  listen: ":9090"        # host:port to bind
  read_timeout: 60s      # how long to wait for the request body
  write_timeout: 0s      # response write deadline (0 disables it)
  idle_timeout: 120s     # how long idle connections stay open
```

The defaults are sensible for most setups. If you omit `listen`, it defaults to
`:8080`. If you omit `read_timeout`, it defaults to 60 seconds. If you omit
`idle_timeout`, it defaults to 120 seconds.

The write timeout is zero on purpose. [Streaming](streaming.md) responses are
long-lived SSE connections, and a nonzero write timeout would kill them
mid-stream. The read and idle timeouts still protect you from hung or abandoned
connections.

## Environment variables

You can reference environment variables anywhere in the config file using
`${VAR_NAME}` syntax. Variables are expanded once at startup. If a variable is
not set, the literal `${VAR_NAME}` string is left in place, which usually
results in a clear auth failure rather than a silent misconfiguration.

```yaml
auth:
  mode: inject
  header: Authorization
  value: Bearer ${OPENAI_API_KEY}

default_headers:
  OpenAI-Project: ${OPENAI_PROJECT_ID}
```

This keeps secrets out of version control. Export them in your shell or set them
in your deployment environment, and the config file stays the same everywhere.

## Health check

InsightLayer exposes a health check at `GET /health` that returns
`{"status":"ok"}` with a 200 status code. This endpoint exists outside of the
[routing](routing.md) system, so it works regardless of what routes you have
configured. It is useful for load balancers and orchestration systems that need
to know if the process is alive.

## Graceful shutdown

InsightLayer listens for SIGINT and SIGTERM signals. When it receives one, it
stops accepting new connections and gives in-flight requests up to 15 seconds to
complete before shutting down. This makes it safe to restart behind a load
balancer without dropping requests.

## Logging

All logs are structured JSON written to stderr. Each log line includes a
timestamp, level, message, and any relevant fields like request ID, status code,
or error details. The request ID threads through every log line for a given
request, so you can grep for it to see the full lifecycle.

```json
{"time":"2026-04-01T10:30:00Z","level":"INFO","msg":"server starting","addr":":9090"}
```

## Full example

The `config.example.yaml` file in the repository demonstrates every feature in a
single config. It is intentionally verbose and well-commented. If you are not
sure how a particular option works, that file is a good place to start. You can
also find practical, copy-paste-ready setups on the [Examples](examples.md) page.

## Related pages

The config file has three main sections beyond `server`:

- [Routing](routing.md) -- how incoming requests are matched to backends
- [Backends](backends.md) -- upstream LLM services and their settings
- [Hooks](hooks.md) -- traffic inspection and modification
