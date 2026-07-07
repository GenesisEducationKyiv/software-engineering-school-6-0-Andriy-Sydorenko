package api

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/app/domain"
)

type httpView struct {
	status  int
	message string
}

// errorView is the single source of truth for translating a domain
// error into a public HTTP response. Unmapped errors are treated as
// internal (500) and their text is never returned to the client.
var errorView = map[error]httpView{
	domain.ErrInvalidEmail: {http.StatusBadRequest, "invalid email format"},
	domain.ErrInvalidRepoFormat: {
		http.StatusBadRequest,
		"invalid repository format, expected owner/repo",
	},
	domain.ErrRepoNotFound: {
		http.StatusNotFound,
		"repository not found on GitHub or is private",
	},
	domain.ErrTokenNotFound: {http.StatusNotFound, "token not found"},
	domain.ErrAlreadySubscribed: {
		http.StatusConflict,
		"email already subscribed to this repository",
	},
	domain.ErrRateLimited: {
		http.StatusServiceUnavailable,
		"service temporarily unavailable, try again later",
	},
}

func writeError(c *gin.Context, op string, err error) {
	for sentinel, v := range errorView {
		if errors.Is(err, sentinel) {
			c.JSON(v.status, domain.ErrorResponse{Error: v.message})
			return
		}
	}
	slog.ErrorContext(c.Request.Context(), "unmapped error", "op", op, "err", err)
	c.JSON(http.StatusInternalServerError, domain.ErrorResponse{Error: "internal app error"})
}
