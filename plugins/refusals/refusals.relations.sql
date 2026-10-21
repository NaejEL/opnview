-- blocked_decision — every refusal, whichever engine decided it, under one vocabulary. Closes G2.
--
-- A refusal lives in three tables under three column names and three value sets:
-- flow.action IN ('block', 'reject'), dns_resolution.action IN ('block', 'drop') and
-- security_event.event_action = 'blocked'. This view projects each of them once, keyed by
-- (source_table, source_id), so every refusal appears exactly once.
--
-- engine_kind is opnview's own closed vocabulary, recorded in docs/data-model.md, and every refusal
-- has a place in it:
--   firewall_rule                a filter-log record whose reason is `match`
--   firewall_no_rule             one whose reason is another established pf reason: dropped for a
--                                reason no rule expresses
--   firewall_reason_not_recorded one whose reason is absent or outside the established set, so
--                                neither can be claimed
--   dns_advertising_list, dns_tracking_list, dns_threat_list, dns_parental_list, dns_other_list
--                                a lookup refused by a list carrying that user-assigned purpose
--   dns_unassigned_list          refused by a list nobody has classified
--   dns_list_not_recorded        refused with no list named by the resolver
--   security_engine              a security event the engine blocked
-- The purpose is the user's, from blocklist.purpose, and never derived from a list's name.
--
-- engine_reference is what decided inside the engine: the pf label (`rid`), the list's name, or the
-- rule identity. target is what was refused: the destination address, or the domain.
-- target_is_this_firewall is 1 when that address is one the firewall itself holds, which is how a
-- feed says "-> this firewall".
--
-- dns_resolution, blocklist and security_event are read from the core schema until their waves move
-- them to unbound and suricata (ADR 0020, wave 4), and then by their qualified names.
CREATE OR REPLACE VIEW plugin_refusals.blocked_decision AS
SELECT
    'flow'                                                  AS source_table,
    f.id                                                    AS source_id,
    f.observed_at                                           AS occurred_at,
    CASE
        WHEN f.log_reason = 'match' THEN 'firewall_rule'
        WHEN f.log_reason IN ('bad-offset', 'fragment', 'short', 'normalize', 'memory',
                              'bad-timestamp', 'congestion', 'ip-option', 'proto-cksum',
                              'state-mismatch', 'state-insert', 'state-limit',
                              'src-limit', 'synproxy') THEN 'firewall_no_rule'
        ELSE 'firewall_reason_not_recorded'
    END                                                     AS engine_kind,
    f.rid                                                   AS engine_reference,
    f.src_client_id                                         AS client_id,
    f.src_interface_id                                      AS interface_id,
    f.dst_address                                           AS target,
    'address'                                               AS target_kind,
    CAST(CAST(EXISTS (SELECT 1 FROM plugin_installation.interface_address AS a
            WHERE a.address = f.dst_address AND a.source_field IN ('addr4', 'addr6', 'ipv4', 'ipv6')) AS INTEGER) AS BIGINT)
                                                            AS target_is_this_firewall
FROM plugin_pf.flow AS f
WHERE f.action IN ('block', 'reject')
UNION ALL
SELECT
    'dns_resolution',
    r.id,
    r.looked_up_at,
    CASE
        WHEN r.blocklist_id IS NULL THEN 'dns_list_not_recorded'
        WHEN b.purpose IS NULL THEN 'dns_unassigned_list'
        WHEN b.purpose = 'advertising' THEN 'dns_advertising_list'
        WHEN b.purpose = 'tracking' THEN 'dns_tracking_list'
        WHEN b.purpose = 'threat' THEN 'dns_threat_list'
        WHEN b.purpose = 'parental' THEN 'dns_parental_list'
        ELSE 'dns_other_list'
    END,
    b.name,
    r.client_id,
    r.interface_id,
    r.domain,
    'domain',
    0
FROM opnview.dns_resolution AS r
LEFT JOIN opnview.blocklist AS b ON b.id = r.blocklist_id
WHERE r.action IN ('block', 'drop')
UNION ALL
SELECT
    'security_event',
    e.id,
    e.occurred_at,
    'security_engine',
    e.rule_identity,
    e.src_client_id,
    e.src_interface_id,
    e.dst_address,
    'address',
    CAST(CAST(EXISTS (SELECT 1 FROM plugin_installation.interface_address AS a
            WHERE a.address = e.dst_address AND a.source_field IN ('addr4', 'addr6', 'ipv4', 'ipv6')) AS INTEGER) AS BIGINT)
FROM plugin_suricata.security_event AS e
WHERE e.event_action = 'blocked';
