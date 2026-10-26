// Package config loads runtime configuration from the environment.
package config

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config is the complete backend configuration.
type Config struct {
	Port         string
	MongoURI     string
	MongoDB      string
	GitHubToken  string
	GitHubAPIURL string
	SyncInterval time.Duration
	Concurrency  int
	LogLevel     string
}

// Load reads the environment (optionally seeded from a .env file) and applies
// defaults. It returns an error for invalid values rather than silently
// falling back, so a typo cannot turn off syncing unnoticed.
func Load() (Config, error) {
	loadDotEnv(".env")
	loadDotEnv("../.env")

	cfg := Config{
		Port:         envOr("PORT", "8080"),
		MongoURI:     envOr("MONGODB_URI", "mongodb://localhost:27017"),
		MongoDB:      envOr("MONGODB_DB", "gitsion"),
		GitHubToken:  strings.TrimSpace(os.Getenv("GITHUB_TOKEN")),
		GitHubAPIURL: strings.TrimRight(envOr("GITHUB_API_URL", "https://api.github.com"), "/"),
		LogLevel:     strings.ToLower(envOr("LOG_LEVEL", "info")),
	}

	interval := envOr("SYNC_INTERVAL", "")
	if interval == "" {
		// Unauthenticated GitHub allows only 60 requests/hour and a snapshot
		// costs four calls. A slower cadence keeps a handful of public repos
		// usable without a token.
		if cfg.GitHubToken == "" {
			interval = "30m"
		} else {
			interval = "15m"
		}
	}
	d, err := time.ParseDuration(interval)
	if err != nil {
		return Config{}, fmt.Errorf("SYNC_INTERVAL: %w", err)
	}
	if d < time.Minute {
		return Config{}, fmt.Errorf("SYNC_INTERVAL must be at least 1m, got %s", d)
	}
	cfg.SyncInterval = d

	cfg.Concurrency, err = strconv.Atoi(envOr("SYNC_CONCURRENCY", "3"))
	if err != nil || cfg.Concurrency < 1 || cfg.Concurrency > 10 {
		return Config{}, fmt.Errorf("SYNC_CONCURRENCY must be an integer between 1 and 10")
	}

	if cfg.MongoURI == "" {
		return Config{}, fmt.Errorf("MONGODB_URI must not be empty")
	}
	if cfg.GitHubAPIURL == "" {
		return Config{}, fmt.Errorf("GITHUB_API_URL must not be empty")
	}
	switch cfg.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return Config{}, fmt.Errorf("LOG_LEVEL must be debug, info, warn or error")
	}

	return cfg, nil
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

// loadDotEnv is a deliberately small .env reader for local development. It
// never overrides variables that are already set, so Docker and shell env win.
func loadDotEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		value = strings.Trim(value, `"'`)
		if key == "" {
			continue
		}
		if _, exists := os.LookupEnv(key); exists {
			continue
		}
		_ = os.Setenv(key, value)
	}
}
