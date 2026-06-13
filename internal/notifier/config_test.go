package notifier_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/notifier"
)

func TestLoadServiceConfig_ok(t *testing.T) {
	t.Setenv("INTERNAL_API_TOKEN", "tok")
	t.Setenv("SMTP_HOST", "mail")
	t.Setenv("SMTP_USERNAME", "u")
	t.Setenv("SMTP_PASSWORD", "p")

	cfg, err := notifier.LoadServiceConfig()
	require.NoError(t, err)
	assert.Equal(t, "tok", cfg.Token)
	assert.Equal(t, ":50051", cfg.GRPCAddr)
	assert.Equal(t, ":8081", cfg.AdminAddr)
	assert.Equal(t, "mail", cfg.SMTP.Host)
	assert.Equal(t, "587", cfg.SMTP.Port)
	assert.Equal(t, "http://localhost:8080", cfg.SMTP.BaseURL)
}

func TestLoadServiceConfig_missingToken(t *testing.T) {
	t.Setenv("SMTP_HOST", "mail")
	t.Setenv("SMTP_USERNAME", "u")
	t.Setenv("SMTP_PASSWORD", "p")
	_, err := notifier.LoadServiceConfig()
	require.Error(t, err)
}

func TestLoadServiceConfig_missingSMTP(t *testing.T) {
	t.Setenv("INTERNAL_API_TOKEN", "tok")
	_, err := notifier.LoadServiceConfig()
	require.Error(t, err)
}
