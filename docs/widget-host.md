# The widget host

What a widget plugin's script is handed: one object per area of the core, `host.<area>.<function>`.
It is the reference `docs/plugins.md` cites under *Widgets*. Each area is a file of the core
(`internal/widget/hostareas/<group>/host_<area>.go`), and a test of the repository fails when an area
has no section here, or when a section names an area the host does not have.

It grants the widget's own
statements, the figures every widget shares, and the envelope; it grants no firewall call, no fetch
and no write. A function called wrongly -- a statement the widget does not declare, a parameter the
statement does not name, an argument of the wrong type -- throws a `TypeError`.

#### `host.request`

| Member | What it is |
|---|---|
| `type` | The widget's type. |
| `now` | The instant the request is answered at, UTC epoch seconds. |
| `window` | The requested window `{from, to}` of a windowed widget, `null` otherwise. |
| `aggregate_mode`, `no_domains` | The `aggregate_mode` setting in force (`full` or `no_domains`), and whether it withholds domain names. |

#### `host.params`

The effective parameters, after the core parsed and defaulted them.

| Function | What it answers |
|---|---|
| `text(key)` | An enum's value. |
| `flag(key)` | A boolean. |
| `integer(key)`, `number(key)` | A number. |
| `set(key)` | An `enum_set`'s or `string_list`'s members. |
| `has(key, value)` | Whether an `enum_set` holds a value. |
| `effective()` | Every effective parameter, as the response echoes them. |

#### `host.refs`

The references the core resolved. One that matched nothing has already made the answer
`unresolved_reference` (or failed its own series entry), so a script reads only what resolved.

| Function | What it answers |
|---|---|
| `ids(key)` | The row ids a reference key resolved to, or `null` for none, which means "no restriction". |
| `texts(key)` | The texts it resolved to (a country code, a site), or `null`. |
| `entry(index, key)` | `{id, text}` of one series entry's reference, or `null`. |
| `failed(index)`, `withheld(index)` | Whether a series entry holds a reference that did not resolve, and whether that is a site the aggregate mode withholds. |
| `jsonIds(ids)`, `jsonTexts(texts)` | A list as the JSON text a statement's set parameter takes, `null` for `null`. |

#### `host.query`

| Function | What it answers |
|---|---|
| `rows(name, parameters, period)` | Every row of a statement the widget declares, each an object of its columns. `period` names the period of a template and is left out otherwise. Under a declared measure, a statement whose twin `<name>_measured` the widget declares is run as its twin, with the measure's name bound to `:measure` (`docs/plugins.md`, *Volume figures and their measure*). |
| `row(name, parameters, period)` | Its first row, or `null`. |
| `carriesDomain(name)` | Whether the statement reads a column carrying a domain name. |

A bound value is a number, a string, a boolean, `null`, or an array, bound as its JSON text. A cell
is a number, a string, a boolean or `null`, as PostgreSQL types the column: a `numeric` whose value is
whole -- an integer sum is one -- is an integer and any other `numeric` a number with a fraction, so a
script never meets PostgreSQL's numeric typing; a comparison and a `BOOLEAN` column are booleans, and
`CAST(x AS INTEGER)` reads one as `0` or `1`. The connection itself converts nothing: this is the
plugin host's conversion, made for a plugin's and a widget's statements only.

#### `host.envelope`

| Function | What it does |
|---|---|
| `state(code)` | Records a named state beside the data (`docs/widget-api.md`, *Named states*). |
| `withhold(path)` | Records a field or level the aggregate mode withheld, by its path. |
| `override(condition)` | Replaces the computed condition by the widget's own; `no_owner_exists` answers no data. |
| `cover(interval)` | Records the covered part of the window, `null` for none. |
| `coverFrom(earliest)` | Records coverage from the earliest record a source holds to now; `null` when it holds none. |
| `coverFlows(state)`, `coverFlowsState(state)` | Records the coverage of a reading of the flows alone, and the named state when part of the window lies past the retention horizon (`""` for none). |
| `earliest(name)` | The instant one of the core's `widget_earliest_*` statements reads, or `null`: `widget_earliest_flow`, `widget_earliest_lookup`, `widget_earliest_security_event` and `widget_earliest_sample` are the oldest record each source kind keeps. |
| `availability()`, `gaps()` | The availability and the gaps the response carries. |
| `availabilitySince()` | When each registered provider's current state began, by `"<kind>/<provider key>"`: `{after_at, by_at, state}`, the state began after `after_at` and by `by_at` -- the two checks of opnview's own that bound the change, never an exact instant -- or, with both `null`, `state` naming why: `state_change_not_observed` (no check has seen the state begin) or `state_unknown` (no availability row). |
| `readGaps(kinds, window)` | The gaps of other kinds or another window. |
| `kindCondition(kind)` | The condition of a provider kind: its active providers' best. |
| `hourFreshness(from, to)` | Records the freshness of the 1 h slots of `[from, to)`. |
| `conditions`, `coverage` | The condition codes and the coverage values, by name. |

