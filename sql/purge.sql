-- opnview — retention purge.
--
-- The horizon is configuration, never a constant: every statement below reads
-- it from setting.retention_seconds. A value of 0 means unlimited, and the
-- scalar subquery then yields NULL, so every comparison is NULL and nothing is
-- deleted. No retention duration appears as a literal here, in any screen
-- query, or in any index.
--
-- Bound parameter:
--   :now   the instant the purge runs, UTC epoch seconds
--
-- Order matters only for readability: domain_attribution is removed by the
-- ON DELETE CASCADE of both its parents, so deleting an old flow or an old
-- resolver lookup takes its attribution with it. After the purge,
-- PRAGMA foreign_key_check returns no rows.
--
-- What survives: segment, device, rule, interface_map, ids_rule_info,
-- source_availability, eve_ingest_cursor and setting are bounded reference
-- and state tables and are never purged. The four aggregates are purged by
-- period_end_at, so every period whose window still lies inside the horizon
-- stays queryable; only slots entirely older than the horizon go.

PRAGMA foreign_keys = ON;

BEGIN IMMEDIATE;

DELETE FROM domain_attribution
WHERE attributed_at < (
    SELECT :now - CAST(value AS INTEGER)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS INTEGER) > 0
);

DELETE FROM flow
WHERE observed_at < (
    SELECT :now - CAST(value AS INTEGER)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS INTEGER) > 0
);

DELETE FROM dns_resolution
WHERE looked_up_at < (
    SELECT :now - CAST(value AS INTEGER)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS INTEGER) > 0
);

DELETE FROM alert
WHERE occurred_at < (
    SELECT :now - CAST(value AS INTEGER)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS INTEGER) > 0
);

DELETE FROM dhcp_lease
WHERE observed_at < (
    SELECT :now - CAST(value AS INTEGER)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS INTEGER) > 0
);

DELETE FROM pair_volume_observation
WHERE day_start_at < (
    SELECT :now - CAST(value AS INTEGER)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS INTEGER) > 0
);

-- A device is removed only once every observation that named it has gone:
-- purging an identity that a surviving flow still points at would leave that
-- flow unable to name a machine. Every statement above runs first, so by the
-- time this one runs the guards below see the purged state.
DELETE FROM device
WHERE last_seen_at < (
    SELECT :now - CAST(value AS INTEGER)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS INTEGER) > 0
)
AND NOT EXISTS (SELECT 1 FROM flow f
                WHERE f.src_device_id = device.id OR f.dst_device_id = device.id)
AND NOT EXISTS (SELECT 1 FROM alert a WHERE a.src_device_id = device.id)
AND NOT EXISTS (SELECT 1 FROM dhcp_lease l WHERE l.device_id = device.id)
AND NOT EXISTS (SELECT 1 FROM dns_resolution r WHERE r.device_id = device.id);

DELETE FROM geo_asn
WHERE looked_up_at < (
    SELECT :now - CAST(value AS INTEGER)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS INTEGER) > 0
);

DELETE FROM volume_aggregate_1h
WHERE period_end_at < (
    SELECT :now - CAST(value AS INTEGER)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS INTEGER) > 0
);

DELETE FROM volume_aggregate_24h
WHERE period_end_at < (
    SELECT :now - CAST(value AS INTEGER)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS INTEGER) > 0
);

DELETE FROM volume_aggregate_7d
WHERE period_end_at < (
    SELECT :now - CAST(value AS INTEGER)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS INTEGER) > 0
);

DELETE FROM volume_aggregate_30d
WHERE period_end_at < (
    SELECT :now - CAST(value AS INTEGER)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS INTEGER) > 0
);

COMMIT;
