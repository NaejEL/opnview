// The maxmind plugin's own tests: what it asks MaxMind, when it asks nothing, what it keeps, and
// what its lookups answer. MaxMind's two hosts are arranged answers: the download host, which
// redirects, and the R2 host it redirects to, which serves the archive. Every database is written
// by the test API (mmdb_archive) from the records below; nothing is read from MaxMind. Which
// addresses are placed, and the rows written, are the plugin's two derivations, tested below.

var downloadHost = "https://download.maxmind.com";
var r2Host = "https://mm-prod-geoip-databases.a2649acb697e2c09b632799562c076f2.r2.cloudflarestorage.com";
var editions = ["GeoLite2-City", "GeoLite2-ASN"];

function permalink(edition) {
  return downloadHost + "/geoip/databases/" + edition + "/download?suffix=tar.gz";
}

function stored(edition) {
  return r2Host + "/geoip-databases/" + edition + ".tar.gz";
}

// The network the test databases place, a documentation range.
var placed = "203.0.113.0/24";

function records(edition) {
  if (edition === "GeoLite2-City") {
    return [{ network: placed, data: {
      country: { iso_code: "FR", names: { en: "France" } },
      location: { latitude: 48.85, longitude: 2.35 }
    } }];
  }
  return [{ network: placed, data: { autonomous_system_number: 64500,
    autonomous_system_organization: "Example Operator" } }];
}

// httpDate is an instant as an HTTP date.
function httpDate(seconds) {
  return new Date(seconds * 1000).toUTCString();
}

var firstBuild = Date.UTC(2026, 8, 29) / 1000;
var secondBuild = Date.UTC(2026, 9, 2) / 1000;

// publish makes a build of both editions available: the download host redirects to the R2 host,
// which serves the archive, uploaded an hour after it was built. `damage` replaces one edition's
// answer.
function publish(t, build, damage) {
  for (var i = 0; i < editions.length; i++) {
    var edition = editions[i];
    t.web(permalink(edition), { status: 302, headers: { Location: stored(edition) + "?X-Amz-Signature=example" } });
    var reply = { headers: { "Last-Modified": httpDate(build + 3600) },
      mmdb_archive: { edition: edition, build_epoch: build, records: records(edition) } };
    if (damage && damage[edition]) {
      reply = damage[edition];
      reply.headers = { "Last-Modified": httpDate(build + 3600) };
    }
    t.web(stored(edition), reply);
  }
}

function credentials(t) {
  t.setting("account_id", 100001);
  t.setting("licence_key", "example-licence");
}

// downloads counts the archives fetched from the R2 host.
function downloads(t) {
  var count = 0;
  var fetched = t.webRequests();
  for (var i = 0; i < fetched.length; i++) {
    if (fetched[i].indexOf("GET " + r2Host) === 0) {
      count++;
    }
  }
  return count;
}

test("nothing is requested without a licence key", function (t) {
  t.setting("account_id", 100001);
  var refreshed = t.call("ip_geolocation", "refresh");
  t.equal(refreshed.result.probe, "no_licence_key", "the probe");
  t.equal(refreshed.result.outcome, "no_licence_key", "the outcome");
  t.equal(refreshed.ready, false, "the dataset");
  t.equal(t.webRequests().length, 0, "requests to MaxMind");
});

test("nothing is requested without an account ID", function (t) {
  t.setting("licence_key", "example-licence");
  var refreshed = t.call("ip_geolocation", "refresh");
  t.equal(refreshed.result.probe, "no_account_id", "the probe");
  t.equal(refreshed.result.outcome, "no_account_id", "the outcome");
  t.equal(t.webRequests().length, 0, "requests to MaxMind");
});

test("a refusal is its own state and is not followed by a download", function (t) {
  credentials(t);
  t.web(permalink("GeoLite2-City"), { status: 401 });
  var refreshed = t.call("ip_geolocation", "refresh");
  t.equal(refreshed.result.probe, "download_refused", "the probe of a 401");
  t.equal(refreshed.result.outcome, "download_refused", "the outcome of a 401");
  t.assert(refreshed.error !== null, "a refusal reported no error");
  var fetched = t.webRequests();
  t.equal(fetched, ["HEAD " + permalink("GeoLite2-City") + " (authorized)"], "the requests");
});

test("a 403 is a refusal too", function (t) {
  credentials(t);
  t.web(permalink("GeoLite2-City"), { status: 403 });
  var refreshed = t.call("ip_geolocation", "refresh");
  t.equal(refreshed.result.probe, "download_refused", "the probe of a 403");
});

