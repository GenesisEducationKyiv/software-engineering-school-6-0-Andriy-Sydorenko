-- Baseline schema. Uses IF NOT EXISTS so this migration is a no-op on
-- databases that were previously provisioned via GORM AutoMigrate;
-- only the schema_migrations row is added.

CREATE TABLE IF NOT EXISTS subscriptions (
    id                 BIGSERIAL PRIMARY KEY,
    email              VARCHAR(255) NOT NULL,
    repo               VARCHAR(255) NOT NULL,
    confirmed          BOOLEAN      NOT NULL DEFAULT FALSE,
    last_seen_tag      VARCHAR(255)          DEFAULT '',
    unsubscribe_token  VARCHAR(64),
    created_at         TIMESTAMPTZ,
    updated_at         TIMESTAMPTZ,
    deleted_at         TIMESTAMPTZ
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_subscriptions_unsubscribe_token
    ON subscriptions (unsubscribe_token);

CREATE INDEX IF NOT EXISTS idx_subscriptions_deleted_at
    ON subscriptions (deleted_at);

-- Uniqueness scoped to live rows so soft-deleted (email, repo) pairs
-- can be re-subscribed.
CREATE UNIQUE INDEX IF NOT EXISTS idx_email_repo_live
    ON subscriptions (email, repo) WHERE deleted_at IS NULL;

CREATE TABLE IF NOT EXISTS confirmation_tokens (
    id               BIGSERIAL PRIMARY KEY,
    token            VARCHAR(255) NOT NULL,
    subscription_id  BIGINT       NOT NULL,
    created_at       TIMESTAMPTZ,
    deleted_at       TIMESTAMPTZ,
    CONSTRAINT fk_confirmation_tokens_subscription
        FOREIGN KEY (subscription_id) REFERENCES subscriptions (id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_confirmation_tokens_token
    ON confirmation_tokens (token);

CREATE INDEX IF NOT EXISTS idx_confirmation_tokens_subscription_id
    ON confirmation_tokens (subscription_id);

CREATE INDEX IF NOT EXISTS idx_confirmation_tokens_deleted_at
    ON confirmation_tokens (deleted_at);
