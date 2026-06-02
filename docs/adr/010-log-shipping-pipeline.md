# ADR-010: Log shipping to Elasticsearch via a Filebeat sidecar

## Status

Accepted

---

## Context

ADR-009 made the app emit structured JSON logs to stdout, but they stop at `docker logs`:
ephemeral, per-container, ungreppable in aggregate. We want them centralized in
Elasticsearch and searchable/aggregatable in Kibana, while the app stays a pure log
producer (no ES client, no ES dependency). The question is *how* the JSON travels from
container to ES — and how *reliably*.

---

## Decision

Ship logs with a **Filebeat sidecar that tails Docker's container log files**:
`app (slog JSON → stdout) → Docker json-file logs → Filebeat → Elasticsearch → Kibana`.

- `deploy/filebeat/filebeat.yml`: tail `/var/lib/docker/containers/*/*.log`,
  `add_docker_metadata`, keep **only the app container** via `drop_event` on the Compose
  service label, `decode_json_fields` to root, and a `timestamp` processor mapping slog
  `time` → `@timestamp`.
- Plain date-based index `repo-release-notifier-%{+yyyy.MM.dd}`, managed template/ILM
  disabled → ES auto-creates it with dynamic mapping.
- Lives in a separate `docker-compose.observability.yml` (run combined via `-f`). The app
  keeps the default `json-file` driver; the only app change is `LOG_FORMAT=json`.

Local/dev posture only: ES security and TLS are disabled.

---

## Consequences

**Positive** — Filebeat reads the on-disk files with a position registry, so it survives
restarts, back-fills what it missed, and never silently drops logs; the app is fully
decoupled from ES and the collector; slog fields land as first-class ES fields; app-only
filtering is clean.

**Negative** — needs a privileged read-only mount of `/var/lib/docker/containers` + the
Docker socket and runs as `root`; Filebeat is heavier than a minimal forwarder; tailing
all containers then filtering is marginally more config than a push setup.

---

## Alternatives Considered

- **Docker `fluentd` driver + Fluent Bit** (tried, reverted). Minimal — only the app uses
  the driver, so no mounts/socket/root/filter. Reverted because with `fluentd-async` it
  **silently drops** logs on any connection blip and is coupled to the engine's stdout
  reader: on an unstable engine it shipped the startup burst but dropped per-request logs
  with no error. Reliability beat minimalism.
- **Logstash stage.** Wrong layer — a JVM transformer that runs *after* collection, so it
  still needs a shipper in front and doesn't help collection reliability. Our logs are
  already final-shape JSON; nothing to grok/enrich. Heavy, with no job.
- **Direct from app (custom `slog.Handler` → go-elasticsearch).** Not a free swap: you'd
  write the handler plus an async bounded buffer, batching, backpressure, retry, and
  shutdown-flush — exactly what Filebeat already provides. Its one upside is bypassing the
  container-log layer (`app → HTTP → ES`). Rejected for decisive costs: **logs lost on
  crash** (the in-memory buffer dies with the process), **app runtime coupled to ES**
  (stalls stress the app; a bad buffer policy means drops or OOM), **`docker logs` lost**
  unless dual-writing, and a transport concern leaking into app code (ADR-007) — the
  non-standard pattern. This is why the scratch `elasticsearch.NewTyped(...)` was deleted
  from `main.go`: the app holds no ES client unless it must query/index ES as a product
  feature, which it does not.

---

## Rollout

Add `docker-compose.observability.yml` + `deploy/filebeat/filebeat.yml`, set
`LOG_FORMAT=json` (override only), bring the stack up, create the `repo-release-notifier-*`
Kibana data view. No migration, no app behavior change. Rollback: run without the override
file. Notes: debugging moves from `docker logs | grep` to Kibana; ES heap is bounded via
`ES_JAVA_OPTS` for laptops; a shipper switch that changes a field's shape (e.g. `log`
string → object) needs a fresh, Filebeat-owned index.

---

## References

- ADR-009 (structured logging), ADR-007 (layering)
- Filebeat: <https://www.elastic.co/guide/en/beats/filebeat/current/index.html>
