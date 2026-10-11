// The publicsuffix plugin's own tests: its refresh against an arranged answer of publicsuffix.org,
// and its grouping. What the core does with the state -- the availability row, the activation, the
// collection gap of a failed refresh -- is tested in opnview's core.

var listURL = "https://publicsuffix.org/list/public_suffix_list.dat";

test("with no copy, nothing is held and nothing is grouped", function (t) {
  var status = t.call("registrable_domain", "status");
  t.assert(status.ready === false && status.result.state === "unavailable", "with no copy: " + JSON.stringify(status));
  var grouped = t.call("registrable_domain", "group", ["www.example.com"]);
  t.assert(grouped.error !== null, "a grouping with no copy answered " + JSON.stringify(grouped));
});

test("a failed first download keeps nothing and says it failed", function (t) {
  t.web(listURL, { status: 503 });
  var refreshed = t.call("registrable_domain", "refresh");
  t.assert(refreshed.error !== null && refreshed.failed === true && refreshed.ready === false,
    "a failed first download gave " + JSON.stringify(refreshed));
  t.equal(refreshed.result.probe, "download_failed", "the probe");
  t.equal(refreshed.result.outcome, "download_failed", "the outcome");
});

test("a download is kept, used, and not downloaded again while unchanged", function (t) {
  t.web(listURL, { file: "public_suffix_list_example.dat", headers: { ETag: "\"example-etag-1\"",
    "Last-Modified": "Thu, 01 Oct 2026 23:03:02 GMT" } });
  var refreshed = t.call("registrable_domain", "refresh");
  if (refreshed.error !== null || !refreshed.ready) {
    t.fatal("downloading gave " + JSON.stringify(refreshed));
  }
  t.equal(refreshed.result.probe, "list_downloaded", "the probe of a download");
  t.equal(refreshed.result.outcome, "healthy", "the outcome of a download");
  t.equal(refreshed.version, "example-version", "the list's own version");
  t.equal(refreshed.confirmed_at, t.now(), "the instant the copy was confirmed");

  var grouped = t.call("registrable_domain", "group", ["www.example.com", "api.example.com", "com"]);
  t.equal(grouped.groups, [
    { name: "www.example.com", registrable_domain: "example.com" },
    { name: "api.example.com", registrable_domain: "example.com" },
    { name: "com" }
  ], "the grouping");

  var again = t.call("registrable_domain", "refresh");
  t.equal(again.result.probe, "list_not_modified", "the probe of an unchanged list");
  t.equal(again.result.outcome, "healthy", "the outcome of an unchanged list");
  var fetched = t.webRequests();
  t.equal(fetched.length, 2, "requests to publicsuffix.org");
});

test("a malformed download keeps the previous copy", function (t) {
  t.web(listURL, { file: "public_suffix_list_example.dat", headers: { ETag: "\"example-etag-1\"" } });
  t.call("registrable_domain", "refresh");
  t.web(listURL, { body: "<html>not the list</html>", headers: { ETag: "\"example-etag-2\"" } });
  var malformed = t.call("registrable_domain", "refresh");
  t.assert(malformed.error !== null && malformed.failed === true, "a malformed download gave " +
    JSON.stringify(malformed));
  t.equal(malformed.result.probe, "download_malformed", "the probe");
  t.equal(malformed.result.outcome, "download_malformed", "the outcome");
  t.assert(malformed.ready === true && malformed.result.state === "reachable", "the copy in use was lost");
  var grouped = t.call("registrable_domain", "group", ["www.example.com"]);
  t.equal(grouped.groups, [{ name: "www.example.com", registrable_domain: "example.com" }],
    "the grouping after a malformed download");
});

test("a failed download after a good one keeps the copy in use", function (t) {
  t.web(listURL, { file: "public_suffix_list_example.dat", headers: { ETag: "\"example-etag-1\"" } });
  t.call("registrable_domain", "refresh");
  t.web(listURL, { status: 500 });
  var failed = t.call("registrable_domain", "refresh");
  t.assert(failed.error !== null && failed.failed === true, "a failed refresh gave " + JSON.stringify(failed));
  t.equal(failed.result.probe, "download_failed", "the probe");
  t.equal(failed.result.state, "reachable", "the state of the copy in use");
});

test("the copy kept on disk is in use at start", function (t) {
  t.web(listURL, { file: "public_suffix_list_example.dat", headers: { ETag: "\"example-etag-1\"" } });
  t.call("registrable_domain", "refresh");
  var status = t.call("registrable_domain", "status");
  t.assert(status.ready === true && status.result.state === "reachable", "at start: " + JSON.stringify(status));
  t.equal(status.version, "example-version", "the version of the kept copy");
});

test("no host but publicsuffix.org is contacted, redirects included", function (t) {
  t.web(listURL, { status: 302, headers: { Location: "https://elsewhere.example/list" } });
  t.web("https://elsewhere.example/list", { file: "public_suffix_list_example.dat" });
  var refreshed = t.call("registrable_domain", "refresh");
  t.assert(refreshed.error !== null && refreshed.ready === false, "a redirect elsewhere gave " +
    JSON.stringify(refreshed));
  var fetched = t.webRequests();
  for (var i = 0; i < fetched.length; i++) {
    t.assert(fetched[i].indexOf("elsewhere.example") < 0, "a request reached an undeclared host: " + fetched[i]);
  }
  t.equal(fetched, ["GET " + listURL], "the requests");
});
