package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/observability"
)

func NewRouter(h *Handler) *gin.Engine {
	router := gin.New()
	router.Use(observability.MetricsMiddleware(), gin.Recovery())
	registerRoutes(router, h)
	return router
}

func registerRoutes(router *gin.Engine, h *Handler) {
	router.GET(
		"/health", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"status": "ok"})
		},
	)
	router.GET("/metrics", gin.WrapH(promhttp.Handler()))
	router.GET("/", subscribePage)

	limiter := newIPRateLimiter(subscribeBurst).middleware()

	apiGroup := router.Group("/api")
	{
		apiGroup.POST("/subscribe", limiter, h.Subscribe)
		apiGroup.GET("/subscriptions", limiter, h.GetSubscriptions)
		apiGroup.GET("/confirm/:token", h.ConfirmSubscription)
		apiGroup.GET("/unsubscribe/:token", h.Unsubscribe)
		apiGroup.POST("/unsubscribe/:token", h.Unsubscribe)
	}
}
