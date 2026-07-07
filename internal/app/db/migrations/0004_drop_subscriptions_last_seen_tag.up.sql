-- Contract phase of the watched_repos split (0003): per-subscriber last_seen_tag
-- is dead now that the scanner tracks the release cursor per repo. 0003 already
-- backfilled watched_repos from this column, so no data is lost where it's read.
-- DROP COLUMN is metadata-only in Postgres (no table rewrite).
ALTER TABLE subscriptions DROP COLUMN IF EXISTS last_seen_tag;
