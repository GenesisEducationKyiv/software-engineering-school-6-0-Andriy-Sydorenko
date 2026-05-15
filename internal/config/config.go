package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"time"

	"github.com/joho/godotenv"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/cache"
	database "github.com/Andriy-Sydorenko/repo-release-notifier/internal/db"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/github"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/notifier"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/scanner"
)

const envFile = ".env"

type Config struct {
	DB      database.Config
	Redis   cache.Config
	SMTP    notifier.Config
	Scanner scanner.Config
	GitHub  github.Config

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
			Host:     getEnvOrDefault("DB_HOST", "localhost"),
			Port:     getEnvOrDefault("DB_PORT", "5432"),
			User:     os.Getenv("DB_USER"),
			Password: os.Getenv("DB_PASSWORD"),
			Name:     os.Getenv("DB_NAME"),
			SSLMode:  getEnvOrDefault("DB_SSLMODE", "disable"),
		},
		Redis: cache.Config{
			URL:      os.Getenv("REDIS_URL"),
			Host:     os.Getenv("REDIS_HOST"),
			Port:     getEnvOrDefault("REDIS_PORT", "6379"),
			Password: os.Getenv("REDIS_PASSWORD"),
			DB:       getEnvOrDefault("REDIS_DB", "0"),
		},
		SMTP: notifier.Config{
			Host:     os.Getenv("SMTP_HOST"),
			Port:     getEnvOrDefault("SMTP_PORT", "587"),
			Username: os.Getenv("SMTP_USERNAME"),
			Password: os.Getenv("SMTP_PASSWORD"),
			BaseURL:  getEnvOrDefault("BASE_URL", "http://localhost:8080"),
		},
		Scanner: loadScannerConfig(),
		GitHub: github.Config{
			Token:   os.Getenv("GITHUB_TOKEN"),
			Timeout: getEnvDuration("GITHUB_TIMEOUT", 10*time.Second),
		},
		Port:   getEnvOrDefault("PORT", "8080"),
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
	concurrency := getEnvInt("SCAN_CONCURRENCY", 8)
	if concurrency < 1 {
		panic(fmt.Sprintf("config: SCAN_CONCURRENCY must be >= 1, got %d", concurrency))
	}
	return scanner.Config{
		Interval:    getEnvDuration("SCAN_INTERVAL", 5*time.Minute),
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
	return nil
}

func getEnvOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// getEnvDuration parses a duration env var, falling back when unset.
// A malformed value panics: the operator set something on purpose and
// got the format wrong; silently using the default would hide the bug.
func getEnvDuration(key string, fallback time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		panic(fmt.Sprintf("config: invalid %s %q: %v", key, v, err))
	}
	return d
}

// getEnvInt parses an int env var, falling back when unset. Malformed
// values panic — see getEnvDuration for the rationale.
func getEnvInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		panic(fmt.Sprintf("config: invalid %s %q: %v", key, v, err))
	}
	return n
}
