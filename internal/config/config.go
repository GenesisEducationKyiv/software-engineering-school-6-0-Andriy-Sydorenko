package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"

	"github.com/joho/godotenv"
)

const envFile = ".env"

type Config struct {
	DatabaseURL string
	DBHost      string
	DBPort      string
	DBUser      string
	DBPassword  string
	DBName      string
	DBSSLMode   string

	RedisURL      string
	RedisHost     string
	RedisPort     string
	RedisPassword string
	RedisDB       string

	Port string

	GitHubToken string

	SMTPHost     string
	SMTPPort     string
	SMTPUserName string
	SMTPPassword string

	ScanInterval time.Duration
	BaseURL      string

	APIKey string
}

func LoadConfig() (*Config, error) {
	// Missing .env is fine (containers/CI); malformed is not.
	if err := godotenv.Load(envFile); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("failed to load %s: %w", envFile, err)
	}

	cfg := &Config{
		DatabaseURL:   os.Getenv("DATABASE_URL"),
		DBHost:        getEnvOrDefault("DB_HOST", "localhost"),
		DBPort:        getEnvOrDefault("DB_PORT", "5432"),
		DBUser:        os.Getenv("DB_USER"),
		DBPassword:    os.Getenv("DB_PASSWORD"),
		DBName:        os.Getenv("DB_NAME"),
		DBSSLMode:     getEnvOrDefault("DB_SSLMODE", "disable"),
		RedisURL:      os.Getenv("REDIS_URL"),
		RedisHost:     getEnvOrDefault("REDIS_HOST", "localhost"),
		RedisPort:     getEnvOrDefault("REDIS_PORT", "6379"),
		RedisPassword: os.Getenv("REDIS_PASSWORD"),
		RedisDB:       getEnvOrDefault("REDIS_DB", "0"),
		Port:          getEnvOrDefault("PORT", "8080"),
		GitHubToken:   os.Getenv("GITHUB_TOKEN"),
		SMTPHost:      os.Getenv("SMTP_HOST"),
		SMTPPort:      getEnvOrDefault("SMTP_PORT", "587"),
		SMTPUserName:  os.Getenv("SMTP_USERNAME"),
		SMTPPassword:  os.Getenv("SMTP_PASSWORD"),
		BaseURL:       getEnvOrDefault("BASE_URL", "http://localhost:8080"),
		APIKey:        os.Getenv("API_KEY"),
	}

	intervalStr := getEnvOrDefault("SCAN_INTERVAL", "5m")
	interval, err := time.ParseDuration(intervalStr)
	if err != nil {
		return nil, fmt.Errorf("invalid SCAN_INTERVAL %q: %w", intervalStr, err)
	}
	cfg.ScanInterval = interval

	if cfg.DatabaseURL == "" {
		if cfg.DBUser == "" || cfg.DBName == "" {
			return nil, fmt.Errorf("either DATABASE_URL or DB_USER+DB_NAME (with DB_HOST/DB_PORT/DB_PASSWORD) must be set")
		}
	}

	if cfg.SMTPHost == "" || cfg.SMTPUserName == "" || cfg.SMTPPassword == "" {
		return nil, fmt.Errorf("SMTP_HOST, SMTP_USERNAME, and SMTP_PASSWORD are required")
	}

	return cfg, nil
}

func (c *Config) PostgresDSN() string {
	if c.DatabaseURL != "" {
		return c.DatabaseURL
	}
	return fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
		c.DBHost, c.DBPort, c.DBUser, c.DBPassword, c.DBName, c.DBSSLMode,
	)
}

func getEnvOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
