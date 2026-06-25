# RelEasely

> Ship happens. We'll tell you.

A small Go service that lets people subscribe (by email) to GitHub
release notifications for a given repository. Users confirm via a
link in the confirmation email; a background scanner polls GitHub
and sends a notification whenever the latest tag for a repo changes.

A modular core (`cmd/app`) plus an extracted **notifier** microservice
(`cmd/notifier`), connected asynchronously by a **NATS + JetStream** broker.
PostgreSQL for storage, optional Redis for caching GitHub API responses,
SMTP for email.

---

## Running

Live deployment: <https://repo-release-notifier.vercel.app>

The expected path is `docker compose up --build`. That brings up
NATS, Redis, three Postgres instances, and the four services
(orchestrator, subscription, catalog, notifier); migrations run on
startup.

Locally, once `.env` is populated (`cp .env.example .env` and fill
in SMTP credentials at minimum):

```
go run ./cmd/orchestrator  # saga coordinator: POST /subscribe
go run ./cmd/app           # subscription: confirm/unsubscribe API + saga participant
go run ./cmd/catalog       # repo registry + release scanner + saga participant
go run ./cmd/notifier      # NATS consumer → SMTP
go test ./... -race
golangci-lint run ./...
```

Only `DATABASE_URL` (or the split `DB_*` vars) and the SMTP
credentials are strictly required. `REDIS_URL`, `GITHUB_TOKEN` and
`API_KEY` are optional but anything resembling production should
set all three — see the notes below for why.

---

## Architecture

**Four services from one Go module**, coordinated over a **NATS + JetStream** broker; each
stateful service owns its **own Postgres**. Full topology, diagrams, and the saga flow:
[docs/microservices.md](docs/microservices.md). Decisions:
[ADR-012](docs/adr/012-notifier-service-boundary.md) (boundary),
[ADR-013](docs/adr/013-message-broker-nats-jetstream.md) (broker),
[ADR-014](docs/adr/014-cross-service-transaction-strategy.md) (the subscribe saga).

- **orchestrator** (`cmd/orchestrator`) — owns the subscribe **saga** (a `saga_log`, no
  business data) and the public `POST /subscribe`.
- **subscription** (`cmd/app`) — `subscriptions` + tokens, email composition, and the
  confirm / unsubscribe / list API. Saga participant (`create`, the pivot).
- **catalog** (`cmd/catalog`) — the watched-repo registry + the release scanner + GitHub
  client/cache. Saga participant (`register` / `release`).
- **notifier** (`cmd/notifier`) — a stateless JetStream consumer that delivers pre-rendered
  emails over SMTP.

Packages follow the same shape:

```
cmd/{orchestrator,app,catalog,notifier}/main.go   one composition root each

internal/
  orchestrator/        saga coordinator, saga_log, NATS participant clients
  app/                 subscription service
    api/ service/      HTTP handlers + business logic + email composer (templates)
    repository/ db/    GORM queries, Postgres connection + migrations
    saga/              subscription.create handler (the saga pivot)
    releaseconsumer/ confirmationconsumer/   release fan-out + confirmation email
  catalog/             register/release handlers, scanner, github, cache, repository
  notifier/            stateless SMTP sender + JetStream consumer
  shared/
    saga/              saga command / reply / event contract + subjects
    notify/            EmailCommand wire contract
    natsbus/           NATS connect, streams, request-reply + consumer helpers
    config/ observability/   env helpers, structured slog logging
```

The layering is strict:

```
Handler  →  Service  →  Repository  →  DB
             ↘             ↘
              GitHub        Publisher → NATS → Notifier → SMTP
```

Handlers parse HTTP and map domain errors to status codes; they
never touch GORM or know anything about SMTP. The service never
returns HTTP responses or takes a `gin.Context`. Interfaces are
declared at the consumer — `service.SubscriptionRepository`,
`scanner.Repository`, `service.ConfirmationSender` and friends are
defined where they're used, and the implementing packages satisfy
them structurally. No DI framework.

Domain types live in a leaf `internal/app/domain` package that imports
nothing else, so there are no import cycles. Each binary's
`cmd/*/main.go` is the only place that wires its graph together.

---

## Key flows

### Subscribe

`POST /subscribe` (on the **orchestrator**) takes `{email, repo}` and runs an **orchestrated
saga** across the catalog + subscription services (rationale: [ADR-014](docs/adr/014-cross-service-transaction-strategy.md)):

1. **Register** (catalog, compensatable) — validate the repo on GitHub (404 → 404; a
   rate-limit → 503) and register it for watching. Fail-fast: the step most likely to fail,
   so nothing downstream needs undoing if it does.
