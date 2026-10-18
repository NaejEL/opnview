var listURL = "https://lists.example.org/suffixes.txt";
var listFile = "suffixes.txt";

// suffixes reads the list held, or a candidate download: one suffix per line, # for a comment.
function suffixes(host, name) {
  if (!host.files.exists(name)) {
    return null;
  }
  var kept = [];
  var lines = host.files.lines(name, 100000);
  for (var i = 0; i < lines.length; i++) {
    var line = lines[i].trim();
    if (line !== "" && line.charAt(0) !== "#") {
      kept.push(line.toLowerCase());
    }
  }
  return kept.length > 0 ? kept : null;
}

function answer(host, probe, outcome, failed, error) {
  var held = suffixes(host, listFile);
  var reply = {
    result: { probe: probe, state: held ? "reachable" : "unavailable", outcome: held ? outcome : "download_failed" },
    ready: held !== null,
    failed: failed
  };
  if (held) {
    reply.rules = held.length;
  }
  if (error) {
    reply.error = error;
  }
  return reply;
}

exports.registrable_domain = {
  status: function (host) {
    return answer(host, "list_held", "healthy", false, null);
  },

  refresh: function (host) {
    var fetched = host.fetch({
      url: listURL, file: listFile, max_bytes: 1048576, conditional: true,
      check: function (temporary) {
        return { ok: suffixes(host, temporary) !== null, detail: "no suffix in the download" };
      }
    });
    if (fetched.outcome === "fetched") {
      return answer(host, "list_downloaded", "healthy", false, null);
    }
    if (fetched.outcome === "not_modified") {
      return answer(host, "list_not_modified", "healthy", false, null);
    }
    var failed = answer(host, "download_failed", fetched.outcome === "download_malformed" ?
      "download_malformed" : "download_failed", true, "the list could not be refreshed: " + fetched.outcome);
    failed.result.outcome = fetched.outcome === "download_malformed" ? "download_malformed" : "download_failed";
    return failed;
  },

  // group gives each name the longest suffix it ends with plus one label; a name that is itself a
  // suffix, or ends with none, has no registrable domain.
  group: function (host, names) {
    var held = suffixes(host, listFile);
    if (held === null) {
      return { groups: [], error: "no list is held" };
    }
    var groups = [];
    for (var i = 0; i < names.length; i++) {
      var name = names[i].toLowerCase();
      var best = "";
      for (var j = 0; j < held.length; j++) {
        var suffix = held[j];
        if (name.length > suffix.length && name.slice(-suffix.length - 1) === "." + suffix &&
          suffix.length > best.length) {
          best = suffix;
        }
      }
      var grouping = { name: names[i] };
      if (best !== "" && held.indexOf(name) < 0) {
        var labels = name.slice(0, name.length - best.length - 1).split(".");
        grouping.registrable_domain = labels[labels.length - 1] + "." + best;
      }
      groups.push(grouping);
    }
    return { groups: groups };
  }
};
