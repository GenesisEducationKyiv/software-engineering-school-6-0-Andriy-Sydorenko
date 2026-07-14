//go:build generate

// Package codegen is build-only: it holds the mockgen directives that
// `make generate-mocks` runs. The `generate` build tag keeps it out of normal
// builds, so production packages carry no //go:generate metadata of their own.
// Paths are relative to this directory because `go generate` runs each
// directive from the file's own directory.
//
// Add a directive here when you introduce a new mocked interface.
package codegen

//go:generate go tool mockgen -source=../app/api/handler.go          -destination=../app/api/mocks/api_mocks.go         -package=mocks
//go:generate go tool mockgen -source=../app/github/cached_client.go -destination=../app/github/mocks/github_mocks.go   -package=mocks
//go:generate go tool mockgen -source=../app/scanner/scanner.go      -destination=../app/scanner/mocks/scanner_mocks.go -package=mocks
//go:generate go tool mockgen -source=../app/service/service.go        -destination=../app/service/mocks/service_mocks.go         -package=mocks
//go:generate go tool mockgen -source=../app/service/email_notifier.go -destination=../app/service/mocks/email_notifier_mocks.go -package=mocks
//go:generate go tool mockgen -source=../notifier/mailer.go            -destination=../notifier/mocks/notifier_mocks.go            -package=mocks
