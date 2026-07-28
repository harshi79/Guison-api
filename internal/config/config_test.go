package config

import (
	"strings"
	"testing"
)

func setValidEnvironment(t *testing.T) {
	t.Helper()
	t.Setenv("DATABASE_URL", "postgres://example")
	t.Setenv("DATA_ADMIN_PASSWORD", "a-secure-password-for-tests")
	t.Setenv("HTTP_ADDR", ":8080")
	t.Setenv("SYNC_ENABLED", "true")
	t.Setenv("SYNC_ON_START", "true")
	t.Setenv("SYNC_INTERVAL", "5m")
	t.Setenv("MAX_DOWNLOAD_BYTES", "104857600")
	t.Setenv("MAX_INVALID_RATIO", "0.05")
	t.Setenv("SHUTDOWN_TIMEOUT", "15s")
	t.Setenv("DATABASE_MAX_CONNS", "10")
	t.Setenv("SOURCES_FILE", "")
	t.Setenv("SOURCES_JSON", `[{"id":"test","repository":"owner/repo","branch":"main","path":"bins.csv","format":"binlist","priority":10,"min_records":1}]`)
}

func TestLoadValidConfiguration(t *testing.T) {
	setValidEnvironment(t)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.SyncEnabled || !cfg.SyncOnStart || len(cfg.Sources) != 1 {
		t.Fatalf("unexpected config: %+v", cfg)
	}
}

func TestLoadRejectsInvalidEnvironmentValue(t *testing.T) {
	setValidEnvironment(t)
	t.Setenv("SYNC_ENABLED", "sometimes")
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "SYNC_ENABLED") {
		t.Fatalf("expected clear SYNC_ENABLED error, got %v", err)
	}
}

func TestLoadRequiresAdminPassword(t *testing.T) {
	setValidEnvironment(t)
	t.Setenv("DATA_ADMIN_PASSWORD", "")
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "DATA_ADMIN_PASSWORD is required") {
		t.Fatalf("expected password error, got %v", err)
	}
}

func TestLoadRejectsExamplePassword(t *testing.T) {
	setValidEnvironment(t)
	t.Setenv("DATA_ADMIN_PASSWORD", "replace-with-a-long-random-password")
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "example value") {
		t.Fatalf("expected example password error, got %v", err)
	}
}

func TestLoadRejectsUnknownSourceField(t *testing.T) {
	setValidEnvironment(t)
	t.Setenv("SOURCES_JSON", `[{"id":"test","repository":"owner/repo","path":"bins.csv","unexpected":true}]`)
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("expected unknown field error, got %v", err)
	}
}

func TestLoadReservesManualPriority(t *testing.T) {
	setValidEnvironment(t)
	t.Setenv("SOURCES_JSON", `[{"id":"test","repository":"owner/repo","path":"bins.csv","priority":1000}]`)
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "priority") {
		t.Fatalf("expected priority error, got %v", err)
	}
}
