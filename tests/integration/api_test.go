//go:build integration

package integration

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/domain"
)

func doRequest(t *testing.T, env *testEnv, method, path string, body any, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request body: %v", err)
		}
		rdr = bytes.NewReader(buf)
	}
	req := httptest.NewRequest(method, path, rdr)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	env.router.ServeHTTP(w, req)
	return w
}

func authHeaders() map[string]string {
	return map[string]string{"X-API-Key": testAPIKey}
}

func TestHealth_ReturnsOK(t *testing.T) {
	env := newTestEnv(t)
	w := doRequest(t, env, http.MethodGet, "/health", nil, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /health: want 200, got %d (body=%s)", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"status":"ok"`) {
		t.Fatalf(`GET /health: body should contain "status":"ok", got %s`, w.Body.String())
	}
}

func TestSubscribePage_RendersHTML(t *testing.T) {
	env := newTestEnv(t)
	w := doRequest(t, env, http.MethodGet, "/", nil, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /: want 200, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("GET /: want text/html content-type, got %q", ct)
	}
	if !strings.Contains(strings.ToLower(w.Body.String()), "<html") {
		t.Fatalf("GET /: body should be an HTML page (no <html tag found)")
	}
}

// Subscribe rejects a missing key (401) and a present-but-wrong key (403).
func TestSubscribe_RequiresValidAPIKey(t *testing.T) {
	env := newTestEnv(t)

	w := doRequest(t, env, http.MethodPost, "/api/subscribe",
		domain.SubscribeRequest{Email: "a@b.com", Repo: "golang/go"}, nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("subscribe with no API key: want 401, got %d", w.Code)
	}

	w = doRequest(t, env, http.MethodPost, "/api/subscribe",
		domain.SubscribeRequest{Email: "a@b.com", Repo: "golang/go"},
		map[string]string{"X-API-Key": "wrong"})
	if w.Code != http.StatusForbidden {
		t.Fatalf("subscribe with wrong API key: want 403, got %d", w.Code)
	}
}

// Subscribe persists exactly one unconfirmed row + one token and fires exactly
// one confirmation email carrying both the confirm and unsubscribe tokens.
func TestSubscribe_PersistsAndSendsConfirmation(t *testing.T) {
	env := newTestEnv(t)

	w := doRequest(t, env, http.MethodPost, "/api/subscribe",
		domain.SubscribeRequest{Email: "alice@example.com", Repo: "golang/go"}, authHeaders())
	if w.Code != http.StatusOK {
		t.Fatalf("subscribe: want 200, got %d (body=%s)", w.Code, w.Body.String())
	}

	// DB side-effects: exactly one unconfirmed sub + one confirmation token.
	var subCount int64
	env.db.Raw(`SELECT COUNT(*) FROM subscriptions WHERE email=? AND confirmed=false`, "alice@example.com").Scan(&subCount)
	if subCount != 1 {
		t.Fatalf("want 1 unconfirmed subscription persisted, got %d", subCount)
	}
	var tokCount int64
	env.db.Raw(`SELECT COUNT(*) FROM confirmation_tokens`).Scan(&tokCount)
	if tokCount != 1 {
		t.Fatalf("want 1 confirmation token persisted, got %d", tokCount)
	}

	// Notifier was invoked exactly once, with the persisted tokens.
	sends := env.mailer.snapshot()
	if len(sends) != 1 || sends[0].email != "alice@example.com" || sends[0].repo != "golang/go" {
		t.Fatalf("want exactly 1 confirmation email to alice@example.com for golang/go, got %+v", sends)
	}
	if sends[0].token == "" || sends[0].unsubToken == "" {
		t.Fatalf("confirmation email must carry both confirm and unsubscribe tokens, got %+v", sends[0])
	}
}

func TestSubscribe_RejectsInvalidRepoFormat(t *testing.T) {
	env := newTestEnv(t)
	w := doRequest(t, env, http.MethodPost, "/api/subscribe",
		domain.SubscribeRequest{Email: "a@b.com", Repo: "no-slash"}, authHeaders())
	if w.Code != http.StatusBadRequest {
		t.Fatalf("subscribe with invalid repo format: want 400, got %d (body=%s)", w.Code, w.Body.String())
	}
	if n := env.github.callCount(); n != 0 {
		t.Fatalf("invalid repo format must be rejected before any GitHub call, got %d calls", n)
	}
}

