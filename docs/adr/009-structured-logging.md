# ADR-009: Structured Logging with stdlib log/slog

## Status

Accepted

---

## Context

Ad-hoc `log.Printf` calls across `scanner`, `service`, `api`, and `github`
produce unstructured text with no severity levels and no machine-readable fields.
A log aggregator cannot filter by level or field, errors cannot be routed to
alerting, and there is no hook to redact PII before emission.

---

## Decision

Adopt `log/slog` (stdlib, since Go 1.21) as the sole logging backbone — no
third-party dependency. Implemented in `internal/observability/logging`:

- `Config{Level, Format}` — `LOG_LEVEL` (debug|info|warn|error) and `LOG_FORMAT`
  (json|text), validated in `internal/config`. `AddSource` is on only at debug.
- `NewLogger(cfg, w)` — JSON handler for prod (default), custom `TextHandler` for
  dev: Gin-style colored lines (TTY-gated, honors `NO_COLOR`/`FORCE_COLOR`), the
  `err` attr unwound via `errors.Unwrap`, and `Value.Resolve()` on every attr so
  `slog.LogValuer` can redact secrets/PII. `WithGroup` is a no-op (JSON concern).
- `main.go` calls `slog.SetDefault`; its one pre-logger failure uses
  `fmt.Fprintf(os.Stderr, ...)`. Call sites use `*Context` level calls and log
  entity IDs, not raw structs.

---

## Consequences

### Positive

- Structured fields + real levels → aggregator filtering and alerting, no regex.
- Env-tunable: JSON for prod, colored text for dev. Zero new deps.
- PII redaction via `LogValuer`; the `slog.Handler` interface isolates call sites
  from output format.

### Negative

- `TextHandler` is hand-maintained code the project owns and must keep tested.
- `WithGroup` drops the group prefix in text mode (text vs. JSON asymmetry).
- Slower per call than `zap`/`zerolog`; logging sits off the hot path (one record
  per scan tick and per HTTP request), so the cost does not bind.

---

## Alternatives Considered

- **uber-go/zap / rs/zerolog** — faster, but add a dependency and a non-`slog`
  call-site idiom (locking in a future re-migration). Their throughput edge does
  not apply when logging is off the hot path. Rejected per "least change, no deps".
- **log.Printf wrapper** — quasi-structured text parsers can't reliably ingest,
  levels stay advisory strings, no redaction hook. Defers cost without fixing it.

---

## References

- log/slog: <https://pkg.go.dev/log/slog>
- NO_COLOR: <https://no-color.org>
- ADR-007 (layering), ADR-008 (testing strategy — covers `TextHandler` tests).
