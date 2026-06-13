# gRPC vs HTTP/JSON — a benchmark you can actually read

This folder measures **the one real network hop** in this app — the core service
calling the notifier service to send emails — over **two different transports**:

- **gRPC** (Protobuf over HTTP/2) — the transport the app actually runs on.
- **HTTP/JSON** (plain `net/http` + `encoding/json` over HTTP/1.1) — built **only**
  for this benchmark, so we can compare the two fairly.

Both talk to the **exact same** business logic (`notifier.Core`) over the **same**
fake email sender. So the only thing that ever differs between a "gRPC" number and
an "HTTP" number is the transport. Any difference you see **is** the transport.

---

## TL;DR — the bottom line first

> At this app's real scale, **the transport barely matters for speed.** gRPC was
> chosen for its **typed contract and tooling**, not because it's dramatically
> faster. The numbers below back that up — and show where each one actually wins.

| Question | Winner | Why |
|---|---|---|
| Tiny request, one at a time (latency) | **HTTP** | gRPC's per-call machinery (HTTP/2 framing + interceptors) costs more when there's almost nothing to send |
| Big request (100+ recipients), one at a time | **gRPC** (modest) | Protobuf serializes the big list faster/smaller than JSON |
| Many tiny requests at once (high concurrency) | **gRPC** | gRPC multiplexes them all over **one** connection; HTTP/1.1 thrashes its connection pool and slows down |
| Bytes on the wire | **gRPC** (always) | Protobuf is ~1.5× smaller than JSON at every size |
| Easy to debug / universal / readable | **HTTP** | It's just text; `curl` works; every tool speaks it |
| Typed contract, no drift between services | **gRPC** | The `.proto` file is the single source of truth; client/server code is generated |

---

## Part 1 — The concepts (read this if any word below is fuzzy)

### What's a "recipient" vs a "request"?

- A **recipient** = one person who gets an email (an address + an unsubscribe token).
  It's **data**, not a network call.
- A **request** = one network call from core → notifier ("please send this out").
- **One request carries a *list* of recipients.** Sending a release to 100 people is
  **1 request** containing **100 recipients** — not 100 requests.

```
ONE request  ─►  "Release v1.24.0 of golang/go is out. Email these 100 people:
                  [recipient, recipient, … ×100]"
```

This is identical for HTTP and gRPC. The **only** difference is how that one request
(with its list) is packed into bytes:

| | HTTP | gRPC |
|---|---|---|
| Encoding | JSON **text** — repeats `"email":`/`"unsubscribe_token":` for every recipient | Protobuf **binary** — tiny numeric tags instead of field names |
| One recipient on the wire | `{"email":"a@x.com","unsubscribe_token":"…"}` | `0a 20 a@x.com 12 1c …` (binary, no field names) |

### `N` = how many recipients are in one request

We test `N = 1, 100, 1000, 10000`. These are **stress-test sizes** to see how cost
*scales*, not real traffic — this app realistically emails tens to a few hundred
people per release. `N=1` exposes the cost of *making a call at all*; `N=10000`
exposes the cost of *moving a big payload*.

### Two ops being measured

| Operation | What it is | What it isolates |
|---|---|---|
| `SendReleaseNotifications` | 1 request, a list of N recipients | **payload scaling** (the list grows) |
| `SendConfirmation` | 1 request, 1 person | **per-call overhead** (payload removed) |

### Latency vs Throughput — two different questions

- **Latency** ("how long does *one* request take?") → measured by `make bench`,
  which fires requests **one at a time**.
