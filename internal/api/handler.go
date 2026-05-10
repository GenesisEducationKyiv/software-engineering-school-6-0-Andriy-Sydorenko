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

	if err := h.service.Subscribe(c.Request.Context(), req); err != nil {
		writeError(c, "subscribe", err)
		return
	}
	c.JSON(http.StatusOK, domain.MessageResponse{Message: "subscription successful, confirmation email sent"})
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

// writeError maps a service-layer error to an HTTP response. Domain
// errors expose their own message; unknown errors are logged with the
// operation tag and return a generic 500 so internals don't leak.
func writeError(c *gin.Context, op string, err error) {
	status := httpStatus(err)
	if status == http.StatusInternalServerError {
		log.Printf("%s error: %v", op, err)
		c.JSON(status, domain.ErrorResponse{Error: "internal server error"})
		return
	}
	c.JSON(status, domain.ErrorResponse{Error: errorMessage(err)})
}

// errorMessage selects the user-visible string for a known domain
// error, applying friendlier copy where the sentinel's text is too
// terse for a public response.
func errorMessage(err error) string {
	switch {
	case errors.Is(err, domain.ErrTokenNotFound):
		return "token not found"
	case errors.Is(err, domain.ErrRateLimited):
		return "service temporarily unavailable, try again later"
	default:
		return err.Error()
	}
}