func TestSubscribe_RejectsMalformedJSON(t *testing.T) {
	env := newTestEnv(t)
	req := httptest.NewRequest(http.MethodPost, "/api/subscribe", strings.NewReader("{not json"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", testAPIKey)
	w := httptest.NewRecorder()
	env.router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("subscribe with malformed JSON: want 400, got %d", w.Code)
	}
}

func TestSubscribe_RejectsDuplicate(t *testing.T) {
	env := newTestEnv(t)
	body := domain.SubscribeRequest{Email: "dup@example.com", Repo: "golang/go"}

	if w := doRequest(t, env, http.MethodPost, "/api/subscribe", body, authHeaders()); w.Code != http.StatusOK {
		t.Fatalf("first subscribe should succeed: want 200, got %d", w.Code)
	}
	w := doRequest(t, env, http.MethodPost, "/api/subscribe", body, authHeaders())
	if w.Code != http.StatusConflict {
		t.Fatalf("duplicate subscribe: want 409, got %d (body=%s)", w.Code, w.Body.String())
	}
}

func TestSubscribe_RepoNotFoundDoesNotPersist(t *testing.T) {
	env := newTestEnv(t)
	env.github.setErr(domain.ErrRepoNotFound)

	w := doRequest(t, env, http.MethodPost, "/api/subscribe",
		domain.SubscribeRequest{Email: "a@b.com", Repo: "ghost/ghost"}, authHeaders())
	if w.Code != http.StatusNotFound {
		t.Fatalf("subscribe to nonexistent repo: want 404, got %d (body=%s)", w.Code, w.Body.String())
	}
	var count int64
	env.db.Raw(`SELECT COUNT(*) FROM subscriptions`).Scan(&count)
	if count != 0 {
		t.Fatalf("subscription must not be persisted when GitHub validation fails, got %d rows", count)
	}
}

func TestSubscribe_GitHubRateLimitedReturns503(t *testing.T) {
	env := newTestEnv(t)
	env.github.setErr(domain.ErrRateLimited)

	w := doRequest(t, env, http.MethodPost, "/api/subscribe",
		domain.SubscribeRequest{Email: "a@b.com", Repo: "golang/go"}, authHeaders())
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("subscribe while GitHub is rate-limited: want 503, got %d", w.Code)
	}
}

// Confirm flips the subscription to confirmed and consumes (soft-deletes) the
// one-time confirmation token.
func TestConfirm_MarksConfirmedAndConsumesToken(t *testing.T) {
	env := newTestEnv(t)
	subscribeOK(t, env, "carol@example.com", "kubernetes/kubernetes")

	token := readConfirmationToken(t, env)

	w := doRequest(t, env, http.MethodGet, "/api/confirm/"+token, nil, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("confirm with valid token: want 200, got %d (body=%s)", w.Code, w.Body.String())
	}

	// Subscription is now confirmed AND the token row is gone.
	var confirmed bool
	env.db.Raw(`SELECT confirmed FROM subscriptions WHERE email=?`, "carol@example.com").Scan(&confirmed)
	if !confirmed {
		t.Fatal("subscription should be confirmed=true after confirm")
	}
	var leftover int64
	env.db.Raw(`SELECT COUNT(*) FROM confirmation_tokens WHERE deleted_at IS NULL`).Scan(&leftover)
	if leftover != 0 {
		t.Fatalf("confirmation token should be consumed (soft-deleted) after confirm, found %d live", leftover)
	}
}

func TestConfirm_UnknownTokenReturns404(t *testing.T) {
	env := newTestEnv(t)
	w := doRequest(t, env, http.MethodGet, "/api/confirm/no-such-token", nil, nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("confirm with unknown token: want 404, got %d", w.Code)
	}
}

func TestUnsubscribe_GETSoftDeletes(t *testing.T) {
	env := newTestEnv(t)
	subscribeOK(t, env, "dan@example.com", "golang/go")
	unsubToken := readUnsubscribeToken(t, env, "dan@example.com")

	w := doRequest(t, env, http.MethodGet, "/api/unsubscribe/"+unsubToken, nil, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET unsubscribe with valid token: want 200, got %d (body=%s)", w.Code, w.Body.String())
	}

	var live int64
	env.db.Raw(`SELECT COUNT(*) FROM subscriptions WHERE email=? AND deleted_at IS NULL`, "dan@example.com").Scan(&live)
	if live != 0 {
		t.Fatalf("subscription should be soft-deleted after unsubscribe, found %d live rows", live)
	}
}

