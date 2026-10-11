# Widget API

The HTTP API every widget is drawn from: step 5B, `specs/SPEC-step-5b-widget-api.md`.
This document is the contract the interface reads. It names the path, the carrier, the
envelope, the condition codes, the per-type parameters and the reference rules once; it
cites the catalogue (`docs/widget-catalogue.md`) and the dashboard format
(`docs/dashboard-format.md`) rather than restating them, and it carries no sentence an
interface shows: every code below is an English identifier the interface translates
through its string catalogue.

The code is `internal/widget`: one file per served preset and one row in its registry
(`docs/architecture.md`, *The widget registry*), served by `internal/web`.

## Path, carrier and access

- **Path.** `GET /api/widget/{type}`, where `{type}` is the catalogue's `snake_case`
  identifier (`docs/widget-catalogue.md`, *Preset index*). One route per served type, built
  from the registry.
- **Carrier.** One query parameter, `parameters`, holding the widget's `parameters` object
  as JSON, exactly the object a dashboard file carries (`docs/dashboard-format.md`,
  *Parameters*): scalars, arrays of scalars, reference objects and the ordered array of
  objects of `series`. There is no second encoding. An absent `parameters` is the empty
  object.
- **Access.** Every widget route is authenticated. A request with no live session is
  answered `401` with `{"condition": "no_session"}`, or `{"condition": "session_expired"}`
  when the session expired, and no widget data; it is never redirected to a page.
- **Failures.** A type with no endpoint is `404` with
  `{"condition": "unknown_widget_type", "type": ...}` -- the three alert presets of step 6
  answer this until they are built. A `parameters` value that is not a JSON object is `400`
  with `{"condition": "malformed_parameters", "reason": "not_json" | "not_an_object" |
  "trailing_data"}`. A read the store could not complete is `500` with
  `{"condition": "read_failed"}`.
- **Headers.** `Content-Type: application/json`, `Cache-Control: no-store`. No CORS header
  is ever set.
- **Boundaries.** The API reads the local database through read-only connections and
  nothing else. It makes no request to the firewall, MaxMind or the Public Suffix List, and
  no answer carries a credential, a ciphertext, a session token or a CSRF token.

## The envelope

Every `200` carries the same fields.

| Field | Meaning |
|---|---|
| `type` | the type answered |
| `parameters` | the effective parameters, after defaults; a rendering-only key is echoed here |
| `unknown_parameters` | keys the type does not declare, by path (`series[1].foo` inside a series), with their values: kept, reported, never applied, never an error |
| `invalid_parameters` | `{path, value, reason}` for each value that could not be applied; the key took its default. Reasons: `not_in_vocabulary`, `wrong_type`, `out_of_range`, `duplicate_member`, `not_a_reference_object` |
| `missing_parameters` | required keys the request lacked |
| `unresolved_references` | `{path, kind, by, value, label, reason}` per reference that matched nothing |
| `window` | `{requested, covered, coverage, flow_horizon_at}`, null for a type that takes no period; `covered` is null when the store holds nothing of the window, `coverage` is `covered`, `partially_covered` or `not_covered`, and `flow_horizon_at` is the instant before which the retention purge may have removed the flows |
| `gaps` | `{provider_kind, provider_key, from, to, reason}` per interval a bound provider did not cover; while `opnview` was not running every provider went unread, listed as `provider_kind: "opnview"`, `reason: "opnview_not_running"` |
| `availability` | every registered provider of every kind the type binds: `kind`, `provider_key`, `display_name`, `active`, `state`, `outcome`, `probe`, `checked_at`, `detail`, `condition` |
| `condition` | the widget's condition code |
| `freshness` | `{period, slots_read, last_computed_at, stale_slots, stale}` per aggregate period read |
| `aggregate_mode` | `full` or `no_domains` |
| `withheld` | every field or level the aggregate mode withheld, by path |
| `states` | named states: a figure with no answer for a stated reason |
| `resolver_window_bounded` | on a resolver-fed type: true when a `resolver_window_not_honoured` gap meets the window -- the resolver's ring buffer bounded it (catalogue Part 3, fact 1) |
| `data` | the preset's data, or null when there is nothing honest to show |

