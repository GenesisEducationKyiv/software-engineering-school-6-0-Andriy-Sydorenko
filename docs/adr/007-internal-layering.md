# ADR-007: Internal Layering & Consumer-Defined Interfaces

## Status
Accepted

---

## Context

Independently of how the system is deployed (ADR-003 chose a modular monolith), the code inside the binary needs a layout that:

- Keeps HTTP concerns out of business logic and SQL out of HTTP handlers.
- Avoids import cycles between packages that all reference the same domain types (subscriptions, tokens).
- Keeps tests cheap — fakes should be small and aligned with what each caller actually uses.
- Does not require a DI framework for a dependency graph that fits on one screen.

This ADR answers two coupled questions: how layers are wired, and where interfaces live.

---

## Decision

**Strict layering:**

```
Handler  →  Service  →  Repository  →  Database
              ↘             ↘
               GitHub client  Notifier
```

- **Handler** parses HTTP, calls a service, maps domain errors to HTTP status codes. Never touches GORM. Never imports `gin` outside `internal/api`.
- **Service** holds business logic, orchestrates repository + GitHub + notifier, raises domain sentinel errors. Knows nothing about HTTP. Takes a `context.Context`, never a `gin.Context`.
- **Repository** owns SQL via GORM. No business logic. Returns ORM models or `domain` types.
- **Domain** (`internal/domain`) is a leaf package: ORM models, DTOs, sentinel errors. Imports nothing from other internal packages, so no other package can pull it into a cycle.

**Interfaces are declared at the consumer, not the implementer.** A service that needs persistence defines its own `SubscriptionRepository` interface inside `internal/service`; the repository package implements it structurally. This is the Go-idiomatic inversion of "interfaces live next to implementations" — "accept interfaces, return structs."

**No DI framework.** `cmd/server/main.go` is the composition root and the only place that knows the full dependency graph. Wiring is ~50 lines of plain Go.

---

## Consequences

**Positive**
- Consumer-defined interfaces mean tests double the consumer's needs, not the implementer's surface — fakes are tiny and stay aligned with what the caller actually uses.
- No import cycles: `domain` is a sink, every other package depends on it, none of them depend on each other through it.
- Hand-wiring in `main.go` keeps the dependency graph readable in one file; no codegen step, no runtime reflection.
- Swapping an implementation (e.g. a fake repository in tests) requires no changes to producer packages.

**Negative**
- "Modular" is a discipline, not an enforcement: nothing in the compiler stops a handler from importing GORM. Code review and lint rules are the guard.
- Each consumer redeclaring its own interface can produce small amounts of duplication when two consumers need overlapping methods. Acceptable at this scale.
- Hand-wiring scales with the graph; if the graph grows past one screen, a framework may become worth reconsidering.

---

## Alternatives Considered

- **Interfaces declared in implementer packages.** Rejected: forces every consumer to import the implementer's package even in tests, inverts the dependency direction, and makes mocking surface-heavy. Idiomatic Go declares interfaces where they are used.
- **DI framework (Wire, Fx, dig).** Rejected: hand-wiring in `main.go` is ~50 lines today. A framework adds compile-time codegen or runtime reflection without removing the manual graph specification.
- **No layering / fat handlers.** Rejected: handlers that talk directly to GORM are fast to write and impossible to test or evolve; the cost shows up the first time a flow needs to be reused outside an HTTP request (e.g. by the scanner).

---

## Operations

Layering invariants are enforced by code review today:
- no `gin` imports outside `internal/api`;
- no GORM imports outside `internal/repository`;
- no `internal/domain` import of any other `internal/*` package.

`depguard` or `go-arch-lint` can mechanize these rules once the team grows past one engineer.

---

## References

- ADR-003 — Service Decomposition
- Rob Pike, "Accept interfaces, return structs."
- System Design §4, §5
