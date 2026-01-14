package main

import (
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/j/insightlayer/internal/app"
	"github.com/j/insightlayer/internal/config"
	"github.com/j/insightlayer/internal/httpserver"
	"github.com/j/insightlayer/internal/observability"
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to configuration file")
	flag.Parse()

	logger := observability.NewLogger(slog.LevelInfo)

	cfg, err := config.Load(*configPath)
	if err != nil {
		logger.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	handler, err := app.NewHandler(cfg, logger)
	if err != nil {
		logger.Error("failed to initialize handler", "error", err)
		os.Exit(1)
	}

	srv := httpserver.New(cfg.Server, logger)
	srv.Handle("/v1/", handler)

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.ListenAndServe()
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-quit:
		logger.Info("received signal", "signal", sig)
	case err := <-errCh:
		if err != nil {
			logger.Error("server error", "error", err)
			os.Exit(1)
		}
	}

	if err := srv.Shutdown(15 * time.Second); err != nil {
		logger.Error("shutdown error", "error", err)
		os.Exit(1)
	}

	logger.Info("server stopped")
}
