package notifier

import (
	"fmt"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/shared/config"
)

type Config struct {
	Host     string
	Port     string
	Username string
	Password string
}

func (c *Config) Validate() error {
	// values are usually passed to config with default values, this is just-in-case validation logic
	if c.Host == "" || c.Port == "" || c.Username == "" || c.Password == "" {
		return fmt.Errorf("SMTP_HOST, SMTP_PORT, SMTP_USERNAME, and SMTP_PASSWORD are required")
	}
	return nil
}

func LoadConfig() *Config {
	return &Config{
		config.GetEnvOrDefault("SMTP_HOST", "smtp.gmail.com"),
		config.GetEnvOrDefault("SMTP_PORT", "587"),
		config.GetEnvOrDefault("SMTP_USERNAME", "some.email@email.com"),
		config.GetEnvOrDefault("SMTP_PASSWORD", "some_password"),
	}
}
