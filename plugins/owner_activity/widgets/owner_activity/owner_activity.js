// owner_activity: one card per person -- their volume, their allowed and blocked connections,
// their machines as chips each carrying its own state, and an inline timeline -- and the residual
// Unassigned card (docs/widget-catalogue.md, "Per-person activity"). Ownership is never inferred: a
// machine is under a person because somebody said so. A person's figures are the sum of their
// machines' figures, read from the client family; the cards and the Unassigned card together make
// the owner family's total.

// directionInScope reports whether a client-family direction is in a traffic scope.
function directionInScope(host, scope, direction) {
  var directions = host.volume.directions;
  var scopes = host.volume.scopes;
  if (scope === scopes.east_west) {
    return direction === directions.inter_interface;
  }
  if (scope === scopes.north_south) {
    return direction === directions.outbound || direction === directions.inbound;
  }
  return true;
}

// stableSort sorts a list by a comparison, keeping the order of the members it holds equal, as Go's
// sort.SliceStable does.
function stableSort(list, compare) {
  var decorated = list.map(function (value, index) { return { value: value, index: index }; });
  decorated.sort(function (a, b) {
    var order = compare(a.value, b.value);
    return order !== 0 ? order : a.index - b.index;
  });
  return decorated.map(function (entry) { return entry.value; });
}

// descending compares two numbers, the larger first.
function descending(a, b) {
  if (a === b) {
    return 0;
  }
  return a > b ? -1 : 1;
}

// connections is the connections of summable figures.
function connections(figures) {
  return figures.allowed_connections + figures.blocked_connections + figures.unknown_connections;
}

// STALE is the state of a rate older than twice its cadence (host.rates): its figures are served
// beside it.
var STALE = "stale_sample";

// sumRates is the current rate of several machines together: the sum of the rates of those holding
// a current sample, in the shape every rate has. Each figure is a lower bound, so their sum is one.
// The outbound figure is summed only when every rate summed measured it, as the core sums a
// client's local addresses; the newest reading's instant and the coarsest cadence are kept, and the
// sum is stale when any rate summed is. With no rate to sum it is the named absence, never 0.
function sumRates(host, rates) {
  var none = host.rates.none();
  var present = rates.filter(function (rate) { return rate.state !== none.state; });
  if (present.length === 0) {
    return none;
  }
  var sum = {
    state: "current",
    measure: present[0].measure,
    in_bits_per_second: 0,
    out_bits_per_second: 0,
    out_measured: true,
    sampled_at: null,
    span_seconds: null,
    cadence_seconds: null,
    lower_bound: true
  };
  present.forEach(function (rate) {
    if (rate.state === STALE) {
      sum.state = STALE;
    }
    sum.in_bits_per_second += rate.in_bits_per_second;
    if (rate.out_measured) {
      sum.out_bits_per_second += rate.out_bits_per_second;
    } else {
      sum.out_measured = false;
    }
    ["sampled_at", "span_seconds", "cadence_seconds"].forEach(function (key) {
      if (rate[key] !== null && (sum[key] === null || rate[key] > sum[key])) {
        sum[key] = rate[key];
      }
    });
  });
  if (!sum.out_measured) {
    sum.out_bits_per_second = null;
  }
  return sum;
}

// interfaceIds is the interfaces a list of chips' machines are on, ascending, each once.
function interfaceIds(chips) {
  var seen = {};
  var ids = [];
  chips.forEach(function (chip) {
    var id = chip.client.interface_id;
    if (id !== null && !seen[id]) {
      seen[id] = true;
      ids.push(id);
    }
  });
  return ids.sort(function (a, b) { return a - b; });
}

// sampledCount is how many chips hold a current rate: the machines a summed rate covers.
function sampledCount(host, chips) {
  var none = host.rates.none();
  return chips.filter(function (chip) { return chip.current_rate.state !== none.state; }).length;
}

