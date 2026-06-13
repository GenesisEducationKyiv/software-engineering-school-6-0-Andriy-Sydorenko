-- Per-repo release-detection state, owned by the scanner module. Supersedes
-- per-subscription subscriptions.last_seen_tag (now dead but kept; dropped in a
-- later flagged destructive migration). Additive + idempotent.

CREATE TABLE IF NOT EXISTS watched_repo (
    repo            TEXT        PRIMARY KEY,
    last_seen_tag   TEXT        NOT NULL DEFAULT '',
    last_polled_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
