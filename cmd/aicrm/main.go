package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformruntime "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/runtime"
)

func main() {
	if err := run(); err != nil {
		slog.Error("aicrm stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := platformconfig.Load()
	if err != nil {
		return err
	}

	// Bootstrap has no external readiness dependency yet. PostgreSQL, migrations,
	// Outbox and workers replace this checker in the first platform capability PR.
	handler, err := platformruntime.NewHandler(platformruntime.HandlerOptions{
		ReleaseSHA: cfg.ReleaseSHA,
		Readiness:  platformruntime.ReadinessFunc(func(context.Context) error { return nil }),
	})
	if err != nil {
		return err
	}

	server := &http.Server{
		Addr:              cfg.ListenAddress,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		slog.Info("aicrm started", "address", cfg.ListenAddress, "role", cfg.Role, "release_sha", cfg.ReleaseSHA)
		errCh <- server.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	case err = <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
