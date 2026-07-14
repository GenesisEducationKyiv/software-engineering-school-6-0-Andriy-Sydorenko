# Integration tests

HTTP → service → repository → **real Postgres**. GitHub and the mailer
are stubbed at the interface boundary so each test asserts what the
service **handed off**, not what the outside world received (that's the
e2e suite's job).

If you want the cross-layer philosophy, read
[ADR-008](../adr/008-testing-strategy.md). For commands and
prerequisites, [`README.md`](README.md).

## What this layer proves

Unit tests catch logic bugs in isolation. E2e proves cross-process
wiring. Integration sits between them and catches **wiring + side-effect
bugs that need a real database to surface**:

- The router, middleware, service, and repository are correctly
  composed (the right handler is mounted at the right path with the
  right middleware in front).
- Each endpoint's **DB side-effects** match what the response claims:
  row counts, `confirmed` flag flips, hard-delete on unsubscribe,
  token lifecycle (creation, single-use, cleanup).
- Sentinel errors propagate up the stack and map to the right HTTP
  status — proven against a real router, not a handler unit test.
- Mailer call arguments are correct (the service handed off the right
  email, repo, confirm + unsubscribe tokens). The mailer stub records
  every call so the test asserts on it.

The deliberate trade-off: **GitHub and SMTP are stubbed**, so this
layer says nothing about real upstream behaviour. Both are exercised
end-to-end in the e2e suite via real `github.Client` against an
`httptest.Server` fixture and real SMTP through Mailpit.

## What's wired vs stubbed

| Layer | Real | Stubbed |
|---|---|---|
| Router (`gin`) | ✓ | |
| Auth middleware (`X-API-Key`) | ✓ | |
| Service + repository | ✓ | |
| Postgres (testcontainers, migrated) | ✓ | |
| GitHub validator | | `stubGitHub` — `wantErr` per test |
| Mailer | | `stubMailer` — records every send for assertion |

The stubs are tiny in-test types in `harness_test.go`. They implement
the same interfaces production uses (`service.RepoValidator`,
`notifier.ConfirmationSender`). Per-test wiring is via `newTestEnv`
which builds the whole graph fresh.

## Stack

- `testcontainers-go` boots `postgres:16-alpine` (matches prod and the
  e2e suite) **once per package run** (shared via `TestMain` for cheap
  startup).
- `testify/suite` would be overkill for two files; tests are plain
  `func Test*(t *testing.T)` with table-driven cases.
- `testify/require` for hard fail in setup; raw `t.Errorf` for soft
  assertion comparisons.

## Running

```
make test-integration
# go test -tags=integration -timeout=2m -count=1 ./tests/integration/...
```

Requires Docker. Postgres is started once per package via
testcontainers-go and reused; rows are wiped between tests with
`TRUNCATE ... RESTART IDENTITY CASCADE`. Wall time is dominated by
container startup, not the tests themselves (rough local figure, not
benchmarked).

Gated behind `//go:build integration` so the default `go test ./...`
unit run stays container-free.

## Files

- `harness_test.go` — shared Postgres bootstrap, stubs, per-test
  `newTestEnv`.
- `api_test.go` — every endpoint test + small helpers (`subscribeOK`,
  `readTokenValue`, `readUnsubscribeToken`).

## For reviewers

Reviewer questions when reading an integration test change:

1. **Does the test assert on a DB side-effect, not just the
   response?** A test that only checks the HTTP status is half a
   test — the response might lie. Look for `db.First(&sub, ...)` /
   row-count queries.
2. **Are tokens read from the mailer stub, not pre-computed?**
   `readTokenValue(stub)` proves the service actually handed them
   off; pre-computing the token in the test misses the seam.
3. **Is the stub's `wantErr` representative?** A `wantErr` of
   `nil` is fine for happy paths but doesn't cover the interesting
   branches — look for the error-path test alongside.
4. **Should this be e2e or unit instead?** If the test cares about
   real SMTP behaviour or real GitHub semantics, it belongs in e2e.
   If it's a single-method assertion with no DB read, it belongs in
   unit.

## What this layer deliberately doesn't cover

- **Real GitHub behaviour** (auth headers, retry, real status
  parsing) — the stub at the interface boundary skips all of it.
  E2e exercises the real `github.Client` against an `httptest.Server`
  fixture.
- **Real SMTP / mail receipt** — the stub records arguments but
  never sends. E2e uses real `SMTPMailer` → Mailpit.
- **Browser-side validation / UI behaviour** — no browser here.
- **Branch logic of pure functions** — that's the unit suite's job;
  re-asserting through HTTP is slow and noisy.

## When to add an integration test

Add one when:

- A new endpoint lands → at minimum, happy path + one error path +
  the DB side-effect.
- A new sentinel error joins the error-mapping table → add a case
  proving the status-code mapping holds end-to-end through the
  router.
- A schema change touches a column the API reads or writes → assert
  the response shape and the DB state.

Skip when:

- The change is in a pure function with no DB interaction → unit.
- The change is in real upstream behaviour (SMTP, GitHub HTTP
  semantics) → e2e.
