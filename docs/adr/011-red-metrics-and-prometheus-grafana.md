# ADR-011: RED Metrics via Prometheus and Grafana

## Status

Accepted

---

## Context

ADR-009/010 give retrospective logs. Missing is a real-time operational signal —
request rate, error rate, latency — to drive alerting and live dashboards. Logs can't
feed `histogram_quantile` for p95 or `rate()` for RPS without a bespoke pipeline.
Prometheus metrics are the standard complement.

---

## Decision

Instrument the Gin app with a single histogram, expose it to Prometheus, and visualize
in Grafana provisioned as code on the existing observability overlay.

**Metric.** One histogram `http_request_duration_seconds{method,route,status}` covers
all three RED signals:

- **Rate** — `rate(http_request_duration_seconds_count[5m])`
- **Errors** — same, filtered to `status=~"5.."`
- **Duration** — `histogram_quantile(0.95, rate(..._bucket[5m]))`

One metric, no separate counters/gauges to drift out of sync.

**Cardinality.** The `route` label uses `c.FullPath()` (the route template, e.g.
`/api/confirm/:token`), not the raw path; unmatched routes bucket under a fixed
`"unmatched"`, and off-list HTTP verbs collapse to `"OTHER"` — both bound cardinality
against crawlers.

**Wiring.** A Gin middleware in `internal/api` times each request in a `defer`,
recording the histogram and a structured slog access log with the same RED dimensions
(so panic-recovered 500s land in both); it replaces `gin.Logger()`, whose plain text
didn't fit the JSON log pipeline (ADR-010). `promhttp.Handler()` is at `GET /metrics`,
unauthenticated like `/health`. Prometheus scrapes `app:8080`; Grafana loads a
provisioned datasource + RED dashboard from `deploy/grafana/provisioning/`. Both run in
`docker-compose.observability.yml`; the base compose is untouched. One new direct
dependency: `github.com/prometheus/client_golang`.

---

## Consequences

- **+** Real-time rate/error/latency with no aggregation pipeline; histograms aggregate
  across instances and restarts (unlike summaries). Single metric + middleware. Grafana
  as code: reproducible, survives `compose down -v`.
- **−** New dep; `/metrics` is unauthenticated (exposes route shapes — network-restrict
  before any public deploy); two more containers; the dashboard JSON needs manual edits
  when routes change.

---

## Alternatives Considered

- **Counter + summary per signal.** Summaries compute quantiles client-side and **can't
  aggregate across instances**, so p95 across replicas is meaningless; histograms sum on
  the server. Three metrics also multiply label-mismatch bugs.
- **Raw URL path as `route`.** Every token/UUID becomes a distinct series — thousands of
  series from a few users. Rejected for cardinality.
- **Manual Grafana setup.** Not reproducible, lost on `compose down -v`. Provisioning as
  code parallels how `filebeat.yml` ships (ADR-010).

---

## References

- ADR-009 (logging), ADR-010 (log shipping)
- Prometheus histograms: <https://prometheus.io/docs/practices/histograms/>
- client_golang: <https://pkg.go.dev/github.com/prometheus/client_golang/prometheus>
- Grafana provisioning: <https://grafana.com/docs/grafana/latest/administration/provisioning/>
