-- opnview — the pf plugin's statements: what the retention purge runs (ADR 0021, step 6).
--
-- Same conventions as every *.statements.sql file: a "-- statement: <name>" line before each
-- statement, a terminating semicolon, named parameters only. The retention purge runs the statements
-- the manifest names under purge (and before_purge) as this plugin, inside its one transaction, bound
-- with :now, :retention (the installation's retention_seconds, 0 for unlimited), :horizon (now minus
-- the retention, null when unlimited: a comparison with it is null and deletes nothing) and
-- :live_sample_retention (docs/plugins.md, "The retention purge").

-- A flow older than the horizon goes. Its attribution and its measure output go at the same horizon,
-- each purged by its own owner: the unbound plugin and the core.
-- statement: purge_flows
DELETE FROM plugin_pf.flow
WHERE observed_at < CAST(:horizon AS BIGINT);
