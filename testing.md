# Testing

Three test suites, one command each. Containers spin up on demand —
you only need `git`, `docker`, and Go 1.26 installed on the host.

For per-suite detail (mock layout, coverage, conventions, what each
layer deliberately doesn't cover), see:

- [`UNIT_TESTS.md`](UNIT_TESTS.md)
- [`INTEGRATION_TESTS.md`](INTEGRATION_TESTS.md)
- [`E2E_TESTS.md`](E2E_TESTS.md)

## Prerequisites

| Suite | Host tooling |
|---|---|
| Unit | Go 1.26 |
| Integration | Go 1.26 + Docker |
| E2E | Go 1.26 + Docker + Playwright Chromium driver (one-time install — see below) |

Docker must be running for integration and e2e.

## Unit tests

Fast, hermetic, no containers. Covers branch logic across `service`,
`scanner`, `notifier`, `config`, `domain`, `github`, and the `api`
package's handler/middleware. Build tags `integration` and `e2e` keep
the other suites out of this run.

```sh
make test-unit
# go test ./... -race -count=1
```

Wall time ≈ 3–4 s.

## Integration tests

HTTP → service → repository → real Postgres. testcontainers-go boots
`postgres:17-alpine` per package run; migrations apply automatically,
rows truncate between tests. GitHub and the mailer are stubbed at the
service boundary.

```sh
make test-integration
# go test -tags=integration -timeout=2m -count=1 ./internal/integration/...
```

Wall time ≈ 5 s (containers dominate).

## E2E tests

Browser → router → service → real Postgres + real Mailpit (SMTP +
inbox). The GitHub upstream is an in-process `httptest.Server` fixture;
everything else runs as it does in production. testcontainers-go boots
Postgres + Mailpit per harness; the app runs in-process on a random port;
Chromium is driven by `playwright-go`.

**One-time setup** (downloads the Chromium bundle to
`~/Library/Caches/ms-playwright`):

```sh
go run github.com/playwright-community/playwright-go/cmd/playwright install --with-deps chromium
```

Then:

```sh
make test-e2e
# go test -tags=e2e -timeout=5m -count=1 ./e2e/...
```

Wall time ≈ 10–11 s (two harness instances → two container sets).

## All three at once

```sh
make test    # unit, then integration, then e2e
```

## CI

Each suite has its own GitHub Actions workflow so failures don't
cross pipelines:

| Workflow | Purpose |
|---|---|
| `.github/workflows/unit-tests.yml` | `make test-unit` |
| `.github/workflows/integration-tests.yml` | `make test-integration` |
| `.github/workflows/e2e-tests.yml` | `make test-e2e` (caches Playwright Chromium on `go.sum` hash) |
| `.github/workflows/ci.yml` | `golangci-lint` |
