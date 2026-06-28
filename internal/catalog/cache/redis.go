package cache

import (
	"context"
	"errors"
	"net/url"
	"time"

	"github.com/redis/go-redis/v9"
)

var ErrMiss = errors.New("cache miss")

func (c *Config) DSN() string {
	if c.URL != "" {
		return c.URL
	}
	if c.Host == "" {
		return ""
	}
	u := url.URL{
		Scheme: "redis",
		Host:   c.Host + ":" + c.Port,
		Path:   "/" + c.DB,
	}
	if c.Password != "" {
		u.User = url.UserPassword("", c.Password)
	}
	return u.String()
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