test("a spent limit is its own state", function (t) {
  credentials(t);
  t.web(permalink("GeoLite2-City"), { status: 429 });
  var refreshed = t.call("ip_geolocation", "refresh");
  t.equal(refreshed.result.probe, "download_limit_reached", "the probe of a 429");
  t.equal(refreshed.result.outcome, "download_limit_reached", "the outcome of a 429");
  t.equal(downloads(t), 0, "downloads after a spent limit");
});

test("a server error is a failed download", function (t) {
  credentials(t);
  t.web(permalink("GeoLite2-City"), { status: 500 });
  var refreshed = t.call("ip_geolocation", "refresh");
  t.equal(refreshed.result.probe, "download_failed", "the probe of a 500");
  t.equal(refreshed.ready, false, "the dataset");
});

test("the licence key is a secret the listing never shows", function (t) {
  t.setting("licence_key", "example-licence");
  var listed = JSON.stringify(t.settingValues());
  t.assert(listed.indexOf("example-licence") < 0, "the listing shows the licence key: " + listed);
});

test("with no dataset, nothing is held", function (t) {
  var status = t.call("ip_geolocation", "status");
  t.equal(status.ready, false, "the dataset");
});

test("a download authenticates at MaxMind only and follows the redirect", function (t) {
  credentials(t);
  publish(t, firstBuild);
  var refreshed = t.call("ip_geolocation", "refresh");
  if (refreshed.error !== null || !refreshed.ready) {
    t.fatal("the refresh gave " + JSON.stringify(refreshed));
  }
  t.equal(refreshed.result.probe, "dataset_current", "the probe");
  t.equal(refreshed.result.outcome, "healthy", "the outcome");
  t.equal(refreshed.result.state, "reachable", "the state");
  t.equal(refreshed.build, firstBuild, "the build, the City database's");
  var fetched = t.webRequests();
  var toR2 = 0;
  for (var i = 0; i < fetched.length; i++) {
    if (fetched[i].indexOf(r2Host) >= 0) {
      toR2++;
      t.assert(fetched[i].indexOf("(authorized)") < 0, "the credentials reached the R2 host: " + fetched[i]);
    } else {
      t.assert(fetched[i].indexOf("(authorized)") > 0, "a request to MaxMind went without credentials: " + fetched[i]);
    }
  }
  t.equal(downloads(t), 2, "archives downloaded, one per edition");
  t.assert(toR2 >= 2, "the redirect was not followed");
});

test("a redirect to any other host is refused before anything is sent", function (t) {
  credentials(t);
  t.web(permalink("GeoLite2-City"), { status: 302, headers: { Location: "https://elsewhere.example/GeoLite2-City.tar.gz" } });
  t.web("https://elsewhere.example/GeoLite2-City.tar.gz", { body: "never asked" });
  var refreshed = t.call("ip_geolocation", "refresh");
  t.equal(refreshed.result.probe, "download_failed", "the probe of a refused host");
  t.assert(refreshed.error !== null, "a refused host reported no error");
  var fetched = t.webRequests();
  for (var i = 0; i < fetched.length; i++) {
    t.assert(fetched[i].indexOf("elsewhere.example") < 0, "a request reached an undeclared host: " + fetched[i]);
  }
});

test("an unchanged build is not downloaded again, a newer one is", function (t) {
  credentials(t);
  publish(t, firstBuild);
  t.call("ip_geolocation", "refresh");
  t.equal(downloads(t), 2, "the first downloads");
  var again = t.call("ip_geolocation", "refresh");
  t.equal(again.result.probe, "dataset_current", "the probe of an unchanged build");
  t.equal(downloads(t), 2, "downloads after a check of an unchanged build");

  publish(t, secondBuild);
  var newer = t.call("ip_geolocation", "refresh");
  t.equal(downloads(t), 4, "downloads after a newer build");
  t.equal(newer.build, secondBuild, "the build in use after a newer one");
  t.assert(t.dataFiles().indexOf("GeoLite2-City.last-modified") >= 0,
    "the download date was not kept beside the database: " + JSON.stringify(t.dataFiles()));
});

