package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/app/domain"
)

func invokeWriteError(t *testing.T, err error) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	writeError(c, "op", err)
	return w
}

func decodeError(t *testing.T, w *httptest.ResponseRecorder) domain.ErrorResponse {
	t.Helper()
	var got domain.ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	return got
}

// Handler-level mapping lives in handler_test.go; these only cover what it can't:
// wrapped-sentinel matching and unmapped-error sanitization.

func TestWriteErrorMatchesWrappedSentinels(t *testing.T) {
	wrapped := fmt.Errorf("repo layer: %w", domain.ErrAlreadySubscribed)
	w := invokeWriteError(t, wrapped)
	assert.Equal(t, http.StatusConflict, w.Code, "wrapped sentinel must match via errors.Is")
}

func TestWriteErrorSanitizesUnmappedErrors(t *testing.T) {
	leaky := errors.New("pq: relation \"subscriptions\" does not exist at line 17")
	w := invokeWriteError(t, leaky)

	require.Equal(t, http.StatusInternalServerError, w.Code)
	body := decodeError(t, w)
	assert.Equal(t, "internal app error", body.Error, "must not leak internal driver text")
	assert.NotContains(t, body.Error, "pq:")
	assert.NotContains(t, body.Error, "subscriptions")
}
