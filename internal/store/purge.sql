-- opnview — retention purge.
--
-- It lives beside internal/store/schema.sql and is embedded into the binary by
-- internal/store, which is the only code that runs it; sql/schema-checks.sh
-- runs the same file against a throwaway database. Go's embed directive cannot
-- reach outside its own package directory, and a second copy under sql/ would
-- be a second truth, so the one copy lives where the code that applies it can
-- read it.
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
-- What survives: interface, owner, blocklist, rule, interface_map, provider,
-- provider_rule_info, source_availability, eve_ingest_cursor and setting are
-- bounded reference and state tables and are never purged. owner and blocklist
-- in particular carry user input rather than observations: purging a client
-- removes the machine, never the person it was attributed to, and purging a
-- lookup removes the lookup, never the purpose somebody assigned to the list
-- that refused it. The registry and the rule-info cache in
-- particular stay bounded: they hold one row per implementation and one per
-- rule identity seen at least once, not one per observation. interface_network
-- is never purged either: an operator network is user input, and a detected one
-- is one row per network an interface has carried. The twenty-four aggregate
-- tables, six families of four periods, are purged by
-- period_end_at, so every period whose window still lies inside the horizon
-- stays queryable; only slots entirely older than the horizon go. An hour or a
-- day slot is kept for as long as the ISO week and the calendar month holding it
-- as well: a week or a month straddling the horizon has lost flows to the purge,
-- and the refresh composes it from those finer slots (internal/store/derive.sql,
-- "Composition"), so they must outlive the flows they were computed from.
--
-- THE PURGED PART. Before a flow is deleted, its figures are added to
-- purged_flow_hour, per hour and per every key an aggregate family reads, so an
-- hour slot computed after the purge -- the current hour under a retention shorter
-- than an hour, or any hour a reclassification forces -- still counts the flows the
-- purge removed from it (internal/store/derive.sql, "The refresh"). It is the
-- first statement, so the attribution of a flow it records is still there.
--
-- THE WATERMARK. The purge records in retention_purge the furthest horizon it has
-- applied, and the flow and lookup inserts refuse a record older than it, inside
-- the insert statement, whenever this purge commits: the
-- filter-log page keeps offering records the purge removed, and one stored again
-- would be counted twice, in flow and in the purged part.
--
-- A CLIENT THE PURGED PART NAMES IS KEPT, and a lookup an attribution of a
-- surviving flow names is kept, so no family loses a client's bytes or a flow's
-- site name to the purge while the slot that holds them is kept.

PRAGMA foreign_keys = ON;

BEGIN IMMEDIATE;

-- A flow with an end that is this firewall keeps classified_flow's peer_address,
-- the volume family's key, which is NULL when its other end is not outside; it
-- contributes to no peer, client or owner figure. A second leg keeps the device it
-- was logged on, exit_leg_device, which is the only volume figure it counts in.
INSERT INTO purged_flow_hour (hour_start_at, src_interface_id, dst_interface_id, src_client_id,
                              local_client_id, traffic_direction, peer_address, exit_leg_device,
                              rule_id, site_name, action, bytes, connections, purged_at)
SELECT (f.observed_at / 3600) * 3600,
       f.src_interface_id,
       f.dst_interface_id,
       f.src_client_id,
       f.local_client_id,
       f.traffic_direction,
       CASE WHEN f.traffic_direction IN ('to_this_firewall', 'from_this_firewall') THEN f.peer_address
            WHEN f.src_interface_id IS NOT NULL THEN f.dst_address
            WHEN f.dst_interface_id IS NOT NULL THEN f.src_address
            ELSE f.peer_address END,
       f.exit_leg_device,
       f.rule_id,
       a.site_name,
       f.action,
       sum(f.packet_bytes),
       count(*),
       :now
FROM classified_flow AS f
LEFT JOIN domain_attribution AS a ON a.flow_id = f.id
WHERE f.observed_at < (
    SELECT :now - CAST(value AS INTEGER)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS INTEGER) > 0
)
GROUP BY 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11;

INSERT INTO retention_purge (id, purged_before, purged_at)
SELECT 1, :now - CAST(value AS INTEGER), :now
FROM setting
WHERE key = 'retention_seconds' AND CAST(value AS INTEGER) > 0
ON CONFLICT (id) DO UPDATE SET
    purged_before = max(retention_purge.purged_before, excluded.purged_before),
    purged_at = excluded.purged_at;

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

