# ADR-003: Service Decomposition

## Status
Accepted

---

## Context

The service has four concerns: HTTP API, scanner, notifier, and persistence. The question this ADR answers is purely about **process topology** — how many deploy units, and how do they communicate?

Constraints:
- Single-team, single-deploy operation. No platform team to absorb microservice overhead.
- The scanner and the API share a Postgres database — splitting them gains nothing and loses transactional reasoning.
- Current load is modest; bottlenecks are external (GitHub rate limits, SMTP), not CPU or write throughput on any single component.

Internal code structure (layering, where interfaces live, DI) is a separate decision and is covered by ADR-007.

---

## Decision

**Modular monolith.** One Go binary (`cmd/server/main.go`) hosts the API, scanner, and notifier as cooperating in-process components. All persistent state goes through a single Postgres connection pool. No internal RPC, no message bus.

Components communicate by direct Go function calls within the same process. Cross-cutting flows (subscribe → issue token → send email) execute inside a single transaction against a single database.

---

## Consequences

**Positive**
- One deploy unit, one log stream, one process to debug.
- Cross-cutting flows stay in one transaction — no distributed-transaction problem, no eventual-consistency reasoning.
- No RPC layer, no message bus, no service mesh, no per-service CI pipelines. The operational surface is small.
- Onboarding is "read `main.go` top to bottom."

**Negative**
- The whole binary scales together; the scanner cannot be scaled independently of the API. Fine at current load; the upgrade path is in the system-design doc §10.
- A bug in one component can take the whole process down — graceful shutdown and panic recovery in handlers are required.
- A future split into separate services requires reintroducing an inter-process boundary that doesn't exist today.

---

## Alternatives Considered

- **Microservices (API + scanner + notifier as separate deployables).** Rejected: would require an internal RPC layer, a message bus or shared DB, distributed tracing, and at least three deploy pipelines. None of those problems exist today; all of them would be created by the split. The shared Postgres in particular means the scanner and the API are already coupled through data — splitting them at the process level keeps the coupling but adds a network hop.
- **API as one service, scanner+notifier as a worker.** Rejected for the same reasons in miniature — the worker would still need the same database, and the only thing gained is independent restart. At current load that is not worth a second deploy unit.

---

## When to Revisit

Revisit if any of:
- Scanner work consistently starves API request handling.
- Multiple teams need independent deploy cadence on different components.
- A non-Go consumer needs direct in-process access to one component.

Until then, keep the monolith.

---

## References

- ADR-001 — Primary Datastore
- ADR-002 — Release Detection Strategy
- ADR-007 — Internal Layering & Consumer-Defined Interfaces
- System Design §4, §5
