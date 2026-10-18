# Widget API

The HTTP API every widget is drawn from: step 5B, `specs/SPEC-step-5b-widget-api.md`.
This document is the contract the interface reads. It names the path, the carrier, the
envelope, the condition codes, the per-type parameters and the reference rules once; it
cites the catalogue (`docs/widget-catalogue.md`) and the dashboard format
(`docs/dashboard-format.md`) rather than restating them, and it carries no sentence an
interface shows: every code below is an English identifier the interface translates
through its string catalogue.

The code is `internal/widget` (the core) and `plugins/<plugin>/widgets/<type>/` (one directory per
built-in preset, a widget plugin: its `manifest.json`, its statements and, when the shape cannot
express it, its script; `docs/plugins.md`, *Widgets*), served by `internal/web`. A widget is a directory, there is no list of them, and an
operator adds one under `<data-dir>/widgets/<type>/` with a manifest and a query: see
`docs/widget-authoring.md` and `docs/extending.md`. An operator widget answers with this same envelope
and these same conditions; its figures are `{"measure", "lower_bound", "value"}`.

## Path, carrier and access

- **Path.** `GET /api/widget/{type}`, where `{type}` is the catalogue's `snake_case`
  identifier (`docs/widget-catalogue.md`, *Preset index*). One route per served type, built
  from the loaded widgets (built-in and operator).
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
  `{"condition": "read_failed"}`. A widget whose script threw, ran past its time bound or answered
  something that is not an answer is `500` with `{"condition": "script_failed"}`; the reason is in
  the service's log, never in the answer (`docs/plugins.md`, *Widgets*).
- **Headers.** `Content-Type: application/json`, `Cache-Control: no-store`. No CORS header
  is ever set.
