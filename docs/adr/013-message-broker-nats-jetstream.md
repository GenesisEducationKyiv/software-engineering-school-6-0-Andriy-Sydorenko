# ADR-013: Message Broker — NATS + JetStream

## Status

Accepted. **Supersedes the transport decision in [ADR-012](012-notifier-service-boundary.md)** (the synchronous app↔notifier RPC). ADR-012's service *boundary* still holds — two services, the notifier as a stateless email sink; only the link between them changes from a synchronous call to an asynchronous broker.

---

## Context

ADR-012 connected the core to the notifier with a synchronous RPC. It listed **at-most-once delivery** as a known gap (ADR-006) and named "async delivery via a broker" as the alternative to *revisit if at-most-once becomes unacceptable*. At-least-once delivery is now a requirement; this is that revisit.

The synchronous hop has real limits:
- **Lost on failure** — a send that fails (mail server down, notifier restarting) after the originating transaction commits is gone; no retry, no record.
- **Liveness coupling** — the notifier must be up *at call time*; an outage surfaces as failed sends.
- **No backpressure / buffer** — a slow mail server backs pressure straight into the caller.

Closing these requires a broker: at-least-once delivery, a durable buffer that survives a notifier outage, automatic retry, and a dead-letter path.

---

## Decision

Replace the synchronous RPC with **NATS + JetStream**. The core **publishes per-recipient, pre-rendered email messages**; the notifier is a **stateless consumer** that delivers them. The synchronous RPC transport is removed.

- **Persistent streaming** — the broker must persist messages and guarantee **at-least-once** delivery with acknowledgements and redelivery. Best-effort pub/sub would drop messages whenever the consumer is down.
- **Per-recipient messages** — one message per recipient, not one batch message carrying a recipient list. One message = one delivery = one acknowledgement, so a failed recipient retries on its own (no duplicates to the rest) and delivery is tracked per recipient.
- **Rendering stays in the core** — the message carries the already-rendered email; the notifier needs no templates or business logic and stays just a sender.
- **Stateless notifier, no database** — the broker's own per-message state (pending / acknowledged / dead-lettered) *is* the delivery ledger.
- **No backward dependency** — every runtime edge points core→notifier. The core resolves recipients *before* publishing; the notifier never calls back. Only the email-producing triggers become asynchronous; user-facing confirm/unsubscribe stay synchronous request/response.
- **Delivery semantics** — acknowledge only after a successful send; a transient failure is retried a bounded number of times, then the message is dead-lettered to a durable destination for inspection and redrive. Publish-side deduplication (keyed on each message's logical identity) absorbs producer retries.
- **Resilient consumer** — the consumer reconnects indefinitely, so a transient broker outage delays delivery rather than permanently stopping it.

---

## Consequences

### Positive

- **At-least-once with a durable buffer** — a failed send is retried, then parked in a durable dead-letter destination; a notifier outage delays delivery, it doesn't lose it. Closes the at-most-once gap (ADR-006).
- **Decoupling** — the core publishes and moves on; the notifier can be down, slow, or restarting without affecting the user-facing paths. The stream provides natural backpressure.
- **Per-recipient isolation** — one bad address retries and dead-letters on its own.
- **Simpler notifier** — a pure stateless consumer; no RPC server, interceptors, or typed-contract codegen.
- **One transport** — the broker covers async; should a synchronous need ever arise it also supports request/reply, so there's no second stack.

### Negative

- **New infrastructure** — a broker to run, monitor, and reason about (durability, retention, consumer lag).
- **At-least-once means possible duplicates** — see Accepted tradeoffs.
- **Eventual, not immediate** — the confirmation email is queued, not sent within the request.
- **Dead-letters need an operator** — persistent-failure messages park durably for triage and manual redrive (no automated redrive yet; see Operational Impact for why).

---

## Accepted tradeoffs

Documented so they are not mistaken for bugs:

- **Stale unsubscribe** — recipients are resolved at publish time, so a release email may reach a user who unsubscribed in the publish→send window.
- **Rare duplicate** — at-least-once: a crash after the mail server accepts the message but before the acknowledgement causes a resend. Durability favours send-twice over lose-it.

---

## Alternatives Considered

- **Kafka** — rejected: overkill for single-digit messages per minute; heavy to run and slow to boot, and its partitioned-log / replay model buys nothing for a simple task consumer.
- **RabbitMQ** — rejected: a capable broker, but heavier to operate than needed and with no advantage at this scale.
- **Keep the synchronous RPC alongside the broker** — rejected: in a core + stateless-sink topology a synchronous call has no honest job — core→notifier is fire-and-forget (better as an event), notifier→core is a backward dependency, a delivery-status query is obsoleted by the broker's own state, and any real synchronous need is covered by the broker's request/reply.

---

## Rollout Plan

No data migration. The two ends change together in a single cutover: stand up the broker, move the core to publish and the notifier to consume, then remove the synchronous transport. There is no live deployment to migrate.

---

## Operational Impact

- One more service to run (the broker); both the core and the notifier connect to it.
- The notifier no longer exposes a service port — only an admin endpoint for metrics.
- Stuck or failed work is visible as broker consumer backlog and the dead-letter destination. Failure runbook: inspect the dead-letter destination, fix the cause, redrive.
- Manual redrive is deliberate, not an unmanaged backlog: transient failures are retried in-band (and the consumer reconnects indefinitely), so only *persistent* failures reach the dead-letter destination — a malformed message (a bug; auto-retry would just loop) or a bad/blocked address (won't succeed on replay), both of which need human diagnosis rather than a blind resend. Safe automated redrive (poison-detection, backoff, a redrive cap) is real machinery for failures that are rare at this volume and warrant eyes; deferred until volume justifies it.
- A correlation id carried on each message and logged on publish and on send lets one release be traced end-to-end across both services in the log store.

---

## Security Considerations

The internal bearer auth on the synchronous hop is removed with it. The broker runs unauthenticated on the internal network (the same posture as the previous hop's dev mode). Broker authentication and transport encryption are a documented future upgrade, mirroring ADR-012's mTLS note. No new external attack surface.

---

## Performance Considerations

Throughput is irrelevant at this scale. The change is to *latency shape*: delivery becomes asynchronous — publish returns immediately and the send happens out of band, which is fine for email. Persistent storage adds a disk write per publish; negligible at this volume.

---

## References

- [ADR-012](012-notifier-service-boundary.md) — Notifier Service Boundary (transport superseded here; boundary retained)
- [ADR-006](006-transactional-outbox.md) — the at-most-once delivery gap this closes · [ADR-009](009-structured-logging.md) — structured logging (the correlation id rides on this) · [ADR-010](010-log-shipping-pipeline.md) — log shipping pipeline · [ADR-011](011-red-metrics-and-prometheus-grafana.md) — RED metrics
- [`docs/microservices.md`](../microservices.md) — concrete topology, message contract, and runtime flows
