# ADR-014: Cross-Service Transaction Strategy

## Status

Accepted. Builds on [ADR-012](012-notifier-service-boundary.md) (notifier boundary) and [ADR-013](013-message-broker-nats-jetstream.md) (NATS + JetStream). It does not supersede them — it extends the topology with a second stateful service and a coordinator.

---

## Context

ADR-012 carved the stateless notifier off the core, but the core stayed a single stateful service: subscriptions, the watched-repo cursor, and the scanner all shared one database, so "subscribe" was one local ACID transaction.

Two pressures pulled the watched-repo concern out of the core into its own **Catalog** service (own Postgres): the scanner is a timer-driven, GitHub-rate-limited, cache-backed workload with a different scaling and failure profile from user-facing subscription CRUD; and keeping "what we watch" with "who subscribed" forced one database to own two unrelated lifecycles.

That split changes the shape of **subscribe**. It now has to write to two services that own separate databases:

- **Subscription service** — persist the subscription + its confirmation token.
- **Catalog service** — validate the repo on GitHub and register it for watching (a registration per subscription; a repo is polled while it has ≥ 1).

There is no cross-database ACID transaction available, and a *partial* result is invalid: a subscription to a repo nobody watches, or a watched repo with no subscription behind it, is corrupt state that must not persist. So the two writes must be made atomic-in-effect without a distributed lock.

The notifier (ADR-013) cannot help carry this: it is stateless by design and an email cannot be un-sent, so it has nothing to compensate. A real distributed transaction needs **two stateful participants that can each compensate**, which is exactly what the Catalog extraction created.

---

## Decision

Coordinate subscribe as an **orchestrated saga** driven by a standalone **Orchestrator** service. The orchestrator owns a `saga_log` and **no business data**; it sequences the participants and compensates on failure.

The saga has three zones:

1. **Before the pivot (compensatable)** — `catalog.register`: validate the repo on GitHub (fail-fast, since this is the step most likely to fail), then register it. Compensation: `catalog.release`.
2. **The pivot** — `subscription.create`: insert the subscription + token. Once this commits, the transaction is committed.
3. **After the pivot (terminal, retriable, never compensated)** — the confirmation email. The orchestrator publishes a `confirmation.requested` event *only after* writing `COMMITTED`; the subscription service renders and publishes the email. An email is irreversible, so it lives strictly after the pivot.

**Recovery rule, persisted in the `saga_log`:** compensate before the pivot, roll forward after it. On restart the orchestrator sweeps unfinished sagas — pre-pivot states release the registration and abort; post-pivot states re-issue the confirmation (idempotent, deduplicated).

**Transport** reuses the existing broker, no new stack:

- **Synchronous Core NATS request-reply** for saga commands and compensations — the orchestrator needs an immediate ok/fail reply to decide commit-vs-compensate. ADR-013 chose NATS and explicitly noted request/reply was available "should a synchronous need ever arise"; this is that need.
- **JetStream** for the durable, fire-and-forget events: `release.detected`, `subscription.removed`, `confirmation.requested`, and the `notify.*` emails.

**Each service owns its own Postgres** — orchestrator (`saga_log`), subscription, catalog. There is no shared database; that is what makes this a distributed transaction rather than a local one.

**Unsubscribe is deliberately *not* a saga.** Deleting the subscription is the user's goal and is never rolled back; releasing the Catalog registration is idempotent cleanup that can lag. A "compensation" here would mean re-inserting the subscription the user just deleted — a wrong, degenerate compensation. So unsubscribe emits a `subscription.removed` event that Catalog consumes — a reliable-event / eventual-consistency problem, not a distributed transaction. Knowing which operation needs a saga and which does not is the core design judgement here.

The **release-notification** path is the same kind of call, and likewise event-driven choreography rather than a saga: scan → per-recipient fan-out → deliver is best-effort, idempotent, and retriable, with nothing to compensate. Orchestration is reserved for subscribe — the one flow with two stateful writes that must be atomic-in-effect.

---

## Consequences

### Positive

- **Cross-service atomicity-in-effect** for subscribe without a distributed lock: either both writes land or the registration is compensated away.
- **Crash-safe** via the `saga_log` and the compensate-before / forward-after-pivot rule; recovery is deterministic and idempotent.
- **One transport.** Commands ride Core request-reply, events ride JetStream — both on the broker ADR-013 already introduced. No gRPC stack to reintroduce.
- **Honest service boundaries.** Catalog owns the scanner/cursor workload; the subscription service owns the subscriber relationship; the orchestrator owns only the use-case coordination.

### Negative

- **A third (orchestrator) and effectively fourth service to run,** each with its own database — more moving parts, more to monitor.
- **Optimistic-then-compensate replaces validate-everything-first.** A saga gives up "check every service before committing anything" (impossible without 2PC) for "commit locally, compensate on failure"; reasoning about partial states is harder than one transaction.
- **More failure surfaces** — request-reply timeouts, compensation failures, recovery sweeps — that a single local transaction never had.

---

## Accepted tradeoffs

Documented so they are not mistaken for bugs:

