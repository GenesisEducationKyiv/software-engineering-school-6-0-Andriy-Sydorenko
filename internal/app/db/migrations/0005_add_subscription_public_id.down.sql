DROP INDEX IF EXISTS idx_subscriptions_public_id;
ALTER TABLE subscriptions DROP COLUMN IF EXISTS public_id;