-- A lookup an attribution of a surviving flow still names is kept: it was made up to
-- the attribution delay before the flow, so it can lie just before the horizon while
-- the flow lies just after it, and deleting it would take the flow's site name with it.
-- The flows above go first, taking their attributions with them.
DELETE FROM dns_resolution
WHERE looked_up_at < (
    SELECT :now - CAST(value AS INTEGER)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS INTEGER) > 0
)
  AND NOT EXISTS (SELECT 1 FROM domain_attribution a WHERE a.dns_resolution_id = dns_resolution.id);

-- security_event is purged by occurred_at, like every other observation. It
-- has no provider-named detail table to follow: the one thing such a table
-- would have carried is the ingestion coordinate, which lives in
-- eve_ingest_cursor and is a watermark rather than event data.
DELETE FROM security_event
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

-- The purged part of an hour is kept exactly as long as the hour slot it feeds:
-- until the hour, its ISO week and its calendar month have all ended before the
-- horizon. It goes before the clients, so a client only an expired purged part named
-- is removed with it rather than one purge later.
DELETE FROM purged_flow_hour
WHERE hour_start_at + 3600 < (
    SELECT :now - CAST(value AS INTEGER)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS INTEGER) > 0
)
  AND (hour_start_at / 86400) * 86400 - (((hour_start_at / 86400) + 3) % 7) * 86400 + 604800 < (
    SELECT :now - CAST(value AS INTEGER)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS INTEGER) > 0
)
  AND CAST(strftime('%s', hour_start_at, 'unixepoch', 'start of month', '+1 month') AS INTEGER) < (
    SELECT :now - CAST(value AS INTEGER)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS INTEGER) > 0
);

-- A client is removed only once every observation that named it has gone. Its
-- owner, if it had one, is untouched: the person outlives the machine.
-- purging an identity that a surviving flow still points at would leave that
-- flow unable to name a machine. Every statement above runs first, so by the
-- time this one runs the guards below see the purged state.
DELETE FROM client
WHERE last_seen_at < (
    SELECT :now - CAST(value AS INTEGER)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS INTEGER) > 0
)
AND NOT EXISTS (SELECT 1 FROM flow f
                WHERE f.src_client_id = client.id OR f.dst_client_id = client.id)
AND NOT EXISTS (SELECT 1 FROM security_event e WHERE e.src_client_id = client.id)
AND NOT EXISTS (SELECT 1 FROM dhcp_lease l WHERE l.client_id = client.id)
AND NOT EXISTS (SELECT 1 FROM dns_resolution r WHERE r.client_id = client.id)
AND NOT EXISTS (SELECT 1 FROM purged_flow_hour p WHERE p.local_client_id = client.id)
AND NOT EXISTS (SELECT 1 FROM purged_flow_hour p WHERE p.src_client_id = client.id);

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
)
  AND (period_start_at / 86400) * 86400 - (((period_start_at / 86400) + 3) % 7) * 86400 + 604800 < (
    SELECT :now - CAST(value AS INTEGER)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS INTEGER) > 0
)
  AND CAST(strftime('%s', period_start_at, 'unixepoch', 'start of month', '+1 month') AS INTEGER) < (
    SELECT :now - CAST(value AS INTEGER)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS INTEGER) > 0
);

DELETE FROM volume_aggregate_24h
WHERE period_end_at < (
    SELECT :now - CAST(value AS INTEGER)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS INTEGER) > 0
)
  AND (period_start_at / 86400) * 86400 - (((period_start_at / 86400) + 3) % 7) * 86400 + 604800 < (
    SELECT :now - CAST(value AS INTEGER)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS INTEGER) > 0
)
  AND CAST(strftime('%s', period_start_at, 'unixepoch', 'start of month', '+1 month') AS INTEGER) < (
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

-- The per-owner aggregates purge by period_end_at like the four above. The
-- owner rows they point at are untouched, here as everywhere else: the person
-- outlives both the machine and the month.
DELETE FROM owner_volume_aggregate_1h
WHERE period_end_at < (
    SELECT :now - CAST(value AS INTEGER)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS INTEGER) > 0
)
  AND (period_start_at / 86400) * 86400 - (((period_start_at / 86400) + 3) % 7) * 86400 + 604800 < (
    SELECT :now - CAST(value AS INTEGER)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS INTEGER) > 0
)
  AND CAST(strftime('%s', period_start_at, 'unixepoch', 'start of month', '+1 month') AS INTEGER) < (
    SELECT :now - CAST(value AS INTEGER)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS INTEGER) > 0
);

DELETE FROM owner_volume_aggregate_24h
WHERE period_end_at < (
    SELECT :now - CAST(value AS INTEGER)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS INTEGER) > 0
)
  AND (period_start_at / 86400) * 86400 - (((period_start_at / 86400) + 3) % 7) * 86400 + 604800 < (
    SELECT :now - CAST(value AS INTEGER)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS INTEGER) > 0
)
  AND CAST(strftime('%s', period_start_at, 'unixepoch', 'start of month', '+1 month') AS INTEGER) < (
    SELECT :now - CAST(value AS INTEGER)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS INTEGER) > 0
);

