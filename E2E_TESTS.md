# E2E tests

Browser → real app process → **real Postgres + real Mailpit**. The
GitHub upstream is the only thing faked (in-process `httptest.Server`
fixture). This is the **only layer that exercises cross-process flows
and the mail → URL → DB token round-trip** — if a regression breaks
that loop, no other suite catches it.

If you want the cross-layer philosophy, read
[ADR-008](docs/adr/008-testing-strategy.md). For commands and
prerequisites, [`testing.md`](testing.md).

## What this layer proves

Unit tests catch logic in isolation. Integration catches wiring +
DB side-effects with stubbed external boundaries. **E2e catches what
both miss**:

- **Cross-process behaviour.** The browser, the app, Mailpit, and
  Postgres are separate processes. A bug in the listener address, the
  CORS config, the gin middleware order — any of these passes every
  unit and integration test and fails the user. E2e is the layer that
  catches them.
- **The mail → token → handler → DB round-trip.** Subscribe → email
  body → URL extraction → handler → row update is a loop that spans
  every layer of the stack. Unit can't test the loop (it's not
  in-process). Integration can't test the loop (the mailer is
  stubbed). E2e is where it lives.
- **The real GitHub HTTP client.** Integration stubs the client at
  the `RepoValidator` interface — skipping all of `setHeaders`,
  retry, status parsing. E2e routes the real client at an
  `httptest.Server`, so real auth headers and real response parsing
  run on every test.
- **Real SMTP + real MIME.** The composer's MIME output is parsed
  back by Mailpit on receipt. If the body is malformed, Mailpit
  rejects it — proving the real wire format works.
- **Browser-rendered HTML/JS.** The subscribe page has inline JS
  (form submit, status class flipping, HTML5 validation). No API
  test catches a regression in that JS.

The deliberate trade-off: **e2e is narrow on purpose**. We don't
re-assert error-code mapping, pagination, or filter logic here —
those have integration / unit coverage and adding e2e on top would
slow CI for no signal gain.

## What's tested and why

11 tests across two suites. Each test exists because no cheaper layer
catches the bug class.

### `SubscribeSuite` (9 tests, default harness)

The browser-driven UI flow + the cross-process round-trip + GitHub
failure surfaces.

- **`TestRendersForm`** — labels and inputs are reachable by their
  accessible names. Regression here breaks screen readers + every
  other test (they all use `getByLabel`).
- **`TestHappyPath`** — full subscribe flow including the `status ok`
  CSS class flip and form reset. Catches JS-side regressions a
  cURL-against-the-endpoint test would miss.
- **`TestDuplicate`** — two submits of the same payload, first 200
  second 409. Asserted via `ExpectResponse` to avoid a race where
  the `status` element transitions through the success class before
  the second response lands.
- **`TestRepoNotFound`** — pre-stages the GitHub fixture
  (`SetBehavior("ghost", GHNotFound)`), then submits via the form.
  Proves the real `github.Client` parses 404, the service maps it,
  the handler renders it, and the JS shows it.
