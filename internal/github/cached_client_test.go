package github

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/cache"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/domain"
)

type fakeStore struct {
	mu   sync.Mutex
	data map[string]string
}

func newFakeStore() *fakeStore { return &fakeStore{data: map[string]string{}} }

func (f *fakeStore) Get(_ context.Context, key string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.data[key]
	if !ok {
		return "", cache.ErrMiss
	}
	return v, nil
}

func (f *fakeStore) SetEx(_ context.Context, key, value string, _ time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.data[key] = value
	return nil
}

func TestCachedValidateRepoCachesOK(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	inner := NewClient("")
	inner.httpClient = srv.Client()
	inner.httpClient.Transport = &hostRewrite{target: srv.URL}
	c := NewCachedClient(inner, newFakeStore())

	for i := 0; i < 3; i++ {
		if err := c.ValidateRepo(context.Background(), "owner", "repo"); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	if calls != 1 {
		t.Fatalf("want 1 GitHub call after cache, got %d", calls)
	}
}

func TestCachedValidateRepoCachesNotFound(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	inner := NewClient("")
	inner.httpClient = srv.Client()
	inner.httpClient.Transport = &hostRewrite{target: srv.URL}
	c := NewCachedClient(inner, newFakeStore())

	for i := 0; i < 3; i++ {
		err := c.ValidateRepo(context.Background(), "owner", "repo")
		if !errors.Is(err, domain.ErrRepoNotFound) {
			t.Fatalf("call %d: want ErrRepoNotFound, got %v", i, err)
		}
	}
	if calls != 1 {
		t.Fatalf("404 not cached; got %d upstream calls", calls)
	}
}

func TestCachedGetLatestReleaseCachesTag(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		_, _ = w.Write([]byte(`{"tag_name":"v1.2.3"}`))
	}))
	defer srv.Close()

	inner := NewClient("")
	inner.httpClient = srv.Client()
	inner.httpClient.Transport = &hostRewrite{target: srv.URL}
	c := NewCachedClient(inner, newFakeStore())

	for i := 0; i < 2; i++ {
		tag, err := c.GetLatestRelease(context.Background(), "owner", "repo")
		if err != nil || tag != "v1.2.3" {
			t.Fatalf("call %d: tag=%q err=%v", i, tag, err)
		}
	}
	if calls != 1 {
		t.Fatalf("want 1 upstream call, got %d", calls)
	}
}

func TestCachedRateLimitNotCached(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	inner := NewClient("")
	inner.httpClient = srv.Client()
	inner.httpClient.Transport = &hostRewrite{target: srv.URL}
	c := NewCachedClient(inner, newFakeStore())

	_ = c.ValidateRepo(context.Background(), "owner", "repo")
	_ = c.ValidateRepo(context.Background(), "owner", "repo")

	if calls != 2 {
		t.Fatalf("rate-limit responses must NOT be cached; got %d calls", calls)
	}
}
