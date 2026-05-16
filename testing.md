# Testing

Three test suites, one command each. All infra they need spins up from
Docker — you only need `git`, `docker`, and either Go or Node.js
(depending on the suite) installed on the host.

## Prerequisites

| Suite       | Host tooling required |
|-------------|-----------------------|
| Unit        | Go 1.26               |
| Integration | Go 1.26 + Docker      |
| E2E         | Docker + Node.js 20   |

Docker must be running for the integration and E2E suites.

## Unit tests

Fast, hermetic, no containers. Covers `service`, `scanner`, `notifier`,
`config`, `domain`, `github`, and the `api` package's handler/middleware
in isolation.

```sh
make unit
# or, equivalently:
go test ./... -race -count=1
```

Coverage report:

```sh
go test ./... -cover
```

## Integration tests

End-to-end through the real HTTP router → service → repository →
Postgres path. Uses [testcontainers-go](https://golang.testcontainers.org/)
to boot a real Postgres 16 container per package run; migrations run
automatically, tables are truncated between tests. GitHub and SMTP are
stubbed at the service boundary.

Gated behind the `integration` build tag so the unit pipeline stays
fast.

```sh
make integration
# or, equivalently:
go test -tags=integration -timeout=5m -count=1 ./internal/integration/...
```

You don't need to start anything by hand — the test process pulls the
`postgres:16-alpine` image and tears the container down on exit.

## E2E tests (Playwright)

Real Chromium → real backend (in Docker) → real Postgres. The backend
is the dedicated `cmd/e2e-server` binary built by `Dockerfile.e2e` —
same router/service/repository as production, but GitHub validation is
stubbed (owner `ghost` returns 404; everything else passes) and SMTP is
replaced with a stdout logger.

One command (from the repo root):

```sh
make e2e
```

If you don't have `make`, the equivalent four steps are:

```sh
docker compose -f docker-compose.e2e.yml up --build -d
(cd e2e && npm ci && npx playwright install --with-deps chromium && npx playwright test)
docker compose -f docker-compose.e2e.yml down -v
```

The first run downloads the Chromium browser bundle (~90 MB) into the
Playwright cache; subsequent runs reuse it.

To watch the suite in a real browser:

```sh
(cd e2e && npx playwright test --headed)
```

## CI

Each suite has its own GitHub Actions workflow so failures don't cross
pipelines:

| Workflow                                       | Triggers       |
|------------------------------------------------|----------------|
| `.github/workflows/unit-tests.yml`             | push, PR       |
| `.github/workflows/integration-tests.yml`      | push, PR       |
| `.github/workflows/e2e-tests.yml`              | push, PR       |
| `.github/workflows/ci.yml` (lint, golangci-lint) | push, PR     |
