# repo-release-notifier

A small Go service that lets people subscribe (by email) to GitHub
release notifications for a given repository. Users confirm via a
link in the confirmation email; a background scanner polls GitHub
and sends a notification whenever the latest tag for a repo changes.

Single binary, PostgreSQL for storage, optional Redis for caching
GitHub API responses, SMTP for email.

---

## Running

Live deployment: <https://repo-release-notifier.vercel.app>

The expected path is `docker compose up --build`. That brings up
Postgres, Redis and the app together; migrations run on startup.

Locally, once `.env` is populated (`cp .env.example .env` and fill
in SMTP credentials at minimum):

```
go run ./cmd/server
go test ./... -race
golangci-lint run ./...
```

Only `DATABASE_URL` (or the split `DB_*` vars) and the SMTP
credentials are strictly required. `REDIS_URL`, `GITHUB_TOKEN` and
`API_KEY` are optional but anything resembling production should
set all three — see the notes below for why.

---

## Architecture

One process, four concerns:

- **HTTP API** (Gin) — the four endpoints from the swagger spec
  plus a `GET /` that serves an HTML subscription form and a
  `GET /health` for liveness.
- **Service layer** — all business logic; validates input, talks
  to the repository, the GitHub client and the notifier. Depends
  on interfaces, not concrete types.
- **Scanner** — a ticker-driven goroutine that periodically
  iterates every confirmed subscription, checks GitHub for a new
  release tag, updates `last_seen_tag`, and hands off to the
  notifier when a new tag appears.
- **Notifier** — SMTP sender that renders HTML + plaintext email
  bodies from embedded templates.

Packages follow the same shape:

```
cmd/server/main.go           composition root + graceful shutdown

internal/
  domain/                    GORM models, DTOs, sentinel errors (no deps)
  config/                    env loader
  db/                        Postgres connection, migrations
  api/                       HTTP handlers, middleware, router, pages
  service/                   business logic, domain error mapping
  repository/                GORM queries
  github/                    GitHub client + optional Redis-cached wrapper
  notifier/                  SMTP + template rendering
  scanner/                   background release checker
  cache/                     thin Redis wrapper
  templates/                 embedded HTML (emails + pages)
```

The layering is strict:

```
Handler  →  Service  →  Repository  →  DB
             ↘             ↘
              GitHub        Notifier
```

Handlers parse HTTP and map domain errors to status codes; they
never touch GORM or know anything about SMTP. The service never
returns HTTP responses or takes a `gin.Context`. Interfaces are
declared at the consumer — `service.SubscriptionRepository`,
`scanner.Repository`, `service.ConfirmationSender` and friends are
defined where they're used, and the implementing packages satisfy
them structurally. No DI framework.

Domain types live in a leaf `internal/domain` package that imports
nothing else, so there are no import cycles. `cmd/server/main.go`
is the only place that wires the whole graph together.

---

## Key flows

### Subscribe

`POST /api/subscribe` takes `{email, repo}`. The service:

1. Validates `repo` against `^[a-zA-Z0-9._-]+/[a-zA-Z0-9._-]+$`.
   Anything else is a 400 before any external call, so we don't
   burn rate-limit budget on malformed input.
2. Checks for an existing subscription on `(email, repo)` and
   returns 409 if found. Soft-deleted rows don't count — see the
   partial-index note further down.
3. Calls `GET /repos/{owner}/{repo}` on the GitHub API. A 404
   bubbles up as a 404; a 429 or 403 with `X-RateLimit-Remaining: 0`
   / `Retry-After` becomes a 503.
4. Generates a 32-byte `crypto/rand` confirmation token plus a
   separate unsubscribe token (stored on the subscription row
   itself, long-lived, used in release emails).
5. Persists the subscription with `confirmed = false` and fires
   the confirmation email. An SMTP failure is logged but does not
   fail the request: the row exists, the user can hit the confirm
   link if the mail was delayed.

### Confirm / Unsubscribe

`GET /api/confirm/:token` looks up the confirmation token, sets
`confirmed = true` on the associated subscription, and deletes the
token row. One-shot.

`GET /api/unsubscribe/:token` looks the subscription up by its
unsubscribe token and soft-deletes it. `POST /api/unsubscribe/:token`
is the same handler — exposed as POST too so the one-click
unsubscribe header in outgoing mail works natively in Gmail and
Apple Mail.

### Scanner

Started as a goroutine from `main`, fed the process-level context
so SIGINT/SIGTERM cleanly stops it. Each tick:

