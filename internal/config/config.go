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
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/notifier"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/observability/logging"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/platform"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/scanner"
)

const envFile = ".env"

type Config struct {
	DB      database.Config
	Redis   cache.Config
	SMTP    notifier.Config
	Scanner scanner.Config
	GitHub  github.Config
	Log     logging.Config

	Port   string
	APIKey string
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
		SMTP: notifier.Config{
			Host:     os.Getenv("SMTP_HOST"),
			Port:     platform.GetOrDefault("SMTP_PORT", "587"),
			Username: os.Getenv("SMTP_USERNAME"),
			Password: os.Getenv("SMTP_PASSWORD"),
			BaseURL:  platform.GetOrDefault("BASE_URL", "http://localhost:8080"),
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
		Port:   platform.GetOrDefault("PORT", "8080"),
		APIKey: os.Getenv("API_KEY"),
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
	if c.SMTP.Host == "" || c.SMTP.Username == "" || c.SMTP.Password == "" {
		return fmt.Errorf("SMTP_HOST, SMTP_USERNAME, and SMTP_PASSWORD are required")
	}
	if err := c.Log.Validate(); err != nil {
		return err
	}
	return nil
}
