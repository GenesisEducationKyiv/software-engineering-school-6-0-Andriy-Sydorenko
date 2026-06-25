# Unit tests

Single-package tests, no containers, no broker, no network. **What's
tested here is the logic dense enough that an integration test would
never enumerate it efficiently** — switch statements with 5+ branches,
pure functions with non-obvious edge cases, ordering invariants that
hold even when a collaborator fails. Collaborators are either mocked
(uber-go/mock) or replaced with a tiny hand-rolled fake; nothing
reaches out of the process.

The suite spans the **four-service layout** — `internal/app`
(subscription), `internal/catalog`, `internal/orchestrator`,
`internal/notifier` — plus the shared packages under `internal/shared`.

If you want the cross-layer philosophy (why three layers, why these
boundaries), read [ADR-008](../adr/008-testing-strategy.md). The
topology these tests cover is in [`../microservices.md`](../microservices.md).
To run the suite, read [`README.md`](README.md).

## What's tested and why

Grouped by service. Each entry names the bug class the test exists to
catch; if it'd be the same bug an integration test catches, it's not
here.

### `internal/app` — subscription service

The confirm/unsubscribe/list service plus its email composition.

- **`service`** — the orchestration layer between handlers and
  repositories.
  - `ConfirmSubscription` runs find-token → confirm → delete-token in
    order; an empty/unknown token is rejected before any DB call; a
    **confirm failure aborts before the token delete** (no partial
    success); a **delete-token failure is swallowed** (cleanup is
    best-effort — a transient blip must not turn a valid confirmation
    into a 500).
  - `Unsubscribe` deletes the row and publishes the
    `subscription.removed` cleanup event; the **event-publish failure
    is swallowed** (logged, never a 5xx); a delete failure propagates
    and no event fires.
  - `GetSubscriptions` rejects invalid emails before the lookup and
    returns an empty slice (not nil) on no rows.
  - *(gomock: `SubscriptionRepository`, `EventPublisher`.)*
- **`service` composer / email_notifier** — email rendering + publish.
  - The composer builds confirmation + release emails with the right
    subject/body/URLs, RFC 8058 `List-Unsubscribe` + one-click
    headers, and a **ZWSP-broken display URL** so mail clients don't
    auto-linkify the "copy this link" text.
  - The email notifier renders and **publishes** the composed command
    with a non-empty `event_id` (publish→consume correlation) and the
    release dedup id. *(gomock: `Publisher`.)*
- **`api`** — handlers + middleware, service mocked.
  - Confirm / unsubscribe / list map to the right status codes (200 /
    404 / 400); the unsubscribe token passes through verbatim.
    *(gomock: `Service`.)*
  - `writeError` maps **wrapped** sentinels via `errors.Is` and
    **sanitizes unmapped errors** (never leaks internal text to the
    client).
  - The API-key middleware: bypass when the key is empty (dev/staging
    pathway), 200 on a valid key, **401 on missing vs 403 on wrong** —
    a distinction client retry logic depends on.
  - Prometheus middleware records panics as 500, normalizes unmatched
    routes / non-standard methods. *(These share the global registry —
    never `t.Parallel()`.)*
- **`domain`** — schema mapping at the API boundary.
  - `ToSubscriptionResponse` strips secrets (tokens, internal IDs);
    a regression leaks confirmation tokens in the list response. The
    JSON shape is locked in.

### `internal/catalog` — registry + scanner

- **`scanner`** — the background poll loop. Branch-heavy, runs
  unsupervised, must not crash or double-fire. *(gomock: `Repository`,
  `ReleaseFetcher`, `ReleaseNotifier`.)*
  - **Tag-diff branches**: new tag → save + publish `release.detected`;
    same tag → re-save, no publish; first sighting → silent baseline
    (pre-dates all subscriptions); empty tag → short-circuit before
    the cursor.
  - **Rate-limit aborts the cycle** (continuing burns quota); an
    invalid repo string is skipped, not fatal; a repo-listing error is
    terminal for the cycle.
  - **`safeCheckRepo` panic recovery** — a panic in one repo's check
    must not kill the worker pool (panic injected via a fetcher stub).
  - **Failed save skips the publish** — if we can't persist that we saw
    the tag, the cursor doesn't move and the event re-fires next scan
    rather than being lost.
