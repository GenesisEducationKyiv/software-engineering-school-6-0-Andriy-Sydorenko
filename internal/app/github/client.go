package github

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/app/domain"
)

const defaultBaseURL = "https://api.github.com"

type Client struct {
	httpClient *http.Client
	token      string
	baseURL    string
}

type Option func(*Client)

func WithBaseURL(url string) Option {
	return func(c *Client) { c.baseURL = url }
}

func NewClient(cfg *Config, opts ...Option) *Client {
	c := &Client{
		httpClient: &http.Client{Timeout: cfg.RequestTimeout},
		token:      cfg.Token,
		baseURL:    defaultBaseURL,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

func (g *Client) ValidateRepo(ctx context.Context, owner, repo string) error {
	url := fmt.Sprintf("%s/repos/%s/%s", g.baseURL, owner, repo)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	g.setHeaders(req)

	resp, err := g.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to reach GitHub API: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		return nil
	case http.StatusNotFound:
		return domain.ErrRepoNotFound
	case http.StatusForbidden, http.StatusTooManyRequests:
		if isRateLimited(resp) {
			return domain.ErrRateLimited
		}
		return fmt.Errorf("GitHub API forbidden (not rate-limit): %d", resp.StatusCode)
	default:
		return fmt.Errorf("unexpected GitHub API status: %d", resp.StatusCode)
	}
}

func (g *Client) GetLatestRelease(ctx context.Context, owner, repo string) (string, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/releases/latest", g.baseURL, owner, repo)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	g.setHeaders(req)

	resp, err := g.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to reach GitHub API: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		var release struct {
			TagName string `json:"tag_name"`
		}
		if err := decodeJSON(resp.Body, &release); err != nil {
			return "", fmt.Errorf("failed to decode release response: %w", err)
		}
		return release.TagName, nil
	case http.StatusNotFound:
		return "", nil
	case http.StatusForbidden, http.StatusTooManyRequests:
		if isRateLimited(resp) {
			return "", domain.ErrRateLimited
		}
		return "", fmt.Errorf("GitHub API forbidden (not rate-limit): %d", resp.StatusCode)
	default:
		return "", fmt.Errorf("unexpected GitHub API status: %d", resp.StatusCode)
	}
}

// isRateLimited separates rate-limit 403s from unrelated 403s (SAML, etc.).
func isRateLimited(resp *http.Response) bool {
	if resp.StatusCode == http.StatusTooManyRequests {
		return true
	}
	if resp.Header.Get("X-RateLimit-Remaining") == "0" {
		return true
	}
	if retryAfter := resp.Header.Get("Retry-After"); retryAfter != "" {
		slog.Warn("github: rate-limited", "retry_after", retryAfter)
		return true
	}
	return false
}

func (g *Client) setHeaders(req *http.Request) {
	req.Header.Set("User-Agent", "repo-release-notifier")
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	if g.token != "" {
		req.Header.Set("Authorization", "Bearer "+g.token)
	}
}

func decodeJSON(r io.Reader, v any) error {
	if err := json.NewDecoder(r).Decode(v); err != nil {
		return fmt.Errorf("json decode: %w", err)
	}
	return nil
}
