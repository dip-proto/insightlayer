package main

import (
	_ "embed"
	"log/slog"
	"net/http"
	"net/url"
	"os"

	"github.com/fastly/compute-sdk-go/fsthttp"

	"github.com/j/insightlayer/internal/app"
	"github.com/j/insightlayer/internal/config"
	fastlyadapter "github.com/j/insightlayer/internal/fastly"
)

//go:embed config.yaml
var configData []byte

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, err := config.Parse(configData, "yaml")
	if err != nil {
		logger.Error("config parse failed", "error", err)
		return
	}

	transport := buildTransport(cfg)
	client := &http.Client{Transport: transport}

	handler, err := app.NewHandlerWithOptions(cfg, logger, app.HandlerOptions{
		HTTPClient: client,
	})
	if err != nil {
		logger.Error("handler init failed", "error", err)
		return
	}

	adapter := fastlyadapter.NewAdapter(handler)

	fsthttp.Serve(fsthttp.HandlerFunc(adapter.ServeHTTP))
}

func buildTransport(cfg *config.Config) *fsthttp.Transport {
	backends := make(map[string]*url.URL, len(cfg.Backends))
	for _, b := range cfg.Backends {
		u, err := url.Parse(b.BaseURL)
		if err != nil {
			continue
		}
		backends[b.Name] = u
	}
	return fastlyadapter.BuildTransport(backends)
}
