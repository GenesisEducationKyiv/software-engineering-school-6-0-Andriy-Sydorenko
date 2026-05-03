package github

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/domain"
)

// newTestClient wires a real Client at an httptest server via a host-rewriting transport.
func newTestClient(t *testing.T, handler http.HandlerFunc) (client *Client, cleanup func()) {
	t.Helper()
	srv := httptest.NewServer(handler)
	client = NewClient("")
	client.httpClient = srv.Client()
	client.httpClient.Transport = &hostRewrite{target: srv.URL, base: srv.Client().Transport}
	return client, srv.Close
}

type hostRewrite struct {
	target string
	base   http.RoundTripper
}

func (h *hostRewrite) RoundTrip(req *http.Request) (*http.Response, error) {
	// Loopback-only guard: a misconfigured test must not hit the real network.
	if !strings.HasPrefix(h.target, "http://127.0.0.1:") && !strings.HasPrefix(h.target, "http://[::1]:") {
		return nil, errors.New("hostRewrite: target must be an httptest loopback server")
	}
	rewritten := strings.Replace(req.URL.String(), "https://api.github.com", h.target, 1)
	newReq, err := http.NewRequestWithContext(req.Context(), req.Method, rewritten, req.Body) //nolint:gosec // G704: URL rewritten to httptest loopback target validated above; no attacker-reachable path
	if err != nil {
		return nil, err
	}
	newReq.Header = req.Header
	base := h.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(newReq)
}

func runValidateCase(t *testing.T, handler http.HandlerFunc, wantErr error, errorKind string) {
	t.Helper()
	c, cleanup := newTestClient(t, handler)
	defer cleanup()

	err := c.ValidateRepo(context.Background(), "owner", "repo")

	switch {
	case wantErr != nil:
		if !errors.Is(err, wantErr) {
			t.Fatalf("got err=%v, want %v", err, wantErr)
		}
	case errorKind != "":
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), errorKind) {
			t.Fatalf("want error containing %q, got %v", errorKind, err)
		}
	default:
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}
}

func TestValidateRepoStatusMapping(t *testing.T) {
	tests := []struct {
		name      string
		handler   http.HandlerFunc
		wantErr   error
		errorKind string
	}{
		{"200 OK",
			func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) },
			nil, ""},
		{"404 not found",
			func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) },
			domain.ErrRepoNotFound, ""},
		{"500 unexpected",
			func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusInternalServerError) },
			nil, "unexpected"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			runValidateCase(t, tc.handler, tc.wantErr, tc.errorKind)
		})
	}
}

func TestValidateRepoRateLimitDetection(t *testing.T) {
	tests := []struct {
		name      string
		handler   http.HandlerFunc
		wantErr   error
		errorKind string
	}{
		{"429 too many requests",
			func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTooManyRequests) },
			domain.ErrRateLimited, ""},
		{"403 primary rate limit (remaining=0)",
			func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("X-RateLimit-Remaining", "0")
				w.WriteHeader(http.StatusForbidden)
			},
			domain.ErrRateLimited, ""},
		{"403 secondary rate limit (Retry-After)",
			func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Retry-After", "60")
				w.WriteHeader(http.StatusForbidden)
			},
			domain.ErrRateLimited, ""},
		{"403 non-rate-limit (SAML required etc.)",
			func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusForbidden) },
			nil, "forbidden"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			runValidateCase(t, tc.handler, tc.wantErr, tc.errorKind)
		})
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
			name: "200 returns tag",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"tag_name":"v1.2.3"}`))
			},
			wantTag: "v1.2.3",
		},
		{
			name: "404 returns empty tag, no error",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNotFound)
			},
			wantTag: "",
		},
		{
			name: "429 surfaces domain.ErrRateLimited",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusTooManyRequests)
			},
			wantErr: domain.ErrRateLimited,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, cleanup := newTestClient(t, tc.handler)
			defer cleanup()

			tag, err := c.GetLatestRelease(context.Background(), "owner", "repo")

			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("got err=%v, want %v", err, tc.wantErr)
			}
			if tc.wantErr == nil && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tag != tc.wantTag {
				t.Fatalf("tag=%q, want %q", tag, tc.wantTag)
			}
		})
	}
}

func TestSendsAuthHeader(t *testing.T) {
	var gotAuth string
	handler := func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}
	srv := httptest.NewServer(http.HandlerFunc(handler))
	defer srv.Close()

	c := NewClient("secret-token")
	c.httpClient = srv.Client()
	c.httpClient.Transport = &hostRewrite{target: srv.URL}

	_ = c.ValidateRepo(context.Background(), "owner", "repo")

	if gotAuth != "Bearer secret-token" {
		t.Fatalf("auth header=%q, want Bearer secret-token", gotAuth)
	}
}
