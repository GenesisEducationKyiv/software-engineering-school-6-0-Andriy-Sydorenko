# ADR-001: Primary Datastore

## Status
Accepted

---

## Context

The service persists subscriptions, confirmation tokens, and per-repo synchronization state for a GitHub release-notification system. Workload characteristics:

- Strongly relational: `subscription` ↔ `confirmation_token` (1:1, cascading delete); uniqueness on `(email, repo)` excluding soft-deleted rows.
- Mixed read/write concurrency from the HTTP API and a background scanner that polls GitHub.
- Durable, transactional updates required (token issuance, confirmation flips, soft deletes).
- Schema is expected to evolve.

---

## Decision

Use **PostgreSQL** as the single source of truth, accessed through **GORM**. Constraints (foreign key, unique, partial unique, cascading delete) are enforced at the database level, not in application code.

Redis is used only as a cache for the GitHub client - never as a primary store.

---

## Data Model

- `subscriptions` ↔ `confirmation_tokens` are 1:1 with `ON DELETE CASCADE`.
- Soft delete via `subscriptions.deleted_at`.
- A **partial unique index** on `(email, repo) WHERE deleted_at IS NULL` enforces uniqueness only over live rows - a plain unique index would count tombstoned rows and block re-subscription.

---

## Consequences

**Positive**
- Relational integrity enforced by the DB, not the app.
- Mature concurrency, indexing, and transactional isolation.
- Broad operational tooling (backup, migration, observability).

**Negative**
- Dedicated infrastructure vs. an embedded DB; local dev needs Docker.
- Connection-pool tuning becomes a real concern at scale.

---

## Alternatives Considered

### Paradigm: SQL vs NoSQL

**SQL - chosen.**
- *Pros for this workload:* the data model is inherently relational (`subscription` ↔ `confirmation_token` 1:1 with cascading delete, soft-delete-aware uniqueness on `(email, repo)`). Foreign keys, unique constraints, partial unique indexes, and transactions let the database - own integrity. Schema evolution is well-served by mature migration tooling.
- *Cons accepted:* rigid schema requires migrations for every shape change; scales vertically first; more operational surface than an embedded store.

**NoSQL - rejected.**
- *Pros that didn't apply:* flexible schema and easier horizontal scale are real strengths, but neither maps to this project - the schema is small and stable, and the bottlenecks are external (GitHub rate limits, SMTP), not write throughput.
- *Cons that did apply:* most NoSQL engines have no foreign keys and no transactions that span multiple documents. That means the database can't guarantee basic rules for us - e.g. "when a subscription is deleted, its confirmation token is deleted too" or "no two live subscriptions share the same `(email, repo)`". Those checks would have to live in application code, where a missed branch or a race between two requests can silently leave the data in a broken state. SQL gives us those guarantees for free.

### Within SQL

- **SQLite** - single-writer locking conflicts with the scanner's parallel writes; no horizontal-scale path.
- **MySQL** - viable relational alternative, but lacks native partial unique indexes used for soft-delete-aware uniqueness.

### Within NoSQL

- **Redis as primary store** - no durable transactions or relational constraints.
- **MongoDB** - every entity is relational and joined by FK; document model adds no value.

---

## Operations

- **Monitor:** connection-pool saturation, slow queries (`pg_stat_statements`), storage growth, migration failures.
- **Indexes that earn their keep:** `subscriptions (email, repo) WHERE deleted_at IS NULL` (partial unique), `subscriptions (unsubscribe_token)`, `confirmation_tokens (token)`. Bottlenecks at this scale are external (GitHub rate limits, SMTP), not Postgres.

**Future work:**
- **Integration tests against a real Postgres.** Today tests use hand-rolled mocks; integration coverage against a containerized DB is a worthwhile next step.
- **Connection-pool tuning.** `gorm.Open` uses defaults today. When concurrency grows, set `max_open ≈ (cores × 2)` capped by `postgres.max_connections / replicas`, plus `max_idle` and `conn_max_lifetime`.

---

## References

- https://www.postgresql.org/
- https://gorm.io/
- ADR-002 - Release Detection Strategy
