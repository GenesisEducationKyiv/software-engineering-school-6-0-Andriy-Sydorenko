-- Reverse: recreate the watched_repos cursor, then drop public_id.
CREATE TABLE IF NOT EXISTS watched_repos (
    repo           VARCHAR(255) PRIMARY KEY,
    last_seen_tag  VARCHAR(255) NOT NULL DEFAULT '',
    last_polled_at TIMESTAMPTZ  NOT NULL DEFAULT now()
);

DROP INDEX IF EXISTS idx_subscriptions_public_id;
ALTER TABLE subscriptions DROP COLUMN IF EXISTS public_id;
