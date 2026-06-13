# ADR-012: Modular Architecture and Notifier Extraction

## Status

Accepted — supersedes [ADR-002](002-release-detection-strategy.md) (the on-entity
`last_seen_tag` strategy) and [ADR-003](003-service-decomposition.md) (single-
process modular monolith).

---

## Context

HW7 requires a **modular architecture** with all inter-module communication
routed through explicit **public APIs**, **and** at least one domain extracted
into a separate **microservice**. The starred task adds: implement the inter-
service hop over **both HTTP API and gRPC**, benchmark them, and write up the
result.

The app as of ADR-003 is a single binary (`cmd/server`): a Gin HTTP API over
layered service/repository code, an in-process scanner polling GitHub every 5
minutes, and an email path (composer + mailer). ADR-003 chose a modular monolith
and ADR-002 stored `last_seen_tag` on each subscription row. Both decisions were
correct for their constraints; HW7 changes the constraints:

- ADR-003's "no internal RPC, no second deploy unit" directly conflicts with
  HW7's "extract at least one domain into a microservice."
- ADR-002's per-subscription `last_seen_tag` couples release-detection state to
  the subscription table — exactly the cross-module data ownership the modular
  split must untangle. Detection state belongs to the scanner, not to *who
  subscribed*.

A pure modular monolith (zero extracted services) is **non-compliant**. A full
three-service split (subscription + scanner + notifier) is disproportionate for
a two-table, low-RPS, 5-minute-poll app — it adds a second Postgres, a
bidirectional cross-process dependency, and a deeper test matrix for isolation
benefits that don't bite at this scale.

---

## Decision

**Modularize all three domains internally; extract exactly one — the notifier —
as a microservice.**

1. **Three bounded modules**, each with an explicit Go-interface public API; all
   inter-module communication routes through those APIs (depguard-enforced):
   - **subscription** — owns `subscriptions`, `confirmation_tokens`; exposes
     `Subscribe`, `Confirm`, `Unsubscribe`, `ListConfirmedRepos`,
     `ListConfirmedSubscribers`.
   - **scanner** — owns `watched_repo`; exposes `ValidateRepo` + the poll loop.
   - **notifier** — stateless render+send.

2. **Extract the notifier** into its own binary (`cmd/notifier`) and container,
   reached over **gRPC** at runtime. `proto/notifier.proto` is the single source
   of truth for the contract (`SendConfirmation`, `SendReleaseNotifications`).
   The notifier service-core is transport-agnostic.

3. **Move release-detection state to the scanner.** `last_seen_tag` becomes
   per-repo in a new `watched_repo` table (additive migration
   `0003_create_watched_repo`); the scanner reads/writes it. This supersedes
   ADR-002's on-entity strategy. The dead `subscriptions.last_seen_tag` column
   is dropped in a later flagged destructive migration.

4. **gRPC at runtime; HTTP only for the benchmark.** `cmd/server` wires the
   gRPC client exclusively. An idiomatic HTTP/JSON server lives only in `bench/`
   to satisfy the star task's "communication over both HTTP API and gRPC" and to
   produce the comparison.

5. **Internal authentication via a shared bearer token** (`INTERNAL_API_TOKEN`)
   on the core↔notifier hop, verified constant-time by a gRPC server
   interceptor. AuthZ only; no transit encryption (mTLS/mesh is the documented
   production upgrade path).

6. **Deployment:** one multi-stage Dockerfile builds both binaries into one
   image; compose runs it as two services (`app` = core, `notifier`) via
   distinct `command:`. The notifier publishes no host port. Each service
   exposes `/health` + `/metrics`.

---

## Consequences

### Positive

- **HW7-compliant:** clean module boundaries with public APIs + one real
  extracted microservice with a real network boundary.
- **Correct data ownership:** release-detection state lives with the scanner,
  not the subscription table; modules no longer reach into each other's tables.
- **A real benchmark surface:** the core↔notifier boundary is a genuine network
  hop, so the HTTP-vs-gRPC comparison measures something real.
- **HW8-ready:** the notifier is already a separate service, so adding a broker
  (scanner publishes `ReleaseDetected`, notifier consumes) is a pure addition,
  not a re-extraction.
- **No duplicated build/ops:** one image, one Dockerfile, one observability
  stack widened to both services.

### Negative

- **A network hop now exists** where there was an in-process call: per-call
  deadlines, an auth token to manage, and a second container to run and observe.
