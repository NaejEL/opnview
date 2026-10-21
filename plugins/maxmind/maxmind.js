// MaxMind GeoLite2: one implementation of the ip_geolocation kind, and the owner of the table the
// placed addresses go into (ADR 0019, ADR 0020 wave 4). Its derivation mark, in SQL, lists the public
// addresses the flows name that no row holds; its derivation locate places them from the databases.
//
// The City and ASN databases are downloaded on the operator's own account (the account ID and the
// licence key it enters in the settings), kept in this plugin's data directory, refreshed when
// MaxMind publishes a new build, and read locally to place addresses: a lookup contacts nothing.
//
// What MaxMind requires, read on 3 October 2026 at https://dev.maxmind.com/geoip/updating-databases/:
// a download is asked of download.maxmind.com with HTTP Basic authentication -- the account ID and the
// licence key -- at the permalink below, and is redirected to an R2 presigned URL on a second host; a
// HEAD is not counted against the daily download limit. The fetch facility sends the credentials to
// the first host only, never to the host a redirect leads to.
//
// A FILE IS REPLACED ONLY BY ONE THAT OPENED. A new build is extracted beside the one in use, opened,
// verified and checked to be the edition it claims to be; only then is it moved over the old file.
// The old build is gone from the directory the moment it is replaced, which is how the GeoLite EULA's
// rule -- destroy an old build within 30 days of a new release -- is met by construction.

var editions = ["GeoLite2-City", "GeoLite2-ASN"];
var downloadBase = "https://download.maxmind.com";
// maxDatabaseBytes bounds what is downloaded and extracted. GeoLite2 City is the larger edition, at
// tens of megabytes; this leaves an order of magnitude of room and stops a corrupt archive from
// filling the disk.
var maxDatabaseBytes = 1073741824;

// The probes the availability row records: what the last refresh found. The row describes the
// DATASET: it is reachable exactly when both databases are on disk and open, whatever the last
// refresh found, because a failed refresh leaves the databases in use where they were.
var probeCurrent = "dataset_current";
var probeNoLicenceKey = "no_licence_key";
var probeLicenceKeyUndecryptable = "licence_key_undecryptable";
var probeNoAccountID = "no_account_id";
var probeRefused = "download_refused";
var probeLimited = "download_limit_reached";
var probeFailed = "download_failed";

// permalink is the download address of one edition's binary database, a gzip tar archive.
function permalink(edition) {
  return downloadBase + "/geoip/databases/" + edition + "/download?suffix=tar.gz";
}

// database opens one edition held on disk and checks its metadata names the edition, or null.
function database(host, edition, verify) {
  var name = edition + ".mmdb";
  if (!host.files.exists(name)) {
    return null;
  }
  var opened = host.files.openMMDB(name, verify === true);
  if (opened.error || opened.database_type !== edition) {
    return null;
  }
  return opened;
}

// dataset is whether both editions are open, and the build lookups are recorded against: the City
// build, which answers the location a row is resolved by.
function dataset(host) {
  var city = database(host, "GeoLite2-City");
  var asn = database(host, "GeoLite2-ASN");
  if (city === null || asn === null) {
    return { ready: false, build: null };
  }
  return { ready: true, build: city.build_epoch };
}

// outcomeOf names a refresh's probe in the closed outcome set; a current dataset that is not on disk
// is not healthy, whatever the last request said.
function outcomeOf(probe, ready) {
  switch (probe) {
    case probeCurrent: return ready ? "healthy" : "download_failed";
    case probeNoLicenceKey: return "no_licence_key";
    case probeLicenceKeyUndecryptable: return "licence_key_undecryptable";
    case probeNoAccountID: return "no_account_id";
    case probeRefused: return "download_refused";
    case probeLimited: return "download_limit_reached";
  }
  return "download_failed";
}

// answer is a status or refresh answer: the dataset's state, recorded under the probe.
function answer(host, probe, error) {
  var held = dataset(host);
  var result = { probe: probe, state: held.ready ? "reachable" : "unavailable", outcome: outcomeOf(probe, held.ready) };
  if (held.ready) {
    // The build, as an RFC 3339 instant in UTC.
    result.detail = new Date(held.build * 1000).toISOString().replace(".000Z", "Z");
  }
  var reply = { result: result, ready: held.ready };
  if (held.ready) {
    reply.build = held.build;
  }
  if (error) {
    reply.error = error;
  }
  return reply;
}

// probeFor names a fetch failure.
function probeFor(outcome) {
  switch (outcome) {
    case "download_refused": return probeRefused;
    case "download_limit_reached": return probeLimited;
    default: return probeFailed;
  }
}

