package api

import (
	"context"
	"errors"
	"net/http"
	"regexp"

	"github.com/gin-gonic/gin"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/orchestrator/domain"
)

var repoFormatRegex = regexp.MustCompile(`^[a-zA-Z0-9._-]+/[a-zA-Z0-9._-]+$`)

// maxSubscribeBody caps the request body on the public subscribe endpoint — the
// payload is a tiny {email, repo}, so anything larger is junk or abuse.
const maxSubscribeBody = 4 << 10 // 4 KiB

// Subscriber is the coordinator capability the HTTP layer drives.
type Subscriber interface {
	Subscribe(ctx context.Context, email, repo string) error
}

// SubscriptionActions backs the confirm/unsubscribe pages (over NATS to the
// subscription service). Returns domain.ErrTokenNotFound for a bad/expired token.
type SubscriptionActions interface {
	Confirm(ctx context.Context, token string) error
	Unsubscribe(ctx context.Context, token string) error
}

type HTTPHandler struct {
	coord Subscriber
	subs  SubscriptionActions
}

func NewHTTPHandler(coord Subscriber, subs SubscriptionActions) *HTTPHandler {
	return &HTTPHandler{coord: coord, subs: subs}
}

type subscribeRequest struct {
	Email string `json:"email" binding:"required,email"`
	Repo  string `json:"repo"  binding:"required"`
}

func (h *HTTPHandler) Subscribe(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxSubscribeBody)
	var req subscribeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid input: email and repo are required"})
		return
	}
	if !repoFormatRegex.MatchString(req.Repo) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid repository format, expected owner/repo"})
		return
	}
	if err := h.coord.Subscribe(c.Request.Context(), req.Email, req.Repo); err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "subscription successful, confirmation email sent"})
}

func writeError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, domain.ErrRepoNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	case errors.Is(err, domain.ErrAlreadySubscribed):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case errors.Is(err, domain.ErrRateLimited):
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": err.Error()})
	default:
		// Never surface internal error details to the client.
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
	}
}

// ConfirmPage is the GET target of the confirmation email link: it confirms the
// token (over NATS) and renders a result page.
func (h *HTTPHandler) ConfirmPage(c *gin.Context) {
	switch err := h.subs.Confirm(c.Request.Context(), c.Param("token")); {
	case err == nil:
		renderResult(c, http.StatusOK, resultPage{
			Icon: "✓", Class: "ok", Title: "Subscription confirmed",
			Message: "You're all set — we'll email you when there's a new release.",
		})
	case errors.Is(err, domain.ErrTokenNotFound):
		renderResult(c, http.StatusNotFound, resultPage{
			Icon: "✕", Class: "err", Title: "Link invalid or expired",
			Message: "This confirmation link is no longer valid.",
		})
	default:
		renderResult(c, http.StatusInternalServerError, resultPage{
			Icon: "!", Class: "err", Title: "Something went wrong",
			Message: "Please try again in a moment.",
		})
	}
}

// UnsubscribePage is the GET target of the unsubscribe link.
func (h *HTTPHandler) UnsubscribePage(c *gin.Context) {
	switch err := h.subs.Unsubscribe(c.Request.Context(), c.Param("token")); {
	case err == nil:
		renderResult(c, http.StatusOK, resultPage{
			Icon: "✓", Class: "ok", Title: "Unsubscribed",
			Message: "You won't receive any more release emails for this repository.",
		})
	case errors.Is(err, domain.ErrTokenNotFound):
		renderResult(c, http.StatusNotFound, resultPage{
			Icon: "✕", Class: "err", Title: "Link invalid or expired",
			Message: "This unsubscribe link is no longer valid.",
		})
	default:
		renderResult(c, http.StatusInternalServerError, resultPage{
			Icon: "!", Class: "err", Title: "Something went wrong",
			Message: "Please try again in a moment.",
		})
	}
}

// UnsubscribeOneClick is the RFC 8058 List-Unsubscribe-Post target — no page,
// just a status. An already-gone token is treated as success (idempotent).
func (h *HTTPHandler) UnsubscribeOneClick(c *gin.Context) {
	err := h.subs.Unsubscribe(c.Request.Context(), c.Param("token"))
	if err != nil && !errors.Is(err, domain.ErrTokenNotFound) {
		c.Status(http.StatusInternalServerError)
		return
	}
	c.Status(http.StatusOK)
}

func NewRouter(h *HTTPHandler) *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())
	r.GET("/health", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"status": "ok"}) })
	r.GET("/", subscribePage)
	r.POST("/subscribe", h.Subscribe)
	r.GET("/confirm/:token", h.ConfirmPage)
	r.GET("/unsubscribe/:token", h.UnsubscribePage)
	r.POST("/unsubscribe/:token", h.UnsubscribeOneClick)
	return r
}