- **Cross-process failure modes:** a notifier outage degrades sends; the scan
  cycle must log-and-continue. (Bounded; no DLQ until HW8's broker.)
- **Behavior change:** a newly-confirmed subscriber no longer receives the
  *current* latest release on confirm — only releases detected afterward (the
  cost of moving `last_seen_tag` to per-repo scanner state).
- **Deeper local stack:** developers run two services + Postgres + Redis +
  Mailpit instead of one binary.

---

## Alternatives Considered

### Option 1: Pure modular monolith, zero extracted services

Rejected — **non-compliant** with HW7's "extract at least one domain into a
microservice." Modules-only leaves no network boundary to benchmark.

### Option 2: Full three-service split (subscription + scanner + notifier)

Rejected for now — exceeds "at least one." It adds a second Postgres (or a shared
DB that re-couples the services), a bidirectional cross-process dependency
(`ValidateRepo` inbound to the scanner), distributed tracing across three hops,
and three deploy pipelines — for isolation benefits that don't bite at this
scale (5-minute poll, a handful of repos, I/O-bound). The clean scanner **module**
boundary keeps a future extraction mechanical.

### Option 3: Extract the scanner instead of the notifier

Rejected — the scanner is stateful (its DB would have to be extracted too), has
inbound dependencies (`ValidateRepo` on the subscribe path), and a bidirectional
call pattern. The notifier is stateless, a pure sink, and is exactly the future
async (HW8) consumer — the safest, highest-leverage extraction.

### Option 4: HTTP as a permanent dual runtime transport (a `TRANSPORT` switch)

Rejected — HTTP earns only enough implementation to be benchmarked. Maintaining
it as a parallel production transport is dead weight; gRPC is the one runtime
transport.

---

## Rollout Plan

Strangler by implementation order — each phase is one reviewable PR; build +
tests stay green throughout:

1. **Foundations** — `proto/`, `internal/platform` (gRPC bootstrap +
   interceptors + admin server), `internal/observability` (correlation +
   context-aware slog + gRPC metrics). Additive; monolith untouched.
2. **Modularize in-place** — carve `internal/subscription` + `internal/scanner`
   with public-API interfaces; add `watched_repo` (`0003`); depguard rule.
3. **Extract notifier-svc** — `internal/notifier` core + gRPC server +
   `cmd/notifier`; core switches its two send call-sites to the gRPC client.
4. **Benchmark** — `bench/` HTTP + gRPC over the same notifier core.
5. **Infra + docs** — one Dockerfile → two compose services; observability
   widened; e2e drives the flow across the gRPC boundary; this ADR + the tracked
   architecture doc.

Forward-only. A later flagged destructive migration drops
`subscriptions.last_seen_tag` once the scanner is fully on `watched_repo`.

---

## Operational Impact

- **Deployments:** two containers from one image (distinct `command:`); compose
  gates the core on the notifier being healthy.
- **Monitoring:** Prometheus scrapes both `/metrics` (jobs `core`, `notifier`);
  Filebeat ships both log streams to Elasticsearch; Grafana RED panels split by
  a `service` label.
- **Debugging:** a correlation ID propagates across the gRPC hop, so a single
  request's logs join across both services.
- **CI:** the image build compiles both binaries; e2e exercises the real gRPC
  hop in-process.
- **Cost:** one extra small stateless container locally; negligible.

---

## Security Considerations

- **Internal authZ:** the core↔notifier hop requires a shared bearer token
  (`INTERNAL_API_TOKEN`), constant-time verified, gating the PII-bearing,
  email-triggering `Send*` calls against lateral movement on the internal
  network. The token is never logged.
- **No transit encryption** on internal calls (plaintext gRPC) — accepted for a
  homework/local posture; mTLS/mesh is the documented production upgrade path.
- **Reduced public surface:** the notifier publishes no host port; only the
  core's user HTTP is exposed.

---

## Performance Considerations

The added network hop introduces per-call latency vs the prior in-process call,
bounded by a context deadline. The HTTP-vs-gRPC benchmark (star task) measures
the boundary at N = 1/100/1k/10k recipients and on the tiny `SendConfirmation`
call. **Measured (Apple M1 Pro / Go 1.26.2):** the result is two-sided — for
tiny payloads HTTP is marginally *faster* (loopback per-call overhead favors it;
gRPC's HTTP/2 framing + interceptor chain cost more), gRPC overtakes around
N ≈ 100 and leads modestly (~3–12%) on larger batches, and Protobuf is
consistently ~1.5× smaller on the wire at every size. At this app's scale the
delta is small either way; the decision driver for gRPC is typed contracts +
tooling + HW8-readiness, not raw throughput. Numbers and methodology:
`bench/README.md`.

---

## References

- [ADR-002](002-release-detection-strategy.md) — Release Detection Strategy (superseded by this ADR for the `last_seen_tag` location)
- [ADR-003](003-service-decomposition.md) — Service Decomposition (superseded by this ADR for process topology)
- [ADR-006](006-transactional-outbox.md) — Transactional Outbox (HW8 forward path)
- [ADR-007](007-internal-layering.md) — Internal Layering & Consumer-Defined Interfaces
- [ADR-011](011-red-metrics-and-prometheus-grafana.md) — RED Metrics & Prometheus/Grafana
- Tracked architecture doc: [`../architecture/microservices.md`](../architecture/microservices.md)
- Benchmark: [`../../bench/README.md`](../../bench/README.md)
- `proto/notifier.proto` — the cross-process contract