#### `host.figures`

The shapes of the figures every widget writes, so a script writes them exactly as the core does.
`opnview` sees only what crosses the firewall, so a byte or connection figure is a lower bound and
says so beside its measure.

| Function | What it answers |
|---|---|
| `volume(figures)` | The Volume of summable figures, in their own measure: `{measure, lower_bound, bytes, allowed_bytes, blocked_bytes, unknown_bytes, connections, allowed_connections, blocked_connections, unknown_connections}`, and for a netflow Volume its packets and its coverage, whole and per part: `packets`, `allowed_packets`, `blocked_packets`, `unknown_packets`, `coverage`, `allowed_coverage`, `blocked_coverage`, `unknown_coverage`, each coverage `{measured, partial, unmeasured, capture_not_known, direction_states, counted_copy}` (`docs/widget-api.md`, *Volumes and the observation-point limit*). |
| `logged(figures)` | The same as `volume`: the name it had while every figure was logged. |
| `count(value)` | A count of the connections or records the answer's figures count, `{measure, lower_bound: true, value}`, in the measure the request is answered under. |
| `byteCount(figures)` | The Count of the figures' bytes, in their measure. |
| `connectionBytes(figures)` | The same for one connection's figures. A netflow figure of one connection measured in both directions is exact, and is the only figure whose `lower_bound` is `false`. |
| `cover(figures)` | Says the answer counts figures whose numbers the script uses directly, rather than through a Volume or a Count, so the core judges them as it judges a Volume. |
| `loggedCount(value)` | `{measure: "logged", lower_bound: true, value}`: a count of logged records, which an answer in the netflow measure cannot carry. |
| `withMeasure(figures, from)` | Figures carrying the measure of `from` -- a row or figures read in it: its measure and its netflow parts. |
| `zero()`, `add(a, b)` | Summable figures holding nothing, in the measure the request is answered under, and the sum of two. |
| `value(measure, figures)` | The figure a `measure` names: `bytes`, `connections`, `allowed`, `blocked`, or `packets` of a declared measure's figures. |
| `remainder(members, volume, count)` | What a truncating parameter dropped: `{members, volume, count}`, a `null` volume or count left out. `count` is a Count built by this area, or a number, read as `loggedCount` reads one. |
| `compare(a, b)` | `-1`, `0` or `1`: two strings in byte order, the order every key of the core is ranked in. JavaScript's `<` compares UTF-16 code units and differs outside ASCII. |
| `nameFreeCompare(a, b)` | The tie-break between rows whose name is withheld: by a hash of the name, which says nothing about it. |
| `interfaceKey(id)`, `clientKey(id)` | A member key, `interface:<id>` or `client:<id>`. |
| `measures` | The measures, by name: `logged`, `sampled`, `interface_counter` and every declared measure. |
| `declared` | The declared measures' names, in name order (`docs/plugins.md`, *Measures*). |
| `measure` | The measure the request is answered under: `logged` or a declared measure. |

Summable figures are `{bytes, allowed_bytes, blocked_bytes, unknown_bytes, allowed_connections,
blocked_connections, unknown_connections}`; a member left out is `0`. Figures of a declared measure
also carry `measure`, its name, and `measure_parts`, its parts as one array (`docs/plugins.md`, *Measures*): for each part
`allowed`, `blocked` and `unknown`, its bytes, its packets, its four coverage classes, one count per
declared direction state and one per declared marker. A script never needs to read them:
`host.figures.add` and `host.figures.zero` keep them, and a Volume handed back is read as its figures,
its parts included.

