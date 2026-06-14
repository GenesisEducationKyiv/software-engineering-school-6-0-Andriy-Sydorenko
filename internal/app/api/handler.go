package api

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/app/domain"
)

type Service interface {
	Subscribe(ctx context.Context, req domain.SubscribeRequest) error
	ConfirmSubscription(ctx context.Context, token string) error
	Unsubscribe(ctx context.Context, token string) error
	GetSubscriptions(ctx context.Context, email string) ([]domain.SubscriptionResponse, error)
}

type Handler struct {
	service Service
}

func NewHandler(service Service) *Handler {
	return &Handler{service: service}
}

func (h *Handler) Subscribe(c *gin.Context) {
	var req domain.SubscribeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(
			http.StatusBadRequest,
			domain.ErrorResponse{Error: "invalid input: email and repo are required"},
		)
		return
	}

	if err := h.service.Subscribe(c.Request.Context(), req); err != nil {
		writeError(c, "subscribe", err)
		return
	}
	c.JSON(
		http.StatusOK,
		domain.MessageResponse{Message: "subscription successful, confirmation email sent"},
	)
}

func (h *Handler) Unsubscribe(c *gin.Context) {
	if err := h.service.Unsubscribe(c.Request.Context(), c.Param("token")); err != nil {
		writeError(c, "unsubscribe", err)
		return
	}
	c.JSON(http.StatusOK, domain.MessageResponse{Message: "unsubscribed successfully"})
}

func (h *Handler) GetSubscriptions(c *gin.Context) {
	subs, err := h.service.GetSubscriptions(c.Request.Context(), c.Query("email"))
	if err != nil {
		writeError(c, "get subscriptions", err)
		return
	}
	c.JSON(http.StatusOK, subs)
}

func (h *Handler) ConfirmSubscription(c *gin.Context) {
	if err := h.service.ConfirmSubscription(c.Request.Context(), c.Param("token")); err != nil {
		writeError(c, "confirm", err)
		return
	}
	c.JSON(http.StatusOK, domain.MessageResponse{Message: "subscription confirmed successfully"})
}
