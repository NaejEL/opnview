-- opnview — the read statements the widget code in flows runs.
--
-- Same conventions as every *.statements.sql file of the tree (see service.statements.sql
-- in internal/widget for the parameter conventions). sql/schema-checks.sh plans each
-- statement at both seed sizes.
--
-- These read the flows by their columns -- the view classified_flow, whichever source wrote the
-- rows -- each connection once (no second leg), in a traffic scope ('all' for every one),
-- restricted to some inside interfaces and some clients when the request names them
-- (:interface_ids and :client_ids, a JSON list, NULL for no restriction).
--
-- THE DECISION of a record is 'allowed' for a pass, the blocked_decision view's engine kind for a
-- block or a reject -- 'firewall_rule', 'firewall_no_rule' or 'firewall_reason_not_recorded', by the
-- same rule the view applies (internal/store/schema.sql, blocked_decision; a test holds the two
-- equal) -- and 'unknown' for an action nobody read.


-- The window's flows per direction, protocol, destination port and decision: the parts the
-- protocol, service-port and blocking-reason compositions are summed from.
-- statement: widget_flow_parts
SELECT f.traffic_direction AS traffic_direction,
       f.protocol AS protocol,
       f.dst_port AS port,
       CASE WHEN f.action = 'pass' THEN 'allowed'
            WHEN f.action IN ('block', 'reject') THEN
                 CASE WHEN f.log_reason = 'match' THEN 'firewall_rule'
                      WHEN f.log_reason IN ('bad-offset', 'fragment', 'short', 'normalize', 'memory',
                                            'bad-timestamp', 'congestion', 'ip-option', 'proto-cksum',
                                            'state-mismatch', 'state-insert', 'state-limit',
                                            'src-limit', 'synproxy') THEN 'firewall_no_rule'
                      ELSE 'firewall_reason_not_recorded' END
            ELSE 'unknown' END AS decision,
       sum(f.packet_bytes) AS bytes,
       sum(CASE WHEN f.action = 'pass' THEN f.packet_bytes ELSE 0 END) AS allowed_bytes,
       sum(CASE WHEN f.action IN ('block', 'reject') THEN f.packet_bytes ELSE 0 END) AS blocked_bytes,
       sum(CASE WHEN f.action = 'unknown' THEN f.packet_bytes ELSE 0 END) AS unknown_bytes,
       sum(CASE WHEN f.action = 'pass' THEN 1 ELSE 0 END) AS allowed_connections,
       sum(CASE WHEN f.action IN ('block', 'reject') THEN 1 ELSE 0 END) AS blocked_connections,
       sum(CASE WHEN f.action = 'unknown' THEN 1 ELSE 0 END) AS unknown_connections
FROM classified_flow AS f
WHERE f.observed_at >= :from
  AND f.observed_at < :to
  AND f.exit_leg_device IS NULL
  AND (:scope = 'all' OR f.traffic_scope = :scope)
  AND (:interface_ids IS NULL
       OR f.local_interface_id IN (SELECT CAST(j.value AS BIGINT) FROM jsonb_array_elements_text(CAST(:interface_ids AS jsonb)) AS j(value)))
  AND (:client_ids IS NULL
       OR f.local_client_id IN (SELECT CAST(j.value AS BIGINT) FROM jsonb_array_elements_text(CAST(:client_ids AS jsonb)) AS j(value)))
GROUP BY 1, 2, 3, 4;


