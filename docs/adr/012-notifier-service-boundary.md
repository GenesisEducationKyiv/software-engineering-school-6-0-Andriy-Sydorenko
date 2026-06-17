# ADR-012: Notifier Service Boundary

## Status

Accepted (boundary). **Transport superseded by [ADR-013](013-message-broker-nats-jetstream.md)** — the app↔notifier hop is now NATS + JetStream, not gRPC. The two-binary boundary and stateless-notifier rationale below still hold; the gRPC transport, interceptors, and benchmark described here have been removed. Partially supersedes ADR-003 — notifier domain only; its reasoning still holds for the API + scanner.

---

## Context

ADR-003 chose a single-binary modular monolith: no component justified the RPC/deploy overhead of separation. HW7 revisits that with a concrete "extract at least one domain" requirement.

The notifier (SMTP email sending) is the one clean extraction candidate:

- **Narrow contract** — its whole surface is "send one rendered email"; no shared schema with the core.
- **Independent failure domain** — an SMTP outage must not affect subscribe/confirm availability.
- **I/O-bound, independently scalable** — email volume can spike without touching scanner or API throughput.

Subscription and scanner, by contrast, share the Postgres schema and run cross-domain queries in one transaction; splitting them would buy distributed transactions and cross-service joins for no benefit at this scale. They stay co-located.

---

## Decision

Modularize into `internal/app`, `internal/notifier`, `internal/shared`; ship two binaries from one module:

- `cmd/app` — HTTP API, subscription, scanner, **and email composition** (templates + `BASE_URL` links).
- `cmd/notifier` — a stateless SMTP sink exposing one gRPC RPC, `SendEmail(recipient, subject, html_body)`. The app renders the email; the notifier only delivers it. No public HTTP — just an admin server (`ADMIN_ADDR`) serving Prometheus `/metrics`.

**Transport: gRPC, not HTTP/JSON.** A benchmark (`bench/`; reproduce with `make bench` / `make bench-throughput`, on M1 Pro / loopback) ran both transports behind identical auth. Sequentially, HTTP/JSON leads on tiny payloads (1 KB: ~50 µs vs gRPC ~70 µs) but gRPC leads as the payload grows (100 KB: ~150 µs vs HTTP ~700 µs). Under concurrency the gap widens: at 64 in-flight callers gRPC sustains roughly 2–3× the throughput, because HTTP/1.1 saturates its connection pool while gRPC multiplexes over one HTTP/2 connection. For a fan-out hop, gRPC's concurrency behaviour plus a typed, versioned contract outweigh HTTP's tiny-payload edge.

**Hardened hop** — chained unary interceptors (`internal/shared/observability/grpcmw`), outermost first:

1. **Recovery** — handler panic → `codes.Internal`; the connection survives.
2. **Auth** — constant-time bearer token (`INTERNAL_API_TOKEN`, `crypto/subtle`); mounted only when the token is non-empty (empty = bypass for local/dev/e2e, matching the API-key convention). Env-injected, never logged. Placed ahead of metrics so unauthenticated calls are rejected without polluting the latency histogram.
3. **RequestID** — propagates `x-request-id`, minting one server-side when absent, so app and notifier logs correlate without a tracing system.
4. **Metrics** — RED histograms (ADR-011).

Shutdown is bounded: `GracefulStop` with an 8 s `Stop()` fallback so a hung RPC can't block exit. `net/smtp` itself can't be cancelled, so the mailer runs the blocking send in a goroutine and returns on context expiry — per-call deadlines and the shutdown budget free the handler, while the orphaned send drains on its own TCP timeout. Delivery is at-most-once regardless (ADR-006).

---

## Consequences

**Positive**

- The notifier deploys and scales independently; an SMTP problem or notifier restart can't take down subscribe/confirm.
- The `SendEmail` contract is typed and versioned (protobuf); breaking changes fail at compile time.
- `x-request-id` correlation spans the hop; Prometheus scrapes both services, Filebeat ships both log streams.
- The transport choice is benchmark-grounded, not asserted.

**Negative**

- A function call becomes a network hop: new latency, new failure modes (dial, reset), a `codes.*` surface to handle.
- `INTERNAL_API_TOKEN` is a shared secret to provision, rotate, and keep out of logs.
- Two services to run locally instead of one (compose covers it, but the cognitive surface grows). The app dials the notifier lazily, so a notifier outage surfaces as failed sends, not a startup gate.
- **Delivery is at-most-once** — no transactional outbox between app and notifier; a send that fails after the subscription commit is lost. Known gap (ADR-006), revisit if guarantees harden.

---

## Alternatives Considered

- **Full three-service split (API + scanner + notifier)** — rejected: subscription and scanner share the schema, so splitting them needs distributed transactions / cross-service joins. Real cost, no benefit at this scale — the same reasoning ADR-003 gave, still valid for those two domains.
- **HTTP/JSON for the hop** — rejected: untyped contract, manual JSON error parsing, and 2–3× lower concurrency throughput per the benchmark; the tiny-payload latency edge doesn't apply to email.
- **Async delivery via a broker (NATS/RabbitMQ)** — rejected: the notifier is a synchronous leaf; a broker adds infra for no current need. Revisit if at-most-once becomes unacceptable.

---

## References

- ADR-003 — Service Decomposition (partially superseded here — notifier only)
- ADR-006 — Transactional Outbox (the delivery gap) · ADR-009 — Structured Logging · ADR-011 — RED Metrics
- [`docs/microservices.md`](../microservices.md) — topology, contract, runtime flows
- `bench/` — HTTP vs gRPC benchmark · `internal/shared/observability/grpcmw/` — interceptors
