package notifierclient

import (
	"fmt"
	"time"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/shared/config"
)

type Config struct {
	Transport      string // "grpc" (default) or "rest"
	Addr           string // grpc addr e.g. "notifier:9090"
	RESTURL        string // rest addr e.g. "http://notifier:9091"
	Token          string
	RequestTimeout time.Duration // REST transport only
}

func LoadConfig() *Config {
	return &Config{
		Transport:      config.GetEnvOrDefault("NOTIFIER_TRANSPORT", "grpc"),
		Addr:           config.GetEnvOrDefault("NOTIFIER_GRPC_ADDR", "localhost:9090"),
		RESTURL:        config.GetEnvOrDefault("NOTIFIER_REST_URL", ""),
		Token:          config.GetEnvOrDefault("INTERNAL_API_TOKEN", ""),
		RequestTimeout: config.GetEnvDuration("NOTIFIER_REST_TIMEOUT", 10*time.Second),
	}
}

func (c *Config) Validate() error {
	if c.Transport != "grpc" && c.Transport != "rest" {
		return fmt.Errorf(
			"notifier config: invalid NOTIFIER_TRANSPORT %q (want grpc or rest)",
			c.Transport,
		)
	}
	if c.Transport == "rest" && c.RESTURL == "" {
		return fmt.Errorf("notifier config: NOTIFIER_REST_URL is required when NOTIFIER_TRANSPORT=rest")
	}
	if c.RequestTimeout <= 0 {
		return fmt.Errorf(
			"notifier config: NOTIFIER_REST_TIMEOUT must be > 0, got %s",
			c.RequestTimeout,
		)
	}
	return nil
}
