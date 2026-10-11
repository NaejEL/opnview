// public_address: per upstream interface -- an interface carrying a gateway, never one called
// something -- the addresses it holds, their history as opnview saw it, and whether each lies in a
// special-purpose range of the IANA registries (docs/widget-catalogue.md, "Public address"). Behind
// an upstream NAT the firewall does not see its public IPv4 address at all, and nothing opnview
// reads can supply it: the response says the installation is behind one, by arithmetic on the
// address, and never presents a special-purpose address as public. The change instant is absent
// until a second address has been seen. It describes the firewall, not traffic, so its figures
// carry no observation-point bound.

// describe classifies one held address against the registries.
function describe(host, held) {
  var classified = host.specialpurpose.classify(held.address);
  return {
    address: held.address,
    family: held.address_family === 6 ? "ipv6" : "ipv4",
    prefix_length: held.prefix_length,
    source_field: held.source_field,
    special_purpose: classified.special_purpose,
    public: classified.public,
    first_seen_at: held.first_seen_at,
    last_seen_at: held.last_seen_at
  };
}

// unknownInterface is an interface the interface table does not hold, as the zero row it has
// always been written as.
function unknownInterface() {
  return {
    id: 0,
    identifier: "",
    device: "",
    description: "",
    user_label: null,
    label: "",
    labelled: false,
    link_kind: "",
    vlan_tag: null,
    upstream: false
  };
}

exports.widget = {
  serve: function (host) {
    var fields = host.addresses.sourceFields;
    var gateways = host.params.set("gateways") || [];
    var showHistory = host.params.flag("show_history");
    var historyLimit = host.params.integer("history_limit");
    var upstreams = [];
    host.addresses.upstreams().forEach(function (upstream) {
      var info = host.installation.interface(upstream.interface_id);
      var entry = { interface: info === null ? unknownInterface() : info, current: [] };
      var held = [];
      var behind = null;
      var latest = {};
      var seenPerField = {};
      upstream.history.forEach(function (address) {
        latest[address.source_field] = Math.max(latest[address.source_field] || 0, address.last_seen_at);
        seenPerField[address.source_field] = (seenPerField[address.source_field] || 0) + 1;
      });
      var previous = [];
      upstream.history.forEach(function (address) {
        var described = describe(host, address);
        if (!host.params.has("families", described.family)) {
          return;
        }
        if (address.source_field === fields.gateways) {
          if (gateways.length > 0 && gateways.indexOf(address.address) < 0) {
            return;
          }
          held.push(described);
          return;
        }
        if (address.last_seen_at < latest[address.source_field]) {
          previous.push(described);
          return;
        }
        if (seenPerField[address.source_field] > 1) {
          described.changed_at = address.first_seen_at;
          described.change_state = "changed";
        } else {
          described.change_state = "not_recorded_only_one_address_seen";
        }
        entry.current.push(described);
        if (described.family === "ipv4" && address.source_field === fields.addr4) {
          behind = !described.public;
        }
      });
      if (gateways.length > 0 && held.length === 0) {
        return;
      }
      if (showHistory) {
        var history = previous.slice(0, historyLimit);
        if (history.length > 0) {
          entry.history = history;
        }
        entry.history_remainder = { members: Math.max(previous.length - historyLimit, 0) };
      }
      entry.gateways = held;
      entry.behind_upstream_nat = behind;
      upstreams.push(entry);
    });
    return {
      data: { upstreams: upstreams, registries: host.specialpurpose.registries },
      has_rows: upstreams.length > 0
    };
  }
};
