package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// httpRequestDuration is the single RED metric: _count gives rate and errors,
// _bucket feeds histogram_quantile for latency.
var httpRequestDuration = promauto.NewHistogramVec(
	prometheus.HistogramOpts{
		Name:    "http_request_duration_seconds",
		Help:    "HTTP request latency in seconds, labeled by method, route, and status.",
		Buckets: prometheus.DefBuckets,
	},
	[]string{"method", "route", "status"},
)

// standardMethods caps method-label cardinality: unmatched routes accept
// arbitrary verbs, so off-list values collapse to "OTHER".
var standardMethods = map[string]bool{
	http.MethodGet: true, http.MethodHead: true, http.MethodPost: true,
	http.MethodPut: true, http.MethodPatch: true, http.MethodDelete: true,
	http.MethodConnect: true, http.MethodOptions: true, http.MethodTrace: true,
}

// MetricsMiddleware observes each request in a defer, so panic-recovered 500s
// are still recorded; c.FullPath() keeps the route label bounded.
func MetricsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		defer func() {
			route := c.FullPath()
			if route == "" {
				route = "unmatched"
			}
			method := c.Request.Method
			if !standardMethods[method] {
				method = "OTHER"
			}
			httpRequestDuration.
				WithLabelValues(method, route, strconv.Itoa(c.Writer.Status())).
				Observe(time.Since(start).Seconds())
		}()
		c.Next()
	}
}
