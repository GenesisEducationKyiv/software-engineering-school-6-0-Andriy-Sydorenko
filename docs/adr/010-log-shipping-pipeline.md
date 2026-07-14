# ADR-010: Log shipping to Elasticsearch via a Filebeat sidecar

## Status

Accepted

---

## Context

ADR-009 made the app emit structured JSON to stdout, but logs stop at `docker logs`:
ephemeral, per-container, ungreppable in aggregate. We want them centralized in
Elasticsearch and searchable in Kibana, while the app stays a pure log producer (no ES
client, no ES dep). The question is *how* the JSON travels — and how reliably.

---

## Decision

A **Filebeat sidecar that tails Docker's container log files**:
`app (slog JSON → stdout) → Docker json-file logs → Filebeat → Elasticsearch → Kibana`.

- `deploy/filebeat/filebeat.yml`: tail `/var/lib/docker/containers/*/*.log`,
  `add_docker_metadata`, keep only the app container (`drop_event` on the Compose
  service label), `decode_json_fields` to root, map slog `time` → `@timestamp`.
- Date-based index `repo-release-notifier-%{+yyyy.MM.dd}`; ES auto-creates it with
  dynamic mapping.
- Lives in `docker-compose.observability.yml`; the only app change is `LOG_FORMAT=json`.

Local/dev only: ES security and TLS are disabled.

---

## Consequences

- **+** Filebeat's on-disk position registry survives restarts and back-fills — no silent
  drops; the app is fully decoupled from ES; slog fields land as first-class ES fields.
- **−** Needs a read-only mount of `/var/lib/docker/containers` + the Docker socket and
  runs as root; heavier than a minimal forwarder.

---

## Alternatives Considered

- **Docker `fluentd` driver + Fluent Bit**. Minimal — no
  mounts/socket/root. Reverted because `fluentd-async` **silently drops** logs on any
  connection blip: on an unstable engine it shipped the startup burst but lost
  per-request logs with no error. Reliability beat minimalism.
- **Logstash.** Wrong layer — a JVM transformer *after* collection; still needs a shipper
  in front, and our logs are already final-shape JSON. Heavy, no job.
- **Direct from app (custom `slog.Handler` → go-elasticsearch).** Would mean hand-writing
  async buffering, batching, backpressure, retry, and shutdown-flush — exactly what
  Filebeat already gives. Rejected: **logs lost on crash** (in-memory buffer dies with the
  process), app runtime coupled to ES, `docker logs` lost unless dual-writing, and a
  transport concern leaking into app code (ADR-007). Accordingly the app holds no ES
  client.

---

## Rollout

Add the overlay + `filebeat.yml`, set `LOG_FORMAT=json`, bring the stack up, create the
`repo-release-notifier-*` Kibana data view. No migration, no app behavior change.
Rollback: run without the overlay. ES heap is bounded via `ES_JAVA_OPTS` for laptops.

---

## References

- ADR-009 (structured logging), ADR-007 (layering)
- Filebeat: <https://www.elastic.co/guide/en/beats/filebeat/current/index.html>
