.PHONY: test test-unit test-integration test-e2e build-check generate mocks verify-generate install-hooks

# Compile every main package without producing artifact files.
# Vets default + integration + e2e tagged code.
build-check:
	go build -o /dev/null ./cmd/server
	go vet ./...
	go vet -tags=integration ./internal/integration/...
	go vet -tags=e2e ./e2e/...

# Regenerate mocks. Directives live in gen.go (build-tag-gated so production
# source stays free of codegen metadata). Add a new //go:generate line in
# gen.go when introducing a new mocked interface — nothing else to touch.
generate:
	go generate -tags=generate ./...

mocks: generate

# Fail if running `make generate` would change anything. Used by CI and the
# pre-commit hook to enforce that committed mocks match gen.go (catches
# stale mocks after interface edits and missing mocks after gen.go edits).
verify-generate: generate
	@git diff --exit-code -- internal/*/mocks/ || \
	  (echo "ERROR: mocks are out of date — run 'make generate' and commit"; exit 1)

# Install the repo's git hooks (currently: pre-commit runs verify-generate).
# Re-run after pulling a hook update. Skip a single commit with --no-verify.
install-hooks:
	@mkdir -p .git/hooks
	@ln -sf ../../scripts/pre-commit .git/hooks/pre-commit
	@echo "installed: .git/hooks/pre-commit → scripts/pre-commit"

# Unit tests — no containers, fast.
test-unit:
	go test ./... -race -count=1

# Integration tests — testcontainers boots Postgres automatically.
test-integration:
	go test -tags=integration -timeout=2m -count=1 ./internal/integration/...

# E2E — testcontainers boots Postgres + Mailpit, app runs in-process.
# Requires Docker + Playwright Chromium driver
# (run once: `go run github.com/playwright-community/playwright-go/cmd/playwright install --with-deps chromium`).
test-e2e:
	go test -tags=e2e -timeout=5m -count=1 ./e2e/...

# All three suites, in order.
test: test-unit test-integration test-e2e
