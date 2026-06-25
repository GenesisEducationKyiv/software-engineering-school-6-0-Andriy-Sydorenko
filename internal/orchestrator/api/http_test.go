package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/orchestrator/domain"
)

type stubActions struct {
	confirmErr error
	unsubErr   error
}

func (s stubActions) Confirm(context.Context, string) error     { return s.confirmErr }
func (s stubActions) Unsubscribe(context.Context, string) error { return s.unsubErr }

func get(t *testing.T, h *HTTPHandler, path string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	NewRouter(h).ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, http.NoBody))
	return w
}

func TestConfirmPage(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		status int
		body   string
	}{
		{"ok", nil, http.StatusOK, "confirmed"},
		{"invalid token", domain.ErrTokenNotFound, http.StatusNotFound, "invalid or expired"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := get(t, NewHTTPHandler(nil, stubActions{confirmErr: tc.err}), "/confirm/tok")
			if w.Code != tc.status {
				t.Fatalf("status=%d want %d", w.Code, tc.status)
			}
			if !strings.Contains(strings.ToLower(w.Body.String()), tc.body) {
				t.Fatalf("body missing %q: %s", tc.body, w.Body.String())
			}
		})
	}
}

func TestUnsubscribePage(t *testing.T) {
	w := get(t, NewHTTPHandler(nil, stubActions{}), "/unsubscribe/tok")
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d", w.Code)
	}
	if !strings.Contains(strings.ToLower(w.Body.String()), "unsubscribed") {
		t.Fatalf("body: %s", w.Body.String())
	}
}
