.PHONY: test test-unit test-integration test-e2e build-check generate-mocks verify-mocks install-hooks proto

# Compile every main package without producing artifact files.
# Vets default + integration + e2e tagged code.
build-check:
	go build -o /dev/null ./cmd/server
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

# Integration tests — testcontainers boots Postgres automatically.
test-integration:
	go test -tags=integration -timeout=2m -count=1 ./tests/integration/...

# E2E — testcontainers boots Postgres + Mailpit + chromedp/headless-shell;
# the app runs in-process. Requires only Docker on the host.
test-e2e:
	go test -tags=e2e -timeout=5m -count=1 ./tests/e2e/...

# All three suites, in order.
test: test-unit test-integration test-e2e

# Generate Go stubs from proto/*.proto. Builds the pinned codegen plugins
# into ./bin (so we don't rely on $PATH or a system install), then runs the
# already-installed protoc against them. Output lands under proto/gen/ via the
# go_package option + --*_opt=module.
PROTO_BIN := $(CURDIR)/bin
proto:
	@mkdir -p $(PROTO_BIN)
	go build -o $(PROTO_BIN)/protoc-gen-go google.golang.org/protobuf/cmd/protoc-gen-go
	go build -o $(PROTO_BIN)/protoc-gen-go-grpc google.golang.org/grpc/cmd/protoc-gen-go-grpc
	protoc \
	  --plugin=protoc-gen-go=$(PROTO_BIN)/protoc-gen-go \
	  --plugin=protoc-gen-go-grpc=$(PROTO_BIN)/protoc-gen-go-grpc \
	  --go_out=. --go_opt=module=github.com/Andriy-Sydorenko/repo-release-notifier \
	  --go-grpc_out=. --go-grpc_opt=module=github.com/Andriy-Sydorenko/repo-release-notifier \
	  proto/notifier.proto
