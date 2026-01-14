# Authentication

InsightLayer supports three authentication modes that control how credentials
flow between the client and the upstream [backend](backends.md).

## inject

The backend always uses the credential you configure, regardless of what the
client sends. This is the default mode if you do not specify one.

```yaml
auth:
  mode: inject
  header: Authorization
  value: Bearer ${OPENAI_API_KEY}
```

Use this when you want to manage API keys centrally. Clients do not need to know
or send any credentials -- InsightLayer injects the right one on their behalf.

## passthrough

The client's credential is forwarded to the backend as-is. If the client does
not send a credential, no credential is sent upstream. There is no fallback.

```yaml
auth:
  mode: passthrough
```

Use this for local servers that do not require authentication, or in setups
where every client brings their own API key and you do not want InsightLayer to
store any secrets.

## prefer_client

If the client sends a credential, use it. If not, fall back to the configured
one.

```yaml
auth:
  mode: prefer_client
  header: X-Api-Key
  value: ${ANTHROPIC_FALLBACK_KEY}
```

This is handy when you want to let power users bring their own keys while
providing a shared fallback for everyone else. It also works well for
development environments where some developers have their own API keys and
others use a team-shared one.

## Cross-protocol credential translation

When a request crosses protocol boundaries (see
[Cross-Protocol Translation](cross-protocol.md)), InsightLayer translates
credentials automatically. OpenAI uses `Authorization: Bearer <key>` headers, while
Anthropic uses `X-Api-Key: <key>` headers. InsightLayer handles the conversion
in both directions.

If a client sends `Authorization: Bearer sk-abc123` and the backend is
Anthropic, InsightLayer strips the `Bearer ` prefix and sets
`X-Api-Key: sk-abc123` on the upstream request.

Going the other way, if a client sends `X-Api-Key: sk-abc123` and the backend
is OpenAI, InsightLayer adds the `Bearer ` prefix and sets
`Authorization: Bearer sk-abc123`.

You do not need to configure this. It happens whenever the inbound and backend
protocols differ, and it works with all three auth modes.
