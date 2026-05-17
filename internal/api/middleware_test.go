package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func newAuthedRouter(apiKey string) *gin.Engine {
	r := gin.New()
	g := r.Group("/")
	g.Use(APIKeyAuth(apiKey))
	g.GET("/guarded", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	return r
}

func TestAPIKeyAuth(t *testing.T) {
	tests := []struct {
		name       string
		configured string
		sent       string
		wantStatus int
	}{
		{"auth disabled when key empty", "", "", http.StatusOK},
		{"valid key", "secret", "secret", http.StatusOK},
		{"missing header", "secret", "", http.StatusUnauthorized},
		{"wrong key", "secret", "other", http.StatusForbidden},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := newAuthedRouter(tc.configured)
			req := httptest.NewRequest(http.MethodGet, "/guarded", http.NoBody)
			if tc.sent != "" {
				req.Header.Set("X-API-Key", tc.sent)
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			assert.Equal(t, tc.wantStatus, w.Code)
		})
	}
}
