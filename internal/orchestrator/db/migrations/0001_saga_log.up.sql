CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- The orchestrator's saga log: one row per subscribe saga, driving forward/backward
-- recovery after a crash. Holds no business data beyond what the saga needs to resume.
CREATE TABLE IF NOT EXISTS saga_log (
    saga_id         UUID PRIMARY KEY,
    type            TEXT        NOT NULL DEFAULT 'subscribe',
    state           TEXT        NOT NULL,
    subscription_id UUID        NOT NULL,
    payload         JSONB       NOT NULL,
    last_error      TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Recovery scans only unfinished sagas; the partial index keeps that cheap.
CREATE INDEX IF NOT EXISTS idx_saga_log_unfinished ON saga_log (state)
    WHERE state NOT IN ('DONE', 'ABORTED', 'COMPENSATED');
