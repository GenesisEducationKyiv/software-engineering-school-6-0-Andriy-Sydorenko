package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// These tests share the global Prometheus registry, so never add t.Parallel().
func TestMetricsSmoke(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(MetricsMiddleware())
	r.GET("/ping", func(c *gin.Context) { c.Status(http.StatusOK) })
	r.GET("/metrics", gin.WrapH(promhttp.Handler()))

	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/ping", http.NoBody))

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/metrics", http.NoBody))
	body := w.Body.String()
	if !strings.Contains(body, "http_request_duration_seconds_bucket") {
		t.Fatalf("histogram missing from /metrics")
	}
	if !strings.Contains(body, `route="/ping"`) {
		t.Fatalf("expected route label for /ping, got:\n%s", body)
	}
}

func TestMetricsRecordsPanicsAndUnmatched(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(MetricsMiddleware(), gin.Recovery())
	r.GET("/boom", func(c *gin.Context) { panic("kaboom") })
	r.GET("/metrics", gin.WrapH(promhttp.Handler()))

	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/boom", http.NoBody))
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("WEIRDVERB", "/nope", http.NoBody))

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/metrics", http.NoBody))
	body := w.Body.String()

	if !strings.Contains(body, `route="/boom",status="500"`) {
		t.Fatalf("panicked request not recorded as 500, got:\n%s", body)
	}
	if !strings.Contains(body, `method="OTHER",route="unmatched"`) {
		t.Fatalf("non-standard method not normalized to OTHER, got:\n%s", body)
	}
}

func TestMetricsUnmatchedAndExplicit500(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(MetricsMiddleware())
	r.GET("/fail", func(c *gin.Context) { c.JSON(http.StatusInternalServerError, gin.H{}) })
	r.GET("/metrics", gin.WrapH(promhttp.Handler()))

	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/no-such-route", http.NoBody))
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/fail", http.NoBody))

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/metrics", http.NoBody))
	body := w.Body.String()

	if !strings.Contains(body, `route="unmatched",status="404"`) {
		t.Fatalf("unregistered path not recorded as unmatched/404, got:\n%s", body)
	}
	if !strings.Contains(body, `route="/fail",status="500"`) {
		t.Fatalf("explicit 500 not recorded, got:\n%s", body)
	}
}
