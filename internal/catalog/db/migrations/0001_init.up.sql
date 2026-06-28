-- Catalog owns the watched-repo cursor and the per-subscription registrations
-- that make a repo "active". A repo is polled iff it has >= 1 registration.
CREATE TABLE IF NOT EXISTS watched_repos (
    repo          VARCHAR(255) PRIMARY KEY,
    last_seen_tag VARCHAR(255) NOT NULL DEFAULT ''
);

-- One row per subscription that wants this repo watched. subscription_id is the
-- cross-service identity minted by the orchestrator; it makes register/release
-- idempotent (ON CONFLICT DO NOTHING / delete-by-id) and ties unsubscribe back here.
CREATE TABLE IF NOT EXISTS repo_registrations (
    subscription_id UUID PRIMARY KEY,
    repo            VARCHAR(255) NOT NULL,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT now(),
    CONSTRAINT fk_repo_registrations_repo
        FOREIGN KEY (repo) REFERENCES watched_repos (repo)
);
CREATE INDEX IF NOT EXISTS idx_repo_registrations_repo ON repo_registrations (repo);
