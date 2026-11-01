// The retention purge as this plugin takes part in it (ADR 0021, step 6): a lookup past the horizon
// goes, unless an attribution of a surviving flow still names it.
var NOW = 1790000400;
var DAY = 86400;

function lookup(t, id, at) {
  t.sql("INSERT INTO plugin_unbound.dns_resolution (id, lookup_key, client_address, domain, resolver, action, looked_up_at, " +
    "ingested_at) VALUES (:id, :key, '192.0.2.10', 'example.net', 'unbound', 'pass', :at, :at)",
    { id: id, key: "k" + id, at: at });
}

test("a lookup past the horizon goes, unless an attribution of a surviving flow names it", function (t) {
  t.clock(NOW);
  t.sql("INSERT INTO setting (key, value, updated_at) VALUES ('retention_seconds', :value, :now) " +
    "ON CONFLICT (key) DO UPDATE SET value = excluded.value", { value: String(DAY), now: NOW });
  var justBefore = NOW - DAY - 30;
  lookup(t, 1, NOW - 3 * DAY);
  lookup(t, 2, justBefore);
  lookup(t, 3, NOW - 600);
  // A flow kept, attributed to the lookup made just before the horizon: the lookup is kept with it.
  t.sql("INSERT INTO plugin_pf.flow (id, log_digest, observed_at, ingested_at, interface_device, interface_lookup_state, " +
    "src_address, dst_address, dst_port, protocol, ip_version, action, direction, packet_bytes, rule_lookup_state, " +
    "traffic_scope) VALUES (7, 'd7', :at, :at, 'vlan0.10', 'resolved', '192.0.2.10', '203.0.113.7', 443, 'tcp', 4, " +
    "'pass', 'in', 100, 'pending', 'north_south')", { at: NOW - DAY + 30 });
  t.sql("INSERT INTO plugin_unbound.domain_attribution (flow_id, dns_resolution_id, site_name, correlation_delay_seconds, " +
    "attributed_at, method) VALUES (7, 2, 'example.net', 60, :at, 'lookup_timing')", { at: NOW - DAY + 30 });
  t.purge();
  t.equal(t.query("SELECT id FROM plugin_unbound.dns_resolution ORDER BY id").map(function (row) { return row.id; }), [2, 3],
    "the old lookup went; the one an attribution names, and the recent one, are kept");
  t.equal(t.query("SELECT count(*) AS n FROM plugin_unbound.domain_attribution")[0].n, 1, "the attribution of the kept flow stays");
  t.equal(t.query("SELECT count(*) AS n FROM plugin_pf.flow")[0].n, 1, "the flow inside the horizon stays");
});
