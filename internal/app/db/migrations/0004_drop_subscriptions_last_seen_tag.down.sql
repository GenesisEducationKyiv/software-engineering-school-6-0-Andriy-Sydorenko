-- Restores the column shape (matching 0001: nullable varchar default ''), not
-- its data — the per-subscriber values are gone after the drop.
ALTER TABLE subscriptions ADD COLUMN IF NOT EXISTS last_seen_tag VARCHAR(255) DEFAULT '';
