-- Cross-service identity minted by the orchestrator at saga start: lets Catalog
-- key its registration and lets unsubscribe address the right registration.
-- Backfilled for pre-existing rows so the column can be NOT NULL UNIQUE.
CREATE EXTENSION IF NOT EXISTS pgcrypto;

ALTER TABLE subscriptions ADD COLUMN IF NOT EXISTS public_id UUID;
UPDATE subscriptions SET public_id = gen_random_uuid() WHERE public_id IS NULL;
ALTER TABLE subscriptions ALTER COLUMN public_id SET NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS idx_subscriptions_public_id ON subscriptions (public_id);
