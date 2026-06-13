package notifier

import (
	"fmt"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/platform"
)

// ServiceConfig is the notifier process's full config: SMTP/email (Config) + the
// service's own listen addrs and the shared internal auth token.
type ServiceConfig struct {
	SMTP      Config
	Token     string
	GRPCAddr  string
	AdminAddr string
}

// LoadServiceConfig reads the notifier process config from the environment.
// INTERNAL_API_TOKEN and the SMTP credentials are required; addrs default to the
// compose-wired ports (spec §11).
func LoadServiceConfig() (*ServiceConfig, error) {
	token, err := platform.MustGet("INTERNAL_API_TOKEN")
	if err != nil {
		return nil, err
	}

	smtp := Config{
		Host:     platform.GetOrDefault("SMTP_HOST", ""),
		Port:     platform.GetOrDefault("SMTP_PORT", "587"),
		Username: platform.GetOrDefault("SMTP_USERNAME", ""),
		Password: platform.GetOrDefault("SMTP_PASSWORD", ""),
	}
	if smtp.Host == "" || smtp.Username == "" || smtp.Password == "" {
		return nil, fmt.Errorf("SMTP_HOST, SMTP_USERNAME, and SMTP_PASSWORD are required")
	}

	return &ServiceConfig{
		SMTP:      smtp,
		Token:     token,
		GRPCAddr:  platform.GetOrDefault("NOTIFIER_GRPC_ADDR", ":50051"),
		AdminAddr: platform.GetOrDefault("ADMIN_ADDR", ":8081"),
	}, nil
}
