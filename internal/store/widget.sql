-- opnview — the read statements of the widget API (step 5B,
-- specs/SPEC-step-5b-widget-api.md).
--
-- A sibling of read.sql, with the same conventions: a "-- statement: <name>" line
-- before each, a semicolon after, @period@ for the aggregate period, named parameters
-- only. Embedded into the binary by internal/store (statements.go) and run through
-- Store.WidgetRows on the read-only connections (readpool.go). sql/schema-checks.sh
-- splits this file like the other two and plans every statement at the 100 000- and
-- 1 000 000-row seeds; none may scan a growing table.
--
-- Sets of ids arrive as one JSON array text bound to a parameter -- :interface_ids,
-- :client_ids, :engine_kinds and the like -- and NULL means "no restriction". A set is
-- read through json_each, a table-valued function over the bound text rather than a
-- table, so a statement keeps one text whatever the size of the set.
--
-- A flow belongs to its LOCAL interface and client (classified_flow.local_interface_id
-- and .local_client_id), the end that is inside, as the aggregate families key it; and a
-- second leg (exit_leg_device set) is never counted a second time.
--
-- Observation-point limit: every volume below counts only traffic that crossed the
-- firewall; traffic between two clients behind one interface is in none of them.

-- statement: widget_earliest_lookup
SELECT min(looked_up_at) FROM dns_resolution;

-- statement: widget_earliest_security_event
SELECT min(occurred_at) FROM security_event;

-- statement: widget_earliest_sample
SELECT min(sampled_at) FROM measurement_sample;

-- statement: widget_earliest_flow
SELECT min(observed_at) FROM flow;

-- Every registered provider with its availability, or NULLs when the provider carries no
-- availability row: a screen reports that as "state unknown", never as healthy.
-- statement: widget_availability
SELECT p.id, p.kind, p.provider_key, p.display_name, p.is_active,
       a.state, a.probe, a.detail, a.checked_at, a.outcome
FROM provider AS p
LEFT JOIN source_availability AS a ON a.provider_id = p.id
ORDER BY p.kind, p.provider_key;

-- The collection gaps of the providers of some kinds, overlapping a window. The provider
-- is read first so each gap is an index search on (provider_id, interval_start_at).
-- statement: widget_collection_gaps
SELECT p.kind, p.provider_key, g.interval_start_at, g.interval_end_at, g.reason
FROM provider AS p
JOIN collection_gap AS g ON g.provider_id = p.id
WHERE p.kind IN (SELECT value FROM json_each(:kinds))
  AND g.interval_start_at < :to
  AND g.interval_end_at >= :from
ORDER BY g.interval_start_at, p.kind, p.provider_key;

-- statement: widget_setting
SELECT value FROM setting WHERE key = :key;

-- ---------------------------------------------------------------------------
-- Freshness. An hour is stale when no 1 h slot of it was computed strictly after the
-- newest ingested_at among its flows -- the recognition rule of the refresh contract,
-- docs/data-model.md, "The aggregate families" -- which also catches an hour holding
-- flows and no slot at all.
-- ---------------------------------------------------------------------------

-- statement: widget_hour_slots_computed
SELECT count(*), min(computed_at), max(computed_at)
FROM volume_aggregate_1h
WHERE period_start_at >= :from
  AND period_start_at < :to;

-- statement: widget_stale_hours
SELECT count(*)
FROM (SELECT f.observed_at - f.observed_at % 3600 AS hour_start_at, max(f.ingested_at) AS newest
      FROM flow AS f
      WHERE f.observed_at >= :from
        AND f.observed_at < :to
      GROUP BY f.observed_at - f.observed_at % 3600) AS h
WHERE NOT EXISTS (SELECT 1 FROM volume_aggregate_1h AS v
                  WHERE v.period_start_at = h.hour_start_at
                    AND v.computed_at > h.newest);

-- Aggregate freshness, per pre-computed period, over the slots starting in [from, to).
-- statement: widget_period_coverage
SELECT count(*), min(period_start_at), max(period_end_at), max(computed_at), sum(bytes)
FROM volume_aggregate_@period@
WHERE period_start_at >= :from
  AND period_start_at < :to;

-- statement: widget_period_stale_slots
SELECT count(*)
FROM (SELECT period_start_at, period_end_at, max(computed_at) AS computed
      FROM volume_aggregate_@period@
      WHERE period_start_at >= :from
        AND period_start_at < :to
      GROUP BY period_start_at, period_end_at) AS computed_slot
