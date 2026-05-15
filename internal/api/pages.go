package api

import (
	"log"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/templates"
)

func subscribePage(c *gin.Context) {
	body, err := templates.Page("subscribe.html")
	if err != nil {
		log.Printf("subscribe page render: %v", err)
		c.String(http.StatusInternalServerError, "internal server error")
		return
	}
	c.Data(http.StatusOK, "text/html; charset=utf-8", body)
}
