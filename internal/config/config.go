package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Source describes one CSV file in a GitHub repository. Only GitHub repository
// coordinates are accepted; arbitrary URLs are deliberately not supported.
type Source struct {
	ID         string `json:"id"`
	Repository string `json:"repository"`
	Branch     string `json:"branch"`
	Path       string `json:"path"`
	Format     string `json:"format"`
	Priority   int    `json:"priority"`
	MinRecords int    `json:"min_records"`
}

type Config struct {
	HTTPAddr         string
	DatabaseURL      string
	GitHubToken      string
	WebhookSecret    string
	AdminToken       string
	SyncEnabled      bool
	SyncOnStart      bool
	SyncInterval     time.Duration
	MaxDownloadBytes int64
	MaxInvalidRatio  float64
	CacheTTL         time.Duration
	CacheMaxEntries  int
	ShutdownTimeout  time.Duration
	DatabaseMaxConns int32
	DatabaseMinConns int32
	Sources          []Source
}

var (
	idPattern   = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,63}$`)
	repoPattern = regexp.MustCompile(`^[a-zA-Z0-9_.-]+/[a-zA-Z0-9_.-]+$`)
)

func Load() (Config, error) {
	cfg := Config{
		HTTPAddr:         env("HTTP_ADDR", ":8080"),
		DatabaseURL:      strings.TrimSpace(os.Getenv("DATABASE_URL")),
		GitHubToken:      strings.TrimSpace(os.Getenv("GITHUB_TOKEN")),
		WebhookSecret:    os.Getenv("GITHUB_WEBHOOK_SECRET"),
		AdminToken:       os.Getenv("SYNC_ADMIN_TOKEN"),
		SyncEnabled:      envBool("SYNC_ENABLED", true),
		SyncOnStart:      envBool("SYNC_ON_START", true),
		SyncInterval:     envDuration("SYNC_INTERVAL", 5*time.Minute),
		MaxDownloadBytes: envInt64("MAX_DOWNLOAD_BYTES", 100<<20),
		MaxInvalidRatio:  envFloat("MAX_INVALID_RATIO", 0.05),
		CacheTTL:         envDuration("CACHE_TTL", 5*time.Minute),
		CacheMaxEntries:  envInt("CACHE_MAX_ENTRIES", 100_000),
		ShutdownTimeout:  envDuration("SHUTDOWN_TIMEOUT", 15*time.Second),
		DatabaseMaxConns: int32(envInt("DATABASE_MAX_CONNS", 20)),
		DatabaseMinConns: int32(envInt("DATABASE_MIN_CONNS", 2)),
	}

	if cfg.DatabaseURL == "" {
		return Config{}, errors.New("DATABASE_URL is required")
	}
	if cfg.SyncInterval < 30*time.Second {
		return Config{}, errors.New("SYNC_INTERVAL must be at least 30s")
	}
	if cfg.MaxDownloadBytes < 1<<20 {
		return Config{}, errors.New("MAX_DOWNLOAD_BYTES must be at least 1 MiB")
	}
	if cfg.MaxInvalidRatio < 0 || cfg.MaxInvalidRatio > 1 {
		return Config{}, errors.New("MAX_INVALID_RATIO must be between 0 and 1")
	}
	if cfg.CacheMaxEntries < 1 {
		return Config{}, errors.New("CACHE_MAX_ENTRIES must be positive")
	}
	if cfg.DatabaseMaxConns < 2 || cfg.DatabaseMinConns < 0 || cfg.DatabaseMinConns > cfg.DatabaseMaxConns {
		return Config{}, errors.New("invalid DATABASE_MIN_CONNS/DATABASE_MAX_CONNS")
	}

	sources, err := loadSources()
	if err != nil {
		return Config{}, err
	}
	cfg.Sources = sources
	return cfg, nil
}

func loadSources() ([]Source, error) {
	data := strings.TrimSpace(os.Getenv("SOURCES_JSON"))
	if path := strings.TrimSpace(os.Getenv("SOURCES_FILE")); path != "" {
		if data != "" {
			return nil, errors.New("set only one of SOURCES_JSON and SOURCES_FILE")
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read SOURCES_FILE: %w", err)
		}
		data = string(contents)
	}
	if data == "" {
		// This is the most recently maintained, permissively licensed community
		// dataset known when this service was created. It can be replaced without
		// rebuilding the application by setting SOURCES_FILE.
		data = `[{"id":"bin-list-data","repository":"venelinkochev/bin-list-data","branch":"master","path":"bin-list-data.csv","format":"binlist","priority":100,"min_records":100000}]`
	}

	var sources []Source
	if err := json.Unmarshal([]byte(data), &sources); err != nil {
		return nil, fmt.Errorf("parse sources configuration: %w", err)
	}
	if len(sources) == 0 {
		return nil, errors.New("at least one source is required")
	}
	seen := make(map[string]struct{}, len(sources))
	for i := range sources {
		s := &sources[i]
		s.ID = strings.TrimSpace(s.ID)
		s.Repository = strings.TrimSpace(s.Repository)
		s.Branch = strings.TrimSpace(s.Branch)
		s.Path = strings.Trim(strings.TrimSpace(s.Path), "/")
		s.Format = strings.ToLower(strings.TrimSpace(s.Format))
		if s.Branch == "" {
			s.Branch = "main"
		}
		if s.Format == "" {
			s.Format = "generic"
		}
		if s.MinRecords == 0 {
			s.MinRecords = 100
		}
		if !idPattern.MatchString(s.ID) {
			return nil, fmt.Errorf("source %d has invalid id %q", i, s.ID)
		}
		if !repoPattern.MatchString(s.Repository) {
			return nil, fmt.Errorf("source %q has invalid repository %q", s.ID, s.Repository)
		}
		if s.Path == "" || strings.Contains(s.Path, "..") {
			return nil, fmt.Errorf("source %q has invalid path", s.ID)
		}
		switch s.Format {
		case "generic", "binlist", "ranges":
		default:
			return nil, fmt.Errorf("source %q has unsupported format %q", s.ID, s.Format)
		}
		if s.MinRecords < 1 {
			return nil, fmt.Errorf("source %q min_records must be positive", s.ID)
		}
		if _, ok := seen[s.ID]; ok {
			return nil, fmt.Errorf("duplicate source id %q", s.ID)
		}
		seen[s.ID] = struct{}{}
	}
	return sources, nil
}

func env(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func envBool(name string, fallback bool) bool {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envDuration(name string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envInt(name string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envInt64(name string, fallback int64) int64 {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return fallback
	}
	return parsed
}

func envFloat(name string, fallback float64) float64 {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return fallback
	}
	return parsed
}