WHERE EXISTS (SELECT 1 FROM flow AS f
              WHERE f.observed_at >= computed_slot.period_start_at
                AND f.observed_at < computed_slot.period_end_at
                AND f.ingested_at >= computed_slot.computed);

-- ---------------------------------------------------------------------------
-- The installation's own rows, each read by key.
-- ---------------------------------------------------------------------------

-- The label an interface is named by is the user's own where one is set and the discovered
-- description otherwise; whether the user set one is the last column. Neither is ever read
-- to decide what an interface is.
-- statement: widget_interfaces
SELECT id, identifier, device, description, user_label, link_kind, vlan_tag, is_upstream,
       coalesce(nullif(user_label, ''), description),
       CASE WHEN nullif(user_label, '') IS NULL THEN 0 ELSE 1 END
FROM interface
ORDER BY id;

-- statement: widget_owners
SELECT id, display_name FROM owner ORDER BY id;

-- statement: widget_client
SELECT id, identity_kind, identity_key, interface_id, mac, hostname, last_address, owner_id,
       unstable_identity, last_seen_at, owner_assigned_at
FROM client
WHERE id = :client_id;

-- statement: widget_clients_of_owner
SELECT id, identity_kind, identity_key, interface_id, mac, hostname, last_address, owner_id,
       unstable_identity, last_seen_at, owner_assigned_at
FROM client
WHERE owner_id = :owner_id
ORDER BY id;

-- statement: widget_rule
SELECT id, pf_label, description, action, is_automatic FROM rule WHERE id = :rule_id;

-- References, resolved by exact match on the declared key and on nothing else. The label
-- of a reference is never read by any of these.
-- statement: widget_ref_interface_identifier
SELECT id FROM interface WHERE identifier = :value;

-- statement: widget_ref_interface_device
SELECT id FROM interface WHERE device = :value;

-- statement: widget_ref_interface_user_label
SELECT id FROM interface WHERE user_label = :value;

-- statement: widget_ref_client_identity
SELECT id FROM client WHERE identity_kind = :identity_kind AND identity_key = :value;

-- A host name is not unique; the API refuses to pick one of several clients.
-- statement: widget_ref_client_hostname
SELECT id FROM client WHERE hostname = :value LIMIT 2;

-- statement: widget_ref_rule_pf_label
SELECT id FROM rule WHERE pf_label = :value;

-- statement: widget_ref_rule_description
SELECT id FROM rule WHERE description = :value LIMIT 2;

-- statement: widget_ref_owner_display_name
SELECT id FROM owner WHERE display_name = :value;

-- statement: widget_ref_provider
SELECT id FROM provider WHERE kind = :kind AND provider_key = :value;

-- statement: widget_ref_country
SELECT 1 FROM geo_asn WHERE country_code = :value LIMIT 1;

-- statement: widget_ref_operator
SELECT 1 FROM geo_asn WHERE asn = :asn LIMIT 1;

-- A site name is the name a lookup asked for, so it resolves when a stored lookup asked for
-- it, compared the way the attribution compares names.
-- statement: widget_ref_site
SELECT 1 FROM dns_resolution WHERE lower(rtrim(domain, '.')) = lower(rtrim(:value, '.')) LIMIT 1;

-- statement: widget_geo
SELECT address, lookup_state, country_code, country_name, latitude, longitude, asn, operator,
       dataset_build_at
FROM geo_asn
WHERE address = :address;

-- ---------------------------------------------------------------------------
-- Volumes per client, by the assembly ReadVolumeWindow uses: whole hours from the client
-- and peer families' 1 h slots, the partial hours at the edges from the flows.
-- ---------------------------------------------------------------------------

-- statement: widget_client_volume_slots
SELECT client_id, traffic_direction,
       sum(bytes), sum(allowed_bytes), sum(blocked_bytes), sum(unknown_bytes),
       sum(allowed_connections), sum(blocked_connections), sum(unknown_connections)
FROM client_volume_aggregate_1h
WHERE period_start_at >= :from
  AND period_start_at < :to
GROUP BY client_id, traffic_direction;

-- statement: widget_client_volume_flows
SELECT f.local_client_id, f.traffic_direction,
       sum(f.packet_bytes),
       sum(CASE WHEN f.action = 'pass' THEN f.packet_bytes ELSE 0 END),
       sum(CASE WHEN f.action IN ('block', 'reject') THEN f.packet_bytes ELSE 0 END),
       sum(CASE WHEN f.action = 'unknown' THEN f.packet_bytes ELSE 0 END),
       sum(CASE WHEN f.action = 'pass' THEN 1 ELSE 0 END),
       sum(CASE WHEN f.action IN ('block', 'reject') THEN 1 ELSE 0 END),
       sum(CASE WHEN f.action = 'unknown' THEN 1 ELSE 0 END)