exports.widget = {
  serve: function (host) {
    var owners = host.installation.owners();
    if (owners.length === 0) {
      host.envelope.override(host.envelope.conditions.no_owner_exists);
    }
    var read = host.clients.volumes();
    var volumes = read.clients;
    var scope = host.params.text("scope");
    var measure = host.params.text("measure");
    var selectedIds = host.refs.ids("owners");
    var selected = null;
    if (selectedIds !== null) {
      selected = {};
      selectedIds.forEach(function (id) { selected[id] = true; });
    }
    var perClient = {};
    volumes.forEach(function (volume) {
      var figures = host.figures.zero();
      Object.keys(volume.by_direction).forEach(function (direction) {
        if (directionInScope(host, scope, direction)) {
          figures = host.figures.add(figures, volume.by_direction[direction]);
        }
      });
      perClient[volume.client_id] = figures;
    });
    var limit = host.params.integer("clients_per_card");
    var rates = host.rates.byClient();
    var noRate = host.rates.none();

    // chipOf is one machine's chip: measured with its volume, or no_data_in_period -- which is not
    // a zero the reader might take for its absence.
    function chipOf(info) {
      var chip = {
        client: info,
        state: "no_data_in_period",
        volume: null,
        current_rate: rates.hasOwnProperty(String(info.id)) ? rates[String(info.id)] : noRate
      };
      var figures = perClient[info.id];
      if (figures !== undefined && (figures.bytes > 0 || connections(figures) > 0)) {
        chip.state = "measured";
        chip.volume = host.figures.volume(figures);
      }
      return chip;
    }

    // build is one card: its chips by volume, the machines past clients_per_card in its remainder.
    function build(key, owner, members) {
      var card = {
        key: key,
        owner: owner,
        value: 0,
        volume: null,
        machines: 0,
        // The person's rate now and the interfaces their machines are on, over every machine of
        // the card, those folded past clients_per_card included (rows 18 and E7 of the mockup's
        // coverage); current_rate_clients counts the machines whose rate is summed.
        current_rate: null,
        current_rate_clients: 0,
        interfaces: [],
        chips: [],
        chip_remainder: null,
        timeline: undefined
      };
      var figures = host.figures.zero();
      var chips = members.map(function (info) {
        var chip = chipOf(info);
        if (chip.volume !== null) {
          card.machines++;
          figures = host.figures.add(figures, chip.volume);
        }
        return chip;
      });
      // A measured machine first, by bytes, then by id; one with no data after, by id.
      chips.sort(function (a, b) {
        if (a.volume === null || b.volume === null) {
          if (a.volume !== null) {
            return -1;
          }
          if (b.volume !== null) {
            return 1;
          }
        } else if (a.volume.bytes !== b.volume.bytes) {
          return descending(a.volume.bytes, b.volume.bytes);
        }
        return a.client.id - b.client.id;
      });
      var restMembers = 0;
      var rest = host.figures.zero();
      var folded = [];
      chips.forEach(function (chip, index) {
        if (index < limit) {
          card.chips.push(chip);
          return;
        }
        restMembers++;
        folded.push(chip);
        if (chip.volume !== null) {
          rest = host.figures.add(rest, chip.volume);
        }
      });
      card.current_rate = sumRates(host, chips.map(function (chip) { return chip.current_rate; }));
      card.current_rate_clients = sampledCount(host, chips);
      card.interfaces = interfaceIds(chips);
      card.chip_remainder = host.figures.remainder(restMembers, rest, null);
      // The machines folded past clients_per_card, each the chip it would have been, in the same
      // order, with their rate together: what a person's detail lists for every machine.
      card.chip_remainder.current_rate = sumRates(host, folded.map(function (chip) { return chip.current_rate; }));
      card.chip_remainder.chips = folded;
      card.volume = host.figures.volume(figures);
      card.value = host.figures.value(measure, figures);
      return card;
    }

    var data = {
      measure: measure,
      cards: [],
      unassigned: null,
      totals_exclude_unassigned: false,
      total: null
    };
    var total = host.figures.zero();
    var hasRows = volumes.length > 0;
    owners.forEach(function (owner) {
      if (selected !== null && !selected[owner.id]) {
        return;
      }
      var members = [];
      host.query.rows("widget_owner_activity_clients_of_owner", { owner_id: owner.id }).forEach(function (row) {
        var info = host.installation.client(row.client_id);
        if (info !== null) {
          members.push(info);
        }
      });
      var card = build("owner:" + owner.id, owner, members);
      total = host.figures.add(total, card.volume);
      data.cards.push(card);
    });
    var includeUnassigned = host.params.flag("include_unassigned");
    if (includeUnassigned && selected === null) {
      var unassigned = [];
      volumes.forEach(function (volume) {
        var info = host.installation.client(volume.client_id);
        if (info !== null && info.owner_id === null) {
          unassigned.push(info);
        }
      });
      // host.clients.volumes() lists the clients by ascending id already.
      var card = build("unassigned", null, unassigned);
      total = host.figures.add(total, card.volume);
      data.unassigned = card;
    } else if (!includeUnassigned) {
      data.totals_exclude_unassigned = true;
    }
    data.cards = sortCards(host, data.cards, host.params.text("sort"));
    if (host.params.flag("show_timeline")) {
      ownerTimelines(host, data, read.covered, scope, measure);
    }
    data.total = host.figures.volume(total);
    return { data: data, has_rows: hasRows };
  }
};

