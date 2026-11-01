-- opnview — the retention purge of the core's own tables (ADR 0021, step 6).
--
-- It lives beside internal/store/schema.sql and is embedded into the binary by internal/store/purge,
-- the only code that runs it, inside the one purge transaction: after every plugin's before_purge
-- statements, before the plugin tables purged by their declared retention and every plugin's purge
-- statements (internal/store/plugintables, purgehook.go). THIS FILE NAMES NO PLUGIN'S TABLE: the flows,
-- the lookups and attributions, the security events, the samples, the geolocations, the clients, the
-- rollups and the purged part are each purged by the statements their plugin's manifest names under
-- purge, as that plugin (plugins/<name>/<name>.statements.sql).
--
-- The horizon is configuration, never a constant: every statement below reads it from
-- setting.retention_seconds. A value of 0 means unlimited, and the scalar subquery then yields NULL,
-- so every comparison is NULL and nothing is deleted. No retention duration appears as a literal
-- here, in any screen query, or in any index.
--
-- Bound parameter:
--   :now   the instant the purge runs, UTC epoch seconds
--
-- What survives: provider, provider_kind, source_availability, setting, declared_measure and its
-- states and markers, push_listener, sequence_bound and provider_read_through are bounded reference
-- and state tables and are never purged.

BEGIN;

-- THE WATERMARK. The purge records in retention_purge the furthest horizon it has applied, and the
-- flow and lookup inserts refuse a record older than it, inside the insert statement, whenever this
-- purge commits: the filter-log page keeps offering records the purge removed, and one stored again
-- would be counted twice, in flow and in the purged part.
INSERT INTO retention_purge (id, purged_before, purged_at)
SELECT 1, :now - CAST(value AS BIGINT), :now
FROM setting
WHERE key = 'retention_seconds' AND CAST(value AS BIGINT) > 0
ON CONFLICT (id) DO UPDATE SET
    purged_before = GREATEST(retention_purge.purged_before, excluded.purged_before),
    purged_at = excluded.purged_at;

-- Every measure's output goes with the connections it belongs to: a connection is purged with its
-- first leg, at the same horizon (schema.sql, connection_measure). The pf plugin deletes the legs.
DELETE FROM connection_measure
WHERE connection_start_at < (
    SELECT :now - CAST(value AS BIGINT)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS BIGINT) > 0
);

-- A collection gap is purged by detected_at: a gap describing a window whose observations would
-- themselves have gone says nothing a screen can use, and keeping it would make the oldest edge of
-- the history read as permanently broken.
DELETE FROM collection_gap
WHERE detected_at < (
    SELECT :now - CAST(value AS BIGINT)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS BIGINT) > 0
);

-- An interval during which opnview was not running is purged by the same rule as a collection gap,
-- and for the same reason: once the observations of its window are gone, it describes nothing a
-- screen still reads.
DELETE FROM opnview_downtime
WHERE detected_at < (
    SELECT :now - CAST(value AS BIGINT)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS BIGINT) > 0
);

-- Source periods. A period is written at every handover between polling and a push path
-- (internal/store/push/push.go, OpenSourcePeriod), and every start of opnview whose last period was a
-- push one writes a pull period (internal/collect/kinds/filterlog/pushpaths.go, loadPushPath): the
-- table grows with the restarts and with the push source's comings and goings, so it is purged at the
-- flows' horizon. A closed period that ended before it goes; the open period is never purged, however
-- old, since it is the one in force, and a period straddling the horizon is kept whole: it still says
-- which path delivered the records the retention keeps.
DELETE FROM provider_source_period
WHERE to_at IS NOT NULL
  AND to_at < (
    SELECT :now - CAST(value AS BIGINT)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS BIGINT) > 0
);

COMMIT;
