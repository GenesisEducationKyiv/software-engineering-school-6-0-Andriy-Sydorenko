package orchestrator

import (
	"context"
	"errors"
	"net/http"
	"regexp"

	"github.com/gin-gonic/gin"
)

var repoFormatRegex = regexp.MustCompile(`^[a-zA-Z0-9._-]+/[a-zA-Z0-9._-]+$`)

// Subscriber is the coordinator capability the HTTP layer drives.
type Subscriber interface {
	Subscribe(ctx context.Context, email, repo string) error
}

type HTTPHandler struct {
	coord Subscriber
}

func NewHTTPHandler(coord Subscriber) *HTTPHandler {
	return &HTTPHandler{coord: coord}
}

type subscribeRequest struct {
	Email string `json:"email" binding:"required,email"`
	Repo  string `json:"repo"  binding:"required"`
}

func (h *HTTPHandler) Subscribe(c *gin.Context) {
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
	case errors.Is(err, ErrRepoNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	case errors.Is(err, ErrAlreadySubscribed):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case errors.Is(err, ErrRateLimited):
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": err.Error()})
	default:
		// Never surface internal error details to the client.
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
	}
}

func NewRouter(h *HTTPHandler) *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())
	r.GET("/health", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"status": "ok"}) })
	r.POST("/subscribe", h.Subscribe)
	return r
}