// refreshEdition downloads one edition when MaxMind has a build newer than the one downloaded last.
// THE COMPARISON IS AGAINST THE Last-Modified OF THE LAST DOWNLOAD, kept beside the database, and not
// against the build date inside it: a file is uploaded after it is built, so the two never agree.
function refreshEdition(host, authorization, edition) {
  var headers = { Authorization: authorization };
  var head = host.fetch({ url: permalink(edition), method: "HEAD", headers: headers });
  if (head.outcome !== "answered") {
    return { probe: probeFor(head.outcome), error: "maxmind: requesting " + edition + ": " + head.outcome +
      (head.detail ? " (" + head.detail + ")" : "") };
  }
  var modified = Date.parse(head.last_modified);
  if (isNaN(modified)) {
    return { probe: probeFailed, error: "maxmind: the " + edition + " answer carries no readable Last-Modified" };
  }
  modified = Math.floor(modified / 1000);
  var recordName = edition + ".last-modified";
  if (database(host, edition) !== null) {
    var known = parseInt(host.files.readText(recordName) || "", 10);
    if (!isNaN(known) && modified <= known) {
      return null;
    }
  }
  var archive = edition + ".tar.gz";
  var fetched = host.fetch({
    url: permalink(edition), headers: headers, file: archive, max_bytes: maxDatabaseBytes,
    check: function (temporary) {
      var extracted = host.files.extractTarGz(temporary, edition + ".mmdb", maxDatabaseBytes);
      if (extracted.error) {
        return { ok: false, detail: extracted.error };
      }
      var candidate = host.files.openMMDB(extracted.file, true);
      if (candidate.error || candidate.database_type !== edition) {
        host.files.remove(extracted.file);
        return { ok: false, detail: candidate.error || ("the archive holds " + candidate.database_type + ", not " + edition) };
      }
      host.files.replace(extracted.file, edition + ".mmdb");
      return { ok: true };
    }
  });
  // The archive itself is not kept: the database extracted from it is what is in use.
  host.files.remove(archive);
  if (fetched.outcome !== "fetched") {
    return { probe: probeFor(fetched.outcome), error: "maxmind: downloading " + edition + ": " + fetched.outcome +
      (fetched.detail ? " (" + fetched.detail + ")" : "") };
  }
  host.files.writeText(recordName, modified + "\n");
  return null;
}

exports.ip_geolocation = {
  // status reports the databases on disk and requests nothing.
  status: function (host) {
    return answer(host, probeCurrent, null);
  },

  // refresh checks each edition and downloads the ones that changed. WITHOUT A LICENCE KEY OR AN
  // ACCOUNT ID, NOTHING IS REQUESTED, and the state says which is missing. A refusal or a spent limit
  // is the same answer for the second edition, so the first failure ends the refresh.
  refresh: function (host) {
    var settings = host.settings || {};
    var states = host.secret_states || {};
    if (states.licence_key === "undecryptable") {
      return answer(host, probeLicenceKeyUndecryptable, null);
    }
    if (states.licence_key !== "ready") {
      return answer(host, probeNoLicenceKey, null);
    }
    if (settings.account_id === null || settings.account_id === undefined || settings.account_id === "") {
      return answer(host, probeNoAccountID, null);
    }
    var authorization = "Basic " + opnview.base64(String(settings.account_id) + ":" + settings.licence_key);
    for (var i = 0; i < editions.length; i++) {
      var failure = refreshEdition(host, authorization, editions[i]);
      if (failure !== null) {
        return answer(host, failure.probe, failure.error);
      }
    }
    return answer(host, probeCurrent, null);
  }
};

// lookupAddresses asks both databases about each address. It reads the files and contacts nothing.
function lookupAddresses(host, addresses) {
  var city = database(host, "GeoLite2-City");
  var asn = database(host, "GeoLite2-ASN");
  if (city === null || asn === null) {
    return { build: 0, answers: [], error: "maxmind: the dataset is not ready" };
  }
  var answers = [];
  for (var i = 0; i < addresses.length; i++) {
    var address = addresses[i];
    var placed = { address: address };
    var place = city.lookup(address) || {};
    // The registered country is read for an address whose own country the database leaves empty.
    var country = place.country || {};
    if (!country.iso_code) {
      country = place.registered_country || {};
    }
    if (country.iso_code) {
      placed.country_code = country.iso_code;
      if (country.names && country.names.en) {
        placed.country_name = country.names.en;
      }
    }
    var location = place.location || {};
    if (typeof location.latitude === "number") {
      placed.latitude = location.latitude;
    }
    if (typeof location.longitude === "number") {
      placed.longitude = location.longitude;
    }
    var system = asn.lookup(address) || {};
    if (system.autonomous_system_number) {
      placed.asn = system.autonomous_system_number;
    }
    if (system.autonomous_system_organization) {
      placed.operator = system.autonomous_system_organization;
    }
    answers.push(placed);
  }
  return { build: city.build_epoch, answers: answers };
}

// maxLookupsPerRun bounds one placing run, so a first run over a large history does not hold the
// derivation's loop for long; what is left waits for the next run.
var maxLookupsPerRun = 20000;

exports.derivations = {
  // locate places the pending rows and the rows an older build placed, from the databases on disk,
  // and writes each as resolved or as a miss under the build that answered. With no dataset nothing is
  // placed and the rows wait.
  locate: function (host, run) {
    var held = dataset(host);
    if (!held.ready) {
      return { rows: { geo_asn: [] } };
    }
    var due = host.query("SELECT address, provider_id FROM plugin_maxmind.geo_asn " +
      "WHERE lookup_state = 'pending' OR dataset_build_at IS NULL OR dataset_build_at < :build " +
      "ORDER BY address LIMIT :bound", { build: held.build, bound: maxLookupsPerRun });
    if (due.length === 0) {
      return { rows: { geo_asn: [] } };
    }
    var looked = lookupAddresses(host, due.map(function (row) { return row.address; }));
    if (looked.error) {
      return { rows: { geo_asn: [] } };
    }
    var providerOf = {};
    due.forEach(function (row) { providerOf[row.address] = row.provider_id; });
    var rows = looked.answers.map(function (placed) {
      return {
        address: placed.address,
        provider_id: providerOf[placed.address],
        lookup_state: placed.country_code ? "resolved" : "miss",
        country_code: placed.country_code === undefined ? null : placed.country_code,
        country_name: placed.country_name === undefined ? null : placed.country_name,
        latitude: placed.latitude === undefined ? null : placed.latitude,
        longitude: placed.longitude === undefined ? null : placed.longitude,
        asn: placed.asn === undefined ? null : placed.asn,
        operator: placed.operator === undefined ? null : placed.operator,
        dataset_build_at: looked.build,
        looked_up_at: run.now
      };
    });
    return { rows: { geo_asn: rows } };
  }
};

