var listURL = "https://lists.example.org/suffixes.txt";

test("a downloaded list groups names", function (t) {
  t.web(listURL, { body: "# example suffixes\nexample\nco.example\n", headers: { ETag: "\"v1\"" } });
  var refreshed = t.call("registrable_domain", "refresh");
  t.equal(refreshed.ready, true, "the list is held");
  t.equal(refreshed.rules, 2, "the suffixes");
  var grouped = t.call("registrable_domain", "group", ["www.shop.co.example", "co.example", "api.example"]);
  t.equal(grouped.groups, [
    { name: "www.shop.co.example", registrable_domain: "shop.co.example" },
    { name: "co.example" },
    { name: "api.example", registrable_domain: "api.example" }
  ], "the grouping");
});

test("an unchanged list is not downloaded again", function (t) {
  t.web(listURL, { body: "example\n", headers: { ETag: "\"v1\"" } });
  t.call("registrable_domain", "refresh");
  var again = t.call("registrable_domain", "refresh");
  t.equal(again.result.probe, "list_not_modified", "the second refresh");
  t.equal(t.webRequests().length, 2, "two requests, the second conditional");
});

test("a download with no suffix keeps nothing", function (t) {
  t.web(listURL, { body: "# nothing here\n" });
  var refreshed = t.call("registrable_domain", "refresh");
  t.equal(refreshed.ready, false, "nothing is held");
  t.equal(refreshed.result.outcome, "download_malformed", "the outcome");
});
