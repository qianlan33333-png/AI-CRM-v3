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
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	application, err := compose(ctx, cfg)
	if err != nil {
		return err
	}
	defer application.Close()
	if cfg.Role == platformconfig.RoleWorker {
		if !cfg.WeCom.Enabled {
			slog.Info("wecom worker skipped because provider is disabled", "release_sha", cfg.ReleaseSHA)
			return nil
		}
		processed, processErr := application.weComProcessor.ProcessOnce(ctx, cfg.WorkerOwner, cfg.WorkerLimit)
		if processErr == nil {
			slog.Info("wecom worker complete", "processed", processed, "release_sha", cfg.ReleaseSHA)
		}
		return processErr
	}
	if cfg.Role == platformconfig.RoleEffectsWorker {
		return application.effectsRuntime.Run(ctx)
	}
	if err = application.bootstrap(ctx, cfg.Bootstrap); err != nil {
		return err
	}

	server := &http.Server{
		Addr:              cfg.ListenAddress,
		Handler:           application.handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

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
