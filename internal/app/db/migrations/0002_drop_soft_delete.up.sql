-- Drop soft-delete columns and the partial unique index that scoped
-- uniqueness to live rows. Unsubscribe is now a hard DELETE, so a
-- plain unique index on (email, repo) is sufficient and re-subscribe
-- works because the prior row no longer exists.

DROP INDEX IF EXISTS idx_email_repo_live;
DROP INDEX IF EXISTS idx_subscriptions_deleted_at;
DROP INDEX IF EXISTS idx_confirmation_tokens_deleted_at;

ALTER TABLE subscriptions       DROP COLUMN IF EXISTS deleted_at;
ALTER TABLE confirmation_tokens DROP COLUMN IF EXISTS deleted_at;

CREATE UNIQUE INDEX IF NOT EXISTS idx_subscriptions_email_repo
    ON subscriptions (email, repo);
