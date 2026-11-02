package config

import (
	"strings"
	"testing"
)

func setValidEnvironment(t *testing.T) {
	t.Helper()
	t.Setenv("TURSO_DATABASE_URL", "libsql://example-test.turso.io")
	t.Setenv("TURSO_AUTH_TOKEN", "test-auth-token")
	t.Setenv("DATA_ADMIN_PASSWORD", "a-secure-password-for-tests")
	t.Setenv("HTTP_ADDR", ":8080")
	t.Setenv("SYNC_ENABLED", "true")
	t.Setenv("SYNC_ON_START", "true")
	t.Setenv("SYNC_INTERVAL", "5m")
	t.Setenv("MAX_DOWNLOAD_BYTES", "104857600")
	t.Setenv("MAX_INVALID_RATIO", "0.05")
	t.Setenv("SHUTDOWN_TIMEOUT", "15s")
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

func TestLoadUsesProviderPort(t *testing.T) {
	setValidEnvironment(t)
	t.Setenv("HTTP_ADDR", "")
	t.Setenv("PORT", "9090")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTPAddr != ":9090" {
		t.Fatalf("HTTPAddr=%q, want :9090", cfg.HTTPAddr)
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

func TestLoadRequiresTursoURL(t *testing.T) {
	setValidEnvironment(t)
	t.Setenv("TURSO_DATABASE_URL", "")
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "TURSO_DATABASE_URL is required") {
		t.Fatalf("expected TURSO_DATABASE_URL error, got %v", err)
	}
}

func TestLoadRequiresTursoToken(t *testing.T) {
	setValidEnvironment(t)
	t.Setenv("TURSO_AUTH_TOKEN", "")
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "TURSO_AUTH_TOKEN is required") {
		t.Fatalf("expected TURSO_AUTH_TOKEN error, got %v", err)
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

// A sharded OpenBIIN source points its path at the directory of shard files
// rather than at a single CSV, so the directory path must survive loading with
// no stray slashes and the format must be accepted.
func TestLoadAcceptsShardedOpenBIINSource(t *testing.T) {
	setValidEnvironment(t)
	t.Setenv("SOURCES_JSON", `[{"id":"openbiin","repository":"Wayproyect/openbiin","branch":"main",`+
		`"path":"/functions/data/","format":"OpenBIIN","priority":50,"min_records":300000}]`)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	source := cfg.Sources[0]
	if source.Format != "openbiin" {
		t.Errorf("format = %q, want openbiin", source.Format)
	}
	if source.Path != "functions/data" {
		t.Errorf("path = %q, want functions/data", source.Path)
	}
	if source.Priority != 50 || source.MinRecords != 300000 {
		t.Errorf("unexpected source: %+v", source)
	}
}

func TestLoadRejectsUnknownFormat(t *testing.T) {
	setValidEnvironment(t)
	t.Setenv("SOURCES_JSON", `[{"id":"test","repository":"owner/repo","branch":"main","path":"bins.csv","format":"openbin","priority":10,"min_records":1}]`)
	if _, err := Load(); err == nil {
		t.Fatal("a typo in a format name was accepted")
	} else if !strings.Contains(err.Error(), "unsupported format") {
		t.Errorf("unexpected error: %v", err)
	}
}
