// The refresh derivation: an hour slot is computed from the flows inside it, composed into its day,
// week and month, and a second run over unchanged flows rewrites nothing but the current slots.
var NOW = 1790000000; // a Sunday, 09:33:20 UTC -- the harness's instant is set to it below

function flow(t, at, bytes, action) {
  t.sql("INSERT INTO plugin_pf.flow (log_digest, observed_at, ingested_at, interface_device, interface_lookup_state, " +
    "src_address, dst_address, src_port, dst_port, protocol, ip_version, action, direction, packet_bytes, " +
    "rule_lookup_state, src_is_this_firewall, dst_is_this_firewall, traffic_scope) VALUES " +
    "(:digest, :at, :at, 'vlan01', 'pending', '192.0.2.10', '198.51.100.9', 40000, 443, 'tcp', 4, :action, 'out', " +
    ":bytes, 'pending', false, false, 'north_south')", { digest: "d" + at + bytes, at: at, action: action, bytes: bytes });
}

test("an hour is computed from its flows, and the coarser periods compose it", function (t) {
  t.clock(NOW);
  var hour = NOW - NOW % 3600;
  flow(t, hour + 10, 100, "pass");
  flow(t, hour + 20, 50, "block");
  flow(t, hour - 3600 + 5, 7, "pass");
  var run = t.derive("refresh");
  t.equal(run.error, null, "the run succeeds");
  var hours = t.query("SELECT period_start_at, bytes, allowed_bytes, blocked_bytes, allowed_connections, blocked_connections " +
    "FROM plugin_rollups.volume_aggregate_1h ORDER BY period_start_at");
  t.equal(hours.length, 2, "two hour slots");
  t.equal(hours[1].bytes, 150, "the current hour's bytes");
  t.equal(hours[1].allowed_bytes, 100, "its allowed bytes");
  t.equal(hours[1].blocked_bytes, 50, "its blocked bytes");
  t.equal(hours[1].blocked_connections, 1, "its blocked connection");
  t.equal(hours[0].bytes, 7, "the previous hour's bytes");
  var day = t.query("SELECT bytes FROM plugin_rollups.volume_aggregate_24h");
  t.equal(day.length, 1, "one day slot");
  t.equal(day[0].bytes, 157, "the day composes its hours");
  t.equal(t.query("SELECT bytes FROM plugin_rollups.volume_aggregate_7d")[0].bytes, 157, "the week composes its days");
  t.equal(t.query("SELECT bytes FROM plugin_rollups.volume_aggregate_30d")[0].bytes, 157, "the month composes its days");
  t.equal(t.query("SELECT watermark FROM plugin_rollups.refresh_state")[0].watermark, NOW, "the watermark is the run's instant");
  var rule = t.query("SELECT count(*) AS n FROM plugin_rollups.rule_volume_aggregate_1h")[0].n;
  t.equal(rule, 2, "the rule family holds a row per hour, the rule unresolved");
  t.equal(t.query("SELECT count(*) AS n FROM plugin_rollups.pair_volume_observation")[0].n, 1, "the day's pair volume");
});

test("a second run rewrites only the current slots, and a slot holding nothing stays absent", function (t) {
  t.clock(NOW);
  var hour = NOW - NOW % 3600;
  flow(t, hour - 7200 + 1, 11, "pass");
  t.derive("refresh");
  var before = t.query("SELECT period_start_at, computed_at FROM plugin_rollups.volume_aggregate_1h ORDER BY 1");
  t.equal(before.length, 1, "the hour with the flow alone");
  t.clock(NOW + 60);
  var run = t.derive("refresh");
  t.equal(run.error, null, "the second run succeeds");
  var after = t.query("SELECT period_start_at, computed_at FROM plugin_rollups.volume_aggregate_1h ORDER BY 1");
  t.equal(after.length, 1, "still one hour row");
  t.equal(after[0].computed_at, before[0].computed_at, "an unchanged closed hour is not rewritten");
  t.equal(t.query("SELECT count(*) AS n FROM plugin_rollups.refresh_slot WHERE period = '1h'")[0].n, 1,
    "the current hour alone was a slot of the second run");
});