-- The same parts over a short trailing window, per bucket of :width seconds from the epoch and per
-- inside interface and client as well: the recent part of a composition, which a reader groups by
-- any of its dimensions.
-- statement: widget_flow_recent_parts
SELECT f.observed_at - f.observed_at % :width AS bucket_start,
       f.traffic_direction AS traffic_direction,
       f.protocol AS protocol,
       f.dst_port AS port,
       CASE WHEN f.action = 'pass' THEN 'allowed'
            WHEN f.action IN ('block', 'reject') THEN
                 CASE WHEN f.log_reason = 'match' THEN 'firewall_rule'
                      WHEN f.log_reason IN ('bad-offset', 'fragment', 'short', 'normalize', 'memory',
                                            'bad-timestamp', 'congestion', 'ip-option', 'proto-cksum',
                                            'state-mismatch', 'state-insert', 'state-limit',
                                            'src-limit', 'synproxy') THEN 'firewall_no_rule'
                      ELSE 'firewall_reason_not_recorded' END
            ELSE 'unknown' END AS decision,
       f.local_interface_id AS interface_id,
       f.local_client_id AS client_id,
       sum(f.packet_bytes) AS bytes,
       sum(CASE WHEN f.action = 'pass' THEN f.packet_bytes ELSE 0 END) AS allowed_bytes,
       sum(CASE WHEN f.action IN ('block', 'reject') THEN f.packet_bytes ELSE 0 END) AS blocked_bytes,
       sum(CASE WHEN f.action = 'unknown' THEN f.packet_bytes ELSE 0 END) AS unknown_bytes,
       sum(CASE WHEN f.action = 'pass' THEN 1 ELSE 0 END) AS allowed_connections,
       sum(CASE WHEN f.action IN ('block', 'reject') THEN 1 ELSE 0 END) AS blocked_connections,
       sum(CASE WHEN f.action = 'unknown' THEN 1 ELSE 0 END) AS unknown_connections
FROM classified_flow AS f
WHERE f.observed_at >= :from
  AND f.observed_at < :to
  AND f.exit_leg_device IS NULL
  AND (:scope = 'all' OR f.traffic_scope = :scope)
  AND (:interface_ids IS NULL
       OR f.local_interface_id IN (SELECT CAST(j.value AS BIGINT) FROM jsonb_array_elements_text(CAST(:interface_ids AS jsonb)) AS j(value)))
  AND (:client_ids IS NULL
       OR f.local_client_id IN (SELECT CAST(j.value AS BIGINT) FROM jsonb_array_elements_text(CAST(:client_ids AS jsonb)) AS j(value)))
GROUP BY 1, 2, 3, 4, 5, 6, 7;


-- The window's connections per inside end, direction, outside destination, inferred site name,
-- service port and protocol: the destination levels of a connection tree. The outside destination
-- is the peer address and the state of its geolocation ('no_row' when never looked up); the site
-- name is the attribution's, NULL where none was inferred.
-- statement: widget_flow_destinations
SELECT f.local_interface_id AS interface_id,
       f.local_client_id AS client_id,
       c.owner_id AS owner_id,
       f.traffic_direction AS traffic_direction,
       f.peer_address AS peer_address,
       CASE WHEN f.peer_address IS NULL THEN NULL
            WHEN g.address IS NULL THEN 'no_row'
            ELSE g.lookup_state END AS lookup_state,
       g.asn AS asn,
       a.site_name AS site_name,
       f.dst_port AS port,
       f.protocol AS protocol,
       count(*) AS connections,
       sum(f.packet_bytes) AS bytes,
       sum(CASE WHEN f.action IN ('block', 'reject') THEN 1 ELSE 0 END) AS blocked,
       sum(CASE WHEN f.action IN ('block', 'reject') THEN f.packet_bytes ELSE 0 END) AS blocked_bytes,
       max(f.observed_at) AS newest
FROM classified_flow AS f
LEFT JOIN client AS c ON c.id = f.local_client_id
LEFT JOIN geo_asn AS g ON g.address = f.peer_address
LEFT JOIN domain_attribution AS a ON a.flow_id = f.id
WHERE f.observed_at >= :from
  AND f.observed_at < :to
  AND f.exit_leg_device IS NULL
GROUP BY 1, 2, 3, 4, 5, 6, 7, 8, 9, 10;


-- The same under aggregate_mode = no_domains: the site name is not read at all, so the answer is
-- the one the database would give with no attribution stored, and no name selects or orders
-- anything.
-- statement: widget_flow_destinations_no_domains
SELECT f.local_interface_id AS interface_id,
       f.local_client_id AS client_id,
       c.owner_id AS owner_id,
       f.traffic_direction AS traffic_direction,
       f.peer_address AS peer_address,
       CASE WHEN f.peer_address IS NULL THEN NULL
            WHEN g.address IS NULL THEN 'no_row'
            ELSE g.lookup_state END AS lookup_state,
       g.asn AS asn,
       CAST(NULL AS TEXT) AS site_name,
       f.dst_port AS port,
       f.protocol AS protocol,
       count(*) AS connections,
       sum(f.packet_bytes) AS bytes,
       sum(CASE WHEN f.action IN ('block', 'reject') THEN 1 ELSE 0 END) AS blocked,
       sum(CASE WHEN f.action IN ('block', 'reject') THEN f.packet_bytes ELSE 0 END) AS blocked_bytes,
       max(f.observed_at) AS newest