DELETE FROM owner_volume_aggregate_7d
WHERE period_end_at < (
    SELECT :now - CAST(value AS INTEGER)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS INTEGER) > 0
);

DELETE FROM owner_volume_aggregate_30d
WHERE period_end_at < (
    SELECT :now - CAST(value AS INTEGER)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS INTEGER) > 0
);

-- ---------------------------------------------------------------------------
-- The two sampled tables this cycle added.
--
-- A collection gap is purged by detected_at: a gap describing a window whose
-- observations would themselves have gone says nothing a screen can use, and
-- keeping it would make the oldest edge of the history read as permanently
-- broken. A measurement sample is purged by sampled_at like every other
-- observation.
-- ---------------------------------------------------------------------------

DELETE FROM collection_gap
WHERE detected_at < (
    SELECT :now - CAST(value AS INTEGER)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS INTEGER) > 0
);

DELETE FROM measurement_sample
WHERE sampled_at < (
    SELECT :now - CAST(value AS INTEGER)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS INTEGER) > 0
);


-- ---------------------------------------------------------------------------
-- The three aggregate families step 5 added -- per client, per site name and
-- per rule -- purge by period_end_at exactly as the eight above do. The client
-- and rule rows they point at are bounded or purged on their own terms; a client
-- purged above takes its slots with it through ON DELETE CASCADE.
-- ---------------------------------------------------------------------------
DELETE FROM client_volume_aggregate_1h
WHERE period_end_at < (
    SELECT :now - CAST(value AS INTEGER)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS INTEGER) > 0
)
  AND (period_start_at / 86400) * 86400 - (((period_start_at / 86400) + 3) % 7) * 86400 + 604800 < (
    SELECT :now - CAST(value AS INTEGER)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS INTEGER) > 0
)
  AND CAST(strftime('%s', period_start_at, 'unixepoch', 'start of month', '+1 month') AS INTEGER) < (
    SELECT :now - CAST(value AS INTEGER)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS INTEGER) > 0
);

DELETE FROM client_volume_aggregate_24h
WHERE period_end_at < (
    SELECT :now - CAST(value AS INTEGER)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS INTEGER) > 0
)
  AND (period_start_at / 86400) * 86400 - (((period_start_at / 86400) + 3) % 7) * 86400 + 604800 < (
    SELECT :now - CAST(value AS INTEGER)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS INTEGER) > 0
)
  AND CAST(strftime('%s', period_start_at, 'unixepoch', 'start of month', '+1 month') AS INTEGER) < (
    SELECT :now - CAST(value AS INTEGER)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS INTEGER) > 0
);

DELETE FROM client_volume_aggregate_7d
WHERE period_end_at < (
    SELECT :now - CAST(value AS INTEGER)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS INTEGER) > 0
);

DELETE FROM client_volume_aggregate_30d
WHERE period_end_at < (
    SELECT :now - CAST(value AS INTEGER)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS INTEGER) > 0
);

DELETE FROM domain_volume_aggregate_1h
WHERE period_end_at < (
    SELECT :now - CAST(value AS INTEGER)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS INTEGER) > 0
)
  AND (period_start_at / 86400) * 86400 - (((period_start_at / 86400) + 3) % 7) * 86400 + 604800 < (
    SELECT :now - CAST(value AS INTEGER)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS INTEGER) > 0
)
  AND CAST(strftime('%s', period_start_at, 'unixepoch', 'start of month', '+1 month') AS INTEGER) < (
    SELECT :now - CAST(value AS INTEGER)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS INTEGER) > 0
);

DELETE FROM domain_volume_aggregate_24h
WHERE period_end_at < (
    SELECT :now - CAST(value AS INTEGER)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS INTEGER) > 0
)
  AND (period_start_at / 86400) * 86400 - (((period_start_at / 86400) + 3) % 7) * 86400 + 604800 < (
    SELECT :now - CAST(value AS INTEGER)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS INTEGER) > 0
)
  AND CAST(strftime('%s', period_start_at, 'unixepoch', 'start of month', '+1 month') AS INTEGER) < (
    SELECT :now - CAST(value AS INTEGER)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS INTEGER) > 0
);

DELETE FROM domain_volume_aggregate_7d
WHERE period_end_at < (
    SELECT :now - CAST(value AS INTEGER)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS INTEGER) > 0
);

