CREATE TABLE IF NOT EXISTS watched_repos (
    repo           VARCHAR(255) PRIMARY KEY,
    last_seen_tag  VARCHAR(255) NOT NULL DEFAULT '',
    last_polled_at TIMESTAMPTZ  NOT NULL DEFAULT now()
);