FROM classified_flow AS f
LEFT JOIN client AS c ON c.id = f.local_client_id
LEFT JOIN geo_asn AS g ON g.address = f.peer_address
WHERE f.observed_at >= :from
  AND f.observed_at < :to
  AND f.exit_leg_device IS NULL
GROUP BY 1, 2, 3, 4, 5, 6, 7, 8, 9, 10;


-- ---------------------------------------------------------------------------
-- The same four reads of the `netflow` measure (measure.go): the same rows and connections, with the
-- bytes the NetFlow export reported for them in place of the logged bytes, then the packets and the
-- connections measured, partial, unmeasured and not joinable. A flow's figure is its connection's
-- measured volume, counted on its first leg's row (no second leg is read here), as the aggregate
-- refresh counts it (internal/store, insert_volume).
-- ---------------------------------------------------------------------------

-- statement: widget_flow_parts_netflow
SELECT f.traffic_direction AS traffic_direction,
       f.protocol AS protocol,
       f.dst_port AS port,
       CASE WHEN f.action = 'pass' THEN 'allowed'
            WHEN f.action IN ('block', 'reject') THEN
                 CASE WHEN f.log_reason = 'match' THEN 'firewall_rule'
                      WHEN f.log_reason IN ('bad-offset', 'fragment', 'short', 'normalize', 'memory',
                                            'bad-timestamp', 'congestion', 'ip-option', 'proto-cksum',
                                            'state-mismatch', 'state-insert', 'state-limit',
                                            'src-limit', 'synproxy') THEN 'firewall_no_rule'
                      ELSE 'firewall_reason_not_recorded' END
            ELSE 'unknown' END AS decision,
       sum(n.bytes) AS bytes,
       sum(CASE WHEN f.action = 'pass' THEN n.bytes ELSE 0 END) AS allowed_bytes,
       sum(CASE WHEN f.action IN ('block', 'reject') THEN n.bytes ELSE 0 END) AS blocked_bytes,
       sum(CASE WHEN f.action = 'unknown' THEN n.bytes ELSE 0 END) AS unknown_bytes,
       sum(CASE WHEN f.action = 'pass' THEN 1 ELSE 0 END) AS allowed_connections,
       sum(CASE WHEN f.action IN ('block', 'reject') THEN 1 ELSE 0 END) AS blocked_connections,
       sum(CASE WHEN f.action = 'unknown' THEN 1 ELSE 0 END) AS unknown_connections,
       sum(n.packets) AS packets,
       sum(CASE WHEN n.coverage = 'measured' THEN 1 ELSE 0 END) AS measured_connections,
       sum(CASE WHEN n.coverage = 'partial' THEN 1 ELSE 0 END) AS partial_connections,
       sum(CASE WHEN n.coverage = 'unmeasured' THEN 1 ELSE 0 END) AS unmeasured_connections,
       sum(CASE WHEN n.coverage = 'not_joinable' THEN 1 ELSE 0 END) AS not_joinable_connections
FROM classified_flow AS f
LEFT JOIN LATERAL (
    SELECT coalesce(sum(v.bytes), 0) AS bytes,
           coalesce(sum(v.packets), 0) AS packets,
           CASE WHEN count(*) = 0 THEN 'not_joinable'
                WHEN count(*) FILTER (WHERE v.state = 'measured') = 2 THEN 'measured'
                WHEN count(*) FILTER (WHERE v.records > 0) = 0 THEN 'unmeasured'
                ELSE 'partial' END AS coverage
    FROM connection_measured_volume AS v
    WHERE v.connection_flow_id = f.id
) AS n ON true
WHERE f.observed_at >= :from
  AND f.observed_at < :to
  AND f.exit_leg_device IS NULL
  AND (:scope = 'all' OR f.traffic_scope = :scope)
  AND (:interface_ids IS NULL
       OR f.local_interface_id IN (SELECT CAST(j.value AS BIGINT) FROM jsonb_array_elements_text(CAST(:interface_ids AS jsonb)) AS j(value)))
  AND (:client_ids IS NULL
       OR f.local_client_id IN (SELECT CAST(j.value AS BIGINT) FROM jsonb_array_elements_text(CAST(:client_ids AS jsonb)) AS j(value)))
