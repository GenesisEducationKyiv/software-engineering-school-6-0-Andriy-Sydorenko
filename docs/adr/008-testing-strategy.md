# ADR-008: Functional Testing Strategy

## Status

Accepted

---

## Context

The project ships a webhook-style backend: a thin HTTP layer fronts a
service with two external boundaries (GitHub for repo validation, SMTP
for confirmation/release mail) and one persistent store (Postgres). A
background scanner polls GitHub and fans out mail on new releases.

Three things make a single-layer test strategy unattractive:

1. **Cross-process invariants exist.** A subscription is only valid
   once the confirmation email's token round-trips DB → mail → URL →
   handler → DB. No single in-process test can prove that loop.
2. **External boundaries dominate the bug surface.** Most production
   regressions sit at GitHub rate-limit handling, mail headers, SMTP
   auth, token formats, and middleware wiring. Mocking those at the
   service boundary hides the bug; testing them through the real
   protocol catches it.
3. **Branch density is high in narrow places.** The service's
   persist-then-send ordering, the GitHub client's status→error
   mapping, the scanner's tag-diff and panic recovery — each has 5+
   branches that an integration test can't enumerate efficiently.

Picking one layer punishes you on the others. Only-unit hides wiring
bugs; only-integration is slow and misses cross-process flows;
only-e2e is unaffordable to grow.

---

## Decision

We adopt **three test layers with sharp scope rules and per-layer CI**,
following the Testing Trophy shape (heavy integration, focused unit,
narrow e2e).

### Layer roles

| Layer | Boundary | Real | Faked |
|---|---|---|---|
| Unit | Single package | Pure functions; mocked deps | All collaborators |
| Integration | HTTP → service → repo → real Postgres | Router, middleware, service, repo, DB, migrations | GitHub validator, mailer (at interface boundary) |
| E2E | Browser → real app process → real Postgres + real SMTP | Everything above + browser + Mailpit + real `github.Client` HTTP | Only the GitHub upstream (`httptest.Server` fixture) |

### Isolation mechanism

**Go build tags**, not path exclusion:

- `//go:build integration` on every `internal/integration/*_test.go`.
- `//go:build e2e` on every `./e2e/...` file (tests + harness package).
- Default `go test ./...` runs unit only — no Docker, no browser.

Build tags chosen over path exclusion because exclusion lives only in
CI/Makefile shell strings (easy to drift); tags live next to the code
and fail at compile time when stale.

### CI

One workflow per layer:

- `.github/workflows/unit-tests.yml` → `make test-unit`
- `.github/workflows/integration-tests.yml` → `make test-integration`
- `.github/workflows/e2e-tests.yml` → `make test-e2e`
- `.github/workflows/ci.yml` → `golangci-lint`

Separate jobs over a consolidated one for: failure isolation (flaky e2e
doesn't mask a real unit failure), feedback speed (unit job ~30s in
parallel vs ~3min serial), and per-job caching (Playwright Chromium
cache only matters in e2e).

### E2E architecture

**In-process harness over docker-compose.** `e2e/harness/` boots
Postgres + Mailpit via `testcontainers-go`, then runs the real app
in-process on a random port. The harness is a regular Go package, not a
script; it's reusable by any suite that needs the same topology.

**Real Mailpit over in-memory mail capture.** The real `SMTPMailer`
talks to Mailpit, which receives via real SMTP and exposes captured
messages via HTTP. Tests extract tokens from the actual email body,
proving the composer + SMTP + parsing all work as a unit. The cost is
one container; the payoff is no test-only mailer code path.

**Real `github.Client` against `httptest.Server` fixture, over
stub-at-interface.** `internal/github.Config.BaseURL` (env
`GITHUB_API_URL`) lets the harness redirect the real client. We
exercise the real auth headers, retry, and status parsing — only the
upstream is fake.

### What each layer deliberately doesn't cover

- **Unit doesn't cover** SQL, GORM wiring, migrations, cross-process
  flows. Those need a real DB or a real process — push down.
- **Integration doesn't cover** browser-rendered HTML/JS, real SMTP
  send/receive, full mail-token round-trips. Stubs at the interface
  boundary by design — what the service handed off, not what the
  outside world received.
- **E2E doesn't cover** branch logic (status-code mapping, error
  hierarchies, validation) or pagination/filtering. Those belong in
  cheaper layers — e2e proves wiring, not switch statements.

These exclusions are enforced by review, not by tooling. Per-suite docs
state them explicitly.

---

## Consequences

### Positive

- **Bugs land in the layer that can debug them.** A status-mapping
  regression fails a fast unit test with a stack trace; a wiring bug
  fails integration with the response body; a cross-process bug fails
  e2e with a screenshot + container logs.
- **Default `go test ./...` stays under 5 seconds.** Pre-commit hooks,
  IDE save-test, contributor first-run — all fast.
- **Each suite has a clear "when to add" rule.** Documented per-layer;
  reduces the "should this be a unit or integration test?" debate.
- **Production code carries the test seams it needs.**
  `GITHUB_API_URL` and `SMTP_HOST/PORT` are real prod knobs (staging
  uses them); the harness piggybacks. Zero `if testing` branches in
  prod paths.
- **CI green = three independent signals.** Each layer's pipeline can
  be marked required or advisory independently.

### Negative