#### `host.volume`

| Function | What it answers |
|---|---|
| `window()` | The rolling window of volume, in the measure the request is answered under, `{requested, covered, rows}`, assembled from the 1 h slots and, at either edge, the flows. Each row is `{src_interface_id, dst_interface_id, peer_address, traffic_scope, traffic_direction, exit_leg_device}` and its summable figures. Reading it records the window's coverage and the freshness of the slots read. |
| `total(rows)` | The summable figures of every row that counts a connection: every row but a second leg, so a connection logged on two interfaces counts once. |
| `localInterface(row)` | The inside interface a row belongs to, or `null`. |
| `scopeMatches(scope, row)` | Whether a row is in a traffic scope; `all` holds every row. |
| `directions`, `scopes` | The traffic directions and scopes, by name. |

#### `host.buckets`

| Function | What it answers |
|---|---|
| `width()` | The bucket width of the request's period, in seconds. |
| `intervals(window, width)` | The buckets of a window, each clipped to it. |
| `floorMod(value, width)` | The non-negative remainder. |
| `isGap(bucket, covered, gaps)` | Whether a bucket lies outside the covered interval or meets a gap: a bucket the core draws as a gap, never a zero. |

#### `host.periods`

| Function | What it answers |
|---|---|
| `names()` | The aggregate periods, finest first: `["1h", "24h", "7d", "30d"]`. |
| `slotStart(name, t)`, `slotEnd(name, start)` | The slot of a period holding an instant, and where a slot ends. |

#### `host.installation`

What an answer names its members by. Every label is the user's own where one is set and the
firewall's discovered description otherwise.

| Function | What it answers |
|---|---|
| `interfaces()`, `interface(id)` | The interfaces, or one: `{id, identifier, device, description, user_label, label, labelled, link_kind, vlan_tag, upstream}`. |
| `client(id)` | One client, or `null`. |
| `owners()` | The people somebody created: `{id, display_name}`. |
| `rule(id)` | One firewall rule, or `null`. |

#### `host.services`

What the two service kinds answered, as stored. A widget never downloads or looks anything up.

| Function | What it answers |
|---|---|
| `registrable(names)` | `{available, groups}`: each name's registrable domain, by the list in use; `available` is `false` while none is held. |
| `geolocation(address)` | The stored IP geolocation of an address: `{lookup_state, country_code, country_name, latitude, longitude, asn, operator, dataset_build_at}`; `lookup_state` is `resolved`, `miss`, `pending`, or `no_row` for an address never looked up. |

A widget that places many peers costs no statement a peer. Every outside peer the request has
read -- the rows of `host.volume.window()` and `host.flows.destinations()`, and the `peer_address`
column of any row your own statement returns through `host.query` -- is *placed*, and the first
`geolocation` call reads the geolocations of every peer placed so far in one statement; the calls
after it are answered from what that read. Name the column that holds a peer's address
`peer_address` in your statement and your widget is served the same way.

#### `host.settings`

| Member | What it is |
|---|---|
| `get(key)` | A setting of `opnview`'s configuration, as stored, or `null`. Only the keys in `keys` are read: a setting row can hold a credential, and no widget reads one. |
| `keys` | The keys a widget may read. |

#### `host.json`

| Function | What it does |
|---|---|
| `map(object)` | Marks an object as a map, written with its keys sorted; answers the object. |
| `encode(value)` | The JSON the host would write for a value. |

#### `host.sites`

The inferred site names of the window, the figures every widget ranking sites is built on. Inside
the flow horizon they come from the attributions, which carry each name's method; for the whole
hours the retention purge has already removed, from the domain family's 1 h slots, which keep the
totals and not the method.

