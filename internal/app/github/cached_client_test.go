package github

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/app/cache"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/app/domain"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/app/github/mocks"
)

// newCachedClient wires a mock Store + real Client + httptest upstream; the returned int counts upstream hits.
func newCachedClient(t *testing.T, handler http.HandlerFunc) (
	*CachedClient,
	*mocks.MockStore,
	*int,
) {
	t.Helper()
	var calls int
	srv := httptest.NewServer(
		http.HandlerFunc(
			func(w http.ResponseWriter, r *http.Request) {
				calls++
				handler(w, r)
			},
		),
	)
	t.Cleanup(srv.Close)

	inner := NewClient(&Config{RequestTimeout: 10 * time.Second})
	inner.httpClient = srv.Client()
	inner.httpClient.Transport = &hostRewrite{target: srv.URL}

	ctrl := gomock.NewController(t)
	store := mocks.NewMockStore(ctrl)
	return NewCachedClient(inner, store), store, &calls
}

func TestCachedValidateRepo_CachesOK(t *testing.T) {
	c, store, upstreamCalls := newCachedClient(
		t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		},
	)

	key := "gh:validate:owner/repo"
	store.EXPECT().Get(gomock.Any(), key).Return("", cache.ErrMiss)
	store.EXPECT().SetEx(gomock.Any(), key, cachedOK, gomock.Any()).Return(nil)
	store.EXPECT().Get(gomock.Any(), key).Return(cachedOK, nil).Times(2)

	for i := 0; i < 3; i++ {
		require.NoError(t, c.ValidateRepo(context.Background(), "owner", "repo"))
	}
	assert.Equal(t, 1, *upstreamCalls, "second and third calls must hit cache only")
}

func TestCachedValidateRepo_CachesNotFound(t *testing.T) {
	c, store, upstreamCalls := newCachedClient(
		t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		},
	)

	key := "gh:validate:owner/repo"
	store.EXPECT().Get(gomock.Any(), key).Return("", cache.ErrMiss)
	store.EXPECT().SetEx(gomock.Any(), key, cachedNotFound, gomock.Any()).Return(nil)
	store.EXPECT().Get(gomock.Any(), key).Return(cachedNotFound, nil).Times(2)

	for i := 0; i < 3; i++ {
		err := c.ValidateRepo(context.Background(), "owner", "repo")
		require.ErrorIs(t, err, domain.ErrRepoNotFound)
	}
	assert.Equal(t, 1, *upstreamCalls, "404s must be cached to avoid hammering GitHub")
}

func TestCachedGetLatestRelease_CachesTag(t *testing.T) {
	c, store, upstreamCalls := newCachedClient(
		t, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"tag_name":"v1.2.3"}`))
		},
	)

	key := "gh:latest:owner/repo"
	store.EXPECT().Get(gomock.Any(), key).Return("", cache.ErrMiss)
	store.EXPECT().SetEx(gomock.Any(), key, "v1.2.3", gomock.Any()).Return(nil)
	store.EXPECT().Get(gomock.Any(), key).Return("v1.2.3", nil)

	for i := 0; i < 2; i++ {
		tag, err := c.GetLatestRelease(context.Background(), "owner", "repo")
		require.NoError(t, err)
		assert.Equal(t, "v1.2.3", tag)
	}
	assert.Equal(t, 1, *upstreamCalls)
}

func TestCachedGetLatestRelease_CachesEmptyTag(t *testing.T) {
	// Repo with no releases: GitHub 404s /releases/latest, inner returns ("", nil).
	// The empty result must be cached as the sentinel and read back as ("", nil).
	c, store, upstreamCalls := newCachedClient(
		t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		},
	)

	key := "gh:latest:owner/repo"
	store.EXPECT().Get(gomock.Any(), key).Return("", cache.ErrMiss)
	store.EXPECT().SetEx(gomock.Any(), key, cachedEmptyTag, gomock.Any()).Return(nil)
	store.EXPECT().Get(gomock.Any(), key).Return(cachedEmptyTag, nil)

	for i := 0; i < 2; i++ {
		tag, err := c.GetLatestRelease(context.Background(), "owner", "repo")
		require.NoError(t, err)
		assert.Empty(t, tag, "sentinel must decode back to an empty tag, not %q", cachedEmptyTag)
	}
	assert.Equal(t, 1, *upstreamCalls, "empty-release result must be cached too")
}

func TestCachedRateLimitNotCached(t *testing.T) {
	// 429 must NOT be cached — would block calls past the rate-limit window. No SetEx EXPECT guards this.
	c, store, upstreamCalls := newCachedClient(
		t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusTooManyRequests)
		},
	)

	store.EXPECT().Get(gomock.Any(), gomock.Any()).Return("", cache.ErrMiss).Times(2)

	_ = c.ValidateRepo(context.Background(), "owner", "repo")
	_ = c.ValidateRepo(context.Background(), "owner", "repo")

	assert.Equal(t, 2, *upstreamCalls)
}

func TestCachedValidateRepo_FallsThroughOnStoreError(t *testing.T) {
	// Redis down (non-ErrMiss): must degrade to upstream, not fail the request.
	c, store, upstreamCalls := newCachedClient(
		t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		},
	)

	store.EXPECT().Get(gomock.Any(), gomock.Any()).Return(
		"",
		errors.New("redis: connection refused"),
	)
	store.EXPECT().SetEx(gomock.Any(), gomock.Any(), cachedOK, gomock.Any()).Return(nil)

	require.NoError(t, c.ValidateRepo(context.Background(), "owner", "repo"))
	assert.Equal(t, 1, *upstreamCalls, "broken cache must not break the request")
}

func TestCachedGetLatestRelease_FallsThroughOnStoreError(t *testing.T) {
	c, store, upstreamCalls := newCachedClient(
		t, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"tag_name":"v9.9.9"}`))
		},
	)

	store.EXPECT().Get(gomock.Any(), gomock.Any()).Return("", errors.New("redis: timeout"))
	store.EXPECT().SetEx(gomock.Any(), gomock.Any(), "v9.9.9", gomock.Any()).Return(nil)

	tag, err := c.GetLatestRelease(context.Background(), "owner", "repo")
	require.NoError(t, err)
	assert.Equal(t, "v9.9.9", tag)
	assert.Equal(t, 1, *upstreamCalls)
}