2. **Create** (subscription, the *pivot*) — insert the subscription (`confirmed = false`) +
   a confirmation token in one local transaction. If it fails (already subscribed), the
   orchestrator **compensates** by releasing the registration.
3. **Confirmation email** (terminal, only after the saga commits) — the orchestrator emits a
   `confirmation.requested` event; the subscription service renders it and the notifier
   delivers it. An email is irreversible, so it lives strictly *after* the pivot; on a crash
   the `saga_log` rolls forward (re-publish, deduplicated), never back.

The orchestrator records every step in a `saga_log`, so a crash recovers deterministically:
compensate before the pivot, roll forward after it.

### Confirm / Unsubscribe

`GET /api/confirm/:token` looks up the confirmation token, sets
`confirmed = true` on the associated subscription, and deletes the
token row. One-shot.

`GET /api/unsubscribe/:token` looks the subscription up by its
unsubscribe token and deletes it. `POST /api/unsubscribe/:token`
is the same handler — exposed as POST too so the one-click
unsubscribe header in outgoing mail works natively in Gmail and
Apple Mail.

### Scanner

Runs in the **catalog** service as a goroutine fed the process context. Each tick:

1. List the repos with at least one registration (`ActiveRepos`) — one GitHub call per repo
   per cycle, not one per subscription.
2. For each repo: fetch `releases/latest`. If the tag matches the stored `last_seen_tag`,
   skip. Otherwise advance the cursor and **publish one `release.detected` event**. The
   subscription service (which owns the subscriber list) consumes it and fans out one
   `notify.release` per recipient — the scanner is the detector, not the address book.
3. If GitHub returns rate-limited, abort the cycle rather than burning further budget. Next
   tick retries.
4. `checkRepo` is wrapped in `safeCheckRepo`, which recovers panics — one bad repo never
   kills the goroutine.

### Silent first scan

A newly registered repo's cursor (`watched_repos.last_seen_tag`) starts
`""`. The first scan records the current tag but does **not** publish — a
new subscriber shouldn't immediately get a notification for a release
that happened a year ago. From the second scan onwards, a change fires.

### Late unsubscribe (accepted)

Release recipients are resolved when the subscription service handles
`release.detected`, so a user who unsubscribes in the brief window before
send may still receive that one release email. This is deliberate — the same tolerance
every email system has ("allow a few days for changes to take effect"),
not a bug. See [ADR-013](docs/adr/013-message-broker-nats-jetstream.md).

---

## Data model

One database **per stateful service**.

**subscription** — `subscriptions (id, public_id, email, repo, confirmed, unsubscribe_token,
created_at, updated_at)` + `confirmation_tokens (id, token, subscription_id FK ON DELETE
CASCADE, created_at)`. `public_id` is the UUID the orchestrator mints as the cross-service
identity. Unique indexes on `(email, repo)` and `public_id`; subscriptions are hard-deleted
on unsubscribe, so a plain unique index suffices.

**catalog** — `watched_repos (repo PK, last_seen_tag)` (the scan cursor) +
`repo_registrations (subscription_id PK, repo, FK → watched_repos)` (a repo is polled while
it has ≥ 1 registration).

**orchestrator** — `saga_log (saga_id PK, state, subscription_id, payload, …)`, the durable
record that drives recovery.

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

`GET /` on the **orchestrator** serves a minimal form from
`internal/orchestrator/api/subscribe.html` (embedded via `//go:embed`).
It `fetch`es `POST /subscribe` same-origin — the orchestrator owns the
subscribe saga, so the form lives with its endpoint — with a JSON body,
and shows inline success / error messages.

The subscription service emits only emails now; they're rendered from
templates under `internal/app/templates/emails/` via `RenderEmail`.

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

`GET /api/subscriptions` (on the subscription service) sits behind an
`X-API-Key` header check when `API_KEY` is set. Subscribe is public —
the confirmation email is the double opt-in — and confirm and
unsubscribe stay open because they're opened from mail clients,
which can't attach request headers — the token in the URL is the
capability for those routes (UUID v4, 122 bits of entropy).

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

(That's about the *public* API. The internal app→notifier hop was
previously gRPC and is now an async **NATS + JetStream** broker — see
[ADR-013](docs/adr/013-message-broker-nats-jetstream.md).)

---

## Constant-time token comparison

Tokens are UUID v4 — 36 characters, 122 bits of entropy. A timing
attack against an indexed `WHERE token = ?` lookup at that amount
of entropy is still not reachable in practice (2^122 search space),
and any sane deployment rate-limits the endpoint anyway.

