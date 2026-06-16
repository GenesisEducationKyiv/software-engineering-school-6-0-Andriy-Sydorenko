.PHONY: test test-unit test-integration test-e2e build-check generate-mocks generate-proto verify-mocks install-hooks bench bench-throughput

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

# Regenerate gRPC stubs from the .proto. Plugins are built from the versions
# pinned by the go.mod tool directives (no global install), so generated code
# can't drift. Output lands next to the .proto via paths=source_relative.
generate-proto:
	go build -o bin/ google.golang.org/protobuf/cmd/protoc-gen-go google.golang.org/grpc/cmd/protoc-gen-go-grpc
	PATH="$(CURDIR)/bin:$$PATH" protoc -I . \
			--go_out=.      --go_opt=module=github.com/Andriy-Sydorenko/repo-release-notifier \
			--go-grpc_out=. --go-grpc_opt=module=github.com/Andriy-Sydorenko/repo-release-notifier \
			proto/notifier.proto

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

# go test prints no column header and labels units inline; this adds a header row
# and aligns the columns (name, iterations, ns/op, B/op, allocs/op) above the values.
BENCHFMT = awk 'BEGIN{f="%-44s %10s %14s %14s %13s\n"} /^Benchmark/&&!h{printf f,"benchmark","iters","ns/op","B/op","allocs/op";h=1} /^Benchmark/{printf f,$$1,$$2,$$3,$$5,$$7;next} {print}'

# LATENCY benchmark: app→notifier SendEmail one call at a time, swept over
# html_body payload size (1KB/10KB/100KB). Lower ns/op is faster. For numbers
# stable enough to quote, average several runs (needs benchstat):
#   go test -bench='_Send$$' -benchmem -run='^$$' -benchtime=2s -count=5 ./bench/... | benchstat -
bench:
	@go test -bench='_Send$$' -benchmem -run='^$$' -benchtime=2s ./bench/... | $(BENCHFMT)

# THROUGHPUT benchmark: the same call driven concurrently via b.RunParallel over
# the SAME persistent client (one gRPC channel vs one pooled HTTP transport) —
# the request-efficiency / multiplexing test. -cpu sweeps the concurrency level
# (1 / 8 / 64 goroutines in flight). Throughput req/s = 1e9 / ns_op.
bench-throughput:
	@go test -bench='_Parallel$$' -benchmem -run='^$$' -cpu=1,8,64 ./bench/... | $(BENCHFMT)
