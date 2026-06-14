//go:build e2e

package harness

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
)

// GHBehavior controls the response the fixture serves for a given owner.
type GHBehavior int

const (
	GHOK          GHBehavior = iota // 200 OK with minimal repo payload
	GHNotFound                      // 404
	GHRateLimited                   // 429 with rate-limit headers
	GHServerError                   // 500
)

// GitHubFixture is an httptest.Server that mimics the subset of api.github.com
// the app calls. Default behavior is GHOK for every owner; override per-owner
// via SetBehavior.
type GitHubFixture struct {
	srv *httptest.Server

	mu        sync.RWMutex
	behaviors map[string]GHBehavior
}

func newGitHubFixture() *GitHubFixture {
	f := &GitHubFixture{behaviors: map[string]GHBehavior{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	return f
}

// URL is the fixture's base URL — drop in as github.Config.BaseURL.
func (f *GitHubFixture) URL() string { return f.srv.URL }

// SetBehavior configures how the fixture responds for the given owner. Pass
// the empty string as owner to set the default for all unmapped owners.
func (f *GitHubFixture) SetBehavior(owner string, b GHBehavior) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.behaviors[owner] = b
}

// Reset clears all per-owner overrides; default GHOK resumes.
func (f *GitHubFixture) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.behaviors = map[string]GHBehavior{}
}

func (f *GitHubFixture) close() { f.srv.Close() }

// handle serves /repos/:owner/:repo and /repos/:owner/:repo/releases/latest.
func (f *GitHubFixture) handle(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
	if len(parts) < 3 || parts[0] != "repos" {
		http.NotFound(w, r)
		return
	}
	owner := parts[1]
	repo := parts[2]

	switch f.behaviorFor(owner) {
	case GHNotFound:
		http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
	case GHRateLimited:
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.Header().Set("Retry-After", "60")
		http.Error(w, `{"message":"API rate limit exceeded"}`, http.StatusTooManyRequests)
	case GHServerError:
		http.Error(w, `{"message":"app error"}`, http.StatusInternalServerError)
	default: // GHOK
		w.Header().Set("Content-Type", "application/json")
		if len(parts) == 5 && parts[3] == "releases" && parts[4] == "latest" {
			fmt.Fprintf(w, `{"tag_name":"v0.0.0-fixture-%s-%s"}`, owner, repo)
			return
		}
		fmt.Fprintf(w, `{"full_name":"%s/%s"}`, owner, repo)
	}
}

func (f *GitHubFixture) behaviorFor(owner string) GHBehavior {
	f.mu.RLock()
	defer f.mu.RUnlock()
	if b, ok := f.behaviors[owner]; ok {
		return b
	}
	if b, ok := f.behaviors[""]; ok {
		return b
	}
	return GHOK
}
