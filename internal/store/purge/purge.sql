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
-- no foreign key is left dangling, which PostgreSQL enforces on every statement.
--
-- What survives: interface, owner, blocklist, rule, interface_map, provider,
-- provider_rule_info, source_availability, eve_ingest_cursor,
-- resource_record_read and setting are
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
-- A CLIENT THE PURGED PART NAMES IS KEPT, and a lookup or a resolver record an
-- attribution of a surviving flow names is kept, so no family loses a client's
-- bytes or a flow's site name to the purge while the slot that holds them is kept.

BEGIN;

-- A flow with an end that is this firewall keeps classified_flow's peer_address,
-- the volume family's key, which is NULL when its other end is not outside; it
-- contributes to no peer, client or owner figure. A second leg keeps the device it
-- was logged on, exit_leg_device, which is the only volume figure it counts in.
--
-- Every declared measure of the flows purged goes with them (PS21 of the NetFlow join
-- amendment, generalised by PA5 of the plugin-API amendment): each flow's parts in every
-- measure, read through flow_measures (schema.sql) before the flows and their measure
-- output are deleted, so the purged part keeps a purged figure in every measure.
INSERT INTO purged_flow_hour (hour_start_at, src_interface_id, dst_interface_id, src_client_id,
                              local_client_id, traffic_direction, peer_address, exit_leg_device,
                              rule_id, site_name, action, bytes, connections, measure_parts,
                              purged_at)
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
       measure_parts_sum(n.parts),
       :now
FROM classified_flow AS f
LEFT JOIN domain_attribution AS a ON a.flow_id = f.id
LEFT JOIN LATERAL flow_measures(f.id, f.pair_outcome, f.paired_flow_id, f.action) AS n ON true
WHERE f.observed_at < (
    SELECT :now - CAST(value AS BIGINT)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS BIGINT) > 0
)
GROUP BY 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11;

INSERT INTO retention_purge (id, purged_before, purged_at)
SELECT 1, :now - CAST(value AS BIGINT), :now
FROM setting
WHERE key = 'retention_seconds' AND CAST(value AS BIGINT) > 0
ON CONFLICT (id) DO UPDATE SET
    purged_before = GREATEST(retention_purge.purged_before, excluded.purged_before),
    purged_at = excluded.purged_at;

DELETE FROM domain_attribution
WHERE attributed_at < (
    SELECT :now - CAST(value AS BIGINT)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS BIGINT) > 0
);

DELETE FROM flow
WHERE observed_at < (
    SELECT :now - CAST(value AS BIGINT)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS BIGINT) > 0
);

-- Every measure's output goes with the connections it belongs to: a connection is
-- purged with its first leg, at the same horizon (schema.sql, connection_measure).
DELETE FROM connection_measure
WHERE connection_start_at < (
    SELECT :now - CAST(value AS BIGINT)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS BIGINT) > 0
);

-- A lookup an attribution of a surviving flow still names is kept: it was made up to
-- the attribution delay before the flow, so it can lie just before the horizon while
-- the flow lies just after it, and deleting it would take the flow's site name with it.
-- The flows above go first, taking their attributions with them.
DELETE FROM dns_resolution
WHERE looked_up_at < (
    SELECT :now - CAST(value AS BIGINT)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS BIGINT) > 0
)
  AND NOT EXISTS (SELECT 1 FROM domain_attribution a WHERE a.dns_resolution_id = dns_resolution.id);

-- A record the resolver held is purged once its coverage interval ended before the
-- horizon: the resolver can no longer have held it for any instant the history still
-- keeps. An observation an attribution of a surviving flow names is kept, for the
-- reason the lookup above is: deleting it would leave that attribution naming
-- nothing, which the foreign key refuses. The flows above go first, taking their
-- attributions with them.
DELETE FROM resource_record_observation
WHERE covered_until_at < (
    SELECT :now - CAST(value AS BIGINT)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS BIGINT) > 0
)
  AND NOT EXISTS (SELECT 1 FROM domain_attribution a
                  WHERE a.address_observation_id = resource_record_observation.id);

-- security_event is purged by occurred_at, like every other observation: the suricata plugin's table,
-- reached through the bridge until wave 5 (ADR 0020). The ingestion coordinate is the provider's
-- durable state, a watermark rather than event data.
DELETE FROM security_event
WHERE occurred_at < (
    SELECT :now - CAST(value AS BIGINT)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS BIGINT) > 0
);

DELETE FROM pair_volume_observation
WHERE day_start_at < (
    SELECT :now - CAST(value AS BIGINT)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS BIGINT) > 0
);