- **Boundaries.** The API reads the local database through read-only connections and
  nothing else. It makes no request to the firewall or to any destination a plugin declares, and
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
| `state_details` | the detail of a state of `states` that carries one, keyed by the state's name: `{"netflow_not_covering_window": {"interval_states": ["capture_configuration_unknown", "capture_configured"]}}` (*Named states*). **Absent** when no state of the answer carries a detail; `states` stays a list of names |
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
| `unnamed_destinations_beyond_flow_horizon` | part of the window precedes the purge horizon: no aggregate keeps the destinations no name was inferred for by address, so those hours have no address rows (`top_sites`, `sites_by_client`); never estimated |
| `netflow_not_configured` | a volume answer is not of the `netflow` measure, or carries no figure: no provider of `flow_record` is active, or the capture configuration in force names no interface or has the export disabled |
| `capture_configuration_unknown` | the same: the capture configuration could not be read, or lies between two reads that differ |
| `netflow_not_covering_window` | the same: the window mixes intervals in different states, or the answer counts a connection that is not covered (*Volumes and the observation-point limit*). Its detail, `state_details.netflow_not_covering_window.interval_states`, lists the capture states of the intervals the window meets, in name order: `capture_configured` for an interval with a readable capture configuration whose export is enabled and captures an interface (`opnview`'s own term: no product names it), and `netflow_not_configured` or `capture_configuration_unknown` for the others. A window with a configuration throughout whose answer counts an uncovered connection lists `capture_configured` alone |
| `netflow_not_served_by_type` | the same: the type built a figure the core did not read in the `netflow` measure, so its answer cannot be given in it (`docs/plugins.md`, *Volume figures and their measure*) |

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
interface reaches no source, so every aggregate byte, packet and connection figure is a lower
bound. Each figure says so in the payload, beside the measure it is (decision 7); this document
states the limit, and no screen prints it (`CLAUDE.md`).

| `measure` | What it is |
|---|---|
| `logged` | the filter log: bytes are the sum of the logged packet lengths -- the packet that creates a state, not the connection -- and connections are the logged records |
| `netflow` | bytes and packets the firewall's NetFlow export reported for the logged connections, each direction counted once, from the records in the first leg's form, or from the translated form where no exporter sees the first leg's form; copies are otherwise never counted (`docs/data-model.md`, *NetFlow volumes*); its connections are the logged connections it reports them for |
| `sampled` | the bytes and rates seen in the `traffic/top` samples, a few seconds each (`store.SampleSeconds`) |
| `interface_counter` | the interface's own counters, which include traffic no logging rule matched |
| `refusals`, `dns_lookups`, `security_events`, `clients`, `addresses` | counts of those things |

A `Volume` object carries `measure`, `lower_bound`, the bytes and connections, each split
allowed / blocked / unknown (an action nobody read is neither). A `logged` Volume gains no
field. A `netflow` Volume also carries `packets` and `coverage`, and, as its bytes and connections
are split, `allowed_packets`, `blocked_packets`, `unknown_packets` and `allowed_coverage`,
`blocked_coverage`, `unknown_coverage`: each part carries its own packets and coverage, so a map,
which keeps the allowed or the blocked part, serves that part's own. A `coverage` object holds how
many connections fall in each coverage class -- `measured` (both directions measured), `partial`
(some measured bytes, not both directions measured), `unmeasured` (a readable configuration, no
measured byte) and `capture_not_known` (no readable, enabled capture configuration at the
connection's start), the four summing to its connections -- `direction_states`: per direction state
of the connection's measured volume (`measured`, `in_progress`, `awaiting_export`, `missing`,
`export_gap`, `not_exported_when_refused`, `forwarding_export_unsettled`, `ingress_not_captured`,
`parent_copies_only`, `interface_not_exported`, `interface_not_captured`,
`capture_configuration_unknown`; `docs/data-model.md`, *NetFlow volumes*), the connections with at
least one direction in it, so that no direction NetFlow cannot measure is silently zero -- and
`counted_copy`, the connections with a direction counted from translated copies. A connection whose two directions are in two states counts under
both, so `direction_states` says which states were met and does not sum to the connections. A
`Count` carries `measure`, `lower_bound` and `value`. No two measures are ever summed:
not `logged`, `netflow`, `sampled` and `interface_counter` with one another.

**`volume_measure`.** Every type serving a `Volume` or a byte `Count` takes `volume_measure`:
`logged`, every measure a plugin declares (`netflow` for the netflow plugin; `docs/plugins.md`,
*Measures*), or `best_available`, default `best_available`; the observation-point limit holds for every
declared measure as for `logged`. The core gives the parameter to
every type whose manifest declares `volumes` (`docs/widget-authoring.md`), and decides which measure
the answer is given in; the types are those whose row in *Parameters* carries the key. One answer
is of one measure, and every figure in it says which.

- **`logged`** answers the filter log's figures, as every volume type always has.
- **`best_available`** answers `netflow` only when the window has a readable capture configuration
  with the export enabled throughout, and every connection the answer counts is covered; with several
  declared measures, the first in name order that answers. Otherwise it answers `logged`, byte for
  byte what `logged` answers, with
  one named state saying why (*Named states*): `netflow_not_configured`,
  `capture_configuration_unknown`, `netflow_not_covering_window` or `netflow_not_served_by_type`.
  Every interface may be captured; an answer counting a connection NetFlow cannot measure -- an
  egress-only interface's inbound traffic, forwarding between two exported interfaces -- falls back
  to `logged` with `netflow_not_covering_window`. The rule is strict on purpose this cycle (decided
  question 5 of the amendment "the NetFlow join with every interface captured").
- **`netflow`** answers the netflow figures. When the window has no readable, enabled configuration,
  or the type built a figure the core did not read in that measure, it carries **no figure** --
  `data` is `null` -- and the same named state; it never carries logged figures labelled `netflow`.
  When NetFlow covered only part of what the answer counts, it answers the netflow figures, each
  with its `coverage`, and the state `netflow_not_covering_window`.

Whether a connection is covered is read from what is stored -- the capture configuration in force,
never inferred from the records, and each connection's measured volume, which the aggregates count
per direction state -- and judged over the figures the answer itself builds. A connection is
**covered** when its capture was known and each of its directions is `measured`, `in_progress`,
`awaiting_export` or `missing`: the export reports it, or should have and reported nothing. Every
other state of NJ5, or a connection whose capture was not known (`capture_not_known`), is not
covered. So a figure still
growing (`in_progress`) or one whose records are late or lost (`awaiting_export`, `missing`) is
answered in the `netflow` measure, and its `coverage` says so: a covered connection is not
necessarily `measured`.

**`lower_bound`** is `true` on every `logged` figure and on every aggregate `netflow` figure: an
unlogged connection is in no keyed aggregate, and traffic inside one interface is invisible. It is
`false` only on a single `client_traffic_detail` record of the `netflow` measure whose two
directions are `measured` -- a `measured` direction is one no NetFlow gap met. Such a record also
carries, per direction (`forward` and `reverse`, as its first leg logged them), the bytes, the
packets, the state and `counted_copy` (whether it was counted from translated copies), and a
`coverage` object, or `null` directions when its connection started under no readable, enabled capture
configuration -- the coverage then counts it `capture_not_known`.

`custom_chart`'s `traffic` source offers two measures for every declared measure, `<measure>_bytes`
and `<measure>_packets` -- `netflow_bytes` and `netflow_packets` for the netflow plugin's: a series of
either is that measure whatever `volume_measure` says, drawn beside the chart's other series and never
summed with them, and carries no figure, with the measure's named state, when the measure cannot
answer the window.

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
| `client_traffic_detail` | `records[].site_name`, `records[].method`, `records[].correlation_delay_seconds` -- every record is in address form |
| `connection_tree` | `outside[].children[].site_name` -- every destination below the operator is its address node, named ones included; the core reads no site name (`host.flows.destinations`) |
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

**The answer does not depend on whether a name was ever inferred.** In `connection_tree`, the
owner-rooted views and `client_traffic_detail`, every destination is shown by its address with
its operator and country, named ones included, so those answers are byte for byte the ones the
same database would give with no attribution stored (C2 of the backend-completion cycle).

A widget that is not built in is held to this by the core: under `no_domains`, its script's read
of a statement carrying a domain name is not run and its whole answer is withheld
(`withheld_by_aggregate_mode`, `data` null; `docs/plugins.md`, *Widgets*), as a declarative widget
reading such a column already is.

Every count and volume that carries no name is unchanged. `destination_countries`,
`destination_operators` and every other type carrying no domain are served unchanged.

## Series of `custom_chart`

Each entry of `series` takes `source`, `metric` or `measure`, `axis` (`left` | `right`),
`label`, `render` (`line` | `area` | `bars` | `points`), `aggregation`
(`mean` | `min` | `max` | `sum` | `last`), and an optional reference scoping it under the key
of its kind: `interface`, `client`, `rule`, `provider`, `country`, `operator`, `site`.

| `source` | `metric` / `measure` | Default aggregation | Provider |
|---|---|---|---|
| `traffic` | `measure`: `bytes`, `connections`, `allowed`, `blocked`, and `<measure>_bytes`, `<measure>_packets` for every declared measure (`netflow_bytes`, `netflow_packets`) | `sum` | `firewall_log` (plus `ip_geolocation` with a country or operator, `dns_lookup` with a site) |
| `firewall_metric` | `metric`: `uptime`, `cpu`, `memory`, `swap`, `disk`, `temperature`, `gateway_latency`, `gateway_loss` | `mean` | `measurement_sample` |
| `interface_throughput` | `measure`: `bits_per_second`, `bytes`, `packets`, `errors` | `mean` | `measurement_sample` |
| `dns` | `measure`: `lookups`, `blocked` | `sum` | `dns_lookup` |
| `security_event` | `measure`: `events`, `blocked` | `sum` | `security_event` |

`annotations` overlays `security_events` or `blocked_spikes` as per-bucket counts.

**A series may also read any dataset of the data catalogue that has a time column** (below), a
plugin's table included: `source` is the dataset's name, `measure` one of its measure columns
(the first by default), and `aggregation` any aggregation that column admits (a sum over an event
dataset and a mean over a sample one by default, where admitted). `metric` and the references do
not apply to it and are reported invalid. Its provider is the one that dates the dataset; its
buckets are the chart's, read by the catalogue's compiled query, and an empty bucket is a zero only
for a sum or a count over an event dataset. The five sources of the table keep their own reads,
and their answers are unchanged.

## Data catalogue

Every dataset the core and every loaded plugin expose, and one query against one (P8 of
`specs/SPEC-backend-completion-plugins.md`). Both routes are authenticated like the widget routes:
without a session they answer `401` with a JSON condition. **No SQL text is accepted from the
browser**: a query names a dataset, columns, aggregations, operators and values; the server takes
every identifier from the dataset's declaration, quoted, binds every value, and runs one `SELECT`
on the read connections in a read-only transaction.

**`GET /api/datasets`** lists `{"datasets": [...]}`, ordered by name, each with:

| Field | Meaning |
|---|---|
| `name` | a core dataset's name, or `<plugin>.<table>` |
| `origin` | `core`, or the plugin's name |
| `label` | the catalogue key of its name: `dataset.<name>`, or `plugin.<plugin>.dataset.<table>` |
| `nature` | `event` (a covered bucket with no row is a true zero) or `sample` (a bucket with no row is a gap) |
| `time_column` | its time column, or null |
| `retention_seconds`, `retention_horizon_at` | how long a row is kept and the instant before which rows may be gone; null when kept without limit |
| `freshness` | `{provider_kind, provider_key, last_success_at, condition}`: the provider whose last healthy pass dates it |
| `loss_detectable` | present on a dataset a push source feeds: true when a loss between the firewall and `opnview` is detected and recorded as a gap (TCP syslog, NetFlow), false over UDP syslog |
| `columns` | `{name, type, role, unit, nullable, carries_domain, aggregations}` each; `role` is `time`, `dimension` or `measure` |

The listing never carries a set-aside table, `account`, `session`, `encrypted_credential`, a
credential or secret setting, or `plugin_state`.

**The core's datasets** are the five sources of `custom_chart`, the two NetFlow datasets and the four a
plugin joining another source reads (`docs/plugins.md`, *Datasets a plugin reads*), each
declared as data in its own directory under `internal/dataset/core/` (its declaration, the `SELECT` it
reads and its label), so adding one is adding a directory:

| Dataset | Nature | Time column | Measures | Provider |
|---|---|---|---|---|
| `traffic` | event | `observed_at` | `bytes` (byte), `connections`, `allowed`, `blocked` (connection) | `firewall_log` |
| `firewall_metric` | sample | `sampled_at` | `value` (the unit is in the `unit` dimension) | `measurement_sample` |
| `interface_throughput` | sample | `sampled_at` | `increase`, `per_second` (the `counter` dimension names the unit) | `measurement_sample` |
| `dns` | event | `looked_up_at` | `lookups`, `blocked` (lookup) | `dns_lookup` |
| `security_event` | event | `occurred_at` | `events`, `blocked` (event) | `security_event` |
| `flow_record` | event | `start_at` | `octets` (byte), `packets` (packet), `records` (record) | `flow_record` |
| `unmatched_record` | event | `period_start_at` | `bytes` (byte), `packets` (packet), `records` (record) | `flow_record` |
| `leg` | event | `observed_at` | `packet_bytes` (byte) | `firewall_log` |
| `interface` | sample | none | none | none: discovery |
| `this_firewall_address` | sample | none | none | none: discovery |
| `provider_value` | sample | none | none | none: the active providers' exported values |

`flow_record` serves every flow record stored, whatever the capture configuration was, with what
its two indexes named at reception (`ingress_role`, `ingress_identifier`, `egress_role`,
`egress_identifier`), its `join_state`, `copy_kind`, `counted_copy` and `unmatched_reason`, and
**`loss_detectable` per row**: 1 over the intervals whose configuration bounded the exporter's
sequence counters, so that a loss would have been recorded as a gap, and 0 elsewhere. Its label states
that a sum over records that are not joined counts copies -- a packet is exported again in its
translated form, or by a VLAN's parent -- and no built-in widget serves such a sum.
`unmatched_record` is the unmatched family's hour slots: the records that joined no logged leg and are
no copy, by ingress and egress interface (an identifier, or the role of an index that names none),
protocol, IP version and reason.

**Every table a loaded plugin declares is a dataset**, its columns as its manifest declares them
(`docs/plugins.md`, *Tables*).

**`GET /api/datasets/{name}/query`** carries one query parameter, `parameters`, holding a JSON
object, exactly as `GET /api/widget/{type}` carries a widget's:

| Key | Value | Default |
|---|---|---|
| `period` | `1h`, `24h`, `7d` or `30d`: the rolling window ending now | `24h` |
| `x` | `{"column": ..., "bucket": ...}`: the time column with a `bucket` of `auto` or a number of seconds, or one dimension | the time column, `auto` |
| `y` | a list of `{"column": ..., "aggregation": ...}`; no column counts the rows | `[{"aggregation": "count"}]` |
| `filters` | a list of `{"column": ..., "op": ..., "value": ...}` | none |
| `split_by` | `{"column": ..., "limit": ...}`: one dimension, its `limit` values ranked by the first measure, and a remainder entry for every other value and for null | none; `limit` 10 |
| `order`, `limit` | for a dimension `x`: `desc` or `asc` by the first measure, and how many keys | `desc`, 100 |

The aggregations are `sum`, `mean`, `min`, `max`, `last` and `count` on a measure, and `count` and
`count_distinct` on a dimension, each where the column admits it. The operators are `eq`, `ne`,
`in`, `not_in`, `lt`, `le`, `gt`, `ge` (on numbers), `is_null` and `is_not_null` (on a nullable
column); a value is typed against its column -- a whole number, a number or a string, never
converted -- and a text value is matched literally, whatever it holds.

`auto` is the widget API's bucket for the period: 1 m, 15 m, 1 h and 24 h. A bucket is a multiple
of its width from the epoch, in UTC, except two: **604800 seconds is the ISO week** (Monday 00:00
UTC) **and 2592000 seconds the calendar month** (the 1st, 00:00 UTC), the boundaries of the volume
aggregates' `7d` and `30d` periods (`docs/widget-catalogue.md`, *Custom chart*).

**The answer** is the envelope above, `dataset` where a widget's carries `type`, and `data`:

- on a time axis, `{x: {column, bucket_seconds, alignment}, series: [...]}`, one series per
  measure -- per measure and split value with the remainder last when the query splits -- each
  `{key, column, aggregation, unit, split, buckets}`;
- on a dimension axis, `{x: {column}, members, measures, rows}`, `members` the number of keys in
  all and each row `{key, split, values}`.

A bucket the collection did not cover -- before the dataset's oldest row, inside a collection gap
of its provider or while `opnview` was not running -- is a gap; so is any bucket of a sample dataset
with no row. A covered bucket with no row of an event dataset reads 0 for a sum or a count, and has
no value otherwise. **Under `no_domains`**, a query reading, filtering or splitting on a column that
carries a domain answers `withheld_by_aggregate_mode` with `data: null`.

**Refusals.** A query the server will not compile answers `400` -- `404` for an unknown dataset --
with `{"condition": ..., "path": ...}`, and runs no statement:

| Condition | When |
|---|---|
| `unknown_dataset` | the name is no dataset of the catalogue |
| `malformed_parameters` | `parameters` is not one JSON object, or a key has not the shape above |
| `unknown_column` | a column the dataset does not declare |
| `column_role_not_admitted` | a measure as `x`, a time column as `split_by`, the time column as a measure |
| `aggregation_not_admitted` | an aggregation outside the list, or one the column does not admit |
| `operator_not_admitted` | an operator outside the list, or one the column's type does not admit |
| `value_type_mismatch` | a filter value that is not of its column's type |
| `bucket_not_admitted` | a bucket that is not `auto` nor a whole number of at least 60 seconds |
| `too_many_buckets` | more than 1 000 buckets in the period |
| `too_many_series` | more than 20 series: measures times split values, the remainder aside |
| `too_many_rows` | a dimension `limit` above 1 000, or more than 100 values in one filter |

**Bounds**, each a constant written once in `internal/dataset/query.go`: 1 000 buckets per series,
20 series plus the remainder, 1 000 rows on a dimension axis, 100 values per filter, a bucket of at
least 60 seconds, and a statement timeout of 5 s.

**The firewall's own data.** The tables the broker learned from the firewall's answers, and the
streams it recorded, are datasets of origin `opnsense`, named `opnsense.<table>`, labelled
`dataset.opnsense_mirror` (A15 of the broker amendment). Their roles are inferred from the types the
broker learned: a learned table's time column is `seen_at` -- when an answer last held the row --
or, for a record stream on a timestamp cursor, `cursor_at`, the record's own time; a recorded
stream's is `received_at`. A number is a measure and everything else a dimension. Both are
`sample` datasets: a bucket no answer and no event reached is a gap, never a zero. A column the
broker learned carries a domain name says so, and under `no_domains` a query touching it answers
`withheld_by_aggregate_mode`.

## API catalogue

**`GET /api/firewall/catalogue`** answers what the broker learned of every endpoint it called (A15
of the broker amendment): for each endpoint its path and method, its parameters and their roles,
each datum's nature, row set, key, cursor and schema versions -- each with the OPNsense version
that produced it --, the readers and the fields each read, the tables it fills, its costs (calls,
median latency, median size), its freshness floor, whether it was seen to answer as a stream and
for how long a connection lasts, the plugins that read it without a survey citation (`uncited`),
and its counters; and every stream subscription held, with its subscribers and whether it feeds a
table. It is authenticated like every route here: without a session it answers `401` with a JSON
condition. **It never carries a field value, a credential, the firewall's address or an
authorisation header.** A plugin reads the same content with `host.catalogue()`.

Its `push` part is what `opnview` receives rather than calls (`docs/plugins.md`, *Push sources*):
`listeners[]` -- each `{protocol, transport, state, received, decoded, connections, counters, seen,
flags, loss_detectable}`, `seen` the programs or templates met with their counts --, `subscriptions[]`
-- each push declaration `{id, protocol, plugin, entry, output_table | kind, columns, replaces,
transports, listener_state, liveness, stored, flushes, queued, counters}`, `listener_state` being
`listening` or `listener_not_configured` --, `senders` -- the accepted set's size, whether it has been
derived, and the refused senders by address with their counts --, and `protocols`, those this build
decodes. It carries no message and no field value.

## Live figures

A **live figure** is a current rate read from the samples of the `measurement_sample` kind: a second
measure, `sampled`, never summed with a logged one. The widget API reads it from the database and
never calls the firewall. Any authenticated answer carrying one -- `traffic_composition`
(`data.current_rates`), `owner_activity` (`cards[].current_rate`, `chips[].current_rate`,
`chip_remainder.current_rate`), `client_traffic_detail` (`data.current_rate`,
`data.current_rate_by_peer`) and the rolling strip below -- renews the **live-interest lease**
(`live_interest_seconds`, 30 s by default). While the lease holds, every plugin declaring a live pass
(`docs/plugins.md`, *Live pass*) samples at its live cadence through the broker; when it lapses, fine
sampling stops. The baseline pass runs at the measurement interval throughout.

Every live figure is `{state, measure, in_bits_per_second, out_bits_per_second, out_measured,
sampled_at, span_seconds, cadence_seconds, lower_bound}`:

| Field | Meaning |
|---|---|
| `state` | `current`; `stale_sample` when its oldest reading is older than twice `cadence_seconds`, its figures beside it; `no_current_sample` with no reading within twice the measurement interval, every figure null |
| `measure` | `sampled` |
| `sampled_at` | the instant of its newest reading |
| `span_seconds` | how long one reading covers: 2, one `traffic/top` capture |
| `cadence_seconds` | how often the readings are taken: the live cadence of a fine sample, the measurement interval of a baseline one |
| `lower_bound` | always true: traffic between two clients behind one interface reaches no source |

**Rates summed and rates per peer.** A person's rate (`owner_activity` `cards[].current_rate`, and
`chip_remainder.current_rate` for the machines folded past `clients_per_card`) is the sum of the rates
of their machines holding a current sample, `cards[].current_rate_clients` of them: `out_bits_per_second`
only when every rate summed measured it, `sampled_at` the newest reading, `cadence_seconds` the
coarsest, `stale_sample` when any rate summed is; with none, `no_current_sample`. A rate per peer
(`client_traffic_detail` `current_rate_by_peer[]`, `{peer, rate}` by address in byte order) is the
per-pair reading of `traffic/top` for that peer: `in_bits_per_second` what the peer sent,
`out_bits_per_second` what was sent to it, measured only when the local address was read with that one
peer -- `traffic/top` reports a local address's sending only as its total over every peer -- and
otherwise `null` with `out_measured` false. A peer with no current reading is absent from the list,
never a rate of 0. The core reads them once for every widget: `host.rates.byPeer()` and
`host.rates.clientPeers(id)` (`docs/plugins.md`, *The widget host*).

