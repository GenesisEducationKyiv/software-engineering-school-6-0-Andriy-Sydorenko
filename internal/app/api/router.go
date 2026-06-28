package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func NewRouter(h *Handler, apiKey string) *gin.Engine {
	router := gin.New()
	router.Use(RequestID(), MetricsMiddleware(), gin.Recovery())
	registerRoutes(router, h, apiKey)
	return router
}

func registerRoutes(router *gin.Engine, h *Handler, apiKey string) {
	router.GET(
		"/health", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"status": "ok"})
		},
	)
	router.GET("/metrics", gin.WrapH(promhttp.Handler()))

	// confirm/unsubscribe moved to the orchestrator's public pages; the subscription
	// service handles them over NATS now (subscription.confirm / .unsubscribe).
	apiGroup := router.Group("/api")
	{
		protected := apiGroup.Group("")
		protected.Use(APIKeyAuth(apiKey))
		{
			protected.GET("/subscriptions", h.GetSubscriptions)
		}
	}
}
