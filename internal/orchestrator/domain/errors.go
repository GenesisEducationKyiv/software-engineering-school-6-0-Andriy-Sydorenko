package domain

import "errors"

// Saga-outcome errors the HTTP layer maps to status codes.
var (
	ErrRepoNotFound      = errors.New("repository not found on GitHub or is private")
	ErrRateLimited       = errors.New("GitHub API rate limit exceeded")
	ErrAlreadySubscribed = errors.New("email already subscribed to this repository")
	ErrTokenNotFound     = errors.New("confirmation or unsubscribe link is invalid or expired")
	ErrInternal          = errors.New("internal error")
)
