# InsightLayer for Fastly Compute

This is the Fastly Compute fork of InsightLayer. It reuses the shared core
(config, routing, protocol codecs, hooks, stream encoders, backend logic)
and adds a thin transport adapter for the Compute runtime.

## Fork-only files

- `cmd/insightlayer-fastly/` — entrypoint and embedded config
- `internal/fastly/` — Compute adapter, stream emitter, transport builder
- `fastly.toml` — Fastly service manifest with backend declarations

Everything else comes from upstream InsightLayer.

## Configuration

Edit `cmd/insightlayer-fastly/config.yaml` before building. This file is
embedded into the WASM binary at compile time.

Backend names in the config must match backend declarations in `fastly.toml`.

## Building

Standard Go (larger binary, works today):

    GOOS=wasip1 GOARCH=wasm go build -o insightlayer.wasm ./cmd/insightlayer-fastly/

TinyGo (smaller binary, requires Go 1.25 until TinyGo adds 1.26 support):

    tinygo build -target=wasip1 -o insightlayer.wasm ./cmd/insightlayer-fastly/

## Local testing with fastlike

    fastlike \
      -wasm insightlayer.wasm \
      -backend openai=https://api.openai.com \
      -bind localhost:8080

Then send requests to `http://localhost:8080/v1/chat/completions` as usual.

## Deploying to Fastly

    fastly compute publish

Make sure `fastly.toml` backend declarations match the backends in your
embedded config.

## Syncing with upstream

See the sync routine in `plans/fastly-compute-port-plan.md`. The short
version: pull `main`, rebase this fork onto it, resolve only fork-local
files. If rebase conflicts touch shared packages, upstream the missing
abstraction first.
