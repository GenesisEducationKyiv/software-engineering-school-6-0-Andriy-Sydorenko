.PHONY: test test-unit test-integration test-e2e build-check generate mocks

# Compile every main package without producing artifact files.
# Vets default + integration + e2e tagged code.
build-check:
	go build -o /dev/null ./cmd/server
	go vet ./...
	go vet -tags=integration ./internal/integration/...
	go vet -tags=e2e ./e2e/...

# Regenerate mocks. Run after changing any interface with a //go:generate
# directive. Idempotent — overwrites internal/<pkg>/mocks/.
generate:
	go generate ./internal/...

mocks: generate

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