-- The purged part of an hour is kept exactly as long as the hour slot it feeds:
-- until the hour, its ISO week and its calendar month have all ended before the
-- horizon. It goes before the clients, so a client only an expired purged part named
-- is removed with it rather than one purge later.
DELETE FROM purged_flow_hour
WHERE hour_start_at + 3600 < (
    SELECT :now - CAST(value AS BIGINT)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS BIGINT) > 0
)
  AND (hour_start_at / 86400) * 86400 - (((hour_start_at / 86400) + 3) % 7) * 86400 + 604800 < (
    SELECT :now - CAST(value AS BIGINT)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS BIGINT) > 0
)
  AND next_month_start_at(hour_start_at) < (
    SELECT :now - CAST(value AS BIGINT)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS BIGINT) > 0
);

-- A client is removed only once every observation that named it has gone, and
-- NEVER while somebody has assigned it to a person (specs/SPEC-backend-completion-
-- plugins.md, C1): an assignment is user input, and a machine somebody attributed
-- outlives the retention as the person does, so it is still there to be listed,
-- reassigned or unassigned when it next appears. Purging an identity that a
-- surviving flow still points at would leave that flow unable to name a machine.
-- Every statement above runs first, so by the time this one runs the guards below
-- see the purged state.
DELETE FROM client
WHERE last_seen_at < (
    SELECT :now - CAST(value AS BIGINT)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS BIGINT) > 0
)
AND owner_id IS NULL
AND NOT EXISTS (SELECT 1 FROM flow f
                WHERE f.src_client_id = client.id OR f.dst_client_id = client.id)
AND NOT EXISTS (SELECT 1 FROM security_event e WHERE e.src_client_id = client.id)
AND NOT EXISTS (SELECT 1 FROM security_event e WHERE e.dst_client_id = client.id)
AND NOT EXISTS (SELECT 1 FROM dhcp_lease l WHERE l.client_id = client.id)
AND NOT EXISTS (SELECT 1 FROM dns_resolution r WHERE r.client_id = client.id)
AND NOT EXISTS (SELECT 1 FROM purged_flow_hour p WHERE p.local_client_id = client.id)
AND NOT EXISTS (SELECT 1 FROM purged_flow_hour p WHERE p.src_client_id = client.id);

DELETE FROM geo_asn
WHERE looked_up_at < (
    SELECT :now - CAST(value AS BIGINT)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS BIGINT) > 0
);

DELETE FROM volume_aggregate_1h
WHERE period_end_at < (
    SELECT :now - CAST(value AS BIGINT)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS BIGINT) > 0
)
  AND (period_start_at / 86400) * 86400 - (((period_start_at / 86400) + 3) % 7) * 86400 + 604800 < (
    SELECT :now - CAST(value AS BIGINT)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS BIGINT) > 0
)
  AND next_month_start_at(period_start_at) < (
    SELECT :now - CAST(value AS BIGINT)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS BIGINT) > 0
);

DELETE FROM volume_aggregate_24h
WHERE period_end_at < (
    SELECT :now - CAST(value AS BIGINT)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS BIGINT) > 0
)
  AND (period_start_at / 86400) * 86400 - (((period_start_at / 86400) + 3) % 7) * 86400 + 604800 < (
    SELECT :now - CAST(value AS BIGINT)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS BIGINT) > 0
)
  AND next_month_start_at(period_start_at) < (
    SELECT :now - CAST(value AS BIGINT)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS BIGINT) > 0
);

DELETE FROM volume_aggregate_7d
WHERE period_end_at < (
    SELECT :now - CAST(value AS BIGINT)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS BIGINT) > 0
);

DELETE FROM volume_aggregate_30d
WHERE period_end_at < (
    SELECT :now - CAST(value AS BIGINT)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS BIGINT) > 0
);

-- The per-owner aggregates purge by period_end_at like the four above. The
-- owner rows they point at are untouched, here as everywhere else: the person
-- outlives both the machine and the month.
DELETE FROM owner_volume_aggregate_1h
WHERE period_end_at < (
    SELECT :now - CAST(value AS BIGINT)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS BIGINT) > 0
)
  AND (period_start_at / 86400) * 86400 - (((period_start_at / 86400) + 3) % 7) * 86400 + 604800 < (
    SELECT :now - CAST(value AS BIGINT)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS BIGINT) > 0
)
  AND next_month_start_at(period_start_at) < (
    SELECT :now - CAST(value AS BIGINT)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS BIGINT) > 0
);

DELETE FROM owner_volume_aggregate_24h
WHERE period_end_at < (
    SELECT :now - CAST(value AS BIGINT)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS BIGINT) > 0
)
  AND (period_start_at / 86400) * 86400 - (((period_start_at / 86400) + 3) % 7) * 86400 + 604800 < (
    SELECT :now - CAST(value AS BIGINT)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS BIGINT) > 0
)
  AND next_month_start_at(period_start_at) < (
    SELECT :now - CAST(value AS BIGINT)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS BIGINT) > 0
);

