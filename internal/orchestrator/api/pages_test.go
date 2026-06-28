package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestSubscribePageServed locks in that the orchestrator serves the subscribe
// form at GET / and that the form posts same-origin to /subscribe (the bug that
// orphaned it was a stale /api/subscribe target on the old subscription host).
func TestSubscribePageServed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// GET / serves a static embed; it never touches the deps, so nil is fine.
	router := NewRouter(NewHTTPHandler(nil, nil))

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", http.NoBody))

	if w.Code != http.StatusOK {
		t.Fatalf("page status=%d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("page content-type=%s", ct)
	}
	body := w.Body.String()
	if !strings.Contains(body, "<form") {
		t.Fatalf("page body is not the subscribe form")
	}
	if !strings.Contains(body, "fetch('/subscribe'") {
		t.Fatalf("subscribe form must post to /subscribe, not a stale path")
	}
}
