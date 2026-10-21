-- classified_flow — a flow, with the three facts every derivation reads from it written ONCE, as
-- expressions over its stored columns.
--
-- traffic_direction is opnview's own term, taken from the widget catalogue's scopes: 'outbound' (the
-- destination is outside), 'inbound' (the source is outside) or 'inter_interface' (neither end is
-- outside). It is NOT flow.direction, which stays pf's per-interface `dir`: a packet leaving a client
-- for the Internet is `in` on the client's interface. When BOTH ends are outside -- an unsolicited
-- packet to the firewall's own upstream address, or one the firewall sends from it -- membership
-- cannot decide, and pf's `dir` on the interface the record was logged on can: `out` is outbound,
-- anything else inbound. That tie-break is the only place `dir` enters, so the three directions
-- partition the flows exactly.
--
-- local_interface_id and local_client_id name the INSIDE end: the source when it has an interface and
-- is not this firewall, else the destination on the same terms, else nothing. peer_address is the
-- OUTSIDE end: the destination of an outbound flow, the source of an inbound one, and NULL between two
-- interfaces.
--
-- THIS FIREWALL IS NEITHER END KIND, and has two directions of its own: 'from_this_firewall' when the
-- source is this firewall, 'to_this_firewall' when the destination is and the source is not. Its
-- interface is recorded on the flow, but it is not an inside end -- it has no client -- and not an
-- outside one: peer_address of such a flow is the other end when that end is outside, and NULL
-- otherwise.
--
-- exit_leg_device is the device a SECOND LEG was logged on (flow.pair_outcome 'second_leg'), and NULL
-- for every other record. A second leg is counted on that interface's own figures and in the rule
-- family, and in no other figure, so the statements that count read it.
--
-- A view rather than columns: they are pure functions of the row, and a view over one table is
-- flattened by the planner, so every statement that reads it keeps the flow indexes.
CREATE OR REPLACE VIEW plugin_pf.classified_flow AS
SELECT
    id,
    observed_at,
    ingested_at,
    interface_device,
    src_interface_id,
    dst_interface_id,
    src_client_id,
    dst_client_id,
    src_address,
    dst_address,
    src_port,
    dst_port,
    protocol,
    action,
    direction,
    log_reason,
    packet_bytes,
    rid,
    rule_id,
    traffic_scope,
    src_is_this_firewall,
    dst_is_this_firewall,
    pair_outcome,
    paired_flow_id,
    rule_lookup_state,
    ip_version,
    CASE
        WHEN src_is_this_firewall THEN 'from_this_firewall'
        WHEN dst_is_this_firewall THEN 'to_this_firewall'
        WHEN src_interface_id IS NOT NULL AND dst_interface_id IS NOT NULL THEN 'inter_interface'
        WHEN src_interface_id IS NOT NULL THEN 'outbound'
        WHEN dst_interface_id IS NOT NULL THEN 'inbound'
        WHEN direction = 'out' THEN 'outbound'
        ELSE 'inbound'
    END AS traffic_direction,
    CASE
        WHEN NOT src_is_this_firewall AND src_interface_id IS NOT NULL THEN src_interface_id
        WHEN NOT dst_is_this_firewall THEN dst_interface_id
    END AS local_interface_id,
    CASE
        WHEN NOT src_is_this_firewall AND src_interface_id IS NOT NULL THEN src_client_id
        WHEN NOT dst_is_this_firewall AND dst_interface_id IS NOT NULL THEN dst_client_id
    END AS local_client_id,
    CASE
        WHEN src_is_this_firewall AND dst_is_this_firewall THEN NULL
        WHEN src_is_this_firewall THEN CASE WHEN dst_interface_id IS NULL THEN dst_address END
        WHEN dst_is_this_firewall THEN CASE WHEN src_interface_id IS NULL THEN src_address END
        WHEN src_interface_id IS NOT NULL AND dst_interface_id IS NOT NULL THEN NULL
        WHEN src_interface_id IS NOT NULL THEN dst_address
        WHEN dst_interface_id IS NOT NULL THEN src_address
        WHEN direction = 'out' THEN dst_address
        ELSE src_address
    END AS peer_address,
    CASE WHEN pair_outcome = 'second_leg' THEN interface_device END AS exit_leg_device
FROM plugin_pf.flow;

-- blocked_event — every refused filter-log record: action 'block' or 'reject'. A record whose action
-- could not be named ('unknown') is not a refusal and is not here, and it is not counted as allowed
-- anywhere either.
CREATE OR REPLACE VIEW plugin_pf.blocked_event AS
SELECT
    id AS flow_id,
    observed_at,
    ingested_at,
    interface_device,
    interface_lookup_state,
    src_interface_id,
    dst_interface_id,
    src_client_id,
    dst_client_id,
    src_address,
    dst_address,
    src_port,
    dst_port,
    protocol,
    ip_version,
    direction,
    log_reason,
    packet_bytes,
    rid,
    rule_id,
    rule_lookup_state,
    action,
    traffic_scope
FROM plugin_pf.flow
WHERE action IN ('block', 'reject');
