package platform_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/platform"
)

func TestAdminHandler_health(t *testing.T) {
	h := platform.AdminHandler()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", http.NoBody))
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestAdminHandler_metrics(t *testing.T) {
	h := platform.AdminHandler()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", http.NoBody))
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "# HELP")
}