`outcome` is the closed code of `source_availability.outcome` (`docs/data-model.md`,
*`source_availability`*), the value an interface translates; `detail` is the collector's
diagnostic text, passed through verbatim and never read (decision 18).

## Condition codes

A provider's `condition` is one of the catalogue's four conditions (`docs/widget-catalogue.md`,
Part 1, *Four conditions, not three*), or the absence of a row:

| Code | When |
|---|---|
| `not_yet_probed` | `unavailable`, and the outcome or the probe is `not_yet_probed` |
| `unavailable` | `unavailable` after a probe |
| `present_but_disabled` | `present_but_disabled` |
| `reachable` | `reachable` |
| `state_unknown` | no availability row at all -- never read as healthy |

The widget's `condition` is its primary kind's: the best condition among the active
providers of that kind, or of all its providers when none is active. On top of those:

| Code | When |
|---|---|
| `reachable_no_rows` | the source is reachable and the window holds nothing: the one condition that draws measured zeros |
| `unresolved_reference` | a top-level reference matched nothing; `data` is null, never zeros |
| `missing_required_parameter` | a required key is absent (`client_traffic_detail`'s `client`) |
| `withheld_by_aggregate_mode` | the type is withheld whole under `no_domains`; for one series of `custom_chart`, that series is scoped by a site under `no_domains` |
| `no_owner_exists` | `owner_activity` with nobody created: its fifth condition |

A degraded condition with nothing stored answers `data: null`. A degraded condition with
history stored answers that history under the degraded condition. A type that reads only
`opnview`'s own records (`public_address`, `aggregate_freshness`, `source_availability`,
`custom_chart` as a whole) is `reachable` or `reachable_no_rows`. In `custom_chart`, and in
`traffic_over_time_by_scope`, every series carries its own `condition`, `availability`,
`window`, `gaps` and `unresolved_references`.

## Named states

| State | Meaning |
|---|---|
| `matrix_rules_beyond_flow_horizon` | part of the window precedes the purge horizon; the rules per cell come from the flows, which are gone there |
| `records_beyond_flow_horizon` | records (client detail, blocked feed) exist only inside the flow horizon |
| `rules_beyond_flow_horizon`, `tree_beyond_flow_horizon`, `attribution_beyond_flow_horizon`, `composition_beyond_flow_horizon`, `client_left_beyond_flow_horizon`, `joins_beyond_flow_horizon` | the same, for the figure each names |
| `site_methods_not_recorded_beyond_flow_horizon` | site totals beyond the horizon come from the domain family, which keeps no method; those flows count under `not_recorded_beyond_flow_horizon` |
| `series_read_from_hour_slots_beyond_flow_horizon` | whole-hour buckets before the earliest flow came from the interface family's slots |
| `registrable_domain_grouping_unavailable` | no Public Suffix List has been downloaded yet; nothing is grouped |

## Windows, horizon, buckets and freshness

- **The window** of a type taking `period` is the rolling `[now - period, now)`: 1 h,
  24 h, 7 d or 30 d.
- **Volume figures** come from `store.ReadVolumeWindow`'s assembly, and the client figures
  from the same assembly over the client and peer families: whole hours from the 1 h slots,
  the partial hours at either edge from the flows. Inside the flow horizon that equals a
  computation over the flows; beyond it the slots answer and `covered` says which interval
  they cover (A7). Figures no family keeps -- a record, a rule per matrix cell, a site
  name's method -- are read from the flows alone and carry a named state beyond the horizon.
- **Buckets** (decision 8) are UTC-aligned multiples of their width from the epoch: 1 m
  for 1 h, 15 m for 24 h, 1 h for 7 d, 24 h for 30 d. A bucket outside the covered interval,
  or meeting a gap, is `gap: true` and has no value unless rows fall in it; a covered,
  gap-free bucket with no row reads 0. The buckets of a series sum to the series' total.
  A sampled series aggregates each bucket by its `aggregation` and a bucket with no sample is
  a gap.
- **Freshness.** A response assembled from 1 h slots reports the slots of its whole hours;
  a slot is stale when its `computed_at` is not strictly later than the newest `ingested_at`
  among its flows, or its hour holds flows and no slot (the refresh contract's recognition
  rule, `docs/data-model.md`, *The aggregate families*). `aggregate_freshness` applies the
  same rule per period over the slots of the last 31 days.

