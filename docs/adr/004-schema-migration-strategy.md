# ADR-004: Schema Migration Strategy

## Status
Accepted

---

## Context

The service runs Postgres with a small set of tables (`subscriptions`, `confirmation_tokens`). Schema changes happen rarely but happen — adding a column, an index, or a new table.

Constraints:
- Migrations must run unattended on container start.
- Rollback should be possible for at least the most recent migration.
- Multiple replicas must not race the same migration on simultaneous boot.
- Destructive changes (drop column, type change with data loss) must be visible in code review before they ship.
- Some changes (partial unique indexes, online index builds, data backfills) cannot be expressed as struct tags and must live in SQL.

The scanner's advisory-lock leader election (ADR-002) requires multi-replica safety, which in turn requires migrations that do not race on concurrent boot.

---

## Decision

**Versioned SQL migrations via `golang-migrate`.**

- Migration files live in `migrations/` as numbered pairs: `001_baseline.up.sql` / `001_baseline.down.sql`, applied in order, tracked in a `schema_migrations` table.
- The container entrypoint runs `migrate up` before exec'ing the binary. The app no longer touches DDL at runtime.
- `golang-migrate` takes a Postgres advisory lock for the duration of a migration, so concurrent boots of multiple replicas serialize safely.
- The partial unique index on `subscriptions (email, repo) WHERE deleted_at IS NULL` is part of the baseline migration, not application code.
- Destructive changes (drop column, rename, type change requiring backfill) are explicit SQL in a numbered file and visible in code review.

---

## Consequences

**Positive**
- Destructive changes are explicit, reviewable, reversible.
- Multi-replica deploys are safe — the advisory lock serializes concurrent migration runs.
- Out-of-band operations are straightforward (`CREATE INDEX CONCURRENTLY`, batched backfills, data fixes).
- The full schema is reconstructible from the migrations directory; no need to boot the binary to see it.
- Integration tests run `migrate up` against a fresh Postgres — the same path as production.

**Negative**
- Every schema change is two SQL files instead of a struct edit. Small friction, real cost over time.
- A separate `migrate` step in the deploy pipeline must succeed before the app starts. Failure modes (bad migration, dirty state) need a documented recovery path.
- Down migrations are only safe when written non-destructively; in practice rolling forward is more common than rolling back.

---

## Alternatives Considered

- **GORM `AutoMigrate` on boot.** Used initially while the deployment was single-replica and the schema was small. Rejected once multi-replica became real: concurrent `AutoMigrate` calls race on DDL, it cannot drop columns or rename safely, it provides no review history, and the partial unique index had to live outside the `AutoMigrate` path anyway — easy to forget on a fresh DB.
- **`atlas`** — declarative schema diffing, more powerful than `golang-migrate`. Rejected: more concepts to learn, overkill for a two-table schema. Reconsider if the schema grows past ~10 tables or starts using complex declarative constraints.
- **Pure raw-SQL files run by a `psql` step in the Dockerfile entrypoint.** Rejected: no version tracking, no rollback, no advisory lock against concurrent runs.
- **Migrations as a separate one-shot job rather than entrypoint step.** Better at scale (Kubernetes Job, pre-deploy hook). The entrypoint approach is simpler today and the upgrade to a dedicated job is mechanical.

---

## Operations & When to Revisit

- Integration tests run `migrate up` against a fresh Postgres container.
- The container entrypoint runs `migrate up` before exec'ing the binary; a failed migration prevents the app from starting.
- Rollback is `migrate down 1` and only safe when the down step is non-destructive — destructive rollbacks require an explicit data plan, not a tool invocation.

Revisit on any of:
- Migrations start to need orchestration the entrypoint can't provide (long-running backfills, online index builds against a populated table) — move to a dedicated migration job.
- Schema grows past ~10 tables or starts to need declarative constraint management — evaluate `atlas`.
- Multiple services start writing to the same database — schema ownership and migration coordination need an explicit answer (which service owns which migration).

---

## References

- ADR-001 — Primary Datastore
- ADR-002 — Release Detection Strategy and Scanner Concurrency
- System Design §13
- https://github.com/golang-migrate/migrate
- https://atlasgo.io/
