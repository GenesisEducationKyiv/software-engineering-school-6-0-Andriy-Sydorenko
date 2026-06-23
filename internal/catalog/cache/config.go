package cache

import (
	"fmt"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/shared/config"
)

// Config bundles cache knobs. URL wins when set; otherwise Host/Port/
// Password/DB assemble one. Empty Host (with empty URL) means "no Redis".
type Config struct {
	URL      string
	Host     string
	Port     string
	Password string
	DB       string
}

func (c *Config) Validate() error {
	if c.URL != "" || c.Host == "" {
		return nil
	}
	if c.Port == "" {
		return fmt.Errorf("cache config: REDIS_PORT required when REDIS_HOST is set")
	}
	return nil
}

func LoadConfig() *Config {
	return &Config{
		config.GetEnvOrDefault("REDIS_URL", ""),
		config.GetEnvOrDefault("REDIS_HOST", ""),
		config.GetEnvOrDefault("REDIS_PORT", "6379"),
		config.GetEnvOrDefault("REDIS_PASSWORD", ""),
		config.GetEnvOrDefault("REDIS_DB", "0"),
	}
}
