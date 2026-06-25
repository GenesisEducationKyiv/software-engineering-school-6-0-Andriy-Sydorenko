-- This PR's app-side schema delta: add the cross-service public_id, and drop the
-- watched_repos cursor (it now lives in the Catalog service's own database).

-- public_id is the orchestrator-minted cross-service identity: it lets Catalog key
-- its registration and lets unsubscribe address the right registration. Backfilled
-- for pre-existing rows so the column can be NOT NULL UNIQUE.
CREATE EXTENSION IF NOT EXISTS pgcrypto;

ALTER TABLE subscriptions ADD COLUMN IF NOT EXISTS public_id UUID;
UPDATE subscriptions SET public_id = gen_random_uuid() WHERE public_id IS NULL;
ALTER TABLE subscriptions ALTER COLUMN public_id SET NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS idx_subscriptions_public_id ON subscriptions (public_id);

DROP TABLE IF EXISTS watched_repos;
