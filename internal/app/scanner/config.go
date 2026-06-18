package scanner

import (
	"fmt"
	"time"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/shared/config"
)

// Config bundles scanner knobs. The per-GitHub-call deadline lives on
// the GitHub client, not here.
type Config struct {
	Interval    time.Duration
	Concurrency int
}

func (c *Config) withDefaults() {
	if c.Interval <= 0 {
		c.Interval = 5 * time.Minute
	}
	if c.Concurrency <= 0 {
		c.Concurrency = 8
	}
}

func (c *Config) Validate() error {
	if c.Interval < 0 {
		return fmt.Errorf("scanner config: SCAN_INTERVAL must not be negative, got %s", c.Interval)
	}
	if c.Concurrency < 1 {
		return fmt.Errorf("scanner config: SCAN_CONCURRENCY must be >= 1, got %d", c.Concurrency)
	}
	return nil
}

func LoadConfig() *Config {
	return &Config{
		Interval:    config.GetEnvDuration("SCAN_INTERVAL", 5*time.Minute),
		Concurrency: config.GetEnvInt("SCAN_CONCURRENCY", 8),
	}
}