| Function | What it answers |
|---|---|
| `rows()` | The window's site figures over its eligible flows (the attribution rate's rule), filtered by the request's `interfaces` and `clients` references: `[{site, client_id, interface_id, method, country_code, asn, operator, flows, bytes, delay_sum, delay_max}]`. `method` is `resolver_cache_answer`, `lookup_timing`, or `not_recorded_beyond_flow_horizon` for a row read from the slots, whose delays are `null`. Reading them records the window's coverage, and the named state `site_methods_not_recorded_beyond_flow_horizon` when slots were read. |
| `accumulate(name, rows)` | The table row a list of those rows sums to, named `name`: `{kind, name, flows, bytes, clients, client_ids, methods, mean_delay_seconds, max_delay_seconds, country_code, asn, operator}`, `kind` being `site`. `methods` is the named flows per method; the delays are `null` when no row was timed; the country and operator are those that carried most of the row's bytes, `null` when none of its destinations was placed. |
| `addresses()` | The window's eligible flows no site name was inferred for, per destination address, filtered as `rows()` is: `[{address, client_id, interface_id, lookup_state, country_code, asn, operator, flows, bytes}]`; `lookup_state` is `resolved`, `miss`, `pending`, or `no_row` for an address never looked up. Eligible is the attribution rate's rule (`host.attribution`), so a partly named address gives its named flows to the site rows and the rest here, and the site rows read from the flows and these rows make the window's eligible flows. No aggregate keeps these rows per address, so a window reaching past the flow horizon records the named state `unnamed_destinations_beyond_flow_horizon` and its purged hours have none. |
| `accumulateAddress(address, rows)` | The table row of one address: `{kind, address, flows, bytes, clients, client_ids, lookup_state, country_code, asn, operator}`, `kind` being `address`. |

#### `host.flows`

The window's flows by their columns, whichever source wrote them: the parts of a composition by
protocol, service port or blocking reason, its recent part, and the destination levels of a
connection tree. A filter is `{scope, interfaces, clients}`: a traffic scope (`all`, or absent, for
every one) and id lists, `null` for no restriction. Every figure counts each connection once.

| Member | What it answers |
|---|---|
| `parts(filter)` | The window's flows per direction, protocol, destination port and decision: `[{traffic_direction, protocol, port, decision}` and their summable figures`]`. |
| `recent(seconds, filter)` | The logged flows of the trailing `seconds` before now, in buckets of at least a second (a sixtieth of the span, rounded up): `{from, to, bucket_seconds, complete_until, buckets, parts}`. `complete_until` is when the filter log's active provider last ended a pass healthy, `null` when none did; `buckets` is `[{from, to, gap}]`, a bucket after `complete_until`, before the earliest flow kept or meeting a collection gap being a gap, never a zero; `parts` is `[{bucket_start, traffic_direction, protocol, port, decision, interface_id, client_id}` and their summable figures`]`. |
| `destinations()` | The window's connections per inside end, direction, outside destination, site name, port and protocol: `[{interface_id, client_id, owner_id, traffic_direction, peer_address, lookup_state, asn, site_name, port, protocol, connections, bytes, blocked, blocked_bytes, newest}]`. Under `aggregate_mode = no_domains` the core reads no site name at all, so every `site_name` is `null` and the answer is the one a database with no attribution would give. |
| `serviceKey(protocol, port)` | The key of a service, its port and protocol together, as IANA identifies a service by both: `port:53/udp`, and `port:not_recorded` when no port was logged. |
| `decisions` | The decisions of a logged record, by name: `allowed`, the `blocked_decision` view's `firewall_rule`, `firewall_no_rule` and `firewall_reason_not_recorded` for a refused one, and `unknown` for an action nobody read. |

#### `host.attribution`

How much of the window's outbound traffic could be named at all, as a whole and per client. A rate is
**undefined** -- `null`, never `0` -- when no resolver source answered for the window or there is no
eligible flow to divide by, and `rate_state` says which: `defined`,
`undefined_no_resolver_evidence` or `undefined_no_eligible_flow`.

| Function | What it answers |
|---|---|
| `window()` | `{resolver_covered, cache_covered, attribution}`: whether `opnview` holds evidence a resolver source answered for the window, whether the resolver's cache was read over it, and the window's attribution row as a whole, its `client` empty and with no `eligible_bytes`. |
| `clients()` | The ids of the clients with an eligible flow in the window, ascending. |
| `client(id)` | One client's attribution row: `{client, eligible_flows, eligible_bytes, named_flows, methods, rate, rate_state, mean_delay_seconds, max_delay_seconds}`, its counts `0` when it has no eligible flow; `null` for no such client. |

#### `host.clients`

Every client's logged volume over the rolling window, assembled as `host.volume.window()` assembles
the interface family's: whole hours from the client and peer families' 1 h slots, the partial hours
at either edge from the flows. A client's flows are those whose inside end it is; this firewall is
never a client, and a second leg is its first leg's connection.

