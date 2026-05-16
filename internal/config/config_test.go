package config

import (
	"strings"
	"testing"
	"time"
)

func TestGetEnvOrDefault(t *testing.T) {
	t.Setenv("X_TEST_KEY", "")
	if got := getEnvOrDefault("X_TEST_KEY", "fallback"); got != "fallback" {
		t.Errorf("empty env: got %q, want fallback", got)
	}
	t.Setenv("X_TEST_KEY", "explicit")
	if got := getEnvOrDefault("X_TEST_KEY", "fallback"); got != "explicit" {
		t.Errorf("set env: got %q, want explicit", got)
	}
}

func TestGetEnvDuration(t *testing.T) {
	t.Setenv("X_DUR", "")
	if got := getEnvDuration("X_DUR", 5*time.Second); got != 5*time.Second {
		t.Errorf("fallback: got %v", got)
	}
	t.Setenv("X_DUR", "2m30s")
	if got := getEnvDuration("X_DUR", time.Second); got != 2*time.Minute+30*time.Second {
		t.Errorf("parsed: got %v", got)
	}
}

func TestGetEnvDurationPanicsOnInvalid(t *testing.T) {
	t.Setenv("X_DUR_BAD", "not-a-duration")
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic on malformed duration")
		}
		if !strings.Contains(r.(string), "X_DUR_BAD") {
			t.Errorf("panic should mention env key, got %v", r)
		}
	}()
	_ = getEnvDuration("X_DUR_BAD", time.Second)
}

func TestGetEnvInt(t *testing.T) {
	t.Setenv("X_INT", "")
	if got := getEnvInt("X_INT", 42); got != 42 {
		t.Errorf("fallback: got %d", got)
	}
	t.Setenv("X_INT", "17")
	if got := getEnvInt("X_INT", 1); got != 17 {
		t.Errorf("parsed: got %d", got)
	}
}

func TestGetEnvIntPanicsOnInvalid(t *testing.T) {
	t.Setenv("X_INT_BAD", "abc")
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on malformed int")
		}
	}()
	_ = getEnvInt("X_INT_BAD", 1)
}

func TestLoadScannerConfigDefaults(t *testing.T) {
	t.Setenv("SCAN_INTERVAL", "")
	t.Setenv("SCAN_CONCURRENCY", "")
	cfg := loadScannerConfig()
	if cfg.Interval != 5*time.Minute {
		t.Errorf("Interval default: %v", cfg.Interval)
	}
	if cfg.Concurrency != 8 {
		t.Errorf("Concurrency default: %d", cfg.Concurrency)
	}
}

func TestLoadScannerConfigPanicsOnZeroConcurrency(t *testing.T) {
	t.Setenv("SCAN_CONCURRENCY", "0")
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic when SCAN_CONCURRENCY < 1")
		}
	}()
	_ = loadScannerConfig()
}

func TestLoadScannerConfigPanicsOnNegativeConcurrency(t *testing.T) {
	t.Setenv("SCAN_CONCURRENCY", "-3")
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic when SCAN_CONCURRENCY < 1")
		}
	}()
	_ = loadScannerConfig()
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(c *Config)
		wantErr string
	}{
		{
			name:   "ok with DATABASE_URL",
			mutate: func(c *Config) {},
		},
		{
			name: "ok with discrete DB fields",
			mutate: func(c *Config) {
				c.DB.URL = ""
				c.DB.User = "u"
				c.DB.Name = "db"
			},
		},
		{
			name: "missing DB",
			mutate: func(c *Config) {
				c.DB.URL = ""
				c.DB.User = ""
				c.DB.Name = ""
			},
			wantErr: "DATABASE_URL",
		},
		{
			name: "missing SMTP host",
			mutate: func(c *Config) {
				c.SMTP.Host = ""
			},
			wantErr: "SMTP_HOST",
		},
		{
			name: "missing SMTP username",
			mutate: func(c *Config) {
				c.SMTP.Username = ""
			},
			wantErr: "SMTP_HOST",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := baseValidConfig()
			tc.mutate(c)
			err := c.validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want substring %q", err, tc.wantErr)
			}
		})
	}
}

func baseValidConfig() *Config {
	c := &Config{}
	c.DB.URL = "postgres://x"
	c.SMTP.Host = "smtp.example.com"
	c.SMTP.Username = "u"
	c.SMTP.Password = "p"
	return c
}