**The rolling strip.** `GET /api/live/strip`, authenticated, takes `parameters` as a widget does:
`{"window_seconds": n}`, 120 by default, from two cadences to 3 600 and at most 1 000 buckets; a value
outside that is `400` `{"condition": "window_out_of_range", "key", "minimum", "maximum"}`, and a
`parameters` that is not that object `400` `{"condition": "malformed_parameters"}`. Its buckets are
`cadence_seconds` wide -- the finest live cadence in force, or the measurement interval where no
plugin declares a live pass -- aligned on UTC multiples from the epoch, and it answers
`{cadence_seconds, window, live_interest_until, throughput, decisions, gaps}`:

- `throughput[]`, keyed `outbound`, `inbound`, `inter_interface` and `outbound_unsplit`, each with
  `measure` `sampled`, `unit` `bit_per_second`, `span_seconds`, `cadence_seconds`, `lower_bound` and
  `buckets[]` of `{from, to, gap, value, samples, sampled_at}`: the mean of the samples, fine and
  baseline, taken in the bucket; a bucket with no sample is a gap with no value;
- `decisions[]`, keyed `allowed`, `firewall_rule`, `firewall_no_rule`,
  `firewall_reason_not_recorded` (the filter log, `measure` `logged`) and `dns_refusal` (the resolvers'
  refusals of every list, `measure` `dns_lookups`), counted from the records' own timestamps. Each
  carries `provider_kind` and `complete_until`, the end of its active providers' last healthy pass:
  a bucket ending after it, or meeting a gap of its kind, is `gap: true` -- with the records already
  stored in it as its value, never a zero -- and with no healthy pass recorded every bucket is a gap.

**Settings.** `GET /api/live/settings` answers `{settings, cadences}`: `live_interest_seconds`
(default 30, 5 to 3 600) and `live_sample_retention_seconds` (default 3 600, 600 to 604 800), each
`{key, value, default, minimum, maximum, set}`, and per live pass `{plugin, kind, provider_key,
setting_key, cadence_seconds, floor_seconds}`. `POST /api/live/settings`, a write (*Write endpoints*),
takes an object of the two keys; an unknown key, a value that is not a whole number, or one out of
range is `400` `{"condition", "key"}` -- `unknown_setting`, `invalid_setting_value`,
`setting_out_of_range` -- and writes nothing. A live cadence is its plugin's setting, written through
`POST /api/plugins/{name}/settings`; the cadence in force is never below its endpoints' freshness
floor. Fine samples are purged after `live_sample_retention_seconds`; baseline samples follow
`retention_seconds`.

**NetFlow settings.** The netflow plugin declares them, with their bounds and defaults, in its manifest:
`join_tolerance_seconds` (default 3, 1 to 60), how far after a leg was logged a flow record may start
and still join it, and `retention_flow_record_seconds` (default 0, which follows `retention_seconds`), how
long a flow record is kept. They are read and written as every plugin's settings are, through
`GET` and `POST /api/plugins/netflow/settings`, which also carry the page the manifest groups them in
(PS17 and PS19 of the NetFlow join amendment, as PA8 of the plugin-API amendment moves them).

