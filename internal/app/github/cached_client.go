package github

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/app/cache"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/app/domain"
)

const cacheTTL = 10 * time.Minute

// Sentinel values stored in Redis to distinguish cached negatives from a
// real tag. "__none__" can't collide with any valid Git tag.
const (
	cachedOK       = "ok"
	cachedNotFound = "404"
	cachedEmptyTag = "__none__"
)

type Store interface {
	Get(ctx context.Context, key string) (string, error)
	SetEx(ctx context.Context, key, value string, ttl time.Duration) error
}

type CachedClient struct {
	inner *Client
	store Store
}

func NewCachedClient(inner *Client, store Store) *CachedClient {
	return &CachedClient{inner: inner, store: store}
}

func (c *CachedClient) ValidateRepo(ctx context.Context, owner, repo string) error {
	key := "gh:validate:" + owner + "/" + repo

	if v, err := c.store.Get(ctx, key); err == nil {
		switch v {
		case cachedOK:
			return nil
		case cachedNotFound:
			return domain.ErrRepoNotFound
		}
	} else if !errors.Is(err, cache.ErrMiss) {
		slog.WarnContext(ctx, "cache: get failed", "key", key, "err", err)
	}

	err := c.inner.ValidateRepo(ctx, owner, repo)
	switch {
	case err == nil:
		c.set(ctx, key, cachedOK)
	case errors.Is(err, domain.ErrRepoNotFound):
		c.set(ctx, key, cachedNotFound)
	}
	return err
}

func (c *CachedClient) GetLatestRelease(ctx context.Context, owner, repo string) (string, error) {
	key := "gh:latest:" + owner + "/" + repo

	if v, err := c.store.Get(ctx, key); err == nil {
		if v == cachedEmptyTag {
			return "", nil
		}
		return v, nil
	} else if !errors.Is(err, cache.ErrMiss) {
		slog.WarnContext(ctx, "cache: get failed", "key", key, "err", err)
	}

	tag, err := c.inner.GetLatestRelease(ctx, owner, repo)
	if err != nil {
		return "", err
	}
	if tag == "" {
		c.set(ctx, key, cachedEmptyTag)
	} else {
		c.set(ctx, key, tag)
	}
	return tag, nil
}

func (c *CachedClient) set(ctx context.Context, key, value string) {
	if err := c.store.SetEx(ctx, key, value, cacheTTL); err != nil {
		slog.WarnContext(ctx, "cache: set failed", "key", key, "err", err)
	}
}