- **Three sets of dependencies to maintain.** `testify`, `uber-go/mock`,
  `testcontainers-go`, `playwright-go`, Mailpit container image,
  Playwright Chromium driver. Each can break independently.
- **First-run friction in e2e.** Requires Docker daemon + a one-time
  `playwright install` step (~90 MB Chromium download). Documented in
  `testing.md`.
- **Container startup dominates wall time for short suites.**
  Integration is ~5s wall for ~3s of test; e2e is ~10s wall for ~3s of
  test. Acceptable for the layer's value.
- **Build tags hide tests from default `go test`.** A new contributor
  could miss the integration/e2e suites if they only know
  `go test ./...`. Mitigated by `make test` and `testing.md`.

---

## Alternatives Considered

### Option 1: Single `go test ./...` with everything in-process

Rejected. Forces every contributor to have Docker running for unit
tests, conflates flakes across layers, makes CI's failure isolation
useless. The cost of one extra build tag is trivially less than the
cost of "the unit job is red but it's actually a Postgres container
flake."

### Option 2: Docker-compose-based e2e stack with a `cmd/e2e-server` binary

Rejected after initial implementation. The dedicated test binary forced
a stubbed-mailer code path (`stdoutMailer`) that prod never ran — a
divergence risk for any future mail-related change. Compose orchestration
also required a curl-loop readiness check in CI. `testcontainers-go` +
in-process harness eliminates both.

### Option 3: TS Playwright project at `./e2e/`

Rejected after initial implementation. Two toolchains (Node + Go), two
dependency files, two CI install paths. For a single-page UI tested
by ~10 tests, the Playwright official tooling advantage (codegen,
trace viewer) didn't justify the friction. `playwright-go` keeps
everything in one language and one `go.mod`.

### Option 4: In-memory mail capture (custom `Mailer` impl)

Rejected after initial design. Required a `_e2e/mail/last` debug route
on the e2e binary (another test-only seam). Mailpit removes the seam
and exercises the real SMTP send.

### Option 5: Stub GitHub at the interface boundary in e2e

Rejected. The interface stub skips all the real `github.Client` code:
auth headers, retry, status parsing, rate-limit detection. With the
`httptest.Server` fixture we exercise the real client — the only thing
faked is the upstream HTTP server.

---

## Rollout Plan

Already shipped in HW#5:

1. Add build tags to existing integration and e2e files.
2. Rewrite Makefile with `test-unit / test-integration / test-e2e / test`.
3. Implement `e2e/harness/` (testcontainers + in-process app +
   GitHub fixture + Mailpit polling).
4. Migrate e2e from TS Playwright → `playwright-go`.
5. Delete `cmd/e2e-server`, `Dockerfile.e2e`, `docker-compose.e2e.yml`.
6. Split CI: one workflow per layer.
7. Document per-layer scope in `UNIT_TESTS.md`,
   `INTEGRATION_TESTS.md`, `E2E_TESTS.md`; overview in `testing.md`.

No migration needed for downstream consumers — this is a test-only
refactor with no API or schema impact.

---

## Operational Impact

- **CI runner cost.** Unit job ~30s; integration ~1m (cold container
  pulls dominate); e2e ~2–3m (Chromium driver + Postgres + Mailpit
  pulls). Within free-tier budgets for this repo's PR volume.
- **Developer workflow.** `make test` runs all three locally in ~20s
  after a warm cache. `make test-unit` for the inner loop.
- **Debugging.** Each layer fails with the artifact you need: stack
  trace (unit), HTTP body + DB row (integration), Playwright trace +
  container logs (e2e). No "which layer is this even from?" guessing.
- **Onboarding.** `testing.md` is the single entry point; per-suite
  docs cover details. Build tags discoverable via `make help` or
  `Makefile` directly.

---

## Security Considerations

- The harness sets real SMTP credentials against Mailpit
  (`MP_SMTP_AUTH_ACCEPT_ANY=true`). Mailpit accepts any auth; no
  credentials are reused from prod.
- The GitHub fixture serves canned JSON. No outbound traffic from CI
  to `api.github.com` in any test path — eliminates rate-limit
  exposure on PR runs.
- Test-only env vars (`E2E_BASE_URL`, etc.) are read by the harness
  only. Production reads its own config.

No significant security impact.

---

## Performance Considerations

- Unit suite wall time: ~3–4 s (parallel across packages).
- Integration suite wall time: ~5 s (Postgres container ~3 s startup,
  shared per package via `TestMain`).
- E2E suite wall time: ~10–11 s (two harness instances → two
  container sets; Chromium reused via `TestMain`).
- CI total (parallel jobs): ~3 min cold, ~1 min warm.

If wall time becomes painful, the first lever is sharing one harness
across `SubscribeSuite` and `AuthSuite` (currently kept separate so the
API-key middleware can be toggled). That would shave one container
set off e2e (~3s) at the cost of one extra test-isolation knob.

---

## References

- `testing.md` — operator-facing overview.
- `UNIT_TESTS.md`, `INTEGRATION_TESTS.md`, `E2E_TESTS.md` —
  per-suite detail.
- Testing Trophy concept: <https://kentcdodds.com/blog/the-testing-trophy-and-testing-classifications>
- ADR-007 (internal layering) — the layer boundaries this strategy
  asserts on.
