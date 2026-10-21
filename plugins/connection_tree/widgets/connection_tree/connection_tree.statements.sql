-- opnview — the read statements the connection_tree widget's script runs.
--
-- Same conventions as every *.statements.sql file of the tree: each statement begins with its
-- marker line and binds its parameters by name. sql/schema-checks.sh plans each statement at both
-- seed sizes. Every column is named with AS, because the script reads a row as an object of its
-- columns.
--
-- THE TREE IS GROUPED HERE, LEVEL BY LEVEL, so the script receives the nodes it draws -- hundreds of
-- rows -- rather than every destination of the window, tens of thousands over 30 days (decision 1 of
-- the performance cycle, 18 October 2026, as PA18.2 of the plugin-API amendment allows). The rule is
-- the one the script applied to the core's destination rows (host.flows.destinations), unchanged:
--
--   * a destination row is the flows of one inside end, direction, outside destination, site name,
--     port and protocol, as the core groups them (internal/widget/traffic/flows.statements.sql,
--     widget_flow_destinations); with include_blocked false, a row whose every flow was blocked is
--     left out whole, and a row holding an allowed flow counts only its allowed connections and bytes
--     but keeps every flow's instant;
--   * every flow lands in exactly one node per level on each side: inside, the chosen root -- an
--     interface, a client or a person -- with the client below it, or this firewall; outside, the
--     operator or the condition the destination is in, its site name or address below that, and the
--     service, port and protocol together below that (depth 3);
--   * each level is ranked within its parent by the chosen sort -- the measure's value, the
--     connections or the latest instant, most first, or the key -- and equal values by key in byte
--     order; past limit_per_level, the level's nodes are folded into one remainder row per parent,
--     members the nodes it folds, and the levels below a folded node are not read;
--   * one row of side 'total' sums every connection the tree counts, members the flows it counts.
--
-- A row of side 'outside', level 2, keyed by an address carries that address as peer_address, so the
-- geolocations of the addresses drawn are read together (host.query).

