//go:build generate

// Package codegen is build-only: it holds the mockgen directives that
// `make generate-mocks` runs. The `generate` build tag keeps it out of normal
// builds, so production packages carry no //go:generate metadata of their own.
// Paths are relative to this directory because `go generate` runs each
// directive from the file's own directory.
//
// Add a directive here when you introduce a new mocked interface.
package codegen

//go:generate go tool mockgen -source=../api/handler.go          -destination=../api/mocks/api_mocks.go         -package=mocks
//go:generate go tool mockgen -source=../github/cached_client.go -destination=../github/mocks/github_mocks.go   -package=mocks
//go:generate go tool mockgen -source=../scanner/scanner.go      -destination=../scanner/mocks/scanner_mocks.go -package=mocks
//go:generate go tool mockgen -source=../subscription/subscription.go -destination=../subscription/mocks/subscription_mocks.go -package=mocks
