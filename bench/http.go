package bench

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/notifier"
)

const (
	httpPathConfirmation = "/v1/confirmation"
	httpPathRelease      = "/v1/release-notifications"
	bearerPrefix         = "Bearer "
)

// newHTTPHandler builds the idiomatic HTTP/JSON handler over the shared Core.
// Routing via http.ServeMux; auth via a transport-neutral middleware so the
// bearer check is symmetric with the gRPC AuthServerInterceptor.
func newHTTPHandler(core *notifier.Core) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("POST "+httpPathConfirmation, func(w http.ResponseWriter, r *http.Request) {
		var req jsonSendConfirmationRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		sent, failed, err := core.SendConfirmation(
			r.Context(), req.Email, req.Repo, req.ConfirmURL, req.UnsubscribeURL,
		)
		writeAck(w, sent, failed, err)
	})

	mux.HandleFunc("POST "+httpPathRelease, func(w http.ResponseWriter, r *http.Request) {
		var req jsonSendReleaseNotificationsRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		recipients := make([]notifier.Recipient, len(req.Recipients))
		for i, rc := range req.Recipients {
			recipients[i] = notifier.Recipient{Email: rc.Email, UnsubscribeURL: rc.UnsubscribeURL}
		}
		sent, failed, err := core.SendReleaseNotifications(
			r.Context(), req.Repo, req.Tag, req.NotesURL, recipients,
		)
		writeAck(w, sent, failed, err)
	})

	return authMiddleware(token, mux)
}

func writeAck(w http.ResponseWriter, sent, failed uint32, err error) {
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(jsonSendAck{Sent: sent, Failed: failed})
}

// authMiddleware enforces the same bearer token the gRPC server checks, using a
// constant-time compare (symmetric with platform.AuthServerInterceptor).
func authMiddleware(want string, next http.Handler) http.Handler {
	wantBytes := []byte(want)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := []byte(strings.TrimPrefix(r.Header.Get("Authorization"), bearerPrefix))
		if subtle.ConstantTimeCompare(got, wantBytes) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// httpClient is the persistent client used by the HTTP benchmarks. It keeps
// connections alive and pools them, so per-call connection setup is NOT measured
// (spec §8 fairness — mirrors the persistent gRPC channel).
type httpClient struct {
	base   string
	client *http.Client
}

// newHTTPClient builds a keep-alive, pooled HTTP client targeting base.
func newHTTPClient(base string) *httpClient {
	transport := &http.Transport{
		DialContext:         (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 100,
		IdleConnTimeout:     90 * time.Second,
		DisableCompression:  true, // measure raw JSON bytes, not gzip
	}
	return &httpClient{
		base:   base,
		client: &http.Client{Transport: transport, Timeout: 30 * time.Second},
	}
}

func (c *httpClient) post(ctx context.Context, path string, body any) (jsonSendAck, error) {
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(body); err != nil {
		return jsonSendAck{}, fmt.Errorf("encode %s: %w", path, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+path, &buf)
	if err != nil {
		return jsonSendAck{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", bearerPrefix+token)

	resp, err := c.client.Do(req)
	if err != nil {
		return jsonSendAck{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// Drain so the connection can be reused even on the error path.
		msg, _ := io.ReadAll(resp.Body)
		return jsonSendAck{}, fmt.Errorf("%s: status %d: %s", path, resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	var ack jsonSendAck
	if err := json.NewDecoder(resp.Body).Decode(&ack); err != nil {
		return jsonSendAck{}, fmt.Errorf("decode ack %s: %w", path, err)
	}
	return ack, nil
}

func (c *httpClient) sendConfirmation(ctx context.Context, email, repo, confirmURL, unsubscribeURL string) (jsonSendAck, error) {
	return c.post(ctx, httpPathConfirmation, jsonSendConfirmationRequest{
		Email: email, Repo: repo, ConfirmURL: confirmURL, UnsubscribeURL: unsubscribeURL,
	})
}

func (c *httpClient) sendRelease(ctx context.Context, repo, tag, notesURL string, recipients []notifier.Recipient) (jsonSendAck, error) {
	jr := make([]jsonRecipient, len(recipients))
	for i, r := range recipients {
		jr[i] = jsonRecipient{Email: r.Email, UnsubscribeURL: r.UnsubscribeURL}
	}
	return c.post(ctx, httpPathRelease, jsonSendReleaseNotificationsRequest{
		Repo: repo, Tag: tag, NotesURL: notesURL, Recipients: jr,
	})
}
