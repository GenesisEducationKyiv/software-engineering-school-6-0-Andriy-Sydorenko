package domain

import "errors"

var (
	ErrInvalidRepoFormat = errors.New("invalid repository format, expected owner/repo")
	ErrRepoNotFound      = errors.New("repository not found on GitHub or is private")
	ErrAlreadySubscribed = errors.New("email already subscribed to this repository")
	ErrTokenNotFound     = errors.New("token not found")
	ErrInvalidEmail      = errors.New("invalid email format")
	ErrRateLimited       = errors.New("GitHub API rate limit exceeded")
)
