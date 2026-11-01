-- opnview — the installation plugin's statements: what the retention purge runs (ADR 0021, step 6).
--
-- Same conventions as every *.statements.sql file: a "-- statement: <name>" line before each
-- statement, a terminating semicolon, named parameters only. The retention purge runs the statements
-- the manifest names under purge (and before_purge) as this plugin, inside its one transaction, bound
-- with :now, :retention (the installation's retention_seconds, 0 for unlimited), :horizon (now minus
-- the retention, null when unlimited: a comparison with it is null and deletes nothing) and
-- :live_sample_retention (docs/plugins.md, "The retention purge").

-- An interface address is purged by the last instant discovery saw it. A current address is moved
-- forward by every discovery pass, so only history older than the horizon goes. The interfaces, the
-- rules, the owners and the networks are bounded reference tables and are never purged; the clients
-- are the clients plugin's to remove, once nothing names them.
-- statement: purge_interface_addresses
DELETE FROM plugin_installation.interface_address
WHERE last_seen_at < CAST(:horizon AS BIGINT);
