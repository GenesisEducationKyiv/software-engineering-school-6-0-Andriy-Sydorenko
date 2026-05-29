# Design: Ship structured logs to Elasticsearch + Kibana

**Date:** 2026-05-29
**Status:** Approved (pending spec review)
**Scope:** Log delivery + visualization only. Metrics (Prometheus) and dashboards (Grafana) are a separate, later slice.

## Problem

The app already emits structured logs via `slog` (`internal/observability/logging`), but they only reach a
terminal / `docker logs`. That makes them ephemeral, scattered across containers, and impossible to search or
aggregate by field. We want centralized, queryable logs: search by `level`/`component`/`repo`, aggregate over
time, and visualize in a UI.

## Goal / non-goals

**Goals**
- Centralize the app's logs in Elasticsearch, indexed by their structured fields.
- Make them searchable and aggregatable in Kibana.
- Keep the app fully decoupled from the log backend (no ES code, no ES dependency in `go.mod`).
- Add no new Go dependencies; touch as little existing code as possible.

**Non-goals (deferred to a later slice)**
- RED metrics, Prometheus, Grafana.
- Authentication/TLS on the Elastic stack (local/dev posture only).
- ECS field normalization, ILM/retention policies, multi-source ingestion.

## Key finding

This slice requires **essentially zero Go code**. When run with `LOG_FORMAT=json`, the app's stdlib
`slog.NewJSONHandler` already emits final-shape JSON to stdout:

```json
{"time":"2026-05-29T20:48:00.123Z","level":"ERROR","msg":"scanner: repo check failed","component":"scanner","repo":"golang/go","err":"github: rate limited"}
```

These are already first-class fields for Elasticsearch. The work is purely a delivery + visualization layer
bolted around the running container.

## Architecture

```
app (slog JSON → stdout) → Docker json-file logs → Filebeat → Elasticsearch → Kibana
```

The app stays a pure log *producer*. A Filebeat sidecar tails the Docker container log files, parses the
slog JSON, and ships to Elasticsearch. Kibana visualizes.

> **Approach iterated during implementation.** A push-based variant (Docker `fluentd` log driver → Fluent
> Bit) was tried for minimalism — only the app uses the driver, so no host mounts, socket, root, or filter.
> It was **reverted to Filebeat file-tailing** because the driver silently dropped per-request logs on
> connection blips (`fluentd-async`) and was coupled to the engine's stdout reader — unreliable on a flaky
> engine. Reliability beat minimalism. See **ADR-010** for the full decision.

### Why no Logstash

Lightweight shippers (Filebeat) and Logstash (heavyweight JVM transformation engine) are complementary, not
competitors — and Logstash is the wrong layer: it processes *after* collection, so it still needs a shipper
in front and doesn't address collection reliability. Our logs are already clean JSON — no grok/mutate/enrich
work. A Logstash stage would be infrastructure with no job. Insertable later if unstructured sources appear.

### Why a separate compose file

Keeps the lean app stack (db/redis/app) independent of the heavy observability stack. Bring the stack up only
when needed, combined via:

```
docker compose -f docker-compose.yml -f docker-compose.observability.yml up
```

Combining with `-f` puts all services in one Compose project on the shared default network, so Filebeat can
reach Elasticsearch by service name and the daily index is reachable from Kibana.

## Components

### 1. `docker-compose.observability.yml` (new)

- **elasticsearch** (8.15.3): `discovery.type=single-node`, `xpack.security.enabled=false` (local only),
  bounded JVM heap via `ES_JAVA_OPTS`, fixed `hostname` (8.x log4j needs a resolvable hostname), `esdata`
  named volume, healthcheck on `:9200`.
- **kibana** (8.15.3): `ELASTICSEARCH_HOSTS=http://elasticsearch:9200`, port `5601`, `depends_on` ES healthy.
- **filebeat** (8.15.3): tails `/var/lib/docker/containers/*/*.log` (read-only mount) via the Docker socket;
  runs as root with `--strict.perms=false`; `depends_on` ES healthy.
- **app override:** enables `LOG_FORMAT=json` **only in this file**, so the base stack is untouched. The app
  keeps the default `json-file` driver.

### 2. `deploy/filebeat/filebeat.yml` (new)

- **Input:** static `type: container` over `/var/lib/docker/containers/*/*.log`.
- **Processors:** `add_docker_metadata`; `drop_event` keeping only the app container (Compose service label);
  `decode_json_fields` merges the slog JSON to root; `timestamp` maps slog `time` → `@timestamp`; `drop_fields`
  removes the raw `time`.
- **Output:** `output.elasticsearch` → plain daily index `repo-release-notifier-%{+yyyy.MM.dd}`,
  `setup.ilm.enabled: false` + `setup.template.enabled: false` so ES auto-creates it with dynamic mapping.

### 3. `docker-compose.yml` — unchanged

