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
	"github.com/harshi79/project-17/internal/syncer"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))

	cfg, err := config.Load()
	if err != nil {
		slog.Error("invalid configuration", "error", err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	db, err := database.Open(ctx, cfg.DatabaseURL, cfg.DatabaseMaxConns)
	if err != nil {
		slog.Error("database startup failed", "error", err)
		os.Exit(1)
	}
	defer db.Close()
	if err := db.ConfigureSources(ctx, cfg.Sources); err != nil {
		slog.Error("source configuration failed", "error", err)
		os.Exit(1)
	}

	github := syncer.NewGitHubClient(cfg.GitHubToken, cfg.MaxDownloadBytes)
	dataImporter := syncer.New(db.Pool, github, cfg.Sources, cfg.SyncInterval, cfg.MaxInvalidRatio, cfg.MaxDownloadBytes)
	if cfg.SyncEnabled {
		dataImporter.Run(ctx, cfg.SyncOnStart)
	}

	api := httpapi.New(db, dataImporter, cfg.DataAdminPassword, cfg.MaxDownloadBytes)
	server := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           api.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       5 * time.Minute,
		WriteTimeout:      5 * time.Minute,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    16 << 10,
	}
	go func() {
		slog.Info("API listening", "address", cfg.HTTPAddr, "automatic_import", cfg.SyncEnabled)
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
}
