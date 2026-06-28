package cache

import (
	"testing"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfigDSN(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
		want string
	}{
		{
			name: "explicit URL wins",
			cfg:  Config{URL: "redis://override:6379/0", Host: "ignored"},
			want: "redis://override:6379/0",
		},
		{
			name: "empty host means no redis",
			cfg:  Config{},
			want: "",
		},
		{
			name: "no password",
			cfg:  Config{Host: "localhost", Port: "6379", DB: "0"},
			want: "redis://localhost:6379/0",
		},
		{
			name: "plain password",
			cfg:  Config{Host: "localhost", Port: "6379", Password: "secret", DB: "0"},
			want: "redis://:secret@localhost:6379/0",
		},
		{
			name: "password with URL-significant characters is escaped",
			cfg:  Config{Host: "localhost", Port: "6379", Password: "p@ss:w/o#rd", DB: "0"},
			want: "redis://:p%40ss%3Aw%2Fo%23rd@localhost:6379/0",
		},
	}
	for _, tc := range tests {
		t.Run(
			tc.name, func(t *testing.T) {
				assert.Equal(t, tc.want, tc.cfg.DSN())
			},
		)
	}
}

func TestConfigDSN_ParsesBackToOriginalPassword(t *testing.T) {
	cfg := Config{Host: "localhost", Port: "6379", Password: "p@ss:w/o#rd", DB: "0"}

	opts, err := redis.ParseURL(cfg.DSN())
	require.NoError(t, err)
	assert.Equal(t, cfg.Password, opts.Password)
}