FROM classified_flow AS f
WHERE f.observed_at >= :from
  AND f.observed_at < :to
  AND f.local_client_id IS NOT NULL
  AND f.traffic_scope <> 'this_firewall'
  AND f.exit_leg_device IS NULL
GROUP BY f.local_client_id, f.traffic_direction;

-- statement: widget_client_peer_slots
SELECT DISTINCT client_id, peer_address
FROM peer_volume_aggregate_1h
WHERE period_start_at >= :from
  AND period_start_at < :to;

-- The far end of a client's flow, as the peer family keys it (derive.sql).
-- statement: widget_client_peer_flows
SELECT DISTINCT f.local_client_id,
       CASE WHEN f.src_interface_id IS NOT NULL THEN f.dst_address ELSE f.src_address END
FROM classified_flow AS f
WHERE f.observed_at >= :from
  AND f.observed_at < :to
  AND f.local_client_id IS NOT NULL
  AND f.traffic_scope <> 'this_firewall'
  AND f.exit_leg_device IS NULL;

-- ---------------------------------------------------------------------------
-- Records and groupings read from the flows of the window alone. They hold what no
-- aggregate family keeps -- a rule per matrix cell, a site name's method, a record --
-- so beyond the flow horizon they have no answer, and the API says so by name.
-- ---------------------------------------------------------------------------

-- statement: widget_matrix_rules
SELECT f.src_interface_id, f.dst_interface_id, f.traffic_direction, f.rule_id, r.description,
       f.rid, count(*)
FROM classified_flow AS f
LEFT JOIN rule AS r ON r.id = f.rule_id
WHERE f.observed_at >= :from
  AND f.observed_at < :to
  AND f.exit_leg_device IS NULL
GROUP BY f.src_interface_id, f.dst_interface_id, f.traffic_direction, f.rule_id,
         r.description, f.rid;

-- One client's records, newest first, with what enriches them: the geolocation of the
-- destination, the site name and its method, and the interface the record was logged on.
-- statement: widget_client_records
SELECT f.id, f.observed_at, f.dst_address, f.dst_port, f.protocol, f.action, f.traffic_scope,
       f.packet_bytes, f.interface_device, m.description, m.interface_id,
       g.lookup_state, g.country_code, g.asn, g.operator, g.dataset_build_at,
       a.site_name, a.method, a.correlation_delay_seconds
FROM flow AS f
LEFT JOIN interface_map AS m ON m.device = f.interface_device
LEFT JOIN geo_asn AS g ON g.address = f.dst_address
LEFT JOIN domain_attribution AS a ON a.flow_id = f.id
WHERE f.src_client_id = :client_id
  AND f.observed_at >= :from
  AND f.observed_at < :to
  AND (f.pair_outcome IS NULL OR f.pair_outcome <> 'second_leg')
  AND (:action = 'all'
       OR (:action = 'allowed' AND f.action = 'pass')
       OR (:action = 'blocked' AND f.action IN ('block', 'reject')))
  AND (:scope = 'all' OR f.traffic_scope = :scope)
ORDER BY f.observed_at DESC, f.id DESC
LIMIT :limit;

-- statement: widget_client_record_totals
SELECT count(*), coalesce(sum(f.packet_bytes), 0)
FROM flow AS f
WHERE f.src_client_id = :client_id
  AND f.observed_at >= :from
  AND f.observed_at < :to
  AND (f.pair_outcome IS NULL OR f.pair_outcome <> 'second_leg')
  AND (:action = 'all'
       OR (:action = 'allowed' AND f.action = 'pass')
       OR (:action = 'blocked' AND f.action IN ('block', 'reject')))
  AND (:scope = 'all' OR f.traffic_scope = :scope);

-- Which server issued each of a client's leases: one row per provider and address, with
-- the latest generation seen.
-- statement: widget_client_leases
SELECT p.kind, p.provider_key, l.backend, l.address, max(l.observed_at), max(l.expires_at)
FROM dhcp_lease AS l
JOIN provider AS p ON p.id = l.provider_id
WHERE l.client_id = :client_id
GROUP BY p.kind, p.provider_key, l.backend, l.address
ORDER BY max(l.observed_at) DESC, l.address;