- **Benign registration leak on a register timeout.** If `catalog.register` succeeds but its reply is lost, the saga aborts and leaves a registration with no subscription. It is harmless (the scanner finds no confirmed subscribers and emails no one) and is reclaimed when that repo's last real registration is released. A transactional outbox / dedup sweep would close it; deferred at this volume.
- **Eventual unsubscribe cleanup.** Between the delete and Catalog consuming `subscription.removed`, the scanner may poll a now-subscriberless repo once more. Benign, self-healing.
- **Harmless unconfirmed subscription** if the confirmation email permanently fails to deliver. Unconfirmed subscriptions are inert (the scanner emails only confirmed ones), so no inconsistency results.

---

## Alternatives Considered

### Option 1: Embedded orchestrator (coordinator inside the subscription service)

Lighter (one fewer deployable), but the subscription service would be both coordinator and participant — the double role muddies exactly the boundary the saga is meant to make explicit, and couples the saga log to a participant's database. Rejected for a standalone coordinator.

### Option 2: Choreographed saga (participants react to each other's events, no coordinator)

Scatters the compensation logic across participants and makes the overall state hard to observe or recover. A central `saga_log` and a single place that owns "what to do on failure" is worth the coordinator. Rejected.

### Option 3: Asynchronous orchestration (orchestrator as a durable event-driven state machine)

Survives crashes natively but adds real machinery — durable consumers per command/event, correlation, timeouts, a full state machine — for no benefit at single-digit subscribes per minute. Synchronous request-reply plus the `saga_log` gives the same recovery story with far less code. Deferred until volume justifies it.

### Option 4: Reintroduce gRPC/HTTP for the saga commands

A second comms stack alongside the broker. ADR-013 removed synchronous RPC deliberately and noted request/reply covers any synchronous need on the existing transport. Rejected — no honest job for a second stack at this scale.

### Option 5: Notifier as a saga participant

The notifier is stateless (ADR-013) and an email is not reversible, so it cannot compensate. It stays the terminal, retriable, non-transactional sink. Rejected.

### Option 6: Two-phase commit across the two Postgres databases

Requires a coordinator holding locks across services for the duration — fragile, blocking under coordinator failure, and unsupported by the participants' plain transactional writes. A saga trades 2PC's blocking atomicity for non-blocking compensation, which fits this domain (a failed subscribe is fine to retry). Rejected.

---

## Rollout Plan

No data migration of existing rows. The change is structural and ships together: extract Catalog (move the scanner + watched-repo registry to its own database), stand up the orchestrator, route subscribe through it, and add the saga command handlers + event consumers to the participants. One forward migration adds `public_id` to `subscriptions` (the cross-service identity) and drops the now-moved `watched_repos` from the subscription database.

---

## Operational Impact

- **Four services + three databases + the broker** to deploy and monitor (was: core + notifier + broker, two databases).
- **Stuck work is visible in the `saga_log`** — sagas lingering in non-terminal states (`CATALOG_OK`, `SUBSCRIPTION_PENDING`, `COMMITTED`, `COMPENSATING`) indicate a participant outage or a recovery sweep that has not run. The partial index on unfinished states keeps the sweep cheap.
- **Failure runbook:** a request-reply timeout surfaces as a `5xx` on `POST /subscribe` and a non-`DONE` saga; the recovery sweep resolves it on the next tick once participants are healthy.
- **Debugging spans more processes** than the monolith did; the saga id ties a subscribe across services in the logs.

---

## Security Considerations

The public `POST /subscribe` entry point moves from the subscription service to the orchestrator; confirm and unsubscribe stay on the subscription service as open, token-capability endpoints — the unguessable per-subscription token in the URL is the authorization (open by design so a mail client can act on the link without attaching headers; token rationale in [ADR-005](005-token-strategy.md)), unchanged by this work. Saga commands run over the broker on the internal network, unauthenticated — the same posture as the existing internal NATS traffic (ADR-013); broker auth + transport encryption remain the documented future upgrade. No new external attack surface: the saga is internal coordination behind one public endpoint. Internal error details are never returned to the client (the orchestrator maps unmapped failures to a generic 500).

---

## Performance Considerations

Throughput is irrelevant at this scale (single-digit subscribes per minute). The change to *latency shape*: a subscribe now makes two synchronous request-reply round-trips (register, create) before returning, plus a post-commit publish — still sub-second on a local broker, and bounded by a per-command timeout. The scanner's polling load is unchanged; it simply runs in the Catalog process and emits one `release.detected` event per release instead of fanning out inline.

---

## References

- [ADR-001](001-primary-datastore.md) — Primary datastore (Postgres)
- [ADR-012](012-notifier-service-boundary.md) — Notifier service boundary (extended here)
- [ADR-013](013-message-broker-nats-jetstream.md) — NATS + JetStream (the transport this reuses)
- [`docs/microservices.md`](../microservices.md) — concrete topology, saga flow, and message contracts
- Pat Helland, "Life beyond Distributed Transactions"
- https://microservices.io/patterns/data/saga.html