- **`scanner/workerpool`** — the standalone concurrency primitive.
  Bug class (deadlock, leaked goroutines, missed jobs) is hard to
  surface higher up.
  - Runs every job; respects the concurrency cap (peak in-flight never
    exceeds the size); stops dispatch on context cancel; no-op on
    empty input; size < 1 clamps to 1.
- **`github`** — the HTTP client to api.github.com. Each response code
  maps to a specific domain error.
  - **Status → domain error**: 200 → nil, 404 → `ErrRepoNotFound`,
    429 → `ErrRateLimited`, **403 with `X-RateLimit-Remaining: 0`** and
    **403 with `Retry-After`** → `ErrRateLimited`, **403 without
    rate-limit signals** (SAML etc.) → forbidden, 500 → unexpected.
    `GetLatestRelease` returns `("", nil)` on a release-less 404.
    Sends `Authorization: Bearer <token>`. *(Hand-rolled `httptest`
    server with a host-rewriting transport.)*
  - **Cached client invariants**: OK / not-found / tag / empty-tag are
    cached (one upstream call for repeated reads); **429 is not cached**
    (a cached 429 would serve stale rate-limit errors past the reset);
    a store error **degrades to upstream** rather than failing.
    *(gomock: `Store`.)*
- **`cache`** — Redis DSN construction. URL wins over host/port; empty
  host means no Redis; passwords with URL-significant characters are
  escaped and **parse back to the original** via `redis.ParseURL`.

### `internal/orchestrator` — saga coordinator

The coordinator's own unit tests live with its package
(`internal/orchestrator/service`) and drive the **full saga state
machine** with mocked participants + a recording store — see the
[integration suite](integration.md) for the same machine over real
NATS. The `api` package adds:

- **`api`** — `GET /` serves the subscribe form, and the form posts
  **same-origin to `/subscribe`** (locks in the fix for a stale
  `/api/subscribe` target that once orphaned it).

### `internal/notifier` — stateless delivery

