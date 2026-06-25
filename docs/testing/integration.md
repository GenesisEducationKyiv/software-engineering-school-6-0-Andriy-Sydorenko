# Integration tests

Real infrastructure, **stubbed only at the outermost boundary**
(GitHub, SMTP). Every test in this tier boots the dependency it needs
via testcontainers — Postgres, NATS + JetStream, or both — and asserts
the **side-effects that need real infrastructure to surface**: row
state, saga-log transitions, idempotent upserts, JetStream
dedup/DLQ behaviour, and request-reply round-trips.

This tier sits between unit (logic in isolation) and e2e (the whole
four-service flow). It catches **wiring + side-effect bugs against real
Postgres / NATS** without paying for the full in-process topology.

If you want the cross-layer philosophy, read
[ADR-008](../adr/008-testing-strategy.md). The topology these tests
exercise is in [`../microservices.md`](../microservices.md). For
commands and prerequisites, [`README.md`](README.md).

## What's tested and why

Five groups, by the infrastructure they need and the bug class they
catch.

### Saga — real NATS + three Postgres (`saga_test.go`)

The defining integration test of the system: the orchestrator's
coordinator wired to the **real** catalog + subscription participants
over a live NATS broker, each backed by its **own** migrated Postgres.
GitHub is the only stub (`stubValidator`, flipped per test to drive
the bad-repo / rate-limit paths through the real catalog handler).
The terminal `saga_log` state is read straight from the orchestrator
DB.

- **`TestSaga_HappyPath`** — register + create both succeed: one
  unconfirmed subscription + token, one repo registration, and
  `saga_log = DONE` (DONE ⟹ the confirmation event was published).
- **`TestSaga_BadRepo_Aborts`** — the validator returns
  `ErrRepoNotFound`; the saga aborts before the pivot. No subscription,
  no registration, `saga_log = ABORTED`, and the coordinator surfaces
  `ErrRepoNotFound`.
- **`TestSaga_CreateFails_Compensates`** — a duplicate `(email, repo)`
  already exists, so the pivot (`create`) conflicts. Register ran
  first, so compensation (`release`) must undo it: zero registrations,
  `saga_log = COMPENSATED`, error is `ErrAlreadySubscribed`. **This is
  the compensation that fires in practice.**
- **`TestSaga_CrashAfterCommit_RecoverRepublishes`** — seeds a
  `COMMITTED` saga record that never sent its confirmation, then runs
  `Recover`. The sweep rolls forward (re-publishes confirmation),
  ending `saga_log = DONE`. Proves crash recovery is idempotent.

The NATS container + the three Postgres + the participant handler
registrations are built **once per package run** (`mustSagaInfra` /
`sync.Once`) and reset between tests (`TRUNCATE` + clear the stub
error).

### Subscription HTTP API — real Postgres (`api_test.go`)

HTTP → service → repository → Postgres for the subscription service's
own endpoints (the confirm / unsubscribe / list surface that stayed
behind when subscribe moved to the orchestrator). The subscribe flow
itself is no longer an endpoint here, so tests seed the unconfirmed
subscription + token directly (`seedSubscription`) — standing in for
what the saga writes.

- **`TestHealth`** — `/health` returns `{"status":"ok"}`.
- **`TestConfirmFlow`** — confirm flips `confirmed = true` **and**
  deletes the token row (single-use cleanup).
- **`TestConfirmUnknownToken`** — unknown token → 404.
- **`TestUnsubscribeGET`** — GET unsubscribe hard-deletes the row.
- **`TestUnsubscribePOSTOneClick`** — RFC 8058 one-click POST is
  token-only, unauthenticated, 200.
- **`TestUnsubscribeUnknownToken`** — unknown token → 404.
- **`TestGetSubscriptions`** — list is scoped to the queried email (no
  cross-email leak), returns the right count.
- **`TestGetSubscriptionsRequiresAPIKey`** — missing `X-API-Key` → 401.
- **`TestGetSubscriptionsInvalidEmail`** — malformed `email` query →
  400 before the service is hit.

Postgres is started once per package (`mustSharedDB`) and rows truncate
between tests.

### Catalog repository — dedicated Postgres (`catalog_repository_test.go`, `catalog_cursor_test.go`)

The catalog service owns its own database, so its repo tests spin up a
**dedicated** Postgres with the catalog schema (`newCatalogRepo`) —
not the shared subscription harness DB.

- **`TestCatalogRegister_IsIdempotent`** — the participant contract:
  `Register` / `Release` keyed by `subscription_id` apply exactly once
  under retries (which saga recovery depends on). A second `Register`
  doesn't double-count; a second `Release` doesn't error;
  `ActiveRepos` reflects the net state.
- **`TestCatalogWatchedRepoUpsert`** — the scanner's cursor: an absent
  repo reads back `nil` (first sighting), the first save inserts, and a
  second save **upserts in place** (tag advances, no duplicate row) —
  the real GORM `OnConflict` path that mocks can't cover.

### Notifier consumer — real NATS + JetStream (`notifier_nats_test.go`)

The notifier's JetStream consumer against a live broker, with a
`recordingMailer` standing in for SMTP so the test can count sends and
force a failure.

