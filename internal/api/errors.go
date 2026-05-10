package api

import (
	"errors"
	"net/http"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/domain"
)

// statusMap maps domain errors to HTTP status codes, unmapped errors default to 500.
var statusMap = map[error]int{
	domain.ErrInvalidEmail:      http.StatusBadRequest,
	domain.ErrInvalidRepoFormat: http.StatusBadRequest,
	domain.ErrRepoNotFound:      http.StatusNotFound,
	domain.ErrTokenNotFound:     http.StatusNotFound,
	domain.ErrAlreadySubscribed: http.StatusConflict,
	domain.ErrRateLimited:       http.StatusServiceUnavailable,
}

// httpStatus walks the wrapped-error chain and returns the first
// matching status, defaults to 500.
func httpStatus(err error) int {
	for sentinel, code := range statusMap {
		if errors.Is(err, sentinel) {
			return code
		}
	}
	return http.StatusInternalServerError
}
