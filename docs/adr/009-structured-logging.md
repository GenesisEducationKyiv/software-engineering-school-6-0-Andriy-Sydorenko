# ADR-009: Structured Logging with stdlib log/slog

## Status

Accepted

---

## Context

Ad-hoc `log.Printf` across `scanner`, `service`, `api`, and `github` produces
unstructured text — no levels, no machine-readable fields. An aggregator can't
filter by level or field, errors can't route to alerting, and there's no hook to
redact PII.

---

## Decision

Adopt `log/slog` (stdlib, Go 1.21+) as the sole logging backbone — no third-party
dep. In `internal/observability/logging`:

- `Config{Level, Format}` from `LOG_LEVEL` (debug|info|warn|error) and `LOG_FORMAT`
  (json|text), rejected at config load if invalid; `AddSource` only at debug.
- `NewLogger` — a custom `TextHandler` for dev (the default, `LOG_FORMAT=text`) and
  stdlib JSON for prod (`LOG_FORMAT=json`). The text handler writes colored TTY lines
  (honors `NO_COLOR`/`FORCE_COLOR`), unwinds the `err` attr via `errors.Unwrap`, and
  calls `Value.Resolve()` per attr so `slog.LogValuer` can redact secrets/PII.
  `WithGroup` is a no-op (JSON-only concern).
- `main.go` configures `slog.SetDefault`; service and scanner call sites use the
  `*Context` variants and log entity IDs, not raw structs.

---

## Consequences

- **+** Real levels + structured fields → aggregator filtering and alerting, no regex.
  Env-tunable, zero new deps. PII redaction via `LogValuer`.
- **−** `TextHandler` is hand-maintained code we own and must keep tested; `WithGroup`
  drops the group prefix in text mode. Slower per call than `zap`/`zerolog`, but
  logging is off the hot path (one record per scan tick / request), so it doesn't bind.

---

## Alternatives Considered

- **zap / zerolog** — faster, but add a dep and a non-`slog` call-site idiom; their
  throughput edge doesn't apply off the hot path. Rejected per "least change, no deps".
- **log.Printf wrapper** — quasi-structured, unparseable, no redaction hook. Defers the
  cost without fixing it.

---

## References

- log/slog: <https://pkg.go.dev/log/slog>; NO_COLOR: <https://no-color.org>
- ADR-007 (layering), ADR-008 (testing — covers `TextHandler` tests).
