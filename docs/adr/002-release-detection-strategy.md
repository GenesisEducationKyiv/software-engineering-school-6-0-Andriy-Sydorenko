# ADR-002: Release Detection Strategy and Scanner Concurrency

## Status
Accepted

---

## Context

The service notifies users when tracked GitHub repos publish releases. Two questions to resolve up front: (1) push or pull, and (2) how to structure the polling loop safely under load.

Webhooks need admin access on source repos we don't own. Polling works for any public repo without upstream cooperation, but introduces concurrency, rate-limit, and per-call-deadline concerns the scanner must own end-to-end.

Constraints: any public repo without owner cooperation; bounded latency; stay within GitHub's 5,000 req/h authenticated ceiling; no public inbound endpoint to harden; one slow upstream cannot stall a tick; an accidentally replicated scanner must not double-send.

---

## Decision

**Strategy - periodic polling.** Reject webhooks (no admin access). A ticker-driven goroutine per process iterates confirmed subscriptions, fetches the latest tag per repo, diffs against persisted `last_seen_tag`, dispatches notifications on change. `SCAN_INTERVAL` (default 5 min) sets cadence. Authenticated `GITHUB_TOKEN` lifts the rate-limit ceiling. A Redis cache in front of the GitHub client deduplicates lookups across subscribers - many subscribers, one upstream request per repo per interval. GitHub 429 / `Retry-After` maps to 503 to API callers; scanner backs off on the next tick.

**Concurrency contract.** Each tick fans out through a bounded `WorkerPool` (`SCAN_CONCURRENCY=8`). Every GitHub call runs under `context.WithTimeout(GITHUB_TIMEOUT=10s)` so a hung repo cannot stall a worker. Tick duration is bounded: when `elapsed > 0.8 × SCAN_INTERVAL`, the next pending tick is drained rather than queued, preventing pile-up. Single-owner today holds by construction (deployment is single-replica); the upgrade path is `pg_try_advisory_lock`-based leader election when multi-replica deploy becomes a real need (gated on ADR-004).

**Tag-diff lives on the entity.** `(*domain.Subscription).IsNewTag(tag string) bool` - GRASP Information Expert. Future rules ("ignore pre-releases") attach to the data owner.

**Not abstracted today:** `WorkerPool` is a concrete struct, not an interface. One impl, one consumer; an interface is added when a second arrives. Same call applied to leadership, ticking, and event publishing - abstractions emerge from real second-impl pressure (ADR-006 outbox, multi-replica), not from speculation.

---

## Consequences

**Positive**
- Works for any public repo, no upstream cooperation.
- Hung GitHub call no longer stalls the tick; pool keeps draining.
- Tick pile-up is observable; rate-limit budget reasoned about per-tick.
- Cache deduplicates upstream calls.

**Negative**
- Notification latency floored by `SCAN_INTERVAL`.
- Continuous outbound traffic, even for quiet repos.
- Single-replica scanner is the horizontal-scaling ceiling; multi-replica needs leader election + versioned migrations (ADR-004).

---

## Alternatives Considered

- **Webhooks** - rejected: need admin access we don't have, plus secret/retry/inbound-hardening cost. Reconsider for opt-in repos whose owners install the integration.
- **RSS / Atom feeds** - rejected: inconsistent metadata, no auth, lower reliability than the REST API.
- **Manual user-triggered refresh** - rejected: defeats the product.
- **Inline goroutine fan-out (no `WorkerPool` type)** - rejected: spreads concurrency policy across scanner internals, can't swap for a serial impl in tests.
- **Unbounded goroutine per repo** - rejected: exhausts connection pool and rate-limit budget at scale.

---

## Future Work

- **ETag / `If-None-Match`.** `304 Not Modified` responses don't count against the rate limit, so caching unchanged tags lifts the per-tick repo ceiling on the same 5,000 req/h budget. No schema change.
- **Leader election** via `pg_try_advisory_lock` for multi-replica safety. Gated on ADR-004 migration to versioned SQL.
- **Outbox-backed delivery** (ADR-006) - flips at-most-once → at-least-once; today the scanner calls the notifier directly, tomorrow it publishes to an outbox drained by a separate worker.
- **Hybrid push/pull** for repos whose owners opt into webhooks.

---

## Security

Authenticated GitHub requests; tokens via env / secret manager, never logged. Absence of inbound webhook reduces external attack surface.

---

## References

- https://docs.github.com/en/rest/releases/releases
- https://docs.github.com/en/rest/using-the-rest-api/rate-limits-for-the-rest-api
- ADR-001 (datastore), ADR-003 (service decomposition), ADR-004 (gates leader election), ADR-006 (outbox future), ADR-007 (internal layering)
