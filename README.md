# repo-release-notifier

A small Go system that lets people subscribe (by email) to GitHub release
notifications for a given repository. Users confirm via a link in the
confirmation email; a background scanner polls GitHub and sends a notification
whenever the latest tag for a repo changes.

**Two services** (HW7): a **core** (`cmd/server`) hosting the user-facing HTTP
API + the subscription and scanner modules + the poll loop, and an extracted
**notifier** microservice (`cmd/notifier`) that renders and sends email over a
gRPC boundary. PostgreSQL for storage, optional Redis for caching GitHub
responses, SMTP for email.

See **[docs/architecture/microservices.md](docs/architecture/microservices.md)**
for the module/service boundaries, public APIs, the boundary diagram, data
ownership, and the HTTP-vs-gRPC benchmark — and
**[ADR-012](docs/adr/012-modular-architecture-and-notifier-extraction.md)** for
the decision record.

---

## Running

The expected path is Docker Compose, which brings up Postgres, Redis, the core
(`app`), and the internal notifier service together; migrations run on startup.

```bash
cp .env.example .env        # fill in SMTP credentials at minimum
docker compose up -d --build
curl -fs localhost:8080/health
```

- Core (user HTTP): <http://localhost:8080>
- Notifier: **internal only** — gRPC `:50051` + admin HTTP `:8081` on the
  compose network, **not** published to the host. The core dials it at
  `notifier:50051` with the shared `INTERNAL_API_TOKEN`.

Run both binaries locally without Docker (two terminals):
```bash
# terminal 1 — notifier (serves gRPC :50051 + admin :8081)
INTERNAL_API_TOKEN=dev NOTIFIER_GRPC_ADDR=:50051 ADMIN_ADDR=:8081 \
  BASE_URL=http://localhost:8080 SMTP_HOST=... SMTP_USERNAME=... SMTP_PASSWORD=... \
  go run ./cmd/notifier

# terminal 2 — core (dials the notifier on loopback)
INTERNAL_API_TOKEN=dev NOTIFIER_ADDR=127.0.0.1:50051 \
  go run ./cmd/server
```

Tests + lint:
```bash
make test-unit          # go test ./... -race
make test-integration   # testcontainers Postgres
make test-e2e           # testcontainers Postgres + Mailpit + Chromium; real gRPC hop
golangci-lint run ./...
```

Only the DB vars (`DATABASE_URL` or the split `DB_*`) and — on the **notifier** —
the SMTP credentials are strictly required. `REDIS_URL` and `GITHUB_TOKEN` are
optional; production should set both.

---

## Architecture

Two binaries, three modules, one network boundary (core ↔ notifier).

- **core** (`cmd/server`, compose service `app`):
  - **subscription module** — owns `subscriptions` + `confirmation_tokens`;
    user-facing HTTP/JSON; public API `Subscribe/Confirm/Unsubscribe/
    ListConfirmedRepos/ListConfirmedSubscribers`.
  - **scanner module** — owns `watched_repo`; the poll loop + GitHub client;
    public API `ValidateRepo`.
  - The two modules call each other **only** through their Go-interface public
    APIs (depguard-enforced) — never into each other's internals.
- **notifier** (`cmd/notifier`, compose service `notifier`): stateless
  render+send, reached over **gRPC** (`SendConfirmation`,
  `SendReleaseNotifications`). SMTP lives here, not in the core.

The core dials the notifier with a shared bearer token (`INTERNAL_API_TOKEN`,
constant-time verified). A correlation ID propagates across the hop so a single
request's logs join across both services. Full detail + diagram:
[docs/architecture/microservices.md](docs/architecture/microservices.md).

Package layout:

