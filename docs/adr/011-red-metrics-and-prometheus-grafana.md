# ADR-011: RED Metrics via Prometheus and Grafana

## Status

Accepted

---

## Context

ADR-009 gave the app structured logs; ADR-010 ships them to Elasticsearch for search and
aggregation. Both are retrospective: they tell you what happened after the fact. What is
missing is a real-time operational signal — request rate, error rate, and latency — that
drives alerting and live dashboards without grepping logs. Logs cannot feed a
`histogram_quantile` for p95 latency or a `rate()` panel for RPS without a bespoke
aggregation pipeline. Prometheus metrics are the standard complement.

---

## Decision

Instrument the Gin HTTP app with a single histogram and expose it to Prometheus; visualize
in Grafana provisioned as code alongside the existing observability compose overlay.

**Metric shape.** One histogram `http_request_duration_seconds` with labels `method`,
`route`, and `status` covers all three RED signals:

- **Rate** — `rate(http_request_duration_seconds_count[5m])`
- **Errors** — same, filtered to `status=~"5.."`
- **Duration** — `histogram_quantile(0.95, rate(http_request_duration_seconds_bucket[5m]))`

One metric, no separate counters or gauges to drift out of sync.

**Cardinality control.** The `route` label uses `c.FullPath()` — the Gin route template
(e.g. `/api/confirm/:token`), not the raw request path. Requests that match no registered
route (404s from unknown paths) are bucketed under a fixed label `"unmatched"` instead of
their raw path, preventing unbounded cardinality from crawlers and scanners.

**Implementation.** A Gin middleware in `internal/api` wraps each handler, starts a timer,
and records the observation after the handler returns. `promhttp.Handler()` is registered at
`GET /metrics` in `router.go` before the API-key-protected group — the same posture as
`/health`, which is also unauthenticated. One new direct dependency:
`github.com/prometheus/client_golang`.

**Deployment.** `prometheus` (`prom/prometheus`, scrapes `app:8080/metrics`) and `grafana`
(`grafana/grafana`) are added to `docker-compose.observability.yml`, keeping the base
compose unchanged. Grafana is provisioned as code under `deploy/grafana/provisioning/`:
a datasource YAML pointing at Prometheus and a dashboard-provider YAML loading a
hand-authored RED dashboard JSON. The dashboard survives `compose down -v` and is
version-controlled.

---

## Consequences

### Positive

- Real-time rate, error, and latency signals with no aggregation pipeline; `histogram_quantile`
  works across restarts and multiple instances (unlike summaries).
- Single metric, single middleware — minimal instrumentation surface.
- Grafana provisioned as code: reproducible, diff-able, no manual UI setup.
- Observability overlay stays modular: base compose is unaffected.

### Negative

- `github.com/prometheus/client_golang` is a new direct dependency; the histogram allocates
  one bucket slice per (method × route × status) series — small but fixed memory for each
  unique label combination.
- `/metrics` is unauthenticated; it exposes request rates and route shapes (acceptable in
  dev/internal environments, but must be network-restricted before any public deployment).
- Two additional containers (`prometheus`, `grafana`) added to the observability overlay,
  increasing local resource use alongside the existing Elasticsearch + Kibana + Filebeat trio.
- Hand-authored dashboard JSON requires manual updates when new routes are added.

---

## Alternatives Considered

### Separate counter + summary per RED signal

A `http_requests_total` counter for rate/errors and a `http_request_duration_summary` for
latency. Rejected: summaries compute quantiles client-side over a sliding window and
**cannot be aggregated across instances**, so p95 across N replicas is meaningless. A
histogram's buckets can be summed and fed to `histogram_quantile` on the Prometheus server.
Three separate metrics also multiply the places where a label mismatch silently breaks a
query.

### Raw URL path as `route` label

Use `c.Request.URL.Path` instead of `c.FullPath()` for richer per-path detail. Rejected:
every unique token, UUID, or cursor in a path becomes a distinct label value; a handful of
active users would produce thousands of series, violating Prometheus cardinality best
practices and degrading TSDB performance.

### Manual Grafana datasource and dashboard setup

Document the click-through in a runbook. Rejected: not reproducible, not version-controlled,
and lost on `compose down -v`. Provisioning as code is the direct parallel to how
`filebeat.yml` is shipped for ADR-010.

---

## References

- ADR-009 (structured logging), ADR-010 (log shipping)
- Prometheus histograms: <https://prometheus.io/docs/practices/histograms/>
- client_golang: <https://pkg.go.dev/github.com/prometheus/client_golang/prometheus>
- Grafana provisioning: <https://grafana.com/docs/grafana/latest/administration/provisioning/>