- **Throughput** ("how many requests/sec can it handle when *many* are in flight at
  once?") → measured by `make bench-parallel`, which fires requests **concurrently**.

These can disagree! A transport can have *worse* latency but *better* throughput.
That's exactly what happens here, and it's the most interesting finding.

### The four numbers in every result row

```
benchmark                               runs       ns/op       B/op     allocs/op
BenchmarkSendReleaseNotifications_gRPC/N=100-10   1640   726905   571953   8199
```

| Column | Meaning | Better |
|---|---|---|
| **runs** | how many times Go ran it to average over ~1 second. Just bookkeeping — fast ops get more runs | — |
| **ns/op** | **latency: nanoseconds per request** (1,000,000 ns = 1 ms). The headline number | lower |
| **B/op** | memory allocated per request | lower |
| **allocs/op** | number of separate memory allocations per request | lower |

The `-10` / `-8` / `-64` suffix on the name = **how many requests were in flight at
once** (the concurrency level). In `make bench` it's `-10` (your 10 CPU cores) but
requests still run one-at-a-time. In `make bench-parallel` it's the real knob.

**Handy conversion:** under the concurrent benchmark, **requests/sec = 1,000,000,000 ÷ ns/op**.
Lower ns/op = more requests/sec.

---

## Part 2 — How to run it

```bash
# LATENCY — one request at a time:
make bench

# THROUGHPUT — many requests at once, swept across 1 / 8 / 64 concurrent:
make bench-parallel

# Proof both transports return identical results + the wire-size table:
go test -v ./bench/...
```

For numbers stable enough to quote, average several runs:

```bash
go test -bench=. -benchmem -run='^$' -benchtime=2s -count=5 ./bench/... | benchstat -
```

---

## Part 3 — Results: LATENCY (one request at a time)

`make bench` — lower ns/op is faster.

| Operation | N (recipients) | gRPC (ns/op) | HTTP (ns/op) | Winner |
|---|---|---|---|---|
| SendConfirmation | 1 | 87,176 (~87 µs) | **67,131 (~67 µs)** | **HTTP** ~30% faster |
| SendReleaseNotifications | 1 | 85,342 (~85 µs) | **58,200 (~58 µs)** | **HTTP** ~32% faster |
| SendReleaseNotifications | 100 | **726,905 (~0.73 ms)** | 809,134 (~0.81 ms) | **gRPC** ~11% faster |
| SendReleaseNotifications | 1000 | **5,989,950 (~6.0 ms)** | 6,819,753 (~6.8 ms) | **gRPC** ~12% faster |
| SendReleaseNotifications | 10000 | **57,606,196 (~57.6 ms)** | 61,431,393 (~61.4 ms) | **gRPC** ~6% faster |

**What this says, in plain terms:**

- **For tiny requests, HTTP is faster.** When the payload is tiny, the cost is just
  "make a call." gRPC's HTTP/2 framing + its interceptor chain (auth, logging,
  metrics, recovery) + Protobuf setup costs more than a warm, reused HTTP/1.1
  connection doing one small `json.Decode`.
- **Around N≈100 they cross over**, and gRPC stays modestly ahead for bigger lists,
  because now the *payload* dominates and Protobuf packs the big list more efficiently
  than JSON (which repeats field names for every recipient).
- **The gap is never huge** — single-digit to low-double-digit percent. Nobody wins
  by 2×.

A useful way to see scaling — cost **per recipient** for gRPC: `85 µs` at N=1 →
`~6 µs` at N=1000+. The N=1 number is almost all fixed per-call overhead; by N=1000
that overhead is spread thin and you're paying the true marginal `~6 µs`/recipient.

---

## Part 4 — Results: THROUGHPUT (many requests at once)

`make bench-parallel` — this is the "send a LOT of requests" test. Below, ns/op is
converted to **requests/sec** (higher = better). Concurrency = how many requests are
in flight simultaneously.

### Tiny requests (`SendConfirmation`) — the clearest story

| Concurrency | gRPC req/s | HTTP req/s | Notes |
|---|---|---|---|
| 1 | ~18,200 | **~21,800** | HTTP wins when serial (matches the latency result) |
| 8 | ~41,200 | **~54,800** | both ~3× up; HTTP still ahead |
| 64 | **~40,400** | ~26,400 | **HTTP collapses; gRPC holds steady** |

**This is the headline.** Pile on concurrency and the transports *swap places*:

- **gRPC stays flat (~40k req/s)** because it multiplexes all 64 concurrent requests
  over **one HTTP/2 connection**. More load → no extra connections → no thrash.
- **HTTP/1.1 degrades (54k → 26k req/s)** because it's one-request-per-connection. At
  64-in-flight it churns its TCP connection pool — notice its `B/op` nearly **doubles**
  (16.6 KB → 31.6 KB) from connection management overhead.

So: **HTTP is faster when lightly loaded; gRPC is more stable under heavy concurrent
load.** On a *real* network (not loopback) this gap widens further, since gRPC also
avoids repeated connection setup.

### Bigger requests — throughput becomes payload-bound

Once each request carries a real payload, the bottleneck shifts from "managing
connections" to "CPU composing + serializing the list," so the transport matters less:

| N (recipients) | gRPC req/s @ conc 8 | HTTP req/s @ conc 8 | Verdict |
|---|---|---|---|
| 1 | ~46,800 | **~58,200** | HTTP ahead (tiny payload) |
| 100 | ~3,940 | **~4,130** | basically tied |
| 1000 | **~620** | ~490 | gRPC ~25% ahead |
| 10000 | ~60 | ~62 | tied — limited by CPU, not transport |

At N=10000, both do ~60 release-calls/sec — and each call emails 10,000 people, so
that's **~600,000 recipients/sec** either way. Far beyond anything this app needs.

---

## Part 5 — Results: BYTES ON THE WIRE (the consistent gRPC win)

Independent of speed, how big is each request on the wire? (`go test -run TestWireSize -v ./bench/...`)

