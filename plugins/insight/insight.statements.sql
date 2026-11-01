-- opnview — the insight plugin's statements: what the retention purge runs (ADR 0021, step 6).
--
-- Same conventions as every *.statements.sql file: a "-- statement: <name>" line before each
-- statement, a terminating semicolon, named parameters only. The retention purge runs the statements
-- the manifest names under purge (and before_purge) as this plugin, inside its one transaction, bound
-- with :now, :retention (the installation's retention_seconds, 0 for unlimited), :horizon (now minus
-- the retention, null when unlimited: a comparison with it is null and deletes nothing) and
-- :live_sample_retention (docs/plugins.md, "The retention purge").

-- A measurement sample is purged by sampled_at like every other observation.
-- statement: purge_samples
DELETE FROM plugin_insight.measurement_sample
WHERE sampled_at < CAST(:horizon AS BIGINT);


-- A FINE SAMPLE -- one a live pass took while somebody looked at a live figure -- is kept for
-- live_sample_retention_seconds and no longer, to the exact second; a baseline sample, and a fine one
-- the baseline pass promoted, carries no live cadence and is untouched here (docs/plugins.md, "Live
-- passes"). The core hands the setting's value, or its default, as :live_sample_retention.
-- statement: purge_fine_samples
DELETE FROM plugin_insight.measurement_sample
WHERE live_cadence_seconds IS NOT NULL
  AND sampled_at < CAST(:now AS BIGINT) - CAST(:live_sample_retention AS BIGINT);
