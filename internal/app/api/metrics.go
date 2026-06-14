package api

import (
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var httpRequestDuration = promauto.NewHistogramVec(
	prometheus.HistogramOpts{
		Name:    "http_request_duration_seconds",
		Help:    "HTTP request latency in seconds, labeled by method, route, and status.",
		Buckets: prometheus.DefBuckets,
	},
	[]string{"method", "route", "status"},
)

func MetricsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		defer func() {
			duration := time.Since(start)
			status := c.Writer.Status()
			route := c.FullPath()
			if route == "" {
				route = "unmatched"
			}
			method := c.Request.Method
			switch method {
			case http.MethodGet, http.MethodHead, http.MethodPost,
				http.MethodPut, http.MethodPatch, http.MethodDelete:
			default:
				method = "OTHER"
			}
			httpRequestDuration.
				WithLabelValues(method, route, strconv.Itoa(status)).
				Observe(duration.Seconds())
			slog.InfoContext(
				c.Request.Context(), "http request",
				"method", method,
				"route", route,
				"status", status,
				"duration_ms", duration.Milliseconds(),
			)
		}()
		c.Next()
	}
}