Switching to `subtle.ConstantTimeCompare` would force a full
table scan on every confirm / unsubscribe request (or a
constant-time-equal subquery per row). Real performance cost for
a theoretical attack — wrong trade at this entropy level. ADR-005
flags it as defense-in-depth Future Work; if it ever lands, it
will likely be paired with shorter tokens or another reason to
walk the rows.

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

**No graceful Redis close.** Decision: rely on OS socket reclaim
on process exit. The only Redis operations are short `Get`/`SetEx`
calls on a 10-minute TTL cache; a connection dropped mid-write
costs at most one extra GitHub call from the next caller. Doing
this properly would require a scanner-goroutine `WaitGroup` join
plus ordered teardown — ~15 lines of code for operational polish
(cleaner deploy logs), not correctness. Revisit if Redis-side
"connection reset" warnings start cluttering deploy dashboards.

---

## Testing

Three tiers, gated by Go build tags and orchestrated via the
`Makefile`:

- **Unit** (`make test-unit` → `go test ./... -race`) — no
  containers. Collaborators are faked with `uber-go/mock` mocks
  (regenerate with `make generate-mocks`); assertions use `testify`.
  Tests live next to the code they cover:
  - `service` — subscribe / confirm / unsubscribe /
    get-subscriptions happy and error paths, plus the repo-format
    regex matrix and DTO conversion.
  - `scanner` — new-tag emits, same-tag no-op, silent first-scan,
    empty-tag skip, rate-limit aborts cycle, bad-repo
    skip-and-continue, ctx-cancelled.
  - `github` — rate-limit detection matrix and auth-header
    assertion via `httptest`; positive/negative caching with
    rate-limit responses never cached.
  - `api` — each endpoint end-to-end via `httptest` with a mocked
    service; API-key enabled / disabled / missing / wrong.
  - `domain` — DTO shape matches the swagger keys exactly, so
    `UnsubscribeToken` can't accidentally leak.
- **Integration** (`make test-integration`, build tag
  `integration`, in `tests/integration/`) — `testcontainers-go`
  boots a real Postgres and exercises the repository layer and
  migrations against the actual schema.
- **E2E** (`make test-e2e`, build tag `e2e`, in `tests/e2e/`) —
  `testcontainers-go` boots Postgres + Mailpit + a headless
  Chromium sidecar driven over CDP; the app runs in-process and
  the full subscribe → confirm → notify flow is driven through the
  browser and asserted against captured mail. Requires a Docker
  daemon.

See `docs/testing/` for per-suite detail.

---

## Observability: logs + metrics

The app emits structured `slog` JSON to stdout (`LOG_FORMAT=json`). A **Filebeat**
sidecar tails the app's Docker logs, parses the JSON, and ships it to
**Elasticsearch**; **Kibana** searches and aggregates. The app stays decoupled — no
ES client. **Prometheus** scrapes `/metrics` and **Grafana** serves a provisioned RED
dashboard. See ADR-009/010/011 for the decisions (including the reverted push driver).

The whole stack lives in an overlay compose file; one command brings it up:

```
docker compose -f docker-compose.yml -f docker-compose.observability.yml up --build -d
```

`... down` tears it down (`-v` also drops every data volume, Postgres included).

- App <http://localhost:8080> · Elasticsearch <http://localhost:9200> ·
  Kibana <http://localhost:5601> · Grafana <http://localhost:3000>

Local/dev posture only: ES security and TLS are disabled — do not expose.

### See the logs in Kibana

Generate activity (subscribe, or wait for a scanner tick), then create the data view
once — Kibana needs it before Discover shows anything:

```
curl -X POST localhost:5601/api/data_views/data_view \
  -H 'kbn-xsrf: true' -H 'Content-Type: application/json' \
  -d '{"data_view":{"title":"repo-release-notifier-*","timeFieldName":"@timestamp"}}'
```

(Or Kibana → **Stack Management → Data Views → Create**, pattern
`repo-release-notifier-*`, time field `@timestamp`.) Open **Discover** — every line is
structured slog JSON: `level`, `msg`, `container.name`, plus attrs like `route`,
`status`, `duration_ms` (HTTP access logs) or `repo`, `err` (app events).

---

## What's intentionally not here

Bonus items from the spec I didn't implement:

- **Deployment.** No hosting wired up. `docker-compose.yml` gets
  you the full stack locally.

Schema changes go through versioned, forward-only SQL migrations
under `internal/db/migrations/` (golang-migrate), applied on
startup. The baseline migration is idempotent (`IF NOT EXISTS`),
so it cleanly adopts databases originally provisioned by GORM's
`AutoMigrate`.
