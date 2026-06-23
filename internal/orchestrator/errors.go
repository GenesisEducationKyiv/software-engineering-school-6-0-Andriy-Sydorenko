package orchestrator

import "errors"

// Saga-outcome errors the HTTP layer maps to status codes.
var (
	ErrRepoNotFound      = errors.New("repository not found on GitHub or is private")
	ErrRateLimited       = errors.New("GitHub API rate limit exceeded")
	ErrAlreadySubscribed = errors.New("email already subscribed to this repository")
	ErrInternal          = errors.New("internal error")
)
