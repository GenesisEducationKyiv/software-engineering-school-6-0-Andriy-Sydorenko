# E2E tests

API-driven, **in-process**, across all four services. The harness boots
the **orchestrator**, **subscription**, **catalog**, and **notifier**
in one test process against **ephemeral Postgres (one per stateful
service)** + **Mailpit** (real SMTP capture) + a **NATS + JetStream**
container, then drives the real HTTP + broker flow. No browser.

This is the **only layer that exercises the whole subscribe saga across
the broker and the mail handoff** — orchestrator request-reply →
catalog/subscription participants → confirmation event → notifier →
SMTP → Mailpit. If a regression breaks that loop, no other suite
catches it.

If you want the cross-layer philosophy, read
[ADR-008](../adr/008-testing-strategy.md). The topology these tests
exercise is in [`../microservices.md`](../microservices.md). For
commands and prerequisites, [`README.md`](README.md).

## What this layer proves

Unit tests catch logic in isolation. Integration catches wiring +
DB side-effects with the external boundaries (GitHub, SMTP) stubbed.
**E2e catches what both miss**:

- **The saga across the real broker.** The orchestrator coordinates
  catalog + subscription over Core NATS request-reply and publishes
  the confirmation event over JetStream. A bug in stream setup,
  subject wiring, or consumer config passes every unit and integration
  test of the individual handlers and only surfaces when all four
  services run against one live broker.
- **The confirmation event → email → token round-trip.** The
  orchestrator commits, publishes `events.confirmation.requested`, the
  subscription service renders the email and publishes
  `notify.confirmation`, the notifier delivers it to Mailpit. The
  confirm/unsubscribe tokens are then pulled **out of the captured
  email body** and replayed against the orchestrator's `/confirm` /
  `/unsubscribe` pages. Unit can't test the loop (it's not
  cross-service); integration stubs the mailer.
- **Real SMTP + real MIME.** The notifier sends through a real
  `SMTPMailer` to Mailpit, which parses the MIME back on receipt. A
  malformed body is rejected by Mailpit — proving the wire format
  works.
- **The real GitHub HTTP client.** Catalog's `register` participant
  validates the repo against the real `github.Client`, pointed at an
  in-process `httptest.Server` fixture, so real headers + status
  parsing run on every subscribe.
- **Saga abort with no side-effects.** A bad repo must abort the saga
  before the pivot — no subscription row, no email.

The deliberate trade-off: **e2e is narrow on purpose**. Per-endpoint
error mapping, pagination, token edge cases, and the individual saga
state transitions have integration / unit coverage; re-asserting them
here would only slow CI.

## What's tested and why

One suite, two tests. Each exists because no cheaper layer catches the
bug class.

### `SubscribeSuite` (`tests/e2e/subscribe_test.go`)

