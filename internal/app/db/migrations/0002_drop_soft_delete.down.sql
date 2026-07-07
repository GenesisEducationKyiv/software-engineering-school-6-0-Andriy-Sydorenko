-- Restore soft-delete columns and the partial unique index. The data
-- in dropped columns cannot be recovered; this only rebuilds shape.

DROP INDEX IF EXISTS idx_subscriptions_email_repo;

ALTER TABLE subscriptions       ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ;
ALTER TABLE confirmation_tokens ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS idx_subscriptions_deleted_at
    ON subscriptions (deleted_at);

CREATE INDEX IF NOT EXISTS idx_confirmation_tokens_deleted_at
    ON confirmation_tokens (deleted_at);

CREATE UNIQUE INDEX IF NOT EXISTS idx_email_repo_live
    ON subscriptions (email, repo) WHERE deleted_at IS NULL;
