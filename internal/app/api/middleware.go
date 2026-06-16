package api

import (
	"crypto/subtle"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/app/domain"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/shared/observability/logging"
)

const requestIDHeader = "X-Request-ID"

// RequestID puts a correlation ID on the request context and echoes it in the
// response header. It reuses an inbound X-Request-ID if present, else mints a
// UUID. Mount first so the access log and every downstream call carry it.
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader(requestIDHeader)
		if id == "" {
			id = uuid.NewString()
		}
		c.Request = c.Request.WithContext(logging.WithRequestID(c.Request.Context(), id))
		c.Header(requestIDHeader, id)
		c.Next()
	}
}

func APIKeyAuth(apiKey string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if apiKey == "" {
			c.Next()
			return
		}

		key := c.GetHeader("X-API-Key")
		if key == "" {
			c.AbortWithStatusJSON(
				http.StatusUnauthorized,
				domain.ErrorResponse{Error: "missing API key"},
			)
			return
		}
		if subtle.ConstantTimeCompare([]byte(key), []byte(apiKey)) != 1 {
			c.AbortWithStatusJSON(
				http.StatusForbidden,
				domain.ErrorResponse{Error: "invalid API key"},
			)
			return
		}

		c.Next()
	}
}