// sortCards orders the person cards: by the sort parameter, then by key; by name alone, a tie
// keeping the order the owners were read in (by id).
function sortCards(host, cards, order) {
  return stableSort(cards, function (a, b) {
    var compared = 0;
    switch (order) {
      case "blocked":
        compared = descending(a.volume.blocked_connections, b.volume.blocked_connections);
        break;
      case "name":
        return host.figures.compare(a.owner.display_name, b.owner.display_name);
      case "machines":
        compared = descending(a.machines, b.machines);
        break;
      default:
        compared = descending(a.value, b.value);
    }
    return compared !== 0 ? compared : host.figures.compare(a.key, b.key);
  });
}

// ownerTimelines fills each card's inline timeline from the flows, per owner.
function ownerTimelines(host, data, covered, scope, measure) {
  var width = host.buckets.width();
  var window = host.request.window;
  var sums = {};
  host.query.rows("widget_owner_activity_buckets", { from: window.from, to: window.to, width: width })
    .forEach(function (row) {
      if (!directionInScope(host, scope, row.traffic_direction)) {
        return;
      }
      var key = row.owner_id === null ? "unassigned" : "owner:" + row.owner_id;
      if (sums[key] === undefined) {
        sums[key] = {};
      }
      var sum = sums[key][row.bucket_start];
      sums[key][row.bucket_start] = host.figures.add(sum === undefined ? host.figures.zero() : sum, row);
    });
  var gaps = host.envelope.gaps();
  var intervals = host.buckets.intervals(window, width);
  function timeline(key) {
    return intervals.map(function (bucket) {
      var start = bucket.from - host.buckets.floorMod(bucket.from, width);
      var sum = sums[key] === undefined ? undefined : sums[key][start];
      var entry = {
        from: bucket.from,
        to: bucket.to,
        gap: host.buckets.isGap(bucket, covered, gaps),
        value: null,
        volume: undefined
      };
      if (sum !== undefined || !entry.gap) {
        var figures = sum === undefined ? host.figures.zero() : sum;
        entry.value = host.figures.value(measure, figures);
        entry.volume = host.figures.volume(figures);
      }
      return entry;
    });
  }
  // A timeline with no bucket is left out, as an empty optional field is.
  function setTimeline(card) {
    var buckets = timeline(card.key);
    card.timeline = buckets.length > 0 ? buckets : undefined;
  }
  data.cards.forEach(setTimeline);
  if (data.unassigned !== null) {
    setTimeline(data.unassigned);
  }
}