| Function | What it answers |
|---|---|
| `volumes()` | `{requested, covered, clients}`: `covered` is the covered part of the window as recorded, `null` when the store holds none of it; `clients` is one entry per client with traffic in the window, by ascending id: `{client_id, figures, by_direction, destinations}`, `figures` its summable figures, `by_direction` the same per traffic direction (`outbound`, `inbound`, `inter_interface`), `destinations` the distinct far ends it reached, counted over the hours and never summed. Reading it records the window's coverage and the freshness of the slots read. |

#### `host.rates`

The current rates, from the latest samples of the `measurement_sample` kind, fine or baseline,
whichever provider wrote them: a second measure, `sampled`, never summed with the logged one. A
subject with no current sample reads its named absence, `no_current_sample`, and one older than twice
its cadence `stale_sample`, its figures beside it; never a rate of `0`. Every rate is `{state,
measure, in_bits_per_second, out_bits_per_second, out_measured, sampled_at, span_seconds,
cadence_seconds, lower_bound}` (`docs/widget-api.md`, *Live figures*). Reading one renews the
live-interest lease.

| Function | What it answers |
|---|---|
| `current(groupBy)` | The rates a "now" figure shows: `{by_direction, outbound_unsplit, by_member}`. `by_direction` is one `{key, rate}` per direction (`outbound`, `inbound`, `inter_interface`); `outbound_unsplit` the outbound rate of the local addresses whose peers lie both outside and behind an interface, which no sample splits; `by_member` one `{key, rate}` per client (`client`) or interface (`interface`) holding a current sample, sorted by key, empty for any other grouping. |
| `byClient()` | Every client holding a current sample: an object from its id, as text, to its rate. |
| `client(id)` | One client's current rate, or its named absence. |
| `byPeer()` | Every peer address an inside local address was read with in a current sample: an object from the address to its rate, `in_bits_per_second` what the peer sent, `out_bits_per_second` what was sent to it -- measured only for a local address read with that one peer, since `traffic/top` reports a local address's sending only as its total over every peer, and otherwise `null` with `out_measured` false. A reading whose local end is outside -- the firewall's upstream address, the same conversation read again after NAT -- is not summed. A peer with no current reading is absent, never a rate of 0. |
| `clientPeers(id)` | The same for one client: an object from each peer address it was read with to its rate; empty when it has none. |
| `none()` | The named absence of a rate, what a subject with no current sample reads. |
| `series(window)` | Every sampling instant of `[from, to)`, fine and baseline, oldest first, split per direction as the current rates are: `{span_seconds, instants: [{at, by_direction, outbound_unsplit}]}`, `by_direction` an object from each direction read at that instant to `{in_bits_per_second, out_bits_per_second}`. A direction absent from an instant read nothing there, a measured 0 at a sampled instant; an instant absent from the list was not sampled -- never a rate of 0. It reads history, and renews no lease. |

**A rate a widget builds from another source keeps this shape.** A figure the source cannot fill is
`null`, `out_measured` false for the outbound one, never a rate of `0`. `traffic_composition` builds
the rate of a protocol or service-port member from the `pf_states` plugin's dataset `pf_states.rate`
(below, *`host.datasets`*), read `direction` `eq` `in` so a forwarded connection counts once. A chip
rate is relative to the local side -- `in` what it receives, `out` what it sends -- and a direction-in
state sits on the interface its connection entered by, so the local side is the connection's
initiator:

| Chip field | From `pf_states.rate`, summed over the member's rows of the latest pass |
|---|---|
| `out_bits_per_second` | `forward_bits_per_second`: pf's `bytes[0]`, from the initiator |
| `in_bits_per_second` | `reverse_bits_per_second`: pf's `bytes[1]`, back to the initiator |
| `out_measured` | `true`: pf keeps both counters on every state, and neither column is nullable, so neither figure is ever unfilled |
| `sampled_at` | the pass's `sampled_at` |
| `span_seconds`, `cadence_seconds` | the pass's `span_seconds`, the seconds between the two reads the rate is taken over: the cadence the pass actually ran at |
| `state` | `current`, or `stale_sample` when the pass is older than twice that, its figures beside it |