**Rate by port and protocol: the pf state table.** `traffic/top` runs iftop without `-P`, so its
samples carry no port and no protocol (`scripts/interfaces/traffic_top.py`). The built-in `pf_states`
plugin reads the pf state table instead, through `POST /api/diagnostics/firewall/query_states`, a
read action (`docs/opnsense-api-survey.md`, *Live rate endpoints, read from source for C3 (backend
completion)*), and stores the rate as the dataset `pf_states.rate` (*Data catalogue*): per pass
`sampled_at`, `span_seconds` (the seconds between the two reads the rate is taken over),
`interface_device`, `direction` (pf's `in` or `out`), `protocol`, `port` (the destination port, `null`
for a protocol with none), `forward_bits_per_second` (from the state's initiator),
`reverse_bits_per_second`, `bits_per_second` and `states`. A rate is the difference of each state's
cumulative bytes between two reads, over the seconds between them; a state created in between counts
whole, and one that ended in between is gone with its last bytes, so every figure is a lower bound of
the `sampled` measure, never summed with a logged one. pf keeps a state on the interface a forwarded
connection enters (`in`) and another on the one it leaves by (`out`): filter `direction` `eq` `in` to
count a forwarded connection once (the firewall's own traffic has an `out` state only).
`traffic_composition` serves it per protocol or service member as a live figure of the shape above,
like a client's or an interface's: a live figure is relative to the local side, and a direction-in
state sits on the interface its connection entered by, so the local side is the initiator --
`out_bits_per_second` is the sum of `forward_bits_per_second`, `in_bits_per_second` of
`reverse_bits_per_second`, `out_measured` is `true` (pf keeps both counters on every state and neither
column is nullable; a figure a source cannot fill is `null` with `out_measured` false, never a rate of
`0`, and none is unfilled here), `sampled_at` is the pass's instant, and `span_seconds` and
`cadence_seconds` are both the pass's `span_seconds`, the cadence the pass actually ran at;
`bits_per_second` and `states` have no field in a live figure and are not carried. The provider
(kind `pf_state_rate`) is active on installation and passes every 60 seconds by default
(`cadence__pf_state_rate__pf_states`, at least 10); it reads at most `max_states` states a pass
(50 000 by default), and a table larger than that is read in part, its availability
`partial_readings`. Its cost on the firewall -- one walk of the whole state table per pass -- is
measured by LV-C, and the maintainer decides whether it stays on if it exceeds 2 processor points.
It is not a live pass: its cadence does not follow the live-interest lease. Logged volume by port and
protocol is served from the filter log.

## Events

The front is event-driven: it asks a widget again when something the widget reads was stored, and
polls only while it cannot hear (decided by the orchestrator on the maintainer's instruction,
18 October 2026; `specs/SPEC-step-7-frontend.md`, D21). **`GET /api/events`**, authenticated like
every read -- without a session it answers `401` with a JSON condition --, answers
`text/event-stream` (Server-Sent Events), `Cache-Control: no-store`, same origin. The page opens one
`EventSource` on it; `connect-src 'self'` already allows it.

The stream relays the notifications the core publishes when what it stored is committed
(`docs/plugins.md`, *Notifications*), one event each. Every event's SSE name is its `type`, and its
`data` is one JSON object carrying `type`, `at` (when the server relayed it, UTC epoch seconds) and
what the notification names. It carries no figure: an answer is only ever composed by the widget
API, and asking it again is what renews the live-interest lease (*Live figures*).

| `type` | Sent when | Members beside `type` and `at` |
|---|---|---|
| `hello` | The stream opened. | `heartbeat_seconds`, `lifetime_seconds` |
| `collection_pass` | A collection pass of a core kind ended without an error, once per provider it read (`opnview_collection_pass`). | `kind`, `provider_key` |
| `plugin_pass` | A pass of a plugin's own kind stored its rows (`opnview_plugin_pass`). | `plugin`, `provider` |
| `aggregate_refresh` | An aggregate refresh rewrote slots (`opnview_aggregate_refresh`). It follows every derivation -- every filter-log pass, every pushed syslog or NetFlow batch stored, every lease, lookup and security-event pass -- because the current slot of each period is rewritten every time. | `periods`, `ranges` (`[{period, from, to}]`, per period the span of the slots rewritten), `families` (`volume`, `rule`, `domain`, `peer`, `client`, `owner`) |
| `live_sample` | A live pass stored fine samples (`opnview_live_sample`). The baseline pass of the same kind is a `collection_pass` of `measurement_sample`. | `kind`, `provider_key`, `sampled_at` (the newest reading) |
| `resync` | Events were lost for this reader, which fell behind by more than 64: anything it shows may have changed. | none |
| `end` | The stream's lifetime elapsed; the server closes it. | none |

**Which widget an event concerns** is read from the widget registry (*The widget registry*): a
`collection_pass` or `live_sample` concerns the widgets whose `providers` include its `kind`; a
`plugin_pass` the widgets of that `plugin`; an `aggregate_refresh` every windowed widget whose
window meets one of its `ranges` -- the current slot is in every refresh, so in practice every
windowed widget; `hello` after a reconnection and `resync` concern every widget on the canvas shown.

**Bounds.** A comment line (`: heartbeat`) is written every `heartbeat_seconds`, 15, so an
intermediary does not close an idle stream and a reader that went away is found. After
`lifetime_seconds`, 300, the stream sends `end` and closes; the browser opens it again after the
`retry` the stream states, 2 000 ms, through the session check like any request, so a stream never
outlives its session by more than one lifetime. An event is never replayed: a reader that opens the
stream again asks again what it shows. Nothing is buffered beyond 64 events per reader; the server
holds one database listener while at least one stream is open, and none otherwise.

**The freshness these events stand for.** The current slot of every period is rewritten by every
derivation (`docs/data-model.md`, *Refresh contract*, rule 2), and a filter-log pass runs every
`poll_interval_firewall_log_seconds`, 10 by default: a windowed answer can change after each pass,
and its `aggregate_refresh` is on the stream as soon as the pass has committed.

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
| `interface_traffic_matrix` | `volume_measure` | data | `"best_available"` |
| `traffic_over_time_by_scope` | `period` | data | `"24h"` |
| `traffic_over_time_by_scope` | `scopes` | data | `["outbound","inbound","inter_interface"]` |
| `traffic_over_time_by_scope` | `measure` | data | `"bytes"` |
| `traffic_over_time_by_scope` | `interfaces` | data | none |
| `traffic_over_time_by_scope` | `stacked` | rendering | `false` |
| `traffic_over_time_by_scope` | `volume_measure` | data | `"best_available"` |
| `interface_volume_ranking` | `period` | data | `"24h"` |
| `interface_volume_ranking` | `measure` | data | `"bytes"` |
| `interface_volume_ranking` | `scope` | data | `"all"` |
| `interface_volume_ranking` | `limit` | data | `10` |
| `interface_volume_ranking` | `include_unlabelled` | data | `true` |
| `interface_volume_ranking` | `volume_measure` | data | `"best_available"` |
| `client_volume_ranking` | `period` | data | `"24h"` |
| `client_volume_ranking` | `interfaces` | data | none |
| `client_volume_ranking` | `identity` | data | `"any"` |
| `client_volume_ranking` | `limit` | data | `10` |
| `client_volume_ranking` | `include_unstable` | data | `true` |
| `client_volume_ranking` | `measure` | data | `"bytes"` |
| `client_volume_ranking` | `volume_measure` | data | `"best_available"` |
| `client_traffic_detail` | `client` | data | required |
| `client_traffic_detail` | `period` | data | `"24h"` |
| `client_traffic_detail` | `action` | data | `"all"` |
| `client_traffic_detail` | `scope` | data | `"all"` |
| `client_traffic_detail` | `columns` | rendering | `["observed_at","destination","port","protocol","action","scope","bytes","country","operator","site_name","method","correlation_delay","interface"]` |
| `client_traffic_detail` | `limit` | data | `100` |
| `client_traffic_detail` | `volume_measure` | data | `"best_available"` |
| `traffic_composition` | `period` | data | `"24h"` |
| `traffic_composition` | `group_by` | data | `"interface"` |
| `traffic_composition` | `measure` | data | `"bytes"` |
| `traffic_composition` | `slices` | data | `6` |
| `traffic_composition` | `interfaces` | data | none |
| `traffic_composition` | `clients` | data | none |
| `traffic_composition` | `scope` | data | `"all"` |
| `traffic_composition` | `split_by` | data | `"none"` |
| `traffic_composition` | `window_seconds` | data | none |
| `traffic_composition` | `show_other` | rendering | `true` |
| `traffic_composition` | `line_interpolation` | rendering | `"linear"` |
| `traffic_composition` | `volume_measure` | data | `"best_available"` |
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
| `connection_tree` | `volume_measure` | data | `"best_available"` |
| `owner_activity` | `period` | data | `"24h"` |
| `owner_activity` | `owners` | data | none |
| `owner_activity` | `include_unassigned` | data | `true` |
| `owner_activity` | `measure` | data | `"bytes"` |
| `owner_activity` | `scope` | data | `"all"` |
| `owner_activity` | `sort` | data | `"volume"` |
| `owner_activity` | `clients_per_card` | data | `6` |
| `owner_activity` | `show_timeline` | data | `true` |
| `owner_activity` | `volume_measure` | data | `"best_available"` |
| `top_sites` | `period` | data | `"24h"` |
| `top_sites` | `interfaces` | data | none |
| `top_sites` | `clients` | data | none |
| `top_sites` | `limit` | data | `20` |
| `top_sites` | `min_flows` | data | `1` |
| `top_sites` | `group_by_registrable_domain` | data | `false` |
| `top_sites` | `volume_measure` | data | `"best_available"` |
| `sites_by_client` | `period` | data | `"24h"` |
| `sites_by_client` | `interfaces` | data | none |
| `sites_by_client` | `clients` | data | none |
| `sites_by_client` | `sites_per_client` | data | `5` |
| `sites_by_client` | `min_flows` | data | `1` |
| `sites_by_client` | `show_attribution_rate` | data | `true` |
| `sites_by_client` | `volume_measure` | data | `"best_available"` |
| `attribution_rate_per_client` | `period` | data | `"24h"` |
| `attribution_rate_per_client` | `interfaces` | data | none |
| `attribution_rate_per_client` | `clients` | data | none |
| `attribution_rate_per_client` | `limit` | data | `20` |
| `attribution_rate_per_client` | `sort` | data | `"worst_first"` |
| `attribution_rate_per_client` | `volume_measure` | data | `"best_available"` |
| `passed_traffic_world_map` | `period` | data | `"24h"` |
| `passed_traffic_world_map` | `interfaces` | data | none |
| `passed_traffic_world_map` | `scope` | data | `"north_south"` |
| `passed_traffic_world_map` | `size_by` | data | `"bytes"` |
| `passed_traffic_world_map` | `min_bytes` | data | `0` |
| `passed_traffic_world_map` | `show_unplaced` | rendering | `true` |
| `passed_traffic_world_map` | `cluster` | rendering | `true` |
| `passed_traffic_world_map` | `volume_measure` | data | `"best_available"` |
| `blocked_traffic_world_map` | `period` | data | `"24h"` |
| `blocked_traffic_world_map` | `engines` | data | `["firewall_rule","security_engine"]` |
| `blocked_traffic_world_map` | `interfaces` | data | none |
| `blocked_traffic_world_map` | `endpoint` | data | `"destination"` |
| `blocked_traffic_world_map` | `size_by` | data | `"connections"` |
| `blocked_traffic_world_map` | `min_count` | data | `0` |
| `blocked_traffic_world_map` | `show_unplaced` | rendering | `true` |
| `blocked_traffic_world_map` | `cluster` | rendering | `true` |
| `blocked_traffic_world_map` | `volume_measure` | data | `"best_available"` |
| `destination_countries` | `period` | data | `"24h"` |
| `destination_countries` | `interfaces` | data | none |
| `destination_countries` | `limit` | data | `20` |
| `destination_countries` | `series` | data | `["allowed","blocked"]` |
| `destination_countries` | `sort` | data | `"bytes"` |
| `destination_countries` | `volume_measure` | data | `"best_available"` |
| `destination_operators` | `period` | data | `"24h"` |
| `destination_operators` | `interfaces` | data | none |
| `destination_operators` | `limit` | data | `20` |
| `destination_operators` | `series` | data | `["allowed","blocked"]` |
| `destination_operators` | `group` | data | `"asn"` |
| `destination_operators` | `volume_measure` | data | `"best_available"` |
| `traffic_sankey` | `period` | data | `"24h"` |
| `traffic_sankey` | `left` | data | `"interface"` |
| `traffic_sankey` | `right` | data | `"operator"` |
| `traffic_sankey` | `scope` | data | `"all"` |
| `traffic_sankey` | `interfaces` | data | none |
| `traffic_sankey` | `measure` | data | `"bytes"` |
| `traffic_sankey` | `max_nodes` | data | `12` |
| `traffic_sankey` | `min_share` | data | `0` |
| `traffic_sankey` | `show_blocked` | data | `true` |
| `traffic_sankey` | `volume_measure` | data | `"best_available"` |
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
| `custom_chart` | `volume_measure` | data | `"best_available"` |
| `source_availability` | `kinds` | data | `["firewall_log","security_event","flow_volume","dhcp_lease","dns_lookup","ip_geolocation","measurement_sample","reconciled_state","registrable_domain","resolver_cache","resolver_local_data"]` |
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
column is returned. `depth` is 1, 2 or 3: the two levels each side of the tree holds, and on the
outside side a third, the service port and protocol below each destination.

**Destinations with no name** (C2). `top_sites` and `sites_by_client` rows carry `kind`:
`site` for an inferred name, `address` for a destination no name was inferred for, which
carries `address`, `country_code`, `asn`, `operator` and `lookup_state` (`resolved`, `miss`,
`pending` or `no_row`). Address rows are ranked with the site rows and truncated by the same
`limit` / `sites_per_client` and `min_flows`, so the rows, `remainder` and `below_min_flows`
make `attribution.eligible_flows`. Below the operator, `connection_tree` holds `site:<name>` and
`address:<address>` nodes, the latter labelled the same way; `site:none` is gone. Each
`traffic_sankey` `other.unnamed` bin lists its addresses in `members`, `[{address, volume}]`,
summing to the bin.

**Services** (C4). A service is a port and a protocol together: `traffic_composition`'s
`service_port` slices are `port:<port>/<protocol>` -- 53/udp and 53/tcp are two -- and
`port:not_recorded` for a record with no port; the `connection_tree` level below a destination
uses the same keys and carries `port` and `protocol`; `unified_blocked_feed` entries carry
`port` and `protocol` for an address target. `traffic_composition` grouped by
`blocking_reason` keys its slices `decision:<decision>` (`allowed`, `firewall_rule`,
`firewall_no_rule`, `firewall_reason_not_recorded`, `unknown`) and carries `aside`, the DNS
lists' and the detection engine's refusals counted beside the rings; `split_by: direction` adds
`rings`, one per traffic direction; `window_seconds` adds `recent`, `{seconds, from, to,
bucket_seconds, complete_until, series}`, the logged figures per slice key (and `other`) in
buckets of a sixtieth of the span, every bucket after `complete_until` -- when the filter log
last ended a pass healthy -- a gap and never a zero.

## Write endpoints

A write is a `POST` with a JSON body, made by the application with a session -- except the
setup, the sign-in and the reset, made without one (*Settings API*). It carries:

- `Content-Type: application/json`;
- the session's form token in the header `X-CSRF-Token`, the same token every page's form
  carries;
- when the browser sends one, an `Origin` naming this server's own host.

A write without a session is `401` with `{"condition": "no_session"}` or
`{"condition": "session_expired"}`. A write with no token, a wrong or another session's token,
another origin, `Sec-Fetch-Site: cross-site` or a body of another type is `403` with
`{"condition": "csrf_refused"}`. A refused write changes nothing. No `GET` changes anything.

### People and machines

The people the user creates and the machines the user assigns to them (C1 of
`specs/SPEC-backend-completion-plugins.md`; surface `internal/web/surfaces/people`). **Ownership
is assigned, never inferred**: every write names its clients by id and changes exactly those, and
nothing matches a client by its hostname, MAC, vendor or address (`docs/data-model.md`,
*`owner`*). A client with an unstable identity is assigned like any other and listed as such.
Nothing here contacts the firewall.

| Route | Body | Answer |
|---|---|---|
| `GET /api/owners` | | `{"owners": [{id, display_name, created_at, updated_at, client_count}]}`, by display name |
| `POST /api/owners` | `{"display_name"}` | `{"owner"}`: the person created |
| `POST /api/owners/{id}/rename` | `{"display_name"}` | `{"owner"}`: the person renamed |
| `POST /api/owners/{id}/delete` | `{}` | `{id, display_name, cleared: [{client, owner_assigned_at}]}`: every assignment the deletion cleared |
| `POST /api/owners/restore` | `{display_name, cleared: [{client, owner_assigned_at}]}` -- a deletion's answer, unchanged | `{owner, restored: [{client, owner_assigned_at}], not_restored: [{client, reason, owner_id}]}` |
| `POST /api/owners/{id}/assign` | `{"clients": [id, ...]}` | `{"assignments": [{client, owner_id, owner_assigned_at, changed}]}` |
| `POST /api/clients/unassign` | `{"clients": [id, ...]}` | the same |
| `GET /api/clients` | | `{"clients": [...]}`: every client, with or without traffic, by id |

- A `display_name` is trimmed; once trimmed it must be non-empty and held by nobody else, compared
  byte for byte as the schema's `UNIQUE` compares it.
- **Undo** creates the person again and restores each assignment **with its original
  `owner_assigned_at`**. A client assigned to somebody else in the meantime is listed in
  `not_restored` with `reason` `assigned_elsewhere` and the `owner_id` it now has, a client that
  no longer exists with `client_not_found`; both are left as they are.
- **Assignment** sets `owner_assigned_at` to now. A client already assigned to that person keeps its
  original instant and is answered `changed: false`, as is an unassigned client unassigned again.
- **A client of the listing** carries `id`, `identity_kind`, `identity_key`, `hostname`, `mac`,
  `last_address`, `interface` (`{id, identifier, description, user_label}` or null),
  `unstable_identity`, `owner` (`{id, display_name}` or null), `owner_assigned_at`,
  `first_seen_at` and `last_seen_at`.
- **An assigned client is never removed by the retention purge**; unassigned, it is purged as
  before (`docs/data-model.md`, *Purge*).
- **A change of ownership is derived like any other change.** Assigning, reassigning or unassigning
  a client, deleting a person and undoing it record every kept hour slot holding the clients'
  traffic; the next derivation rewrites them, and until then `freshness` reports them in
  `stale_slots` (`docs/data-model.md`, *Refresh contract*, rule 3).

A refusal is `{"condition", "client"}`, `client` present when one client is the reason: `400`
with `display_name_empty`, `display_name_taken`, `unknown_client`, `no_clients`,
`duplicate_client`, `invalid_owner_assigned_at` or `malformed_body`, and `404` with
`unknown_owner` for a person the path names who does not exist. An assignment naming one unknown
client changes no client.

### Labels and purposes

The label the user gives an interface and the purpose the user gives a blocklist (C1; surface
`internal/web/surfaces/labels`). Neither is inferred: a label is never derived from a
description, a purpose never from a list's name. Discovery never writes a label, and the purge
never removes a purpose.

| Route | Body | Answer |
|---|---|---|
| `POST /api/interfaces/{id}/label` | `{"user_label": text}`, or `null` to clear it | `{"interface": {id, identifier, description, user_label}}` |
| `GET /api/blocklists` | | `{"blocklists": [{id, name, purpose, purpose_assigned_at, first_seen_at, last_seen_at}], "purposes": [...]}` |
| `POST /api/blocklists/{id}/purpose` | `{"purpose": text}`, or `null` to clear it | `{"blocklist": {...}}` |

A label is trimmed and must be non-empty. A purpose is one of `purposes`, exactly the vocabulary of
`blocklist.purpose` (`advertising`, `tracking`, `threat`, `parental`, `other`), and
`purpose_assigned_at` is set to now; clearing it clears both. Each is seen in the next
`interface_volume_ranking` and `blocked_dns_lookups` response. A refusal is `{"condition"}`: `400`
with `user_label_empty`, `purpose_not_in_vocabulary` or `malformed_body`, `404` with
`unknown_interface` or `unknown_blocklist`.

## Settings API

Every figure and every action of the Go-rendered pages had a JSON equivalent (C5 of
`specs/SPEC-backend-completion-plugins.md`), and step 7 replaced the pages with the front's views
on these routes and removed them: their paths are now routes of the shell, so a bookmark still
lands on the same view (`specs/SPEC-step-7-frontend.md`, *Page-removal list*). Each JSON route
lives in its surface and kept **the page's validation** -- the functions the page saved with --
**the page's access level and the page's CSRF rule**: an authenticated route answers `401` with
no session, and a write is a JSON write as above. A test enumerates the routes of the five
surfaces and holds them to the table below, whose left column names the removed page route each
JSON route replaced.

**Without a session.** The setup, the sign-in and the reset are reached with no session, as their
pages are. Their form token is the one in the browser's own cookie, as a page's form compares it:
`GET /api/session` sets that cookie when there is none and answers the token, and the write
carries it in `X-CSRF-Token`. The reset is authorised by the key file, presented as the body's
`key_file` member, and refused `403` with `key_file_refused` without it. A JSON setup after an
account exists is `403` with `setup_closed`.

**Answers.** A save answers `200` with every figure of its page as it then stands, and a refusal
`400` (or the page's status) with `{"condition", "field"}`: the condition is the page's refusal as
a closed code -- its catalogue key's last segment, `error.firewall_url_invalid` being
`firewall_url_invalid` -- and `field` the body member it is about. A body that is not one JSON
object is `malformed_body`. A state the page draws is answered as the same kind of code
(`ready`, `not_attempted`, `healthy`, `selection_auto`...), an instant as epoch seconds, a duration
in seconds, and a figure the page replaces by a named state as `null` beside that state. No answer
carries a secret, a ciphertext or a session token; the form token travels in the answer of
`GET /api/session`, a setup and a sign-in only.

| Page route | JSON route |
|---|---|
| `GET /setup` | `GET /api/session` (`{account_exists, signed_in, csrf_token}`) |
| `POST /setup` | `POST /api/account/setup` |
| `GET /sign-in` | `GET /api/session` |
| `POST /sign-in` | `POST /api/session/sign-in` |
| `POST /sign-out` | `POST /api/session/sign-out` |
| `GET /recover` | `GET /api/session` |
| `POST /recover` | `POST /api/account/recover` |
| `GET /settings` | `GET /api/settings` |
| `POST /settings` | `POST /api/settings/firewall`, `POST /api/settings/firewall/certificate`, `POST /api/plugins/{name}/settings` (MaxMind, `name` `maxmind`; the Public Suffix List, `name` `publicsuffix`) |
| `GET /collection` | `GET /api/collection` |
| `POST /collection` | `POST /api/collection/kinds/{kind}` |
| `POST /collection/settings` | `POST /api/collection/kinds/{kind}` (a polled kind), `POST /api/collection/settings/{card}` |
| `GET /interfaces` | `GET /api/interface-networks` |
| `POST /interfaces` | `POST /api/interface-networks` |
| `GET /sources` | `GET /api/sources` |

**Account.**

- `POST /api/account/setup` takes `{setup_token, login, password, password_again}` and answers
  `{signed_in, csrf_token}` with the session in its cookie. Refusals: `setup_token_refused` (`403`),
  `login_required`, `password_required`, `password_too_short`, `password_mismatch` (`400`),
  `setup_closed` (`403`).
- `POST /api/session/sign-in` takes `{login, password}` and answers as the setup; both failures are
  the one `401` `sign_in_refused`.
- `POST /api/session/sign-out` deletes the session's row and answers `{signed_in: false}`.
- `POST /api/account/recover` takes `{key_file, login, password, password_again}`, refused as the
  setup is, and `key_file_refused` (`400`) for a login that does not exist; it answers
  `{signed_in: false}`.

**Settings.** `GET /api/settings` answers `{firewall}`:

- `firewall`: `url`, `api_key`, `certificate_fingerprint` (each `null` when not set),
  `credential_state` (`absent`, `ready`, `undecryptable`) and `verification`.

The appearance is the front's: `GET` and `POST /api/appearance` (*Front*, below).

A plugin's settings -- MaxMind's account ID and licence key, the Public Suffix List's -- are the
plugin's own: `GET` and `POST /api/plugins/{name}/settings` (*Plugins*, below).

| Route | Body | Refusals |
|---|---|---|
| `POST /api/settings/firewall` | `{url, api_key, api_secret, certificate_fingerprint}`, the card's whole content: an absent or empty `url`, `api_key` or `certificate_fingerprint` removes that setting, an absent or empty `api_secret` keeps the stored one | `firewall_url_invalid`, `fingerprint_invalid`, `api_key_required`; the answer's `firewall.verification` is the check the save made |
| `POST /api/settings/firewall/certificate` | `{url}`; stores nothing | `firewall_url_required`, `firewall_url_invalid`, `certificate_not_fetched`; answers `{presented_fingerprint}` |

**Collection.** `GET /api/collection` answers `{kinds, polled_kinds, settings}`:

- each of `kinds`, a sized kind the provider table holds, in the page's order: `kind`,
  `page_size_adjustable`, `source_state` (`{availability, selection}`), `in_force`
  (`{page_size, interval_seconds, page_ceiling, page_state}`), `measurement` (`window_start_at`,
  `window_end_at`, `retention_seconds` -- `null` for unlimited --, `covered_from_at`,
  `covered_to_at` -- `null` when the window holds no record --, `record_count`, `mean_rate`,
  `ingested_mean_rate`, `peak_rate` -- each `{per_second, state}` --, `gap_count`,
  `missed_seconds`, `gaps`), `suggestion` (`{state, page_size, interval_seconds}`) and
  `implementations` (`{provider_key, selection}`);
- each of `polled_kinds`: `kind`, `interval_seconds` and `implementations`, each with its
  `availability`;
- each of `settings`: `{card, settings: [{key, value}]}`, the cards `storage`
  (`retention_seconds`, 0 being unlimited, and `aggregate_mode`), `pairing`
  (`leg_pairing_window_seconds`), `lease_backends` (`lease_backend_failure_pass_limit`) and
  `attribution` (`attribution_max_delay_seconds`, `attribution_max_cache_answer_delay_seconds`).

| Route | Body | Refusals |
|---|---|---|
| `POST /api/collection/kinds/{kind}` | `{interval_seconds, page_size, selections: {provider_key: "auto" \| "on" \| "off"}}`; `page_size` only where `page_size_adjustable`, and a selection the body leaves out is left as it is | `collection_value_invalid`, `collection_interval_too_large`, `collection_kind_unknown` |
| `POST /api/collection/settings/{card}` | a member per setting of the card, a whole number of seconds or the mode's word, and `confirm` | the setting's own refusal, `collection_kind_unknown` for an unknown card; a save shortening the retention horizon without `confirm` equal to the new horizon stores nothing and answers `409` `{condition: "retention_confirmation_required", in_force_retention_seconds, requested_retention_seconds, changes: [{key, in_force, requested}]}` |

The page's fill and cancel controls store nothing and have no JSON route: the suggestion is in the
answer.

**Interfaces.** `GET /api/interface-networks` answers `{interfaces}`, each `{id, description,
identifier, device, upstream, link_local, networks, overlaps}`, a network being `{network, origin,
removed, counts, not_counting_reason}` and an overlap `{network, other, other_interface_id,
other_description}`. `POST /api/interface-networks` takes `{interface, action, network,
link_local}`, `action` one of `add`, `confirm`, `remove`, `restore` and `link_local`, `link_local`
a boolean, and refuses with `interface_unknown`, `interface_upstream`, `interface_action_unknown`,
`link_local_invalid`, `network_unknown`, `network_malformed`, `network_link_local`,
`network_ipv4_mapped` or `network_host_bits`. An edit is applied at the collector's next
derivation, as the page's.

**Sources.** `GET /api/sources` answers `{providers, outages, outage_count, recent_gaps,
recent_gap_window_seconds}`. A provider carries `kind`, `provider_key`, `display_name`,
`availability`, `outcome`, `active`, `selection`, `in_outage`, `checked_at` (`null` when never
checked), `probe` and `detail` (the collector's diagnostic text, verbatim, never read),
`opnsense_pages` (`{url, page}`, for a provider installed and not collecting: the OPNsense page
that turns it on, a change opnview never makes) and **`since`**: when its current state began, as
opnview's own checks bound it -- `{after_at, by_at, state}`, the state having begun after the
check at `after_at` and by the one at `by_at`, never stated as one instant; with both `null`,
`state` names why: `state_change_not_observed` or `state_unknown` (coverage row 39). An outage is
`{kind, provider_key}`, `provider_key` `null` for a kind's; `outage_count` is the top bar's
indicator. A gap is `{kind, provider_key, reason, start_at, end_at, missed_seconds, detail}`.

## Plugins

- **`GET /api/plugins`**, authenticated, lists every plugin discovered, built-ins first
  (`docs/plugins.md`): `{"plugins": [...]}`, each with `name`, `version`, `api_version`,
  `display_name`, `description`, `origin` (`builtin` or `data_directory`), `state` (`loaded`
  or `refused`), `reason` (`{code, detail}`, for a refused plugin), `providers` (each
  `kind`, `key`, `display_name`, `form`, `read_strategy`, cadence and `endpoints`),
  `destinations`, `tables` (each with its row count), `aside_tables`, `widgets`, `settings`
  (each declaration with its `value` and whether it is `set`; a secret's value is always
  `null`) and `events` (`schema_rebuilt`, with the aside table and its row count). It
  carries no credential, ciphertext, session token or CSRF token.
- **`POST /api/plugins/{name}/settings`**, a write, takes one JSON object of setting keys and
  values. Every key must be declared and every value within its declaration, or nothing is
  written: `400` with `{"condition", "key"}`, the condition one of `unknown_setting`,
  `invalid_setting_value`, `setting_out_of_range`, or `no_key_file` for a secret when the key
  file is lost; `{"condition": "malformed_body"}` for a body that is not one object;
  `404` with `{"condition": "unknown_plugin"}`. It answers `200` with
  `{"plugin", "settings"}`, the settings as they now stand.

## Front API

The routes the front -- the interface a browser draws (`docs/front-plugins.md`) -- reads beside the
widget routes: its registry, its strings, the appearance, the widget registry, the reference
candidates and the dashboards (`specs/SPEC-step-7-frontend.md`, A4 to A8). Every answer is JSON;
every write is a JSON write (*Write endpoints*).

### The shell, the assets and the front's registry

- **The shell.** `GET` on every path no other route answers -- `/`, `/canvas/{dashboard}/{canvas}`,
  `/settings`, `/collection`, `/interfaces`, `/sources`, `/people`, `/blocklists`, `/setup`,
  `/sign-in`, `/recover` among them -- answers the one shell document. It is public and carries no
  data; the front reads the session and routes itself.
- **`GET /front/{path}`**, public, answers an asset the front's index declares: the core's scripts
  and stylesheet, the generated palette stylesheet `palettes.css`, each theme file, the shape
  renderers, each widget's renderer entry, the basemap. Any other path answers `404`
  `{"condition": "unknown_asset"}`. The shell and every asset carry the policy of
  `docs/front-plugins.md`, *What the browser is served*, `nosniff`, `Cache-Control: no-cache` and a
  content `ETag`.
- **`GET /api/{anything}`** that no route answers is `404` `{"condition": "unknown_route"}`, never
  the shell.
- **`GET /api/front`**, public: `{themes, languages, assets}`. Each theme is `{id, name, url,
  origin}`, `name` keyed by language code with English always present, `url` the theme file's
  asset; each language `{code, name, origin}`, `name` the language's name in itself; `assets` is
  `{scripts, stylesheets}`, the URLs the shell loads, in order. `origin` is `builtin` or
  `data_directory`.

### Strings

**`GET /api/strings/{language}`**, public, answers `{language, strings}`: the merged catalogue of
the core, the surfaces, every loaded plugin, every widget directory and, for a language of the data
directory, its file. It carries an `ETag`. A key an installed language leaves out is not filled
from English here: the front falls back to English itself, then to the key marked as missing. A
language not installed is `404` `{"condition": "language_unknown"}`.

### Appearance

The appearance replaces the theme setting: `POST /api/settings/theme` and the `theme` member of
`GET /api/settings` are gone.

- **`GET /api/appearance`**, public: `{palette, mode, language, default_palette, palettes, modes,
  languages}`. The first three are the stored values, each `null` while never set -- the
  first-launch behaviour: the mode follows the operating system, the palette is `default_palette`
  (`industrial`), and the language is the browser's first installed one, else English. `palettes`
  are `{id, name}`, `modes` are `system`, `light` and `dark`, and `languages` are the entries of
  `GET /api/front`: the language picker's options. The picker is shown only when two or more are
  installed.
- **`POST /api/appearance`**, authenticated, takes any of `palette`, `mode` and `language`; a
  member set to `null` clears its value. A value not offered is refused `400` with
  `{"condition", "field"}`, the condition `palette_unknown`, `mode_unknown` or `language_unknown`,
  and nothing is written; any other member is `malformed_body`. It answers as the `GET`.

### The widget registry

**`GET /api/widgets`**, authenticated, answers `{widgets, shapes, themes, languages, refused}`:

| Member | What it holds |
|---|---|
| `widgets` | every loaded widget, served types and front-only types alike: `{type, shape, origin, plugin, backend, windowed, providers, primary, parameters, references, renderer_url, title_key, question_key, missing_strings}`. `parameters` is exactly the manifest's, plus `volume_measure` on a type declaring `volumes`; `backend` is false for a front-only widget; `renderer_url` is the widget's own renderer, or `null` when its shape's renderer draws it; `title_key` and `question_key` are its catalogue keys; `missing_strings` lists the keys of its own namespace a widget of the data directory does not carry |
| `shapes` | the core's shape renderers, `{shape, url}` |
| `themes`, `languages` | every loaded theme `{id, origin, file}` and language `{code, origin, file, missing_strings}` |
| `refused` | every widget and front part left out at start, `{part, id, code, path, detail}`, with the closed codes of `docs/front-plugins.md` and `docs/widget-authoring.md`, *Refusals* |

A front-only type is never served: `GET /api/widget/{type}` answers it `404`
`{"condition": "front_only_widget", "type": ...}`.

### References

- **`GET /api/references/{kind}`**, authenticated, lists the candidates of one reference kind of
  `docs/dashboard-format.md`, *The reference object*: `{kind, by, q, limit, candidates,
  remainder}`. `by` are the kind's accepted keys; each candidate carries `keys` -- every accepted
  `by` key with the value a reference by it would carry, or `null` -- a display `label` (data,
  never matched on), `id` for a kind naming a row, and `referenceable`. A client whose only
  identity is its address in an interface is listed with `referenceable: false` and `reason:
  "address_in_interface"`; a randomised MAC carries `unstable_identity`. The parameter `q`
  filters; at most `limit` (200) candidates are answered and `remainder` counts the rest. A `site`
  listing under `no_domains` answers `condition: "withheld_by_aggregate_mode"` and no candidate.
  An unknown kind is `404` `{"condition": "unknown_kind", "kind"}`.
- **`GET /api/references/resolve?references=<JSON array>`**, authenticated, resolves up to 200
  reference objects by the widget API's own rules (*References*) and answers `{aggregate_mode,
  references}`, one entry per object in order: `{index, kind, by, value, label, state}`, `state`
  one of `resolved`, `no_match`, `ambiguous`, `unknown_kind`, `unknown_by`,
  `withheld_by_aggregate_mode` or `not_a_reference_object`. Under `no_domains` a site's `value`
  and `label` are left out. A parameter that is not one JSON array is `400`
  `{"condition": "malformed_parameters", "reason"}` (`not_json`, `not_an_array`,
  `trailing_data`); more than 200 objects is `too_many_references`. A widget's own parameters
  are resolved through its envelope's `unresolved_references`; this route is the canvas scope's,
  and there is no second resolver.

### Dashboards

A dashboard is one document of `docs/dashboard-format.md`, stored whole in the `dashboard` table:
`id`, `position`, `document` (type `json`, so members keep their order and unknown keys their
bytes), `revision`, `origin` (`created`, `imported` or `default`), `source_name` (the imported
file's name, or `null`), `created_at` and `updated_at` (epoch seconds). Every route is
authenticated; every write is a JSON write. A document is answered as the text it was stored with,
byte for byte.

| Route | Body | Answer |
|---|---|---|
| `GET /api/dashboards` | | `{dashboards: [{id, position, title, revision, origin, source_name, canvases: [{id, title}]}], format: {current: 1, minimum: 1}}`, in position order |
| `GET /api/dashboards/{id}` | | `{id, revision, origin, source_name, document}` |
| `POST /api/dashboards` | `{document, origin, source_name}`; `origin` defaults to `created` | the dashboard created: `{id, position, revision, origin, source_name, created_at, updated_at, document}` |
| `POST /api/dashboards/{id}` | `{revision, document}` | the dashboard saved at the next revision; a `revision` that is not the stored one is `409` `{condition: "revision_conflict", revision}`, the stored one |
| `POST /api/dashboards/{id}/delete` | `{}` | the whole deleted dashboard, for an undo |
| `POST /api/dashboards/restore` | a deletion's answer, unchanged | the dashboard recreated at its own position, revision, origin and instants, under its own id when no dashboard holds it, else under a new one |
| `POST /api/dashboards/order` | `{ids}`, every dashboard's id once | `{ids}`, the new order |
| `POST /api/dashboards/default` | `{}` | `{created}`: whether this call stored the default dashboard |

**Structural validation is fail-closed** and runs on every document written, following
`docs/dashboard-format.md`, *Import validation*: a refusal is `400` `{condition, canvas_index,
widget_index, field, path}` and nothing is written. The codes are `malformed_body`,
`format_version_missing`, `format_version_too_new` and `format_version_too_old` (each with
`file_version`, `current` and `minimum`), `canvases_missing`, `canvases_empty`,
`canvas_id_missing`, `canvas_id_malformed`, `canvas_title_missing`, `widget_id_missing`,
`widget_type_and_shape_missing`, `placement_missing`, `placement_not_integer`, `duplicate_id`,
`nesting_too_deep` and `document_too_large` (above 1 MiB). An id naming no dashboard is `404`
`unknown_dashboard`. **Referential validity is never checked on save**: an unknown type, an
unresolved reference or an unknown key is stored as written.

**The default dashboard is offered once, ever** (F7-28). The front asks
`POST /api/dashboards/default` once a reader is signed in. The first ask ever claims a mark in the
settings and, when no dashboard is stored, stores the built-in default
(`front/dashboards/default.json`, portable references only) with origin `default`; every later ask
stores nothing, so deleting every dashboard never brings the default back. It is a write because
a read never changes anything.

## Coverage of the mockup

What the step-3 mockup (`docs/mockups/canvas-mockup.html`) displays, element by element: the 45 rows
of *Data the mockup displays* (`specs/SPEC-correlation-classification-aggregation.md`), then the
elements outside them (E1 to E14). Each is **served** -- by a type and its field, or by a route of this
document -- or carries the reason it cannot be, cited to OPNsense 26.7.3 through
`docs/opnsense-api-survey.md`. Rows 2, 3, 18, 20 and 24 are served by the live rate (C3 of
`specs/SPEC-backend-completion-plugins.md`), row 4's rate by the pf state table (C4); rows 19, 26 and
27 to 29 by destinations shown by address (C2); row 45, the labels and the purposes by the write
endpoints (C1). A field path is under `data` unless it names the envelope.

The envelope member `state_details`, keyed by state name (for `netflow_not_covering_window`, its
`interval_states`), the `netflow` coverage classes per direction state, and coverage per action
(allowed, blocked, unknown) on the netflow Volumes of the two map types are built, and `best_available`
judges "covered" from the stored direction states (*Volumes and the observation-point limit*).

**Every row is re-examined with push sources available** (C6 as amended). The last column names the
re-examination by one of these classes; a row whose figure exists only with an export names the
export, the field, and what is served without it.

| Class | Re-examination |
|---|---|
| `none` | The row reads nothing a push export carries: the samples of `traffic/top` and `traffic/interface`, the firewall's telemetry, discovery, the rule table, or `opnview`'s own records. No export changes it. |
| `filter-log` | The filter log may arrive by remote syslog: the `pf` plugin's push entry `filter_log` receives program `filterlog` over TCP (survey, *Push sources*, S1 to S4) and replaces the poll of `/api/diagnostics/firewall/log` while it is alive (`docs/plugins.md`, *Replacing polling*). Its records equal the polled ones in every field of the contract but the identity, so the row's fields are unchanged; a decision series' `complete_until` then trails reception rather than the poll. Without the export: polled, the same answer. |
| `netflow` | **Export-only second measure.** With a NetFlow v9 export to `opnview`'s `netflow/udp` listener (*Reporting > NetFlow*; 26.7.3 offers v5 or v9 and no IPFIX, S5), **every interface may be captured** (decision 15d): each record is joined from its own interface indexes and each direction of a connection counted once, and the row's Volumes answer in the `netflow` measure (`volume_measure` `netflow`, or `best_available` when every counted connection is covered: each direction `measured`, `in_progress`, `awaiting_export` or `missing`, as PS22 judges it): `measure: "netflow"`, `packets`, `coverage`. Served without it: the `logged` figures under `best_available`, or no figure (`data: null`) under `netflow`, with one named state: `netflow_not_configured` (no export, no active `flow_record` provider, or a capture naming no interface), `capture_configuration_unknown`, `netflow_not_covering_window` or `netflow_not_served_by_type`. A direction NetFlow cannot measure carries its named state in `coverage.direction_states` -- an egress-only interface's inbound traffic is `ingress_not_captured`, an interface the exporter cannot capture `interface_not_exported` -- and `best_available` then answers `logged`. Ingress is captured before pf and egress after it (S11): a packet pf blocks on ingress at the captured interface is exported, one it blocks on egress is not. |
| `site-names` | Site names come **only from resolver lookups** (the `unbound` plugin: `search_queries`, the resolver cache). S13 is negative: Suricata's syslog output carries type `alert` only, so `tls.sni` and `http.hostname` cannot be received over syslog on 26.7.3, and no push source names a site. |
| `unbound-log` | Unbound's query and reply lines (S8) reach a syslog target only with no program filter (S3) and carry **no verdict and no blocklist**. They are stored as the dataset `unbound.query_log` (provider kind `unbound_query_log`) and replace nothing: refusals, lists and lookups still come from the polled `search_queries`. |
| `suricata` | Suricata can send **alerts only** to syslog, as eve JSON, program `suricata` (S7). Receiving them is deferred to step 6 (decision 15c); nothing is built. Without it: the polled `query_alerts`. |

| # | Item | Served by (type and field), or why not | Push |
|---|---|---|---|
| 1 | canvas period 1 h / 24 h / 7 d / 30 d | **Served.** Every windowed type: `parameters.period`; envelope `window` (`requested`, `covered`, `coverage`, `flow_horizon_at`) | `none` |
| 2 | current throughput, inbound / outbound / between interfaces | **Served** (C3). `traffic_composition`: `current_rates.by_direction[]` (`key`, `rate`), `current_rates.outbound_unsplit`; each rate `{state, measure: "sampled", in_bits_per_second, out_bits_per_second, sampled_at, span_seconds, cadence_seconds, lower_bound}` (*Live figures*) | `none`. NetFlow is no rate: a record is exported when its flow ends or at the active timeout (default 1800 s; inactive 15 s, S5) |
| 3 | the same by interface or by client | **Served** (C3). `traffic_composition` with `group_by` `interface` or `client`: `current_rates.by_member[]` | `none`, as row 2 |
| 4 | throughput by protocol or by service port | **Served.** Logged volume by protocol and by service, port and protocol together (C4): `traffic_composition` with `group_by` `protocol` or `service_port`, `slices[]` (`key` `port:<port>/<protocol>`, `protocol`, `port`), and `recent` with `window_seconds`. **The current rate by protocol and by port** (C4), from the pf state table, not from `traffic/top`, which runs iftop without `-P`: the `pf_states` plugin's dataset `pf_states.rate` (`GET /api/datasets/pf_states.rate/query`; `sampled_at`, `span_seconds`, `interface_device`, `direction`, `protocol`, `port`, `forward_bits_per_second`, `reverse_bits_per_second`, `bits_per_second`, `states`), filtered `direction` `eq` `in` so a forwarded connection counts once, split by `protocol` or by `port` with `protocol`: the `sampled` measure, a lower bound, over the provider's cadence (60 s by default) (*Live figures*, *Rate by port and protocol*; survey, *Live rate endpoints, read from source for C3*). **In the widget itself:** `traffic_composition` with `group_by` `protocol` or `service_port` fills `current_rates.by_member[]` from the latest pass of `pf_states.rate`, filtered `direction` `eq` `in` and read by its catalogue name through `host.datasets.latest` (`docs/plugins.md`, *The widget host*): one `{key, rate}` per member, `key` the slice's own (`protocol:<protocol>`, or `port:<port>/<protocol>` and `port:not_recorded`), `rate` a live figure as a client's or an interface's, `{state, measure, in_bits_per_second, out_bits_per_second, out_measured, sampled_at, span_seconds, cadence_seconds, lower_bound}`, summed over the pass's interfaces -- `out_bits_per_second` the forward counter, from the connection's initiator, `in_bits_per_second` the reverse one, back to it, `out_measured` true, `cadence_seconds` the pass's `span_seconds` (*Rate by port and protocol*) -- `current`, or `stale_sample` when the pass is older than twice its `span_seconds`. `current_rates.by_member_source` `{dataset, state, at, condition}` names where it was read: `unknown_dataset` (no `pf_states` plugin; the data catalogue API's refusal code for that name) or `no_rows` (no pass within the dataset's horizon) is the named absence, `by_member` then empty, never a rate of 0 | `netflow`: the same slices in the netflow measure (`slices[].volume`, `packets`); and the dataset `flow_record` (`GET /api/datasets/flow_record/query`, `split_by` `protocol` or `dst_port`, `octets` and `packets` per bucket; filter `join_state` `eq` `joined` so no copy counts a packet twice). Still not a current rate: a record is dated by its start, at one-second resolution (S10), and arrives after its flow ends or at the active timeout (S5). Without the export: the logged slices; `flow_record` holds no row |
| 5 | packets by blocking reason, per direction | **Served.** `traffic_composition` with `group_by` `blocking_reason` and `split_by` `direction`: `rings[]` (`direction`, `slices[]` keyed `decision:<decision>`, `other`, `total`), `aside[]`; `traffic_over_time_by_scope`: `series[].buckets[].volume` (allowed / blocked / unknown). The logged measure counts records, not packets | `filter-log`; `netflow`: a packet count is `slices[].volume.packets` in the netflow measure only, and its blocked part holds only what pf refused after an ingress capture (S11) |
| 6 | refused lookups beside the rings | **Served.** `traffic_composition` (`blocking_reason`): `aside[]` (`dns_list`, `count`); `blocked_dns_lookups`: `total`; `blocked_traffic_world_map`: `dns_blocked_lookups` | `unbound-log` |
| 7 | detection-engine figures | **Partly.** State: `source_availability` (`security_event`), `traffic_composition` `aside[]` (`security_engine`, `condition`). Refusals: `blocked_traffic_world_map.security_events`, `.security_unplaced`; `unified_blocked_feed` (`engine_kind` `security_engine`, `decided_by.kind` `signature`). Figures by signature, over time and by client: **step 6** -- `security_alerts_over_time`, `alerts_by_signature`, `alerts_by_client_and_interface` answer `unknown_widget_type`; the target interface is gap G7, the covered interfaces G8 (`docs/widget-catalogue.md`, *Gaps found*) | `suricata` |
| 8 | rolling strip at 2-second marks | **Served** (C3). `GET /api/live/strip`: `throughput[]` (`outbound`, `inbound`, `inter_interface`, `outbound_unsplit`; sampled) and `decisions[]` (`allowed`, `firewall_rule`, `firewall_no_rule`, `firewall_reason_not_recorded`, `dns_refusal`), each with `complete_until`, buckets after it gaps; `window_seconds`. **2 s** is the floor, one `traffic/top` capture (survey, *Live rate endpoints*); the default live cadence is 10 s pending LV-C, so 2 s marks need the `insight` plugin's `live_cadence_seconds` set to 2 | `filter-log`: the decision series is complete to reception; `unbound-log`: `dns_refusal` stays polled; `netflow`: no rate, as row 2 |
| 9 | "Port not recorded" | **Served.** `traffic_composition` with `group_by` `service_port`: slice `port:not_recorded` | `filter-log`; `netflow` (the same key in the netflow measure) |
| 10 | totals outbound / inbound / between interfaces | **Served.** `traffic_over_time_by_scope`: `series[].total`, `series[].total_volume` | `filter-log`; `netflow` |
| 11 | buckets per period | **Served.** `traffic_over_time_by_scope`: `bucket_seconds`, `series[].buckets[]` (`from`, `to`, `gap`, `value`, `volume`) | `filter-log`; `netflow` |
| 12 | label, VLAN tag, identifier, device | **Served.** `interface_volume_ranking`: `bars[].interface` (`label`, `user_label`, `labelled`, `vlan_tag`, `identifier`, `device`); the label is written by C1 (E11) | `none` |
| 13 | bytes east-west / north-south, share, connections, blocked | **Served.** `interface_volume_ranking`: `bars[].east_west`, `.north_south`, `.volume` (`bytes`, `connections`, `blocked_connections`), `.value` over `total` | `filter-log`; `netflow` |
| 14 | clients seen of clients known | **Served.** `interface_volume_ranking`: `bars[].clients_seen`, `.clients_known` | `filter-log` |
| 15 | "N rules do not log" | **Served.** `interface_volume_ranking`: `bars[].rules_not_logging`; `floating_rules_not_logging`, `unresolved_rules_not_logging`, `rules_logging_not_reported` | `none` |
| 16 | hostname, MAC, address, unstable badge, interface, bytes, blocked, destinations | **Served.** `client_volume_ranking`: `bars[].client` (`hostname`, `mac`, `last_address`, `unstable_identity`), `.interface`, `.volume` (`bytes`, `blocked_connections`), `.destinations` | `filter-log`; `netflow` |
| 17 | IPv4, IPv6, carrying interface, gateway, last changed | **Served.** `public_address`: `upstreams[]` (`interface`, `current[]` with `address`, `family`, `changed_at` or `change_state`, `gateways[]`, `history[]`, `behind_upstream_nat`). Behind an upstream NAT the public address is not visible from the firewall | `none` |
| 18 | per person: clients, blocked, current rate, bytes, connections, interfaces | **Served** (C3). `owner_activity`: `cards[].owner`, `.machines`, `.volume` (`bytes`, `connections`, `blocked_connections`), `.current_rate` (the person's rate: the sum over every machine of the card, those past `clients_per_card` included, *Live figures*), `.current_rate_clients` (the machines it sums), `.interfaces` (the interface ids of every machine of the card), `.chips[]`, and `.chip_remainder` (`members`, `volume`, `current_rate`, `chips[]`: each machine folded past `clients_per_card` as the chip it would have been) | `netflow` (the volumes); the rate is `none` |
| 19 | "Reaching": site name, else operator, else address | **Served** (C2). `connection_tree` with `root` `owner` and `owners` naming the person: `outside[]` (operator `operator:<asn>` with `operator`, or `unplaced:<lookup_state>`), below it `site:<name>` (`site_name`) or `address:<address>` (`address`, `lookup_state`, `country_code`, `asn`, `operator`) | `site-names`; `netflow` |
| 20 | per-client chip: address, MAC, rate or "no data in the period" | **Served** (C3). `owner_activity`: `cards[].chips[]` (`client.last_address`, `client.mac`, `current_rate`, `state` `measured` or `no_data_in_period`, `volume`) | `none`; `netflow` (the chip's `volume`) |
| 21 | passed map, per placed peer: allowed bytes and connections, interfaces, coordinates, operator, country, AS | **Served.** `passed_traffic_world_map`: `points[]` (`address`, `geo` with `latitude`, `longitude`, `country_code`, `country_name`, `asn`, `operator`; `volume`, `interfaces`, `size`), `below_minimum`, `total` | `filter-log`; `netflow`, with coverage per action |
| 22 | unplaced volume (`miss`, `pending`, no row, east-west) | **Served.** `passed_traffic_world_map`: `unplaced[]` (`reason` `miss`, `pending`, `no_row`, `no_coordinates`, `east_west`, `this_firewall`; `addresses`, `volume`) | `filter-log`; `netflow` |
| 23 | blocked map: outbound by destination, unplaced, between interfaces, inbound by source | **Served.** `blocked_traffic_world_map` with `endpoint` `destination` or `source`: `points[]` (`engine`, `geo`, `volume` or `events`), `unplaced[]` (`east_west`, `other_endpoint` and the lookup states), `security_unplaced[]`, `total_refusals` | `filter-log`; `netflow`, with coverage per action, the blocked part as row 5; `suricata` |
| 24 | client current rate, and the share per destination | **Served** (C3). Current rate: `client_traffic_detail`: `current_rate`. Share per destination: over the period, `records[].bytes` over `total`; now, `current_rate_by_peer[]` (`peer`, `rate`), keyed by the address `records[].destination` carries: the per-pair readings of `traffic/top`, which the core exposes to every widget as `host.rates.clientPeers(id)` and `host.rates.byPeer()` (*Live figures*). No other destination type is drawn with a rate in the mockup | `none` (the rate); `netflow` (the bytes, as row 25) |
| 25 | `local ⇄ peer:port`, connections, bytes | **Served.** `client_traffic_detail`: `records[]` (`destination`, `port`, `protocol`, `action`, `scope`, `bytes`, `observed_at`, `interface_device`), `total.members`, `remainder` | `filter-log`; `netflow`: `records[].bytes` is exact (`lower_bound: false`) for a connection measured in both directions, and `records[].directions` carries `forward` / `reverse` `bytes`, `packets`, `state`, `counted_copy`, and `records[].coverage` its coverage object, or `null` directions with no readable, enabled capture configuration at its start |
| 26 | connection tree, children summing to their node | **Served** (C2). `connection_tree`: `inside[]`, `outside[]` (`children`, `remainder_members`, `bytes`, `connections`, `blocked_connections`; `site:` and `address:` nodes, the service level at `depth` 3), `total` | `site-names`; `netflow` |
| 27 | top sites: attribution rate, named / outbound flows; per site flows, clients, bytes, dominant operator | **Served** (C2). `top_sites`: `rows[]` (`kind` `site` or `address`; `name` or `address`, `flows`, `clients`, `client_ids`, `bytes`, `country_code`, `asn`, `operator`, `methods`), `attribution`, `remainder`, `below_min_flows`, `total`; withheld whole under `no_domains` | `site-names`; `netflow` (`bytes`) |
| 28 | collapse to the registrable domain | **Served** (C2). `top_sites` with `group_by_registrable_domain`: `grouping`, `rows[].site_names`; state `registrable_domain_grouping_unavailable` | `site-names` |
| 29 | top sites per client | **Served** (C2). `sites_by_client`: `rows[]` (`client`, `state`, `sites[]` as `top_sites` rows, `remainder`, `below_min_flows`, `attribution`) | `site-names`; `netflow` |
| 30 | outbound flows, named, rate, mean and maximum delay | **Served.** `attribution_rate_per_client`: `rows[]` (`eligible_flows`, `named_flows`, `rate`, `rate_state`, `mean_delay_seconds`, `max_delay_seconds`), `resolver_covered`, `cache_covered` | `site-names`; `netflow` (`eligible_bytes`) |
| 31 | AS, operator, bytes, allowed, blocked, peers, interfaces, "no geolocation answer" | **Served.** `destination_operators`: `rows[]` (`asn`, `operator`, `bytes`, `allowed`, `blocked`, `peers`, `interfaces`), `unresolved[]` | `filter-log`; `netflow` |
| 32 | Sankey: interface to interface / operator / "no geolocation answer", Other band | **Served.** `traffic_sankey`: `nodes[]`, `ribbons[]` (`value`, `bytes`, `connections`, `blocked_share`), `other` (`ribbons`, `bytes`, `connections`, `unnamed[]` listing `members`) | `filter-log`; `netflow` |
| 33 | matrix: bytes, allowed, blocked, rules; totals; outside column | **Served.** `interface_traffic_matrix`: `cells[]` (`row`, `column`, `value`, `volume`, `rules[]` with `description`, `rid`, `state`, `connections`), `rows[]`, `columns[]` (`outside`, `north_south`, `this_firewall`), `total` | `filter-log`; `netflow` |
| 34 | blocked feed: instant, engine, target, decided by, run count, "→ this firewall" | **Served.** `unified_blocked_feed`: `entries[]` (`occurred_at`, `engine_kind`, `decided_by`, `target`, `target_kind`, `port`, `protocol`, `interface`, `client_id`, `target_is_this_firewall`); repeated targets as `group_by` `target`, `groups[].count` | `filter-log`; `unbound-log`; `suricata` |
| 35 | totals per engine | **Served.** `unified_blocked_feed`: `counts_by_engine_kind`, `sources[]`, `total` | `filter-log`; `unbound-log`; `suricata` |
| 36 | rule table: description, pf label, deleted rule with its `rid`, interface fired on, interfaces and clients, blocked, sparkline | **Served.** `blocked_by_firewall_rule`: `rows[]` (`rule`, `rid`, `state` `rule_not_found` for a deleted rule, `fired_on`, `interfaces`, `clients`, `blocked_connections`, `last_seen_at`, `timeline`) | `filter-log` |
| 37 | blocked DNS lookups: domain, list, purpose, clients and interfaces, refused count | **Served.** `blocked_dns_lookups`: `rows[]` (`domain`, `list`, `purpose`, `lookups`, `clients`, `unmatched_client_addresses`, `interfaces`, `last_seen_at`); the purpose is written by C1 (E11) | `unbound-log` |
| 38 | kind, provider, availability, active, checked, detail | **Served.** `source_availability`: `providers[]` (`kind`, `provider_key`, `display_name`, `active`, `state`, `outcome`, `condition`, `checked_at`, `detail`, `since`); `GET /api/sources` | A push provider is listed like any other, its availability `listener_not_configured`, `no_message_received` within its liveness span, or healthy (`docs/plugins.md`, *Liveness and gaps*); `GET /api/firewall/catalogue` `push` |
| 39 | "detection engine stopped at 06:12" | **Served as a bounded interval.** `source_availability`: `providers[].since` `{after_at, by_at, state}` (`GET /api/sources` the same): the state began after the check at `after_at` and by the one at `by_at`, `opnview`'s own probes; with both `null`, `state` is `state_change_not_observed` or `state_unknown`. **Never one instant**: `/api/ids/service/status` answers a status and not when it began (survey, *Data source 2*, *Degradation*) | `suricata`: an alert stream would carry no stop event either |
| 40 | uptime, CPU, memory, disk, temperature, history | **Served.** `firewall_health_overview`: `tiles[]` (`metric`, `state`, `value`, `unit`, `last_sampled_at`, `series`, `threshold_state`) | `none` |
| 41 | swap; gateway latency and loss | **Served.** `firewall_health_overview`: metrics `swap`, `gateway_latency`, `gateway_loss` | `none` |
| 42 | bit/s, bytes, packets | **Served.** `interface_throughput`: `series[]` (`interface`, `direction`, `measure` `interface_counter`, `unit`, `points`, `resets`) | `none`: NetFlow is never substituted for an interface counter |
| 43 | errors | **Served.** `interface_throughput` with `measure` `errors` | `none` |
| 44 | custom chart: throughput, volume and CPU per bucket | **Served.** `custom_chart`: `series[]` (`interface_throughput` `bits_per_second`, `traffic` `bytes`, `firewall_metric` `cpu`) | `netflow`: `traffic` measures `netflow_bytes`, `netflow_packets`; any dataset with a time column, `flow_record`, `unmatched_record` and `unbound.query_log` included (*Series of `custom_chart`*) |
| 45 | People page: owners, client counts, identity, unstable badge, assignment date | **Served** (C1). `GET /api/owners` (`client_count`), `GET /api/clients` (`identity_kind`, `identity_key`, `unstable_identity`, `owner`, `owner_assigned_at`, `interface`), and the writes of *People and machines* | `none` |

**Outside the 45 rows.**

| # | Element | Served by, or why not | Push |
|---|---|---|---|
| E1 | the dashboard's title in the header | **Canvases.** The dashboard document's `title` (`docs/dashboard-format.md`); canvases have their own spec | `none` |
| E2 | the live indicator and its clock | **Served.** `GET /api/live/strip`: `live_interest_until`; every live figure's `sampled_at`. The clock itself is the browser's | `filter-log`, as row 8 |
| E3 | palette and mode selectors | **Served** by the front: `GET` and `POST /api/appearance` (`palette`, `mode`, `language`), a theme being a file (specs/SPEC-step-7-frontend.md, A5) | `none` |
| E4 | canvas tabs, rail, range selector, layout editing, dashboard editor, import report, widget picker | **Canvases.** Their own spec and `docs/dashboard-format.md`. What they read here: the types, `GET /api/plugins` (`plugins[].widgets`); the parameters, *Parameters*; an import's unresolved references, each response's `unresolved_references` | `none` |
| E5 | a card's header: condition, scope, own range, "kept, not understood" | **Served.** Envelope `condition`, `availability`; `parameters` (effective, `period` among them); `unknown_parameters`; `unresolved_references` (`path`, `kind`, `by`, `value`, `label`, `reason`). "Unapplied" and "cut off at this size" are the frontend's | `none` |
| E6 | state panels: source unavailable, switched off, no data in the period, reference not found, not in the model | **Served.** Conditions `unavailable` and `not_yet_probed`, `present_but_disabled`, `reachable_no_rows`, `unresolved_reference`; "not in the model" is `unknown_widget_type` for a step-6 preset; the open gaps, G7, G8 and G12, are named in `docs/widget-catalogue.md`, *Gaps found* | `none` |
| E7 | a person's detail: now, period total, connections, blocked, interfaces, per-client now and period, destinations | **Served.** As rows 18 to 20: `owner_activity` `cards[]`: now `current_rate`, the period total, connections and blocked `volume`, `interfaces`; per client now and period, every machine: `chips[]` and `chip_remainder.chips[]` (`client`, `current_rate`, `state`, `volume`); the destinations from `connection_tree` (`root` `owner`, `owners` the person, `limit_per_level`, `remainder_members`) | `netflow`; `site-names` |
| E8 | a slice's detail: value, share of the ring, the ring's total, what it is made of | **Served.** `traffic_composition`: `slices[].value` over `total`, or `rings[].slices[]` over `rings[].total`; per direction with `split_by` `direction`; a "now" figure from `current_rates.by_member[]`. Other's make-up: `other.slices[]` and `rings[].other.slices[]`, each member folded into Other as the slice it would have been, in rank order, summing to `other.volume` | `filter-log`; `netflow` |
| E9 | a destination's detail: address, country, coordinates, AS, bytes, allowed, blocked, engine, interfaces | **Served.** `passed_traffic_world_map.points[]` and `blocked_traffic_world_map.points[]`: `address`, `geo` (`country_name`, `country_code`, `latitude`, `longitude`, `asn`, `operator`), `volume`, `engine`, `interfaces`. The geolocation holds no city | `filter-log`; `netflow`, with coverage per action |
| E10 | the People page's add, remove and assign controls | **Served** (C1). *People and machines*: `POST /api/owners`, `POST /api/owners/{id}/delete`, `POST /api/owners/restore`, `POST /api/owners/{id}/assign`, `POST /api/clients/unassign` | `none` |
| E11 | interface labels and blocklist purposes | **Served** (C1). *Labels and purposes*: `POST /api/interfaces/{id}/label`, `GET /api/blocklists`, `POST /api/blocklists/{id}/purpose`; read back in rows 12 and 37 | `none` |
| E12 | the "alerts by signature" card | **Step 6.** `alerts_by_signature` answers `unknown_widget_type`; its "stopped at" line is row 39 | `suricata` |
| E13 | the "telemetry: no provider kind" counter | **Superseded.** `measurement_sample` is a kind with providers (G9 and G10 closed), listed in `source_availability.providers[]` | `none` |
| E14 | the import example's `client_activity_timeline` | **Unknown by design.** `404` `unknown_widget_type`; no such type exists | `none` |

The mockup draws no card for `destination_countries`, `aggregate_freshness` or `unresolved_joins`; they are
served and reach no row.
