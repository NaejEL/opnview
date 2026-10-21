// The rollups plugin's own tests: its tables hold what the refresh writes, keyed as the refresh keys
// them, and the purged part's client columns are the clients plugin's to re-point.
var NOW = 1790000400;

test("a volume rollup row is keyed by its slot, its ends, its peer, its direction and its exit leg", function (t) {
  t.sql("INSERT INTO plugin_rollups.volume_aggregate_1h (period_start_at, period_end_at, src_interface_id, dst_interface_id, " +
    "peer_address, traffic_scope, traffic_direction, exit_leg_device, bytes, allowed_bytes, blocked_bytes, unknown_bytes, " +
    "allowed_connections, blocked_connections, unknown_connections, computed_at) VALUES " +
    "(:start, :end, 1, NULL, '198.51.100.9', 'north_south', 'outbound', NULL, 100, 100, 0, 0, 1, 0, 0, :now)",
    { start: NOW - NOW % 3600, end: NOW - NOW % 3600 + 3600, now: NOW });
  var rows = t.query("SELECT period_start_at, bytes, measure_parts FROM plugin_rollups.volume_aggregate_1h");
  t.equal(rows.length, 1, "one slot");
  t.equal(rows[0].bytes, 100, "its bytes");
  t.equal(JSON.stringify(rows[0].measure_parts), "{}", "no declared measure's parts, as a document");
});

test("every family has its four periods, and the purged part and the pair volumes are there", function (t) {
  ["volume", "owner_volume", "client_volume", "peer_volume", "domain_volume", "rule_volume"].forEach(function (family) {
    ["1h", "24h", "7d", "30d"].forEach(function (period) {
      var rows = t.query("SELECT count(*) AS n FROM plugin_rollups." + family + "_aggregate_" + period);
      t.equal(rows[0].n, 0, family + " " + period + " is empty");
    });
  });
  t.equal(t.query("SELECT count(*) AS n FROM plugin_rollups.purged_flow_hour")[0].n, 0, "the purged part");
  t.equal(t.query("SELECT count(*) AS n FROM plugin_rollups.pair_volume_observation")[0].n, 0, "the pair volumes");
});