The dataset's `bits_per_second` (the two summed) and `states` are not carried: a chip has no field
for either. The firewall's own traffic has an `out` state only and is therefore not in these rates.

#### `host.datasets`

Another plugin's dataset, read by its data-catalogue name (`<plugin>.<table>`, or a core dataset's
own name; `docs/widget-api.md`, *Data catalogue*), without SQL. The read goes through the catalogue's
own filter grammar and compiler -- the same as `GET /api/datasets/<name>/query` -- so it names nothing a
catalogue query could not name and binds every value. The plugin's manifest names every dataset its
scripts read under `datasets` (`docs/plugins.md`, *Datasets a plugin reads*); an installation without one answers a named
absence.

| Function | What it answers |
|---|---|
| `latest(name, filter)` | The dataset's **latest rows**: `{dataset, state, at, condition, rows, truncated, withheld}`. Among the rows `filter` keeps, within the dataset's retention horizon, the rows whose time column holds the greatest instant -- every row of the most recent instant, and nothing older; a sample dataset written by a pass answers that pass's rows whole. A dataset with no time column has no instant: every row `filter` keeps, `at` `null`. `filter` is a list of `{column, op, value}`, exactly the catalogue's `filters` (the same operators and type rules); `null` or absent keeps every row. `rows` are objects keyed by column name, values as the catalogue reads them (an instant as epoch seconds, a boolean, an address and a MAC address as text), at most 1 000, `truncated` true when more matched. `condition` is the condition of the dataset's provider kind, `null` for an absent dataset. |
| `rows(name, options)` | The rows `options` keep: `{dataset, state, condition, rows, truncated, withheld}`, `state` as `latest`'s. `options` is `{filters, keys, from, to, columns}`, every member optional: `filters` the catalogue's, an `in` list up to 10 000 values; `keys` a set of tuples, `{columns: [...], values: [[...], ...]}` (bare values for one column), up to 10 000; `from` and `to` bound the time column, within the retention horizon; `columns` the columns to answer, every one by default. **One statement whatever the number of keys**: each list is bound as one array, so a derivation reads what a batch needs in a bounded number of reads. At most 10 000 rows, `truncated` true when more matched. |

`state` is a named state, never an error that breaks the widget:

| `state` | Meaning |
|---|---|
| `current` | rows were found; `at` is their instant |
| `no_rows` | the dataset is there, and nothing `filter` keeps lies within its horizon |
| `unknown_dataset` | the catalogue holds no dataset of that name, the data catalogue API's own refusal code for it -- the plugin declaring it is not installed, or was refused; `rows` is empty and `at` `null` |
| `withheld_by_aggregate_mode` | `aggregate_mode` is `no_domains` and `filter` reads a column that carries a domain name: nothing is read |

Under `no_domains` a column carrying a domain name is left out of every row and named in `withheld`. A
filter the catalogue refuses -- a column the dataset does not declare, an operator its type does not
admit, a value of the wrong type -- is the script's mistake: a `TypeError` naming the refusal's code and
path, which the script may catch.

```js
// The rate by protocol, from the pf_states plugin when the installation has it.
var latest = host.datasets.latest("pf_states.rate", [{ column: "direction", op: "eq", value: "in" }]);
if (latest.state !== "current") {
  // unknown_dataset, no_rows or withheld_by_aggregate_mode: show the absence, never a rate of 0.
}
```

#### `host.peers`

The outside peers of the rolling window and where the geolocation placed them. A peer the
geolocation could not place is never dropped: it lands in a named unplaced bin, so placed plus
unplaced is always the total. `geo` is what `host.services.geolocation` answers; an interface filter
is an id list, or `null` for no restriction, as `host.refs.ids` answers it.

