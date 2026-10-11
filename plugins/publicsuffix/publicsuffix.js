// The Public Suffix List: one implementation of the registrable_domain kind.
//
// The list is downloaded from its canonical location, conditionally on the copy held, and kept in
// this plugin's data directory. The list's own header says: "Please pull this list from, and only
// from https://publicsuffix.org/list/public_suffix_list.dat", and the project's page
// (https://publicsuffix.org/list/) asks an application to download it "no more than once per day";
// the server answers with an ETag and a Last-Modified, and answers a conditional request whose copy
// is current with 304 and no body. The list is published under the Mozilla Public License 2.0
// (https://mozilla.org/MPL/2.0/); opnview downloads and reads it and never modifies or redistributes it.
//
// The list's format and its formal algorithm (github.com/publicsuffix/list/wiki/Format) are read by
// the plugin host's suffix-list helper: the file is opened once per version and every name is
// matched against it there, so grouping a widget's names contacts nothing.

var listURL = "https://publicsuffix.org/list/public_suffix_list.dat";
var listFile = "public_suffix_list.dat";
var metadataFile = "public_suffix_list.json";
// maxListBytes bounds a download. The list is a few hundred kilobytes; this leaves two orders of
// magnitude of room and stops a broken answer from filling the disk.
var maxListBytes = 33554432;

// The probes the availability row records: what the last refresh found. The row describes the LIST:
// it is reachable exactly when a parsed copy is held, whatever the last refresh found, because a
// failed refresh leaves the copy in use where it was.
var probeDownloaded = "list_downloaded";
var probeNotModified = "list_not_modified";
var probeFailed = "download_failed";
var probeMalformed = "download_malformed";

// held opens the copy on disk, or answers null.
function held(host) {
  if (!host.files.exists(listFile)) {
    return null;
  }
  var list = host.files.openSuffixList(listFile);
  return list.error ? null : list;
}

// confirmedAt is the last instant a refresh confirmed the copy current, kept beside it.
function confirmedAt(host) {
  var text = host.files.readText(metadataFile);
  if (text === null) {
    return 0;
  }
  try {
    var meta = JSON.parse(text);
    return typeof meta.refreshed_at === "number" ? meta.refreshed_at : 0;
  } catch (e) {
    return 0;
  }
}

// answer is a status or refresh answer: the list held, recorded under the probe.
function answer(host, probe, failed, error) {
  var list = held(host);
  var result = { probe: probe, state: list === null ? "unavailable" : "reachable" };
  if (probe === probeMalformed) {
    result.outcome = "download_malformed";
  } else if ((probe === probeDownloaded || probe === probeNotModified) && list !== null) {
    result.outcome = "healthy";
  } else {
    result.outcome = "download_failed";
  }
  var reply = { result: result, ready: list !== null, confirmed_at: confirmedAt(host), failed: failed };
  if (list !== null) {
    var described = list.rules + " rules";
    if (list.version !== "") {
      described = "VERSION " + list.version + ", " + described;
    }
    result.detail = described;
    reply.version = list.version;
    reply.rules = list.rules;
  }
  if (error) {
    reply.error = error;
  }
  return reply;
}

exports.registrable_domain = {
  // status reports the copy on disk and requests nothing.
  status: function (host) {
    return answer(host, probeDownloaded, false, null);
  },

  // refresh asks for the list, conditionally on the copy held, and puts a newer copy in use only
  // once it parses. A failure keeps the copy in use; the core records it as a collection gap from
  // the instant the copy was last confirmed current.
  refresh: function (host) {
    var previous = confirmedAt(host);
    var fetched = host.fetch({
      url: listURL, file: listFile, max_bytes: maxListBytes, conditional: held(host) !== null,
      check: function (temporary) {
        var candidate = host.files.openSuffixList(temporary);
        return candidate.error ? { ok: false, detail: candidate.error } : { ok: true };
      }
    });
    var now = host.now();
    switch (fetched.outcome) {
      case "fetched":
        host.files.writeText(metadataFile, JSON.stringify({ refreshed_at: now }));
        return answer(host, probeDownloaded, false, null);
      case "not_modified":
        host.files.writeText(metadataFile, JSON.stringify({ refreshed_at: now }));
        return answer(host, probeNotModified, false, null);
      case "download_malformed":
        var malformed = answer(host, probeMalformed, true,
          "publicsuffix: the download is not the Public Suffix List (" + fetched.detail + ")");
        malformed.confirmed_at = previous;
        return malformed;
    }
    var failed = answer(host, probeFailed, true, "publicsuffix: the list host answered " +
      (fetched.status_code ? fetched.status_code : fetched.outcome) + (fetched.detail ? " (" + fetched.detail + ")" : ""));
    failed.confirmed_at = previous;
    return failed;
  },

  // group gives each name its registrable domain, or none: a name that is itself a public suffix,
  // or one the algorithm refuses, groups on its own.
  group: function (host, names) {
    var list = held(host);
    if (list === null) {
      return { groups: [], error: "publicsuffix: no copy of the list is held" };
    }
    var groups = [];
    for (var i = 0; i < names.length; i++) {
      var domain = list.registrableDomain(names[i]);
      var grouping = { name: names[i] };
      if (domain !== null) {
        grouping.registrable_domain = domain;
      }
      groups.push(grouping);
    }
    return { groups: groups };
  }
};