1. `FindDistinctConfirmedRepos` returns one string per unique
   repo that at least one confirmed user cares about — we dedupe
   so we make one GitHub call per repo per cycle, not one per
   subscription.
2. For each repo: fetch `releases/latest`, then every confirmed
   subscriber on that repo. If the new tag matches the stored
   `last_seen_tag`, skip. Otherwise update the tag and email.
3. If GitHub returns rate-limited, abort the entire cycle rather
   than burning further budget on guaranteed failures. Next tick
   retries normally.
4. `checkRepo` is wrapped in `safeCheckRepo`, which recovers
   panics and logs them — one bad repo never kills the goroutine.

### Silent first scan

A freshly confirmed subscription has `last_seen_tag = ""`. The
first scan records the current tag but does **not** email — a new
subscriber shouldn't immediately receive a notification for a
release that happened a year ago. From the second scan onwards,
a change triggers an email.

---

## Data model

Two tables.

`subscriptions`:
`id, email, repo, confirmed, last_seen_tag, unsubscribe_token,
created_at, updated_at, deleted_at`

`confirmation_tokens`:
`id, token, subscription_id (FK, ON DELETE CASCADE), created_at,
deleted_at`

**Partial unique index.** GORM's `uniqueIndex` tag produces a
plain unique index on `(email, repo)`, which counts soft-deleted
rows — so a user who unsubscribed from `golang/go` could never
re-subscribe to it. On migration we drop that index and create

```
CREATE UNIQUE INDEX idx_email_repo_live
  ON subscriptions (email, repo) WHERE deleted_at IS NULL;
```

Re-subscribing now works; the tombstone row still exists for
audit purposes but doesn't block live writes.

---

## Rate limiting and caching

Without a `GITHUB_TOKEN`, GitHub allows 60 requests an hour per
IP. That's enough to validate a subscribe here and there, but
the scanner burns through it quickly once you have even a few
distinct repos. The client:

- Sets `User-Agent` (required) and `Authorization: Bearer` when a
  token is configured.