| Operation | N | Protobuf (B) | JSON (B) | JSON ÷ Proto |
|---|---|---|---|---|
| SendConfirmation | 1 | 93 | 149 | 1.60× |
| SendReleaseNotifications | 1 | 135 | 209 | 1.55× |
| SendReleaseNotifications | 100 | 6,471 | 9,515 | 1.47× |
| SendReleaseNotifications | 1000 | 64,071 | 94,115 | 1.47× |
| SendReleaseNotifications | 10000 | 640,071 | 940,115 | 1.47× |

**Protobuf is ~1.5× smaller than JSON at every size**, because JSON repeats the field
names (`"email":`, `"unsubscribe_token":`) and quotes for every recipient, while
Protobuf uses tiny numeric tags. This is the one gRPC advantage that holds at *all*
sizes and concurrency levels.

---

## Part 6 — Pros & cons, side by side

### gRPC (Protobuf / HTTP/2)
**Pros**
- ✅ ~1.5× smaller payloads on the wire — always.
- ✅ Stable under high concurrency — one multiplexed connection, no pool thrash.
- ✅ Modest latency edge for larger payloads (N ≥ ~100).
- ✅ **Typed, generated contract** — the `.proto` is the single source of truth, so
  client and server can't silently drift apart.
- ✅ First-class deadlines, status codes, streaming, and reusable interceptors.

**Cons**
- ❌ Slower for tiny, infrequent requests at low concurrency.
- ❌ Slightly more allocations per call.
- ❌ Binary format — can't just `curl` it or read it by eye (need `grpcurl`).
- ❌ More setup: a `.proto` compiler and generated code in the build.
- ❌ Not browser-native (needs grpc-web or a gateway).

### HTTP / JSON (HTTP/1.1)
**Pros**
- ✅ Faster for tiny requests at low concurrency.
- ✅ Human-readable — trivial to debug with `curl` or a browser.
- ✅ Universal — every language, tool, and proxy speaks it.
- ✅ Minimal setup, no code generation.

**Cons**
- ❌ ~1.5× bigger payloads (repeats field names per item).
- ❌ Throughput **degrades** under high concurrency (connection-pool thrash).
- ❌ No enforced contract — request/response shapes are hand-maintained on both sides
  and can drift out of sync.
- ❌ More memory churn under concurrent load.

---

## Part 7 — So why does this app use gRPC?

Honestly: **not for raw speed.** This service polls a few repos every 5 minutes and
fans out to a repo's confirmed subscribers — realistically tens to a few hundred
recipients, at low concurrency. In that regime the measured differences are tens of
microseconds to a couple of milliseconds per release — invisible next to a 5-minute
poll cycle, and HTTP is even marginally *faster* for the tiny confirmation emails.

gRPC is the runtime transport because of the things that **don't** show up as a
latency number:

1. **Typed contract that can't drift.** `proto/notifier.proto` generates both the
   client and server code, so the cross-service boundary stays in sync by construction
   — no hand-written JSON structs maintained in two places.
2. **Tooling & operability.** Built-in deadlines, status codes, and a reusable
   interceptor chain (auth, correlation, metrics, recovery) — and it's streaming-ready.
3. **Future-proofing for async.** The same contract becomes the event the notifier
   consumes once a message broker is added.

**The fair summary:** at this scale you'd lose almost nothing on speed with HTTP — and
gain easy debuggability. gRPC wins on **contract safety, wire size, and stability under
concurrency**, which is the right trade for *internal service-to-service* calls but not
an obvious one for a public, browser-facing API.

---

## How this comparison is kept fair

- **Same business logic, both transports.** Both servers wrap one `*notifier.Core`
  over a no-op counting mailer — email sending is stubbed, so we measure transport +
  (de)serialization, **not** SMTP.
- **Provably identical behavior.** `TestTransportsEquivalent` asserts both transports
  return the same result (`sent`/`failed`) for the same input at every N. So any
  measured delta is transport-only.
- **Warm, persistent connections on both sides** — one gRPC channel and one pooled
  keep-alive HTTP client, each established once. Neither pays per-call connection setup.
  HTTP compression is off so we compare raw JSON bytes.
- **Auth on, symmetric.** The same bearer token is checked with a constant-time compare
  on both sides — equal cost, no thumb on the scale.
- **Plaintext, loopback (`127.0.0.1`), one machine, one tool** (`testing.B`) — no
  cross-tool variance from `ghz`/`hey`/`k6`.
- **Deterministic payloads**, warm-up before timing, `b.ResetTimer()` after setup,
  `b.ReportAllocs()`.

## Environment & caveats

- Machine: Apple M1 Pro · darwin/arm64 · Go 1.26.
- Numbers above are single `-count=1` runs on **loopback** — treat **±10% as noise**,
  and the smallest-sample rows (N=10000, ~20 runs) as the least precise. The
  HTTP-collapse-at-concurrency-64 effect is real and repeatable in direction, but the
  exact magnitude wobbles run to run.
- For anything you'd quote as fact, re-run with `-benchtime=2s -count=5` and compare
  with `benchstat`.
