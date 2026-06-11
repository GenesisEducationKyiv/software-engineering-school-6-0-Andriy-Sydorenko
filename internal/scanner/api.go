package scanner

import "context"

// API is the scanner module's public interface. The subscription module calls
// ValidateRepo through this — never the scanner's internals or the GitHub client
// directly.
//
// ValidateRepo reports whether owner/repo exists and is accessible: a definitive
// "does not exist" is (false, nil); a transport/rate-limit failure propagates as
// a non-nil error, so the caller maps to the right user-facing error.
type API interface {
	ValidateRepo(ctx context.Context, owner, repo string) (bool, error)
}
