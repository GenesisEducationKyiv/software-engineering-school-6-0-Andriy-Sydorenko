package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/observability/logging"
)

func TestLoadScannerConfigDefaults(t *testing.T) {
	t.Setenv("SCAN_INTERVAL", "")
	t.Setenv("SCAN_CONCURRENCY", "")
	cfg := loadScannerConfig()
	assert.Equal(t, 5*time.Minute, cfg.Interval)
	assert.Equal(t, 8, cfg.Concurrency)
}

func TestLoadScannerConfigPanicsOnNonPositiveConcurrency(t *testing.T) {
	for _, val := range []string{"0", "-3"} {
		t.Run(val, func(t *testing.T) {
			t.Setenv("SCAN_CONCURRENCY", val)
			assert.Panics(t, func() { _ = loadScannerConfig() })
		})
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(c *Config)
		wantErr string
	}{
		{"ok with DATABASE_URL", func(c *Config) {}, ""},
		{"ok with discrete DB fields", func(c *Config) {
			c.DB.URL = ""
			c.DB.User = "u"
			c.DB.Name = "db"
		}, ""},
		{"missing DB", func(c *Config) {
			c.DB.URL = ""
			c.DB.User = ""
			c.DB.Name = ""
		}, "DATABASE_URL"},
		{"missing internal token", func(c *Config) { c.InternalToken = "" }, "INTERNAL_API_TOKEN"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := baseValidConfig()
			tc.mutate(c)
			err := c.validate()
			if tc.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

func baseValidConfig() *Config {
	c := &Config{}
	c.DB.URL = "postgres://x"
	c.InternalToken = "internal-token"
	c.Log = logging.Config{Level: logging.LevelInfo, Format: logging.FormatJSON}
	return c
}
