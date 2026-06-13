# HTTP API vs gRPC — core↔notifier benchmark

Benchmarks the one real network boundary in this design (core → notifier) over
**both** transports, wrapping the **same** `notifier.Core` so transport is the
only variable. gRPC is the runtime transport; the HTTP/JSON server here exists
**only** to benchmark and document the comparison (spec §8).

## What's measured

Two operations, each over gRPC and over idiomatic HTTP/JSON:

| Operation | Workload | Story |
|---|---|---|
| `SendReleaseNotifications` | N = 1 / 100 / 1 000 / 10 000 recipients | payload / serialization scaling (growing `repeated` list) |
| `SendConfirmation` | one tiny request/response | per-call overhead (framing, round-trip) |

Metrics per benchmark: `ns/op`, `B/op`, `allocs/op` (from `testing.B`), plus the
on-the-wire **serialized request size** in bytes (Protobuf vs JSON).

## How to run

```bash
# Benchmarks only (ns/op · B/op · allocs/op):
make bench
#   └─ go test -bench=. -benchmem -run='^$' ./bench/...

# Equivalence proof + the Protobuf-vs-JSON wire-size table:
go test -v ./bench/...

# Just the wire-size table:
go test -run TestWireSize -v ./bench/...
```

For more stable numbers: `go test -bench=. -benchmem -run='^$' -benchtime=2s -count=5 ./bench/...`
and compare with `benchstat`.

## Fairness / methodology (why this comparison is defensible)

- **Same Core, both transports.** Both servers wrap one `*notifier.Core` over a
  no-op counting mailer — SMTP is stubbed, so we measure transport + (de)serialization,
  **not** email sending.
- **Provably equivalent behavior.** `TestTransportsEquivalent` asserts both
  transports return the same `SendAck` (`sent`/`failed`) for identical input, at
  every N. Any measured delta is therefore transport-only.
- **Persistent connections both sides.** One gRPC channel (`platform.Dial`, dialed
  once) and one keep-alive, pooled `*http.Client` — neither pays per-call
  connection setup. HTTP compression is disabled so we compare raw JSON bytes.
- **Auth enabled, symmetric.** The same bearer token is checked with a
  constant-time compare on both sides (gRPC interceptor / HTTP middleware), so
  auth cost is present and equal — not a thumb on the scale.
- **Plaintext both sides** (no TLS), loopback `127.0.0.1`, one machine, one load
  pattern (`testing.B`) — no `ghz`/`hey`/`k6` variance across tools.
- **Identical, deterministic payloads** built once from shared builders; warm-up
  before measurement; `b.ResetTimer()` after setup; `b.ReportAllocs()`.

## Results — latency & allocations

| op | N | transport | ns/op | B/op | allocs/op |
|---|---|---|---|---|---|
| SendConfirmation | 1 | gRPC | 88,272 | 19,485 | 259 |
| SendConfirmation | 1 | HTTP | **59,940** | 18,297 | 191 |
| SendReleaseNotifications | 1 | gRPC | 86,741 | 17,853 | 266 |
| SendReleaseNotifications | 1 | HTTP | **59,825** | 17,066 | 200 |
| SendReleaseNotifications | 100 | gRPC | **735,119** | 571,670 | 8,199 |
| SendReleaseNotifications | 100 | HTTP | 815,490 | 624,097 | 7,959 |
| SendReleaseNotifications | 1000 | gRPC | **6,010,080** | 6,460,907 | 80,265 |
| SendReleaseNotifications | 1000 | HTTP | 6,841,479 | 5,914,160 | 78,211 |
| SendReleaseNotifications | 10000 | gRPC | **59,242,665** | 55,742,801 | 800,542 |
| SendReleaseNotifications | 10000 | HTTP | 61,003,463 | 57,953,563 | 780,319 |

(Bold = faster transport for that row.)

## Results — on-the-wire request size

