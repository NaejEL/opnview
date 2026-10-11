// The pf plugin's push entry: the filter log received over a remote syslog target, delivered
// through opnview's real syslog decoder and router. The parity test below is what allows the entry
// to declare that it replaces polling (PS12 of the push amendment): the same lines, read once
// through the polled endpoint and once over syslog, give records equal in every field of the
// firewall_log contract but the identity.

// recordKey identifies one record across the two paths without its identity.
function recordKey(record) {
  return [record.observed_at, record.src_address, record.dst_address, record.src_port, record.dst_port].join("|");
}

// The fields of the contract compared; digest is the identity and is the one field that differs.
var compared = ["observed_at", "device", "src_address", "dst_address", "src_port", "dst_port", "protocol",
  "ip_version", "action", "direction", "log_reason", "packet_bytes", "rid", "ip_id", "tcp_seq"];

test("the same lines pulled and pushed give equal records but for the identity", function (t) {
  t.answer("filter_log", { fixture: "filter_log_pulled.json" });
  var pulled = t.call("firewall_log", "records", 100);
  if (pulled.error !== null) {
    t.fatal("reading the pulled fixture: " + pulled.error);
  }
  t.deliverSyslog(t.fixture("filter_log_syslog.json"), { transport: "tcp" });
  var pushed = t.pushed("filter_log");
  t.equal(pushed.length, pulled.records.length, "records pushed against records pulled");
  var byKey = {};
  pulled.records.forEach(function (record) { byKey[recordKey(record)] = record; });
  pushed.forEach(function (record) {
    var twin = byKey[recordKey(record)];
    if (!twin) {
      t.fatal("a pushed record has no pulled twin: " + JSON.stringify(record));
    }
    compared.forEach(function (field) {
      var a = record[field] === undefined ? null : record[field];
      var b = twin[field] === undefined ? null : twin[field];
      t.equal(JSON.stringify(a), JSON.stringify(b), field + " of the record at " + record.observed_at);
    });
  });
});

test("a pushed record's identity is namespaced and never a pulled one", function (t) {
  t.answer("filter_log", { fixture: "filter_log_pulled.json" });
  var pulled = t.call("firewall_log", "records", 100);
  t.deliverSyslog(t.fixture("filter_log_syslog.json"), { transport: "tcp" });
  var digests = {};
  pulled.records.forEach(function (record) { digests[record.digest] = true; });
  t.pushed("filter_log").forEach(function (record) {
    t.assert(record.digest.indexOf("push:") === 0, "the pushed digest " + record.digest + " carries no push prefix");
    t.assert(!digests[record.digest], "a pushed digest equals a pulled one");
  });
});

test("a message of the program that is not a filter-log record is skipped and counted", function (t) {
  t.deliverSyslog(t.fixture("filter_log_syslog.json"), { transport: "tcp" });
  t.equal(t.pushCounters("filter_log").skipped, 1, "skipped messages");
});

test("over UDP the core kind is not fed: only TCP may replace polling", function (t) {
  t.deliverSyslog(t.fixture("filter_log_syslog.json"), { transport: "udp" });
  t.equal(t.pushed("filter_log").length, 0, "records delivered over UDP");
  t.assert(t.pushCounters("filter_log").transport_not_accepted_udp > 0, "the refused transport is not counted");
});

test("a line of a program nobody subscribes to costs no parse and stores nothing", function (t) {
  t.deliverSyslog(["<30>1 2026-09-26T23:51:06+00:00 example-host example-program 1 - - a message"],
    { transport: "tcp" });
  t.equal(t.listenerCounters("syslog", "tcp").no_subscriber, 1, "lines with no subscriber");
  t.equal(t.pushCounters("filter_log").script_calls || 0, 0, "parse calls");
});
