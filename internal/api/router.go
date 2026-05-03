package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func NewRouter(h *Handler, apiKey string) *gin.Engine {
	router := gin.Default()
	RegisterRoutes(router, h, apiKey)
	return router
}

func RegisterRoutes(router *gin.Engine, h *Handler, apiKey string) {
	router.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	router.GET("/", subscribePage)

	apiGroup := router.Group("/api")
	{
		// Token-in-URL routes: opened from mail clients, no API key.
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
