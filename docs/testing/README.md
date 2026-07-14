# Testing

Three test suites, one command each. Containers spin up on demand —
you only need `git`, `docker`, and Go 1.26 installed on the host.

For per-suite detail (mock layout, coverage, conventions, what each
layer deliberately doesn't cover), see:

- [`unit.md`](unit.md)
- [`integration.md`](integration.md)
- [`e2e.md`](e2e.md)

## Prerequisites

| Suite | Host tooling |
|---|---|
| Unit | Go 1.26 |
| Integration | Go 1.26 + Docker |
| E2E | Go 1.26 + Docker |

Docker must be running for integration and e2e. Chromium runs inside
a sidecar container — nothing extra to install on the host.

**First-time setup** (one command, optional but recommended):

```sh
make install-hooks    # installs a pre-commit hook that runs `make verify-mocks`
```

The hook blocks commits if mocks are out of date — same check CI runs.
Skip a single commit with `git commit --no-verify`.

## Unit tests

Fast, hermetic, no containers. Covers branch logic across `service`,
`scanner`, `notifier`, `config`, `domain`, `github`, and the `api`
package's handler/middleware. Build tags `integration` and `e2e` keep
the other suites out of this run.

```sh
make test-unit
# go test ./... -race -count=1
```

Fast — no containers, runs in seconds.

## Integration tests

HTTP → service → repository → real Postgres. testcontainers-go boots
`postgres:16-alpine` per package run; migrations apply automatically,
rows truncate between tests. GitHub and the mailer are stubbed at the
service boundary.

```sh
make test-integration
# go test -tags=integration -timeout=2m -count=1 ./tests/integration/...
```

Container startup dominates the wall time; the tests themselves are quick.

## E2E tests

Browser → router → service → real Postgres + real Mailpit (SMTP +
inbox). The GitHub upstream is an in-process `httptest.Server` fixture;
everything else runs as it does in production. testcontainers-go boots
Postgres, Mailpit, and a `chromedp/headless-shell` Chromium sidecar
per harness; the app runs in-process on a random port and is reached
by the browser via `host.testcontainers.internal`.

```sh
make test-e2e
# go test -tags=e2e -timeout=5m -count=1 ./tests/e2e/...
```

Container startup dominates the wall time. First run also pulls the
`chromedp/headless-shell:stable` image (~360 MB) and fetches the
`playwright-go` Node driver (~50 MB) into `~/.cache/ms-playwright-go`;
both are cached after.
See [`e2e.md`](e2e.md) for the trade-off discussion.

## All three at once

```sh
make test    # unit, then integration, then e2e
```

## CI

Each suite has its own GitHub Actions workflow so failures don't
cross pipelines:

Each runs the `go test` invocation inline (not the make target — the targets
above are the local equivalents):

| Workflow                                  | Runs |
|-------------------------------------------|---|
| `.github/workflows/unit-tests.yml`        | `go test ./... -race -count=1` |
| `.github/workflows/integration-tests.yml` | `go test -tags=integration -timeout=2m ./tests/integration/...` |
| `.github/workflows/e2e-tests.yml`         | `go test -tags=e2e -timeout=10m ./tests/e2e/...` (Chromium via testcontainers sidecar) |
| `.github/workflows/lint.yml`              | `golangci-lint` + `make verify-mocks` |
