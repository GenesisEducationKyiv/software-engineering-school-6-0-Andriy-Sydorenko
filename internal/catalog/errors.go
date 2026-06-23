package catalog

import "errors"

// Catalog owns its GitHub-validation error sentinels (the GitHub client moves
// here in Phase 2 and returns these), so the service doesn't depend on the
// subscription service's domain package.
var (
	ErrRepoNotFound      = errors.New("repository not found on GitHub or is private")
	ErrRateLimited       = errors.New("GitHub API rate limit exceeded")
	ErrInvalidRepoFormat = errors.New("invalid repository format, expected owner/repo")
)
