.PHONY: unit integration e2e e2e-up e2e-down test build-check

# Compile every main package without producing any artifact files.
# Use this instead of `go build ./cmd/<name>`, which drops a stray
# binary in the repo root.
build-check:
	go build -o /dev/null ./cmd/server
	go build -o /dev/null ./cmd/e2e-server
	go vet ./...

# Unit tests — no containers, fast.
unit:
	go test ./... -race -count=1

# Integration tests — testcontainers boots Postgres automatically.
integration:
	go test -tags=integration -timeout=5m -count=1 ./internal/integration/...

# E2E — bring up the Docker stack, install Playwright, run, tear down.
# Tear-down runs even on failure so the port is freed.
e2e:
	@root="$$PWD"; \
	docker compose -f "$$root/docker-compose.e2e.yml" up --build -d; \
	trap 'docker compose -f "'$$root'/docker-compose.e2e.yml" down -v' EXIT; \
	cd e2e && \
	  (test -f package-lock.json && npm ci || npm install --no-audit --no-fund) && \
	  npx playwright install --with-deps chromium && \
	  npx playwright test

e2e-up:
	docker compose -f docker-compose.e2e.yml up --build -d

e2e-down:
	docker compose -f docker-compose.e2e.yml down -v

# All three suites, in order.
test: unit integration e2e
