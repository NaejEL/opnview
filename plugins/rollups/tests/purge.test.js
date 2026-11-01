// The retention purge as this plugin takes part in it (ADR 0021, step 6): the purged part written
// before any flow goes, the slots purged by their period's end, the hour and the day kept while the
// week and the month holding them are. The pf plugin, required, purges the flows in the same run.
var NOW = 1790000400;
var DAY = 86400;

function retention(t, seconds) {
  t.sql("INSERT INTO setting (key, value, updated_at) VALUES ('retention_seconds', :value, :now) " +
    "ON CONFLICT (key) DO UPDATE SET value = excluded.value", { value: String(seconds), now: NOW });
}

function installation(t) {
  t.sql("INSERT INTO plugin_installation.interface (id, identifier, device, description, link_type, link_kind, " +
    "first_seen_at, last_seen_at) VALUES (1, 'opt1', 'vlan0.10', 'Inside', 'ethernet', 'vlan', :then, :now)",
    { then: NOW - 10 * DAY, now: NOW });
}

function flow(t, id, at, bytes) {
  t.sql("INSERT INTO plugin_pf.flow (id, log_digest, observed_at, ingested_at, interface_device, interface_lookup_state, " +
    "src_interface_id, src_address, dst_address, dst_port, protocol, ip_version, action, direction, packet_bytes, " +
    "rule_lookup_state, traffic_scope) VALUES (:id, :digest, :at, :at, 'vlan0.10', 'resolved', 1, '192.0.2.10', " +
    "'203.0.113.7', 443, 'tcp', 4, 'pass', 'in', :bytes, 'pending', 'north_south')",
    { id: id, digest: "d" + id, at: at, bytes: bytes });
}

function slot(t, period, start, end) {
  t.sql("INSERT INTO plugin_rollups.volume_aggregate_" + period + " (period_start_at, period_end_at, src_interface_id, " +
    "dst_interface_id, peer_address, traffic_scope, traffic_direction, exit_leg_device, bytes, allowed_bytes, blocked_bytes, " +
    "unknown_bytes, allowed_connections, blocked_connections, unknown_connections, computed_at) VALUES " +
    "(:start, :end, 1, NULL, '203.0.113.7', 'north_south', 'outbound', NULL, 10, 10, 0, 0, 1, 0, 0, :now)",
    { start: start, end: end, now: NOW });
}

test("the purged part keeps the figures of the flows the purge removes, per hour, before they go", function (t) {
  t.clock(NOW);
  installation(t);
  retention(t, DAY);
  var old = NOW - 3 * DAY, oldHour = old - old % 3600;
  flow(t, 1, old, 500);
  flow(t, 2, old + 60, 700);
  flow(t, 3, NOW - 600, 900);
  t.purge();
  var purged = t.query("SELECT hour_start_at, src_interface_id, traffic_direction, peer_address, action, bytes, connections, " +
    "purged_at FROM plugin_rollups.purged_flow_hour");
  t.equal(purged.length, 1, "one hour's purged part");
  t.equal([purged[0].hour_start_at, purged[0].src_interface_id, purged[0].traffic_direction, purged[0].peer_address,
    purged[0].action, purged[0].bytes, purged[0].connections, purged[0].purged_at],
    [oldHour, 1, "outbound", "203.0.113.7", "pass", 1200, 2, NOW], "the two flows summed on their key");
  t.equal(t.query("SELECT id FROM plugin_pf.flow ORDER BY id").map(function (row) { return row.id; }), [3],
    "the pf plugin removed the two old flows and kept the recent one");
  t.equal(t.query("SELECT purged_before FROM retention_purge")[0].purged_before, NOW - DAY, "the core recorded the horizon");
});

test("a slot goes once its period, its week and its month have ended before the horizon", function (t) {
  t.clock(NOW);
  installation(t);
  retention(t, DAY);
  var horizon = NOW - DAY;
  var longAgo = horizon - 40 * DAY;
  slot(t, "1h", longAgo - longAgo % 3600, longAgo - longAgo % 3600 + 3600);
  var twoDays = horizon - 2 * DAY;
  slot(t, "1h", twoDays - twoDays % 3600, twoDays - twoDays % 3600 + 3600);
  slot(t, "7d", longAgo - 7 * DAY, longAgo);
  slot(t, "7d", horizon - 3 * DAY, horizon + 4 * DAY);
  t.purge();
  var hours = t.query("SELECT period_start_at FROM plugin_rollups.volume_aggregate_1h ORDER BY period_start_at");
  t.equal(hours.length, 1, "the hour forty days old went, the one two days old is kept for its week and month");
  t.equal(hours[0].period_start_at, twoDays - twoDays % 3600, "the hour kept");
  var weeks = t.query("SELECT period_start_at FROM plugin_rollups.volume_aggregate_7d ORDER BY period_start_at");
  t.equal(weeks.length, 1, "the week that ended before the horizon went");
  t.equal(weeks[0].period_start_at, horizon - 3 * DAY, "the week straddling the horizon is kept");
});

test("an unlimited retention purges nothing", function (t) {
  t.clock(NOW);
  installation(t);
  retention(t, 0);
  flow(t, 1, NOW - 400 * DAY, 500);
  slot(t, "30d", NOW - 400 * DAY, NOW - 370 * DAY);
  t.purge();
  t.equal(t.query("SELECT count(*) AS n FROM plugin_pf.flow")[0].n, 1, "the flow is kept");
  t.equal(t.query("SELECT count(*) AS n FROM plugin_rollups.volume_aggregate_30d")[0].n, 1, "the slot is kept");
  t.equal(t.query("SELECT count(*) AS n FROM plugin_rollups.purged_flow_hour")[0].n, 0, "nothing was purged, so no purged part");
});
