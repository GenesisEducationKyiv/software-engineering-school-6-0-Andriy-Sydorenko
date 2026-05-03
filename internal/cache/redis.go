// Package cache wraps Redis with the two primitives the project uses.
package cache

import (
	"context"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

// ErrMiss is returned from Get when the key is not present.
var ErrMiss = errors.New("cache miss")

type Redis struct {
	client *redis.Client
}

func NewRedis(url string) (*Redis, error) {
	opts, err := redis.ParseURL(url)
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

func (r *Redis) Close() error {
	return r.client.Close()
}