-- statement: widget_tree_nodes
-- plan-with: include_blocked=true
-- plan-with: root=interface
-- plan-with: depth=2
-- plan-with: sort=volume
-- plan-with: value_by=bytes
-- plan-with: limit=10
WITH flows AS (
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
           coalesce(f.packet_bytes, 0) AS bytes,
           CASE WHEN f.action IN ('block', 'reject') THEN 1 ELSE 0 END AS blocked,
           f.observed_at AS observed_at
    FROM plugin_pf.classified_flow AS f
    LEFT JOIN plugin_installation.client AS c ON c.id = f.local_client_id
    LEFT JOIN plugin_maxmind.geo_asn AS g ON g.address = f.peer_address
    LEFT JOIN plugin_unbound.domain_attribution AS a ON a.flow_id = f.id
    WHERE f.observed_at >= :from
      AND f.observed_at < :to
      AND f.exit_leg_device IS NULL
      AND (CAST(:interface_ids AS jsonb) IS NULL
           OR f.local_interface_id IN (SELECT CAST(j.value AS BIGINT)
                                       FROM jsonb_array_elements_text(CAST(:interface_ids AS jsonb)) AS j(value)))
      AND (CAST(:client_ids AS jsonb) IS NULL
           OR f.local_client_id IN (SELECT CAST(j.value AS BIGINT)
                                    FROM jsonb_array_elements_text(CAST(:client_ids AS jsonb)) AS j(value)))
      AND (CAST(:owner_ids AS jsonb) IS NULL
           OR c.owner_id IN (SELECT CAST(j.value AS BIGINT)
                             FROM jsonb_array_elements_text(CAST(:owner_ids AS jsonb)) AS j(value)))
),
kept AS (
    -- Every flow, when the blocked are counted: a condition on a parameter alone, which PostgreSQL
    -- evaluates once, before reading either branch, so the window below is read only when it is needed.
    SELECT flows.* FROM flows WHERE CAST(:include_blocked AS BOOLEAN)
    UNION ALL
    SELECT w.interface_id, w.client_id, w.owner_id, w.traffic_direction, w.peer_address, w.lookup_state, w.asn,
           w.site_name, w.port, w.protocol, w.bytes, w.blocked, w.observed_at
    FROM (SELECT flows.*,
                 bool_or(flows.blocked = 0) OVER (PARTITION BY flows.interface_id, flows.client_id, flows.owner_id,
                                                               flows.traffic_direction, flows.peer_address,
                                                               flows.lookup_state, flows.asn, flows.site_name,
                                                               flows.port, flows.protocol) AS passed
          FROM flows
          WHERE NOT CAST(:include_blocked AS BOOLEAN)) AS w
    WHERE w.passed
),
counted AS (
    SELECT k.*,
           k.traffic_direction IN ('to_this_firewall', 'from_this_firewall')
               AND (k.peer_address IS NOT NULL OR k.interface_id IS NULL) AS firewall_inside,
           k.traffic_direction IN ('to_this_firewall', 'from_this_firewall') AND k.peer_address IS NULL AS firewall_outside
    FROM kept AS k
),
keyed AS (
    SELECT k.*,
           CASE WHEN CAST(:include_blocked AS BOOLEAN) THEN 1 ELSE 1 - k.blocked END AS counted_connections,
           CASE WHEN CAST(:include_blocked AS BOOLEAN) THEN k.bytes ELSE k.bytes * (1 - k.blocked) END AS counted_bytes,
           CASE WHEN CAST(:include_blocked AS BOOLEAN) THEN k.blocked ELSE 0 END AS counted_blocked,
           CASE WHEN k.firewall_inside THEN 'this_firewall'
                WHEN CAST(:root AS TEXT) = 'interface' THEN 'interface:' || coalesce(CAST(k.interface_id AS TEXT), 'none')
                WHEN CAST(:root AS TEXT) = 'client' THEN 'client:' || coalesce(CAST(k.client_id AS TEXT), 'none')
                WHEN k.client_id IS NULL THEN 'owner:none'
                ELSE 'owner:' || coalesce(CAST(k.owner_id AS TEXT), 'unassigned') END AS inside_first,
           CASE WHEN CAST(:depth AS INTEGER) < 2 OR CAST(:root AS TEXT) = 'client' THEN ''
                WHEN k.firewall_inside THEN 'this_firewall'
                ELSE 'client:' || coalesce(CAST(k.client_id AS TEXT), 'none') END AS inside_second,
           CASE WHEN k.firewall_outside THEN 'this_firewall'
                WHEN k.traffic_direction = 'inter_interface' THEN 'inter_interface'
                WHEN k.lookup_state = 'resolved' THEN 'operator:' || coalesce(CAST(k.asn AS TEXT), 'none')
                WHEN k.lookup_state IS NOT NULL THEN 'unplaced:' || k.lookup_state
                ELSE 'unplaced:no_row' END AS outside_first,
           CASE WHEN CAST(:depth AS INTEGER) < 2 OR k.peer_address IS NULL OR k.firewall_outside
                     OR k.traffic_direction = 'inter_interface' THEN ''
                WHEN k.site_name IS NOT NULL THEN 'site:' || k.site_name
                ELSE 'address:' || k.peer_address END AS outside_second,
           CASE WHEN CAST(:depth AS INTEGER) < 3 OR k.peer_address IS NULL OR k.firewall_outside
                     OR k.traffic_direction = 'inter_interface' THEN ''
                WHEN k.port IS NULL THEN 'port:not_recorded'
                ELSE 'port:' || CAST(k.port AS TEXT) || '/' || coalesce(nullif(k.protocol, ''), 'not_recorded') END
               AS outside_third
    FROM counted AS k
),
nodes AS (
    SELECT CASE WHEN GROUPING(c.inside_first) = 0 THEN 'inside' ELSE 'outside' END AS side,
           CASE WHEN GROUPING(c.outside_third) = 0 THEN 3
                WHEN GROUPING(c.inside_second) = 0 OR GROUPING(c.outside_second) = 0 THEN 2
                ELSE 1 END AS level,
           CASE WHEN GROUPING(c.inside_second) = 0 THEN c.inside_first
                WHEN GROUPING(c.outside_second) = 0 THEN c.outside_first
                ELSE '' END AS parent_first,
           CASE WHEN GROUPING(c.outside_third) = 0 THEN c.outside_second ELSE '' END AS parent_second,
           CASE WHEN GROUPING(c.outside_third) = 0 THEN c.outside_third
                WHEN GROUPING(c.outside_second) = 0 THEN c.outside_second
                WHEN GROUPING(c.outside_first) = 0 THEN c.outside_first
                WHEN GROUPING(c.inside_second) = 0 THEN c.inside_second
                ELSE c.inside_first END AS node,
           sum(c.counted_connections) AS connections,
           sum(c.counted_bytes) AS bytes,
           sum(c.counted_blocked) AS blocked,
           max(c.observed_at) AS newest
    FROM keyed AS c
    GROUP BY GROUPING SETS ((c.inside_first), (c.inside_first, c.inside_second), (c.outside_first),
                            (c.outside_first, c.outside_second), (c.outside_first, c.outside_second, c.outside_third))
),
ranked AS (
    SELECT n.*,
           row_number() OVER (PARTITION BY n.side, n.level, n.parent_first, n.parent_second
                              ORDER BY CASE CAST(:sort AS TEXT)
                                           WHEN 'connections' THEN n.connections
                                           WHEN 'recency' THEN n.newest
                                           WHEN 'name' THEN 0
                                           ELSE CASE WHEN CAST(:value_by AS TEXT) = 'connections'
                                                     THEN n.connections ELSE n.bytes END
                                       END DESC,
                                       n.node COLLATE "C") AS position
    FROM nodes AS n
    WHERE n.node <> ''
),
shown AS (
    SELECT r.*,
           CASE WHEN r.position <= CAST(:limit AS BIGINT) THEN r.node ELSE 'remainder' END AS shown_node
    FROM ranked AS r
    WHERE r.level = 1
       OR (EXISTS (SELECT 1 FROM ranked AS p
                   WHERE p.side = r.side AND p.level = 1 AND p.node = r.parent_first
                     AND p.position <= CAST(:limit AS BIGINT))
           AND (r.level = 2
                OR EXISTS (SELECT 1 FROM ranked AS q
                           WHERE q.side = r.side AND q.level = 2 AND q.parent_first = r.parent_first
                             AND q.node = r.parent_second AND q.position <= CAST(:limit AS BIGINT))))
)
SELECT s.side AS side,
       s.level AS level,
       s.parent_first AS parent_first,
       s.parent_second AS parent_second,
       s.shown_node AS node,
       CASE WHEN s.shown_node LIKE 'address:%' THEN substr(s.shown_node, length('address:') + 1) END AS peer_address,
       sum(s.connections) AS connections,
       sum(s.bytes) AS bytes,
       sum(s.blocked) AS blocked,
       max(s.newest) AS newest,
       count(*) AS members
