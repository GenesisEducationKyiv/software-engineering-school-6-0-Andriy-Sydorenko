# ADR-006: Transactional Outbox for Release Notifications

## Status
Proposed

---

## Context

Today's scan-and-notify path advances `last_seen_tag` *before* attempting the SMTP send (system-design §9.1). The tradeoff is explicit: SMTP failure or process crash between the tag update and the send results in a **lost** notification, never a duplicate. This was chosen because duplicate emails damage domain reputation and trigger spam complaints, and lost emails are recoverable by the user via GitHub's release feed.

This is fine for now. Two signals would force a revisit:

1. **User-visible loss reports.** "I never got the v2.0 email" complaints become routine.
2. **SMTP failure rate.** Sustained > 1% send failures translate to that same fraction of releases being lost.

If either of those happens, the system needs at-least-once delivery with idempotent retries. The standard solution is the **transactional outbox** pattern.

---

## Decision (proposed)

Introduce an `outbox` table written in the same transaction as the `last_seen_tag` update. A separate worker drains the outbox and sends mail with retry + backoff.

**Schema:**

```sql
CREATE TABLE outbox (
  id              BIGSERIAL PRIMARY KEY,
  subscription_id UUID NOT NULL REFERENCES subscriptions(id) ON DELETE CASCADE,
  tag             TEXT NOT NULL,
  payload         JSONB NOT NULL,         -- rendered subject + body
  attempts        INT  NOT NULL DEFAULT 0,
  next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  delivered_at    TIMESTAMPTZ,
  last_error      TEXT,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (subscription_id, tag)            -- idempotency key
);
CREATE INDEX idx_outbox_pending
  ON outbox (next_attempt_at)
  WHERE delivered_at IS NULL;
```

**Write path (in scanner, one transaction):**

```sql
BEGIN;
  UPDATE subscriptions SET last_seen_tag = $T WHERE id = $S;
  INSERT INTO outbox (subscription_id, tag, payload) VALUES ($S, $T, $P)
    ON CONFLICT (subscription_id, tag) DO NOTHING;
COMMIT;
```

The `ON CONFLICT … DO NOTHING` is the idempotency guarantee - if the same `(subscription, tag)` is enqueued twice (scanner restart, double-tick), only one row exists and only one mail is sent.

**Drain path (separate goroutine, leader-elected when replicas > 1):** claim a batch with `SELECT … FOR UPDATE SKIP LOCKED LIMIT N`, send via SMTP, set `delivered_at` on success or bump `attempts` + `next_attempt_at = now() + backoff(attempts)` on failure. After `MAX_ATTEMPTS` (default 8), dead-letter the row and alert. `SKIP LOCKED` lets multiple workers drain in parallel using only Postgres's own coordination.

---

## Consequences

**Positive**
- Flips the loss-vs-duplication tradeoff: at-least-once delivery, no silent loss.
- Idempotency is enforced by a unique constraint, not application code.
- Retries become a database concern, not a goroutine concern - survives process crashes.
- The drain worker can scale horizontally with `SKIP LOCKED`.
- DLQ surfaces hard failures (bad mailbox, persistent SMTP rejection) for human action.

**Negative**
- **Storage growth.** One row per notification. Mitigation: purge `delivered_at < now() - INTERVAL '30 days'`.
- **Two failure surfaces** (write path, drain path) instead of one.
- **Latency floor.** Drain polling caps minimum delivery latency; sub-second requires `LISTEN/NOTIFY`.
- **No per-subscription ordering across retries** - fine for independent release emails; would not be for chat-style streams.

---

## Alternatives Considered

- **Status quo (persist-then-send, at-most-once).** Current design - correct until loss complaints arrive.
- **Send-then-persist (no idempotency).** Crash between send and tag-update fans out duplicates on retry. Exactly the deliverability hazard we want to avoid.
- **External queue (SQS, RabbitMQ, NATS).** Same semantics as outbox but adds a deploy dependency and a second consistency surface. Revisit only if we stop being a monolith.
- **Two-phase commit between Postgres and SMTP.** SMTP has no prepare phase.

---

## Operations

- **Migration.** One versioned migration adds the table + partial index. No backfill - outbox starts empty.
- **Metrics & alerts.** `outbox_pending` (page if monotonically growing → drainer dead), `outbox_dlq_total` (page on rising rate), `outbox_attempts_total{outcome}`, `outbox_drain_duration_seconds`.

---

## When to Adopt

Any one of: SMTP failure rate > 1% over 24 h, > 2 user-reported missed releases in a month, or move to multi-replica scanners (leader election makes single-shot sends unsafe). Until then, the current behavior is fine.

---

## References

- System Design §9.1 - Tag-advance vs. email-send ordering
- ADR-001 - Primary Datastore
- Pat Helland, "Life beyond Distributed Transactions"
- https://microservices.io/patterns/data/transactional-outbox.html
