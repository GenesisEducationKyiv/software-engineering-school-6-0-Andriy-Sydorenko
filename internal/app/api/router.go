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
	router.GET("/", subscribePage)

	apiGroup := router.Group("/api")
	{
		apiGroup.GET("/confirm/:token", h.ConfirmSubscription)
		apiGroup.GET("/unsubscribe/:token", h.Unsubscribe)
		apiGroup.POST("/unsubscribe/:token", h.Unsubscribe)

		protected := apiGroup.Group("")
		protected.Use(APIKeyAuth(apiKey))
		{
			protected.POST("/subscribe", h.Subscribe)
			protected.GET("/subscriptions", h.GetSubscriptions)
		}
	}
}