FROM shown AS s
GROUP BY s.side, s.level, s.parent_first, s.parent_second, s.shown_node
UNION ALL
SELECT 'total', 0, '', '', 'total', CAST(NULL AS TEXT),
       coalesce(sum(c.counted_connections), 0),
       coalesce(sum(c.counted_bytes), 0),
       coalesce(sum(c.counted_blocked), 0),
       0,
       count(*)
FROM keyed AS c;


-- The same under aggregate_mode = no_domains: the site name is not read at all, so every destination
-- below its operator is its address, as the database would give it with no attribution stored.
-- statement: widget_tree_nodes_no_domains
-- plan-with: include_blocked=true
-- plan-with: root=interface
-- plan-with: depth=2
-- plan-with: sort=volume
-- plan-with: value_by=bytes
-- plan-with: limit=10
WITH flows AS (
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
           coalesce(f.packet_bytes, 0) AS bytes,
           CASE WHEN f.action IN ('block', 'reject') THEN 1 ELSE 0 END AS blocked,
           f.observed_at AS observed_at
    FROM plugin_pf.classified_flow AS f
    LEFT JOIN plugin_installation.client AS c ON c.id = f.local_client_id
    LEFT JOIN plugin_maxmind.geo_asn AS g ON g.address = f.peer_address
    WHERE f.observed_at >= :from
      AND f.observed_at < :to
      AND f.exit_leg_device IS NULL
      AND (CAST(:interface_ids AS jsonb) IS NULL
           OR f.local_interface_id IN (SELECT CAST(j.value AS BIGINT)
                                       FROM jsonb_array_elements_text(CAST(:interface_ids AS jsonb)) AS j(value)))
      AND (CAST(:client_ids AS jsonb) IS NULL
           OR f.local_client_id IN (SELECT CAST(j.value AS BIGINT)
                                    FROM jsonb_array_elements_text(CAST(:client_ids AS jsonb)) AS j(value)))
      AND (CAST(:owner_ids AS jsonb) IS NULL
           OR c.owner_id IN (SELECT CAST(j.value AS BIGINT)
                             FROM jsonb_array_elements_text(CAST(:owner_ids AS jsonb)) AS j(value)))
),
kept AS (
    -- Every flow, when the blocked are counted: a condition on a parameter alone, which PostgreSQL
    -- evaluates once, before reading either branch, so the window below is read only when it is needed.
    SELECT flows.* FROM flows WHERE CAST(:include_blocked AS BOOLEAN)
    UNION ALL
    SELECT w.interface_id, w.client_id, w.owner_id, w.traffic_direction, w.peer_address, w.lookup_state, w.asn,
           w.site_name, w.port, w.protocol, w.bytes, w.blocked, w.observed_at
    FROM (SELECT flows.*,
                 bool_or(flows.blocked = 0) OVER (PARTITION BY flows.interface_id, flows.client_id, flows.owner_id,
                                                               flows.traffic_direction, flows.peer_address,
                                                               flows.lookup_state, flows.asn, flows.site_name,
                                                               flows.port, flows.protocol) AS passed
          FROM flows
          WHERE NOT CAST(:include_blocked AS BOOLEAN)) AS w
    WHERE w.passed
),
counted AS (
    SELECT k.*,
           k.traffic_direction IN ('to_this_firewall', 'from_this_firewall')
               AND (k.peer_address IS NOT NULL OR k.interface_id IS NULL) AS firewall_inside,
           k.traffic_direction IN ('to_this_firewall', 'from_this_firewall') AND k.peer_address IS NULL AS firewall_outside
    FROM kept AS k
),
keyed AS (
    SELECT k.*,
           CASE WHEN CAST(:include_blocked AS BOOLEAN) THEN 1 ELSE 1 - k.blocked END AS counted_connections,
           CASE WHEN CAST(:include_blocked AS BOOLEAN) THEN k.bytes ELSE k.bytes * (1 - k.blocked) END AS counted_bytes,
           CASE WHEN CAST(:include_blocked AS BOOLEAN) THEN k.blocked ELSE 0 END AS counted_blocked,
           CASE WHEN k.firewall_inside THEN 'this_firewall'
                WHEN CAST(:root AS TEXT) = 'interface' THEN 'interface:' || coalesce(CAST(k.interface_id AS TEXT), 'none')
                WHEN CAST(:root AS TEXT) = 'client' THEN 'client:' || coalesce(CAST(k.client_id AS TEXT), 'none')
                WHEN k.client_id IS NULL THEN 'owner:none'
                ELSE 'owner:' || coalesce(CAST(k.owner_id AS TEXT), 'unassigned') END AS inside_first,
           CASE WHEN CAST(:depth AS INTEGER) < 2 OR CAST(:root AS TEXT) = 'client' THEN ''
                WHEN k.firewall_inside THEN 'this_firewall'
                ELSE 'client:' || coalesce(CAST(k.client_id AS TEXT), 'none') END AS inside_second,
           CASE WHEN k.firewall_outside THEN 'this_firewall'
                WHEN k.traffic_direction = 'inter_interface' THEN 'inter_interface'
                WHEN k.lookup_state = 'resolved' THEN 'operator:' || coalesce(CAST(k.asn AS TEXT), 'none')
                WHEN k.lookup_state IS NOT NULL THEN 'unplaced:' || k.lookup_state
                ELSE 'unplaced:no_row' END AS outside_first,
           CASE WHEN CAST(:depth AS INTEGER) < 2 OR k.peer_address IS NULL OR k.firewall_outside
                     OR k.traffic_direction = 'inter_interface' THEN ''
                WHEN k.site_name IS NOT NULL THEN 'site:' || k.site_name
                ELSE 'address:' || k.peer_address END AS outside_second,
           CASE WHEN CAST(:depth AS INTEGER) < 3 OR k.peer_address IS NULL OR k.firewall_outside
                     OR k.traffic_direction = 'inter_interface' THEN ''
                WHEN k.port IS NULL THEN 'port:not_recorded'
                ELSE 'port:' || CAST(k.port AS TEXT) || '/' || coalesce(nullif(k.protocol, ''), 'not_recorded') END
               AS outside_third
    FROM counted AS k
),
nodes AS (
    SELECT CASE WHEN GROUPING(c.inside_first) = 0 THEN 'inside' ELSE 'outside' END AS side,
           CASE WHEN GROUPING(c.outside_third) = 0 THEN 3
                WHEN GROUPING(c.inside_second) = 0 OR GROUPING(c.outside_second) = 0 THEN 2
                ELSE 1 END AS level,
           CASE WHEN GROUPING(c.inside_second) = 0 THEN c.inside_first
                WHEN GROUPING(c.outside_second) = 0 THEN c.outside_first
                ELSE '' END AS parent_first,
           CASE WHEN GROUPING(c.outside_third) = 0 THEN c.outside_second ELSE '' END AS parent_second,
           CASE WHEN GROUPING(c.outside_third) = 0 THEN c.outside_third
                WHEN GROUPING(c.outside_second) = 0 THEN c.outside_second
                WHEN GROUPING(c.outside_first) = 0 THEN c.outside_first
                WHEN GROUPING(c.inside_second) = 0 THEN c.inside_second
                ELSE c.inside_first END AS node,
           sum(c.counted_connections) AS connections,
           sum(c.counted_bytes) AS bytes,
           sum(c.counted_blocked) AS blocked,
           max(c.observed_at) AS newest
    FROM keyed AS c
    GROUP BY GROUPING SETS ((c.inside_first), (c.inside_first, c.inside_second), (c.outside_first),
                            (c.outside_first, c.outside_second), (c.outside_first, c.outside_second, c.outside_third))
),
ranked AS (
    SELECT n.*,
           row_number() OVER (PARTITION BY n.side, n.level, n.parent_first, n.parent_second
                              ORDER BY CASE CAST(:sort AS TEXT)
                                           WHEN 'connections' THEN n.connections
                                           WHEN 'recency' THEN n.newest
                                           WHEN 'name' THEN 0
                                           ELSE CASE WHEN CAST(:value_by AS TEXT) = 'connections'
                                                     THEN n.connections ELSE n.bytes END
                                       END DESC,
                                       n.node COLLATE "C") AS position
    FROM nodes AS n
    WHERE n.node <> ''
),
shown AS (
    SELECT r.*,
           CASE WHEN r.position <= CAST(:limit AS BIGINT) THEN r.node ELSE 'remainder' END AS shown_node
    FROM ranked AS r
    WHERE r.level = 1
       OR (EXISTS (SELECT 1 FROM ranked AS p
                   WHERE p.side = r.side AND p.level = 1 AND p.node = r.parent_first
                     AND p.position <= CAST(:limit AS BIGINT))
           AND (r.level = 2
                OR EXISTS (SELECT 1 FROM ranked AS q
                           WHERE q.side = r.side AND q.level = 2 AND q.parent_first = r.parent_first
                             AND q.node = r.parent_second AND q.position <= CAST(:limit AS BIGINT))))
)
SELECT s.side AS side,
       s.level AS level,
       s.parent_first AS parent_first,
       s.parent_second AS parent_second,
       s.shown_node AS node,
       CASE WHEN s.shown_node LIKE 'address:%' THEN substr(s.shown_node, length('address:') + 1) END AS peer_address,
       sum(s.connections) AS connections,
       sum(s.bytes) AS bytes,
       sum(s.blocked) AS blocked,
       max(s.newest) AS newest,
       count(*) AS members