```
cmd/server/main.go           core composition root (subscription + scanner + dial notifier)
cmd/notifier/main.go         notifier service composition root (gRPC + admin)

proto/                       notifier.proto contract + generated stubs (notifierpb)

internal/
  domain/                    GORM models, DTOs, sentinel errors (leaf, no deps)
  config/                    core env loader
  db/                        Postgres connection, migrations
  api/                       core HTTP handlers, middleware, router, pages
  subscription/              subscription module — service + public API + repository
  scanner/                   scanner module — poll loop + ValidateRepo + watched_repo
  notifier/                  notifier service-core (Core), gRPC server, SMTP, email templates
  notifierclient/            core-side gRPC client adapter (the modules' notifier port)
  platform/                  shared gRPC bootstrap, interceptors, admin server, env helpers
  observability/             correlation ID, context-aware slog, HTTP + gRPC metrics
  github/                    GitHub client + optional Redis-cached wrapper
  cache/                     thin Redis wrapper
  templates/                 embedded HTML (public pages)
bench/                       HTTP-vs-gRPC benchmark (star task)
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
declared at the consumer — `subscription.SubscriptionRepository`,
`scanner.WatchedRepoStore`, `subscription.ConfirmationSender` and friends are
defined where they're used, and the implementing packages satisfy
them structurally. No DI framework.

Domain types live in a leaf `internal/domain` package that imports
nothing else, so there are no import cycles. `cmd/server/main.go`
is the only place that wires the whole graph together.

---

## Key flows

### Subscribe

`POST /api/subscribe` and `GET /api/subscriptions` are public (unauthenticated)
by design — there is no account model. Both are per-IP rate-limited (token
bucket: burst 5, 1 req/s) so a single source can't bomb arbitrary inboxes with
confirmation mail or rapidly enumerate subscriptions. Double opt-in is the
second line: an unconfirmed subscription never receives release mail.

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
with a JSON body and shows inline success / error messages.

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

## HTTP API vs gRPC benchmark (star task)

The core↔notifier boundary runs on **gRPC** in production. To satisfy the star
task — "implement the inter-service hop over both HTTP API and gRPC and compare"
— `bench/` stands up, in-process over loopback, a real gRPC server and a real
idiomatic HTTP/JSON server, **both wrapping the same notifier core**, and
measures both on the same workloads:

- `SendReleaseNotifications` at N = 1 / 100 / 1 000 / 10 000 recipients
  (payload/serialization scaling).
- `SendConfirmation` (per-call overhead).

Run it:

```bash
make bench   # go test -bench=. -benchmem -run='^$' ./bench/...
```

Result (measured): the comparison is two-sided — for tiny payloads HTTP/JSON is
marginally *faster* on loopback, gRPC overtakes around N≈100 and leads modestly
on larger batches, and Protobuf is consistently ~1.5× smaller on the wire. At
this app's scale the delta is small, so the real reasons we run gRPC are typed
contracts + tooling + HW8-readiness, not raw throughput. **Full numbers +
methodology: [`bench/README.md`](bench/README.md).**

---

## Environment variables

`.env.example` is the source of truth; copy it to `.env`. Vars are owned by the
service that reads them.

| Variable | Service | Default | Purpose |
|---|---|---|---|
| `DATABASE_URL` / `DB_*` | core | split `DB_*` | Postgres connection (URL wins if set) |
| `REDIS_URL` | core | empty (disabled) | cache GitHub responses 10m |
| `PORT` | core | `8080` | user-facing HTTP port |
| `GITHUB_TOKEN` | core | empty | lifts GitHub rate limit (60→5000 req/hr) |
| `SCAN_INTERVAL` | core | `5m` | poll cadence |
| `NOTIFIER_ADDR` | core | `notifier:50051` | gRPC dial target for the notifier |
| `INTERNAL_API_TOKEN` | **both** | `dev-internal-token` | shared bearer secret on the gRPC hop (must match) |
| `NOTIFIER_GRPC_ADDR` | notifier | `:50051` | gRPC listen addr (internal only) |
| `ADMIN_ADDR` | notifier | `:8081` | admin HTTP (`/health` + `/metrics`) |
| `BASE_URL` | core | `http://localhost:8080` | base for email links (core renders confirm/unsubscribe URLs; the notifier no longer knows it) |
| `SMTP_HOST` / `SMTP_PORT` / `SMTP_USERNAME` / `SMTP_PASSWORD` | notifier | — | email transport (required on the notifier) |
| `LOG_LEVEL` / `LOG_FORMAT` | both | `info` / `text` | logging |

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
    service (handlers, routing, domain-error → status mapping).
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
