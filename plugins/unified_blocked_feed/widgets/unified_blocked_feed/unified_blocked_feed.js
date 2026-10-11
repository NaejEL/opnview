// unified_blocked_feed: every refusal of the window, newest first, over the four engines, each
// entry naming the engine, the client, the target and -- the load-bearing field -- the rule, list
// or signature that produced the decision (docs/widget-catalogue.md, "Unified blocked feed"). It
// reads the blocked_decision view, which places each refusal once under one engine_kind
// vocabulary. Each contributing source carries its own condition, so an empty feed is never read
// as "nothing happened" unless every contributor is healthy.

// FEED_ENGINES are the four engines a reader selects.
var FEED_ENGINES = ["firewall_rule", "dns_advertising_list", "dns_threat_list", "security_engine"];

// ENGINE_KINDS maps a selected engine to the blocked_decision engine kinds it covers.
var ENGINE_KINDS = {
  firewall_rule: ["firewall_rule", "firewall_no_rule", "firewall_reason_not_recorded"],
  dns_advertising_list: ["dns_advertising_list"],
  dns_threat_list: ["dns_threat_list"],
  security_engine: ["security_engine"]
};

// SOURCE_KINDS are the provider kinds that contribute refusals, in the order the sources are
// listed.
var SOURCE_KINDS = ["firewall_log", "dns_lookup", "security_event"];

// engineProvider is the provider kind behind an engine kind.
function engineProvider(kind) {
  if (kind === "security_engine") {
    return "security_event";
  }
  if (kind.length > 4 && kind.slice(0, 4) === "dns_") {
    return "dns_lookup";
  }
  return "firewall_log";
}

// refusalCount is a count of refusals: logged records, lookups and events, each a lower bound for
// the observation-point reason.
function refusalCount(value) {
  return { measure: "refusals", lower_bound: true, value: value };
}

// coverFlows records the coverage of a reading of the flows alone, and the named state when part
// of the window lies past the retention horizon.
function coverFlows(host, state) {
  host.envelope.coverFrom(host.envelope.earliest("widget_earliest_flow"));
  host.envelope.coverFlowsState(state);
}

