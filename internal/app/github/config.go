package github

import (
	"fmt"
	"time"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/shared/config"
)

// Config bundles GitHub-client knobs. RequestTimeout is the authoritative
// per-request ceiling — callers should not wrap with context.WithTimeout.
type Config struct {
	Token          string // GITHUB_TOKEN; empty uses anon rate limit
	RequestTimeout time.Duration
}

func LoadConfig() *Config {
	return &Config{
		Token:          config.GetEnvOrDefault("GITHUB_TOKEN", ""),
		RequestTimeout: config.GetEnvDuration("GITHUB_REQUEST_TIMEOUT", 10*time.Second),
	}
}

func (c *Config) Validate() error {
	if c.RequestTimeout <= 0 {
		return fmt.Errorf(
			"github config: GITHUB_REQUEST_TIMEOUT must be > 0, got %s",
			c.RequestTimeout,
		)
	}
	return nil
}
