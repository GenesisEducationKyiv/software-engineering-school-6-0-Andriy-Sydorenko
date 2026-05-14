# ADR-XXX: <Short Decision Title>

## Status

Proposed | Accepted | Rejected | Deprecated | Superseded

---

## Context

Describe the problem, constraints, and engineering pressure that led to this decision.

Include:
- current situation
- operational pain
- scaling concerns
- technical limitations
- business constraints
- relevant incidents/problems

Example questions:
- What is broken?
- What will break soon?
- Why is this decision needed now?
- What constraints exist?

---

## Decision

Describe the exact decision being made.

Be direct and explicit.

Examples:
- "We will use PostgreSQL as the primary relational database."
- "Internal services will communicate via gRPC."
- "We will adopt a modular monolith architecture."

Avoid vague wording like:
- "We are considering..."
- "It might be beneficial..."
- "Possibly..."

---

## Consequences

### Positive

List the expected benefits.

Examples:
- stronger type safety
- lower latency
- easier observability
- simplified deployments
- reduced infrastructure cost

### Negative

List the tradeoffs and damage caused by this decision.

Examples:
- higher operational complexity
- steeper onboarding
- migration cost
- additional infrastructure maintenance
- more difficult local debugging

If there are no negatives, the ADR is probably shallow.

---

## Alternatives Considered

### Option 1: <Name>

Why it was rejected.

Include:
- technical limitations
- operational risks
- migration cost
- scaling problems
- team expertise mismatch

### Option 2: <Name>

Why it was rejected.

Repeat as needed.

---

## Rollout Plan

Describe how this decision will be introduced safely.

Example:
1. Create shared protobuf repository
2. Add CI generation pipeline
3. Migrate auth service
4. Deprecate REST endpoints
5. Remove legacy code

If migration is unnecessary, explicitly state:
- "No migration required."

---

## Operational Impact

Describe impact on:
- deployments
- monitoring
- CI/CD
- on-call
- debugging
- infra cost
- developer workflow

This section is usually what separates real engineering ADRs from tutorial garbage.

---

## Security Considerations

Describe:
- authentication impact
- authorization impact
- secrets handling
- compliance implications
- attack surface changes

If none:
- "No significant security impact."

---

## Performance Considerations

Describe expected impact on:
- latency
- throughput
- memory
- CPU
- database load
- network traffic

Include benchmark references if available.

---

## References

Links to:
- RFCs
- benchmarks
- incidents
- diagrams
- tickets
- external documentation
- related ADRs

Example:
- ADR-001
- INC-1842
- https://example.io/docs/