- Detects rate limiting from both 429 and 403 + `X-RateLimit-Remaining: 0`
  (GitHub's primary-rate-limit response) and 403 + `Retry-After`
  (secondary). Other 403s are surfaced as-is.

When `REDIS_URL` is set, `internal/github/cached_client.go` wraps
the raw client with a 10-minute TTL. Cached outcomes:

- `ValidateRepo` → OK (`ok`) or not-found (`404`).
- `GetLatestRelease` → the tag, or a sentinel for "no releases yet".

Network errors and rate-limit responses are **never** cached —
the next call retries. If Redis is unreachable at startup, the
service logs a warning and runs without the cache. Nothing else
changes; the dependency is genuinely optional.

---

## HTML subscription page

`GET /` serves a minimal form from `internal/templates/pages/subscribe.html`
(embedded via `//go:embed`). It `fetch`es `POST /api/subscribe`
with a JSON body and shows inline success / error messages. If
the deployment has an API key configured, there's an optional
password field on the form for it.

All HTML the service ever emits — emails and public pages — lives
under `internal/templates/` and goes through `RenderEmail` or
`Page` helpers. Consumers don't touch the filesystem.

---

## Emails

Each outbound mail is multipart/alternative (plaintext + HTML)
with three things worth mentioning:

- **`List-Unsubscribe` + `List-Unsubscribe-Post` headers** so
  Gmail, Apple Mail, Outlook and Yahoo render a native
  "Unsubscribe" button in the UI. One POST from the client to
  `/api/unsubscribe/:token` (we accept both GET and POST) and the
  subscription is gone.
- **Unsubscribe link in the confirmation email**, not only in
  release notifications. If someone subscribes you without
  consent, you shouldn't have to wait for a release to get out.
- **Zero-width space trick.** The confirmation email prints the
  URL twice — once inside a button, once as plaintext so users
  can copy it. Mail clients aggressively auto-linkify any bare
  `https://…` they see, which turns the copy-paste version back
  into a clickable link that defeats its purpose. `breakAutoLink`
  inserts a U+200B between `https` and `://` so the clients'
  detectors don't match. Browsers strip the ZWSP on paste, so the
  link still works.

---

## API key authentication

`POST /api/subscribe` and `GET /api/subscriptions` sit behind an
`X-API-Key` header check when `API_KEY` is set. Confirm and
unsubscribe stay open because they're opened from mail clients,
which can't attach request headers — the token in the URL is the
capability for those routes (256 bits of entropy from `crypto/rand`).

When `API_KEY` is unset, the middleware no-ops. That's convenient
for local development; production deployments must set the env var.

---

## REST vs gRPC

The task lists gRPC as a bonus. I skipped it on purpose.

The two services we talk to are GitHub (REST + GraphQL only — no
gRPC endpoint to consume) and SMTP (obviously not gRPC). The two
services that talk to us are browsers and email clients. Browsers
can't speak gRPC natively — they need gRPC-Web and a proxy in
front. Email clients open HTTP links, period. There is no
internal service mesh here, no streaming, no bidirectional flow,
no sub-millisecond latency target.

Adding gRPC would mean duplicating every handler against a
protobuf service, running codegen, and then still needing the
REST surface anyway because the web page and the email links
can't go away. That's pure surface area for zero consumer
benefit, so the endpoints stay REST-only.

If this project ever grew a second Go service that wanted to
subscribe/unsubscribe users programmatically in bulk, gRPC would
start making sense. It doesn't today.

---

## Constant-time token comparison

Tokens are 32 bytes of `crypto/rand`, hex-encoded — 64 characters,
256 bits of entropy. A timing attack against an indexed
`WHERE token = ?` lookup on that amount of entropy is not
reachable in practice: you'd need ~10^18 probe requests to leak
one byte, against an endpoint that returns 404 on bad tokens and
is rate-limited by any reasonable load balancer.

Switching to `subtle.ConstantTimeCompare` would force a full
table scan on every confirm / unsubscribe request (or a
constant-time-equal subquery per row). Real performance cost,
theoretical attack, wrong trade. If the tokens were shorter
(≤ 128 bits) or user-chosen, I'd flip the decision — but at 256
bits the indexed lookup is the right call.

---

## Graceful shutdown

`main` is deliberately tiny — `main()` calls `run() error`, and a
failure from `run` is logged and turned into `os.Exit(1)`. All
fallible initialization lives in `run`, so deferred cleanup
actually runs on any failure path.

The composition root creates a `signal.NotifyContext(SIGINT,
SIGTERM)` and hands it to the scanner goroutine and a separate
goroutine that waits for cancellation to call `server.Shutdown`.
The HTTP server itself has explicit `ReadHeaderTimeout`,
`ReadTimeout`, `WriteTimeout` and `IdleTimeout` — using Gin's
`router.Run(addr)` helper doesn't set any of them, which upsets
both Slowloris-aware linters and production load balancers.

---

## Testing

Unit tests use the standard `testing` package with hand-rolled
mocks (no gomock, no testify beyond what stdlib already gives
you). Tests live next to the code they test:

- `service_test.go` — subscribe / confirm / unsubscribe /
  get-subscriptions happy and error paths, plus the repo-format
  regex matrix and DTO conversion.
- `scanner_test.go` — new-tag emits, same-tag no-op, silent
  first-scan, empty-tag skip, rate-limit aborts cycle, bad-repo
  skip-and-continue, ctx-cancelled.
- `github/client_test.go` — the rate-limit detection matrix and
  auth-header assertion, driven by `httptest`.
- `github/cached_client_test.go` — positive/negative caching,
  rate-limit responses not cached.
- `api/handler_test.go` — each endpoint end-to-end via `httptest`
  with a fake service.
- `api/middleware_test.go` — API-key enabled / disabled / missing
  / wrong.
- `domain/schema_test.go` — DTO shape matches the swagger keys
  exactly, so `UnsubscribeToken` can't accidentally leak.

Integration tests against a real Postgres + Redis would be a
worthwhile next step; the current interface mocks are deliberate
unit-level coverage.

---

## What's intentionally not here

A handful of bonus items from the spec I didn't implement:

- **Prometheus `/metrics`.** Trivial to add (`promhttp.Handler`
  behind a middleware) but without a scraping target there's
  nowhere for the data to go. Env vars are reserved.
- **Deployment.** No hosting wired up. `docker-compose.yml` gets
  you the full stack locally.
- **Structured logging (`slog`).** The service uses `log.Printf`.
  Worth migrating if this ever went into an environment with a
  log aggregator; for now it's noise for no gain.

And one item I *did* choose to leave as `AutoMigrate`: real
production would want goose or atlas, with reviewable up/down
migrations. At the scope of this assignment, `AutoMigrate` +
a raw-SQL partial-index step is enough.
