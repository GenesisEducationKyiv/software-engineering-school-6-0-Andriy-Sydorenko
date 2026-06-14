package github

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/app/domain"
)

// newTestClient: real Client routed at httptest app via a host-rewriting transport.
func newTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	c := NewClient(&Config{RequestTimeout: 10 * time.Second})
	c.httpClient = srv.Client()
	c.httpClient.Transport = &hostRewrite{target: srv.URL, base: srv.Client().Transport}
	return c
}

type hostRewrite struct {
	target string
	base   http.RoundTripper
}

func (h *hostRewrite) RoundTrip(req *http.Request) (*http.Response, error) {
	// Loopback-only: a misconfigured test must not hit the real network.
	if !strings.HasPrefix(h.target, "http://127.0.0.1:") && !strings.HasPrefix(
		h.target,
		"http://[::1]:",
	) {
		return nil, errors.New("hostRewrite: target must be an httptest loopback app")
	}

	targetURL, err := url.Parse(h.target)
	if err != nil {
		return nil, fmt.Errorf("parse target url: %w", err)
	}

	newReq := req.Clone(req.Context())
	newURL := *req.URL
	newURL.Scheme = targetURL.Scheme
	newURL.Host = targetURL.Host
	newReq.URL = &newURL
	newReq.Host = targetURL.Host
	newReq.Header = req.Header.Clone()

	base := h.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(newReq)
}

func TestValidateRepoStatusMapping(t *testing.T) {
	tests := []struct {
		name      string
		handler   http.HandlerFunc
		wantErr   error
		errorKind string
	}{
		{
			"200 OK",
			func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) },
			nil, "",
		},
		{
			"404 not found",
			func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) },
			domain.ErrRepoNotFound, "",
		},
		{
			"500 unexpected",
			func(
				w http.ResponseWriter,
				_ *http.Request,
			) {
				w.WriteHeader(http.StatusInternalServerError)
			},
			nil, "unexpected",
		},
	}
	for _, tc := range tests {
		t.Run(
			tc.name, func(t *testing.T) {
				c := newTestClient(t, tc.handler)
				err := c.ValidateRepo(context.Background(), "owner", "repo")

				switch {
				case tc.wantErr != nil:
					assert.ErrorIs(t, err, tc.wantErr)
				case tc.errorKind != "":
					require.Error(t, err)
					assert.Contains(t, err.Error(), tc.errorKind)
				default:
					assert.NoError(t, err)
				}
			},
		)
	}
}

func TestValidateRepoRateLimitDetection(t *testing.T) {
	tests := []struct {
		name      string
		handler   http.HandlerFunc
		wantErr   error
		errorKind string
	}{
		{
			"429 too many requests",
			func(
				w http.ResponseWriter,
				_ *http.Request,
			) {
				w.WriteHeader(http.StatusTooManyRequests)
			},
			domain.ErrRateLimited, "",
		},
		{
			"403 primary rate limit (remaining=0)",
			func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("X-RateLimit-Remaining", "0")
				w.WriteHeader(http.StatusForbidden)
			},
			domain.ErrRateLimited, "",
		},
		{
			"403 secondary rate limit (Retry-After)",
			func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Retry-After", "60")
				w.WriteHeader(http.StatusForbidden)
			},
			domain.ErrRateLimited, "",
		},
		{
			"403 non-rate-limit (SAML required etc.)",
			func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusForbidden) },
			nil, "forbidden",
		},
	}
	for _, tc := range tests {
		t.Run(
			tc.name, func(t *testing.T) {
				c := newTestClient(t, tc.handler)
				err := c.ValidateRepo(context.Background(), "owner", "repo")

				if tc.wantErr != nil {
					assert.ErrorIs(t, err, tc.wantErr)
				} else {
					require.Error(t, err)
					assert.Contains(t, err.Error(), tc.errorKind)
				}
			},
		)
	}
}

func TestGetLatestRelease(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		wantTag string
		wantErr error
	}{
		{
			"200 returns tag",
			func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"tag_name":"v1.2.3"}`))
			},
			"v1.2.3", nil,
		},
		{
			"404 returns empty tag, no error",
			func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) },
			"", nil,
		},
		{
			"429 surfaces domain.ErrRateLimited",
			func(
				w http.ResponseWriter,
				_ *http.Request,
			) {
				w.WriteHeader(http.StatusTooManyRequests)
			},
			"", domain.ErrRateLimited,
		},
	}

	for _, tc := range tests {
		t.Run(
			tc.name, func(t *testing.T) {
				c := newTestClient(t, tc.handler)
				tag, err := c.GetLatestRelease(context.Background(), "owner", "repo")

				if tc.wantErr != nil {
					assert.ErrorIs(t, err, tc.wantErr)
				} else {
					assert.NoError(t, err)
				}
				assert.Equal(t, tc.wantTag, tag)
			},
		)
	}
}

func TestSendsAuthHeader(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(
		http.HandlerFunc(
			func(w http.ResponseWriter, r *http.Request) {
				gotAuth = r.Header.Get("Authorization")
				w.WriteHeader(http.StatusOK)
			},
		),
	)
	defer srv.Close()

	c := NewClient(&Config{Token: "secret-token", RequestTimeout: 10 * time.Second})
	c.httpClient = srv.Client()
	c.httpClient.Transport = &hostRewrite{target: srv.URL}

	_ = c.ValidateRepo(context.Background(), "owner", "repo")

	assert.Equal(t, "Bearer secret-token", gotAuth)
}