DELETE FROM domain_volume_aggregate_30d
WHERE period_end_at < (
    SELECT :now - CAST(value AS INTEGER)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS INTEGER) > 0
);

DELETE FROM rule_volume_aggregate_1h
WHERE period_end_at < (
    SELECT :now - CAST(value AS INTEGER)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS INTEGER) > 0
)
  AND (period_start_at / 86400) * 86400 - (((period_start_at / 86400) + 3) % 7) * 86400 + 604800 < (
    SELECT :now - CAST(value AS INTEGER)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS INTEGER) > 0
)
  AND CAST(strftime('%s', period_start_at, 'unixepoch', 'start of month', '+1 month') AS INTEGER) < (
    SELECT :now - CAST(value AS INTEGER)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS INTEGER) > 0
);

DELETE FROM rule_volume_aggregate_24h
WHERE period_end_at < (
    SELECT :now - CAST(value AS INTEGER)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS INTEGER) > 0
)
  AND (period_start_at / 86400) * 86400 - (((period_start_at / 86400) + 3) % 7) * 86400 + 604800 < (
    SELECT :now - CAST(value AS INTEGER)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS INTEGER) > 0
)
  AND CAST(strftime('%s', period_start_at, 'unixepoch', 'start of month', '+1 month') AS INTEGER) < (
    SELECT :now - CAST(value AS INTEGER)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS INTEGER) > 0
);

DELETE FROM rule_volume_aggregate_7d
WHERE period_end_at < (
    SELECT :now - CAST(value AS INTEGER)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS INTEGER) > 0
);

DELETE FROM rule_volume_aggregate_30d
WHERE period_end_at < (
    SELECT :now - CAST(value AS INTEGER)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS INTEGER) > 0
);

-- The peer family, added by the step-5A corrections, purges as the families above.
DELETE FROM peer_volume_aggregate_1h
WHERE period_end_at < (
    SELECT :now - CAST(value AS INTEGER)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS INTEGER) > 0
)
  AND (period_start_at / 86400) * 86400 - (((period_start_at / 86400) + 3) % 7) * 86400 + 604800 < (
    SELECT :now - CAST(value AS INTEGER)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS INTEGER) > 0
)
  AND CAST(strftime('%s', period_start_at, 'unixepoch', 'start of month', '+1 month') AS INTEGER) < (
    SELECT :now - CAST(value AS INTEGER)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS INTEGER) > 0
);

DELETE FROM peer_volume_aggregate_24h
WHERE period_end_at < (
    SELECT :now - CAST(value AS INTEGER)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS INTEGER) > 0
)
  AND (period_start_at / 86400) * 86400 - (((period_start_at / 86400) + 3) % 7) * 86400 + 604800 < (
    SELECT :now - CAST(value AS INTEGER)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS INTEGER) > 0
)
  AND CAST(strftime('%s', period_start_at, 'unixepoch', 'start of month', '+1 month') AS INTEGER) < (
    SELECT :now - CAST(value AS INTEGER)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS INTEGER) > 0
);

DELETE FROM peer_volume_aggregate_7d
WHERE period_end_at < (
    SELECT :now - CAST(value AS INTEGER)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS INTEGER) > 0
);

DELETE FROM peer_volume_aggregate_30d
WHERE period_end_at < (
    SELECT :now - CAST(value AS INTEGER)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS INTEGER) > 0
);

-- An address's classification record is purged by the instant it was last placed
-- in full; an address with no record is placed in full again at its next pass.
DELETE FROM address_classification
WHERE classified_at < (
    SELECT :now - CAST(value AS INTEGER)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS INTEGER) > 0
);

-- An interface address is purged by the last instant discovery saw it. A current
-- address is moved forward by every discovery pass, so only history older than
-- the horizon goes.
DELETE FROM interface_address
WHERE last_seen_at < (
    SELECT :now - CAST(value AS INTEGER)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS INTEGER) > 0
);

-- ---------------------------------------------------------------------------
-- Reconciled state. A snapshot is purged by the instant at which its set was
-- complete, and its items go with it through ON DELETE CASCADE, so state_item
-- needs no statement of its own: an item without its snapshot would be a set
-- member with no set and no instant, which is the one thing this kind exists to
-- prevent. The LATEST snapshot of a set is never older than the horizon while
-- collection is running, so purging does not silently turn a live set into a
-- departure — and a set whose provider stopped answering ages out entirely,
-- rather than leaving a stale snapshot that a screen would read as current.
-- ---------------------------------------------------------------------------
DELETE FROM state_snapshot
WHERE captured_at < (
    SELECT :now - CAST(value AS INTEGER)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS INTEGER) > 0
);

COMMIT;
