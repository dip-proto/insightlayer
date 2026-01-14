package httpserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/j/insightlayer/internal/config"
)

type Server struct {
	http   *http.Server
	mux    *http.ServeMux
	logger *slog.Logger
}

func New(cfg config.ServerConfig, logger *slog.Logger) *Server {
	mux := http.NewServeMux()
	s := &Server{
		http: &http.Server{
			Addr:         cfg.Listen,
			Handler:      mux,
			ReadTimeout:  cfg.ReadTimeout,
			WriteTimeout: cfg.WriteTimeout,
			IdleTimeout:  cfg.IdleTimeout,
		},
		mux:    mux,
		logger: logger,
	}
	s.registerHealthRoutes()
	return s
}

func (s *Server) Mux() *http.ServeMux {
	return s.mux
}

func (s *Server) Handle(pattern string, handler http.Handler) {
	s.mux.Handle(pattern, handler)
}

func (s *Server) registerHealthRoutes() {
	s.mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, `{"status":"ok"}`)
	})
}

func (s *Server) ListenAndServe() error {
	s.logger.Info("server starting", "addr", s.http.Addr)
	err := s.http.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (s *Server) Shutdown(timeout time.Duration) error {
	s.logger.Info("server shutting down", "timeout", timeout)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return s.http.Shutdown(ctx)
}
