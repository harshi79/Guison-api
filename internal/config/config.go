package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Source describes one CSV file in a GitHub repository.
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
	HTTPAddr          string
	DatabaseURL       string
	GitHubToken       string
	DataAdminPassword string
	SyncEnabled       bool
	SyncOnStart       bool
	SyncInterval      time.Duration
	MaxDownloadBytes  int64
	MaxInvalidRatio   float64
	ShutdownTimeout   time.Duration
	DatabaseMaxConns  int32
	Sources           []Source
}

var (
	idPattern   = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,63}$`)
	repoPattern = regexp.MustCompile(`^[a-zA-Z0-9_.-]+/[a-zA-Z0-9_.-]+$`)
)

func Load() (Config, error) {
	cfg := Config{
		HTTPAddr:          envString("HTTP_ADDR", ":8080"),
		DatabaseURL:       strings.TrimSpace(os.Getenv("DATABASE_URL")),
		GitHubToken:       strings.TrimSpace(os.Getenv("GITHUB_TOKEN")),
		DataAdminPassword: os.Getenv("DATA_ADMIN_PASSWORD"),
	}
	var err error
	if cfg.SyncEnabled, err = envBool("SYNC_ENABLED", true); err != nil {
		return Config{}, err
	}
	if cfg.SyncOnStart, err = envBool("SYNC_ON_START", true); err != nil {
		return Config{}, err
	}
	if cfg.SyncInterval, err = envDuration("SYNC_INTERVAL", 5*time.Minute); err != nil {
		return Config{}, err
	}
	if cfg.MaxDownloadBytes, err = envInt64("MAX_DOWNLOAD_BYTES", 100<<20); err != nil {
		return Config{}, err
	}
	if cfg.MaxInvalidRatio, err = envFloat("MAX_INVALID_RATIO", 0.05); err != nil {
		return Config{}, err
	}
	if cfg.ShutdownTimeout, err = envDuration("SHUTDOWN_TIMEOUT", 15*time.Second); err != nil {
		return Config{}, err
	}
	maxConns, err := envInt("DATABASE_MAX_CONNS", 10)
	if err != nil {
		return Config{}, err
	}
	if int64(maxConns) > int64(1<<31-1) {
		return Config{}, errors.New("DATABASE_MAX_CONNS is too large")
	}
	cfg.DatabaseMaxConns = int32(maxConns)

	if cfg.DatabaseURL == "" {
		return Config{}, errors.New("DATABASE_URL is required")
	}
	if strings.TrimSpace(cfg.DataAdminPassword) == "" {
		return Config{}, errors.New("DATA_ADMIN_PASSWORD is required")
	}
	if len(cfg.DataAdminPassword) < 16 {
		return Config{}, errors.New("DATA_ADMIN_PASSWORD must be at least 16 characters")
	}
	if cfg.DataAdminPassword == "replace-with-a-long-random-password" {
		return Config{}, errors.New("DATA_ADMIN_PASSWORD must be changed from the example value")
	}
	if err := validateHTTPAddr(cfg.HTTPAddr); err != nil {
		return Config{}, err
	}
	if cfg.SyncInterval < 30*time.Second {
		return Config{}, errors.New("SYNC_INTERVAL must be at least 30s")
	}
	if cfg.MaxDownloadBytes < 1<<20 || cfg.MaxDownloadBytes > 1<<30 {
		return Config{}, errors.New("MAX_DOWNLOAD_BYTES must be between 1 MiB and 1 GiB")
	}
	if math.IsNaN(cfg.MaxInvalidRatio) || math.IsInf(cfg.MaxInvalidRatio, 0) || cfg.MaxInvalidRatio < 0 || cfg.MaxInvalidRatio > 1 {
		return Config{}, errors.New("MAX_INVALID_RATIO must be between 0 and 1")
	}
	if cfg.ShutdownTimeout <= 0 {
		return Config{}, errors.New("SHUTDOWN_TIMEOUT must be positive")
	}
	if cfg.DatabaseMaxConns < 2 {
		return Config{}, errors.New("DATABASE_MAX_CONNS must be at least 2")
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
	if filePath := strings.TrimSpace(os.Getenv("SOURCES_FILE")); filePath != "" {
		if data != "" {
			return nil, errors.New("set only one of SOURCES_JSON and SOURCES_FILE")
		}
		contents, err := os.ReadFile(filePath)
		if err != nil {
			return nil, fmt.Errorf("read SOURCES_FILE: %w", err)
		}
		data = string(contents)
	}
	if data == "" {
		data = `[{"id":"bin-list-data","repository":"venelinkochev/bin-list-data","branch":"master","path":"bin-list-data.csv","format":"binlist","priority":100,"min_records":100000}]`
	}

	var sources []Source
	decoder := json.NewDecoder(strings.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&sources); err != nil {
		return nil, fmt.Errorf("parse sources configuration: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, errors.New("sources configuration must contain one JSON array")
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
		if !idPattern.MatchString(s.ID) || strings.EqualFold(s.ID, "manual") {
			return nil, fmt.Errorf("source %d has invalid or reserved id %q", i, s.ID)
		}
		if !repoPattern.MatchString(s.Repository) {
			return nil, fmt.Errorf("source %q has invalid repository %q", s.ID, s.Repository)
		}
		if s.Path == "" || strings.ContainsRune(s.Path, '\x00') || hasParentPath(s.Path) {
			return nil, fmt.Errorf("source %q has invalid path", s.ID)
		}
		switch s.Format {
		case "generic", "binlist", "ranges":
		default:
			return nil, fmt.Errorf("source %q has unsupported format %q", s.ID, s.Format)
		}
		if s.Priority >= 1000 {
			return nil, fmt.Errorf("source %q priority must be below 1000; priority 1000 is reserved for manual data", s.ID)
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

func validateHTTPAddr(address string) error {
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("invalid HTTP_ADDR %q: %w", address, err)
	}
	number, err := strconv.Atoi(port)
	if err != nil || number < 1 || number > 65535 {
		return fmt.Errorf("HTTP_ADDR %q must contain a port between 1 and 65535", address)
	}
	return nil
}

func hasParentPath(filePath string) bool {
	for _, part := range strings.Split(filePath, "/") {
		if part == ".." {
			return true
		}
	}
	return false
}

func envString(name, fallback string) string {
	if value, ok := os.LookupEnv(name); ok {
		return strings.TrimSpace(value)
	}
	return fallback
}

func envBool(name string, fallback bool) (bool, error) {
	value, ok := os.LookupEnv(name)
	if !ok {
		return fallback, nil
	}
	parsed, err := strconv.ParseBool(strings.TrimSpace(value))
	if err != nil {
		return false, fmt.Errorf("%s must be true or false", name)
	}
	return parsed, nil
}

func envDuration(name string, fallback time.Duration) (time.Duration, error) {
	value, ok := os.LookupEnv(name)
	if !ok {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(strings.TrimSpace(value))
	if err != nil {
		return 0, fmt.Errorf("%s must be a duration such as 5m: %w", name, err)
	}
	return parsed, nil
}

func envInt(name string, fallback int) (int, error) {
	value, ok := os.LookupEnv(name)
	if !ok {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", name, err)
	}
	return parsed, nil
}

func envInt64(name string, fallback int64) (int64, error) {
	value, ok := os.LookupEnv(name)
	if !ok {
		return fallback, nil
	}
	parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", name, err)
	}
	return parsed, nil
}

func envFloat(name string, fallback float64) (float64, error) {
	value, ok := os.LookupEnv(name)
	if !ok {
		return fallback, nil
	}
	parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be a number: %w", name, err)
	}
	return parsed, nil
}