Drives the orchestrator's public `POST /subscribe` and verifies state
**through behavior** — the captured email and the token replays
(confirm is idempotent, the unsubscribe token is one-shot) — not DB
reads (those are the integration tier's job).

- **`TestLifecycle`** — the full happy path across all four services.
  `POST /subscribe` (200) → poll Mailpit for the real confirmation
  email → extract the confirm + unsubscribe tokens from the body →
  `GET /confirm/:t` (200) → `GET /confirm/:t` **again** (200, proving
  confirm is idempotent / prefetch-safe — a scanner's first GET must
  not 404 the user's real click) → `GET /unsubscribe/:t` (200) →
  replay the unsubscribe token (must **not** be 200, proving it's
  one-shot). All paths are bare `/confirm` / `/unsubscribe` on the
  **orchestrator** (no `/api` prefix). This is the case the harness
  exists to make possible — no cheaper layer can run it.
- **`TestBadRepo_AbortsWithNoEmail`** — stages the GitHub fixture to
  return 404 for the owner (`GitHub.SetBehavior("ghost", GHNotFound)`),
  then subscribes. Asserts `POST /subscribe` returns 404 **and** the
  subscription DB has zero rows — the saga aborted before the pivot,
  so nothing was created and no email was sent.

## What's wired vs faked

| Layer | Real | Fixture |
|---|---|---|
| Orchestrator (`POST /subscribe`, `/confirm` + `/unsubscribe` pages, coordinator, `saga_log`) | ✓ | |
| Subscription service (saga create, list API, email composer) | ✓ | |
| Catalog (register/release participants) | ✓ | |
| Notifier (JetStream consumer → real SMTP send) | ✓ | |
| NATS + JetStream (testcontainers, streams provisioned) | ✓ | |
| Postgres — one per stateful service (testcontainers, migrated) | ✓ | |
| Mailpit (real SMTP receive + HTTP API) | ✓ | |
| GitHub client (real HTTP, headers, parsing) | ✓ | `httptest.Server` serving `/repos/:owner/:repo[/releases/latest]` |

The GitHub upstream is the **only** faked dependency — pointed at via
the `github.WithBaseURL` seam, a real config knob (staging uses it),
so no `if testing` branch lives in production code.

## The harness

`tests/e2e/harness/` is a regular Go package, gated behind
`//go:build e2e`. `New(t)` boots the containers and wires the four
in-process services; cleanup is registered with `t.Cleanup`.

| Field / method | Purpose |
|---|---|
| `New(t, opts...)` | Boots NATS + Mailpit + three Postgres + the GH fixture, wires all four services in-process, returns `*Harness`. |
| `OrchestratorURL` | Orchestrator HTTP root (`POST /subscribe`, `/confirm` + `/unsubscribe` pages) |
| `AppURL` | Subscription service HTTP root (list) |
| `MailpitURL` | Mailpit HTTP API root |
| `SubDB` / `CatDB` / `OrchDB` | `*gorm.DB` per service for direct row inspection |
| `GitHub` | `*GitHubFixture` — `SetBehavior(owner, b)`, `Reset()` |
| `TruncateDB(t)` | Wipes mutable rows across all three service DBs |
| `ResetMailpit(t)` | Deletes all captured messages |
| `WaitForMail(t, addr, timeout)` | Polls Mailpit, extracts confirm + unsub tokens by regex on the email body |
| `DumpContainerLogs(t)` | On failure, dumps each container's log into `_artifacts/` for CI inspection |
| `BaseSuite` | `testify/suite` embed; owns one `Harness` per suite, resets DB + Mailpit + GH fixture between tests |

`Options` is currently empty — reserved for future per-suite tuning.

## Layout

```
tests/e2e/
  subscribe_test.go        SubscribeSuite: TestLifecycle, TestBadRepo_AbortsWithNoEmail
  harness/                 testcontainers + in-process four-service wiring
    harness.go             New(t): boots containers, wires all four services, shutdown
    suite.go               BaseSuite (testify) — owns one Harness, resets between tests
    github_fixture.go      httptest.Server with per-owner behavior map (GHOK/NotFound/RateLimited/ServerError)
    mailpit.go             WaitForMail — polls Mailpit + extracts tokens from the body
    artifacts.go           DumpContainerLogs — failure diagnostics into _artifacts/
```

## Stack

- **No browser.** The flow is driven entirely over HTTP (`net/http`)
  and the broker — there is no Playwright, CDP, or Chromium anywhere.
- **Container lifecycle**: `testcontainers-go` — three `postgres:16-alpine`
  (one per stateful service), Mailpit (`axllent/mailpit`), and NATS
  (`nats:2.10-alpine`, `-js` for JetStream) via generic containers.
- **In-process services**: orchestrator + subscription expose HTTP on
  ephemeral `127.0.0.1` ports; catalog + notifier run as broker
  consumers. The orchestrator URL is resolved *before* the email
  composer is wired, so the confirm/unsubscribe links it embeds point
  at the live orchestrator listener.
- **Suite framework**: `testify/suite` — `harness.BaseSuite` owns one
  Harness per suite.
- **Assertions**: `testify/require` — hard-fail; polling with
  `WaitForMail` for the async mail handoff.

## Running

```
make test-e2e
# go test -tags=e2e -timeout=5m -count=1 ./tests/e2e/...
```

Requires only Docker. testcontainers boots the three Postgres
instances, Mailpit, and NATS at runtime. Wall time is dominated by
container startup; the tests themselves are quick.

Gated behind `//go:build e2e` so the default `go test ./...` unit run
stays container-free.

## Conventions

- **State is verified through behavior, not DB reads.** Tokens come
  from the real captured email (`WaitForMail`); replaying the token
  proves the contract — confirm is **idempotent** (the second GET
  still 200s, prefetch-safe), the unsubscribe token is **one-shot**
  (the replay is not 200) — not by querying the row. The bad-repo test
  is the one exception — it asserts zero rows to prove the saga left no
  trace.
- **`SetBehavior` is per-test.** `BaseSuite.SetupTest` calls
  `GitHub.Reset()` so behavior overrides don't leak between tests.
- **Hand-rolled fixtures only at the upstream boundary.** GitHub is
  the only one; every other dependency is real.

## For reviewers

When reading an e2e-test change, the questions to ask:

1. **Does this test prove something cross-service?** If the behaviour
   fits in one service (handler → service → repo, or a single saga
   participant), integration or unit is cheaper and gives a better
   stack trace on failure.
2. **Are tokens extracted from real mail?** A test that pre-computes
   them or reads them from the DB is testing wiring at the wrong layer.
3. **Does the assertion ride on observable behaviour?** The 200/404 on
   the public endpoint and the captured email are the contract; a
   detailed assertion on an intermediate saga state belongs in the
   saga integration tests.

## What this layer deliberately doesn't cover

- **Per-error-code mapping** on the subscription API — integration.
- **Pagination / filtering** on `GET /api/subscriptions` — integration.
- **Confirm/unsubscribe token edge cases** (garbage, unknown) — unit
  (subscription service token rejection + the orchestrator's 404
  page mapping).
- **Individual saga state transitions + compensation + recovery** —
  the saga integration tests (real NATS + three Postgres) and the
  coordinator unit tests.
- **Catalog repository SQL, migrations, GORM wiring** — integration.
- **Branch logic of the scanner / GitHub client / composer / notifier
  classifier** — unit.

## When to add an e2e test

Add one when behaviour can **only** be proven across services and the
broker:

- A user-visible flow that spans orchestrator → broker → participants
  → mail and back (`TestLifecycle` is the archetype).
- Wiring of an external seam (stream/consumer setup, base-URL config)
  where a broken wire passes every unit + integration test but fails
  the user.

Skip:

- Anything already covered by the saga integration tests with the
  right side-effect asserts.
- Error-code mapping — service / handler unit tests are cheaper.
- Background scanner runs and other behaviour the user never sees.

## CI

`.github/workflows/e2e-tests.yml`: `setup-go` → `go mod download` →
`go test -tags=e2e -timeout=10m -count=1 ./tests/e2e/...`. testcontainers
pulls Postgres, Mailpit, and NATS at runtime on the `ubuntu-latest`
Docker daemon. On failure the harness dumps container logs into
`tests/e2e/_artifacts/` for upload.
