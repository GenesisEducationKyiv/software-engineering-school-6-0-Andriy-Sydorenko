package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"time"

	"github.com/joho/godotenv"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/cache"
	database "github.com/Andriy-Sydorenko/repo-release-notifier/internal/db"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/github"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/observability/logging"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/platform"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/scanner"
)

const envFile = ".env"

type Config struct {
	DB      database.Config
	Redis   cache.Config
	Scanner scanner.Config
	GitHub  github.Config
	Log     logging.Config

	Port          string
	NotifierAddr  string
	InternalToken string
	// BaseURL is the core's externally-visible base; it builds the confirm/
	// unsubscribe links the notifier renders, so the route scheme stays core-side.
	BaseURL string
}

func LoadConfig() (*Config, error) {
	// Missing .env is fine (containers/CI); malformed is not.
	if err := godotenv.Load(envFile); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("failed to load %s: %w", envFile, err)
	}

	cfg := &Config{
		DB: database.Config{
			URL:      os.Getenv("DATABASE_URL"),
			Host:     platform.GetOrDefault("DB_HOST", "localhost"),
			Port:     platform.GetOrDefault("DB_PORT", "5432"),
			User:     os.Getenv("DB_USER"),
			Password: os.Getenv("DB_PASSWORD"),
			Name:     os.Getenv("DB_NAME"),
			SSLMode:  platform.GetOrDefault("DB_SSLMODE", "disable"),
		},
		Redis: cache.Config{
			URL:      os.Getenv("REDIS_URL"),
			Host:     os.Getenv("REDIS_HOST"),
			Port:     platform.GetOrDefault("REDIS_PORT", "6379"),
			Password: os.Getenv("REDIS_PASSWORD"),
			DB:       platform.GetOrDefault("REDIS_DB", "0"),
		},
		Scanner: loadScannerConfig(),
		GitHub: github.Config{
			Token:   os.Getenv("GITHUB_TOKEN"),
			Timeout: platform.GetDuration("GITHUB_TIMEOUT", 10*time.Second),
			BaseURL: os.Getenv("GITHUB_API_URL"),
		},
		Log: logging.Config{
			Level:  logging.Level(strings.ToLower(platform.GetOrDefault("LOG_LEVEL", "info"))),
			Format: logging.Format(strings.ToLower(platform.GetOrDefault("LOG_FORMAT", "text"))),
		},
		Port:          platform.GetOrDefault("PORT", "8080"),
		NotifierAddr:  platform.GetOrDefault("NOTIFIER_ADDR", "notifier:50051"),
		InternalToken: os.Getenv("INTERNAL_API_TOKEN"),
		BaseURL:       platform.GetOrDefault("BASE_URL", "http://localhost:8080"),
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// loadScannerConfig panics on malformed env: config is a system
// boundary and an operator typo must not silently flip behavior.
func loadScannerConfig() scanner.Config {
	concurrency := platform.GetInt("SCAN_CONCURRENCY", 8)
	if concurrency < 1 {
		panic(fmt.Sprintf("config: SCAN_CONCURRENCY must be >= 1, got %d", concurrency))
	}
	return scanner.Config{
		Interval:    platform.GetDuration("SCAN_INTERVAL", 5*time.Minute),
		Concurrency: concurrency,
	}
}

func (c *Config) validate() error {
	if c.DB.URL == "" && (c.DB.User == "" || c.DB.Name == "") {
		return fmt.Errorf("either DATABASE_URL or DB_USER+DB_NAME (with DB_HOST/DB_PORT/DB_PASSWORD) must be set")
	}
	if c.InternalToken == "" {
		return fmt.Errorf("INTERNAL_API_TOKEN is required (core dials the notifier service)")
	}
	if err := c.Log.Validate(); err != nil {
		return err
	}
	return nil
}
