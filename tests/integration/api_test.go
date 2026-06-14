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

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/app/domain"
)

func doRequest(
	t *testing.T,
	env *testEnv,
	method, path string,
	body any,
	headers map[string]string,
) *httptest.ResponseRecorder {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
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

func TestHealth(t *testing.T) {
	env := newTestEnv(t)
	w := doRequest(t, env, http.MethodGet, "/health", nil, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("health status=%d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"status":"ok"`) {
		t.Fatalf("health body=%s", w.Body.String())
	}
}

func TestSubscribePage(t *testing.T) {
	env := newTestEnv(t)
	w := doRequest(t, env, http.MethodGet, "/", nil, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("page status=%d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("page content-type=%s", ct)
	}
	if !strings.Contains(strings.ToLower(w.Body.String()), "<html") {
		t.Fatalf("page body doesn't look like HTML")
	}
}

func TestSubscribeRequiresAPIKey(t *testing.T) {
	env := newTestEnv(t)

	w := doRequest(
		t, env, http.MethodPost, "/api/subscribe",
		domain.SubscribeRequest{Email: "a@b.com", Repo: "golang/go"}, nil,
	)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("missing key status=%d", w.Code)
	}

	w = doRequest(
		t, env, http.MethodPost, "/api/subscribe",
		domain.SubscribeRequest{Email: "a@b.com", Repo: "golang/go"},
		map[string]string{"X-API-Key": "wrong"},
	)
	if w.Code != http.StatusForbidden {
		t.Fatalf("wrong key status=%d", w.Code)
	}
}

func TestSubscribeHappyPath(t *testing.T) {
	env := newTestEnv(t)

	w := doRequest(
		t, env, http.MethodPost, "/api/subscribe",
		domain.SubscribeRequest{Email: "alice@example.com", Repo: "golang/go"}, authHeaders(),
	)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}

	// DB side-effects: one unconfirmed sub + one confirmation token.
	var subCount int64
	env.db.Raw(
		`SELECT COUNT(*) FROM subscriptions WHERE email=? AND confirmed=false`,
		"alice@example.com",
	).Scan(&subCount)
	if subCount != 1 {
		t.Fatalf("expected 1 unconfirmed sub, got %d", subCount)
	}
	var tokCount int64
	env.db.Raw(`SELECT COUNT(*) FROM confirmation_tokens`).Scan(&tokCount)
	if tokCount != 1 {
		t.Fatalf("expected 1 token, got %d", tokCount)
	}

	// Notifier was invoked with persisted tokens.
	sends := env.mailer.snapshot()
	if len(sends) != 1 || sends[0].email != "alice@example.com" || sends[0].repo != "golang/go" {
		t.Fatalf("unexpected mailer sends=%+v", sends)
	}
	if sends[0].token == "" || sends[0].unsubToken == "" {
		t.Fatalf("mailer got blank tokens: %+v", sends[0])
	}
}

func TestSubscribeInvalidRepoFormat(t *testing.T) {
	env := newTestEnv(t)
	w := doRequest(
		t, env, http.MethodPost, "/api/subscribe",
		domain.SubscribeRequest{Email: "a@b.com", Repo: "no-slash"}, authHeaders(),
	)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if env.github.callCount() != 0 {
		t.Fatal("github must not be called on invalid format")
	}
}

func TestSubscribeMalformedJSON(t *testing.T) {
	env := newTestEnv(t)
	req := httptest.NewRequest(http.MethodPost, "/api/subscribe", strings.NewReader("{not json"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", testAPIKey)
	w := httptest.NewRecorder()
	env.router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d", w.Code)
	}
}

func TestSubscribeDuplicate(t *testing.T) {
	env := newTestEnv(t)
	body := domain.SubscribeRequest{Email: "dup@example.com", Repo: "golang/go"}

	if w := doRequest(
		t,
		env,
		http.MethodPost,
		"/api/subscribe",
		body,
		authHeaders(),
	); w.Code != http.StatusOK {
		t.Fatalf("first status=%d", w.Code)
	}
	w := doRequest(t, env, http.MethodPost, "/api/subscribe", body, authHeaders())
	if w.Code != http.StatusConflict {
		t.Fatalf("duplicate status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestSubscribeRepoNotFound(t *testing.T) {
	env := newTestEnv(t)
	env.github.setErr(domain.ErrRepoNotFound)

	w := doRequest(
		t, env, http.MethodPost, "/api/subscribe",
		domain.SubscribeRequest{Email: "a@b.com", Repo: "ghost/ghost"}, authHeaders(),
	)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var count int64
	env.db.Raw(`SELECT COUNT(*) FROM subscriptions`).Scan(&count)
	if count != 0 {
		t.Fatalf("must not persist on github failure, got %d rows", count)
	}
}

func TestSubscribeGitHubRateLimited(t *testing.T) {
	env := newTestEnv(t)
	env.github.setErr(domain.ErrRateLimited)

	w := doRequest(
		t, env, http.MethodPost, "/api/subscribe",
		domain.SubscribeRequest{Email: "a@b.com", Repo: "golang/go"}, authHeaders(),
	)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d", w.Code)
	}
}

func TestConfirmFlow(t *testing.T) {
	env := newTestEnv(t)
	subscribeOK(t, env, "carol@example.com", "kubernetes/kubernetes")

	token := readTokenValue(t, env)

	w := doRequest(t, env, http.MethodGet, "/api/confirm/"+token, nil, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("confirm status=%d body=%s", w.Code, w.Body.String())
	}

	// Subscription is now confirmed AND the token row is gone.
	var confirmed bool
	env.db.Raw(
		`SELECT confirmed FROM subscriptions WHERE email=?`,
		"carol@example.com",
	).Scan(&confirmed)
	if !confirmed {
		t.Fatal("subscription should be confirmed")
	}
	var leftover int64
	env.db.Raw(`SELECT COUNT(*) FROM confirmation_tokens`).Scan(&leftover)
	if leftover != 0 {
		t.Fatalf("token row should be deleted, found %d", leftover)
	}
}

func TestConfirmUnknownToken(t *testing.T) {
	env := newTestEnv(t)
	w := doRequest(t, env, http.MethodGet, "/api/confirm/no-such-token", nil, nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status=%d", w.Code)
	}
}

func TestUnsubscribeGET(t *testing.T) {
	env := newTestEnv(t)
	subscribeOK(t, env, "dan@example.com", "golang/go")
	unsubToken := readUnsubscribeToken(t, env, "dan@example.com")

	w := doRequest(t, env, http.MethodGet, "/api/unsubscribe/"+unsubToken, nil, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("unsubscribe status=%d body=%s", w.Code, w.Body.String())
	}

	var live int64
	env.db.Raw(`SELECT COUNT(*) FROM subscriptions WHERE email=?`, "dan@example.com").Scan(&live)
	if live != 0 {
		t.Fatalf("expected sub deleted, found %d rows", live)
	}
}

func TestUnsubscribePOSTOneClick(t *testing.T) {
	env := newTestEnv(t)
	subscribeOK(t, env, "eve@example.com", "golang/go")
	unsubToken := readUnsubscribeToken(t, env, "eve@example.com")

	// RFC 8058 One-Click: POST is unauthenticated, token-only.
	w := doRequest(t, env, http.MethodPost, "/api/unsubscribe/"+unsubToken, nil, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("one-click status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestUnsubscribeUnknownToken(t *testing.T) {
	env := newTestEnv(t)
	w := doRequest(t, env, http.MethodGet, "/api/unsubscribe/ghost-token", nil, nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status=%d", w.Code)
	}
}

func TestGetSubscriptions(t *testing.T) {
	env := newTestEnv(t)
	subscribeOK(t, env, "frank@example.com", "golang/go")
	subscribeOK(t, env, "frank@example.com", "kubernetes/kubernetes")
	subscribeOK(t, env, "other@example.com", "golang/go")

	w := doRequest(
		t, env, http.MethodGet,
		"/api/subscriptions?email=frank@example.com", nil, authHeaders(),
	)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}

	var got []domain.SubscriptionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d subs, want 2: %+v", len(got), got)
	}
	for _, s := range got {
		if s.Email != "frank@example.com" {
			t.Errorf("leaked sub for other email: %+v", s)
		}
	}
}

func TestGetSubscriptionsRequiresAPIKey(t *testing.T) {
	env := newTestEnv(t)
	w := doRequest(t, env, http.MethodGet, "/api/subscriptions?email=a@b.com", nil, nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d", w.Code)
	}
}

func TestGetSubscriptionsInvalidEmail(t *testing.T) {
	env := newTestEnv(t)
	w := doRequest(t, env, http.MethodGet, "/api/subscriptions?email=not-email", nil, authHeaders())
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

// subscribeOK runs a successful subscribe against the live wiring,
// failing the test on anything unexpected.
func subscribeOK(t *testing.T, env *testEnv, email, repo string) {
	t.Helper()
	w := doRequest(
		t, env, http.MethodPost, "/api/subscribe",
		domain.SubscribeRequest{Email: email, Repo: repo}, authHeaders(),
	)
	if w.Code != http.StatusOK {
		t.Fatalf("subscribeOK email=%s status=%d body=%s", email, w.Code, w.Body.String())
	}
}

func readTokenValue(t *testing.T, env *testEnv) string {
	t.Helper()
	var token string
	if err := env.db.Raw(
		`SELECT token FROM confirmation_tokens ORDER BY id DESC LIMIT 1`,
	).Scan(&token).Error; err != nil {
		t.Fatalf("read token: %v", err)
	}
	if token == "" {
		t.Fatal("no confirmation token in DB")
	}
	return token
}

func readUnsubscribeToken(t *testing.T, env *testEnv, email string) string {
	t.Helper()
	var tok string
	if err := env.db.Raw(
		`SELECT unsubscribe_token FROM subscriptions WHERE email=?`,
		email,
	).Scan(&tok).Error; err != nil {
		t.Fatalf("read unsub token: %v", err)
	}
	if tok == "" {
		t.Fatalf("no unsub token for %s", email)
	}
	return tok
}
