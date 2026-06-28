package domain

import "errors"

// Catalog owns its GitHub-validation error sentinels (returned by the GitHub
// client), so the service doesn't depend on the subscription service's domain.
var (
	ErrRepoNotFound      = errors.New("repository not found on GitHub or is private")
	ErrRateLimited       = errors.New("GitHub API rate limit exceeded")
	ErrInvalidRepoFormat = errors.New("invalid repository format, expected owner/repo")
)