DELETE FROM owner_volume_aggregate_7d
WHERE period_end_at < (
    SELECT :now - CAST(value AS BIGINT)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS BIGINT) > 0
);

DELETE FROM owner_volume_aggregate_30d
WHERE period_end_at < (
    SELECT :now - CAST(value AS BIGINT)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS BIGINT) > 0
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
    SELECT :now - CAST(value AS BIGINT)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS BIGINT) > 0
);

-- An interval during which opnview was not running is purged by the same rule as
-- a collection gap, and for the same reason: once the observations of its window
-- are gone, it describes nothing a screen still reads.
DELETE FROM opnview_downtime
WHERE detected_at < (
    SELECT :now - CAST(value AS BIGINT)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS BIGINT) > 0
);

DELETE FROM measurement_sample
WHERE sampled_at < (
    SELECT :now - CAST(value AS BIGINT)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS BIGINT) > 0
);

-- A FINE SAMPLE -- one a live pass took while somebody looked at a live figure -- is
-- kept for live_sample_retention_seconds and no longer, to the exact second; a baseline
-- sample, and a fine one the baseline pass promoted, carries no live cadence and is
-- untouched here (C3 of the backend-completion cycle, "Persistence"). With no row, the
-- default the schema writes applies: the coalesce keeps a deleted row from turning the
-- short retention into none.
DELETE FROM measurement_sample
WHERE live_cadence_seconds IS NOT NULL
  AND sampled_at < :now - coalesce((
    SELECT CAST(value AS BIGINT)
    FROM setting
    WHERE key = 'live_sample_retention_seconds' AND CAST(value AS BIGINT) > 0
), 3600);

-- ---------------------------------------------------------------------------
-- The three aggregate families step 5 added -- per client, per site name and
-- per rule -- purge by period_end_at exactly as the eight above do. The client
-- and rule rows they point at are bounded or purged on their own terms; a client
-- purged above takes its slots with it through ON DELETE CASCADE.
-- ---------------------------------------------------------------------------
DELETE FROM client_volume_aggregate_1h
WHERE period_end_at < (
    SELECT :now - CAST(value AS BIGINT)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS BIGINT) > 0
)
  AND (period_start_at / 86400) * 86400 - (((period_start_at / 86400) + 3) % 7) * 86400 + 604800 < (
    SELECT :now - CAST(value AS BIGINT)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS BIGINT) > 0
)
  AND next_month_start_at(period_start_at) < (
    SELECT :now - CAST(value AS BIGINT)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS BIGINT) > 0
);

DELETE FROM client_volume_aggregate_24h
WHERE period_end_at < (
    SELECT :now - CAST(value AS BIGINT)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS BIGINT) > 0
)
  AND (period_start_at / 86400) * 86400 - (((period_start_at / 86400) + 3) % 7) * 86400 + 604800 < (
    SELECT :now - CAST(value AS BIGINT)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS BIGINT) > 0
)
  AND next_month_start_at(period_start_at) < (
    SELECT :now - CAST(value AS BIGINT)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS BIGINT) > 0
);

DELETE FROM client_volume_aggregate_7d
WHERE period_end_at < (
    SELECT :now - CAST(value AS BIGINT)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS BIGINT) > 0
);

DELETE FROM client_volume_aggregate_30d
WHERE period_end_at < (
    SELECT :now - CAST(value AS BIGINT)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS BIGINT) > 0
);

DELETE FROM domain_volume_aggregate_1h
WHERE period_end_at < (
    SELECT :now - CAST(value AS BIGINT)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS BIGINT) > 0
)
  AND (period_start_at / 86400) * 86400 - (((period_start_at / 86400) + 3) % 7) * 86400 + 604800 < (
    SELECT :now - CAST(value AS BIGINT)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS BIGINT) > 0
)
  AND next_month_start_at(period_start_at) < (
    SELECT :now - CAST(value AS BIGINT)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS BIGINT) > 0
);

DELETE FROM domain_volume_aggregate_24h
WHERE period_end_at < (
    SELECT :now - CAST(value AS BIGINT)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS BIGINT) > 0
)
  AND (period_start_at / 86400) * 86400 - (((period_start_at / 86400) + 3) % 7) * 86400 + 604800 < (
    SELECT :now - CAST(value AS BIGINT)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS BIGINT) > 0
)
  AND next_month_start_at(period_start_at) < (
    SELECT :now - CAST(value AS BIGINT)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS BIGINT) > 0
);

DELETE FROM domain_volume_aggregate_7d
WHERE period_end_at < (
    SELECT :now - CAST(value AS BIGINT)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS BIGINT) > 0
);

