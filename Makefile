.PHONY: test test-unit test-integration test-e2e build-check generate-mocks verify-mocks install-hooks

# Compile every main package without producing artifact files.
# Vets default + integration + e2e tagged code.
build-check:
	go build -o /dev/null ./cmd/app ./cmd/notifier
	go vet ./...
	go vet -tags=integration ./tests/integration/...
	go vet -tags=e2e ./tests/e2e/...

# Regenerate mocks. Directives live in internal/codegen/gen.go (build-tag-gated
# so production source stays free of codegen metadata). Add a new //go:generate
# line there when introducing a new mocked interface — nothing else to touch.
generate-mocks:
	go generate -tags=generate ./...

# Fail if running `make generate-mocks` would change anything. Used by CI and the
# pre-commit hook to enforce that committed mocks match the directives (catches
# stale mocks after interface edits and missing mocks after a new directive).
verify-mocks: generate-mocks
	@git diff --exit-code -- internal/*/mocks/ || \
	  (echo "ERROR: mocks are out of date — run 'make generate-mocks' and commit"; exit 1)

# Install the repo's git hooks (currently: pre-commit runs verify-mocks).
# Re-run after pulling a hook update. Skip a single commit with --no-verify.
install-hooks:
	@mkdir -p .git/hooks
	@ln -sf ../../scripts/pre-commit .git/hooks/pre-commit
	@echo "installed: .git/hooks/pre-commit → scripts/pre-commit"

# Unit tests — no containers, fast.
test-unit:
	go test ./... -race -count=1

# Integration tests — testcontainers boots Postgres + NATS automatically.
test-integration:
	go test -tags=integration -timeout=2m -count=1 ./tests/integration/...

# E2E — testcontainers boots Postgres + Mailpit + NATS + chromedp/headless-shell;
# the app runs in-process. Requires only Docker on the host.
test-e2e:
	go test -tags=e2e -timeout=5m -count=1 ./tests/e2e/...

# All three suites, in order.
test: test-unit test-integration test-e2e
