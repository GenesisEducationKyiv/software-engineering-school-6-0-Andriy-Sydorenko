package api

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/app/domain"
)

type Service interface {
	GetSubscriptions(ctx context.Context, email string) ([]domain.SubscriptionResponse, error)
}

type Handler struct {
	service Service
}

func NewHandler(service Service) *Handler {
	return &Handler{service: service}
}

func (h *Handler) GetSubscriptions(c *gin.Context) {
	subs, err := h.service.GetSubscriptions(c.Request.Context(), c.Query("email"))
	if err != nil {
		writeError(c, "get subscriptions", err)
		return
	}
	c.JSON(http.StatusOK, subs)
}
