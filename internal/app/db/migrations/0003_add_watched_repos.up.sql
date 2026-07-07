-- Per-repo release tracking, split out of the per-subscriber subscriptions row.
-- The scanner records, once per repo, the last release tag it notified about and
-- when it last polled — instead of duplicating tag state across every
-- subscription. `repo` is the natural key (nothing references this table by id).
CREATE TABLE IF NOT EXISTS watched_repos (
    repo           VARCHAR(255) PRIMARY KEY,
    last_seen_tag  VARCHAR(255) NOT NULL DEFAULT '',
    last_polled_at TIMESTAMPTZ  NOT NULL DEFAULT now()
);

-- Backfill from existing confirmed subscriptions so the first post-migration
-- scan doesn't re-notify. MAX(last_seen_tag) keeps the furthest-seen tag per
-- repo; a lagging subscriber may miss at most the one release between their tag
-- and the repo max (one-time, and the alternative re-notifies everyone).
INSERT INTO watched_repos (repo, last_seen_tag)
SELECT repo, MAX(last_seen_tag)
FROM subscriptions
WHERE confirmed = TRUE
GROUP BY repo
ON CONFLICT (repo) DO NOTHING;