test("a broken download leaves the database in use", function (t) {
  credentials(t);
  publish(t, firstBuild);
  t.call("ip_geolocation", "refresh");
  var broken = [
    ["not an archive", { body: "not a gzip stream" }],
    ["another edition under the City name", { mmdb_archive: { edition: "GeoLite2-City",
      database_type: "GeoLite2-ASN", build_epoch: secondBuild, records: records("GeoLite2-ASN") } }],
    ["an archive with no City database", { mmdb_archive: { edition: "GeoLite2-ASN", build_epoch: secondBuild,
      records: records("GeoLite2-City") } }]
  ];
  for (var i = 0; i < broken.length; i++) {
    publish(t, secondBuild, { "GeoLite2-City": broken[i][1] });
    var refreshed = t.call("ip_geolocation", "refresh");
    t.assert(refreshed.error !== null, broken[i][0] + " was installed without an error");
    t.equal(refreshed.result.probe, "download_failed", broken[i][0] + ": the probe");
    t.equal(refreshed.result.state, "reachable", broken[i][0] + ": the state");
    t.equal(refreshed.build, firstBuild, broken[i][0] + ": the build in use");
    t.sql("DELETE FROM plugin_maxmind.geo_asn");
    flow(t, "f" + i, "10.1.0.42", "203.0.113.9");
    t.derive("mark");
    t.derive("locate");
    t.equal(placedRows(t)[0].country_code, "FR", broken[i][0] + ": the dataset no longer answers");
  }
  var files = t.dataFiles();
  for (var j = 0; j < files.length; j++) {
    t.assert(files[j].charAt(0) !== "." && files[j].indexOf(".tar.gz") < 0,
      "a failed download left a file behind: " + files[j]);
  }
});

test("a dataset on disk is in use at start, and a damaged one is downloaded again", function (t) {
  credentials(t);
  publish(t, firstBuild);
  t.call("ip_geolocation", "refresh");
  var status = t.call("ip_geolocation", "status");
  t.equal(status.ready, true, "the databases on disk at start");
  t.equal(status.build, firstBuild, "the build at start");

  t.dataFile("GeoLite2-ASN.mmdb", "not a database");
  var damaged = t.call("ip_geolocation", "status");
  t.equal(damaged.ready, false, "a damaged database read as held");
  var before = downloads(t);
  var refreshed = t.call("ip_geolocation", "refresh");
  t.equal(refreshed.ready, true, "the damaged database was not replaced");
  t.equal(downloads(t), before + 1, "downloads of the damaged edition alone");
});

// The derivations: mark lists the public addresses the flows name, locate places them.

function flow(t, digest, src, dst) {
  t.sql("INSERT INTO plugin_pf.flow (log_digest, observed_at, ingested_at, interface_device, interface_lookup_state, " +
    "src_address, dst_address, dst_port, protocol, ip_version, action, direction, packet_bytes, rule_lookup_state, " +
    "traffic_scope) VALUES (:digest, :at, :at, 'igb1', 'resolved', :src, :dst, 443, 'tcp', 4, 'pass', :direction, 60, " +
    "'not_found', 'north_south')", { digest: digest, at: t.now(), src: src, dst: dst, direction: "in" });
}

function placedRows(t) {
  return t.query("SELECT address, lookup_state, country_code, asn, operator, dataset_build_at FROM plugin_maxmind.geo_asn " +
    "ORDER BY address");
}

test("mark lists the public addresses the flows name, and never a private, a loopback or a link-local one", function (t) {
  t.clock(1790000400);
  flow(t, "f1", "10.1.0.42", "203.0.113.9");
  flow(t, "f2", "192.168.1.5", "198.51.100.7");
  flow(t, "f3", "fe80::1", "2001:db8::1");
  flow(t, "f4", "127.0.0.1", "169.254.1.1");
  var run = t.derive("mark");
  t.equal(run.error, null, "the run");
  t.equal(placedRows(t).map(function (row) { return [row.address, row.lookup_state]; }),
    [["198.51.100.7", "pending"], ["2001:db8::1", "pending"], ["203.0.113.9", "pending"]], "the pending rows");
});

test("locate places the pending rows from the databases, as resolved or as a miss under the build, and nothing without a dataset",
  function (t) {
    t.clock(1790000400);
    flow(t, "f1", "10.1.0.42", "203.0.113.9");
    flow(t, "f2", "10.1.0.42", "198.51.100.7");
    t.derive("mark");
    t.derive("locate");
    t.equal(placedRows(t).map(function (row) { return row.lookup_state; }), ["pending", "pending"], "nothing placed without a dataset");
    credentials(t);
    publish(t, firstBuild);
    t.call("ip_geolocation", "refresh");
    var run = t.derive("locate");
    t.equal(run.error, null, "the run");
    t.equal(placedRows(t), [
      { address: "198.51.100.7", lookup_state: "miss", country_code: null, asn: null, operator: null, dataset_build_at: firstBuild },
      { address: "203.0.113.9", lookup_state: "resolved", country_code: "FR", asn: 64500, operator: "Example Operator",
        dataset_build_at: firstBuild }
    ], "the rows placed");
    // A newer build places every row again.
    publish(t, secondBuild);
    t.call("ip_geolocation", "refresh");
    t.derive("locate");
    t.equal(placedRows(t).map(function (row) { return row.dataset_build_at; }), [secondBuild, secondBuild], "placed again under the new build");
  });