-- Composition by protocol and by service port.
-- statement: widget_composition_protocol
SELECT f.protocol,
       sum(f.packet_bytes),
       sum(CASE WHEN f.action = 'pass' THEN f.packet_bytes ELSE 0 END),
       sum(CASE WHEN f.action IN ('block', 'reject') THEN f.packet_bytes ELSE 0 END),
       sum(CASE WHEN f.action = 'unknown' THEN f.packet_bytes ELSE 0 END),
       sum(CASE WHEN f.action = 'pass' THEN 1 ELSE 0 END),
       sum(CASE WHEN f.action IN ('block', 'reject') THEN 1 ELSE 0 END),
       sum(CASE WHEN f.action = 'unknown' THEN 1 ELSE 0 END)
FROM classified_flow AS f
WHERE f.observed_at >= :from
  AND f.observed_at < :to
  AND f.exit_leg_device IS NULL
  AND (:scope = 'all' OR f.traffic_scope = :scope)
  AND (:interface_ids IS NULL
       OR f.local_interface_id IN (SELECT value FROM json_each(:interface_ids)))
  AND (:client_ids IS NULL
       OR f.local_client_id IN (SELECT value FROM json_each(:client_ids)))
GROUP BY f.protocol;

-- statement: widget_composition_port
SELECT f.dst_port,
       sum(f.packet_bytes),
       sum(CASE WHEN f.action = 'pass' THEN f.packet_bytes ELSE 0 END),
       sum(CASE WHEN f.action IN ('block', 'reject') THEN f.packet_bytes ELSE 0 END),
       sum(CASE WHEN f.action = 'unknown' THEN f.packet_bytes ELSE 0 END),
       sum(CASE WHEN f.action = 'pass' THEN 1 ELSE 0 END),
       sum(CASE WHEN f.action IN ('block', 'reject') THEN 1 ELSE 0 END),
       sum(CASE WHEN f.action = 'unknown' THEN 1 ELSE 0 END)
FROM classified_flow AS f
WHERE f.observed_at >= :from
  AND f.observed_at < :to
  AND f.exit_leg_device IS NULL
  AND (:scope = 'all' OR f.traffic_scope = :scope)
  AND (:interface_ids IS NULL
       OR f.local_interface_id IN (SELECT value FROM json_each(:interface_ids)))
  AND (:client_ids IS NULL
       OR f.local_client_id IN (SELECT value FROM json_each(:client_ids)))
GROUP BY f.dst_port;

-- The connection tree's rows, with what the API needs beyond ReadConnectionTree: the
-- blocked bytes, so blocked connections can be left out whole, and the newest instant, so
-- a level can be sorted by recency.
-- statement: widget_connection_tree
SELECT f.local_interface_id,
       f.local_client_id,
       c.owner_id,
       f.traffic_direction,
       CASE WHEN f.peer_address IS NULL THEN NULL
            WHEN g.address IS NULL THEN 'no_row'
            ELSE g.lookup_state END,
       g.asn,
       a.site_name,
       count(*),
       sum(f.packet_bytes),
       sum(CASE WHEN f.action IN ('block', 'reject') THEN 1 ELSE 0 END),
       sum(CASE WHEN f.action IN ('block', 'reject') THEN f.packet_bytes ELSE 0 END),
       max(f.observed_at)
FROM classified_flow AS f
LEFT JOIN client AS c ON c.id = f.local_client_id
LEFT JOIN geo_asn AS g ON g.address = f.peer_address
LEFT JOIN domain_attribution AS a ON a.flow_id = f.id
WHERE f.observed_at >= :from
  AND f.observed_at < :to
  AND f.exit_leg_device IS NULL
GROUP BY 1, 2, 3, 4, 5, 6, 7;

-- Site names over the flows of a window, per site, source client, source interface,
-- method and destination operator, so every figure a site-name preset shows is a sum of
-- these rows.
-- statement: widget_site_flows
SELECT a.site_name, f.src_client_id, f.src_interface_id, a.method, g.country_code, g.asn,
       g.operator, count(*), sum(f.packet_bytes), sum(a.correlation_delay_seconds),
       max(a.correlation_delay_seconds)
FROM flow AS f
JOIN domain_attribution AS a ON a.flow_id = f.id
LEFT JOIN geo_asn AS g ON g.address = f.dst_address
WHERE f.observed_at >= :from
  AND f.observed_at < :to
  AND (f.pair_outcome IS NULL OR f.pair_outcome <> 'second_leg')
GROUP BY a.site_name, f.src_client_id, f.src_interface_id, a.method, g.country_code, g.asn,
         g.operator;

-- The domain family's 1 h slots, for the whole hours before the earliest flow kept: the
-- site totals there, without a method, which only the purged attributions carried.
-- statement: widget_site_slots
SELECT site_name, client_id, sum(bytes),
       sum(allowed_connections + blocked_connections + unknown_connections)
