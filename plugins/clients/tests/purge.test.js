// The retention purge as this plugin takes part in it (ADR 0021, step 6): a client nothing names any
// more goes once it is past the horizon, never one assigned to a person, never one a surviving flow or
// a purged part still names.
var NOW = 1790000400;
var DAY = 86400;

function client(t, id, lastSeen, owner) {
  t.sql("INSERT INTO plugin_installation.client (id, identity_kind, identity_key, interface_id, last_address, owner_id, " +
    "owner_assigned_at, first_seen_at, last_seen_at) VALUES (:id, 'mac', :key, 1, :address, CAST(:owner AS BIGINT), " +
    "CASE WHEN CAST(:owner AS BIGINT) IS NULL THEN NULL ELSE CAST(:seen AS BIGINT) END, :seen, :seen)",
    { id: id, key: "00:11:22:33:44:0" + id, address: "10.1.0." + id, owner: owner, seen: lastSeen });
}

test("a stale client goes once nothing names it; one assigned, one a flow names and one the purged part names stay", function (t) {
  t.clock(NOW);
  t.sql("INSERT INTO setting (key, value, updated_at) VALUES ('retention_seconds', :value, :now) " +
    "ON CONFLICT (key) DO UPDATE SET value = excluded.value", { value: String(DAY), now: NOW });
  t.sql("INSERT INTO plugin_installation.interface (id, identifier, device, description, link_type, link_kind, " +
    "first_seen_at, last_seen_at) VALUES (1, 'lan', 'igb1', 'LAN', 'ethernet', 'physical', :then, :now)",
    { then: NOW - 10 * DAY, now: NOW });
  t.sql("INSERT INTO plugin_installation.owner (id, display_name, created_at, updated_at) VALUES (1, 'Someone', :now, :now)", { now: NOW });
  var stale = NOW - 5 * DAY;
  client(t, 1, stale, null);
  client(t, 2, stale, 1);
  client(t, 3, stale, null);
  client(t, 4, stale, null);
  client(t, 5, NOW - 60, null);
  // Client 3 is named by a flow inside the horizon, client 4 by the purged part of an hour still kept.
  t.sql("INSERT INTO plugin_pf.flow (id, log_digest, observed_at, ingested_at, interface_device, interface_lookup_state, " +
    "src_interface_id, src_client_id, src_address, dst_address, dst_port, protocol, ip_version, action, direction, " +
    "packet_bytes, rule_lookup_state, traffic_scope) VALUES (9, 'd9', :at, :at, 'igb1', 'resolved', 1, 3, '10.1.0.3', " +
    "'203.0.113.7', 443, 'tcp', 4, 'pass', 'in', 100, 'pending', 'north_south')", { at: NOW - 600 });
  t.sql("INSERT INTO plugin_rollups.purged_flow_hour (hour_start_at, src_interface_id, src_client_id, local_client_id, " +
    "traffic_direction, peer_address, action, bytes, connections, measure_parts, purged_at) VALUES " +
    "(:hour, 1, 4, 4, 'outbound', '203.0.113.7', 'pass', 10, 1, '{}', :now)", { hour: NOW - 2 * DAY - ((NOW - 2 * DAY) % 3600), now: NOW });
  t.purge();
  t.equal(t.query("SELECT id FROM plugin_installation.client ORDER BY id").map(function (row) { return row.id; }), [2, 3, 4, 5],
    "the stale unnamed client went; the assigned one, the named ones and the recent one stay");
});
