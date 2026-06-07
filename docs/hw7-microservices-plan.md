# HW6 — Microservices Refactor Plan

> Goal: split the modular monolith into microservices with **documented boundaries** and
> **public APIs**, routing all inter-service communication through those APIs.
> ⭐ Star task: implement each inter-service call over **both HTTP and gRPC** and benchmark them.
>
> This decision **supersedes ADR-003** (which chose the modular monolith). A new
> ADR-010 must record the split; ADR-006 (outbox) stays *Proposed* and out of scope here.

---

## 1. Service boundaries (the split)

Three services, one Go module, three `cmd/` binaries. **Only the Subscription service touches Postgres** — this is the decisive ownership rule.

| Service | Owns | Inbound public API | Outbound calls |
|---|---|---|---|
| **subscription** | Postgres (`subscriptions`, `confirmation_tokens`), all business rules | **External HTTP** (user-facing): `POST /api/subscribe`, `GET /api/confirm/:token`, `GET\|POST /api/unsubscribe/:token`, `GET /api/subscriptions`. **Internal HTTP+gRPC** (scanner-facing): `ListConfirmedRepos`, `ListConfirmedSubscriptionsByRepo`, `UpdateLastSeenTag` | → notifier (send confirmation) |
| **notifier** | SMTP + email templates (stateless) | **HTTP+gRPC**: `SendConfirmationEmail`, `SendReleaseNotification` | none |
| **scanner** | nothing (no DB) | none (worker; `/health` only) | → subscription (read subs, update tag), → GitHub (fetch release), → notifier (send release) |

**GitHub client stays a shared library** (`internal/github`) imported by subscription (validate repo) and scanner (fetch release). Promoting it to a 4th service buys nothing for this assignment; noted as a future option.

### Inter-service calls (these get dual HTTP+gRPC implementations)
1. `scanner → subscription.ListConfirmedSubscriptionsByRepo` — list payload, read-heavy → best gRPC showcase
2. `scanner → subscription.UpdateLastSeenTag` — tiny write
3. `scanner → notifier.SendReleaseNotification`
4. `subscription → notifier.SendConfirmationEmail`

### Boundary tradeoffs (state these in the ADR)
- **Distributed write:** `subscribe` now spans subscription's DB write + a notifier call. Keep today's **persist-then-send, best-effort email** semantics (matches current behavior / ADR-006 status quo). No 2PC, no outbox in this HW.
- **Scanner reads via API, not DB:** adds a network hop vs. the old direct query, but is *required* by "all inter-module comm through public APIs." Accept it; it also enforces the single-DB-owner rule.

---

## 2. Target repo layout

```
cmd/
  subscription/main.go      # external HTTP + internal HTTP/gRPC server + DB
  notifier/main.go          # HTTP/gRPC server, SMTP
  scanner/main.go           # ticker worker, pure client
proto/
  subscription/v1/*.proto   # internal subscription API
  notifier/v1/*.proto       # notifier API
  gen/...                   # generated stubs (buf or protoc)
internal/
  subscription/             # was service/ + repository/ + api/ + db/
  notifier/                 # was notifier/ + templates/
  scanner/                  # was scanner/, now with remote clients
  github/                   # shared lib (unchanged)
  domain/                   # shared models/errors (unchanged)
  transport/
    notifierclient/         # interface + http impl + grpc impl, selected by env
    subscriptionclient/     # interface + http impl + grpc impl
api/openapi/                # documented external + internal HTTP contracts
docs/adr/010-*.md           # supersedes 003
docs/boundaries.md          # the boundary doc the HW asks for
bench/                      # HTTP-vs-gRPC harness + results
```

Each remote dependency is a **client interface with two implementations** (HTTP, gRPC), chosen by `TRANSPORT=http|grpc`. The service logic calls the interface and is transport-agnostic — this is what makes the benchmark a fair apples-to-apples comparison.

---

## 3. Implementation phases (incremental, reviewable)

1. **Contracts first.** Write `.proto` for notifier + subscription-internal APIs; write OpenAPI for the HTTP equivalents. Generate gRPC stubs (`buf`). Wire `make proto`. *No behavior change yet.*
2. **Extract notifier** (easiest — stateless). New `cmd/notifier` serving HTTP+gRPC. Replace direct `notifier` calls in subscription & scanner with a `notifierclient` interface (http+grpc impls). Subscription & scanner now call notifier over the wire.
3. **Add subscription internal API.** Stand up the internal HTTP+gRPC server in `cmd/subscription` exposing `ListConfirmedRepos / ListConfirmedSubscriptionsByRepo / UpdateLastSeenTag`. Keep external HTTP as-is.
4. **Extract scanner** into `cmd/scanner`. Replace its direct repository access with `subscriptionclient` (http+grpc). Scanner no longer imports `repository`/`db`.
5. **Compose & config.** `docker-compose`: postgres, mailpit, subscription, notifier, scanner (+ optional redis). Each service reads dependency host/port + `TRANSPORT` from env.
6. **Benchmark (star task).** `bench/` harness drives ops #1, #3, #4 over both transports; record p50/p95/p99 latency, throughput, payload bytes, CPU. Write `bench/RESULTS.md` with conclusions.
7. **Docs.** `docs/boundaries.md` (services, ownership, API contracts, sequence of subscribe & scan flows) + ADR-010 superseding ADR-003.

---

## 4. Testing impact

- **Unit:** mostly intact; mock the new `notifierclient` / `subscriptionclient` interfaces (regenerate uber-go/mock).
- **Integration:** subscription service keeps its testcontainers Postgres suite. Add client↔server round-trip tests for both transports.
- **E2E:** harness now spins up all three services via compose; the existing Chromium/CDP + Mailpit flow exercises subscribe→confirm→release end-to-end across process boundaries.

---

## 5. Benchmark design (star task)

For each of the 3 key ops, same business payload, warm connections, N=10k calls, concurrency sweep {1,8,32}:

| Metric | Why |
|---|---|
| latency p50/p95/p99 | gRPC's HTTP/2 + protobuf should win tail latency |
| throughput (req/s) | connection reuse / multiplexing effect |
| payload bytes on wire | protobuf vs JSON size |
| CPU per 10k calls | serialization cost |

Expected conclusion to validate: gRPC wins on payload size + tail latency, especially for the list op; HTTP/JSON wins on debuggability/tooling. Report the *measured* numbers, not assumptions.

---

## 6. Open decisions before coding

- **Stub tooling:** `buf` (recommended) vs raw `protoc`.
- **Shared `domain` package** across services vs. per-service DTOs mapped from generated types. Recommend: keep `domain` shared (same module), map to/from generated types at the transport edge.
- **Auth between services:** reuse the existing API-key middleware on internal endpoints, or leave internal traffic trusted within compose network. Recommend: API-key on internal HTTP/gRPC too (cheap, consistent).
