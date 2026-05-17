//go:build generate

// Package gen centralizes codegen directives so production source files stay
// free of //go:generate metadata. Run `make generate` (or `make mocks`) to
// regenerate all mocks listed below.
//
// Adding a new mocked interface: add one //go:generate line here. Nothing else
// to touch — the Makefile rule will pick it up.
//
// This file is excluded from normal builds via the `generate` build tag; it is
// only compiled when running `go generate -tags=generate ./...`.
package gen

//go:generate go tool mockgen -source=internal/api/handler.go         -destination=internal/api/mocks/api_mocks.go         -package=mocks
//go:generate go tool mockgen -source=internal/github/cached_client.go -destination=internal/github/mocks/github_mocks.go  -package=mocks
//go:generate go tool mockgen -source=internal/scanner/scanner.go     -destination=internal/scanner/mocks/scanner_mocks.go -package=mocks
//go:generate go tool mockgen -source=internal/service/service.go     -destination=internal/service/mocks/service_mocks.go -package=mocks
