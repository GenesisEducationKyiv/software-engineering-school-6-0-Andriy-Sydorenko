-- watched_repos now lives in the Catalog service's own database; the subscription
-- service no longer scans, so drop its copy here.
DROP TABLE IF EXISTS watched_repos;