| Function | What it answers |
|---|---|
| `outside(rows, scope, interfaces)` | The rows of `host.volume.window()` grouped by outside peer, in a traffic scope and for an interface filter: `{peers, others}`. `peers` is `[{address, figures, interfaces}]`, the most bytes first and equal bytes by address, each `interfaces` the sorted ids of the inside interfaces that reached it; `others` is every row of the scope and filter with no outside peer, in order. A second leg is in neither. |
| `sorted(peers)` | Peers `[{address, figures, interfaces}]` a script grouped itself, in the same order, each interface list sorted. |
| `keepInterface(interfaces, row)` | Whether a row's inside interface passes an interface filter. |
| `placement(geo)` | `""` for an address placed with coordinates, otherwise its unplaced bin: `miss`, `pending`, `no_row` or `no_coordinates`. |
| `placementState(geo)` | The bin of an address the geolocation did not resolve: `miss`, `pending` or `no_row`. |
| `bins(reasons)` | Unplaced bins, every reason present even at zero: `{add(reason, figures, addresses), list(), total()}`. `list()` is `[{reason, addresses, volume}]` in the order the reasons were first named; `total()` the summable figures of every bin. |
| `bins(reasons, true)` | The same bins, listing their addresses: `addAddress(reason, address, figures)` adds one address's figures, an address counting once however often it is added, and each bin of `list()` also carries `members`, `[{address, volume}]` by bytes then address, summing to the bin. A destination nothing names is shown by its address, never folded into a count alone. |
| `reasons` | The unplaced bins, by name: `miss`, `pending`, `no_row`, `no_coordinates`, `east_west`, `this_firewall` (`this_firewall_without_outside_end`), `other_endpoint`. |

#### `host.live`

The live mode a live figure is drawn in (`docs/widget-api.md`, *Live figures*). It never calls the
firewall; the built-in `live_strip` widget is drawn from it and `host.rates.series`.

| Function | What it answers |
|---|---|
| `cadence()` | The live cadence in force, in seconds: the finest cadence of the live passes the loaded plugins declare, or the measurement interval where none declares one. |
| `renew()` | Renews the live-interest lease, and answers the instant it lapses, epoch seconds, or `null` when the service holds no lease. |

#### `host.mapfilters`

| Function | What it answers |
|---|---|
| `allowedOnly(figures)` | The allowed half of summable figures: `bytes` is the allowed bytes, `allowed_bytes` and `allowed_connections` are kept, every other member is `0`. |
| `blockedOnly(figures)` | The blocked half: `bytes` is the blocked bytes, `blocked_bytes` and `blocked_connections` are kept, every other member is `0`. |

#### `host.matrixplacement`