FROM shown AS s
GROUP BY s.side, s.level, s.parent_first, s.parent_second, s.shown_node
UNION ALL
SELECT 'total', 0, '', '', 'total', CAST(NULL AS TEXT),
       coalesce(sum(c.counted_connections), 0),
       coalesce(sum(c.counted_bytes), 0),
       coalesce(sum(c.counted_blocked), 0),
       0,
       count(*)
FROM keyed AS c;


-- The two in a declared measure, each run in its statement's place when the answer is given in it
-- (host.query; docs/widget-api.md, "Volumes and the observation-point limit"): the measure's bytes for
-- the same connections, read through flow_measure as the core's reads read them, and on the total row the
-- measure's parts of every connection the tree counts, measure_parts, which the script hands
-- host.figures.cover.
-- statement: widget_tree_nodes_measured
-- plan-with: include_blocked=true
-- plan-with: root=interface
-- plan-with: depth=2
-- plan-with: sort=volume
-- plan-with: value_by=bytes
-- plan-with: limit=10
WITH flows AS (
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
           n.bytes AS bytes,
           CASE WHEN f.action IN ('block', 'reject') THEN 1 ELSE 0 END AS blocked,
           f.observed_at AS observed_at,
           n.parts AS parts,
           n.not_known_index AS not_known_index,
           n.part_width AS part_width
    FROM plugin_pf.classified_flow AS f
    LEFT JOIN plugin_installation.client AS c ON c.id = f.local_client_id
    LEFT JOIN plugin_maxmind.geo_asn AS g ON g.address = f.peer_address
    LEFT JOIN plugin_unbound.domain_attribution AS a ON a.flow_id = f.id
    LEFT JOIN LATERAL flow_measure(f.id, NULL, NULL, f.action, CAST(:measure AS TEXT)) AS n ON true
    WHERE f.observed_at >= :from
      AND f.observed_at < :to
      AND f.exit_leg_device IS NULL
      AND (CAST(:interface_ids AS jsonb) IS NULL
           OR f.local_interface_id IN (SELECT CAST(j.value AS BIGINT)
                                       FROM jsonb_array_elements_text(CAST(:interface_ids AS jsonb)) AS j(value)))
      AND (CAST(:client_ids AS jsonb) IS NULL
           OR f.local_client_id IN (SELECT CAST(j.value AS BIGINT)
                                    FROM jsonb_array_elements_text(CAST(:client_ids AS jsonb)) AS j(value)))
      AND (CAST(:owner_ids AS jsonb) IS NULL
           OR c.owner_id IN (SELECT CAST(j.value AS BIGINT)
                             FROM jsonb_array_elements_text(CAST(:owner_ids AS jsonb)) AS j(value)))
),
kept AS (
    -- Every flow, when the blocked are counted: a condition on a parameter alone, which PostgreSQL
    -- evaluates once, before reading either branch, so the window below is read only when it is needed.
    SELECT flows.* FROM flows WHERE CAST(:include_blocked AS BOOLEAN)
    UNION ALL
    SELECT w.interface_id, w.client_id, w.owner_id, w.traffic_direction, w.peer_address, w.lookup_state, w.asn,
           w.site_name, w.port, w.protocol, w.bytes, w.blocked, w.observed_at, w.parts, w.not_known_index, w.part_width
    FROM (SELECT flows.*,
                 bool_or(flows.blocked = 0) OVER (PARTITION BY flows.interface_id, flows.client_id, flows.owner_id,
                                                               flows.traffic_direction, flows.peer_address,
                                                               flows.lookup_state, flows.asn, flows.site_name,
                                                               flows.port, flows.protocol) AS passed
          FROM flows
          WHERE NOT CAST(:include_blocked AS BOOLEAN)) AS w
    WHERE w.passed
),
counted AS (
    SELECT k.*,
           k.traffic_direction IN ('to_this_firewall', 'from_this_firewall')
               AND (k.peer_address IS NOT NULL OR k.interface_id IS NULL) AS firewall_inside,
           k.traffic_direction IN ('to_this_firewall', 'from_this_firewall') AND k.peer_address IS NULL AS firewall_outside
    FROM kept AS k
),
keyed AS (
    SELECT k.*,
           CASE WHEN CAST(:include_blocked AS BOOLEAN) THEN 1 ELSE 1 - k.blocked END AS counted_connections,
           CASE WHEN CAST(:include_blocked AS BOOLEAN) THEN k.bytes ELSE k.bytes * (1 - k.blocked) END AS counted_bytes,
           CASE WHEN CAST(:include_blocked AS BOOLEAN) THEN k.blocked ELSE 0 END AS counted_blocked,
           CASE WHEN k.firewall_inside THEN 'this_firewall'
                WHEN CAST(:root AS TEXT) = 'interface' THEN 'interface:' || coalesce(CAST(k.interface_id AS TEXT), 'none')
                WHEN CAST(:root AS TEXT) = 'client' THEN 'client:' || coalesce(CAST(k.client_id AS TEXT), 'none')
                WHEN k.client_id IS NULL THEN 'owner:none'
                ELSE 'owner:' || coalesce(CAST(k.owner_id AS TEXT), 'unassigned') END AS inside_first,
           CASE WHEN CAST(:depth AS INTEGER) < 2 OR CAST(:root AS TEXT) = 'client' THEN ''
                WHEN k.firewall_inside THEN 'this_firewall'
                ELSE 'client:' || coalesce(CAST(k.client_id AS TEXT), 'none') END AS inside_second,
           CASE WHEN k.firewall_outside THEN 'this_firewall'
                WHEN k.traffic_direction = 'inter_interface' THEN 'inter_interface'
                WHEN k.lookup_state = 'resolved' THEN 'operator:' || coalesce(CAST(k.asn AS TEXT), 'none')
                WHEN k.lookup_state IS NOT NULL THEN 'unplaced:' || k.lookup_state
                ELSE 'unplaced:no_row' END AS outside_first,
           CASE WHEN CAST(:depth AS INTEGER) < 2 OR k.peer_address IS NULL OR k.firewall_outside
                     OR k.traffic_direction = 'inter_interface' THEN ''
                WHEN k.site_name IS NOT NULL THEN 'site:' || k.site_name
                ELSE 'address:' || k.peer_address END AS outside_second,
           CASE WHEN CAST(:depth AS INTEGER) < 3 OR k.peer_address IS NULL OR k.firewall_outside
                     OR k.traffic_direction = 'inter_interface' THEN ''
                WHEN k.port IS NULL THEN 'port:not_recorded'
                ELSE 'port:' || CAST(k.port AS TEXT) || '/' || coalesce(nullif(k.protocol, ''), 'not_recorded') END
               AS outside_third
    FROM counted AS k
),
nodes AS (
    SELECT CASE WHEN GROUPING(c.inside_first) = 0 THEN 'inside' ELSE 'outside' END AS side,
           CASE WHEN GROUPING(c.outside_third) = 0 THEN 3
                WHEN GROUPING(c.inside_second) = 0 OR GROUPING(c.outside_second) = 0 THEN 2
                ELSE 1 END AS level,
           CASE WHEN GROUPING(c.inside_second) = 0 THEN c.inside_first
                WHEN GROUPING(c.outside_second) = 0 THEN c.outside_first
                ELSE '' END AS parent_first,
           CASE WHEN GROUPING(c.outside_third) = 0 THEN c.outside_second ELSE '' END AS parent_second,
           CASE WHEN GROUPING(c.outside_third) = 0 THEN c.outside_third
                WHEN GROUPING(c.outside_second) = 0 THEN c.outside_second
                WHEN GROUPING(c.outside_first) = 0 THEN c.outside_first
                WHEN GROUPING(c.inside_second) = 0 THEN c.inside_second
                ELSE c.inside_first END AS node,
           sum(c.counted_connections) AS connections,
           sum(c.counted_bytes) AS bytes,
           sum(c.counted_blocked) AS blocked,
           max(c.observed_at) AS newest
    FROM keyed AS c
    GROUP BY GROUPING SETS ((c.inside_first), (c.inside_first, c.inside_second), (c.outside_first),
                            (c.outside_first, c.outside_second), (c.outside_first, c.outside_second, c.outside_third))
),
ranked AS (
    SELECT n.*,
           row_number() OVER (PARTITION BY n.side, n.level, n.parent_first, n.parent_second
                              ORDER BY CASE CAST(:sort AS TEXT)
                                           WHEN 'connections' THEN n.connections
                                           WHEN 'recency' THEN n.newest
                                           WHEN 'name' THEN 0
                                           ELSE CASE WHEN CAST(:value_by AS TEXT) = 'connections'
                                                     THEN n.connections ELSE n.bytes END
                                       END DESC,
                                       n.node COLLATE "C") AS position
    FROM nodes AS n
    WHERE n.node <> ''
),
shown AS (
    SELECT r.*,
           CASE WHEN r.position <= CAST(:limit AS BIGINT) THEN r.node ELSE 'remainder' END AS shown_node
    FROM ranked AS r
    WHERE r.level = 1
       OR (EXISTS (SELECT 1 FROM ranked AS p
                   WHERE p.side = r.side AND p.level = 1 AND p.node = r.parent_first
                     AND p.position <= CAST(:limit AS BIGINT))
           AND (r.level = 2
                OR EXISTS (SELECT 1 FROM ranked AS q
                           WHERE q.side = r.side AND q.level = 2 AND q.parent_first = r.parent_first
                             AND q.node = r.parent_second AND q.position <= CAST(:limit AS BIGINT))))
)
SELECT s.side AS side,
       s.level AS level,
       s.parent_first AS parent_first,
       s.parent_second AS parent_second,
       s.shown_node AS node,
       CASE WHEN s.shown_node LIKE 'address:%' THEN substr(s.shown_node, length('address:') + 1) END AS peer_address,
       sum(s.connections) AS connections,
       sum(s.bytes) AS bytes,
       sum(s.blocked) AS blocked,
       max(s.newest) AS newest,
       count(*) AS members,
       CAST(:measure AS TEXT) AS measure,
       CAST(NULL AS json) AS measure_parts
