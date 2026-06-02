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
   returns 409 if found. Unsubscribing hard-deletes the row, so a
   user who left can always re-subscribe.
3. Calls `GET /repos/{owner}/{repo}` on the GitHub API. A 404
   bubbles up as a 404; a 429 or 403 with `X-RateLimit-Remaining: 0`
   / `Retry-After` becomes a 503.
4. Generates a UUID v4 confirmation token plus a separate
   UUID v4 unsubscribe token (stored on the subscription row
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
unsubscribe token and deletes it. `POST /api/unsubscribe/:token`
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
created_at, updated_at`

`confirmation_tokens`:
`id, token, subscription_id (FK, ON DELETE CASCADE), created_at`

**Unique index on `(email, repo)`.** Subscriptions are hard-deleted
on unsubscribe, so a plain unique index is enough — once the row is
gone the same `(email, repo)` pair can be inserted again. No tombstone
rows, no partial index, no `deleted_at` filtering on reads.

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

## Observability: logs in Elasticsearch + Kibana

The app emits structured `slog` JSON to stdout (`LOG_FORMAT=json`). A **Filebeat**
sidecar tails the app container's Docker log files, parses the JSON, and ships it
to **Elasticsearch**; **Kibana** provides search and aggregation. The app stays
decoupled — no ES client, no ES dependency. See ADR-010 for the pipeline decision
(and why the push-driver alternative was reverted).

The stack lives in its own compose file. One command starts **all containers** —
app, Postgres, Redis, Elasticsearch, Kibana, Filebeat, Prometheus and Grafana —
in a single shared-network project:

```
docker compose -f docker-compose.yml -f docker-compose.observability.yml up --build -d
```

Tear everything down with `... down` (add `-v` to also wipe Elasticsearch data).

- App: <http://localhost:8080> (or your `PORT`)
- Elasticsearch: <http://localhost:9200>
- Kibana: <http://localhost:5601>

Local/dev posture only: ES security and TLS are disabled — do not expose.

### See the logs

1. Generate activity (subscribe, or wait for a scanner tick).
2. Confirm the daily index exists:

   ```
   curl localhost:9200/_cat/indices/repo-release-notifier-*
   ```

3. Create the Kibana **data view** (one-time; Kibana needs it before it
   can show anything). In Kibana → **Stack Management → Data Views →
   Create data view**, set the name/pattern to `repo-release-notifier-*`
   and the time field to `@timestamp`.

   Or script it against the Kibana API:

   ```
   curl -X POST localhost:5601/api/data_views/data_view \
     -H 'kbn-xsrf: true' -H 'Content-Type: application/json' \
     -d '{"data_view":{"title":"repo-release-notifier-*","timeFieldName":"@timestamp"}}'
   ```

4. Open **Discover** — slog lines appear with `level`, `msg`, `container.name`
   (and attrs like `component`/`repo`) as fields, timed by `@timestamp`; Gin
   access lines arrive as raw text in `message`. Aggregate (e.g. count by
   `level.keyword` over time) to confirm search works.

The same overlay also starts **Prometheus** (scraping `/metrics`) and **Grafana**
with a provisioned RED dashboard at <http://localhost:3000> — see ADR-011.

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