## Volumes and the observation-point limit

`opnview` sees only what crosses the firewall: traffic between two clients behind one
interface reaches no source, so every byte, packet and connection figure is a lower bound.
Each figure says so in the payload, beside the measure it is (decision 7); this document
states the limit, and no screen prints it (`CLAUDE.md`).

| `measure` | What it is |
|---|---|
| `logged` | the filter log: bytes are the sum of the logged packet lengths -- the packet that creates a state, not the connection -- and connections are the logged records |
| `sampled` | the bytes and rates seen in the `traffic/top` samples, a few seconds each (`store.SampleSeconds`) |
| `interface_counter` | the interface's own counters, which include traffic no logging rule matched |
| `refusals`, `dns_lookups`, `security_events`, `clients`, `addresses` | counts of those things |

A `Volume` object carries `measure`, `lower_bound`, the bytes and connections, each split
allowed / blocked / unknown (an action nobody read is neither). A `Count` carries
`measure`, `lower_bound` and `value`. Logged and sampled figures are never summed.

## References

A reference object is resolved against the table of `docs/dashboard-format.md`, *The
reference object*, by exact match on its declared `by` key, and on nothing else: its `label`
is never matched on. A `kind` or `by` outside the vocabulary is unresolved
(`unknown_kind`, `unknown_by`); a kind the key does not take is `kind_not_accepted_here`; no
match is `no_match`; several rows matching a non-unique key (a host name, a rule
description) is `ambiguous`; a `site` reference under `no_domains` is
`withheld_by_aggregate_mode` (below). `country`, `operator` and `site` resolve when a geolocated
address, or a stored lookup, carries the value. A top-level unresolved reference empties the
widget; in `series`, it empties its own series.

## Aggregate mode `no_domains`

Decision 6: domain names are withheld field by field, and `top_sites` and
`sites_by_client` are withheld whole. A blocked lookup's domain is a domain name.

| Type | Withheld |
|---|---|
| `top_sites`, `sites_by_client` | `data` (condition `withheld_by_aggregate_mode`) |
| `client_traffic_detail` | `records[].site_name` |
| `connection_tree` | `outside[].children` -- the site level, whose figures are its names' |
| `unified_blocked_feed` | `entries[].target` of a DNS refusal; `groups[].key` of a domain group |
| `blocked_dns_lookups` | `rows[].domain` |
| every type taking a reference | the `value` and `label` of a `site` reference the request sent, wherever it stands: `<path>.value` |

**A domain name selects, scopes and probes nothing under `no_domains`.** A `site` reference is
never looked up: its value and label are removed from the effective parameters and from any
unresolved entry, and its path is listed in `withheld`; past the vocabulary checks, which
depend on nothing stored, it is unresolved with the reason `withheld_by_aggregate_mode`.
A `custom_chart` series it scopes answers the condition `withheld_by_aggregate_mode` with no
buckets, and the other series are untouched. The answer is therefore the same whether or not
the name was ever seen, so no response can tell which names were visited.

Every count and volume that carries no name is unchanged. `destination_countries`,
`destination_operators` and every other type carrying no domain are served unchanged.

## Series of `custom_chart`

Each entry of `series` takes `source`, `metric` or `measure`, `axis` (`left` | `right`),
`label`, `render` (`line` | `area` | `bars` | `points`), `aggregation`
(`mean` | `min` | `max` | `sum` | `last`), and an optional reference scoping it under the key
of its kind: `interface`, `client`, `rule`, `provider`, `country`, `operator`, `site`.

| `source` | `metric` / `measure` | Default aggregation | Provider |
|---|---|---|---|
| `traffic` | `measure`: `bytes`, `connections`, `allowed`, `blocked` | `sum` | `firewall_log` (plus `geo_asn` with a country or operator, `dns_lookup` with a site) |
| `firewall_metric` | `metric`: `uptime`, `cpu`, `memory`, `swap`, `disk`, `temperature`, `gateway_latency`, `gateway_loss` | `mean` | `measurement_sample` |
| `interface_throughput` | `measure`: `bits_per_second`, `bytes`, `packets`, `errors` | `mean` | `measurement_sample` |
| `dns` | `measure`: `lookups`, `blocked` | `sum` | `dns_lookup` |
| `security_event` | `measure`: `events`, `blocked` | `sum` | `security_event` |

