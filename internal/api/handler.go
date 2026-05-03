package api

import (
	"context"
	"errors"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/domain"
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
		c.JSON(http.StatusBadRequest, domain.ErrorResponse{Error: "invalid input: email and repo are required"})
		return
	}

	err := h.service.Subscribe(c.Request.Context(), req)
	if err == nil {
		c.JSON(http.StatusOK, domain.MessageResponse{Message: "subscription successful, confirmation email sent"})
		return
	}

	switch {
	case errors.Is(err, domain.ErrInvalidRepoFormat):
		c.JSON(http.StatusBadRequest, domain.ErrorResponse{Error: err.Error()})
	case errors.Is(err, domain.ErrRepoNotFound):
		c.JSON(http.StatusNotFound, domain.ErrorResponse{Error: err.Error()})
	case errors.Is(err, domain.ErrAlreadySubscribed):
		c.JSON(http.StatusConflict, domain.ErrorResponse{Error: err.Error()})
	case errors.Is(err, domain.ErrRateLimited):
		c.JSON(http.StatusServiceUnavailable, domain.ErrorResponse{Error: "service temporarily unavailable, try again later"})
	default:
		log.Printf("subscribe error: %v", err)
		c.JSON(http.StatusInternalServerError, domain.ErrorResponse{Error: "internal server error"})
	}
}

func (h *Handler) Unsubscribe(c *gin.Context) {
	token := c.Param("token")

	err := h.service.Unsubscribe(c.Request.Context(), token)
	if err == nil {
		c.JSON(http.StatusOK, domain.MessageResponse{Message: "unsubscribed successfully"})
		return
	}

	switch {
	case errors.Is(err, domain.ErrTokenNotFound):
		c.JSON(http.StatusNotFound, domain.ErrorResponse{Error: "token not found"})
	default:
		log.Printf("unsubscribe error: %v", err)
		c.JSON(http.StatusInternalServerError, domain.ErrorResponse{Error: "internal server error"})
	}
}

func (h *Handler) GetSubscriptions(c *gin.Context) {
	email := c.Query("email")

	subs, err := h.service.GetSubscriptions(c.Request.Context(), email)
	if err == nil {
		c.JSON(http.StatusOK, subs)
		return
	}

	switch {
	case errors.Is(err, domain.ErrInvalidEmail):
		c.JSON(http.StatusBadRequest, domain.ErrorResponse{Error: err.Error()})
	default:
		log.Printf("get subscriptions error: %v", err)
		c.JSON(http.StatusInternalServerError, domain.ErrorResponse{Error: "internal server error"})
	}
}

func (h *Handler) ConfirmSubscription(c *gin.Context) {
	token := c.Param("token")

	err := h.service.ConfirmSubscription(c.Request.Context(), token)
	if err == nil {
		c.JSON(http.StatusOK, domain.MessageResponse{Message: "subscription confirmed successfully"})
		return
	}

	switch {
	case errors.Is(err, domain.ErrTokenNotFound):
		c.JSON(http.StatusNotFound, domain.ErrorResponse{Error: "token not found"})
	default:
		log.Printf("confirm error: %v", err)
		c.JSON(http.StatusInternalServerError, domain.ErrorResponse{Error: "internal server error"})
	}
}
