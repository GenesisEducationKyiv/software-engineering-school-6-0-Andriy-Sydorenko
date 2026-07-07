package api

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/app/templates"
)

func subscribePage(c *gin.Context) {
	body, err := templates.Page("subscribe.html")
	if err != nil {
		slog.ErrorContext(c.Request.Context(), "subscribe page render", "err", err)
		c.String(http.StatusInternalServerError, "internal app error")
		return
	}
	c.Data(http.StatusOK, "text/html; charset=utf-8", body)
}