- **`TestHTML5Validation`** — malformed repo string + bad email.
  Asserts **no POST is fired** (not "the status div has display:none"
  — that's testing CSS, not behaviour). Browser's pattern validation
  is doing the work; if a regression in the HTML breaks it, this
  catches it.
- **`TestNetworkFailure`** — `page.Route` aborts the request before
  the server sees it. The JS error handler must show a useful
  message; a regression that silently swallows the error fails this.
- **`TestLifecycle`** — the headline test. POST → poll Mailpit for
  the real email → extract confirm token from the body → `GET
  /api/confirm/:t` → `GET /api/unsubscribe/:t` → re-use the
  unsubscribe token (must fail, proving one-shot). This is the test
  that justifies the harness existing.
- **`TestSubscribe_RateLimited`** — GitHub fixture returns 429 with
  rate-limit headers; the real `github.Client` recognises it; the
  service maps to `ErrRateLimited`; the handler returns the right
  status; the UI surfaces it. No other test exercises the full chain
  on a non-OK upstream response.
- **`TestSubscribe_ServerError`** — same chain for 5xx.

### `AuthSuite` (2 tests, separate harness with APIKey enforced)

Lives in a different suite because it needs the API-key middleware
**actually enforcing**, and toggling that mid-suite would break
prior tests' baseURL contract.

- **`TestSubscriptions_NoKey_401`** — middleware mount point is
  right; a missing header gets a 401 specifically (not 403 or 200).
- **`TestSubscriptions_WrongKey_403`** — the middleware distinguishes
  "no key" from "wrong key", which matters for client retry logic.

## What's wired vs faked

| Layer | Real | Fixture |
|---|---|---|
| Router (`gin`) | ✓ | |
| Auth middleware (`X-API-Key`) | ✓ (opt-in via `Options.APIKey`) | |
| Service + repository | ✓ | |
| Postgres (testcontainers, migrated) | ✓ | |
| Notifier + `SMTPMailer` (real SMTP send) | ✓ | |
| Mailpit (real SMTP receive + HTTP API) | ✓ | |
| GitHub client (real HTTP, headers, parsing) | ✓ | `httptest.Server` serving `/repos/:owner/:repo[/releases/latest]` |
| Browser (Chromium via Playwright) | ✓ | |

Two **production seams** make this possible without `if testing`
branches anywhere in prod code:

| Env | Wires | Default |
|---|---|---|
| `GITHUB_API_URL` | `internal/github.Config.BaseURL` | `https://api.github.com` |
| `SMTP_HOST` / `SMTP_PORT` | `internal/notifier.Config` | `.env` / k8s secret |

Both are real config knobs (staging uses them); the harness just sets
them to the testcontainers / `httptest.Server` URLs.

## The harness

`e2e/harness/` is a regular Go package, not test-only plumbing. The
public API:

| Field / method | Purpose |
|---|---|
| `New(t, opts...)` | Boots Postgres + Mailpit + GH fixture + app, returns `*Harness`. Cleanup wired to `t.Cleanup`. |
| `BaseURL` | App URL (`http://127.0.0.1:<port>`) |
| `MailpitURL` | Mailpit HTTP API root |
| `APIKey` | Mirrors `Options.APIKey` (empty = middleware bypass) |
| `DB` | `*gorm.DB` for direct row inspection |
| `GitHub` | `*GitHubFixture` — `SetBehavior(owner, b)`, `Reset()` |
| `TruncateDB(t)` | Wipes `subscriptions` |
| `ResetMailpit(t)` | Deletes all captured messages |
| `WaitForMail(t, addr, timeout)` | Polls Mailpit, extracts confirm + unsub tokens by regex on the plain-text body |
| `BaseSuite` | `testify/suite` embed; owns one `Harness` per suite, resets DB + Mailpit + GH fixture between tests |

`Options`:
- `GHValidator` — fully replace the GitHub fixture + real-client
  wiring (use only when a test needs a custom stub, not for
  per-behavior overrides).
- `APIKey` — enable the API-key middleware. Default empty so
  browser-form tests work without a header; `AuthSuite` opts in.

## Layout

```
e2e/
  ui_test.go               SubscribeSuite + TestMain + shared helpers
  lifecycle_test.go        SubscribeSuite: TestLifecycle
  github_failures_test.go  SubscribeSuite: rate-limited, server-error
  auth_test.go             AuthSuite (separate harness, APIKey enforced)
  harness/                 testcontainers + in-process app + fixtures
    harness.go             New(t, opts), shutdown, helpers
    suite.go               BaseSuite (testify) — owns one Harness
    github_fixture.go      httptest.Server with per-owner behavior map
    mailpit.go             WaitForMail — polls + extracts tokens
    smoke_test.go          Self-test: /health + real SMTP round-trip
```

## Stack

- **Browser driver**: `playwright-community/playwright-go` (Chromium
  headless). Chromium reused across suites via `TestMain`; per-test
  isolation is a fresh `BrowserContext`.
- **Container lifecycle**: `testcontainers-go` — Postgres 17 module
  + Mailpit via generic container.
- **Suite framework**: `testify/suite` — `harness.BaseSuite` owns
  one Harness per suite.
- **Assertions**: `testify/require` for setup hard-fail;
  Playwright's `PlaywrightAssertions` for browser-driven waits
  (auto-retry on attached/visible/text-match).

## Running

```
make test-e2e
# go test -tags=e2e -timeout=5m -count=1 ./e2e/...
```

Requires Docker (testcontainers boots Postgres 17 + Mailpit) and the
Playwright Chromium driver. **One-time install:**

```
go run github.com/playwright-community/playwright-go/cmd/playwright install --with-deps chromium
```

**Wall time ≈ 10–11 s** (two harness instances → two container sets;
Chromium reused via `TestMain`).

Gated behind `//go:build e2e` so the default `go test ./...` unit
run stays container-free and browser-free.

## Conventions

- **One suite per API-key mode.** Toggling the middleware mid-suite
  would break the BaseURL contract for prior tests. `AuthSuite`
  exists precisely for the protected paths.
- **`SetBehavior` is per-test.** `BaseSuite.SetupTest` calls
  `GitHub.Reset()` so behavior overrides don't leak between tests.
- **Tokens come from real mail.** Don't read them from the DB —
  `WaitForMail` proves the mail → URL → DB round-trip actually
  works. Reading from the DB skips the whole point of e2e.
- **Per-test browser context.** `s.page()` opens a fresh
  `BrowserContext` so cookies / routes / network rewrites don't
  bleed between tests.
- **Hand-rolled fixtures only at the upstream boundary.** GitHub is
  the only one. Every other dep is real.

## For reviewers

When reading an e2e-test change, the questions to ask:

1. **Does this test prove something cross-process?** If the
   behaviour fits in one process (handler → service → repo),
   integration is cheaper and gives a better stack trace on failure.
2. **Are tokens extracted from real mail?** A test that pre-computes
   or reads from the DB is testing wiring at the wrong layer.
3. **Is the assertion behavioural, not CSS-shaped?** Asserting
   "the status div has class `err`" is fine. Asserting "the status
   div has `display: none`" is testing browser defaults, not your
   app — a CSS tweak silently invalidates it.
4. **Does the test need its own harness, or can it ride in
   `SubscribeSuite`?** A new harness costs ~3s of container startup.
   `AuthSuite` exists because middleware-toggling demanded it;
   future suites should justify themselves.
5. **Are `route.Abort` / `SetBehavior` calls scoped to the test
   that needs them?** Leaking into other tests via shared state
   (the GH fixture map without `Reset`, a `page.Route` on a shared
   context) is the most common flake source.

## What this layer deliberately doesn't cover

- **Per-error-code mapping** on `POST /api/subscribe` — integration
  suite.
- **Response shape, filtering, pagination** on `GET
  /api/subscriptions` — integration.
- **Confirm/unsub token edge cases** (garbage, already-used,
  idempotency) — integration.
- **GET/POST parity** on unsubscribe — integration (handler concern).
- **Branch logic of service / scanner / GitHub client / notifier** —
  unit.
- **SQL, migrations, GORM wiring** — integration.
- **Cross-browser**, **visual regression**, **load** — explicit
  non-goals.

## When to add an e2e test

Add one when behaviour can **only** be proven across processes:

- A user-visible flow that spans browser → server → DB → mail and
  back (lifecycle test is the archetype).
- HTML/JS contract on the subscribe page that no API test catches.
- Wiring of an external seam (auth middleware mount, base-URL
  config) where a broken wire passes every unit + integration test
  but fails the user.

Skip:

- Endpoints already covered by integration with the right
  side-effect asserts.
- Error-code mapping — service unit tests are cheaper.
- Anything the user never sees (background scanner runs, internal
  caches).

## CI

`.github/workflows/e2e-tests.yml`: `setup-go` → cached Playwright
Chromium driver (cache key on `go.sum`) → `go test -tags=e2e
-timeout=10m -count=1 ./e2e/...`. testcontainers pulls Postgres +
Mailpit at runtime on the ubuntu-latest Docker daemon.