FROM domain_volume_aggregate_1h
WHERE period_start_at >= :from
  AND period_start_at < :to
GROUP BY site_name, client_id;

-- The attribution rate per client: read_attribution_rate grouped by client, with the
-- client's eligible bytes for the by-volume order. Its counts equal the diagnostic
-- "Attribution rate per client" over the same window.
-- statement: widget_attribution_rate_by_client
SELECT f.src_client_id,
       count(*),
       count(a.flow_id),
       avg(a.correlation_delay_seconds),
       max(a.correlation_delay_seconds),
       coalesce(sum(CASE WHEN a.method = 'resolver_cache_answer' THEN 1 ELSE 0 END), 0),
       coalesce(sum(CASE WHEN a.method = 'lookup_timing' THEN 1 ELSE 0 END), 0),
       sum(f.packet_bytes)
FROM flow AS f
LEFT JOIN domain_attribution AS a ON a.flow_id = f.id
WHERE f.observed_at >= :from
  AND f.observed_at < :to
  AND f.dst_interface_id IS NULL
  AND f.src_client_id IS NOT NULL
  AND f.src_is_this_firewall = 0
  AND f.dst_is_this_firewall = 0
  AND (f.pair_outcome IS NULL OR f.pair_outcome <> 'second_leg')
GROUP BY f.src_client_id;

-- The Sankey with clients on the left: per client, the far interface or the outside peer.
-- statement: widget_sankey_client_flows
SELECT f.local_client_id, f.traffic_direction, f.src_interface_id, f.dst_interface_id,
       f.peer_address,
       sum(f.packet_bytes),
       sum(CASE WHEN f.action IN ('block', 'reject') THEN f.packet_bytes ELSE 0 END),
       count(*),
       sum(CASE WHEN f.action IN ('block', 'reject') THEN 1 ELSE 0 END)
FROM classified_flow AS f
WHERE f.observed_at >= :from
  AND f.observed_at < :to
  AND f.exit_leg_device IS NULL
  AND f.local_client_id IS NOT NULL
GROUP BY f.local_client_id, f.traffic_direction, f.src_interface_id, f.dst_interface_id,
         f.peer_address;

-- ---------------------------------------------------------------------------
-- Refusals.
-- ---------------------------------------------------------------------------

-- The unified blocked feed: the refusals of the window, newest first, over the
-- blocked_decision view, which places each refusal of the four engines once.
-- statement: widget_blocked_feed
SELECT d.source_table, d.source_id, d.occurred_at, d.engine_kind, d.engine_reference,
       d.client_id, d.interface_id, d.target, d.target_kind, d.target_is_this_firewall
FROM blocked_decision AS d
WHERE d.occurred_at >= :from
  AND d.occurred_at < :to
  AND (:engine_kinds IS NULL OR d.engine_kind IN (SELECT value FROM json_each(:engine_kinds)))
  AND (:client_ids IS NULL OR d.client_id IN (SELECT value FROM json_each(:client_ids)))
  AND (:interface_ids IS NULL
       OR d.interface_id IN (SELECT value FROM json_each(:interface_ids)))
ORDER BY d.occurred_at DESC, d.source_table, d.source_id DESC
LIMIT :limit;

-- statement: widget_blocked_feed_counts
SELECT d.engine_kind, count(*)
FROM blocked_decision AS d
WHERE d.occurred_at >= :from
  AND d.occurred_at < :to
  AND (:client_ids IS NULL OR d.client_id IN (SELECT value FROM json_each(:client_ids)))
  AND (:interface_ids IS NULL
       OR d.interface_id IN (SELECT value FROM json_each(:interface_ids)))
GROUP BY d.engine_kind;

-- Blocked firewall records per rule (per rid when no rule matched it) and per device the
-- record was logged on, with the interfaces and clients each one reached.
-- statement: widget_blocked_by_rule
SELECT f.rule_id, f.rid, r.description, r.pf_label, r.is_automatic, f.interface_device,
       m.description, m.interface_id, f.src_interface_id, f.src_client_id,
       count(*), max(f.observed_at)
FROM flow AS f
LEFT JOIN rule AS r ON r.id = f.rule_id
LEFT JOIN interface_map AS m ON m.device = f.interface_device
WHERE f.action IN ('block', 'reject')
  AND f.observed_at >= :from
  AND f.observed_at < :to
  AND (:client_ids IS NULL OR f.src_client_id IN (SELECT value FROM json_each(:client_ids)))
  AND (:interface_ids IS NULL
       OR f.src_interface_id IN (SELECT value FROM json_each(:interface_ids)))
