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

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

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

func TestGetSubscriptions(t *testing.T) {
	env := newTestEnv(t)
	seedSubscription(t, env, "frank@example.com", "golang/go")
	seedSubscription(t, env, "frank@example.com", "kubernetes/kubernetes")
	seedSubscription(t, env, "other@example.com", "golang/go")

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

// seedSubscription inserts an unconfirmed subscription + its confirmation token
// directly, standing in for the subscribe flow that now lives in the orchestrator.
func seedSubscription(t *testing.T, env *testEnv, email, repo string) {
	t.Helper()
	sub := &domain.Subscription{
		PublicID:         uuid.NewString(),
		Email:            email,
		Repo:             repo,
		UnsubscribeToken: uuid.NewString(),
	}
	require.NoError(t, env.db.Create(sub).Error)
	tok := &domain.ConfirmationToken{Token: uuid.NewString(), SubscriptionID: sub.ID}
	require.NoError(t, env.db.Create(tok).Error)
}