exports.widget = {
  serve: function (host) {
    coverFlows(host, "records_beyond_flow_horizon");
    var noDomains = host.request.no_domains;
    var selected = host.params.set("engines") || [];
    var kinds = null;
    if (selected.length < FEED_ENGINES.length) {
      kinds = [];
      selected.forEach(function (engine) {
        kinds = kinds.concat(ENGINE_KINDS[engine] || []);
      });
    }
    var window = host.request.window;
    var clientIds = host.refs.ids("clients");
    var interfaceIds = host.refs.ids("interfaces");
    var filters = { from: window.from, to: window.to, client_ids: clientIds, interface_ids: interfaceIds };
    var filtersWithKinds = {
      from: window.from,
      to: window.to,
      engine_kinds: kinds,
      client_ids: clientIds,
      interface_ids: interfaceIds
    };

    var countsByKind = {};
    var excludedKinds = {};
    host.query.rows("widget_blocked_feed_counts", filters).forEach(function (row) {
      if (kinds !== null && kinds.indexOf(row.engine_kind) < 0) {
        excludedKinds[row.engine_kind] = row.refusals;
        return;
      }
      countsByKind[row.engine_kind] = row.refusals;
    });
    var total = 0;
    var perProvider = {};
    Object.keys(countsByKind).forEach(function (kind) {
      total += countsByKind[kind];
      var provider = engineProvider(kind);
      perProvider[provider] = (perProvider[provider] || 0) + countsByKind[kind];
    });
    var sources = SOURCE_KINDS.map(function (kind) {
      return {
        provider_kind: kind,
        condition: host.envelope.kindCondition(kind),
        count: refusalCount(perProvider[kind] || 0)
      };
    });

    var entries = host.query.rows("widget_blocked_feed", {
      from: window.from,
      to: window.to,
      engine_kinds: kinds,
      client_ids: clientIds,
      interface_ids: interfaceIds,
      limit: host.params.integer("limit")
    }).map(function (row) {
      var decidedBy = { kind: "", reference: row.engine_reference };
      switch (row.engine_kind) {
        case "firewall_rule":
          decidedBy.kind = "rule_not_found";
          if (row.engine_reference !== null) {
            var found = host.query.row("widget_feed_rule_by_pf_label", { value: row.engine_reference });
            if (found !== null) {
              var rule = host.installation.rule(found.id);
              decidedBy.kind = "rule";
              if (rule !== null) {
                decidedBy.rule = rule;
              }
            }
          }
          break;
        case "firewall_no_rule":
          decidedBy.kind = "no_rule";
          break;
        case "firewall_reason_not_recorded":
          decidedBy.kind = "reason_not_recorded";
          break;
        case "dns_list_not_recorded":
          decidedBy.kind = "list_not_recorded";
          break;
        case "security_engine":
          decidedBy.kind = "signature";
          break;
        default:
          decidedBy.kind = "list";
      }
      var target = row.target;
      if (row.target_kind === "domain" && noDomains) {
        target = null;
        host.envelope.withhold("entries[].target");
      }
      return {
        source_table: row.source_table,
        source_id: row.source_id,
        occurred_at: row.occurred_at,
        engine_kind: row.engine_kind,
        decided_by: decidedBy,
        client_id: row.client_id,
        interface: row.interface_id === null ? null : host.installation.interface(row.interface_id),
        target: target,
        target_kind: row.target_kind,
        // An address target's port and protocol, null where the record logged none and for a
        // domain target.
        port: row.port,
        protocol: row.protocol,
        target_is_this_firewall: row.target_is_this_firewall === 1 || row.target_is_this_firewall === true
      };
    });

    // The groups, over the whole window. A withheld domain's group carries its name only as the
    // name-free tie-break, which is never written.
    var groups = [];
    var groupBy = host.params.text("group_by");
    if (groupBy === "engine") {
      Object.keys(countsByKind).sort(host.figures.compare).forEach(function (kind) {
        groups.push({ key: kind, count: countsByKind[kind] });
      });
    } else if (groupBy === "client") {
      host.query.rows("widget_blocked_feed_by_client", filtersWithKinds).forEach(function (row) {
        groups.push({ key: row.client_id === null ? "client:none" : host.figures.clientKey(row.client_id),
          count: row.refusals });
      });
    } else if (groupBy === "target") {
      host.query.rows("widget_blocked_feed_by_target", filtersWithKinds).forEach(function (row) {
        if (row.target_kind === "domain" && noDomains) {
          // The groups of a withheld domain are kept apart without their names: the key is
          // numbered after the sort below, and the tie-break is a hash of the name, so neither the
          // number nor the order carries its rank.
          host.envelope.withhold("groups[].key");
          groups.push({ key: "domain:withheld", count: row.refusals, withheld: row.target });
          return;
        }
        groups.push({ key: row.target_kind + ":" + row.target, count: row.refusals });
      });
    }
    groups = groups.map(function (group, index) { group.index = index; return group; });
    groups.sort(function (a, b) {
      if (a.count !== b.count) {
        return b.count - a.count;
      }
      var aWithheld = a.withheld !== undefined;
      var bWithheld = b.withheld !== undefined;
      if (aWithheld !== bWithheld) {
        return aWithheld ? 1 : -1;
      }
      var order = aWithheld ? host.figures.nameFreeCompare(a.withheld, b.withheld) : host.figures.compare(a.key, b.key);
      return order !== 0 ? order : a.index - b.index;
    });
    // Withheld groups are numbered in the order just settled, which carries no name.
    var withheldNumber = 0;
    groups = groups.map(function (group) {
      var key = group.key;
      if (group.withheld !== undefined) {
        key = "domain:withheld:" + withheldNumber;
        withheldNumber++;
      }
      return { key: key, count: refusalCount(group.count) };
    });

    var data = { group_by: groupBy, entries: entries };
    if (groups.length > 0) {
      data.groups = groups;
    }
    data.remainder = { members: total - entries.length, count: refusalCount(total - entries.length) };
    data.sources = sources;
    data.counts_by_engine_kind = host.json.map(countsByKind);
    data.excluded_engine_kinds = host.json.map(excludedKinds);
    data.total = refusalCount(total);
    return { data: data, has_rows: total > 0 };
  }
};