GROUP BY f.rule_id, f.rid, r.description, r.pf_label, r.is_automatic, f.interface_device,
         m.description, m.interface_id, f.src_interface_id, f.src_client_id;

-- Blocked lookups per name, list, client and interface, with the list's assigned purpose.
-- statement: widget_blocked_lookups
SELECT lower(rtrim(r.domain, '.')), r.blocklist_id, b.name, b.purpose, r.client_id,
       r.client_address, r.interface_id, count(*), max(r.looked_up_at)
FROM dns_resolution AS r
LEFT JOIN blocklist AS b ON b.id = r.blocklist_id
WHERE r.action IN ('block', 'drop')
  AND r.looked_up_at >= :from
  AND r.looked_up_at < :to
GROUP BY lower(rtrim(r.domain, '.')), r.blocklist_id, b.name, b.purpose, r.client_id,
         r.client_address, r.interface_id;

-- The blocked map's detection-engine half: blocked security events per peer end.
-- statement: widget_security_blocked_peers
SELECT CASE WHEN :endpoint = 'source' THEN e.src_address ELSE e.dst_address END,
       e.src_interface_id, count(*)
FROM security_event AS e
WHERE e.event_action = 'blocked'
  AND e.occurred_at >= :from
  AND e.occurred_at < :to
GROUP BY 1, 2;

-- ---------------------------------------------------------------------------
-- Series in buckets, aligned to UTC multiples of :width seconds from the epoch, read from
-- the records of the window. A bucket with no row is returned by none of these; the API
-- fills it with a measured zero only where the window is covered and no gap overlaps it.
-- ---------------------------------------------------------------------------

-- statement: widget_traffic_buckets
SELECT f.observed_at - f.observed_at % :width, f.traffic_direction,
       sum(f.packet_bytes),
       sum(CASE WHEN f.action = 'pass' THEN f.packet_bytes ELSE 0 END),
       sum(CASE WHEN f.action IN ('block', 'reject') THEN f.packet_bytes ELSE 0 END),
       sum(CASE WHEN f.action = 'unknown' THEN f.packet_bytes ELSE 0 END),
       sum(CASE WHEN f.action = 'pass' THEN 1 ELSE 0 END),
       sum(CASE WHEN f.action IN ('block', 'reject') THEN 1 ELSE 0 END),
       sum(CASE WHEN f.action = 'unknown' THEN 1 ELSE 0 END)
FROM classified_flow AS f
LEFT JOIN geo_asn AS g ON g.address = f.peer_address
LEFT JOIN domain_attribution AS a ON a.flow_id = f.id
WHERE f.observed_at >= :from
  AND f.observed_at < :to
  AND f.exit_leg_device IS NULL
  AND (:interface_ids IS NULL
       OR f.local_interface_id IN (SELECT value FROM json_each(:interface_ids)))
  AND (:client_id IS NULL OR f.local_client_id = :client_id)
  AND (:rule_id IS NULL OR f.rule_id = :rule_id)
  AND (:country IS NULL OR g.country_code = :country)
  AND (:asn IS NULL OR g.asn = :asn)
  AND (:site IS NULL OR a.site_name = :site)
GROUP BY 1, 2;

-- The whole hours of a series beyond the flow horizon, from the interface family.
-- statement: widget_traffic_hour_slots
SELECT period_start_at, src_interface_id, dst_interface_id, traffic_direction,
       sum(bytes), sum(allowed_bytes), sum(blocked_bytes), sum(unknown_bytes),
       sum(allowed_connections), sum(blocked_connections), sum(unknown_connections)
FROM volume_aggregate_1h
WHERE period_start_at >= :from
  AND period_start_at < :to
  AND exit_leg_device IS NULL
GROUP BY period_start_at, src_interface_id, dst_interface_id, traffic_direction;

-- statement: widget_dns_buckets
SELECT r.looked_up_at - r.looked_up_at % :width,
       count(*),
       sum(CASE WHEN r.action IN ('block', 'drop') THEN 1 ELSE 0 END)
FROM dns_resolution AS r
WHERE r.looked_up_at >= :from
  AND r.looked_up_at < :to
  AND (:resolver IS NULL OR r.resolver = :resolver)
  AND (:client_id IS NULL OR r.client_id = :client_id)
  AND (:interface_ids IS NULL
       OR r.interface_id IN (SELECT value FROM json_each(:interface_ids)))
GROUP BY 1;

-- statement: widget_security_buckets
SELECT e.occurred_at - e.occurred_at % :width,
       count(*),
       sum(CASE WHEN e.event_action = 'blocked' THEN 1 ELSE 0 END)