`annotations` overlays `security_events` or `blocked_spikes` as per-bucket counts.

## Response budgets

Decision 9, measured by the pre-deployment command (`sql/schema-checks.sh`,
`ci/widgetbudget`): every served type at its defaults, the median of three, at most
250 ms (24 h) and 500 ms (30 d) on the 100 000-row seed, 1 s and 2 s on the 1 000 000-row
one. A figure over its budget fails the run.

## Parameters

Every served type, every key it accepts -- exactly the backticked keys of its catalogue
**Parameters** paragraph -- its class and its default. `data` keys are applied: two values
give two different responses. `rendering` keys are accepted, echoed in `parameters`, and
never applied. `none` is an absent default: an empty optional list means no restriction. A
test compares this table with the code.

| Type | Key | Class | Default |
|---|---|---|---|
| `interface_traffic_matrix` | `period` | data | `"24h"` |
| `interface_traffic_matrix` | `scope` | data | `"all"` |
| `interface_traffic_matrix` | `measure` | data | `"bytes"` |
| `interface_traffic_matrix` | `interfaces` | data | none |
| `interface_traffic_matrix` | `show_rules` | data | `true` |
| `traffic_over_time_by_scope` | `period` | data | `"24h"` |
| `traffic_over_time_by_scope` | `scopes` | data | `["outbound","inbound","inter_interface"]` |
| `traffic_over_time_by_scope` | `measure` | data | `"bytes"` |
| `traffic_over_time_by_scope` | `interfaces` | data | none |
| `traffic_over_time_by_scope` | `stacked` | rendering | `false` |
| `interface_volume_ranking` | `period` | data | `"24h"` |
| `interface_volume_ranking` | `measure` | data | `"bytes"` |
| `interface_volume_ranking` | `scope` | data | `"all"` |
| `interface_volume_ranking` | `limit` | data | `10` |
| `interface_volume_ranking` | `include_unlabelled` | data | `true` |
| `client_volume_ranking` | `period` | data | `"24h"` |
| `client_volume_ranking` | `interfaces` | data | none |
| `client_volume_ranking` | `identity` | data | `"any"` |
| `client_volume_ranking` | `limit` | data | `10` |
| `client_volume_ranking` | `include_unstable` | data | `true` |
| `client_volume_ranking` | `measure` | data | `"bytes"` |
| `client_traffic_detail` | `client` | data | required |
| `client_traffic_detail` | `period` | data | `"24h"` |
| `client_traffic_detail` | `action` | data | `"all"` |
| `client_traffic_detail` | `scope` | data | `"all"` |
| `client_traffic_detail` | `columns` | rendering | `["observed_at","destination","port","protocol","action","scope","bytes","country","operator","site_name","method","correlation_delay","interface"]` |
| `client_traffic_detail` | `limit` | data | `100` |
| `traffic_composition` | `period` | data | `"24h"` |
| `traffic_composition` | `group_by` | data | `"interface"` |
| `traffic_composition` | `measure` | data | `"bytes"` |
| `traffic_composition` | `slices` | data | `6` |
| `traffic_composition` | `interfaces` | data | none |
| `traffic_composition` | `clients` | data | none |
| `traffic_composition` | `scope` | data | `"all"` |
| `traffic_composition` | `show_other` | rendering | `true` |
| `connection_tree` | `period` | data | `"24h"` |
| `connection_tree` | `root` | data | `"interface"` |
| `connection_tree` | `depth` | data | `2` |
| `connection_tree` | `interfaces` | data | none |
| `connection_tree` | `clients` | data | none |
| `connection_tree` | `owners` | data | none |
| `connection_tree` | `measure` | data | `"bytes"` |
| `connection_tree` | `limit_per_level` | data | `10` |
| `connection_tree` | `sort` | data | `"volume"` |
| `connection_tree` | `include_blocked` | data | `true` |
| `owner_activity` | `period` | data | `"24h"` |
| `owner_activity` | `owners` | data | none |
| `owner_activity` | `include_unassigned` | data | `true` |
| `owner_activity` | `measure` | data | `"bytes"` |
| `owner_activity` | `scope` | data | `"all"` |
| `owner_activity` | `sort` | data | `"volume"` |
| `owner_activity` | `clients_per_card` | data | `6` |
| `owner_activity` | `show_timeline` | data | `true` |
| `top_sites` | `period` | data | `"24h"` |
| `top_sites` | `interfaces` | data | none |
| `top_sites` | `clients` | data | none |
| `top_sites` | `limit` | data | `20` |
| `top_sites` | `min_flows` | data | `1` |
| `top_sites` | `group_by_registrable_domain` | data | `false` |
| `sites_by_client` | `period` | data | `"24h"` |
| `sites_by_client` | `interfaces` | data | none |
| `sites_by_client` | `clients` | data | none |
| `sites_by_client` | `sites_per_client` | data | `5` |
| `sites_by_client` | `min_flows` | data | `1` |
| `sites_by_client` | `show_attribution_rate` | data | `true` |
| `attribution_rate_per_client` | `period` | data | `"24h"` |
| `attribution_rate_per_client` | `interfaces` | data | none |
| `attribution_rate_per_client` | `clients` | data | none |
| `attribution_rate_per_client` | `limit` | data | `20` |
| `attribution_rate_per_client` | `sort` | data | `"worst_first"` |
| `passed_traffic_world_map` | `period` | data | `"24h"` |
| `passed_traffic_world_map` | `interfaces` | data | none |
| `passed_traffic_world_map` | `scope` | data | `"north_south"` |
| `passed_traffic_world_map` | `size_by` | data | `"bytes"` |
| `passed_traffic_world_map` | `min_bytes` | data | `0` |
| `passed_traffic_world_map` | `show_unplaced` | rendering | `true` |
| `passed_traffic_world_map` | `cluster` | rendering | `true` |
| `blocked_traffic_world_map` | `period` | data | `"24h"` |
| `blocked_traffic_world_map` | `engines` | data | `["firewall_rule","security_engine"]` |
| `blocked_traffic_world_map` | `interfaces` | data | none |
| `blocked_traffic_world_map` | `endpoint` | data | `"destination"` |
| `blocked_traffic_world_map` | `size_by` | data | `"connections"` |
| `blocked_traffic_world_map` | `min_count` | data | `0` |
| `blocked_traffic_world_map` | `show_unplaced` | rendering | `true` |
| `blocked_traffic_world_map` | `cluster` | rendering | `true` |
| `destination_countries` | `period` | data | `"24h"` |
| `destination_countries` | `interfaces` | data | none |
| `destination_countries` | `limit` | data | `20` |
| `destination_countries` | `series` | data | `["allowed","blocked"]` |
| `destination_countries` | `sort` | data | `"bytes"` |
| `destination_operators` | `period` | data | `"24h"` |
| `destination_operators` | `interfaces` | data | none |
| `destination_operators` | `limit` | data | `20` |
| `destination_operators` | `series` | data | `["allowed","blocked"]` |
| `destination_operators` | `group` | data | `"asn"` |
| `traffic_sankey` | `period` | data | `"24h"` |
| `traffic_sankey` | `left` | data | `"interface"` |
| `traffic_sankey` | `right` | data | `"operator"` |
| `traffic_sankey` | `scope` | data | `"all"` |
| `traffic_sankey` | `interfaces` | data | none |
| `traffic_sankey` | `measure` | data | `"bytes"` |
| `traffic_sankey` | `max_nodes` | data | `12` |
| `traffic_sankey` | `min_share` | data | `0` |
| `traffic_sankey` | `show_blocked` | data | `true` |
| `unified_blocked_feed` | `period` | data | `"24h"` |
| `unified_blocked_feed` | `engines` | data | `["firewall_rule","dns_advertising_list","dns_threat_list","security_engine"]` |
| `unified_blocked_feed` | `interfaces` | data | none |
| `unified_blocked_feed` | `clients` | data | none |
| `unified_blocked_feed` | `limit` | data | `50` |
| `unified_blocked_feed` | `group_by` | data | `"none"` |
| `blocked_by_firewall_rule` | `period` | data | `"24h"` |
| `blocked_by_firewall_rule` | `interfaces` | data | none |
| `blocked_by_firewall_rule` | `clients` | data | none |
| `blocked_by_firewall_rule` | `limit` | data | `20` |
| `blocked_by_firewall_rule` | `include_automatic` | data | `true` |
| `blocked_by_firewall_rule` | `sort` | data | `"blocked"` |
| `blocked_dns_lookups` | `period` | data | `"24h"` |
| `blocked_dns_lookups` | `purposes` | data | `["advertising","tracking","threat","parental","other","unassigned","not_recorded"]` |
| `blocked_dns_lookups` | `blocklists` | data | none |
| `blocked_dns_lookups` | `interfaces` | data | none |
| `blocked_dns_lookups` | `clients` | data | none |
| `blocked_dns_lookups` | `limit` | data | `20` |
| `blocked_dns_lookups` | `min_count` | data | `1` |
| `public_address` | `families` | data | `["ipv4","ipv6"]` |
| `public_address` | `gateways` | data | none |
| `public_address` | `show_history` | data | `true` |
| `public_address` | `history_limit` | data | `5` |
| `firewall_health_overview` | `metrics` | data | `["uptime","cpu","memory","swap","disk","temperature","gateway_latency","gateway_loss"]` |
| `firewall_health_overview` | `period` | data | `"24h"` |
| `firewall_health_overview` | `thresholds` | data | none |
| `firewall_health_overview` | `layout` | rendering | `"tiles"` |
| `firewall_health_overview` | `show_last_sampled` | data | `true` |
| `interface_throughput` | `interfaces` | data | none |
| `interface_throughput` | `period` | data | `"24h"` |
| `interface_throughput` | `direction` | data | `"both"` |
| `interface_throughput` | `measure` | data | `"bits_per_second"` |
| `interface_throughput` | `stacked` | rendering | `false` |
| `interface_throughput` | `per_interface_axis` | rendering | `false` |
| `custom_chart` | `period` | data | `"24h"` |
| `custom_chart` | `series` | data | `[]` |
| `custom_chart` | `axes` | rendering | none |
| `custom_chart` | `legend` | rendering | `"table"` |
| `custom_chart` | `tooltip` | rendering | `"single"` |
| `custom_chart` | `annotations` | data | `[]` |
| `source_availability` | `kinds` | data | `["firewall_log","security_event","flow_volume","dhcp_lease","dns_lookup","geo_asn","measurement_sample","reconciled_state","public_suffix","resolver_cache","resolver_local_data"]` |
| `source_availability` | `show_inactive` | data | `true` |
| `source_availability` | `compact` | rendering | `false` |
| `aggregate_freshness` | `periods` | data | `["1h","24h","7d","30d"]` |
| `aggregate_freshness` | `show_bytes` | data | `true` |
| `aggregate_freshness` | `compact` | rendering | `false` |
| `unresolved_joins` | `period` | data | `"24h"` |
| `unresolved_joins` | `counters` | data | `["interface","rule","client_identity","geo"]` |
| `unresolved_joins` | `interfaces` | data | none |
| `unresolved_joins` | `show_records` | data | `false` |