| Member | What it is |
|---|---|
| `keys` | The keys of the members that are not an interface: `outside` (the row of traffic arriving from outside), `north_south` (the column of traffic leaving to it) and `this_firewall` (this firewall's own row and column). |

#### `host.samples`

The firewall's sampled readings: its own gauges and its interface counters. A sample is a value at
an instant, so a bucket of a longer period wants an aggregation, never a count, and a bucket with no
sample is a gap, never a `0`.

| Function | What it answers |
|---|---|
| `metrics` | Per health metric the loaded plugins declare (`sample_metrics`, `docs/plugins.md`; the built-in `insight` declares `uptime`, `cpu`, `memory`, `swap`, `disk`, `temperature`, `gateway_latency`, `gateway_loss`), the subject kind and the measures it is stored under: `{kind, measures}`. |
| `subjects(metric)` | The subjects that reported one of a metric's measures in the window: `[{kind, key, measure, unit}]`, by measure then key. |
| `points(subject)` | One subject's samples over the window, oldest first: `[{at, value}]`. |
| `throughput(device, measure, direction)` | One interface counter as points: `{points, resets}`. `measure` is `bits_per_second`, `bytes`, `packets` or `errors`, `direction` is `in` or `out`. A point is the per-second rate (times 8 for bits) or the increase between two samples, stamped at the later one; a counter that went down is a reset, counted and never a point. |
| `aggregate(window, width, points, aggregation)` | Points cut into the window's buckets and combined by `mean`, `min`, `max`, `sum` or `last`: `[{from, to, gap, value}]`, a bucket no point falls in being a gap with no value. |

#### `host.series`

What a chart of series reads beside its buckets, and the envelope of a series of its own, for a
chart whose series each degrade on their own.

| Function | What it answers |
|---|---|
| `traffic(filter, width)` | The traffic of the window per bucket start and traffic direction, in the measure the request is answered under: `{buckets, covered}`, `buckets` mapping a bucket start to `{direction: summable figures}` and `covered` the interval read, `null` for none. `filter` is `{interfaces, client_id, rule_id, asn, country, site, measure}`, each `null` or left out for no restriction; `measure`, a declared measure's name, reads that measure whatever the request is answered under, for a series whose measure is `<measure>_bytes` or `<measure>_packets`. Whole-hour buckets the flows no longer hold are read from the 1 h slots when only interfaces restrict the series, and the answer then carries the state `series_read_from_hour_slots_beyond_flow_horizon`. |
| `measureState(measure)` | `null` when a declared measure can answer the window -- its window states recorded as covered throughout it -- and otherwise the measure's named state, which the answer then carries: a series of the measure then carries no figure. A name no plugin declares is a `TypeError`. |
| `providers()` | Every registered provider with its availability and condition, as `host.envelope.availability()` writes one. |
| `kindCondition(availability, kind)` | The condition of a provider kind among an availability list: its active providers' best. |
| `bind(kinds)` | Adds every registered provider of those kinds to the response's availability: for a widget whose kinds depend on its parameters, and which declares none. |
| `window()` | The response's window report, `{requested, covered, coverage, flow_horizon_at}`, or `null` for a widget that is not windowed. |
| `unresolved()` | The response's unresolved references, those of series entries included. |
| `dataset(source)` | The data-catalogue dataset a series source names when it is not one of the five sources of `docs/widget-api.md`, *Series of custom_chart*: `{name, freshness_kind}`, or `null`. |
| `fillDataset(index, window, gaps, width)` | Series entry `index` read from its dataset onto the window's buckets: `{window, measure, unit, buckets, total, rows}`, `window` being the series' report with the dataset's coverage and `total` `null` unless the aggregation adds up; `null` when the entry names no dataset. |

#### `host.interfaces`

What the store counts per interface beyond its volume. No interface is classified by what it is
called.

| Function | What it answers |
|---|---|
| `clients(from, to)` | Per interface, `[{interface_id, seen, known}]`: `seen` is the distinct inside clients of the flows of `[from, to)` on it, `known` the client rows of the interface seen at or after `from`. |
| `ruleLogging()` | The rules whose logging is off: `{not_logging_by_interface, floating_not_logging, unresolved_not_logging, not_reported}`. The first maps an interface id, as a string, to the rules naming it; the others count the rules naming no interface (floating rules), the rules whose interfaces did not resolve, and the rules whose logging flag the firewall did not report at all, which is not the same as a rule that does not log. |

#### `host.addresses`

The addresses this installation's upstream interfaces -- those carrying a gateway -- have held, as
`opnview` saw them. The address an interface holds is what the firewall sees, and only that: behind
an upstream NAT, the public address belongs to a device `opnview` does not read.

| Member | What it is |
|---|---|
| `upstreams()` | Every upstream interface: `[{interface_id, history}]`, `history` being every address and gateway it held, newest first: `[{source_field, address, prefix_length, address_family, first_seen_at, last_seen_at}]`, `prefix_length` `null` when none was reported. |
| `sourceFields` | The fields an address is read from, by name: `addr4`, `addr6`, `ipv4`, `ipv6`, `gateways`. |

#### `host.specialpurpose`

The IANA special-purpose address registries, so a widget says whether an address is public by
arithmetic on the address, never by a guess about a name.

| Member | What it is |
|---|---|
| `classify(address)` | `{special_purpose, public}`: `special_purpose` is the most specific registry block holding the address, `{block, name, reference, globally_reachable}` (`globally_reachable` `null` where the registry gives no value), or `null` for an address in no block; `public` is whether the Internet can reach it. A text that is not an address is in no block and is not public. |
| `registries` | The two registries the blocks are read from, cited by name. |

**A widget that reads domain names withholds them.** Under `aggregate_mode` = `no_domains`, no
answer carries a domain name: a widget built on them declares `"no_domains": "whole"` and is withheld
entirely, and one that shows a name beside other figures sets it to `null` and records the field
with `host.envelope.withhold`. `host.query.carriesDomain` says which statements read one.
**The core enforces it for a widget that is not built in**, so its author has nothing to do: under
`no_domains`, a call of `host.query.rows` or `host.query.row` on a statement that carries a domain
name is not run, the script is stopped (an interruption no `catch` intercepts), and the answer is
withheld whole -- condition `withheld_by_aggregate_mode`, `data` null, `"data"` in `withheld`
(maintainer's decision, 12 October 2026). A third party's widget that checks
`host.query.carriesDomain` first and reads another statement instead keeps its answer.