The base file is untouched. `LOG_FORMAT=json` is applied as an `app` override in
`docker-compose.observability.yml`; a plain `docker compose up` keeps text logs and the default driver.

### 4. Kibana data view (documented, not provisioned)

A data view tells Kibana which indices to read and which time field to use; without it Kibana has no entry
point to the data. Create `repo-release-notifier-*` on time field `@timestamp`:
- **Manual:** Stack Management → Data Views → Create → pattern `repo-release-notifier-*`, time field
  `@timestamp`. (~20s, persists in Kibana storage.)
- **Optional scripted:** a documented `curl` against the Kibana Data Views API for those who prefer it.

Chosen over auto-provisioning to avoid an extra init container with health-wait/idempotency logic for a stack
brought up occasionally.

### 5. `docs/adr/010-log-shipping-pipeline.md` (new)

Short ADR recording the pipeline decision: Filebeat file-tailing over a push driver (Fluent Bit), a Logstash
stage, and direct-from-app shipping; complements ADR-009 (structured logging).

### 6. README observability section (edit)

Document: bring the combined stack up, generate logs (hit the API / let the scanner tick), create the data
view, find logs in Kibana Discover, and run one example aggregation (e.g. count by `level` or `component`
over time).

## Data contract (slog JSON → ES fields)

| slog key   | ES field      | Type    | Notes                                  |
|------------|---------------|---------|----------------------------------------|
| `time`     | `@timestamp`  | date    | mapped by Filebeat's `timestamp` processor; raw `time` dropped |
| `level`    | `level`       | keyword | INFO/WARN/ERROR/DEBUG — aggregatable   |
| `msg`      | `msg`         | text    | log message                            |
| `component`| `component`   | keyword | e.g. `scanner` (where set via `slog.With`) |
| `repo`     | `repo`        | keyword | per-event attrs                        |
| `err`      | `err`         | text    | error chain string                     |

Dynamic mapping is acceptable for this slice; no custom index template fields required beyond the index
name/pattern.

## Risks & mitigations

- **Filebeat ingests unintended container logs** → scope by the Compose service label (`drop_event`).
- **Shipper switch changes field shapes** (e.g. `log` string → object) → 400s on the old index; use a fresh
  Filebeat-owned index.
- **Privileged access** (Docker socket + `/var/lib/docker/containers`, root) → read-only mounts; local/dev only.
- **ES memory pressure on a laptop** → bound `ES_JAVA_OPTS` heap; single-node.
- **ES 8.x won't boot without a resolvable hostname** → set `hostname: elasticsearch`.
- **Security disabled** → explicitly local/dev-only; called out in README and ADR as a non-goal.

## Testing / verification

No Go code changes, so existing unit/integration/e2e suites are unaffected (sanity: `make test-unit`).

Manual verification (infra slice):
1. `docker compose -f docker-compose.yml -f docker-compose.observability.yml up`.
2. Generate activity (subscribe call / scanner tick).
3. Elasticsearch: `curl localhost:9200/_cat/indices` shows `repo-release-notifier-YYYY.MM.dd`.
4. Kibana Discover (after data view): log lines appear with `level`, `msg`, `container_name` (and slog attrs
   like `component`/`repo` where set) as fields and correct `@timestamp`.
5. Run one aggregation (count by `level.keyword` over time) to prove search + aggregation works.

## Follow-ups / known gaps

- **Gin access/debug logs are unstructured.** The router uses `gin.Default()`, whose logger writes plain-text
  `[GIN]`/`[GIN-debug]` lines (not slog JSON). They still reach ES but `decode_json_fields` can't parse them,
  so they index as raw text in `message` (no `level`/`msg`). To make the app's logs fully structured, route
  Gin through slog (custom middleware) and set `GIN_MODE=release`. App-code change — out of this slice's scope.
- **Data view `.keyword` for aggregations.** Dynamic mapping makes string fields `text` with a `.keyword`
  subfield; Kibana aggregations use e.g. `level.keyword`. A custom index template with explicit `keyword`
  mappings would let you aggregate on the bare field name — deferred (dynamic mapping is acceptable here).

## As-built verification (2026-06-01)

Verified end to end with the Filebeat pipeline: app emits slog JSON on the default `json-file` driver;
Filebeat tails the container logs and delivers **app-only** docs (`container.name=…app-1`, 22/22) to a
**plain** daily index `repo-release-notifier-2026.06.01`; structured slog fields + `@timestamp`, and Gin
access lines as raw `message`; **request-time logs captured** (the reverted push driver had silently dropped
them); Kibana `repo-release-notifier-*` data view works; aggregation by `level.keyword` works.

> Note: a `log` string→object mapping conflict appears if Filebeat writes to an index previously created by a
> different shipper — delete the old index so Filebeat owns the mapping.

## Rollback

Run without `docker-compose.observability.yml` — the app keeps the default json-file driver and `docker logs`,
with no app behavior change. No schema/data migrations to revert.