Three choices the catalogue leaves open are recorded here. `show_other` and
`show_unplaced` are rendering-only: the Other slice and the unplaced counter are always
returned, because a truncation is never silent (A8). `columns` is rendering-only: every
column is returned. `depth` is 1 or 2, the two levels each side of the tree holds.

## Coverage of the mockup

Each of the 45 rows of *Data the mockup displays*
(`specs/SPEC-correlation-classification-aggregation.md`) and the type and field that carry
it, or its **cannot** reason.

| # | Item | Type and field, or why not |
|---|---|---|
| 1 | canvas period | every windowed type: `parameters.period`, `window` |
| 2 | current throughput, in / out / between interfaces | `traffic_composition`: `data.current_rates.by_direction` (sampled) |
| 3 | the same by interface or by client | `traffic_composition` with `group_by` `interface` or `client`: `data.current_rates.by_member` |
| 4 | throughput by protocol or service port | **cannot**: `traffic/top` carries neither (5A) |
| 5 | packets by blocking reason, per direction | `unified_blocked_feed`: `data.counts_by_engine_kind`; `traffic_over_time_by_scope`: `series[].buckets[].volume` (allowed / blocked / unknown) |
| 6 | refused lookups beside the rings | `blocked_dns_lookups`: `data.total`; `blocked_traffic_world_map`: `data.dns_blocked_lookups` |
| 7 | detection-engine figures | state: `source_availability` (`security_event`); refusals: `blocked_traffic_world_map.security_events`, `unified_blocked_feed`; figures by signature: step 6 |
| 8 | rolling strip at 2-second marks | **cannot at 2 s**: rates exist at the sampling interval; decisions per 1-minute bucket in `traffic_over_time_by_scope` (`period` 1h) |
| 9 | "Port not recorded" | `traffic_composition` with `group_by` `service_port`: slice `port:not_recorded` |
| 10 | totals outbound / inbound / between | `traffic_over_time_by_scope`: `series[].total_volume` |
| 11 | buckets per period | `traffic_over_time_by_scope`: `series[].buckets` |
| 12 | label, VLAN tag, identifier, device | `interface_volume_ranking`: `bars[].interface` |
| 13 | bytes east-west / north-south, share, connections, blocked | `interface_volume_ranking`: `bars[].east_west`, `.north_south`, `.volume` |
| 14 | clients seen of clients known | `interface_volume_ranking`: `bars[].clients_seen`, `.clients_known` |
| 15 | "N rules do not log" | `interface_volume_ranking`: `bars[].rules_not_logging`, `data.floating_rules_not_logging` |
| 16 | client ranking fields | `client_volume_ranking`: `bars[].client`, `.interface`, `.volume`, `.destinations` |
| 17 | public address | `public_address`: `upstreams[]` |
| 18 | per person: clients, blocked, rate, bytes, connections, interfaces | `owner_activity`: `cards[].machines`, `.volume`, `.chips[].current_rate`, `.chips[].client.interface_id` |
| 19 | "Reaching" | `connection_tree` with `root` `owner`: `outside` (site, else operator, else unplaced state) |
| 20 | per-client chip | `owner_activity`: `cards[].chips[]` (`state`, `current_rate`) |
| 21 | passed map per placed peer | `passed_traffic_world_map`: `points[]` |
| 22 | unplaced volume | `passed_traffic_world_map`: `unplaced[]` |
| 23 | blocked map | `blocked_traffic_world_map`: `points[]`, `unplaced[]`, `endpoint` |
| 24 | client current rate, share per destination | `client_traffic_detail`: `data.current_rate` (sampled); `records[].bytes` over `total` (logged) |
| 25 | local ⇄ peer:port, connections, bytes | `client_traffic_detail`: `records[]` |
| 26 | connection tree | `connection_tree`: `inside`, `outside` |
| 27 | top sites, attribution rate | `top_sites`: `rows[]`, `data.attribution` |
| 28 | registrable domain | `top_sites` with `group_by_registrable_domain`: `grouping`, `rows[].site_names` |
| 29 | sites by client | `sites_by_client`: `rows[]` |
| 30 | attribution rate | `attribution_rate_per_client`: `rows[]` |
| 31 | operators | `destination_operators`: `rows[]`, `unresolved[]` |
| 32 | Sankey | `traffic_sankey`: `ribbons[]`, `other` |
| 33 | matrix | `interface_traffic_matrix`: `cells[]`, `total` |
| 34 | blocked feed entries | `unified_blocked_feed`: `entries[]` (`target_is_this_firewall`); repeated runs as `group_by` `target` |
| 35 | totals per engine | `unified_blocked_feed`: `counts_by_engine_kind` |
| 36 | rule table | `blocked_by_firewall_rule`: `rows[]`, `rows[].timeline` |
| 37 | blocked DNS lookups | `blocked_dns_lookups`: `rows[]` |
| 38 | source availability | `source_availability`: `providers[]` |
| 39 | "detection engine stopped at 06:12" | **cannot**: the status endpoint reports a state, not when it began |
| 40 | health | `firewall_health_overview`: `tiles[]` |
| 41 | swap; gateway latency and loss | `firewall_health_overview`: metrics `swap`, `gateway_latency`, `gateway_loss` |
| 42 | interface throughput | `interface_throughput`: `series[]` |
| 43 | errors | `interface_throughput` with `measure` `errors` |
| 44 | custom chart | `custom_chart`: `series[]` |
| 45 | People page | `owner_activity`: `cards[].owner`, `chips[].client` (`identity_kind`, `unstable_identity`, `owner_assigned_at`) |
