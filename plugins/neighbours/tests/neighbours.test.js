// The neighbours plugin's own tests: its probe and its reading of its own recorded answers, through
// the plugin loader and the same adapter the collector runs it through.

test("the neighbour tables implementation declares itself as a neighbour source", function (t) {
  t.assert(t.registered("neighbour"), "the neighbours implementation is not registered in the neighbour kind");
});

test("both tables are read into neighbour records", function (t) {
  t.answer("arp", { fixture: "get_arp.json" });
  t.answer("ndp", { fixture: "get_ndp.json" });
  var read = t.call("neighbour", "neighbours");
  if (read.error !== null || !read.neighbours || !read.result.separable) {
    t.fatal("reading the fixtures gave " + JSON.stringify(read));
  }
  var addresses = read.neighbours.map(function (neighbour) { return neighbour.address; });
  t.assert(addresses.indexOf("198.51.100.20") >= 0, "the IPv4 table's first neighbour is missing: " +
    JSON.stringify(addresses));
  t.assert(addresses.some(function (address) { return address.indexOf(":") >= 0; }),
    "no IPv6 neighbour was read: " + JSON.stringify(addresses));
  var first = read.neighbours[0];
  t.equal(first.mac, "0a:11:22:33:44:aa", "the hardware address, as the table spells it");
  t.equal(first.interface_device, "exdev0", "the device the neighbour was learned on");
  t.equal(first.vendor_hint, "example vendor string", "the vendor string");
  t.equal(read.result.state, "reachable", "the state of a read that succeeded");
});

test("a row with no address is no neighbour", function (t) {
  t.equal(t.helper("neighbour_of", { mac: "0a:11:22:33:44:aa", intf: "exdev0" }), null, "a row with no ip");
});

// PA12 of the plugin-API amendment: a 404, a 403 or an unparseable answer is reported with the
// broker's outcome, and nothing is inferred from other data.
test("an endpoint that is not there is reported with the broker's outcome", function (t) {
  t.answer("arp", { status: 404, body: "" });
  var probe = t.call("neighbour", "probe");
  t.assert(probe.error === null && !probe.result.separable && probe.result.state === "unavailable",
    "a missing table gave " + JSON.stringify(probe));
  t.equal(probe.result.outcome, "not_found", "the outcome of a 404");
  var read = t.call("neighbour", "neighbours");
  t.assert(read.error !== null && read.neighbours === undefined, "a missing table read " + JSON.stringify(read));
});

test("a refused endpoint is reported with the broker's outcome", function (t) {
  t.answer("arp", { status: 403, body: "" });
  var probe = t.call("neighbour", "probe");
  t.assert(!probe.result.separable && probe.result.state === "unavailable", "a refusal gave " + JSON.stringify(probe));
  t.assert(probe.result.outcome !== "healthy" && probe.result.outcome !== "request_not_made",
    "a refusal reads " + probe.result.outcome);
});

test("an unparseable answer is reported and nothing is read", function (t) {
  t.answer("arp", { fixture: "get_arp.json" });
  t.answer("ndp", { body: "not a table" });
  var read = t.call("neighbour", "neighbours");
  t.assert(read.error !== null && read.neighbours === undefined, "an unparseable table read " +
    JSON.stringify(read));
  t.equal(read.result.state, "unavailable", "the state");
});
