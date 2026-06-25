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

Docker must be running for integration and e2e. Everything else
(Postgres, NATS, Mailpit) runs inside testcontainers — nothing extra
to install on the host.

**First-time setup** (one command, optional but recommended):

```sh
make install-hooks    # installs a pre-commit hook that runs `make verify-mocks`
```

The hook blocks commits if mocks are out of date — same check CI runs.
Skip a single commit with `git commit --no-verify`.

The system is **four services from one Go module** over a NATS +
JetStream broker (orchestrator, subscription, catalog, notifier — see
[`../microservices.md`](../microservices.md)). The tiers test it at
three altitudes.

## Unit tests

Fast, hermetic, no containers, no broker. Covers branch-dense logic
across the four-service layout — `internal/app` (subscription),
`internal/catalog` (scanner + GitHub client + cache), `internal/orchestrator`
(saga coordinator), `internal/notifier`, plus the shared packages
(`internal/shared/...`). Collaborators are mocked (uber-go/mock) or a
small hand-rolled fake. Build tags `integration` and `e2e` keep the
other suites out of this run.

```sh
make test-unit
# go test ./... -race -count=1
```

Fast — no containers, runs in seconds.

## Integration tests

Real infrastructure, stubbed only at the outermost boundary (GitHub,
SMTP). testcontainers-go boots `postgres:16-alpine` and
`nats:2.10-alpine` on demand. Covers the **subscribe saga over real
NATS + three Postgres** (one per stateful service), the catalog
repository (register/release idempotency, scanner cursor upsert), the
notifier's JetStream send/dedup/DLQ, the request-reply round-trip, and
the subscription confirm/unsubscribe/list API.

```sh
make test-integration
# go test -tags=integration -timeout=2m -count=1 ./tests/integration/...
```

Container startup dominates the wall time; the tests themselves are quick.

## E2E tests

**In-process, API-driven, no browser.** The harness boots all four
services in one test process against ephemeral Postgres (one per
stateful service) + Mailpit + a NATS container, then drives the real
subscribe saga over HTTP + the broker: `POST /subscribe` → confirmation
email captured in Mailpit → confirm/unsubscribe replayed against the
subscription service. The GitHub upstream is an in-process
`httptest.Server` fixture; everything else runs as it does in
production.

```sh
make test-e2e
# go test -tags=e2e -timeout=5m -count=1 ./tests/e2e/...
```

Container startup dominates the wall time. See [`e2e.md`](e2e.md) for
what each test asserts and the wiring detail.

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
| `.github/workflows/e2e-tests.yml`         | `go test -tags=e2e -timeout=10m ./tests/e2e/...` (four services in-process; Postgres + NATS + Mailpit via testcontainers) |
| `.github/workflows/lint.yml`              | `golangci-lint` + `make verify-mocks` |