GROUP BY 1, 2, 3, 4;


-- statement: widget_flow_recent_parts_netflow
SELECT f.observed_at - f.observed_at % :width AS bucket_start,
       f.traffic_direction AS traffic_direction,
       f.protocol AS protocol,
       f.dst_port AS port,
       CASE WHEN f.action = 'pass' THEN 'allowed'
            WHEN f.action IN ('block', 'reject') THEN
                 CASE WHEN f.log_reason = 'match' THEN 'firewall_rule'
                      WHEN f.log_reason IN ('bad-offset', 'fragment', 'short', 'normalize', 'memory',
                                            'bad-timestamp', 'congestion', 'ip-option', 'proto-cksum',
                                            'state-mismatch', 'state-insert', 'state-limit',
                                            'src-limit', 'synproxy') THEN 'firewall_no_rule'
                      ELSE 'firewall_reason_not_recorded' END
            ELSE 'unknown' END AS decision,
       f.local_interface_id AS interface_id,
       f.local_client_id AS client_id,
       sum(n.bytes) AS bytes,
       sum(CASE WHEN f.action = 'pass' THEN n.bytes ELSE 0 END) AS allowed_bytes,
       sum(CASE WHEN f.action IN ('block', 'reject') THEN n.bytes ELSE 0 END) AS blocked_bytes,
       sum(CASE WHEN f.action = 'unknown' THEN n.bytes ELSE 0 END) AS unknown_bytes,
       sum(CASE WHEN f.action = 'pass' THEN 1 ELSE 0 END) AS allowed_connections,
       sum(CASE WHEN f.action IN ('block', 'reject') THEN 1 ELSE 0 END) AS blocked_connections,
       sum(CASE WHEN f.action = 'unknown' THEN 1 ELSE 0 END) AS unknown_connections,
       sum(n.packets) AS packets,
       sum(CASE WHEN n.coverage = 'measured' THEN 1 ELSE 0 END) AS measured_connections,
       sum(CASE WHEN n.coverage = 'partial' THEN 1 ELSE 0 END) AS partial_connections,
       sum(CASE WHEN n.coverage = 'unmeasured' THEN 1 ELSE 0 END) AS unmeasured_connections,
       sum(CASE WHEN n.coverage = 'not_joinable' THEN 1 ELSE 0 END) AS not_joinable_connections
FROM classified_flow AS f
LEFT JOIN LATERAL (
    SELECT coalesce(sum(v.bytes), 0) AS bytes,
           coalesce(sum(v.packets), 0) AS packets,
           CASE WHEN count(*) = 0 THEN 'not_joinable'
                WHEN count(*) FILTER (WHERE v.state = 'measured') = 2 THEN 'measured'
                WHEN count(*) FILTER (WHERE v.records > 0) = 0 THEN 'unmeasured'
                ELSE 'partial' END AS coverage
    FROM connection_measured_volume AS v
    WHERE v.connection_flow_id = f.id
) AS n ON true
WHERE f.observed_at >= :from
  AND f.observed_at < :to
  AND f.exit_leg_device IS NULL
  AND (:scope = 'all' OR f.traffic_scope = :scope)
  AND (:interface_ids IS NULL
       OR f.local_interface_id IN (SELECT CAST(j.value AS BIGINT) FROM jsonb_array_elements_text(CAST(:interface_ids AS jsonb)) AS j(value)))
  AND (:client_ids IS NULL
       OR f.local_client_id IN (SELECT CAST(j.value AS BIGINT) FROM jsonb_array_elements_text(CAST(:client_ids AS jsonb)) AS j(value)))
GROUP BY 1, 2, 3, 4, 5, 6, 7;


