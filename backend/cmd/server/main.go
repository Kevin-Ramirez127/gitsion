package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"gitsion/backend/internal/api"
	"gitsion/backend/internal/config"
	"gitsion/backend/internal/github"
	"gitsion/backend/internal/service"
	"gitsion/backend/internal/store"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "gitsion: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("configuration: %w", err)
	}

	logger := newLogger(cfg.LogLevel)
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	connectCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	st, err := store.NewMongo(connectCtx, cfg.MongoURI, cfg.MongoDB)
	if err != nil {
		return err
	}
	defer func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer closeCancel()
		if err := st.Close(closeCtx); err != nil {
			logger.Warn("mongo disconnect failed", "error", err)
		}
	}()

	if err := st.EnsureIndexes(connectCtx); err != nil {
		return err
	}

	gh := github.NewClient(cfg.GitHubAPIURL, cfg.GitHubToken)
	tracker := service.NewTracker(st, gh, logger, cfg.SyncInterval, cfg.Concurrency)
	tracker.Start(ctx)

	handler := api.NewRouter(tracker, logger)
	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       90 * time.Second,
	}

	logger.Info("gitsion backend started",
		"port", cfg.Port,
		"database", cfg.MongoDB,
		"sync_interval", cfg.SyncInterval.String(),
		"sync_concurrency", cfg.Concurrency,
		"github_token", cfg.GitHubToken != "",
	)
	if cfg.GitHubToken == "" {
		logger.Warn("GITHUB_TOKEN is not set: public repositories only, limited to 60 GitHub requests/hour")
	}
	logger.Info("dashboard is public: no login is required")

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("http server: %w", err)
	case <-ctx.Done():
		logger.Info("shutdown signal received")
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	logger.Info("gitsion backend stopped")
	return nil
}

func newLogger(level string) *slog.Logger {
	var l slog.Level
	switch level {
	case "debug":
		l = slog.LevelDebug
	case "warn":
		l = slog.LevelWarn
	case "error":
		l = slog.LevelError
	default:
		l = slog.LevelInfo
	}
	return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: l}))
}
