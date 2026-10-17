// A test plugin: "PING <sender> <value>" datagrams, decoded here, and two derivations.

exports.push = {
  // One datagram: its text, "PING <sender> <value>", is one row of pings.
  ping: function (message) {
    var parts = message.text.split(" ");
    if (parts.length !== 3 || parts[0] !== "PING") {
      return [];
    }
    return [{ sender: parts[1], value: parseInt(parts[2], 10) }];
  }
};

// A run handed a pass of a provider named "broken" throws: it must write nothing.
function refuseBroken(run) {
  run.changes.forEach(function (change) {
    if (change.provider === "broken") {
      throw new Error("a pass of the broken provider");
    }
  });
}

exports.derivations = {
  // The summary of every ping stored: a function of what is stored, so a run over the same inputs writes
  // nothing, and the order the pings arrived in changes nothing.
  summarise: function (host, run) {
    refuseBroken(run);
    var rows = host.query("SELECT sender, count(*) AS pings, sum(value) AS total " +
      "FROM plugin_example_derived.pings GROUP BY sender ORDER BY sender", {});
    return {
      rows: {
        summary: rows.map(function (row) {
          return { sender: row.sender, pings: row.pings, total: row.total };
        })
      }
    };
  },
  // How many changes of each trigger it was handed: counters, added to.
  tally: function (host, run) {
    refuseBroken(run);
    var counts = {};
    run.changes.forEach(function (change) {
      counts[change.trigger] = (counts[change.trigger] || 0) + 1;
    });
    return {
      rows: {
        tallies: Object.keys(counts).map(function (trigger) {
          return { trigger: trigger, runs: counts[trigger] };
        })
      }
    };
  }
};