-- statement: widget_flow_destinations_netflow
SELECT f.local_interface_id AS interface_id,
       f.local_client_id AS client_id,
       c.owner_id AS owner_id,
       f.traffic_direction AS traffic_direction,
       f.peer_address AS peer_address,
       CASE WHEN f.peer_address IS NULL THEN NULL
            WHEN g.address IS NULL THEN 'no_row'
            ELSE g.lookup_state END AS lookup_state,
       g.asn AS asn,
       a.site_name AS site_name,
       f.dst_port AS port,
       f.protocol AS protocol,
       count(*) AS connections,
       sum(n.bytes) AS bytes,
       sum(CASE WHEN f.action IN ('block', 'reject') THEN 1 ELSE 0 END) AS blocked,
       sum(CASE WHEN f.action IN ('block', 'reject') THEN n.bytes ELSE 0 END) AS blocked_bytes,
       max(f.observed_at) AS newest,
       sum(n.packets) AS packets,
       sum(CASE WHEN n.coverage = 'measured' THEN 1 ELSE 0 END) AS measured_connections,
       sum(CASE WHEN n.coverage = 'partial' THEN 1 ELSE 0 END) AS partial_connections,
       sum(CASE WHEN n.coverage = 'unmeasured' THEN 1 ELSE 0 END) AS unmeasured_connections,
       sum(CASE WHEN n.coverage = 'not_joinable' THEN 1 ELSE 0 END) AS not_joinable_connections
FROM classified_flow AS f
LEFT JOIN client AS c ON c.id = f.local_client_id
LEFT JOIN geo_asn AS g ON g.address = f.peer_address
LEFT JOIN domain_attribution AS a ON a.flow_id = f.id
LEFT JOIN LATERAL (
    SELECT coalesce(sum(v.bytes), 0) AS bytes,
           coalesce(sum(v.packets), 0) AS packets,
           CASE WHEN count(*) = 0 THEN 'not_joinable'
                WHEN count(*) FILTER (WHERE v.state = 'measured') = 2 THEN 'measured'
                WHEN count(*) FILTER (WHERE v.records > 0) = 0 THEN 'unmeasured'
                ELSE 'partial' END AS coverage
    FROM connection_measured_volume AS v
    WHERE v.connection_flow_id = f.id
) AS n ON true
WHERE f.observed_at >= :from
  AND f.observed_at < :to
  AND f.exit_leg_device IS NULL
GROUP BY 1, 2, 3, 4, 5, 6, 7, 8, 9, 10;


-- statement: widget_flow_destinations_no_domains_netflow
SELECT f.local_interface_id AS interface_id,
       f.local_client_id AS client_id,
       c.owner_id AS owner_id,
       f.traffic_direction AS traffic_direction,
       f.peer_address AS peer_address,
       CASE WHEN f.peer_address IS NULL THEN NULL
            WHEN g.address IS NULL THEN 'no_row'
            ELSE g.lookup_state END AS lookup_state,
       g.asn AS asn,
       CAST(NULL AS TEXT) AS site_name,
       f.dst_port AS port,
       f.protocol AS protocol,
       count(*) AS connections,
       sum(n.bytes) AS bytes,
       sum(CASE WHEN f.action IN ('block', 'reject') THEN 1 ELSE 0 END) AS blocked,
       sum(CASE WHEN f.action IN ('block', 'reject') THEN n.bytes ELSE 0 END) AS blocked_bytes,
       max(f.observed_at) AS newest,
       sum(n.packets) AS packets,
       sum(CASE WHEN n.coverage = 'measured' THEN 1 ELSE 0 END) AS measured_connections,
       sum(CASE WHEN n.coverage = 'partial' THEN 1 ELSE 0 END) AS partial_connections,
       sum(CASE WHEN n.coverage = 'unmeasured' THEN 1 ELSE 0 END) AS unmeasured_connections,
       sum(CASE WHEN n.coverage = 'not_joinable' THEN 1 ELSE 0 END) AS not_joinable_connections
FROM classified_flow AS f
LEFT JOIN client AS c ON c.id = f.local_client_id
LEFT JOIN geo_asn AS g ON g.address = f.peer_address
LEFT JOIN LATERAL (
    SELECT coalesce(sum(v.bytes), 0) AS bytes,
           coalesce(sum(v.packets), 0) AS packets,
           CASE WHEN count(*) = 0 THEN 'not_joinable'
                WHEN count(*) FILTER (WHERE v.state = 'measured') = 2 THEN 'measured'
                WHEN count(*) FILTER (WHERE v.records > 0) = 0 THEN 'unmeasured'
                ELSE 'partial' END AS coverage
    FROM connection_measured_volume AS v
    WHERE v.connection_flow_id = f.id
) AS n ON true
WHERE f.observed_at >= :from
  AND f.observed_at < :to
  AND f.exit_leg_device IS NULL
GROUP BY 1, 2, 3, 4, 5, 6, 7, 8, 9, 10;