FROM security_event AS e
WHERE e.occurred_at >= :from
  AND e.occurred_at < :to
  AND (:provider_id IS NULL OR e.provider_id = :provider_id)
  AND (:client_id IS NULL OR e.src_client_id = :client_id)
  AND (:interface_ids IS NULL
       OR e.src_interface_id IN (SELECT value FROM json_each(:interface_ids)))
GROUP BY 1;

-- ---------------------------------------------------------------------------
-- Samples.
-- ---------------------------------------------------------------------------

-- The subjects that reported a measure of some kinds over a window.
-- statement: widget_measurement_subjects
SELECT DISTINCT subject_kind, subject_key, measure, unit
FROM measurement_sample
WHERE sampled_at >= :from
  AND sampled_at <= :to
  AND subject_kind IN (SELECT value FROM json_each(:subject_kinds));

-- statement: widget_measurement_series
SELECT sampled_at, value
FROM measurement_sample
WHERE subject_kind = :subject_kind
  AND subject_key = :subject_key
  AND measure = :measure
  AND sampled_at >= :from
  AND sampled_at < :to
ORDER BY sampled_at;

-- statement: widget_measurement_latest
SELECT sampled_at, value, unit
FROM measurement_sample
WHERE subject_kind = :subject_kind
  AND subject_key = :subject_key
  AND measure = :measure
  AND sampled_at <= :to
ORDER BY sampled_at DESC
LIMIT 1;

-- ---------------------------------------------------------------------------
-- Unresolved joins.
-- ---------------------------------------------------------------------------

-- statement: widget_unresolved_flow_joins
SELECT coalesce(sum(CASE WHEN f.interface_lookup_state <> 'resolved' THEN 1 ELSE 0 END), 0),
       coalesce(sum(CASE WHEN f.rid IS NOT NULL AND f.rule_lookup_state <> 'resolved'
                         THEN 1 ELSE 0 END), 0),
       count(*)
FROM flow AS f
WHERE f.observed_at >= :from
  AND f.observed_at < :to
  AND (:interface_ids IS NULL
       OR f.src_interface_id IN (SELECT value FROM json_each(:interface_ids))
       OR f.dst_interface_id IN (SELECT value FROM json_each(:interface_ids)));

-- statement: widget_unresolved_flow_records
SELECT f.id, f.observed_at, f.interface_device, f.interface_lookup_state, f.rid,
       f.rule_lookup_state
FROM flow AS f
WHERE f.observed_at >= :from
  AND f.observed_at < :to
  AND ((:counter = 'interface' AND f.interface_lookup_state <> 'resolved')
       OR (:counter = 'rule' AND f.rid IS NOT NULL AND f.rule_lookup_state <> 'resolved'))
  AND (:interface_ids IS NULL
       OR f.src_interface_id IN (SELECT value FROM json_each(:interface_ids))
       OR f.dst_interface_id IN (SELECT value FROM json_each(:interface_ids)))
ORDER BY f.observed_at DESC, f.id DESC
LIMIT :limit;

-- The clients of the window, with the identity level each is known by.
-- statement: widget_window_clients
SELECT DISTINCT f.local_client_id, c.identity_kind, c.last_address, c.interface_id
FROM classified_flow AS f
JOIN client AS c ON c.id = f.local_client_id
WHERE f.observed_at >= :from
  AND f.observed_at < :to
  AND (:interface_ids IS NULL
       OR f.local_interface_id IN (SELECT value FROM json_each(:interface_ids)));

-- The outside peers of the window and the state of their geolocation.
-- statement: widget_window_peers
SELECT DISTINCT f.peer_address,
       CASE WHEN g.address IS NULL THEN 'no_row' ELSE g.lookup_state END
FROM classified_flow AS f
LEFT JOIN geo_asn AS g ON g.address = f.peer_address
WHERE f.observed_at >= :from
  AND f.observed_at < :to
  AND f.peer_address IS NOT NULL
  AND (:interface_ids IS NULL
       OR f.local_interface_id IN (SELECT value FROM json_each(:interface_ids)));

-- How far back the retention purge has purged: before it, the flows are gone.
-- statement: widget_purged_before
SELECT purged_before FROM retention_purge WHERE id = 1;

-- The earliest 1 h slot of the interface family: with the earliest flow, where a window
-- assembled from slots and flows begins to be covered.
-- statement: widget_earliest_hour_slot
SELECT min(period_start_at) FROM volume_aggregate_1h;

-- An operator's name, from any geolocated address carrying its AS number.
-- statement: widget_operator_name
SELECT operator FROM geo_asn WHERE asn = :asn AND operator IS NOT NULL LIMIT 1;