| op | N | protobuf (B) | json (B) | json/proto |
|---|---|---|---|---|
| SendConfirmation | 1 | 93 | 149 | 1.60× |
| SendReleaseNotifications | 1 | 135 | 209 | 1.55× |
| SendReleaseNotifications | 100 | 6,471 | 9,515 | 1.47× |
| SendReleaseNotifications | 1000 | 64,071 | 94,115 | 1.47× |
| SendReleaseNotifications | 10000 | 640,071 | 940,115 | 1.47× |

## Environment

- Machine / CPU: Apple M1 Pro
- OS / arch (`goos`/`goarch`): darwin / arm64
- Go version: go1.26.2
- `-benchtime` / `-count`: defaults (1s, count=1), single run — treat ±10% as noise

## Conclusions

> Conclusions reflect the measured numbers above, not an assumed result.

**1. Per-call overhead (`SendConfirmation`, tiny payload): HTTP/JSON is actually
faster here — not parity.** At a single tiny request, HTTP wins: **60.0 µs** vs
gRPC's **88.3 µs** (gRPC ≈ 47% slower), with fewer allocations (191 vs 259). On
loopback with no TLS, gRPC's per-call cost — HTTP/2 stream framing + the full
server interceptor chain (recovery → correlation → metrics → auth) + Protobuf
(un)marshalling — exceeds a pooled keep-alive HTTP/1.1 connection + `encoding/json`
for a handful of fields. On the wire the request is tiny either way (93 B proto
vs 149 B json), so size doesn't rescue gRPC at N=1.

**2. Payload scaling (`SendReleaseNotifications`): gRPC overtakes around N≈100 and
stays modestly ahead.**
- **Latency crossover ≈ N=100.** At N=1 HTTP is faster (59.8 µs vs 86.7 µs); by
  N=100 gRPC leads (735 µs vs 815 µs, ≈11% faster), N=1 000 ≈12% faster (6.01 ms
  vs 6.84 ms), N=10 000 ≈3% faster (59.2 ms vs 61.0 ms). The edge is real but
  modest, not order-of-magnitude.
- **Allocations are ~comparable** — gRPC even allocates slightly *more* at small
  N. So gRPC's win isn't an allocation story.
- **Wire size is the consistent, size-independent gRPC win:** Protobuf is
  **~1.47–1.60× smaller** than JSON at every N (JSON repeats field names + quoting
  per recipient; Protobuf uses field tags + varints). That ratio holds from 93 B
  up to 640 KB.

**3. At this app's real scale the throughput delta is small — so throughput is
not the real decision driver.** The service polls a handful of repos every 5
minutes and fans out to a repo's confirmed subscribers — realistically tens to a
few hundred recipients, far below the N=10 000 stress point and nowhere near a hot
loop. At that scale the measured gap is tens of µs to low single-digit ms per
release — negligible against a 5-minute poll cadence, and for tiny confirmations
HTTP is even marginally quicker.

The genuine reasons gRPC is the chosen runtime transport are therefore **not** raw
throughput but:
- **Typed, generated contract** — `proto/notifier.proto` is the single source of
  truth; client/server stubs are generated, so the cross-process boundary can't
  drift (no hand-maintained JSON DTOs on both sides).
- **Tooling & debuggability** — first-class deadlines, status codes, and the
  interceptor chain (auth/recovery/correlation/metrics) reused unchanged; streaming-ready.
- **HW8 readiness** — the same contract becomes the async event the notifier
  consumes once a broker lands.

**Honest bottom line:** at this app's scale we'd lose little throughput with HTTP
— and for tiny requests HTTP is marginally *faster*. gRPC was chosen for the
contract + tooling, not speed. Its measurable throughput wins are (a) ~1.5× smaller
payloads at all sizes and (b) a modest latency edge that only appears above
N≈100 — both of which would matter for a high-fan-out or high-RPS variant, but
don't move the needle at this cadence.
