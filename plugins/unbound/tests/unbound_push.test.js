// The unbound plugin's push entry: Unbound's query and reply lines received over a remote syslog
// target, delivered through opnview's real syslog decoder and router into the plugin's own table.
// A reply line carries no verdict and no blocklist (survey, Push sources, S8), so these rows cannot
// be dns_lookup records: the entry writes a table of its own and replaces nothing.

test("query and reply lines become rows by the entry's regular expression, with no script", function (t) {
  t.deliverSyslog(t.fixture("query_log_syslog.json"), { transport: "tcp" });
  var rows = t.pushed("query_log");
  t.equal(rows.length, 4, "rows");
  var first = rows[0];
  t.equal(first.client, "192.0.2.10", "the client");
  t.equal(first.name, "example.org.", "the name");
  t.equal(first.type, "A", "the type");
  t.equal(first.class, "IN", "the class");
  t.equal(first.rcode, "NOERROR", "the rcode");
  t.equal(first.resolve_seconds, 0.000123, "the time to resolve");
  t.equal(first.cached, 1, "cached");
  t.equal(first.reply_bytes, 56, "the reply size");
  t.equal(first.logged_at, 1790466666, "the header's instant, epoch seconds");
  var query = rows[1];
  t.equal(query.client, "2001:db8::10", "an IPv6 client");
  t.equal(query.rcode, null, "a query line carries no rcode");
  t.equal(rows[2].logged_at, 1790466664, "an RFC 3164 header's instant, read with the measured offset");
  t.equal(t.pushCounters("query_log").pattern_unmatched, 1, "lines the pattern does not match");
  t.equal(t.pushCounters("query_log").script_calls || 0, 0, "script calls");
});

test("the entry accepts UDP: its table carries loss_detectable false over it", function (t) {
  t.deliverSyslog(t.fixture("query_log_syslog.json"), { transport: "udp" });
  t.equal(t.pushed("query_log").length, 4, "rows received over UDP");
});

test("a sender other than the firewall stores nothing", function (t) {
  t.deliverSyslog(t.fixture("query_log_syslog.json"), { transport: "tcp", sender: "203.0.113.99" });
  t.equal(t.pushed("query_log").length, 0, "rows from a refused sender");
});