FROM shown AS s
GROUP BY s.side, s.level, s.parent_first, s.parent_second, s.shown_node
UNION ALL
SELECT 'total', 0, '', '', 'total', CAST(NULL AS TEXT),
       coalesce(sum(c.counted_connections), 0),
       coalesce(sum(c.counted_bytes), 0),
       coalesce(sum(c.counted_blocked), 0),
       0,
       count(*),
       CAST(:measure AS TEXT),
       measure_parts_total(c.parts, c.not_known_index, c.part_width)
FROM keyed AS c;


-- statement: widget_tree_nodes_no_domains_measured
-- plan-with: include_blocked=true
-- plan-with: root=interface
-- plan-with: depth=2
-- plan-with: sort=volume
-- plan-with: value_by=bytes
-- plan-with: limit=10
WITH flows AS (
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
           n.bytes AS bytes,
           CASE WHEN f.action IN ('block', 'reject') THEN 1 ELSE 0 END AS blocked,
           f.observed_at AS observed_at,
           n.parts AS parts,
           n.not_known_index AS not_known_index,
           n.part_width AS part_width
    FROM plugin_pf.classified_flow AS f
    LEFT JOIN plugin_installation.client AS c ON c.id = f.local_client_id
    LEFT JOIN plugin_maxmind.geo_asn AS g ON g.address = f.peer_address
    LEFT JOIN LATERAL flow_measure(f.id, NULL, NULL, f.action, CAST(:measure AS TEXT)) AS n ON true
    WHERE f.observed_at >= :from
      AND f.observed_at < :to
      AND f.exit_leg_device IS NULL
      AND (CAST(:interface_ids AS jsonb) IS NULL
           OR f.local_interface_id IN (SELECT CAST(j.value AS BIGINT)
                                       FROM jsonb_array_elements_text(CAST(:interface_ids AS jsonb)) AS j(value)))
      AND (CAST(:client_ids AS jsonb) IS NULL
           OR f.local_client_id IN (SELECT CAST(j.value AS BIGINT)
                                    FROM jsonb_array_elements_text(CAST(:client_ids AS jsonb)) AS j(value)))
      AND (CAST(:owner_ids AS jsonb) IS NULL
           OR c.owner_id IN (SELECT CAST(j.value AS BIGINT)
                             FROM jsonb_array_elements_text(CAST(:owner_ids AS jsonb)) AS j(value)))
),
kept AS (
    -- Every flow, when the blocked are counted: a condition on a parameter alone, which PostgreSQL
    -- evaluates once, before reading either branch, so the window below is read only when it is needed.
    SELECT flows.* FROM flows WHERE CAST(:include_blocked AS BOOLEAN)
    UNION ALL
    SELECT w.interface_id, w.client_id, w.owner_id, w.traffic_direction, w.peer_address, w.lookup_state, w.asn,
           w.site_name, w.port, w.protocol, w.bytes, w.blocked, w.observed_at, w.parts, w.not_known_index, w.part_width
    FROM (SELECT flows.*,
                 bool_or(flows.blocked = 0) OVER (PARTITION BY flows.interface_id, flows.client_id, flows.owner_id,
                                                               flows.traffic_direction, flows.peer_address,
                                                               flows.lookup_state, flows.asn, flows.site_name,
                                                               flows.port, flows.protocol) AS passed
          FROM flows
          WHERE NOT CAST(:include_blocked AS BOOLEAN)) AS w
    WHERE w.passed
),
counted AS (
    SELECT k.*,
           k.traffic_direction IN ('to_this_firewall', 'from_this_firewall')
               AND (k.peer_address IS NOT NULL OR k.interface_id IS NULL) AS firewall_inside,
           k.traffic_direction IN ('to_this_firewall', 'from_this_firewall') AND k.peer_address IS NULL AS firewall_outside
    FROM kept AS k
),
keyed AS (
    SELECT k.*,
           CASE WHEN CAST(:include_blocked AS BOOLEAN) THEN 1 ELSE 1 - k.blocked END AS counted_connections,
           CASE WHEN CAST(:include_blocked AS BOOLEAN) THEN k.bytes ELSE k.bytes * (1 - k.blocked) END AS counted_bytes,
           CASE WHEN CAST(:include_blocked AS BOOLEAN) THEN k.blocked ELSE 0 END AS counted_blocked,
           CASE WHEN k.firewall_inside THEN 'this_firewall'
                WHEN CAST(:root AS TEXT) = 'interface' THEN 'interface:' || coalesce(CAST(k.interface_id AS TEXT), 'none')
                WHEN CAST(:root AS TEXT) = 'client' THEN 'client:' || coalesce(CAST(k.client_id AS TEXT), 'none')
                WHEN k.client_id IS NULL THEN 'owner:none'
                ELSE 'owner:' || coalesce(CAST(k.owner_id AS TEXT), 'unassigned') END AS inside_first,
           CASE WHEN CAST(:depth AS INTEGER) < 2 OR CAST(:root AS TEXT) = 'client' THEN ''
                WHEN k.firewall_inside THEN 'this_firewall'
                ELSE 'client:' || coalesce(CAST(k.client_id AS TEXT), 'none') END AS inside_second,
           CASE WHEN k.firewall_outside THEN 'this_firewall'
                WHEN k.traffic_direction = 'inter_interface' THEN 'inter_interface'
                WHEN k.lookup_state = 'resolved' THEN 'operator:' || coalesce(CAST(k.asn AS TEXT), 'none')
                WHEN k.lookup_state IS NOT NULL THEN 'unplaced:' || k.lookup_state
                ELSE 'unplaced:no_row' END AS outside_first,
           CASE WHEN CAST(:depth AS INTEGER) < 2 OR k.peer_address IS NULL OR k.firewall_outside
                     OR k.traffic_direction = 'inter_interface' THEN ''
                WHEN k.site_name IS NOT NULL THEN 'site:' || k.site_name
                ELSE 'address:' || k.peer_address END AS outside_second,
           CASE WHEN CAST(:depth AS INTEGER) < 3 OR k.peer_address IS NULL OR k.firewall_outside
                     OR k.traffic_direction = 'inter_interface' THEN ''
                WHEN k.port IS NULL THEN 'port:not_recorded'
                ELSE 'port:' || CAST(k.port AS TEXT) || '/' || coalesce(nullif(k.protocol, ''), 'not_recorded') END
               AS outside_third
    FROM counted AS k
),
nodes AS (
    SELECT CASE WHEN GROUPING(c.inside_first) = 0 THEN 'inside' ELSE 'outside' END AS side,
           CASE WHEN GROUPING(c.outside_third) = 0 THEN 3
                WHEN GROUPING(c.inside_second) = 0 OR GROUPING(c.outside_second) = 0 THEN 2
                ELSE 1 END AS level,
           CASE WHEN GROUPING(c.inside_second) = 0 THEN c.inside_first
                WHEN GROUPING(c.outside_second) = 0 THEN c.outside_first
                ELSE '' END AS parent_first,
           CASE WHEN GROUPING(c.outside_third) = 0 THEN c.outside_second ELSE '' END AS parent_second,
           CASE WHEN GROUPING(c.outside_third) = 0 THEN c.outside_third
                WHEN GROUPING(c.outside_second) = 0 THEN c.outside_second
                WHEN GROUPING(c.outside_first) = 0 THEN c.outside_first
                WHEN GROUPING(c.inside_second) = 0 THEN c.inside_second
                ELSE c.inside_first END AS node,
           sum(c.counted_connections) AS connections,
           sum(c.counted_bytes) AS bytes,
           sum(c.counted_blocked) AS blocked,
           max(c.observed_at) AS newest
    FROM keyed AS c
    GROUP BY GROUPING SETS ((c.inside_first), (c.inside_first, c.inside_second), (c.outside_first),
                            (c.outside_first, c.outside_second), (c.outside_first, c.outside_second, c.outside_third))
),
ranked AS (
    SELECT n.*,
           row_number() OVER (PARTITION BY n.side, n.level, n.parent_first, n.parent_second
                              ORDER BY CASE CAST(:sort AS TEXT)
                                           WHEN 'connections' THEN n.connections
                                           WHEN 'recency' THEN n.newest
                                           WHEN 'name' THEN 0
                                           ELSE CASE WHEN CAST(:value_by AS TEXT) = 'connections'
                                                     THEN n.connections ELSE n.bytes END
                                       END DESC,
                                       n.node COLLATE "C") AS position
    FROM nodes AS n
    WHERE n.node <> ''
),
shown AS (
    SELECT r.*,
           CASE WHEN r.position <= CAST(:limit AS BIGINT) THEN r.node ELSE 'remainder' END AS shown_node
    FROM ranked AS r
    WHERE r.level = 1
       OR (EXISTS (SELECT 1 FROM ranked AS p
                   WHERE p.side = r.side AND p.level = 1 AND p.node = r.parent_first
                     AND p.position <= CAST(:limit AS BIGINT))
           AND (r.level = 2
                OR EXISTS (SELECT 1 FROM ranked AS q
                           WHERE q.side = r.side AND q.level = 2 AND q.parent_first = r.parent_first
                             AND q.node = r.parent_second AND q.position <= CAST(:limit AS BIGINT))))
)
SELECT s.side AS side,
       s.level AS level,
       s.parent_first AS parent_first,
       s.parent_second AS parent_second,
       s.shown_node AS node,
       CASE WHEN s.shown_node LIKE 'address:%' THEN substr(s.shown_node, length('address:') + 1) END AS peer_address,
       sum(s.connections) AS connections,
       sum(s.bytes) AS bytes,
       sum(s.blocked) AS blocked,
       max(s.newest) AS newest,
       count(*) AS members,
       CAST(:measure AS TEXT) AS measure,
       CAST(NULL AS json) AS measure_parts
FROM shown AS s
GROUP BY s.side, s.level, s.parent_first, s.parent_second, s.shown_node
UNION ALL
SELECT 'total', 0, '', '', 'total', CAST(NULL AS TEXT),
       coalesce(sum(c.counted_connections), 0),
       coalesce(sum(c.counted_bytes), 0),
       coalesce(sum(c.counted_blocked), 0),
       0,
       count(*),
       CAST(:measure AS TEXT),
       measure_parts_total(c.parts, c.not_known_index, c.part_width)
FROM keyed AS c;


-- An operator's name, from any geolocated address carrying its AS number.
-- statement: widget_operator_name
SELECT operator AS operator FROM plugin_maxmind.geo_asn WHERE asn = :asn AND operator IS NOT NULL LIMIT 1;