DELETE FROM domain_volume_aggregate_30d
WHERE period_end_at < (
    SELECT :now - CAST(value AS BIGINT)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS BIGINT) > 0
);

DELETE FROM rule_volume_aggregate_1h
WHERE period_end_at < (
    SELECT :now - CAST(value AS BIGINT)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS BIGINT) > 0
)
  AND (period_start_at / 86400) * 86400 - (((period_start_at / 86400) + 3) % 7) * 86400 + 604800 < (
    SELECT :now - CAST(value AS BIGINT)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS BIGINT) > 0
)
  AND next_month_start_at(period_start_at) < (
    SELECT :now - CAST(value AS BIGINT)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS BIGINT) > 0
);

DELETE FROM rule_volume_aggregate_24h
WHERE period_end_at < (
    SELECT :now - CAST(value AS BIGINT)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS BIGINT) > 0
)
  AND (period_start_at / 86400) * 86400 - (((period_start_at / 86400) + 3) % 7) * 86400 + 604800 < (
    SELECT :now - CAST(value AS BIGINT)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS BIGINT) > 0
)
  AND next_month_start_at(period_start_at) < (
    SELECT :now - CAST(value AS BIGINT)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS BIGINT) > 0
);

DELETE FROM rule_volume_aggregate_7d
WHERE period_end_at < (
    SELECT :now - CAST(value AS BIGINT)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS BIGINT) > 0
);

DELETE FROM rule_volume_aggregate_30d
WHERE period_end_at < (
    SELECT :now - CAST(value AS BIGINT)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS BIGINT) > 0
);

-- The peer family, added by the step-5A corrections, purges as the families above.
DELETE FROM peer_volume_aggregate_1h
WHERE period_end_at < (
    SELECT :now - CAST(value AS BIGINT)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS BIGINT) > 0
)
  AND (period_start_at / 86400) * 86400 - (((period_start_at / 86400) + 3) % 7) * 86400 + 604800 < (
    SELECT :now - CAST(value AS BIGINT)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS BIGINT) > 0
)
  AND next_month_start_at(period_start_at) < (
    SELECT :now - CAST(value AS BIGINT)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS BIGINT) > 0
);

DELETE FROM peer_volume_aggregate_24h
WHERE period_end_at < (
    SELECT :now - CAST(value AS BIGINT)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS BIGINT) > 0
)
  AND (period_start_at / 86400) * 86400 - (((period_start_at / 86400) + 3) % 7) * 86400 + 604800 < (
    SELECT :now - CAST(value AS BIGINT)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS BIGINT) > 0
)
  AND next_month_start_at(period_start_at) < (
    SELECT :now - CAST(value AS BIGINT)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS BIGINT) > 0
);

DELETE FROM peer_volume_aggregate_7d
WHERE period_end_at < (
    SELECT :now - CAST(value AS BIGINT)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS BIGINT) > 0
);

DELETE FROM peer_volume_aggregate_30d
WHERE period_end_at < (
    SELECT :now - CAST(value AS BIGINT)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS BIGINT) > 0
);

-- An address's classification record is purged by the instant it was last placed
-- in full; an address with no record is placed in full again at its next pass.
DELETE FROM address_classification
WHERE classified_at < (
    SELECT :now - CAST(value AS BIGINT)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS BIGINT) > 0
);

-- An interface address is purged by the last instant discovery saw it. A current
-- address is moved forward by every discovery pass, so only history older than
-- the horizon goes.
DELETE FROM interface_address
WHERE last_seen_at < (
    SELECT :now - CAST(value AS BIGINT)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS BIGINT) > 0
);

-- ---------------------------------------------------------------------------
DELETE FROM state_snapshot
WHERE captured_at < (
    SELECT :now - CAST(value AS BIGINT)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS BIGINT) > 0
);

-- ---------------------------------------------------------------------------
-- Source periods. A period is written at every handover between polling and a
-- push path (internal/store/push/push.go, OpenSourcePeriod), and every start of
-- opnview whose last period was a push one writes a pull period
-- (internal/collect/kinds/filterlog/pushpaths.go, loadPushPath): the table grows
-- with the restarts and with the push source's comings and goings, so it is
-- purged at the flows' horizon. A closed period that ended before it goes; the
-- open period is never purged, however old, since it is the one in force, and a
-- period straddling the horizon is kept whole: it still says which path
-- delivered the records the retention keeps.
-- ---------------------------------------------------------------------------
DELETE FROM provider_source_period
WHERE to_at IS NOT NULL
  AND to_at < (
    SELECT :now - CAST(value AS BIGINT)
    FROM setting
    WHERE key = 'retention_seconds' AND CAST(value AS BIGINT) > 0
);

COMMIT;