func TestUnsubscribe_POSTOneClickSoftDeletes(t *testing.T) {
	env := newTestEnv(t)
	subscribeOK(t, env, "eve@example.com", "golang/go")
	unsubToken := readUnsubscribeToken(t, env, "eve@example.com")

	// RFC 8058 One-Click: POST is unauthenticated, token-only.
	w := doRequest(t, env, http.MethodPost, "/api/unsubscribe/"+unsubToken, nil, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("POST one-click unsubscribe: want 200, got %d (body=%s)", w.Code, w.Body.String())
	}
}

func TestUnsubscribe_UnknownTokenReturns404(t *testing.T) {
	env := newTestEnv(t)
	w := doRequest(t, env, http.MethodGet, "/api/unsubscribe/ghost-token", nil, nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("unsubscribe with unknown token: want 404, got %d", w.Code)
	}
}

// GetSubscriptions returns only the queried email's rows — no cross-email leakage.
func TestGetSubscriptions_ReturnsOnlyMatchingEmail(t *testing.T) {
	env := newTestEnv(t)
	subscribeOK(t, env, "frank@example.com", "golang/go")
	subscribeOK(t, env, "frank@example.com", "kubernetes/kubernetes")
	subscribeOK(t, env, "other@example.com", "golang/go")

	w := doRequest(t, env, http.MethodGet,
		"/api/subscriptions?email=frank@example.com", nil, authHeaders())
	if w.Code != http.StatusOK {
		t.Fatalf("list subscriptions: want 200, got %d (body=%s)", w.Code, w.Body.String())
	}

	var got []domain.SubscriptionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode subscriptions response: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("frank has 2 subscriptions: want 2 rows, got %d: %+v", len(got), got)
	}
	for _, s := range got {
		if s.Email != "frank@example.com" {
			t.Errorf("response leaked another user's subscription: %+v", s)
		}
	}
}

func TestGetSubscriptions_RequiresAPIKey(t *testing.T) {
	env := newTestEnv(t)
	w := doRequest(t, env, http.MethodGet, "/api/subscriptions?email=a@b.com", nil, nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("list subscriptions with no API key: want 401, got %d", w.Code)
	}
}

func TestGetSubscriptions_RejectsInvalidEmail(t *testing.T) {
	env := newTestEnv(t)
	w := doRequest(t, env, http.MethodGet, "/api/subscriptions?email=not-email", nil, authHeaders())
	if w.Code != http.StatusBadRequest {
		t.Fatalf("list subscriptions with invalid email: want 400, got %d (body=%s)", w.Code, w.Body.String())
	}
}

// subscribeOK runs a successful subscribe against the live wiring,
// failing the test on anything unexpected.
func subscribeOK(t *testing.T, env *testEnv, email, repo string) {
	t.Helper()
	w := doRequest(t, env, http.MethodPost, "/api/subscribe",
		domain.SubscribeRequest{Email: email, Repo: repo}, authHeaders())
	if w.Code != http.StatusOK {
		t.Fatalf("subscribeOK(%s): want 200, got %d (body=%s)", email, w.Code, w.Body.String())
	}
}

func readConfirmationToken(t *testing.T, env *testEnv) string {
	t.Helper()
	var token string
	if err := env.db.Raw(
		`SELECT token FROM confirmation_tokens WHERE deleted_at IS NULL ORDER BY id DESC LIMIT 1`,
	).Scan(&token).Error; err != nil {
		t.Fatalf("read confirmation token: %v", err)
	}
	if token == "" {
		t.Fatal("no confirmation token found in DB")
	}
	return token
}

func readUnsubscribeToken(t *testing.T, env *testEnv, email string) string {
	t.Helper()
	var token string
	if err := env.db.Raw(
		`SELECT unsubscribe_token FROM subscriptions WHERE email=? AND deleted_at IS NULL`,
		email,
	).Scan(&token).Error; err != nil {
		t.Fatalf("read unsubscribe token: %v", err)
	}
	if token == "" {
		t.Fatalf("no unsubscribe token found for %s", email)
	}
	return token
}