-- Per-person activity's inline timeline: the window's flows in buckets, per owner of the
-- inside client (NULL is the unassigned group).
-- statement: widget_owner_buckets
SELECT f.observed_at - f.observed_at % :width, c.owner_id, f.traffic_direction,
       sum(f.packet_bytes),
       sum(CASE WHEN f.action = 'pass' THEN f.packet_bytes ELSE 0 END),
       sum(CASE WHEN f.action IN ('block', 'reject') THEN f.packet_bytes ELSE 0 END),
       sum(CASE WHEN f.action = 'unknown' THEN f.packet_bytes ELSE 0 END),
       sum(CASE WHEN f.action = 'pass' THEN 1 ELSE 0 END),
       sum(CASE WHEN f.action IN ('block', 'reject') THEN 1 ELSE 0 END),
       sum(CASE WHEN f.action = 'unknown' THEN 1 ELSE 0 END)
FROM classified_flow AS f
JOIN client AS c ON c.id = f.local_client_id
WHERE f.observed_at >= :from
  AND f.observed_at < :to
  AND f.traffic_scope <> 'this_firewall'
  AND f.exit_leg_device IS NULL
GROUP BY 1, 2, 3;

-- The unified blocked feed grouped by client, and by target, over the whole window.
-- statement: widget_blocked_feed_by_client
SELECT d.client_id, count(*)
FROM blocked_decision AS d
WHERE d.occurred_at >= :from
  AND d.occurred_at < :to
  AND (:engine_kinds IS NULL OR d.engine_kind IN (SELECT value FROM json_each(:engine_kinds)))
  AND (:client_ids IS NULL OR d.client_id IN (SELECT value FROM json_each(:client_ids)))
  AND (:interface_ids IS NULL
       OR d.interface_id IN (SELECT value FROM json_each(:interface_ids)))
GROUP BY d.client_id;

-- statement: widget_blocked_feed_by_target
SELECT d.target, d.target_kind, count(*)
FROM blocked_decision AS d
WHERE d.occurred_at >= :from
  AND d.occurred_at < :to
  AND (:engine_kinds IS NULL OR d.engine_kind IN (SELECT value FROM json_each(:engine_kinds)))
  AND (:client_ids IS NULL OR d.client_id IN (SELECT value FROM json_each(:client_ids)))
  AND (:interface_ids IS NULL
       OR d.interface_id IN (SELECT value FROM json_each(:interface_ids)))
GROUP BY d.target, d.target_kind;

-- Composition by interface restricted to some clients, which no volume row can answer
-- because the interface family carries no client: read from the flows of the window.
-- statement: widget_composition_interface
SELECT f.local_interface_id,
       sum(f.packet_bytes),
       sum(CASE WHEN f.action = 'pass' THEN f.packet_bytes ELSE 0 END),
       sum(CASE WHEN f.action IN ('block', 'reject') THEN f.packet_bytes ELSE 0 END),
       sum(CASE WHEN f.action = 'unknown' THEN f.packet_bytes ELSE 0 END),
       sum(CASE WHEN f.action = 'pass' THEN 1 ELSE 0 END),
       sum(CASE WHEN f.action IN ('block', 'reject') THEN 1 ELSE 0 END),
       sum(CASE WHEN f.action = 'unknown' THEN 1 ELSE 0 END)
FROM classified_flow AS f
WHERE f.observed_at >= :from
  AND f.observed_at < :to
  AND f.exit_leg_device IS NULL
  AND (:scope = 'all' OR f.traffic_scope = :scope)
  AND (:interface_ids IS NULL
       OR f.local_interface_id IN (SELECT value FROM json_each(:interface_ids)))
  AND (:client_ids IS NULL
       OR f.local_client_id IN (SELECT value FROM json_each(:client_ids)))
GROUP BY f.local_interface_id;

-- Each rule's blocked records in buckets: the inline timeline of Blocked by firewall rule.
-- statement: widget_blocked_rule_buckets
SELECT f.rule_id, f.rid, f.observed_at - f.observed_at % :width, count(*)
FROM flow AS f
WHERE f.action IN ('block', 'reject')
  AND f.observed_at >= :from
  AND f.observed_at < :to
  AND (:client_ids IS NULL OR f.src_client_id IN (SELECT value FROM json_each(:client_ids)))
  AND (:interface_ids IS NULL
       OR f.src_interface_id IN (SELECT value FROM json_each(:interface_ids)))
GROUP BY f.rule_id, f.rid, f.observed_at - f.observed_at % :width;
