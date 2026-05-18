# ADR-008: Functional Testing Strategy

## Status

Accepted

## Context

The backend is a thin HTTP layer over a service with two external boundaries
(GitHub for repo validation, SMTP for confirmation/release mail) and one store
(Postgres), plus a background scanner that polls GitHub and fans out mail.

A single test layer fails this shape three ways:

1. **Cross-process invariants exist.** A subscription is valid only once the
   confirmation token round-trips DB → mail → URL → handler → DB. No in-process
   test proves that loop.
2. **External boundaries dominate the bug surface.** Rate-limit handling, mail
   headers, SMTP auth, and token formats regress in production. Mocking them at
   the service boundary hides the bug.
3. **Branch density is high in narrow places.** Persist-then-send ordering,
   status→error mapping, scanner tag-diff and panic recovery each have 5+
   branches an integration test can't enumerate cheaply.

No single layer suffices: only-unit hides wiring bugs, only-integration is slow
and misses the cross-process loop, only-e2e is unaffordable to grow. An
integration-dominant (Testing Trophy) split is also a poor fit here: with
containerized Postgres/Mailpit/Chromium every integration and e2e test carries
real wall-cost, while the branch-dense logic in (3) is cheapest and most precise
to enumerate at the unit layer. Volume therefore tracks where each bug class is
cheapest to catch — yielding a pyramid (broad unit, focused integration, narrow
e2e), with integration still the highest-confidence layer.

## Decision

Adopt **three test layers with sharp scope rules and one CI workflow per
layer**, in a test-pyramid shape: a broad unit base over branch-dense logic, a
focused integration band over the HTTP → service → repo → Postgres path, and a
narrow e2e cap (the rejection of an integration-dominant Testing Trophy is
argued in Context above).

**Layer scope:**

| Layer | Boundary | Faked |
|---|---|---|
| Unit | Single package, pure logic | All collaborators |
| Integration | HTTP → service → repo → real Postgres | GitHub validator, mailer (at interface) |
| E2E | Browser → real app → real Postgres + real SMTP, real `github.Client` | Only GitHub upstream (`httptest.Server` fixture) |

**Isolation by Go build tags, not path exclusion** (`//go:build integration`,
`//go:build e2e`). Tags live next to the code and fail at compile time when
stale; exclusion lives in CI shell strings that drift. Default `go test ./...`
runs unit only — no Docker, no browser.

**E2E runs in-process via `testcontainers-go`**, not docker-compose: the
harness boots Postgres + Mailpit and runs the real app on a random port. This
removes the test-only mailer code path and CI readiness loops the prior
compose binary required. It uses **real Mailpit** (exercises real SMTP send and
token parsing) and the **real `github.Client` redirected via `GITHUB_API_URL`**
to an `httptest.Server` fixture (exercises real auth/retry/status parsing).

**One CI workflow per layer** (`unit`, `integration`, `e2e`, `lint`) for
failure isolation and feedback speed, instead of one consolidated job.

Layer exclusions are enforced by review: unit skips SQL/migrations/cross-process
flows; integration stubs the outside world at the interface; e2e proves wiring,
not branch logic.

**Scope boundary — the composition root is not under test.** No layer invokes
the production bootstrap (`run()` in `cmd/server/main.go`). Each rebuilds the
dependency graph itself: unit with mocks, integration with a partial graph, and
the e2e harness by mirroring `run()`'s wiring in-process. Two things therefore
fall outside all three layers: (1) `run()` itself — config load, migrate-on-boot,
Redis-cache wiring, and server lifecycle/shutdown; a constructor-signature or
wiring-order drift in `main.go` fails no test, only a real boot. (2) The scanner
goroutine (`scan.Run`), started in `run()` but in no harness, so background
release-scan and mail fan-out are unexercised end-to-end. Accepted as a gap:
covering it means either booting the real binary or extracting a shared wiring
function — deferred until a wiring/scanner regression justifies the cost.

## Consequences

**Positive**

- Bugs fail in the layer that can debug them — stack trace (unit), HTTP body +
  DB row (integration), Playwright trace + container logs (e2e).
- Default `go test ./...` stays under ~4s; the inner loop needs no Docker.
- Production code carries only real seams (`GITHUB_API_URL`, `SMTP_HOST/PORT`
  are staging knobs); zero `if testing` branches in prod paths.
- Each layer's CI signal is independently required or advisory.

**Negative**

- More dependencies to maintain (`testcontainers-go`, `playwright-go`, Mailpit
  and `chromedp/headless-shell` images), each able to break independently.
- E2E needs a Docker daemon and a first-run image/driver pull (~410 MB).
- Container startup dominates wall time for short suites (e2e ~18s wall for ~3s
  of test). CI total ~3 min cold, ~1 min warm.
- Build tags hide integration/e2e from a naive `go test ./...`; mitigated by
  `make test` and [`docs/testing/`](../testing/README.md).

## Alternatives Considered

- **Single in-process `go test ./...`** — rejected: forces Docker on every unit
  run and conflates flakes across layers, defeating failure isolation.
- **Docker-compose e2e with a `cmd/e2e-server` binary** — rejected: the test
  binary forced a stubbed-mailer path prod never ran, plus a CI readiness loop.
- **TS Playwright project** — rejected: two toolchains and CI install paths for
  ~10 tests; `playwright-go` keeps one language and one `go.mod`.
- **In-memory mail capture** — rejected: needed a test-only debug route; Mailpit
  removes the seam and exercises real SMTP.
- **Stub GitHub at the interface in e2e** — rejected: skips the real client's
  auth/retry/status parsing; the fixture fakes only the upstream HTTP server.

## References

- ADR-007 (internal layering) — the boundaries this strategy asserts on.
- Test Pyramid (Fowler): <https://martinfowler.com/articles/practical-test-pyramid.html>
- Testing Trophy (considered, not adopted): <https://kentcdodds.com/blog/the-testing-trophy-and-testing-classifications>
