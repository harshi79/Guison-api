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

	"github.com/harshi79/project-17/internal/config"
	"github.com/harshi79/project-17/internal/database"
	"github.com/harshi79/project-17/internal/httpapi"
	"github.com/harshi79/project-17/internal/syncer"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))
	if err := run(); err != nil {
		slog.Error("application stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("invalid configuration: %w", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	db, err := database.Open(ctx, cfg.TursoDatabaseURL, cfg.TursoAuthToken)
	if err != nil {
		return fmt.Errorf("database startup: %w", err)
	}
	defer db.Close()
	if err := db.ConfigureSources(ctx, cfg.Sources); err != nil {
		return fmt.Errorf("configure sources: %w", err)
	}

	github := syncer.NewGitHubClient(cfg.GitHubToken, cfg.MaxDownloadBytes)
	dataImporter := syncer.New(db.DB(), github, cfg.Sources, cfg.SyncInterval, cfg.MaxInvalidRatio, cfg.MaxDownloadBytes)
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
	serveErrors := make(chan error, 1)
	go func() {
		slog.Info("API listening", "address", cfg.HTTPAddr, "automatic_import", cfg.SyncEnabled)
		serveErrors <- server.ListenAndServe()
	}()

	var serveErr error
	select {
	case <-ctx.Done():
	case err := <-serveErrors:
		if !errors.Is(err, http.ErrServerClosed) {
			serveErr = fmt.Errorf("HTTP server: %w", err)
		}
		stop()
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	shutdownErr := server.Shutdown(shutdownCtx)
	if shutdownErr != nil {
		_ = server.Close()
	}
	dataImporter.Wait()

	if serveErr != nil {
		return serveErr
	}
	if shutdownErr != nil {
		return fmt.Errorf("HTTP shutdown: %w", shutdownErr)
	}
	return nil
}