- **`handler`** — classifies each delivery: a valid command sends +
  acks; an SMTP failure is **transient** (retry); a malformed payload
  or an empty recipient is **permanent** (`ErrPermanent`, don't retry).
  *(gomock: `Mailer`.)*
- **`classify`** (`subscriber_test.go`) — the table that turns
  `(err, numDelivered, maxDeliver)` into ack / nak / term: success →
  ack, transient first try → nak, transient exhausted → term, permanent
  → term.
- **`mailer`** — `buildMIME` round-trips through `net/mail` (real
  clients reject MIME that doesn't parse back), uses **CRLF** line
  endings (RFC 5322), and `Send` honours a pre-cancelled context
  without dialing SMTP.

### `internal/shared`

- **`notify`** — the dedup-id formatters (`confirmation:<token>`,
  `release:<repo>:<tag>:<email>`) that drive publish-side dedup.
- **`observability/logging`** — the custom dev `slog` `TextHandler`:
  line format, level labels, **error-chain unwinding**, `LogValuer`
  resolution (so a redacting valuer hides secrets instead of printing
  the raw struct), empty-key dropping, `WithAttrs` accumulation, and
  `NO_COLOR` / `FORCE_COLOR` handling.

## Stack

- **Assertions**: `github.com/stretchr/testify` — `require` for
  hard-fail in setup, `assert` for soft fail. A few older files use
  raw `t.Fatalf`.
- **Mocks**: `go.uber.org/mock` (uber-go/mock, gomock's maintained
  fork). Generated via `go tool mockgen` (Go `tool` directive in
  `go.mod`), committed to the repo. Drift is caught two ways:
  - **CI** runs `make verify-mocks` in the lint workflow; it fails if
    `make generate-mocks` would change anything.
  - **Pre-commit hook** (`scripts/pre-commit`, installed via
    `make install-hooks`) runs the same check locally. Skip with
    `git commit --no-verify` when you're not touching mocks.
- Most packages use **hand-rolled fakes** instead of generated mocks —
  a tiny in-test type implementing the interface (e.g. the scanner's
  panic-injecting fetcher, the GitHub client's host-rewriting
  transport, the notifier's recording mailer). Generated mocks are
  reserved for the interfaces listed in `internal/codegen/gen.go`.
- **Runner**: stdlib `testing`. No third-party runner.

### Codegen lives in `internal/codegen/gen.go`, not in source files

Production source files carry **no** `//go:generate` metadata. All
mockgen directives are centralized in `internal/codegen/gen.go`, which
is gated by the `generate` build tag so it's invisible to normal
builds. `make generate-mocks` runs `go generate -tags=generate ./...`.

Generated mocks land in `internal/<pkg>/mocks/`, imported as
`<pkg>/mocks`.

| Package | Mocked interfaces |
|---|---|
| `internal/app/api` | `Service` |
| `internal/app/service` | `SubscriptionRepository`, `EventPublisher`, `Publisher` (email) |
| `internal/catalog/github` | `Store` |
| `internal/catalog/scanner` | `Repository`, `ReleaseFetcher`, `ReleaseNotifier` |
| `internal/catalog/saga` | `Store`, `RepoValidator` |
| `internal/orchestrator/service` | `Participants`, `ConfirmationPublisher`, saga store |
| `internal/notifier` | `Mailer` |

**Adding a new mocked interface**:

1. Create the interface in `internal/<pkg>/<file>.go`.
2. Add one line to `internal/codegen/gen.go` (paths are relative to
   that directory, since `go generate` runs each directive from the
   file's dir):
   ```go
   //go:generate go tool mockgen -source=../<pkg>/<file>.go -destination=../<pkg>/mocks/<file>_mocks.go -package=mocks
   ```
3. Run `make generate-mocks`.

Forgetting any of these is caught: missing mocks fail to compile in
unit tests; stale mocks fail `make verify-mocks` in CI and the
pre-commit hook.

## Running

```
make test-unit         # no containers, runs in seconds
make generate-mocks    # regenerate mocks after editing a mocked interface
make verify-mocks      # fail if committed mocks would change after regen
make install-hooks     # symlink scripts/pre-commit into .git/hooks (run once)
```

`make test-unit` is `go test ./... -race -count=1`. The default
`go test ./...` runs unit only: the integration tree
(`//go:build integration`) and the e2e tree (`//go:build e2e`) are
excluded by build tag, so neither compiles in the unit run.

## Conventions

- **gomock strictness is the assertion.** Omitting an `EXPECT()`
  asserts that method must not be called — the saga and service tests
  rely on it (e.g. "confirm failure ⟹ delete-token never called",
  "bad repo ⟹ no create, no compensation"). Don't add permissive
  matchers without a reason.
- **Exact-match args** when the value catches a real bug (the
  coordinator asserts the exact `subscription_id` / token flow into
  the create command). `gomock.Any()` everywhere else.
- **Table-driven** when rows differ only in inputs/outputs;
  `t.Run` subtests when each case needs distinct `EXPECT()` chains.
- **Hand-rolled fakes** are the default — a generated mock is added
  only when the interface earns a directive in `internal/codegen/gen.go`.

## For reviewers

When reviewing a unit-test PR, the questions to ask:

1. **Does this test catch a bug class no cheaper assertion would?** A
   pure rename or a plain getter usually doesn't — that test is
   padding.
2. **Does the test exercise the seam, or re-implement it?** A test
   that mirrors the production logic into the assertion passes for the
   same reasons the code does and fails for the same reasons too.
3. **Is the mock setup specific?** `Any()` everywhere with a `Times(0)`
   on one method is asserting wiring, not behaviour. Sometimes that's
   right (persist-then-send ordering); often it's a missed opportunity.
4. **Could this be an integration test instead?** If the test needs
   real Postgres / NATS or >2 mocked collaborators to set up,
   integration is usually cheaper to read and equally fast to run.

## What this layer deliberately doesn't cover

- SQL, GORM wiring, migrations, idempotent upserts → integration.
- The saga over a real broker (commands, compensation, recovery) →
  integration (the coordinator's *logic* is unit-tested with mocked
  participants; its *wiring* to real NATS + per-service Postgres is
  integration).
- JetStream dedup / DLQ, request-reply round-trips → integration.
- The full four-service flow + real mail receipt → e2e.
- HTTP plumbing already exercised by integration — a unit test proving
  "this handler calls this service method" is dead weight if
  integration already proves the round-trip.
