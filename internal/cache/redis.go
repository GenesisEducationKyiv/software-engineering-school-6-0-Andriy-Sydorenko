// Package cache wraps Redis with the two primitives the project uses.
package cache

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// ErrMiss is returned from Get when the key is not present.
var ErrMiss = errors.New("cache miss")

// Config bundles cache knobs. URL wins when set; otherwise Host/Port/
// Password/DB assemble one. Empty Host (with empty URL) means "no Redis".
type Config struct {
	URL      string
	Host     string
	Port     string
	Password string
	DB       string
}

// DSN returns the connection URL, or "" when no Redis is configured.
func (c *Config) DSN() string {
	if c.URL != "" {
		return c.URL
	}
	if c.Host == "" {
		return ""
	}
	auth := ""
	if c.Password != "" {
		auth = ":" + c.Password + "@"
	}
	return fmt.Sprintf("redis://%s%s:%s/%s", auth, c.Host, c.Port, c.DB)
}

type Redis struct {
	client *redis.Client
}

func NewRedis(cfg *Config) (*Redis, error) {
	opts, err := redis.ParseURL(cfg.DSN())
	if err != nil {
		return nil, err
	}
	client := redis.NewClient(opts)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, err
	}
	return &Redis{client: client}, nil
}

// Get translates go-redis' redis.Nil into ErrMiss.
func (r *Redis) Get(ctx context.Context, key string) (string, error) {
	v, err := r.client.Get(ctx, key).Result()
	if errors.Is(err, redis.Nil) {
		return "", ErrMiss
	}
	return v, err
}

func (r *Redis) SetEx(ctx context.Context, key, value string, ttl time.Duration) error {
	return r.client.Set(ctx, key, value, ttl).Err()
}