- **`TestConsumerSendsDedupsAndDLQs`** covers three behaviours in one
  broker session:
  - **Happy path + dedup** — the same `Nats-Msg-Id` published twice
    results in **exactly one** send (publish-side dedup absorbs the
    retry). Asserted with `Eventually` (one send lands) + `Never` (a
    second never does).
  - **DLQ** — a malformed (`{bad json`) payload is a *permanent*
    failure → dead-lettered to `NOTIFY_DLQ`, **never sent**. The test
    fetches from the DLQ consumer and asserts exactly one message
    landed and the mailer count is unchanged.

### Request-reply transport — real NATS (`reqreply_test.go`)

- **`TestRequestReplyRoundTrip`** — the Core NATS request-reply helpers
  (`natsbus.RespondJSON` / `RequestJSON`) over a real broker: a handler
  doubles a number, the caller decodes the reply. This is the transport
  the saga commands ride on, tested in isolation from the saga.

## What's wired vs stubbed

| Layer | Real | Stubbed |
|---|---|---|
| Subscription router / service / repository | ✓ | |
| Saga coordinator + catalog/subscription participants | ✓ | |
| Catalog repository (own schema) | ✓ | |
| Notifier JetStream consumer | ✓ | |
| Postgres — per service (testcontainers, migrated) | ✓ | |
| NATS + JetStream (testcontainers, streams provisioned) | ✓ | |
| GitHub validator | | `stubValidator` — `setErr` per test |
| SMTP mailer | | `recordingMailer` — records/fails sends |

The stubs are tiny in-test types. They implement the same interfaces
production uses (`catalogsaga.RepoValidator`, `notifier.Mailer`); the
real graph is built fresh per group.

## Stack

- `testcontainers-go` boots `postgres:16-alpine` and `nats:2.10-alpine`
  (`-js` for JetStream). Heavy infra is shared via `sync.Once` per
  package run (the saga infra, the subscription DB); the catalog repo
  and the consumer tests start their own container scoped to the test.
- `testify/require` for hard-fail setup + assertions; raw `t.Errorf` /
  `t.Fatalf` for the older subscription-API comparisons.
- `require.Eventually` / `require.Never` for the async JetStream
  send/dedup assertions.

## Running

```
make test-integration
# go test -tags=integration -timeout=2m -count=1 ./tests/integration/...
```

Requires Docker. Wall time is dominated by container startup, not the
tests themselves (rough local figure, not benchmarked).

Gated behind `//go:build integration` so the default `go test ./...`
unit run stays container-free. This build-tag isolation is the same
mechanism `//go:build e2e` uses for the e2e suite: the default `go
test ./...` compiles neither tagged tree.

## Files

```
tests/integration/
  harness_test.go            shared subscription Postgres bootstrap + newTestEnv + truncate
  api_test.go                subscription confirm/unsubscribe/list endpoint tests
  saga_test.go               saga over real NATS + three Postgres (happy / abort / compensate / recover)
  catalog_repository_test.go catalog repo bootstrap + Register/Release idempotency
  catalog_cursor_test.go     watched-repo upsert (scanner cursor)
  notifier_nats_test.go      JetStream consumer: send, dedup, DLQ
  reqreply_test.go           Core NATS request-reply round-trip
```

## For reviewers

Reviewer questions when reading an integration test change:

1. **Does the test assert a real side-effect, not just a return
   value?** Look for the row query (`count(...)`), the `saga_log`
   state read, the DLQ fetch, the dedup `Never`. A test that only
   checks the function returned `nil` is half a test.
2. **Is the right infrastructure real?** Saga behaviour needs real
   NATS + per-service Postgres; a saga test against a single shared DB
   would hide the cross-database point. JetStream dedup/DLQ needs a
   real broker — there is no faking it.
3. **Are idempotency / retry paths exercised?** Register/Release and
   the dedup id are called twice on purpose; recovery seeds a
   mid-saga record. Dropping the second call drops the contract the
   saga depends on.
4. **Should this be e2e or unit instead?** If it needs the whole
   four-service topology and the mail round-trip, it's e2e. If it's a
   pure-function branch with no infrastructure, it's unit.

## What this layer deliberately doesn't cover

- **The whole subscribe flow end-to-end** (orchestrator HTTP → broker
  → participants → real mail → token replay) — e2e. The saga tests
  here drive the coordinator directly and stub GitHub/SMTP.
- **Real GitHub HTTP semantics** (auth headers, retry, status parsing)
  — the stub at the validator boundary skips it; e2e runs the real
  client against an `httptest.Server`.
- **Real SMTP / mail receipt** — the mailer is stubbed/recording here;
  e2e uses a real `SMTPMailer` → Mailpit.
- **Branch logic of pure functions** (scanner tag-diff, error mapping,
  MIME composition) — the unit suite; re-asserting through infra is
  slow and noisy.

## When to add an integration test

Add one when:

- A new saga path or participant lands → assert the terminal
  `saga_log` state + the per-service row side-effects over real NATS.
- A new endpoint lands on the subscription service → happy path + one
  error path + the DB side-effect.
- A repository gains an upsert / idempotent write → prove it against
  real Postgres (mocks can't cover `OnConflict`).
- A new broker behaviour (a stream, a consumer, a DLQ rule) lands →
  prove it against a real JetStream.

Skip when:

- The change is in a pure function with no infrastructure → unit.
- The change needs the full topology + real mail → e2e.
