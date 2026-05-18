# Unit tests

Single-package tests with all collaborators mocked. **What's tested
here is the logic dense enough that an integration test would never
enumerate it efficiently** — switch statements with 5+ branches, pure
functions with non-obvious edge cases, ordering invariants that hold
even when downstream fails.

If you want the cross-layer philosophy (why three layers, why these
boundaries), read [ADR-008](../adr/008-testing-strategy.md). If you
want to run the suite, read [`README.md`](README.md).

## What's tested and why

Each entry below names a specific class of bug the suite would catch.
That's the test's reason to exist; if it'd be the same bug an
integration test catches, it's not here.

### `internal/service`

The orchestration layer between handlers and repositories.

- **`Subscribe` persist-then-send ordering.** A bug where mail goes
  out before the row is persisted would silently produce orphan
  confirmation emails (user clicks a token that doesn't exist). The
  test asserts that if `Persist` errors, `Send` is never called —
  enforced via `gomock`'s strictness (no `EXPECT()` = must-not-call).
- **`ConfirmSubscription` swallows `DeleteToken` failure.** Token
  cleanup is best-effort; if it errors, the confirmation itself must
  still succeed (otherwise a transient DB blip turns a valid
  confirmation into a 500 the user can't recover from).
- **Sentinel error propagation.** `errors.Is` must keep working
  through service-level wrapping; broken wrapping means the API layer
  can't map errors to HTTP status codes.

### `internal/scanner`

The background polling loop. Branch-heavy, runs unsupervised in
production — must not crash and must not double-fire.

- **Tag-diff branches**: new / same / baseline (first observation) /
  empty (deleted release). Each maps to a different action; getting
  it wrong sends duplicate or missing notifications.
- **`safeCheckRepo` panic recovery.** A panic in one repo's check
  must not kill the worker pool. Test injects a `panickyFetcher`.
- **Rate-limit aborts the cycle.** Continuing through a 429 burns
  quota and gets the account banned.
- **Failed `UpdateLastSeenTag` skips notification.** If we can't
  persist that we saw the tag, sending the notification means we'll
  re-fire on the next tick — better to skip and retry later.

### `internal/scanner/workerpool`

Tiny standalone primitive — gets its own tests because its bug class
(deadlock, leaked goroutines) is hard to surface at any higher layer.

- Concurrency cap, context-cancel dispatch stop, size-clamp on
  malformed inputs.

### `internal/api`

Handlers and middleware. Service is mocked so we test the HTTP
contract in isolation.

- **Binding rejects bad input *before* calling the service.** A
  malformed payload reaching the service is a layering violation; we
  prove the handler short-circuits.
- **`writeError` sentinel mapping + unmapped sanitization.** Sentinel
  errors map to documented status codes; anything unmapped becomes a
  500 with a generic message (never leaks internal error text to the
  client).
- **API-key middleware bypass when key is empty.** This is the
  staging/dev pathway — accidentally enabling auth in dev would
  block local testing.

### `internal/github`

The HTTP client to api.github.com. Lots of branches on response
codes; each must map to a specific domain error.

- **Status-code → domain-error mapping**: 200, 404 →
  `ErrRepoNotFound`, 429 → `ErrRateLimited`, 403 with SAML headers
  (not rate-limit), 403 with `X-RateLimit-Remaining: 0`, 403 with
  `Retry-After`. Each branch caught a real GitHub edge case at some
  point.
- **Cached client invariants.** OK and 404 are cached; **429 is
  not** (caching a 429 means we'd serve stale rate-limit errors for
  10 minutes after the limit reset). Redis down → degrade to
  upstream, not crash.

### `internal/notifier`

The mailer + composer. Easy to get the byte-level format wrong; hard
to debug from logs.

- **Composer URL building.** ZWSP-broken display URL (so mail
  clients don't auto-linkify the "copy this link" text); RFC 8058
  `List-Unsubscribe` + `One-Click` headers (one-click unsubscribe
  compliance with Gmail/Yahoo).
- **`buildMIME` round-trip through `net/mail`.** If the MIME we
  emit can't be parsed back by `net/mail`, real mail clients will
  reject it. CRLF wire format, boundary marker placement, header
  ordering.
- **`Send` honours pre-cancelled context.** Doesn't block on SMTP
  if the caller already gave up.

### `internal/config`

Boot-time env parsing.

- **`getEnv*` parsing and fallback** — covers the easy cases.
- **`loadScannerConfig` panics on non-positive concurrency.** This
  is the boundary rule: bad config should fail loud at boot, not
  produce a silently-degraded scanner.
- **`validate` failure modes.** The cross-field DB rule
  (`DATABASE_URL` or `DB_USER+DB_NAME`) is easy to misconfigure;
  test asserts the boot fails with a useful message.

### `internal/domain`

Schema mapping at the API boundary.

- **`ToSubscriptionResponse` strips secrets** (tokens, internal IDs).
  Regression here leaks confirmation tokens in the
  `GET /subscriptions` response.

## Stack

- **Assertions**: `github.com/stretchr/testify` — `require` for hard
  fail in setup, `assert` for soft fail.
- **Mocks**: `go.uber.org/mock` (uber-go/mock, gomock's maintained
  fork). Generated via `go tool mockgen` (Go 1.24+ `tool` directive in
  `go.mod`), committed to the repo. Drift is caught two ways:
  - **CI** runs `make verify-mocks` in the lint workflow; fails if
    `make generate-mocks` would change anything.
  - **Pre-commit hook** (`scripts/pre-commit`, installed via
    `make install-hooks`) runs the same check locally. Skip with
    `git commit --no-verify` when you know you're not touching mocks.
- **Runner**: stdlib `testing`. No third-party runner.

### Codegen lives in `internal/codegen/gen.go`, not in source files

Production source files carry **no** `//go:generate` metadata. All
mockgen directives are centralized in `internal/codegen/gen.go`, which is
gated by the `generate` build tag so it's invisible to normal builds.

Generated mocks land in `internal/<pkg>/mocks/`, imported as
`<pkg>/mocks`.

| Package | Mocked interfaces |
|---|---|
| `internal/service` | `SubscriptionRepository`, `RepoValidator`, `ConfirmationSender` |
| `internal/scanner` | `Repository`, `ReleaseFetcher`, `ReleaseNotifier` |
| `internal/api` | `Service` |
| `internal/github` | `Store` |

**Adding a new mocked interface**:

1. Create the interface in `internal/<pkg>/<file>.go`.
2. Add one line to `internal/codegen/gen.go` (paths are relative to that
   directory, since `go generate` runs each directive from the file's dir):
   ```go
   //go:generate go tool mockgen -source=../<pkg>/<file>.go -destination=../<pkg>/mocks/<file>_mocks.go -package=mocks
   ```
3. Run `make generate-mocks`.

Forgetting any of these is caught: missing mocks fail to compile in
unit tests; stale mocks fail `make verify-mocks` in CI and in the
pre-commit hook.

## Running

```
make test-unit         # ~3–4 s wall, no containers
make generate-mocks          # regenerate mocks after editing an interface
make verify-mocks   # fail if committed mocks would change after regen
make install-hooks     # symlink scripts/pre-commit into .git/hooks (run once)
```

The unit job excludes `./tests/e2e/...` (`//go:build e2e`) and
`./tests/integration/...` (`//go:build integration`). Default
`go test ./...` runs unit only.

## Conventions

- **gomock strictness is the assertion.** Omitting an `EXPECT()`
  asserts that method must not be called. Tests like
  `TestSubscribe_PersistErrorPropagatesAndSkipsEmail` and
  `TestSameTagNoNotification` rely on this. Don't add permissive
  matchers without a reason.
- **Exact-match args** when the value catches a real bug (`uint(42)`
  proves we passed the token's `SubscriptionID`, not some other ID).
  `gomock.Any()` everywhere else.
- **Table-driven** when rows differ only in inputs/outputs;
  `t.Run` blocks when each case needs distinct `EXPECT()` chains.
- **Hand-rolled fakes only when no interface exists.** Currently
  `hostRewrite` (http.RoundTripper for httptest redirection) and
  `panickyFetcher` (panic injection). Adding more is a smell.

## For reviewers

When reviewing a unit-test PR, the questions to ask:

1. **Does this test catch a bug class no cheaper assertion would?**
   If the code under test is a pure rename or a plain getter, the
   answer is usually no — the test is padding.
2. **Does the test exercise the seam, or re-implement it?** A test
   that mirrors the production function's logic into the assertion
   isn't testing anything — it'll pass for the same reasons the
   prod code does and fail for the same reasons too.
3. **Is the mock setup specific?** If it's `Any()` everywhere with a
   `Times(0)` on one method, the test is asserting wiring, not
   behaviour. Sometimes that's right (persist-then-send); often it's
   a missed opportunity.
4. **Could this be an integration test instead?** If the test
   needs >2 mocked collaborators to set up, integration is usually
   cheaper to read and equally fast to run.

## What this layer deliberately doesn't cover

- SQL, GORM wiring, migrations → integration suite.
- Cross-process flows, browser interaction, real mail receipt → e2e
  suite.
- HTTP plumbing that's already exercised by integration. A unit test
  proving "this handler calls this service method" is dead weight if
  integration already proves the round-trip.
