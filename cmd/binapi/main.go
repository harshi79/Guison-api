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

	"github.com/harshi79/project-17/internal/config"
	"github.com/harshi79/project-17/internal/database"
	"github.com/harshi79/project-17/internal/httpapi"
	"github.com/harshi79/project-17/internal/lookup"
	"github.com/harshi79/project-17/internal/syncer"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	cfg, err := config.Load()
	if err != nil {
		slog.Error("invalid configuration", "error", err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	db, err := database.Open(ctx, cfg.DatabaseURL, cfg.DatabaseMinConns, cfg.DatabaseMaxConns)
	if err != nil {
		slog.Error("database startup failed", "error", err)
		os.Exit(1)
	}
	defer db.Close()
	if err := db.ConfigureSources(ctx, cfg.Sources); err != nil {
		slog.Error("source configuration failed", "error", err)
		os.Exit(1)
	}

	lookupService := lookup.New(db, cfg.CacheTTL, cfg.CacheMaxEntries)
	go lookup.ListenForInvalidations(ctx, db.Pool, lookupService)

	var manager *syncer.Manager
	if cfg.SyncEnabled {
		github := syncer.NewGitHubClient(cfg.GitHubToken, cfg.MaxDownloadBytes)
		manager = syncer.New(db.Pool, github, cfg.Sources, cfg.SyncInterval, cfg.MaxInvalidRatio, lookupService)
		manager.Run(ctx, cfg.SyncOnStart)
	}

	api := httpapi.New(lookupService, db, manager, cfg.WebhookSecret, cfg.AdminToken)
	server := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           api.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       20 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    16 << 10,
	}
	go func() {
		slog.Info("API listening", "address", cfg.HTTPAddr, "sync_enabled", cfg.SyncEnabled, "sources", len(cfg.Sources))
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("HTTP server failed", "error", err)
			stop()
		}
	}()

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		slog.Error("HTTP shutdown failed", "error", err)
	}
	slog.Info("shutdown complete")
}
