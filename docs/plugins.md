# Plugins

A **plugin** is a directory that teaches `opnview` to read something it did not read before: a
source on the firewall, or a service beyond it. "Plugin" is `opnview`'s own term. It is not an
OPNsense plugin package such as `os-ddclient`, which is installed on the firewall; an `opnview`
plugin is installed beside `opnview` and reads the firewall through its API.

Everything `opnview` reads is a plugin, built-ins included: the filter log (`pf`), Suricata's
alerts (`suricata`), the DHCP servers (`kea`, `isc`, `dnsmasq`), the resolvers (`unbound`,
`dnsmasq`), the Insight sampler (`insight`), the pf state table's rate by protocol and port
(`pf_states`), the MaxMind GeoLite2 databases (`maxmind`) and the Public Suffix List
(`publicsuffix`). They are written exactly as a third party writes one, and they
are the best examples there are: `plugins/<name>/` in the repository.

Writing a plugin needs a text editor and the `opnview` binary. It needs no Go toolchain, no
compilation, no fork and no pull request.

## Where plugins live

| Origin | Where | Read |
|---|---|---|
| Built-in | `plugins/<name>/` in the repository, embedded in the binary | at every start |
| Yours | `<data-dir>/plugins/<name>/` | at every start |

**A plugin is discovered at start.** Drop its directory into `<data-dir>/plugins/` and restart the
service; a plugin added to a running service is picked up at the next start.

**A plugin that cannot be loaded is refused, logged with a closed reason, and skipped.** The other
plugins load and the service starts. The plugin listing (`GET /api/plugins`, see
`docs/widget-api.md`) reports every plugin, loaded or refused, with its reason.

**The built-in wins every collision**: a plugin name, a provider's kind and key, a setting. The
later plugin is refused with the code naming the collision.

**There is no activation step.** A loaded plugin's providers join the probe round like any other:
the probe decides whether a provider can be read, the operator's selection on the collection surface
can turn it on or off, and each kind's own rule decides how many providers may be active at once.

## The directory

```text
example_leases/
  plugin.json          the manifest (required)
  example_leases.js    the script, when the manifest names one
  locale/en.json       the plugin's own catalogue strings
  widgets/<type>/      each widget the plugin carries (see Widgets)
  testdata/            recorded answers its tests replay
  tests/*.test.js      its tests, in JavaScript
```

The directory's name is the plugin's name: a snake_case word of at most 48 characters, with no
double underscore. A plugin directory holds no Go file; a test of the repository fails if one does.

## The manifest

`plugin.json` is JSON, read strictly: a key the format does not have is a refusal
(`manifest_invalid`), never a setting silently ignored.

| Key | Required | What it is |
|---|---|---|
| `api_version` | yes | The plugin API version the plugin targets. This build reads version `2` only. |
| `name` | yes | The plugin's name, equal to its directory's name. |
| `version` | yes | The plugin's own version, reported and never interpreted. |
| `display_name` | yes | What the plugin is called, in English. |
| `description` | no | What it does. |
| `script` | no | The JavaScript file holding the code the manifest cannot express. |
| `endpoints` | no | The firewall endpoints the plugin reads, each by a name. |
| `providers` | yes, unless the plugin carries widgets | The kinds the plugin implements. |
| `kinds` | no | The attributes of the kinds the plugin declares for itself (*Kinds of the plugin's own*). |
| `settings` | no | The settings the operator may set. |
| `pages` | no | The sections the plugin's settings are drawn in, each with its settings in display order (*Settings*). |
| `destinations` | no | The hosts beyond the firewall the plugin contacts, each a scheme and a host. |
| `tables` | no | Tables the plugin owns. |
| `widgets` | no | The widgets the plugin carries, each a directory `widgets/<type>/` (see *Widgets*). |
| `push` | no | The push sources the plugin receives: syslog lines and NetFlow records the firewall sends, and datagrams and lines its script decodes (see *Push sources*). |
| `datasets` | no | The data-catalogue names its scripts read through `host.datasets` -- core datasets and other plugins' `<plugin>.<table>` (*Datasets a plugin reads*). Its own tables need no entry. |
| `derivations` | no | Functions of the script the core runs after collection passes, push batches and on a cadence, writing tables, counters and measures (*Derivations*). |

### Endpoints

A plugin reads the firewall through `opnview`'s own OPNsense client, by the endpoints it declares:

| Key | What it is |
|---|---|
| `name` | How the script and the mappings name the endpoint: a snake_case word. |
| `path` | `/api/<module>/<controller>/<command>`. |
| `method` | `GET` or `POST`. A POST that only reads, such as a grid search, is allowed. |
| `body` | `none`, `form` or `json`: how a request body is encoded. |
| `argument` | `none`, or `interface_names` for an endpoint taking interface names as a path argument. |
| `stream` | `true` for a server-sent event stream, which is read only with a bound. |
| `survey_section`, `upstream_url` | What establishes the endpoint: its section of `docs/opnsense-api-survey.md` and the documentation or source it cites. A built-in carries both; please carry them too. |
| `note` | What a caller has to know about it. |
| `max_age_seconds` | How old an answer may be and still be good enough, for every call that states none. Optional: see *The broker*. |
| `floor_seconds` | The shortest interval the endpoint is ever refreshed at: a call asking for fresher is served at this age. Optional. |
| `corrections` | Corrections of what the broker infers of the answers -- `nature`, `rows`, `key`, `cursor`, `parameters` (each a role: `window_start`, `window_end`, `page`, `page_size`, `identifying`, `ignored`), `types`, `domains`, `value_keyed`, `dedupe_on_content`. Optional: see *Corrections*. |
| `discard_events` | For a stream: how many events each connection begins with that are discarded, such as an average since boot. |
| `replaces` | For a stream: the name of another endpoint of the manifest the stream replaces while it runs. A push entry declares one too (*Push sources*, *Replacing polling*). Equivalence is declared, never guessed. |
| `versions`, `variants` | The range of OPNsense versions this declaration serves, and the routes of other ranges. Optional: see *OPNsense versions*. |

**Nothing is ever written to the firewall.** A declared endpoint is refused at load when it is not
under `/api/`, climbs with `..`, names a command that changes the firewall's state
(`endpoint_mutating`), uses another method, or conflicts with an endpoint `opnview` already knows
under the same path. A `POST` is accepted only to a read action -- a command that starts with
`search`, `get`, `query` or `list`, or ends with `status` -- and any other is refused at load
(`endpoint_not_read_action`) and again at every call, before anything is sent. The client refuses,
at every call, any path the plugins and the core have not declared.

### Providers

| Key | What it is |
|---|---|
| `kind` | The kind the provider implements: one of the kinds below. |
| `key` | The provider key, when it is not the plugin's name. |
| `display_name` | The provider's name in the registry. |
| `read_strategy` | `full_reread`, `time_window` or `watermark`: how the source is read, for the listing. |
| `cadence_seconds`, `min_cadence_seconds` | How often a pass runs, for `reconciled_state` and a kind of the plugin's own. A kind whose cadence the collection surface sets refuses one (`cadence_invalid`). |
| `triggered_by` | Core notifications that also run a pass, for a provider that states its own cadence: below. |
| `probe`, `read` | The declarative form, below. A provider declaring neither is served by the script. |
| `live` | A live pass, for a `measurement_sample` provider served by a script: *Live pass*, below. |
| `source` | `push` for a provider of a kind of the plugin's own that its push entries feed: no probe, no read, no script function, and no cadence to declare (its pass, every minute, reads the push receiver and never the firewall). See *Push sources*. |
| `enable_pages` | The OPNsense pages that turn the provider on when the firewall reports it installed and not collecting, each `{path, label, menu_url, note}`: a `/ui/` path of the firewall's web interface, the catalogue key naming the page by its OPNsense menu entry (`plugin.<name>.*`), and the menu definition at `https://github.com/opnsense/core/blob/26.7.3/` establishing the path. The Sources surface links the firewall URL the operator entered followed by the path, under the statement that opnview never makes the change. None is declared for a provider no OPNsense setting makes collect. |
| `value_sources` | For each value the provider exports beside its functions (its kind's `values`), `{source, survey_section, upstream_url}`: what establishes it -- the source's own name for it, the survey section recording it, and the file that section cites. A built-in cites every value taken from a source (*Values another plugin reads*). |

**A provider is served by the script, or declared.** A script exports, under the kind's name, the
functions the kind's contract lists. A provider whose source is one endpoint answering rows can
instead be declared: `probe` names an endpoint and a check (`answers`, or `service_running` for an
OPNsense service status), and `read` names an endpoint, whether to send the standard grid-search
page (`paginate`), and where each field of the kind's record comes from:

```json
"fields": {
  "address": "address",
  "mac": { "from": "hwaddr", "as": "mac" },
  "lease_state": { "value": "active" }
}
```

A mapping is a row's field (a dotted path for a nested one), read as the record field's type, or
read as `epoch`, `iso_time`, `syslog_time`, `filter_log_time` (instants; the firewall's own offset
applied), `port`, `mac`, `now` (the pass's own instant) or `shape` (the JSON shape the field arrived
in); or a constant `value`. A field the record does not have is refused (`mapping_field_unknown`),
and so is a required field left unmapped (`mapping_field_missing`).

### Live pass

A `measurement_sample` provider served by a script may declare a **live pass**: a second pass that
reads only the endpoints it names, run at a cadence the plugin declares as a setting, and only while
somebody looks at a live figure -- while the core's live-interest lease holds (`docs/widget-api.md`,
*Live figures*). Its readings are stored as **fine samples**, marked with the cadence they were taken
at and purged after `live_sample_retention_seconds`; the baseline `sample` pass runs at the
measurement interval throughout, whether anyone looks or not.

```json
"settings": [
  { "key": "live_cadence_seconds", "type": "integer", "default": 10, "min": 2, "max": 300,
    "label": "plugin.insight.setting.live_cadence_seconds" }
],
"providers": [
  { "kind": "measurement_sample", "display_name": "NetFlow / Insight sampling",
    "live": { "function": "live", "cadence_setting": "live_cadence_seconds",
              "endpoints": ["traffic_top", "traffic_interface"] } }
]
```

| Key | What it is |
|---|---|
| `function` | The function the script exports under the kind's name, called as `sample` is -- `(host, discovery, now)` -- and answering a `sample_answer`. |
| `cadence_setting` | A declared `integer` setting with a `default` and a `min` of at least 1: the live cadence, in seconds. The cadence in force is never below that `min`, nor below the `floor_seconds` of the endpoints the pass reads. |
| `endpoints` | The only endpoints the live pass may read, by their names; a stream is refused. |

The live pass's requests carry the live cadence as their max age, so the broker classes them
real-time at or below its threshold (5 s by default) and coalesces every reader of one datum into one
call. **Stamp each reading with the answer's `fetched_at`** (`readObject` answers one too), not with
`now`: when the baseline pass falls due during live mode the broker serves it the live pass's answer
from the mirror, and the same capture stamped with the same instant is promoted to a baseline sample
rather than stored twice. A live pass declared on another kind or on a declarative provider, naming a
function the script does not export, a cadence setting that is not such an integer, or an endpoint the
manifest does not declare or declares as a stream, is refused (`live_pass_invalid`). In a plugin's own
tests, `t.call(kind, function, ...)` calls a live pass's function as the service does: under its
default cadence, reading only its endpoints.

### Notifications

The core publishes a notification when a pass commits, on a closed list of PostgreSQL channels:

| Channel | Published when | Payload |
|---|---|---|
| `opnview_collection_pass` | A collection pass of a core kind ended without an error, once per provider it read. | `{"kind": ..., "provider_key": ...}` |
| `opnview_plugin_pass` | A pass of a plugin's own kind stored its rows and state; published inside that transaction, so a pass that rolls back publishes nothing. | `{"plugin": ..., "provider": ...}` |
| `opnview_aggregate_refresh` | An aggregate refresh rewrote slots. | `{"periods": [...]}` |

A provider that states its own cadence may name some of them in `triggered_by`, alone or followed
by a colon and a subject -- the kind of a collection pass, the plugin of a plugin pass:

```json
"triggered_by": ["opnview_collection_pass:dhcp_lease", "opnview_aggregate_refresh"]
```

Its pass then also runs when one is heard, besides its cadence, and never sooner than
`min_cadence_seconds` after its last pass began: a notification heard earlier waits out the bound.
The passes of one provider never run at once, and the notifications heard during a pass make one
pass more, not one each. A name that is not a published channel, or a trigger on a provider of a
kind whose cadence the collection surface sets, is refused (`trigger_invalid`).

### Tables

A plugin that implements a kind of its own stores what it reads in tables it declares. Each table
lives in the plugin's own PostgreSQL schema, `plugin_<plugin>`, so two plugins may each declare a
table of the same name; a query names it `plugin_<plugin>.<table>`.

```json
"tables": [{
  "name": "readings",
  "nature": "sample",
  "columns": [
    {"name": "sampled_at", "type": "integer", "role": "time"},
    {"name": "meter", "type": "text"},
    {"name": "value", "type": "real", "role": "measure", "unit": "watt"}
  ],
  "primary_key": ["meter", "sampled_at"],
  "retention": {"column": "sampled_at", "seconds": 2592000}
}]
```

| Key | What it is |
|---|---|
| `name` | The table's name: a lower-case snake_case word. |
| `columns` | Each `name`, `type` (a word of the type vocabulary: `integer`, `real`, `text`, `boolean`, `ip`, `mac` or `instant`; see *Push sources*, *The type vocabulary*), and optionally `nullable`, `carries_domain` (the column holds a domain name, withheld under `aggregate_mode` = `no_domains`), `role` (`time`, `dimension` or `measure`; a dimension by default, time for the retention column), `unit` (a measure's lower-case word, such as `byte`) and `aggregations` (what the data catalogue admits on it; the role's defaults otherwise). |
| `primary_key`, `indexes` | The keys. A row written again with the same primary key replaces the first. |
| `retention` | The column holding each row's instant -- an `instant`, or an `integer` of UTC epoch seconds -- and how long a row is kept after it; `seconds` 0 follows the installation's retention. A push output table's is an `instant`. |
| `nature` | `event` or `sample`: what a bucket of the data catalogue with no row means -- a true zero, or a gap. `sample` by default, which never draws a zero it did not read. |
| `counters` | `true` makes it a table of counters, which only a derivation writes (*Derivations*): it has a primary key, every other column is a NOT NULL number, and a row written with a key already held adds its figures to the held row's, in one batched write, instead of replacing it. A counter outlives the purge of whatever it counts. |

A table with a retention column is a TimescaleDB hypertable chunked by day on it, purged by whole
chunks and then to the exact second. A hypertable cannot hold a unique key that leaves out its time
column, so a primary key or a unique index without it is refused (`unique_key_without_time`).

**A declaration that changed is a rebuild, never a refusal.** The old table moves, with its rows,
into the schema `opnview_aside` under a name beginning `aside_`; the new one starts empty; the
plugin's durable state is reset; the listing reports the move. A set-aside table is never read by a
widget or the data catalogue, and is purged by its retention and dropped once empty. A plugin
removed from the data directory keeps its schema and rows, and the plugin listing names them.

**Every table is a dataset of the data catalogue** (`docs/widget-api.md`, *Data catalogue*), named
`<plugin>.<table>`, labelled by the plugin's own string `plugin.<plugin>.dataset.<table>`, and dated
by the plugin's provider of its own kind. A chart can then read it -- a `custom_chart` series whose
`source` is its name -- with no file of `opnview` changed. A table the catalogue could not serve --
a measure of type text, a unit on a dimension, an aggregation its role cannot admit -- is refused
(`table_invalid`).

### Kinds of the plugin's own

A kind is data, whoever declares it. A core kind's attributes are in its file,
`internal/collect/provider/kinds/<kind>.json`; a kind the plugin declares for itself -- a kind one of its
providers names that is no core kind -- takes its attributes from the manifest's `kinds`, one entry per
kind, and the defaults when it has none:

| Key | Default | What it says |
|---|---|---|
| `kind` | | The kind, which one of the plugin's providers implements. |
| `concurrent` | `false` | Several providers of the kind may be active at once. Declare it only when every row of the kind's tables says which provider reported it: the database then lets them all be active, and refuses a second active provider otherwise. |
| `push` | `false` | The kind's provider may be fed by the plugin's push entries (`"source": "push"`, *Push sources*). |
| `bindable` | `true` | A widget manifest may bind the kind in `providers` and `primary`. |

```json
"kinds": [{"kind": "example_meter", "concurrent": true}]
```

The registry rows are the declarations': loading the plugins writes each kind's attribute and each
provider's row, and nothing in the database's schema names a kind or a provider.

### A kind of the plugin's own, polled

A provider of a kind of the plugin's own that is not fed by push entries states its cadence
(`cadence_seconds`, `min_cadence_seconds`) and is served by the script, which exports two functions
under the kind's name:

| Function | Answers |
|---|---|
| `probe(host)` | `{result, error}`: whether the source can be read (`result` as every kind's, below). |
| `collect(host, state)` | `{result, rows, state, error, unsupported}`: `rows` an object from each table the plugin declares to the rows to store, each row checked against its columns; `state` the provider's **durable state**, an object of strings handed to the next pass's `collect` as its `state`. The rows and the state are stored in one transaction. |

The durable state is what a pass keeps for the next one -- a cursor, or a previous reading to
subtract from: the built-in `pf_states` keeps the counters of the pf state table it read, and stores
the rate as the difference at the next pass. In a plugin's own tests, `t.call(kind, "collect", state)`
answers the checked `rows` and `state`.

## The script

The script is JavaScript (ECMAScript 5.1 with most of ES6), run by the interpreter embedded in the
binary. **Each call runs in a fresh interpreter**: nothing a call leaves in a global survives to the
next, and a plugin keeps what it must keep in its settings, its files or its tables. A call that
runs longer than two minutes is stopped.

The script assigns its functions to `exports`, under each kind's name, and may export `helpers` for
its own tests:

```js
exports.dhcp_lease = {
  probe: function (host) { /* ... */ },
  leases: function (host, pageSize) { /* ... */ }
};
```

Every function is handed the **host** first, then the arguments its contract lists.

### The host

| Function | What it does |
|---|---|
| `host.call(name, options)` | Asks for a declared endpoint's answer. `options` may carry `query`, `form`, `json` and `arguments`, and `max_age_seconds` and `after` (below). Answers `{endpoint, outcome, ok, status_code, detail, value, decodable, body, body_bytes, availability_outcome, served_from, fetched_at, age_seconds, gaps, replaced_by, error, refused, json()}`. |
| `host.readCollection(name, ...wrappers)` | Calls an endpoint and reads its rows exactly as the core reads a collection: `{rows, ok, error}`. |
| `host.readObject(name)` | Calls an endpoint and reads its object: `{object, ok, fetched_at, error}`, `fetched_at` the instant the answer was fetched, which the mirror may have held. |
| `host.serviceState(name)` | Reads an OPNsense service status: `{running, state, outcome, detail, error}`. |
| `host.stream(name, {events, within_seconds})` | Reads a bounded server-sent event stream: `{events, values, outcome, ended_by, error}`. |
| `host.events(name, cursor)` | The events of a stream's shared subscription received after a cursor the script holds, without waiting: `{values, events, cursor, connected, gaps, error}`. The first call subscribes. |
| `host.catalogue()` | The learned API catalogue (below), as `GET /api/firewall/catalogue` serves it. |
| `host.firewall` | Read-only: `{version}`, the firewall's OPNsense version as discovery last read it, `null` until read. |
| `host.normaliser()` | The firewall's clock: `filterLog(text)`, `iso(text)`, `syslog(text)`, each `{value, error}` in UTC epoch seconds. |
| `host.now()` | The pass's instant, UTC epoch seconds. |
| `host.pagination(current, rowCount)` | The standard grid-search form. |
| `host.log(text)` | Writes a line to the service's log, under the plugin's name. |
| `host.settings`, `host.secret_states` | The plugin's settings, and each secret's state (`absent`, `ready`, `undecryptable`). Present when the plugin declares settings. |
| `host.fetch(options)` | The fetch facility, below. Present when the plugin declares destinations. |
| `host.files` | The plugin's own data directory, below. |

An `error` a host function answers is the firewall's or the network's failure, named; a script
passes it on in its answer, and the kind records it. A script that calls a host function wrongly
-- an endpoint it did not declare, an option that does not exist -- fails the call. A call refused
by the read-only rule answers `refused: "write_refused"` and an `error`, and nothing reaches the
firewall. **No host function ever answers the API key, the secret, the firewall's address or an
`Authorization` value**; `opnview plugin test` searches every answer for them and fails a test
that finds one.

### The broker

Every request to the firewall -- every plugin's, and the core's own -- goes through one gate, the
broker. A script never calls the firewall directly and never needs to know this, but four things
follow from it.

**An answer may come from the mirror.** The broker keeps, in the `opnsense` schema of the
database, the fields of each answer that some reader read, and serves an answer from there while it
is younger than the caller's **max age**: `max_age_seconds` on the call, else the endpoint's
`max_age_seconds` in the manifest, else the cadence of the pass making the call. `served_from` says
`mirror` or `firewall`, `fetched_at` and `age_seconds` say when it was had. Two plugins asking for
the same answer at the same time cause one call. A request asking for fresher than the endpoint's
**freshness floor** -- its `floor_seconds`, or the median time the firewall takes to answer it -- is
served at the floor, and its `age_seconds` shows it.

**The answer's `value` is an observed object.** A script reads `response.value` as it would read
`JSON.parse(body)`; the broker records which fields it read, and the mirror stores those and no
other. A field the mirror does not hold is not a missing field: the call is run again, transparently,
with that endpoint answered from the firewall. Reading `body`, or calling `json()`, reads the whole
answer, which the mirror then stores whole; a plugin reading a few fields reads them through `value`.
`decodable` says whether the body was JSON.

**A record stream is read after a cursor.** For an answer the broker learned is a record stream --
log lines, lookups -- `after` on the call returns only the rows past a cursor the script holds, in
the order the firewall gives them, with `gaps`: the intervals the mirror could not fill because the
firewall had already dropped them, each `{from, to, reason}`. A gap is never zero rows.

**A stream is shared.** Every subscriber to a stream with the same parameters shares one
connection. A stream a loaded plugin reads fields of is recorded -- each field read, at its
reception, into a table the data catalogue lists -- and stays open while that plugin is loaded. A
stream that drops reconnects with back-off, and its silence is a gap. A stream declared with
`replaces` suspends the plugin's polling of the endpoint it replaces while it runs (`served_from:
"stream"`, `replaced_by` names the stream), and polling resumes when it drops.

### Corrections

The broker infers, from each answer, its **nature** (`snapshot`: current state such as leases; or
`record_stream`: records that accumulate, read on a cursor), its **row set** (the path of its rows),
each row's **key**, a record stream's **cursor**, each field's **type**, which fields carry domain
names, which objects are keyed by values (an object keyed by interface names is rows, not fields),
and each request parameter's **role** (a window's start or end, a page, a page size, or a parameter
that identifies a different datum). Where it infers wrongly, the manifest corrects it:

```json
"corrections": {
  "nature": "record_stream",
  "rows": "rows[]",
  "key": ["fileid", "filepos"],
  "cursor": ["fileid", "filepos"],
  "parameters": {"current": "page", "rowCount": "page_size", "digest": "ignored"}
}
```

A correction is a fact about the firewall, so two plugins correcting one endpoint differently is a
conflict: the built-in's correction wins, else the first loaded plugin's, and the other plugin
loads all the same and is listed with `mirror_correction_conflict`.

### The API catalogue

`host.catalogue()`, and `GET /api/firewall/catalogue` for a signed-in user, answer what the broker
learned of every endpoint it called: its parameters and their roles, each datum's nature, key,
cursor and schema versions (with the OPNsense version that produced each), its readers and the
fields each read, its tables, its costs (calls, median latency and size), its freshness floor, the
streams observed and their subscriptions, and an endpoint a plugin reads that no section of the
survey cites (`uncited`). It never carries a field value, a credential, the firewall's address or
an authorisation header.

### Helpers

The global `opnview` carries the readers the built-in plugins share with the core, so a plugin
reads a firewall field exactly as the core would: `opnview.decode.string(row, key)`, `int`,
`float`, `epoch`, `port`, `flag`, `nested`, `nestedString`, `textField`, `rows(body)`,
`lower(text)`; `opnview.sha256Hex(text)`, `opnview.base64(text)`;
`opnview.dhcp_lease.decodeLease(row)` and `normaliseMAC(text)`;
`opnview.dns_lookup.normaliseAction(text)`.

### What a call can end in

| Outcome | Meaning |
|---|---|
| `script_failed` | The script threw, or answered something that is not its contract's record. |
| `script_timeout` | The call ran past its bound and was stopped. |
| `records_invalid` | A record lacks a required field or carries one its contract does not have. Nothing of that answer is stored. |
| `firewall_version_unsupported` | The plugin maps OPNsense versions to its routes or scripts, and the firewall's version is covered by none of its ranges, or is not known yet. Nothing is called. |

Each is recorded as the provider's availability, with what failed, and nothing else changes.

## Datasets a plugin reads

A plugin's scripts -- its source script, its live pass and push parse functions, its derivations and its
widgets -- read the data catalogue through `host.datasets` (`latest` and `rows`, described under *The widget
host*, `host.datasets`; the same two functions are on a source script's host). Its manifest names every
dataset they read under `datasets`: a core dataset by its own name, another plugin's as
`<plugin>.<table>`. Its own tables need no entry. A read of a dataset the manifest does not name is refused
with `dataset_not_declared`: at load when a script names it by a literal, at the call -- a `TypeError`
carrying the code -- when the name is computed. A declared dataset an installation lacks is no refusal: the
read answers `unknown_dataset`. Nothing in `account`, `session`, `encrypted_credential` or `setting` is a
dataset, and `host.query` reads only the plugin's own tables.

A source script's and a derivation's reads withhold nothing under `aggregate_mode` `no_domains`, because
what they write is stored, and the aggregate mode changes what is shown, never what is stored; a widget's
reads withhold as every widget answer does.

The core datasets a derivation joining another source to the logged connections reads:

| Dataset | Rows |
|---|---|
| `leg` | Every logged connection leg -- each filter-log record, the second leg of a paired connection included -- by `flow_id`, with its `pair_outcome` and `paired_flow_id`, pf's `direction`, its `device` and `interface`, its `action`, the tuple pf logged, and whether either end is *This Firewall*. Timed by `observed_at`. |
| `interface` | The firewall's interfaces as discovery holds them: `identifier`, `device`, `description`, `link_type`, `link_kind`, `vlan_tag`, `is_upstream`. |
| `this_firewall_address` | This firewall's own addresses as discovery holds them, each with its `interface`, `first_seen_at`, `last_seen_at` and `is_current` (held at `t` when `first_seen_at - margin <= t` and `is_current = 1` or `t <= last_seen_at + margin`, the margin the discovery interval). |
| `provider_value` | The values the active providers export (*Values another plugin reads*). |

Discovery is no provider of a kind: the condition of `interface`, `this_firewall_address` and
`provider_value` reads `state_unknown`.

### Values another plugin reads

A kind's contract may name **values** its implementations export beside their functions -- the
`firewall_log` contract's pf state timeouts, `dns_lookup`'s page bound. The core publishes the **active**
providers' values as the core dataset `provider_value` (`kind`, `provider_key`, `name`, and the value in
`integer_value`, `number_value` or `text_value` by its type), rewritten at load and at every probe round, so
a plugin needing a fact another plugin's source states reads it there and never restates it. The pf plugin
exports pf's default state timeouts and cites each in its manifest's `value_sources`, from `pf.h`'s
`PFTM_*_VAL` (survey, *S12*); the values in force are not readable, so they are the defaults.

```js
// pf's established TCP timeout, from whichever firewall_log provider is active.
var values = host.datasets.rows("provider_value", {
  filters: [{ column: "name", op: "eq", value: "state_timeout_tcp_established_seconds" }] });
var established = values.rows.length > 0 ? values.rows[0].integer_value : null; // null: no timeout stated
```

## Derivations

A derivation computes from what the core and the plugin stored, and writes outputs of its own: a
measure's per-connection figures, a summary table, counters. It is a function the script exports under
`derivations`, declared in the manifest:

```json
"derivations": [{
  "name": "summarise",
  "after_passes": ["firewall_log", "dhcp_lease/kea"],
  "after_push": ["pings"],
  "cadence_seconds": 600,
  "datasets": ["leg"],
  "outputs": {"tables": ["summary", "tallies"], "measures": ["example_rate"]}
}]
```

| Key | What it is |
|---|---|
| `name` | Its name, a snake_case word; the function's under `derivations`, unless `function` names another. |
| `after_passes` | Collection passes that run it: a core kind, or a kind and one of its providers (`kind/provider`). |
| `after_push` | The plugin's own push entries whose stored batches run it. |
| `cadence_seconds` | Runs it on a cadence too; 0 runs it on its other triggers alone. At least one trigger is named. |
| `datasets` | The datasets it reads, each also under the manifest's `datasets` (*Datasets a plugin reads*). |
| `outputs` | What it may write: tables of the plugin's own, each with a primary key (counter tables among them, *Tables*), and measures the plugin declares (*Measures*). |

```js
exports.derivations = {
  summarise: function (host, run) {
    // run: {derivation, now, since, changes: [{trigger, at, kind, provider, entry, rows, from, to}]}
    return {
      rows: { summary: [/* rows */], tallies: [/* counters to add */] },
      measures: { example_rate: [{ connection: 1, connection_start_at: 0, direction: "forward",
        state: "measured", bytes: 0, packets: 0, records: 0, markers: [], due_at: null }] },
      intervals: { example_rate: [{ starts_at: 0, ends_at: null, state: "covered" }] }
    };
  }
};
```

**A run is handed what changed, never "everything".** `changes` are the passes (`kind`, `provider`), the
stored batches (`entry`, `rows`, the reception span `from`..`to`) and, with nothing else noted, the cadence
tick, since its last successful run (`since`, null before the first); `now` is the host's clock. They are
handed in an order of what they are, never of when they arrived, so the same changes make the same run.
**Runs of one derivation never overlap**: each has one loop, and changes arriving together make one run.

**A run is one transaction.** What it returns is checked against its declaration -- a table or measure it
did not declare, a column or field that is not one, a state or marker its measure does not declare, refuse
the whole run -- and written together, or not at all. A row replaces the held row of its key only when a
value differs, a measure row only when a figure, its state or its markers differ, and a measure's
`intervals` (its window states for the plugin's provider of the measure's kind) are rewritten only when
they differ: **a run over the same inputs writes nothing.** A counter adds, which is why a run is handed
only new changes. A measure row's `connection` is the logged connection's first leg (*Measures*); the
aggregate slots of every connection whose row changed are refreshed after the run.

**A run that fails writes nothing**, keeps its changes for the next run, and is recorded in the
availability of the plugin's provider -- its first of a kind of its own, else its first -- with the closed
outcome `script_failed`, `script_timeout` or `records_invalid`. A plugin declaring derivations declares a
provider. `internal/collect/testdata/example_derived` is a complete example.

## Settings

A plugin may declare settings the operator sets:

| Key | What it is |
|---|---|
| `key` | The setting's key, a snake_case word. It is stored as `<plugin>_<key>`. |
| `type` | `integer`, `number`, `boolean`, `text`, `enum` or `secret`. |
| `default` | The value an unset setting takes. A secret has none. |
| `min`, `max` | Bounds of an `integer` or a `number`. |
| `values` | An `enum`'s vocabulary. |
| `label` | The catalogue key naming the setting, in the plugin's `locale/`. |
| `note` | What the setting governs and why its default is what it is, for whoever reads the manifest. |

**Pages.** `pages` groups the settings into the sections the step-7 application draws, each
`{key, label, help, settings}`: a snake_case key of its own, the catalogue keys of its heading and of
the text explaining it (`plugin.<name>.*`), and the keys of the settings it holds, in display order. A
setting the manifest does not declare, or one on two pages, is refused at load (`manifest_invalid`). The
pages are served with the listing and by `GET /api/plugins/<name>/settings`. The netflow plugin's
`join_tolerance_seconds` and `retention_flow_record_seconds`, on its page `join`, are an example.

**A provider stating its own cadence offers the operator an override** among its plugin's settings:
`cadence__<kind>__<provider key>`, an integer of seconds whose default is the declared cadence and
which is refused below `min_cadence_seconds`. The scheduler reads it before every wait, so a saved
override reaches the running loop.

Settings are read through `GET /api/plugins/<name>/settings`, written through
`POST /api/plugins/<name>/settings` and listed by `GET /api/plugins` (`docs/widget-api.md`). A value outside its declaration is refused with a closed
condition naming the key (`unknown_setting`, `invalid_setting_value`, `setting_out_of_range`), and
nothing is written. A `null` value removes a setting that is not a secret, as emptying its field on
the settings page does, and its declared default is in force again. **A secret is stored encrypted with the key file beside the database, and no
endpoint ever returns it**: the listing says only whether it is set.

## Fetching and files

A plugin that contacts something beyond the firewall declares each destination, a scheme and a
host, in `destinations`. **A request to a host the manifest does not declare is refused before
anything is sent, redirects included**, so the listing never misstates what a plugin contacts. The
declaration is the author's own, not an operator allowlist.

`host.fetch({url, method, headers, file, max_bytes, conditional, check})`:

- `method` is `GET` (the default) or `HEAD`;
- `headers` go to the host asked and never to a host a redirect leads to;
- a GET streams the body to `file` in the plugin's data directory, never into script memory, under
  `max_bytes`;
- `conditional: true` sends the validators (`ETag`, `Last-Modified`) of the copy held, and a 304
  keeps it;
- the body is written to a temporary file and replaces the copy held only once it is complete and
  `check(temporary)` answers `{ok: true}`, so a failed or malformed download leaves the previous
  copy in use.

It answers `{outcome, status_code, detail, last_modified, etag}`, the outcome one of `fetched`,
`not_modified`, `answered` (a HEAD), `download_refused` (401, 403), `download_limit_reached` (429),
`download_failed`, `download_malformed` (refused by the check) and `host_not_declared`.

The plugin's data directory is `<data-dir>/plugin-data/<name>/`. `host.files` reads and writes plain
file names there, and nothing outside it: `exists`, `readText`, `writeText`, `remove`, `replace`,
`lines(name, limit)`, `sha256`, `extractTarGz(archive, entry, maxBytes)`, `gunzip(name, maxBytes)`,
`openMMDB(name, verify)` (a MaxMind DB format file, which DB-IP and IPinfo also publish:
`{database_type, build_epoch, lookup(address)}`) and `openSuffixList(name)` (the Public Suffix List
format: `{version, rules, registrableDomain(name)}`).

## Strings

A plugin's words are its own, in `locale/<language>.json`, in the format of `opnview`'s catalogue:

```json
{ "language": "en", "strings": { "provider.dhcp_lease.example_leases": "Example leases" } }
```

Its keys are confined to its own namespace: `plugin.<name>.*`, `provider.<kind>.<key>` of a
provider it declares, and `collection.kind.<kind>` of a kind of its own. Any other key is refused
(`string_outside_namespace`): a plugin never redefines a word of the core's.

## Testing a plugin

A plugin's tests are JavaScript, in `tests/*.test.js`, run by the interpreter that runs the plugin:

```text
opnview plugin test <plugin directory>
```

prints one line per test and exits non-zero when one failed. A plugin with push entries is tested by delivering recorded messages: see *Push sources*, *Testing a push entry*. It touches no data directory and
contacts nothing. The repository runs the built-in plugins' tests the same way.

Each test runs against a world of its own: a fake firewall answering only what the test arranged
(every other declared endpoint answers 404, which is a component that is not installed), reached
through `opnview`'s real client; a fresh database and data directory; and a fake web answering only
what the test arranged, reached through the real fetch facility. The plugin is loaded by the loader
every plugin goes through, and `t.call` runs a provider through the adapter the collector runs it
through, contract check included.

| Function | What it does |
|---|---|
| `test(name, function (t) {...})` | Declares a test. |
| `t.answer(endpoint, reply)` | Arranges the firewall's answer to a declared endpoint, on whichever of its routes the firewall's version selects. |
| `t.firewallVersion(version)` | Sets the OPNsense version the firewall runs, for a plugin that maps versions; unknown until set. |
| `t.web(url, reply)` | Arranges a web answer. |
| `t.call(kind, function, ...arguments)` | Runs a provider function; answers its checked record plus `error`, `unsupported` and `failure`. |
| `t.helper(name, ...arguments)` | Runs one of the script's `helpers`. |
| `t.requests(endpoint)`, `t.webRequests()` | What the fake firewall and the fake web received. |
| `t.setting(key, value)`, `t.settingValues()` | Writes a setting through the same validation; lists them as the listing does. |
| `t.clock(epoch)`, `t.now()` | Sets and reads the world's instant. |
| `t.discovery({interfaces, devices})` | A discovery snapshot, for kinds that take one. |
| `t.registered(kind)` | Whether the plugin provides a kind. |
| `t.dataFile(name, text)`, `t.dataFiles()` | Writes into and lists the plugin's data directory. |
| `t.assert(condition, message)`, `t.equal(got, want, message)`, `t.fatal(message)` | Assertions. |

A reply is `{status, json | body | fixture | file | events | mmdb_archive, headers}`: `fixture` names
a recorded answer in `testdata/`, `{"__provenance__": {...}, "body": ...}`; `file` serves a file of
`testdata/` as it is; `mmdb_archive` writes a MaxMind DB in the gzip tar archive MaxMind serves,
`{edition, release, database_type, build_epoch, records: [{network, data}]}`.

## Refusals

| Code | The plugin is refused because |
|---|---|
| `manifest_unreadable` | `plugin.json` cannot be read, or the entry is a file. |
| `manifest_invalid` | The manifest is not valid: malformed JSON, an unknown key, a missing or ill-formed value, a locale file that is not a catalogue. |
| `unsupported_api_version` | It targets a plugin API version this build does not read. |
| `measure_invalid` | A measure declaration is ill-formed, needs a kind none of its providers implements, labels itself outside its catalogue, or takes another plugin's measure's name. |
| `name_mismatch` | Its name is not its directory's. |
| `name_collision` | A built-in, or another plugin, has its name. |
| `unknown_kind` | A provider names a kind that is neither a core kind nor one of its own. |
| `core_only_kind` | A provider names a kind only the core implements (`flow_volume`). |
| `provider_key_collision` | Another provider has the same kind and key. |
| `setting_collision` | Another plugin stores a setting under the same key. |
| `cadence_invalid` | A cadence is missing, not allowed for the kind, or below its own bound. |
| `trigger_invalid` | `triggered_by` names a channel the core does not publish, or is declared on a kind whose cadence the collection surface sets. |
| `endpoint_invalid` | An endpoint declaration is ill-formed or declared twice. |
| `endpoint_not_read_action` | A declared `POST` names a command that is not a read action. |
| `mirror_correction_invalid` | A correction names a nature, a role, a type or a field path in a malformed way, or a broker key is out of range. |
| `version_range_invalid` | A version range has no bound, a bound that is not a version, or bounds in the wrong order; or variants, or scripts, are mapped without a range of the declaration's own. |
| `version_ranges_overlap` | Two ranges of one endpoint, or of the scripts, share a version. |
| `package_version_unavailable` | A range names an OPNsense plugin's package: see *OPNsense versions*. |
| `endpoint_not_relative`, `endpoint_outside_api`, `endpoint_parent_reference`, `endpoint_mutating`, `endpoint_method_not_allowed`, `endpoint_conflict` | An endpoint the client will not call. |
| `script_missing` | The script cannot be read, or a provider needs one and none is named. |
| `script_does_not_compile` | The script is not valid JavaScript, or fails while defining its exports. |
| `script_missing_function` | The script does not export a function its kind's contract lists. |
| `mapping_invalid`, `mapping_field_unknown`, `mapping_field_missing` | A declarative probe or read is wrong. |
| `table_invalid` | A table declaration is refused. |
| `unique_key_without_time` | A table with a time column has a primary key or a unique index that omits it. |
| `widget_missing` | A widget the manifest names has no directory. |
| `string_outside_namespace` | A catalogue string is outside the plugin's namespace. |
| `live_pass_invalid` | A live pass is declared where it cannot run, or names a function, a cadence setting or an endpoint wrongly: see *Live pass*. |
| `push_declaration_invalid` | A push entry's shape is wrong: no name or a name twice, a transport its protocol is not received over, a pattern and a parse function both, a parse function or a pattern for NetFlow, a field mapping naming no field or no column. |
| `push_protocol_unknown` | A push entry names a protocol this build does not decode. |
| `push_filter_invalid` | A push entry's filter is not its protocol's: no program for syslog, no required element for NetFlow, an unknown key, facility, severity or operator. |
| `push_element_unknown` | A push entry names a field its protocol does not carry: a syslog header field outside its list, or an Information Element outside the IANA registry. |
| `push_pattern_invalid` | A push entry's pattern does not compile under RE2, or a named group is not a column of its table. |
| `push_destination_invalid` | A push entry names both or neither of `output_table` and `kind`, a table the plugin does not declare, or a kind it does not provide or no push path receives. |
| `push_table_without_instant` | A push entry's table has no `instant` time column. |
| `push_kind_without_replacement` | A push entry delivers records of a core kind that the kind's polled functions also deliver -- `firewall_log`'s -- and declares no `replaces`. |
| `push_replaces_unknown` | `replaces` names an endpoint the manifest does not declare. |
| `push_liveness_below_floor` | `liveness_seconds` is below 10. |
| `push_provider_missing` | A push entry writes a table of the plugin's own, and the plugin declares not exactly one provider with `source` push. |
| `push_column_unfilled` | A NOT NULL column of a push entry's table, or a required field of the kind record its field mapping fills, is filled by nothing. |
| `push_parse_missing` | A push entry names a parse function the script does not export under `push`. |
| `dataset_not_declared` | A script reads, through `host.datasets`, a dataset the manifest does not name under `datasets`. Refused at load when the script names it by a literal; a name computed at run time is refused at the call, a `TypeError` carrying the code. |
| `derivation_invalid` | A derivation names a function the script does not export, no trigger, a pass of no core kind, a push entry the plugin lacks, a dataset its manifest does not declare, an output that is not one of its tables (with a primary key) or measures, a name twice; or the plugin declares derivations and no provider. |
| `push_decode_missing` | A `datagram` or `line` entry names no decode function, or one the script does not export under `push`; or an entry of another protocol names one. |

## OPNsense versions

**Everything here is optional.** A plugin that maps no version runs as it is on every OPNsense
version, and nothing is asked of its author.

When an endpoint moves or changes shape between two OPNsense versions, a plugin keeps one name for
it and declares, per range of versions, the route that serves it there. The script is written once,
against the stable names, and never tests a version: the engine reads the firewall's version at
every discovery pass (from `/api/diagnostics/system/systemInformation`; see
`docs/opnsense-api-survey.md`, *Firewall versions*) and selects what matches, so an upgrade switches
the route with no restart. A range is `{"from": "26.1", "before": "26.7"}`: `from` included,
`before` excluded, either one alone for an open range. A version no range covers -- or a firewall
whose version is not known yet -- ends the call with `firewall_version_unsupported`, and nothing is
called: the closest range is never guessed. Two ranges sharing a version are refused at load
(`version_ranges_overlap`).

Each **variant** of an endpoint carries its range, its `path`, `method` and `body`, its own
`corrections`, its citation, and two ways to present its answer under the stable names:

- `fields` maps a name the script reads to the name the firewall writes on those versions, in every
  object of the answer;
- `adapter` names a function the script exports under `adapters`, handed the answer's value, already
  renamed, and returning the value the script reads, where a rename is not enough.

A whole script file may also be mapped to a range: `script_versions` is the main script's range,
and `scripts` lists the files of other ranges. Each file exports what the main script exports.

To the broker, each route is an endpoint of its own: a different path is a different datum, and the
API catalogue records which OPNsense version produced which shape.

A range naming an OPNsense plugin's package, `{"package": "os-example", "from": "1.2"}`, is refused
at load (`package_version_unavailable`): the one answer that reports installed package versions,
`/api/core/firmware/info`, makes the firewall refresh its package catalogue from its mirror, which
is an action and not a read, so `opnview` never calls it.

### A worked example

An example lease source whose endpoint is renamed in 26.7, with the hardware address renamed and
the answer wrapped in a `data` object, and whose 27.1 releases change enough to warrant a script of
their own:

```json
{
  "api_version": 2,
  "name": "example_versions",
  "version": "0.1.0",
  "display_name": "Example versions",
  "script": "example_versions.js",
  "script_versions": {"from": "26.1", "before": "27.1"},
  "scripts": [{"versions": {"from": "27.1"}, "script": "example_versions_next.js"}],
  "endpoints": [
    {
      "name": "leases",
      "path": "/api/examplelease/leases/search",
      "method": "GET",
      "versions": {"from": "26.1", "before": "26.7"},
      "variants": [
        {
          "versions": {"from": "26.7"},
          "path": "/api/examplelease/leases4/search",
          "method": "GET",
          "fields": {"hwaddr": "hw_address", "hostname": "host_name"},
          "adapter": "unwrap"
        }
      ]
    }
  ],
  "providers": [{"kind": "dhcp_lease", "display_name": "Example leases", "read_strategy": "full_reread"}]
}
```

The script reads `hwaddr` and `rows` on every version; on 26.7 the engine reads `hw_address` for
it, and the adapter lifts the rows out of `data`:

```js
exports.dhcp_lease = {
  probe: function (host) { /* ... */ },
  leases: function (host, pageSize) {
    var response = host.call("leases", {});
    var rows = opnview.decode.rows(response.value);
    /* ... the same on every version ... */
  }
};
exports.adapters = { unwrap: function (value) { return value.data; } };
```

Its tests hold one recorded answer per route and assert the same records from each:

```js
test("each route serves the same leases from its own recorded answer", function (t) {
  t.firewallVersion("26.1.4");
  t.answer("leases", { fixture: "leases_26_1.json" });
  var before = t.call("dhcp_lease", "leases", 100);
  t.firewallVersion("26.7.3");
  t.answer("leases", { fixture: "leases_26_7.json" });
  var after = t.call("dhcp_lease", "leases", 100);
  t.equal(after.leases, before.leases, "the two routes' leases");
});
```

## Widgets

A **widget** is what an interface draws and what `GET /api/widget/{type}` answers
(`docs/widget-api.md`). A plugin carries widgets in its `widgets/` directory, one directory per
widget, and lists them in its manifest:

```text
example_latency/
  plugin.json                          "widgets": ["example_latency_percentile"]
  widgets/example_latency_percentile/
    manifest.json                      the widget's manifest
    latency.statements.sql             its statements, by name
    latency.js                         its script: the logic a shape cannot express
  tests/*.test.js                      its tests, against a database of their own
```

A plugin may carry widgets and nothing else, or widgets beside its providers and tables. Dropped
into `<data-dir>/plugins/`, the widget is served at the next start at `/api/widget/<type>`, with
nothing compiled and no file of `opnview` edited. The built-in widgets written this way are
directories of `plugins/` in the repository, in the same format.

**Everything around the data is the core's.** Before the script runs, the core has parsed the
request's parameters against the manifest -- defaults applied, unknown keys reported, invalid values
reported and replaced by their defaults, required keys checked -- resolved the references, and read
the availability of the bound providers, the gaps and the window. After it returns, the core writes
the envelope (`docs/widget-api.md`, *The envelope*) and computes the condition. The script answers
the data, and records through its host what only it knows: the covered part of the window, a named
state, a withheld field.

### The widget manifest

`manifest.json` is the widget manifest every widget has, documented field by field in
`docs/widget-authoring.md`, *The manifest*: its `type` (equal to its directory's name), `shape`,
`providers`, `primary`, `windowed`, `no_domains`, `lower_bound`, `parameters` and `references`. A
widget plugin adds one key:

| Key | What it is |
|---|---|
| `script` | The JavaScript file, in the widget's own directory, that answers the widget. |
| `queries` | The statement files the script runs, in the widget's own directory. |
| `volumes` | `true` for a widget serving Volume or byte Count figures: the core gives it `volume_measure` and answers it in the measure that parameter chooses (*Volume figures and their measure*, below). |

A widget with no `script` is declarative: one query file, a single `SELECT`, whose columns the shape
engine turns into the data (`docs/widget-authoring.md`, *Shape contracts*). A key the manifest does
not define is refused (`manifest_invalid`), so a misspelt key never passes silently.

### Statements

A widget's statement files hold named statements, each beginning with a marker line:

```sql
-- statement: widget_example_latency_samples
SELECT target, latency_ms
FROM plugin_example_latency.probe
WHERE measured_at >= :from AND measured_at < :to
ORDER BY target, latency_ms;
```

- **A name is `widget_` and a snake_case word.** The script runs a statement by that name and no
  other: nothing else of the database is reachable from a script.
- **Parameters are `:name`.** The script binds each by name; one it leaves out is bound to `null`.
  The statement is translated to PostgreSQL's own numbering once, when the widget loads, never when
  it runs. PostgreSQL types each parameter from the place it stands in, as it types a literal; where
  the place does not type it -- `:x IS NULL`, `:a - :b`, a bare `SELECT :x` -- cast it:
  `CAST(:interfaces AS jsonb) IS NULL`.
- **`@period@` makes a template.** `volume_aggregate_@period@` reads the aggregates of the period
  the script names when it runs it: `1h`, `24h`, `7d` or `30d`.
- **Every column has a name of its own.** The script reads a row as an object of its columns, so a
  result naming one column twice is refused at run time; give each one with `AS`.
- **`<name>_measured` is `<name>`'s twin in every declared measure** (*Volume figures and their
  measure*): it binds exactly `<name>`'s parameters and `:measure`, and is a period template exactly
  when `<name>` is, or the widget is refused when it loads (`measured_twin_mismatch`).

Every statement is checked when the widget loads, at every period of a template: it is one `SELECT`
that PostgreSQL plans, and it reads none of `account`, `session`, `encrypted_credential`, `setting`
and `plugin_state`, which hold credentials, sessions and settings (`query_reads_credential`). It runs
on the read connections, whose role can neither write nor read those tables, under a statement time
bound.

### The script

The script exports `widget.serve`, handed the host and answering the data:

```js
exports.widget = {
  serve: function (host) {
    var rows = host.query.rows("widget_example_latency_samples",
      { from: host.request.window.from, to: host.request.window.to });
    return { data: { rows: rows }, has_rows: rows.length > 0 };
  }
};
```

- **`data`** is the response's `data`, written as JSON exactly as the script built it (below);
  `null` when there is nothing honest to show.
- **`has_rows`** says whether the data holds any row: `reachable` against `reachable_no_rows`, and,
  for a degraded source, whether there is stored history to show at all. A degraded widget with
  nothing stored answers `data: null` whatever the script returned.

Each request runs in a fresh interpreter, as a source plugin's call does. A request whose script
runs past twenty seconds is stopped. A script that throws, runs past its time or answers something
that is not `{data, has_rows}` answers `500` with `{"condition": "script_failed"}`, and the reason
goes to the service's log, never to the answer; a statement that fails is the store's failure,
`read_failed`, and a script cannot catch it.

**How the data is written.** The data is written as Go writes JSON, so a widget's answer is the same
bytes whichever side of the seam built it:

- **Members keep the order the script set them in.** An object whose keys are names of its own
  ("12", "beta") is a map: mark it with `host.json.map(object)` and it is written with its keys
  sorted. An object holding a key that looks like an array index ("12") is always written sorted,
  since JavaScript moves such keys to the front in numeric order and Go sorts them as text.
- **A number is written as Go writes a double**: `3` is `3`, `2.5` is `2.5`, a whole number is its
  digits. `NaN` and the infinities are refused.
- **A string is escaped as Go escapes it**: `<`, `>` and `&` are written `<`, `>`,
  `&`.
- **A member whose value is `undefined` is left out**, as an empty optional field is. A function, a
  `Date`, a symbol, an `undefined` array element and a cycle are refused.

### The widget host

The host is one object per area of the core, `host.<area>.<function>`. It grants the widget's own
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
| `rows(name, parameters, period)` | Every row of a statement the widget declares, each an object of its columns. `period` names the period of a template and is left out otherwise. Under a declared measure, a statement whose twin `<name>_measured` the widget declares is run as its twin, with the measure's name bound to `:measure` (*Volume figures and their measure*). |
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
| `declared` | The declared measures' names, in name order (*Measures*). |
| `measure` | The measure the request is answered under: `logged` or a declared measure. |

Summable figures are `{bytes, allowed_bytes, blocked_bytes, unknown_bytes, allowed_connections,
blocked_connections, unknown_connections}`; a member left out is `0`. Figures of a declared measure
also carry `measure`, its name, and `measure_parts`, its parts as one array (*Measures*): for each part
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
scripts read under `datasets` (*Datasets a plugin reads*); an installation without one answers a named
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
| `metrics` | Per health metric (`uptime`, `cpu`, `memory`, `swap`, `disk`, `temperature`, `gateway_latency`, `gateway_loss`), the subject kind and the measures it is stored under: `{kind, measures}`. |
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

### Volume figures and their measure

A volume figure is of one **measure**: `logged`, what the filter log recorded, or a measure a plugin
declares (*Measures*), such as the netflow plugin's `netflow`, the bytes and packets the firewall's
NetFlow export reported for the same logged connections, each direction counted once
(`docs/widget-api.md`, *Volumes and the observation-point limit*). Two measures are never summed, and
an answer carries one of them.

**The choice is the core's, and a widget names no measure.** A widget serving Volume or byte Count
figures declares `"volumes": true` in its manifest, and that is all:

- the core gives it the parameter `volume_measure` -- `logged`, every declared measure, or
  `best_available`, `best_available` by default -- which its manifest does not declare
  (`reserved_parameter_key` if it tries);
- the core decides, from what is stored, whether a measure can answer the window, runs the script in
  the measure chosen, and judges what it built: `best_available` answers a declared measure only when
  its window states are covered throughout and every connection the answer counts is covered -- the
  first such measure in name order -- and falls back to `logged` -- byte for byte what
  `volume_measure = logged` answers -- with the measure's named state that says why, and that state's
  detail in `state_details` when it carries one;
- every figure the core hands the script is already of that measure: `host.volume`, `host.clients`,
  `host.peers`, `host.sites`, `host.flows`, `host.series` and `host.attribution` read it, and
  `host.figures` builds every Volume and Count in the figures' own measure.

**What a script does to be answered in a declared measure.** It builds its figures through
`host.figures` and keeps what the core read whole:

- sum with `host.figures.add`, which keeps every member a figure carries, `measure` and
  `measure_parts` included, as the built-in `interface_volume_ranking` does;
- a count of connections is `host.figures.count(n)`, a byte count `host.figures.byteCount(figures)`;
  `host.figures.loggedCount(n)` stays a count of logged records;
- a number taken out of figures and drawn on its own -- a node of a tree -- is written with
  `host.figures.measure` as its measure, and the figures it came from are handed to
  `host.figures.cover`;
- a statement of the widget's own that reads volume columns declares ONE twin, `<name>_measured`,
  which serves every declared measure: the same parameters and the same columns, with the measure's
  bytes in place of the logged ones, plus `measure` (`CAST(:measure AS TEXT) AS measure`) and the
  measure's parts as `measure_parts`. A twin reading the flows joins each flow to every measure's
  figures through `flow_measures`, the one place they are read from, and sums them with
  `measure_parts_sum`; `measure_total_bytes(parts, measure)` and `measure_bytes(parts, measure, part)`
  read a measure's bytes. The built-in `owner_activity`, `traffic_composition` and `traffic_sankey`
  statement files show a twin over the flows; `internal/widget/traffic/buckets.statements.sql` one over
  an aggregate family's `measure_parts`.
- **a twin binds exactly its statement's parameters and `:measure`**, and is a period template exactly
  when its statement is: the core runs it in the statement's place with the statement's parameters and
  the measure's name. A widget whose twin does not is refused when it loads, with
  `measured_twin_mismatch` (`docs/widget-authoring.md`, *Refusals*).

**What happens to a widget that does not.** Nothing wrong is ever served. A run in a declared measure
that builds a figure the core did not read in it -- a non-zero logged figure, a logged count, a figure
the script wrote with `measure: "logged"` -- or that fails, is not served in that measure:
`best_available` answers the widget's logged answer with the measure's not-served state (for netflow,
`netflow_not_served_by_type`), and an explicit `volume_measure` naming the measure answers no figure and
the same state.

### Measures

A plugin whose derivation counts the logged connections a second way declares the **measure** in its
manifest's `measures` key; the core then serves it in every widget that declares `"volumes": true`,
adds it to `volume_measure`, and draws its `<measure>_bytes` and `<measure>_packets` in
`custom_chart`, with no line of Go. "Measure" and "marker" are `opnview`'s own terms
(`docs/data-model.md`, *Vocabulary*). The netflow plugin's manifest is the worked example.

| Key | What it is |
|---|---|
| `name` | The measure's name, a snake_case word: a value of `volume_measure` and of a figure's `measure`. Not one of the core's own (`logged`, `best_available`, `sampled`, `interface_counter`, `dns_lookups`). |
| `label`, `description` | Its catalogue key, `plugin.<plugin>.*`, and what it counts. |
| `provider_kind` | The kind of one of the plugin's providers whose active provider the measure needs. |
| `states` | The direction states, in the order a coverage object lists them, each `{name, covered}`: a direction in a state not covered uncovers its connection. At most 32. |
| `complete_state`, `refused_state` | The state a direction counts complete in, and the state the forward direction of a refused second leg is counted in. |
| `markers` | Named booleans a direction carries, counted in the coverage object. At most 16. |
| `classes` | The names of the four coverage classes: `complete`, `partial`, `unmeasured`, `not_known`. |
| `window_states` | The names of the window's states: `covered`, `unrecorded`, and the three the core decides -- `no_provider`, `not_covering`, `not_served` -- plus `own`, those its derivation records. |
| `best_available` | Whether `best_available` may answer in it. |

**What the derivation writes.** Per logged connection and direction, a row of the core's
per-connection output (`connection_measure`: the measure, the connection's first leg, the direction,
bytes, packets, records, the state and the markers), and the measure's window intervals
(`measure_interval`). The aggregate families, the purge and every twin read them through
`flow_measures`; adding a measure adds no column.

**The observation-point limit applies to every declared measure**: whatever a measure counts, it
counts at the firewall, and traffic between two clients behind one interface is in none of its
figures. This is documented, never printed on a screen.

A refused declaration is `measure_invalid`.

**A complete third-party example** is `internal/plugin/testdata/plugins/example_reporter`: JSON, JavaScript
and SQL alone -- a `datagram` push entry decoded by its script, a derivation run after each stored batch
and on a cadence that reads `leg`, `this_firewall_address` and `interface` and writes its measure's
per-connection rows and window interval, a measure with one covered and one uncovered state and one
marker, a settings page, and a widget declaring volumes whose statement declares its `_measured` twin.
`internal/plugin/thirdparty_test.go` loads it from a data directory and checks every figure it serves.

### Testing a widget

A plugin that carries widgets is tested against a PostgreSQL database of its own, as one that
declares tables is: the test instance `OPNVIEW_TEST_DATABASE` names, with `opnview`'s schema
applied. Besides the functions of *Testing a plugin*:

| Function | What it does |
|---|---|
| `t.sql(statement, parameters)` | Runs one statement on the test's database, to arrange the rows a widget reads. |
| `t.widget(type, parameters)` | Asks a widget as `GET /api/widget/{type}` asks it: `{status, condition, body, raw}`, `body` parsed and `raw` the exact bytes. A script that failed answers its reason in `body.reason`, which only a test sees. |
| `t.golden(file, raw)` | Compares an answer with `tests/<file>`, byte for byte once both are compact. Run the tests once with `OPNVIEW_WRITE_GOLDEN=1` in the environment to write the file instead, then read it before keeping it. |

The clock is the test's (`t.clock`), so a window and every instant in an answer are the same on
every run.

## The plugin API version

`api_version` is the version of everything this document describes: the manifest, the host, the
contracts and the test API. A change a plugin written for the current version would not survive
raises it, and a build reads a range of versions. A plugin targeting a version outside the range is
refused, naming both, so a format change is never half understood. This build reads version 2 alone:
version 2 made kinds, datasets, derivations, measures and pages declarations, and removed the
source-specific host helpers, so a version-1 plugin would not survive it, and no compatibility code
reads one.

## Kinds

A kind is a contract: what an implementation's script exports, and the fields of every record it
answers. The host converts what a script answers against exactly these tables. `flow_volume` is the
core's own and accepts no provider.

Every function answers a `result`:

<!-- contract:shared -->
#### `result`

What a probe or a read concluded: the availability opnview records for the provider. Every function returns one, under the key `result`.

| Field | Type | Required | Unit or values | Description |
|---|---|---|---|---|
| `state` | text | no | `reachable`, `present_but_disabled`, `unavailable` | The availability state the firewall's answer implies; absent only beside an error raised before any answer was read. |
| `outcome` | text | no |  | Why, as one of the closed outcome codes of docs/data-model.md, `source_availability`; absent only beside an error, as state is. |
| `probe` | text | yes |  | The name of the declared endpoint whose answer decided it. |
| `detail` | text | no |  | What happened, in words, for a person reading the availability. |
| `separable` | boolean | no |  | Whether the firewall's own configuration marks this implementation as the one serving clients; the probe round activates only a separable one. |
<!-- /contract:shared -->

<!-- contract:dhcp_lease -->
### `dhcp_lease`

The lease table of a DHCP server: data source 4. Several providers may be active at once (one server per scope); the kind runs the client identity cascade on what they report.

| Function | Called with | Returns | When |
|---|---|---|---|
| `probe` | `host` | `probe_answer` | Called at every probe round: asks the firewall whether this implementation can be read and whether its configuration serves clients. |
| `leases` | `host, page_size` | `leases_answer` | Called at every lease pass: the server's current lease table. |

#### `probe_answer`

What `probe` returns.

| Field | Type | Required | Unit or values | Description |
|---|---|---|---|---|
| `result` | record of `result` | yes |  | The `result` record. |
| `error` | text | no |  | Why the probe could not finish, when it could not. |

#### `leases_answer`

What `leases` returns.

| Field | Type | Required | Unit or values | Description |
|---|---|---|---|---|
| `result` | record of `result` | yes |  | The `result` record. |
| `leases` | records of `dhcp_lease_record` | no |  | The `dhcp_lease_record` records read, absent when the read failed. |
| `error` | text | no |  | Why the read failed, when it did; the result then says what the firewall answered. |
| `unsupported` | text | no |  | Why this implementation's material can never be read, when that is so: a state, recorded with its reason, and not a failure. |

#### `dhcp_lease_record`

One lease generation, with the field-name differences between backends normalised away.

| Field | Type | Required | Unit or values | Description |
|---|---|---|---|---|
| `address` | text | yes |  | The leased address. |
| `mac` | text | no |  | The hardware address; the host lower-cases it and stores a value that is not six colon-separated octets as absent. |
| `hostname` | text | no |  | The host name the client sent. |
| `dhcp_client_id` | text | no |  | The DHCP client identifier. |
| `duid` | text | no |  | The DHCPv6 DUID. |
| `iaid` | text | no |  | The DHCPv6 IAID. |
| `vendor_hint` | text | no |  | The vendor the server names for the address. |
| `lease_state` | text | yes | `active`, `expired`, `reserved`, `unknown` | The lease's state. |
| `interface_identifier` | text | no |  | The interface's configuration key, when the server names it. |
| `interface_device` | text | no |  | The interface's network device, when the server names it. |
| `starts_at` | integer | no | UTC epoch seconds | The validity start, only when the server reports one; never substituted. |
| `expires_at` | integer | no | UTC epoch seconds | The expiry. |
<!-- /contract:dhcp_lease -->

<!-- contract:dns_lookup -->
### `dns_lookup`

The lookups a resolver answered, per querying client: data source 5, the sole source of site names. The kind never presents what it read as coverage of a window.

| Function | Called with | Returns | When |
|---|---|---|---|
| `probe` | `host` | `probe_answer` | Called at every probe round: asks the firewall whether this implementation can be read and whether its configuration serves clients. |
| `lookups` | `host, page` | `lookups_answer` | Called for pages 1 to page_bound at every lookup pass. |

Values the script exports beside the functions:

#### `values`

The exported values.

| Field | Type | Required | Unit or values | Description |
|---|---|---|---|---|
| `page_bound` | integer | yes |  | How many pages one pass may walk, 1 or more. |
| `requested_span_seconds` | integer | yes | seconds | The window the implementation asks for, 0 or more; never presented as coverage. |

#### `probe_answer`

What `probe` returns.

| Field | Type | Required | Unit or values | Description |
|---|---|---|---|---|
| `result` | record of `result` | yes |  | The `result` record. |
| `error` | text | no |  | Why the probe could not finish, when it could not. |

#### `lookups_answer`

What `lookups` returns.

| Field | Type | Required | Unit or values | Description |
|---|---|---|---|---|
| `result` | record of `result` | yes |  | The `result` record. |
| `lookups` | records of `dns_lookup_record` | no |  | The `dns_lookup_record` records read, absent when the read failed. |
| `error` | text | no |  | Why the read failed, when it did; the result then says what the firewall answered. |
| `unsupported` | text | no |  | Why this implementation's material can never be read, when that is so: a state, recorded with its reason, and not a failure. |

#### `dns_lookup_record`

One lookup, decoded and normalised.

| Field | Type | Required | Unit or values | Description |
|---|---|---|---|---|
| `deduplication_key` | text | yes |  | The lookup's identity, composed by the implementation; a lookup read twice is stored once. |
| `client_address` | text | yes |  | The querying client's address, or the host name the resolver logged for it. |
| `client_family` | integer | no |  | 4 or 6: the querying address's family. |
| `domain` | text | yes |  | The name looked up. |
| `action` | text | yes | `pass`, `block`, `drop`, `unknown` | The resolver's verdict. |
| `answer_source` | text | no |  | Where the answer came from. |
| `rcode` | text | no |  | The response code. |
| `dnssec_status` | text | no |  | The DNSSEC status. |
| `blocklist_name` | text | no |  | The list that refused it, when one did and the resolver names it. |
| `looked_up_at` | integer | yes | UTC epoch seconds | When the lookup was made. |
<!-- /contract:dns_lookup -->

<!-- contract:firewall_log -->
### `firewall_log`

Records of packets the firewall allowed or denied: data source 1. The kind keeps no server-side cursor; it reads the newest page and records a gap when the oldest record returned is newer than the newest stored.

| Function | Called with | Returns | When |
|---|---|---|---|
| `probe` | `host` | `probe_answer` | Called at every probe round: asks the firewall whether this implementation can be read and whether its configuration serves clients. |
| `records` | `host, page_size` | `records_answer` | Called at every filter-log pass: the newest page_size records, newest first. |

Values the script exports beside the functions:

#### `values`

The exported values.

| Field | Type | Required | Unit or values | Description |
|---|---|---|---|---|
| `state_timeout_tcp_first_seconds` | integer | no | seconds | pf's timeout of a TCP state after its first packet (pf.conf `tcp.first`): how long a connection that never left its first packet stays idle and keeps its state. |
| `state_timeout_tcp_established_seconds` | integer | no | seconds | pf's timeout of an established TCP state (pf.conf `tcp.established`). |
| `state_timeout_tcp_closing_seconds` | integer | no | seconds | pf's timeout of a TCP state once a FIN was sent (pf.conf `tcp.closing`). |
| `state_timeout_tcp_closed_seconds` | integer | no | seconds | pf's timeout of a TCP state once an RST was sent (pf.conf `tcp.closed`). |
| `state_timeout_udp_multiple_seconds` | integer | no | seconds | pf's timeout of a UDP state with packets both ways (pf.conf `udp.multiple`). |
| `state_timeout_icmp_first_seconds` | integer | no | seconds | pf's timeout of an ICMP state after its first packet (pf.conf `icmp.first`). |
| `state_timeout_other_multiple_seconds` | integer | no | seconds | pf's timeout of a state of any other protocol with packets both ways (pf.conf `other.multiple`). |

#### `probe_answer`

What `probe` returns.

| Field | Type | Required | Unit or values | Description |
|---|---|---|---|---|
| `result` | record of `result` | yes |  | The `result` record. |
| `error` | text | no |  | Why the probe could not finish, when it could not. |

#### `records_answer`

What `records` returns.

| Field | Type | Required | Unit or values | Description |
|---|---|---|---|---|
| `result` | record of `result` | yes |  | The `result` record. |
| `records` | records of `firewall_log_record` | no |  | The `firewall_log_record` records read, absent when the read failed. |
| `error` | text | no |  | Why the read failed, when it did; the result then says what the firewall answered. |
| `unsupported` | text | no |  | Why this implementation's material can never be read, when that is so: a state, recorded with its reason, and not a failure. |

#### `firewall_log_record`

One filter-log record, decoded and normalised.

| Field | Type | Required | Unit or values | Description |
|---|---|---|---|---|
| `digest` | text | yes |  | The record's identity; a record read twice is stored once. |
| `observed_at` | integer | yes | UTC epoch seconds | When the packet was logged. |
| `device` | text | yes |  | The network device the record names; the kind resolves it to an interface. |
| `src_address` | text | yes |  | The source address. |
| `dst_address` | text | yes |  | The destination address. |
| `src_port` | integer | no |  | The source port, 0 to 65535. |
| `dst_port` | integer | no |  | The destination port, 0 to 65535. |
| `protocol` | text | yes |  | The protocol's name; empty when the record names none. |
| `ip_version` | integer | yes |  | 4 or 6. |
| `action` | text | yes | `pass`, `block`, `reject`, `unknown` | The decision. |
| `direction` | text | yes | `in`, `out`, `unknown` | The direction the packet crossed the interface. |
| `log_reason` | text | no |  | The reason the record was logged. |
| `packet_bytes` | integer | yes | bytes | The logged packet length, 0 or more. |
| `rid` | text | no |  | The rule's label, which the kind resolves to a rule. |
| `ip_id` | integer | no |  | The IPv4 identification, which pairs the two records of one connection. |
| `tcp_seq` | integer | no |  | The TCP sequence number, the other pairing field. |
<!-- /contract:firewall_log -->

A `firewall_log` implementation that knows its firewall's state timeouts exports them as the values above;
the core publishes the active provider's as the `provider_value` dataset (*Values another plugin reads*).
One that does not leaves them out: a reader then has no timeout, and decides nothing rather than assuming
one.

<!-- contract:flow_record -->
### `flow_record`

Flow records an exporter sends, received by a push entry with no script, and the exporter's capture configuration, read by polling. One provider is active at a time. The core joins each record from its own interface indexes, and reads coverage from the configuration, never from the records.

| Function | Called with | Returns | When |
|---|---|---|---|
| `probe` | `host` | `probe_answer` | Called at every probe round: asks the firewall whether this implementation can be read and whether its configuration serves clients. |
| `capture` | `host` | `capture_answer` | Called at every capture-configuration pass: the exporter's capture configuration now. |

#### `probe_answer`

What `probe` returns.

| Field | Type | Required | Unit or values | Description |
|---|---|---|---|---|
| `result` | record of `result` | yes |  | The `result` record. |
| `error` | text | no |  | Why the probe could not finish, when it could not. |

#### `capture_answer`

What `capture` returns: the configuration, or the reason it could not be read.

| Field | Type | Required | Unit or values | Description |
|---|---|---|---|---|
| `result` | record of `result` | yes |  | The `result` record; when the configuration could not be read, its outcome is what the firewall answered. |
| `capture` | record of `capture_configuration` | no |  | The configuration read, absent when the read failed. |
| `error` | text | no |  | Why the read failed, when it did: the configuration is then unreadable, and the core records it so. |
| `unsupported` | text | no |  | Why this exporter's configuration can never be read, when that is so. |

#### `capture_configuration`

What the exporter captures and sends.

| Field | Type | Required | Unit or values | Description |
|---|---|---|---|---|
| `enabled` | boolean | yes |  | Whether the export runs at all. |
| `export_version` | text | no |  | The export protocol's version, as the exporter names it. |
| `active_timeout_seconds` | integer | no | seconds | How long a flow that stays active is accounted before a record of it is exported. |
| `inactive_timeout_seconds` | integer | no | seconds | How long a flow stays idle before a record of it is exported. |
| `interfaces` | records of `capture_interface` | yes |  | The `capture_interface` records: every interface the exporter captures on, and nothing else. |

#### `capture_interface`

One interface the exporter captures on.

| Field | Type | Required | Unit or values | Description |
|---|---|---|---|---|
| `identifier` | text | yes |  | The interface's configuration identifier, as discovery names it. |
| `ingress` | boolean | yes |  | Whether packets received on the interface are accounted. |
| `egress` | boolean | yes |  | Whether packets sent on the interface are accounted. |
| `exported` | text | no | `yes`, `no`, `unknown` | Whether the exporter actually exports what is configured for the interface, for each captured direction: yes, no (with not_exported_reason), or unknown when it could not be established. Absent is unknown. |
| `not_exported_reason` | text | no |  | Why the interface is not exported, exactly when exported is no: the provider's own closed name for it. |
| `parent_of` | text_list | no |  | The identifiers of the interfaces whose received packets this interface's exporter also exports with this interface as the receiving interface. Absent is none. |

#### `flow_record_record`

One flow record as the exporter sent it, filled from the push entry's Information Element mapping. Its identity, and what its interface indexes name, are the core's.

| Field | Type | Required | Unit or values | Description |
|---|---|---|---|---|
| `exporter_address` | text | yes |  | The exporter's address. |
| `observation_domain` | integer | yes |  | The observation domain (NetFlow v9's source ID). |
| `ingress_interface` | integer | no |  | The exporter's index of the interface the packets were received on, raw. |
| `egress_interface` | integer | no |  | The exporter's index of the interface the packets were routed to, raw. |
| `ip_version` | integer | no |  | 4 or 6; absent when the exporter sends no version element, the core then reading it from the two addresses, which must agree with it. |
| `protocol` | integer | yes |  | The IANA protocol number. |
| `src_address` | text | yes |  | The source address. |
| `dst_address` | text | yes |  | The destination address. |
| `src_port` | integer | no |  | The source port, 0 to 65535; absent for a protocol without ports. |
| `dst_port` | integer | no |  | The destination port, 0 to 65535; absent for a protocol without ports. |
| `tcp_flags` | integer | no |  | The TCP flags of every packet, ORed together. |
| `octets` | integer | yes | bytes | The octets the record accounts, 0 or more. |
| `packets` | integer | yes |  | The packets the record accounts, 0 or more. |
| `start_at` | number | yes | UTC epoch seconds | The first packet's instant, with the fraction the exporter's precision carries. |
| `end_at` | number | yes | UTC epoch seconds | The last packet's instant, with the fraction the exporter's precision carries. |
| `received_at` | number | yes | UTC epoch seconds | When opnview received the record. |
<!-- /contract:flow_record -->

<!-- contract:ip_geolocation -->
### `ip_geolocation`

Places public addresses: each address's country, coordinates, autonomous system and operator. One provider is active at a time. The core decides which addresses need placing -- never a non-public one -- stores the answers in geo_asn and places every address again when the active provider or its build changes.

| Function | Called with | Returns | When |
|---|---|---|---|
| `status` | `host` | `dataset_answer` | Called at start and after every refresh: whether a dataset is held, and its build. |
| `refresh` | `host` | `dataset_answer` | Called at refresh_interval_geoip_seconds: acquire or update the dataset. |
| `lookup` | `host, addresses` | `lookup_answer` | Called at poll_interval_geo_lookup_seconds with a batch of public addresses. |

#### `dataset_answer`

What `status` and `refresh` return.

| Field | Type | Required | Unit or values | Description |
|---|---|---|---|---|
| `result` | record of `result` | yes |  | The `result` record; its probe is a word naming what the last refresh found. |
| `ready` | boolean | yes |  | Whether a dataset is held and answers lookups. |
| `build` | integer | no | UTC epoch seconds | The build of the dataset held, which every answer is recorded against. |
| `error` | text | no |  | Why the refresh failed, when it did. |

#### `lookup_answer`

What `lookup` returns.

| Field | Type | Required | Unit or values | Description |
|---|---|---|---|---|
| `build` | integer | yes | UTC epoch seconds | The build the answers come from. |
| `answers` | records of `geolocation` | yes |  | One `geolocation` record per address asked about. |
| `error` | text | no |  | Why the lookups failed, when they did. |

#### `geolocation`

What the dataset says about one address.

| Field | Type | Required | Unit or values | Description |
|---|---|---|---|---|
| `address` | text | yes |  | The address asked about. |
| `country_code` | text | no |  | The ISO 3166-1 country code; absent is a miss. |
| `country_name` | text | no |  | The country's English name. |
| `latitude` | number | no |  | The latitude. |
| `longitude` | number | no |  | The longitude. |
| `asn` | integer | no |  | The autonomous system number. |
| `operator` | text | no |  | The autonomous system's organisation. |
<!-- /contract:ip_geolocation -->

<!-- contract:measurement_sample -->
### `measurement_sample`

A numeric reading of a subject at an instant: the firewall's gauges, sampled rates. Several providers may be active at once. A reading that did not answer is named in `missing`, never written as a zero.

| Function | Called with | Returns | When |
|---|---|---|---|
| `probe` | `host` | `probe_answer` | Called at every probe round: asks the firewall whether this implementation can be read and whether its configuration serves clients. |
| `sample` | `host, discovery, now` | `sample_answer` | Called at every sampling pass. discovery holds the interfaces the firewall reported (identifiers, devices); now is the pass's instant. |

#### `probe_answer`

What `probe` returns.

| Field | Type | Required | Unit or values | Description |
|---|---|---|---|---|
| `result` | record of `result` | yes |  | The `result` record. |
| `error` | text | no |  | Why the probe could not finish, when it could not. |

#### `sample_answer`

What `sample` returns.

| Field | Type | Required | Unit or values | Description |
|---|---|---|---|---|
| `result` | record of `result` | yes |  | The `result` record. |
| `readings` | records of `measurement_reading` | no |  | The `measurement_reading` records. |
| `missing` | text_list | no |  | The readings that did not answer, each named in words for the availability detail. |
| `error` | text | no |  | Why the pass failed, when it did. |

#### `measurement_reading`

One reading.

| Field | Type | Required | Unit or values | Description |
|---|---|---|---|---|
| `subject_kind` | text | yes |  | What is measured: a lower-case word with no space (interface, interface_endpoint, gateway, firewall, or the plugin's own). |
| `subject_key` | text | yes |  | Which one; may be empty. |
| `measure` | text | yes |  | What figure: a lower-case word with no space. |
| `unit` | text | yes |  | Its unit: a lower-case word with no space. |
| `value` | number | yes |  | The figure. |
| `sampled_at` | integer | yes | UTC epoch seconds | When it was read. |
| `attributed` | boolean | no |  | Whether the reading names this provider; false for the firewall reporting on itself. True when absent. |
<!-- /contract:measurement_sample -->

<!-- contract:reconciled_state -->
### `reconciled_state`

The complete set of things of one type as of one instant: a ban list, the peers of a tunnel. A thing present in one snapshot and absent from the next has left. Several providers may be active at once.

| Function | Called with | Returns | When |
|---|---|---|---|
| `probe` | `host` | `probe_answer` | Called at every probe round: asks the firewall whether this implementation can be read and whether its configuration serves clients. |
| `snapshot` | `host` | `snapshot_answer` | Called at the plugin's cadence: every set the provider reports, each complete. |

#### `probe_answer`

What `probe` returns.

| Field | Type | Required | Unit or values | Description |
|---|---|---|---|---|
| `result` | record of `result` | yes |  | The `result` record. |
| `error` | text | no |  | Why the probe could not finish, when it could not. |

#### `snapshot_answer`

What `snapshot` returns.

| Field | Type | Required | Unit or values | Description |
|---|---|---|---|---|
| `result` | record of `result` | yes |  | The `result` record. |
| `sets` | records of `state_set` | no |  | The `state_set` records. |
| `error` | text | no |  | Why the read failed, when it did. |

#### `state_set`

One complete set.

| Field | Type | Required | Unit or values | Description |
|---|---|---|---|---|
| `set_key` | text | yes |  | Which set: a lower-case word with no space. |
| `items` | records of `state_item` | yes |  | The `state_item` records; an empty list is a set that is now empty. |

#### `state_item`

One member of a set.

| Field | Type | Required | Unit or values | Description |
|---|---|---|---|---|
| `item_key` | text | yes |  | Its identity within the set. |
| `attributes` | object | no |  | The provider's own fields, displayed and never aggregated. |
| `valid_until_at` | integer | no | UTC epoch seconds | The end of its own validity, when the provider states one. |
<!-- /contract:reconciled_state -->

<!-- contract:registrable_domain -->
### `registrable_domain`

Groups site names by their registrable domain, at read time; no stored name is ever altered. One provider is active at a time; with none, no grouping is offered.

| Function | Called with | Returns | When |
|---|---|---|---|
| `status` | `host` | `list_answer` | Called at start and after every refresh: whether a list is held. |
| `refresh` | `host` | `list_answer` | Called at refresh_interval_public_suffix_list_seconds: acquire or update the list. |
| `group` | `host, names` | `group_answer` | Called while a widget is served, with the site names to group. |

#### `list_answer`

What `status` and `refresh` return.

| Field | Type | Required | Unit or values | Description |
|---|---|---|---|---|
| `result` | record of `result` | yes |  | The `result` record; its probe is a word naming what the last refresh found. |
| `ready` | boolean | yes |  | Whether a list is held. |
| `version` | text | no |  | The list's own version, when it carries one. |
| `rules` | integer | no |  | How many rules the list holds. |
| `confirmed_at` | integer | no | UTC epoch seconds | The last instant a refresh confirmed the list current. |
| `failed` | boolean | no |  | Whether this refresh failed, which the core records as a collection gap from confirmed_at. |
| `error` | text | no |  | Why the refresh failed, when it did. |

#### `group_answer`

What `group` returns.

| Field | Type | Required | Unit or values | Description |
|---|---|---|---|---|
| `groups` | records of `grouping` | yes |  | One `grouping` record per name. |
| `error` | text | no |  | Why the grouping failed, when it did. |

#### `grouping`

One name's registrable domain.

| Field | Type | Required | Unit or values | Description |
|---|---|---|---|---|
| `name` | text | yes |  | The site name, verbatim. |
| `registrable_domain` | text | no |  | Its registrable domain; absent when the name has none, which groups it on its own. |
<!-- /contract:registrable_domain -->

<!-- contract:resolver_cache -->
### `resolver_cache`

The records a resolver holds in its cache, with the seconds each has left. The kind stores A, AAAA and CNAME records as observations and counts every other type.

| Function | Called with | Returns | When |
|---|---|---|---|
| `probe` | `host` | `probe_answer` | Called at every probe round: asks the firewall whether this implementation can be read and whether its configuration serves clients. |
| `cache` | `host` | `dump_answer` | Called at every cache pass: every record the cache holds now. |

#### `probe_answer`

What `probe` returns.

| Field | Type | Required | Unit or values | Description |
|---|---|---|---|---|
| `result` | record of `result` | yes |  | The `result` record. |
| `error` | text | no |  | Why the probe could not finish, when it could not. |

#### `dump_answer`

What `cache` and `local_data` return.

| Field | Type | Required | Unit or values | Description |
|---|---|---|---|---|
| `result` | record of `result` | yes |  | The `result` record. |
| `records` | records of `resolver_record` | no |  | The `resolver_record` records, of every type. |
| `response_bytes` | integer | no | bytes | The size of the answer. |
| `not_records` | records of `not_record_count` | no |  | The `not_record_count` records: rows the implementation recognised as not being records at all. |
| `error` | text | no |  | Why the read failed, when it did. |
| `unsupported` | text | no |  | Why this resolver's records can never be read, when that is so. |

#### `resolver_record`

One record as the endpoint returned it.

| Field | Type | Required | Unit or values | Description |
|---|---|---|---|---|
| `owner_name` | text | yes |  | The owner name. |
| `ttl` | text | no |  | The TTL as text. |
| `ttl_shape` | text | yes | `absent`, `null`, `string`, `other` | The JSON shape the TTL arrived in. |
| `rrtype` | text | yes |  | The record type. |
| `value` | text | yes |  | The record's value. |

#### `not_record_count`

A number of rows that are not records.

| Field | Type | Required | Unit or values | Description |
|---|---|---|---|---|
| `count` | integer | yes |  | How many. |
| `wording` | text | yes |  | What they are, worded so that "skipped <count> <wording>" reads as a sentence. |
<!-- /contract:resolver_cache -->

<!-- contract:resolver_local_data -->
### `resolver_local_data`

The records a resolver serves from its local data, host overrides among them.

| Function | Called with | Returns | When |
|---|---|---|---|
| `probe` | `host` | `probe_answer` | Called at every probe round: asks the firewall whether this implementation can be read and whether its configuration serves clients. |
| `local_data` | `host` | `dump_answer` | Called at every local-data pass: every record of the local data now. |

#### `probe_answer`

What `probe` returns.

| Field | Type | Required | Unit or values | Description |
|---|---|---|---|---|
| `result` | record of `result` | yes |  | The `result` record. |
| `error` | text | no |  | Why the probe could not finish, when it could not. |

#### `dump_answer`

What `cache` and `local_data` return.

| Field | Type | Required | Unit or values | Description |
|---|---|---|---|---|
| `result` | record of `result` | yes |  | The `result` record. |
| `records` | records of `resolver_record` | no |  | The `resolver_record` records, of every type. |
| `response_bytes` | integer | no | bytes | The size of the answer. |
| `not_records` | records of `not_record_count` | no |  | The `not_record_count` records: rows the implementation recognised as not being records at all. |
| `error` | text | no |  | Why the read failed, when it did. |
| `unsupported` | text | no |  | Why this resolver's records can never be read, when that is so. |

#### `resolver_record`

One record as the endpoint returned it.

| Field | Type | Required | Unit or values | Description |
|---|---|---|---|---|
| `owner_name` | text | yes |  | The owner name. |
| `ttl` | text | no |  | The TTL as text. |
| `ttl_shape` | text | yes | `absent`, `null`, `string`, `other` | The JSON shape the TTL arrived in. |
| `rrtype` | text | yes |  | The record type. |
| `value` | text | yes |  | The record's value. |

#### `not_record_count`

A number of rows that are not records.

| Field | Type | Required | Unit or values | Description |
|---|---|---|---|---|
| `count` | integer | yes |  | How many. |
| `wording` | text | yes |  | What they are, worded so that "skipped <count> <wording>" reads as a sentence. |
<!-- /contract:resolver_local_data -->

<!-- contract:security_event -->
### `security_event`

Intrusion-detection events: data source 2. Several providers may be active at once; every event names the provider that reported it, and the kind keeps a watermark per file.

| Function | Called with | Returns | When |
|---|---|---|---|
| `probe` | `host` | `probe_answer` | Called at every probe round: asks the firewall whether this implementation can be read and whether its configuration serves clients. |
| `rotation` | `host` | `rotation_answer` | Called before every read: which of the feed's files still exist, so a lost file is recorded as a permanent gap. |
| `alerts` | `host, page_size` | `alerts_answer` | Called at every security-event pass: the newest page_size events, newest first. |

#### `probe_answer`

What `probe` returns.

| Field | Type | Required | Unit or values | Description |
|---|---|---|---|---|
| `result` | record of `result` | yes |  | The `result` record. |
| `error` | text | no |  | Why the probe could not finish, when it could not. |

#### `rotation_answer`

What `rotation` returns.

| Field | Type | Required | Unit or values | Description |
|---|---|---|---|---|
| `result` | record of `result` | yes |  | The `result` record. |
| `known` | boolean | yes |  | Whether the files could be listed; false declares nothing lost. |
| `present` | text_list | no |  | The file identifiers the feed still carries. |
| `highest_sequence` | integer | no |  | The current file's sequence, when the feed reports one. |
| `error` | text | no |  | Why the listing failed, when it did. |

#### `alerts_answer`

What `alerts` returns.

| Field | Type | Required | Unit or values | Description |
|---|---|---|---|---|
| `result` | record of `result` | yes |  | The `result` record. |
| `alerts` | records of `security_event_alert` | no |  | The `security_event_alert` records read, absent when the read failed. |
| `error` | text | no |  | Why the read failed, when it did; the result then says what the firewall answered. |
| `unsupported` | text | no |  | Why this implementation's material can never be read, when that is so: a state, recorded with its reason, and not a failure. |

#### `security_event_alert`

One event, decoded and normalised.

| Field | Type | Required | Unit or values | Description |
|---|---|---|---|---|
| `file_id` | text | yes |  | The file the event was read from; with byte_offset, the coordinate the watermark keeps. |
| `byte_offset` | integer | yes |  | Where in the file. |
| `occurred_at` | integer | yes | UTC epoch seconds | When the event happened. |
| `rule_identity` | text | yes |  | The rule or signature's identifier. |
| `signature` | text | yes |  | The signature's text; empty when the event carries none. |
| `event_action` | text | yes | `blocked`, `allowed`, `unknown` | What the engine did. |
| `normalised_severity` | text | no |  | The severity, when the event carries one. |
| `src_address` | text | yes |  | The source address. |
| `dst_address` | text | yes |  | The destination address. |
| `src_port` | integer | no |  | The source port, 0 to 65535. |
| `dst_port` | integer | no |  | The destination port, 0 to 65535. |
| `protocol` | text | no |  | The protocol. |
| `in_interface_device` | text | no |  | The device the event was seen on. |
<!-- /contract:security_event -->

## Push sources

Most of what a plugin reads it **asks** the firewall for, through the broker. A push source is data
the firewall **sends** to `opnview` once the user has configured it to: syslog lines (RFC 5424 and
RFC 3164, over UDP and TCP), NetFlow records (version 9, RFC 3954; IPFIX, RFC 7011, over UDP), and
messages the core reads nothing in -- a UDP datagram (`datagram`), or a line of text over TCP or UDP
(`line`) -- which the subscribing plugin's own decode function reads.
`opnview` never configures the export: what the user does on OPNsense, and what OPNsense sends, is in
`docs/opnsense-api-survey.md`, *Push sources: remote syslog and NetFlow export, read from source for
push sources*, and in the README. Receiving is inbound: a listener makes no outbound call, and the
broker stays the only path from `opnview` to the firewall's API.

**The core knows the protocols and nothing of any program.** It holds the listeners, one decoder per
protocol -- the syslog header; NetFlow and IPFIX templates, every field named and typed by the IANA
IPFIX Information Elements registry -- and the routing. A message is decoded **once**, whoever
subscribes; a syslog line no plugin subscribes to costs one header decode and one index lookup, runs
no regular expression and no script, and is counted (`no_subscriber`) and dropped. What a program's
message says, and which columns it fills, is the plugin's, declared under `push`.

### Listening

A listener is opened by a start parameter and by nothing else. **It has no default**, for the reason
`--listen` has none: a port is one installation's configuration.

```text
opnview ... --push-listen syslog/tcp=<host:port> --push-listen syslog/udp=<host:port> --push-listen netflow/udp=<host:port> --push-listen datagram/udp=<host:port> --push-listen line/tcp=<host:port>
```

Each is optional and repeatable. With none, no push socket is opened, and every push declaration's
availability reads `listener_not_configured`: nothing can arrive, and the silence is not an absence of
data.

### The declaration

```json
"push": [{
  "name": "logins",
  "protocol": "syslog",
  "filter": {"program": ["sshd"]},
  "pattern": "^Accepted (?P<method>\\S+) for (?P<user>\\S+) from (?P<client>\\S+)",
  "output_table": "logins"
}]
```

| Key | What it is |
|---|---|
| `name` | The entry's name, a snake_case word: the parse function's name, and the catalogue's. |
| `protocol` | `syslog`, `netflow`, `datagram` or `line`. |
| `filter` | The header filter, in the protocol's form. Syslog: `{"program": [...], "facility": [...], "severity": [...]}`, the program -- RFC 3164's TAG, RFC 5424's APP-NAME -- by exact name and required, facility and severity by RFC 5424's names. NetFlow: `{"require": [...], "where": [...]}`, the Information Elements a row is built from, by registry name, and conditions `{element, operator, value or values}` with the data catalogue's operators (`eq`, `ne`, `lt`, `le`, `gt`, `ge`, `in`, `not_in`, `is_null`, `is_not_null`); a record lacking a required element is skipped for that entry and counted (`netflow_required_element_missing`). `datagram` and `line`: `{}`, or `{"prefix": "..."}` to take only the messages whose octets begin with it. |
| `transports` | The transports the entry accepts: `udp`, `tcp`; every one its protocol is received over by default. |
| `pattern` | Syslog: a regular expression over the message, in RE2 syntax (Go's `regexp`: `(?P<name>...)` names a group; no backreference and no lookaround, because the input is network data and RE2 matches in linear time). Each named group fills the column of its name. A message it does not match is counted (`pattern_unmatched`). |
| `parse` | Syslog: the name of a function the script exports under `push`, for a message a regular expression cannot express. Never with `pattern`. |
| `fields` | Columns -- or, for a `kind` entry, the fields of the kind's record -- filled from fields every record carries: a syslog header field (`facility`, `severity`, `timestamp`, `hostname`, `program`, `process_id`, `message_id`, `structured_data`, `message`), or a NetFlow Information Element by registry name -- one name, or a list whose first present one wins. A `kind` entry's mapping names every required field of the record, and runs no script. |
| `decode` | `datagram` and `line` only, and required there: a function the script exports under `push`, called with ONE message (*The decode function*). Never with `pattern`, `parse` or `fields`. |
| `sequence_bound` | `{"value": "<name>"}`: the value, recorded over time by whatever reads the sender's configuration, that bounds the sequence counters the sender runs (*Liveness and gaps*). Absent asserts every loss its sender's sequence numbers show. |
| `output_table` | One of the plugin's tables, which receives the rows. |
| `kind` | Instead, a core kind the plugin provides, whose records the parse function builds or the `fields` mapping fills (see *Replacing polling*). Never with `output_table`. |
| `replaces` | One of the plugin's endpoints, whose polling this entry replaces while it is alive. |
| `liveness_seconds` | How long the entry stays alive with no message: 120 by default, 10 at least. |
| `survey_section` | The section of the survey that establishes what the firewall sends; a built-in carries it. |

**Where a row's values come from.** A row of an `output_table` entry is filled by its pattern's groups
and its `fields`, or by its parse function; the table's time column, when nothing else names it, is
filled with the record's own instant. Every NOT NULL column must be filled by one of these, or the
declaration is refused (`push_column_unfilled`).

**An `output_table` entry belongs to a provider.** The plugin declares a kind of its own with `push`
(*Kinds of the plugin's own*) and one provider of it with `"source": "push"`: it has no probe, no read and no script function, its availability is the
entries' -- `listener_not_configured`, `no_message_received` within the liveness span, or healthy --
and its gaps are the entries'. The table's dataset is dated by it.

```json
"kinds": [{"kind": "example_logins", "push": true}],
"providers": [{"kind": "example_logins", "display_name": "Example logins", "source": "push"}]
```

### The type vocabulary

A column's `type` is a word of a closed list, for every plugin table, push or not:

| Plugin type | PostgreSQL type | A value is |
|---|---|---|
| `integer` | `bigint` | a whole number; an unsigned 64-bit value above the signed range is refused, never wrapped |
| `real` | `double precision` | a number |
| `text` | `text` | text |
| `boolean` | `boolean` | `true` or `false` |
| `ip` | `inet` | an IPv4 or IPv6 address |
| `mac` | `macaddr` | a 48-bit MAC address |
| `instant` | `timestamptz` | an instant, at the precision the source has |

There is no JSON type: every captured property is its own column. A push output table's time column
is an `instant` (`push_table_without_instant` otherwise), named `*_at` like every instant of the
schema, and the table is a hypertable chunked by day on it. The JSON API still serves every instant
as an integer of UTC epoch seconds, and the data catalogue reads a boolean, an address and a MAC
address as text. In a table a push entry writes, a column with no `role` takes the one its type
implies: an `instant` is time, a number a measure, anything else a dimension; and the dataset's
`nature` is `event` unless it says otherwise.

A value that does not convert to its column's type is null in a nullable column, and refuses the row
in a NOT NULL one; it is counted as `push_value_invalid` either way, never coerced. A NetFlow field is
typed by its registry abstract type:

| IANA abstract data type | Plugin type |
|---|---|
| `unsigned8` ... `unsigned64`, `signed8` ... `signed64` | `integer` |
| `float32`, `float64` | `real` |
| `ipv4Address`, `ipv6Address` | `ip` |
| `macAddress` | `mac` |
| `dateTimeSeconds`, `dateTimeMilliseconds`, `dateTimeMicroseconds`, `dateTimeNanoseconds` | `instant` |
| `string` | `text` |
| `boolean` | `boolean` |
| `octetArray`, `unsigned256` and the list types | `text`, in lower-case hexadecimal |

An enterprise-specific element is exposed as an octet array under `enterprise_<number>_<id>`. Beside
the template's fields, every NetFlow record carries `exporterIPv4Address` or `exporterIPv6Address`,
`observationDomainId`, `exportProtocolVersion` and `collectionTimeMilliseconds` (the reception), and
`flowStartMilliseconds` / `flowEndMilliseconds` computed from v9's `FIRST_SWITCHED` / `LAST_SWITCHED`
and the header's `unix_secs` and `sysUptime`, or from IPFIX's absolute or delta time elements.

**Interface indexes.** `ingressInterface` and `egressInterface` are the exporter's interface
indexes. Before a batch reaches its entry, each is mapped to the firewall's interface through the
broker -- `GET /api/interfaces/overview/interfaces_info/1`, whose `statistics.index` is that index (survey,
*Push sources*, S6) -- and the record gains `<field>_identifier` and `<field>_device` (text), the
mapping in force at reception. An index no interface holds keeps its raw value, gains neither, and is
counted (`interface_index_unmapped`); an index is never matched by position, name or count.

### The parse function

```js
exports.push = {
  logins: function (messages) {
    return messages.map(function (message) { /* one row, or null */ });
  }
};
```

It is called with a batch of at most 500 decoded messages -- never once per message -- in a fresh
interpreter, like every call. Each message is the header's fields (`facility`, `severity`, `timestamp`,
`hostname`, `program`, `process_id`, `message_id`, `structured_data`, `message`), with `sender`,
`transport` and `received_at`; every instant is UTC epoch seconds with its fraction. It answers a
list of the batch's length: for each message a row of the output table (or, for a `kind` entry, a
record of the kind's contract), or `null` to skip it (`skipped`). A call that throws, runs past its
time bound, or answers anything but such a list fails that batch alone: its messages are counted
(`script_failed`, `script_timeout`, `records_invalid`) and recorded as a gap (`push_script_failed`),
and the next batch is processed. The regular expression stays the fast path: it calls no script. A
plugin that maps OPNsense versions to scripts (*OPNsense versions*) has its parse function taken from
the script of the firewall's version.

### The decode function

```js
exports.push = {
  readings: function (message) {
    return [/* the rows, or the kind's records, the message held */];
  }
};
```

A `datagram` or `line` entry's function is called once per message -- one datagram, one line -- in a
fresh interpreter. The message is `{bytes, text, sender, transport, received_at}`: its octets, each 0 to
255; the same read as UTF-8; the sender's address; `udp` or `tcp`; the reception instant as UTC epoch
seconds with its fraction, which also fills the table's time column when the row does not. It answers a
list -- empty for a message holding nothing -- of rows of the output table, or records of the entry's
`kind`, each checked as a parse function's are. A call that throws, runs past its bound or answers
anything but a list loses that message alone: counted, and recorded as a `push_script_failed` gap. A
`line` over TCP is one frame per line (the newline, and a carriage return before it, are not part of the
line); a `line` datagram may hold several lines, each one message. Neither protocol carries a sequence
number the core reads, so their loss is detectable over TCP alone, and a `kind` entry is received over TCP
alone. `internal/plugin/testdata/example_decoded` is a complete example.

### Instants

An RFC 5424 timestamp, or an ISO one carrying its offset, is read as it stands. An RFC 3164 timestamp
-- no year, no zone -- and an ISO one with no offset are read with the firewall's measured offset from
UTC and the year rule of the filter log's timestamps, a line of 31 December read on 1 January
included. The reception instant is recorded for liveness and gaps, and never substituted for a
header instant that parsed.

### Senders

**Only the configured firewall may send.** The accepted addresses are derived at run time, never
written down: the addresses the configured base URL's host resolves to, and every address the
firewall's own interfaces hold as discovery read them. They are derived again every minute, with no
restart. A datagram or a connection from any other address is refused before it is decoded, nothing
of it is stored, and each refusal is counted per address in the API catalogue -- so a container
network that rewrites source addresses is visible at once. **This is a source-address check. It
refuses every other sender; it does not authenticate the firewall, and a forged source address on the
path passes it.**

### Liveness and gaps

An entry is **alive** while messages it accepts have arrived within its liveness span. It stops being
alive only after the span and half of it again, so a sender close to its span does not flip at every
message. The interval during which an entry was not alive is recorded as a gap of its provider
(`push_not_alive`), so a bucket outside it is a gap and never a zero.

**Every queue is bounded, and every drop is a gap.** A drop is counted and recorded as a gap of the
entry's provider, merged with the drops of the same reason less than ten seconds apart:

| Reason | What it records |
|---|---|
| `push_buffer_overflow` | `opnview`'s own queues were full: a datagram found its listener's decode queue full, or a record its entry's queue |
| `push_store_failed` | a batch that could not be committed after its retries |
| `push_listener_down` | the interval during which the listener was not listening, `opnview`'s own downtime between runs included |
| `push_script_failed` | a parse function call that failed, over its messages' reception instants |
| `push_not_alive` | the entry received nothing for longer than its liveness span |
| `netflow_sequence_gap` | a jump in an exporter's sequence numbers, between the export times on either side of it |
| `netflow_template_missing` | NetFlow data whose template never arrived within the bound |

The operating system's own receive-queue drops are not read.

**Loss on the wire.** Syslog over UDP carries no sequence number: a datagram lost between the
firewall and `opnview` is invisible. Every dataset a push entry feeds therefore carries
`loss_detectable`: true when every transport the entry accepts detects its loss -- TCP syslog,
NetFlow -- and false when it accepts UDP syslog.

**A NetFlow exporter running several counters.** One observation domain may carry several sequence
counters -- OPNsense runs one per exporting interface behind one forwarder (survey, *Push sources*,
S11). The decoder follows several: a datagram continues the counter expecting its number, or opens
one. A `netflow_sequence_gap` is asserted only once more counters are open than the exporter runs,
which the entry's `sequence_bound` names -- the netflow plugin's is the value `sequence_counters`, which
its provider records from the capture configuration's exporting interfaces after every read -- and the
core passes to the decoder; a decoder told nothing assumes one counter. A loss is asserted only while one
recorded bound holds both its ends.
With that number unknown no gap is asserted: it is counted as `sequence_gap_not_asserted`, and the
records of that interval say `loss_detectable` 0. The counters are followed afresh when the
configuration changes, and when the exporter's uptime goes backwards -- a restart, which the decoder
tells the subscribers as the condition `netflow_exporter_restarted`, and after which the core reads
the index map afresh before it maps the next record.

| Bound | Value |
|---|---|
| UDP receive buffer asked of the system | 4 MiB |
| One UDP datagram | 65 535 octets |
| One TCP frame | 64 KiB; a longer frame closes its connection (`frame_too_large`) |
| TCP connections per listener | 16 (`connections_refused` beyond), closed after 10 minutes of silence |
| Datagrams waiting for a decoder | 4 096 |
| Records waiting for one entry | 50 000 |
| One batch | 1 000 records, or what arrived within one second |
| NetFlow data sets held for their template | 1 024 per exporter and domain, 60 seconds |
| NetFlow templates per exporter | 256 (`netflow_template_limit` beyond) |
| Retries of a failing batch | 3, from 250 ms, doubling |

### Replacing polling

An entry with `kind` delivers records of a core kind the plugin provides: its parse function builds them,
or its `fields` mapping fills them with no script, as records of the kind's contract (*Kinds*), checked
as a pulled answer is. It is accepted only over the transports whose loss is detectable: **TCP** for
syslog, whose UDP loss is invisible, and UDP for NetFlow, whose sequence numbers show a loss. Two kinds
receive push entries today:

- **`firewall_log`**, whose polled `records` function delivers the same records the entry does. Such an
  entry must name the polled endpoint it `replaces` (`push_kind_without_replacement` otherwise), and the
  rest of this section applies to it.
- **`flow_record`**, which is **push-only**: its polled function, `capture`, returns the exporter's capture
  configuration, a different record (*A push-only kind: flow_record*, below). Its entry names no
  `replaces`, and one that did would replace nothing and is refused.

The load check tells the two apart by the contract and never by the plugin: an entry delivers the
kind's `<kind>_record` table, and when a polled function of the kind can return that table -- directly
or nested -- the entry needs a `replaces`.

**Nothing is counted twice.** The two paths' records cannot share an identity -- a pushed line carries
a header written for the push target, and its identity is `opnview`'s own, `push:` and a digest of the
pushed message -- so instead every instant belongs to one path: the provider's history is cut into
**source periods** that never overlap, and a record is stored only if its instant lies in a period
of the path that delivered it. A record of the other path is **superseded**: counted, not stored.

- **From polling to push.** When the entry becomes alive, polling goes on while the pushed records are
  held; a pass in that overlap leaves out the newest second its page returned. The boundary is the
  second before it, once the push path has been alive since before the boundary: polling closes its
  period there, the push period opens after it, the held records after the boundary are stored and
  the rest superseded, and polling stops -- the pass makes no call at all, and its availability says
  the push source answers.
- **From push to polling.** When the entry stops being alive, its period closes at the last second it
  delivered, and polling resumes at once; pulled records up to the boundary are superseded, and a gap
  polling then detects starts at the boundary, never over what the push path stored.

The periods, the mode and the superseded count are in the entry's catalogue entry. A built-in entry
declares `replaces` only when a parity test passes: the same recorded lines, pulled and pushed, give
records equal in every field of the contract but the identity.

### A push-only kind: `flow_record`

`flow_record` is the *Flow Record* of RFC 3954 and RFC 7011, as an exporter sends it; it is not a
`flow`, which is one filter-log record. A provider of it is a plugin with two halves (the contract is
under *Kinds*):

- **a push entry**, `protocol: netflow`, `kind: flow_record`, whose `fields` map each field of
  `flow_record_record` to the Information Elements that carry it. No script runs per record, nor per
  batch; the entry's counters show `script_calls` at none. The built-in `netflow` plugin's mapping reads
  `sourceIPv4Address` or `sourceIPv6Address` into `src_address`, `ingressInterface` into
  `ingress_interface`, `octetDeltaCount` into `octets`, and so on (`plugins/netflow/plugin.json`);
- **a polled function**, `capture`, which reads the exporter's **capture configuration** through the
  broker -- every interface it captures on, by its OPNsense identifier, whether ingress and egress
  are captured, whether each is actually exported (`exported`, with `not_exported_reason` when it is
  not), and the interfaces whose received packets its exporter also exports as the receiving interface
  (`parent_of`) -- and the active timeout. A read that fails answers its `error` with the `result`
  naming what the firewall answered: `opnview` records the configuration as unreadable, never as empty.

**What the core adds, which no provider fills.**

- **The identity**: the SHA-256 of the exporter, the observation domain, the SHA-256 of the datagram and
  the record's place in it. A datagram delivered twice stores its records once; two records identical
  in every field of one datagram are two rows. Two datagrams identical octet for octet are one delivery,
  the one case the identity cannot tell apart.
- **What the two indexes name**, at reception, with the index map in force -- `interfaces_info` read
  through the broker (survey, *Push sources*, S6), afresh after the exporter restarted: an interface,
  index 0 (`no_receiving_interface`), the loopback's index (`local_delivery`, S6b) or an index the map
  does not hold (`unmapped`). Nothing is re-mapped later. No Observation Point is assigned: the
  exporter sends none.
- **The join** of each record, from its own indexes, to the filter-log leg they name; its
  classification as a translated or a parent copy when it joins none; and each logged connection's
  **measured volume**, the `netflow` measure, each direction counted once, from the configuration's
  `exported` and `parent_of` (`docs/data-model.md`, *NetFlow volumes*). Records are never
  deduplicated by content.

**Adding a second exporter is adding its directory.** A plugin directory under `<data-dir>/plugins/`
with a manifest declaring a `flow_record` provider, an endpoint its `capture` reads, a push entry with
its own mapping, and its strings, is found at start; the core's join, coverage and states read the
contract's fields and nothing of any exporter, so nothing else changes. Only one `flow_record` provider
is active at a time: the records of one that is not are counted and not stored.

```json path=example_exporter/plugin.json
{
  "api_version": 2,
  "name": "example_exporter",
  "version": "1.0.0",
  "display_name": "Example exporter",
  "description": "An exporter of flow records with a capture-configuration read of its own.",
  "script": "example_exporter.js",
  "endpoints": [
    { "name": "export_state", "path": "/api/example/exporter/state", "method": "GET" }
  ],
  "push": [
    {
      "name": "records",
      "protocol": "netflow",
      "filter": { "require": ["octetDeltaCount", "flowStartMilliseconds", "flowEndMilliseconds", "collectionTimeMilliseconds"] },
      "fields": {
        "exporter_address": ["exporterIPv4Address", "exporterIPv6Address"],
        "observation_domain": "observationDomainId",
        "ingress_interface": "ingressInterface",
        "egress_interface": "egressInterface",
        "protocol": "protocolIdentifier",
        "src_address": ["sourceIPv4Address", "sourceIPv6Address"],
        "dst_address": ["destinationIPv4Address", "destinationIPv6Address"],
        "src_port": "sourceTransportPort",
        "dst_port": "destinationTransportPort",
        "tcp_flags": "tcpControlBits",
        "octets": "octetDeltaCount",
        "packets": "packetDeltaCount",
        "start_at": "flowStartMilliseconds",
        "end_at": "flowEndMilliseconds",
        "received_at": "collectionTimeMilliseconds"
      },
      "kind": "flow_record"
    }
  ],
  "providers": [ { "kind": "flow_record", "display_name": "Example exporter" } ]
}
```

```js path=example_exporter/example_exporter.js
function readState(host) {
  var result = { probe: "export_state", state: "unavailable", outcome: "request_not_made" };
  var response = host.call("export_state");
  if (response.error) {
    return { result: result, error: response.error };
  }
  if (!response.ok) {
    result.outcome = response.availability_outcome;
    return { result: result, failed: true };
  }
  var body = response.decodable ? response.value : null;
  if (body === null || typeof body !== "object" || !Array.isArray(body.points)) {
    result.outcome = "undecodable";
    return { result: result, failed: true };
  }
  result.state = "reachable";
  result.outcome = "healthy";
  result.separable = true;
  return { result: result, body: body };
}

exports.flow_record = {
  probe: function (host) {
    var read = readState(host);
    return read.error ? { result: read.result, error: read.error } : { result: read.result };
  },
  capture: function (host) {
    var read = readState(host);
    if (read.error || read.failed) {
      return { result: read.result, error: read.error || "the export state could not be read" };
    }
    return {
      result: read.result,
      capture: {
        enabled: read.body.running === true,
        interfaces: read.body.points.map(function (point) {
          return { identifier: point.name, ingress: point.receive === true, egress: point.send === true };
        })
      }
    };
  }
};
```

```json path=example_exporter/locale/en.json
{ "language": "en", "strings": { "provider.flow_record.example_exporter": "Example exporter" } }
```

```js path=example_exporter/tests/example_exporter.test.js
test("the capture configuration is read, and a record is mapped with no script", function (t) {
  t.answer("export_state", { json: { running: true, points: [{ name: "example_if_a", receive: true, send: true }] } });
  var read = t.call("flow_record", "capture");
  t.equal(read.error, null, "the read");
  t.equal(read.capture.interfaces.length, 1, "captured interfaces");
  t.deliverNetflow(["00090002000186a06ab85c800000000100000000000000580101001400080004000c0004000f0004000a0002000e000200020004000100040018000400170004001600040015000400070002000b0002000600010004000100050001001000040011000400090001000d000101010040c633640acb00711400000000000300010000000a00003778000000000000000000009c4000017318ad2101bb1b060000000000000000001818000000"]);
  var records = t.pushed("records");
  t.equal(records.length, 1, "records");
  t.equal(records[0].octets, 14200, "the octets");
  t.equal(records[0].ip_version, 4, "the version, read from the addresses when no element carries it");
  t.equal(t.pushCounters("records").script_calls, undefined, "script calls");
});
```

### The catalogue

The API catalogue (*The API catalogue*) carries a `push` part: each listener with its protocol,
transport, state, counts, the programs or templates it has seen and its drop counters; each entry with
its plugin, destination, columns, transports, listener state (`listening` or
`listener_not_configured`), liveness, stored rows and counters; and the accepted-sender set's size and
the refused senders. It never carries a message, a field value, a credential or the base URL.

### Testing a push entry

| Function | What it does |
|---|---|
| `t.deliverSyslog(lines, {transport, sender})` | Delivers syslog messages, one per line, through the real decoder and router; TCP by default, from the test world's firewall address. |
| `t.deliverNetflow(packets, {sender})` | Delivers NetFlow or IPFIX packets, each hexadecimal text. |
| `t.acceptSender(address)` | Adds an address to the accepted senders. |
| `t.pushed(entry)` | What the entry stored: rows (instants as epoch seconds, addresses as text), or records of its kind. |
| `t.pushCounters(entry)`, `t.listenerCounters(protocol, transport)` | The counters. |
| `t.fixture(file)` | The body of a recorded fixture of `testdata/`, such as a list of lines. |

A test never calls its parse function directly to prove ingestion: it delivers recorded messages.

### A syslog program into a table, with a regular expression

`example_logins` keeps every accepted SSH login a remote syslog target sends, with no script.

```json path=example_logins/plugin.json
{
  "api_version": 2,
  "name": "example_logins",
  "version": "1.0.0",
  "display_name": "Example logins",
  "description": "Keeps the SSH logins the firewall's syslog target sends.",
  "tables": [
    {
      "name": "logins",
      "columns": [
        { "name": "logged_at", "type": "instant" },
        { "name": "method", "type": "text" },
        { "name": "user", "type": "text" },
        { "name": "client", "type": "ip" }
      ],
      "retention": { "column": "logged_at" }
    }
  ],
  "kinds": [ { "kind": "example_logins", "push": true } ],
  "providers": [ { "kind": "example_logins", "display_name": "Example logins", "source": "push" } ],
  "push": [
    {
      "name": "logins",
      "protocol": "syslog",
      "filter": { "program": ["sshd"] },
      "pattern": "^Accepted (?P<method>\\S+) for (?P<user>\\S+) from (?P<client>\\S+)",
      "output_table": "logins"
    }
  ]
}
```

```json path=example_logins/locale/en.json
{ "language": "en", "strings": { "provider.example_logins.example_logins": "Example logins",
  "collection.kind.example_logins": "Logins", "plugin.example_logins.dataset.logins": "Logins" } }
```

```js path=example_logins/tests/example_logins.test.js
test("an accepted login is a row, and anything else of the program is counted", function (t) {
  t.deliverSyslog([
    "<38>1 2026-09-27T10:00:00+00:00 example-host sshd 4242 - - Accepted publickey for operator from 192.0.2.50 port 50022 ssh2",
    "<38>1 2026-09-27T10:00:01+00:00 example-host sshd 4242 - - Disconnected from user operator 192.0.2.50 port 50022"
  ]);
  var rows = t.pushed("logins");
  t.equal(rows.length, 1, "rows");
  t.equal(rows[0].user, "operator", "the user");
  t.equal(rows[0].client, "192.0.2.50", "the client");
  t.equal(rows[0].logged_at, 1790503200, "the header's instant");
  t.equal(t.pushCounters("logins").pattern_unmatched, 1, "lines the pattern does not match");
});
```

### A parse function

`example_pairs` reads messages of `key=value` pairs, which no regular expression of fixed groups
expresses.

```json path=example_pairs/plugin.json
{
  "api_version": 2,
  "name": "example_pairs",
  "version": "1.0.0",
  "display_name": "Example pairs",
  "description": "Keeps the key=value messages of an example program.",
  "script": "example_pairs.js",
  "tables": [
    {
      "name": "events",
      "columns": [
        { "name": "logged_at", "type": "instant" },
        { "name": "event", "type": "text" },
        { "name": "bytes", "type": "integer", "nullable": true, "role": "measure", "unit": "byte" }
      ],
      "retention": { "column": "logged_at" }
    }
  ],
  "kinds": [ { "kind": "example_pairs", "push": true } ],
  "providers": [ { "kind": "example_pairs", "display_name": "Example pairs", "source": "push" } ],
  "push": [
    {
      "name": "events",
      "protocol": "syslog",
      "filter": { "program": ["example-daemon"] },
      "parse": "events",
      "output_table": "events"
    }
  ]
}
```

```js path=example_pairs/example_pairs.js
exports.push = {
  events: function (messages) {
    return messages.map(function (message) {
      var pairs = {};
      String(message.message).split(" ").forEach(function (part) {
        var equals = part.indexOf("=");
        if (equals > 0) {
          pairs[part.slice(0, equals)] = part.slice(equals + 1);
        }
      });
      if (!pairs.event) {
        return null;
      }
      return { logged_at: message.timestamp, event: pairs.event, bytes: pairs.bytes === undefined ? null : Number(pairs.bytes) };
    });
  }
};
```

```json path=example_pairs/locale/en.json
{ "language": "en", "strings": { "provider.example_pairs.example_pairs": "Example pairs",
  "collection.kind.example_pairs": "Pairs", "plugin.example_pairs.dataset.events": "Events" } }
```

```js path=example_pairs/tests/example_pairs.test.js
test("each message with an event is a row, the others are skipped", function (t) {
  t.deliverSyslog([
    "<14>1 2026-09-27T10:00:00+00:00 example-host example-daemon - - - event=sync bytes=4096",
    "<14>1 2026-09-27T10:00:01+00:00 example-host example-daemon - - - nothing to see"
  ]);
  var rows = t.pushed("events");
  t.equal(rows.length, 1, "rows");
  t.equal(rows[0].event, "sync", "the event");
  t.equal(rows[0].bytes, 4096, "the bytes");
  t.equal(t.pushCounters("events").skipped, 1, "skipped messages");
  t.equal(t.pushCounters("events").script_calls, 1, "one call for the batch");
});
```

### A NetFlow subscriber

`example_flows` keeps the volume of every IPv4 and IPv6 flow record an exporter sends. A packet can be
exported more than once -- by the node of the interface it arrived on, in its translated form by the
node of the interface it left by, again by its VLAN's parent's node (survey, *Push sources*, S5 and
S11) -- so **a sum of this table counts such packets once per copy**; the core's `flow_record` names
each copy, and a plugin's own table does not. Traffic inside one interface's network never
reaches the exporter at all.

```json path=example_flows/plugin.json
{
  "api_version": 2,
  "name": "example_flows",
  "version": "1.0.0",
  "display_name": "Example flows",
  "description": "Keeps the flow records a NetFlow exporter sends.",
  "tables": [
    {
      "name": "records",
      "columns": [
        { "name": "started_at", "type": "instant" },
        { "name": "source", "type": "ip" },
        { "name": "destination", "type": "ip" },
        { "name": "protocol", "type": "integer", "role": "dimension" },
        { "name": "input_index", "type": "integer", "role": "dimension" },
        { "name": "octets", "type": "integer", "role": "measure", "unit": "byte" }
      ],
      "retention": { "column": "started_at" }
    }
  ],
  "kinds": [ { "kind": "example_flows", "push": true } ],
  "providers": [ { "kind": "example_flows", "display_name": "Example flows", "source": "push" } ],
  "push": [
    {
      "name": "records",
      "protocol": "netflow",
      "filter": { "require": ["octetDeltaCount", "flowStartMilliseconds", "ingressInterface"] },
      "fields": {
        "started_at": "flowStartMilliseconds",
        "source": ["sourceIPv4Address", "sourceIPv6Address"],
        "destination": ["destinationIPv4Address", "destinationIPv6Address"],
        "protocol": "protocolIdentifier",
        "input_index": "ingressInterface",
        "octets": "octetDeltaCount"
      },
      "output_table": "records"
    }
  ]
}
```

```json path=example_flows/locale/en.json
{ "language": "en", "strings": { "provider.example_flows.example_flows": "Example flows",
  "collection.kind.example_flows": "Flow records", "plugin.example_flows.dataset.records": "Flow records, counted once per interface" } }
```

```js path=example_flows/tests/example_flows.test.js
test("a v9 template and its record become one row", function (t) {
  t.deliverNetflow(["00090002000186a06ab85c800000000100000000000000580101001400080004000c0004000f0004000a0002000e000200020004000100040018000400170004001600040015000400070002000b0002000600010004000100050001001000040011000400090001000d000101010040c633640acb00711400000000000300010000000a00003778000000000000000000009c4000017318ad2101bb1b060000000000000000001818000000"]);
  var rows = t.pushed("records");
  t.equal(rows.length, 1, "rows");
  t.equal(rows[0].source, "198.51.100.10", "the source");
  t.equal(rows[0].octets, 14200, "the octets");
  t.equal(rows[0].input_index, 3, "the input index");
  t.equal(rows[0].started_at, 1790467140, "uptime 40 s, exported at uptime 100 s");
});
```

## Worked examples

Each example below is complete: its files, copied as they are into a directory, load and pass their
own tests. A test of the repository does exactly that.

### A source plugin of a core kind, with no script

`example_leases` reads the lease table of a DHCP server that answers rows, declared without a line
of code.

```json path=example_leases/plugin.json
{
  "api_version": 2,
  "name": "example_leases",
  "version": "1.0.0",
  "display_name": "Example leases",
  "description": "Reads the lease table of an example DHCP server.",
  "endpoints": [
    { "name": "status", "path": "/api/exampledhcp/service/status", "method": "GET" },
    { "name": "leases", "path": "/api/exampledhcp/leases/search", "method": "POST", "body": "form" }
  ],
  "providers": [
    {
      "kind": "dhcp_lease",
      "display_name": "Example leases",
      "read_strategy": "full_reread",
      "probe": { "endpoint": "status", "check": "service_running" },
      "read": {
        "endpoint": "leases",
        "paginate": true,
        "fields": {
          "address": "address",
          "mac": { "from": "hwaddr", "as": "mac" },
          "hostname": "hostname",
          "lease_state": { "value": "active" },
          "expires_at": { "from": "expire", "as": "epoch" }
        }
      }
    }
  ]
}
```

```json path=example_leases/locale/en.json
{ "language": "en", "strings": { "provider.dhcp_lease.example_leases": "Example leases" } }
```

```js path=example_leases/tests/example_leases.test.js
test("a running server is probed reachable", function (t) {
  t.answer("status", { json: { status: "running" } });
  var probe = t.call("dhcp_lease", "probe");
  t.equal(probe.result.state, "reachable", "the state");
});

test("each row is a lease", function (t) {
  t.answer("leases", { json: { total: 1, rowCount: 1, current: 1, rows: [
    { address: "192.0.2.10", hwaddr: "AA:BB:CC:00:11:22", hostname: "example-host", expire: 1790000000 }
  ] } });
  var read = t.call("dhcp_lease", "leases", 50);
  t.equal(read.leases.length, 1, "the leases read");
  t.equal(read.leases[0].mac, "aa:bb:cc:00:11:22", "the hardware address, lower-cased");
  t.equal(read.leases[0].expires_at, 1790000000, "the expiry");
  t.equal(read.leases[0].lease_state, "active", "the state");
});
```

### A service plugin downloading a file

`example_domains` gives site names their registrable domain from a list of suffixes it downloads,
one per line, keeping the copy in its data directory and asking for it again only when it changed.

```json path=example_domains/plugin.json
{
  "api_version": 2,
  "name": "example_domains",
  "version": "1.0.0",
  "display_name": "Example suffixes",
  "description": "Groups site names by a list of suffixes downloaded from lists.example.org.",
  "script": "example_domains.js",
  "destinations": ["https://lists.example.org"],
  "providers": [
    { "kind": "registrable_domain", "display_name": "Example suffixes", "read_strategy": "full_reread" }
  ]
}
```

```js path=example_domains/example_domains.js
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
```

```js path=example_domains/tests/example_domains.test.js
var listURL = "https://lists.example.org/suffixes.txt";

test("a downloaded list groups names", function (t) {
  t.web(listURL, { body: "# example suffixes\nexample\nco.example\n", headers: { ETag: "\"v1\"" } });
  var refreshed = t.call("registrable_domain", "refresh");
  t.equal(refreshed.ready, true, "the list is held");
  t.equal(refreshed.rules, 2, "the suffixes");
  var grouped = t.call("registrable_domain", "group", ["www.shop.co.example", "co.example", "api.example"]);
  t.equal(grouped.groups, [
    { name: "www.shop.co.example", registrable_domain: "shop.co.example" },
    { name: "co.example" },
    { name: "api.example", registrable_domain: "api.example" }
  ], "the grouping");
});

test("an unchanged list is not downloaded again", function (t) {
  t.web(listURL, { body: "example\n", headers: { ETag: "\"v1\"" } });
  t.call("registrable_domain", "refresh");
  var again = t.call("registrable_domain", "refresh");
  t.equal(again.result.probe, "list_not_modified", "the second refresh");
  t.equal(t.webRequests().length, 2, "two requests, the second conditional");
});

test("a download with no suffix keeps nothing", function (t) {
  t.web(listURL, { body: "# nothing here\n" });
  var refreshed = t.call("registrable_domain", "refresh");
  t.equal(refreshed.ready, false, "nothing is held");
  t.equal(refreshed.result.outcome, "download_malformed", "the outcome");
});
```

### A widget computing what a query cannot

`example_latency` keeps the round-trip times a prober measured in a table of its own, and carries a
widget ranking each target by a percentile of them -- a figure no single `SELECT` returns per member,
so its script computes it. In practice the plugin's own provider writes the table (*Tables*); its
tests arrange the rows themselves. Its golden file is the whole answer, written once with
`OPNVIEW_WRITE_GOLDEN=1` and read before it was kept.

```json path=example_latency/plugin.json
{
  "api_version": 2,
  "name": "example_latency",
  "version": "1.0.0",
  "display_name": "Example latency",
  "description": "Ranks the targets a prober measures by a percentile of their round-trip time.",
  "tables": [
    {
      "name": "probe",
      "nature": "sample",
      "columns": [
        { "name": "measured_at", "type": "integer", "role": "time" },
        { "name": "target", "type": "text" },
        { "name": "latency_ms", "type": "real", "role": "measure", "unit": "millisecond" }
      ],
      "retention": { "column": "measured_at" }
    }
  ],
  "widgets": ["example_latency_percentile"]
}
```

```json path=example_latency/widgets/example_latency_percentile/manifest.json
{
  "manifest_version": 1,
  "type": "example_latency_percentile",
  "shape": "ranked_bars",
  "windowed": true,
  "parameters": [
    { "key": "period", "kind": "enum", "values": ["1h", "24h"], "default": "24h" },
    { "key": "percentile", "kind": "integer", "min": 1, "max": 99, "default": 50 },
    { "key": "limit", "kind": "integer", "min": 1, "default": 10 },
    { "key": "compact", "kind": "boolean", "default": false, "class": "rendering" }
  ],
  "queries": ["latency.statements.sql"],
  "script": "latency.js"
}
```

```sql path=example_latency/widgets/example_latency_percentile/latency.statements.sql
-- Every sample of the window, each target's in ascending order.
-- statement: widget_example_latency_samples
SELECT target, latency_ms
FROM plugin_example_latency.probe
WHERE measured_at >= :from AND measured_at < :to
ORDER BY target, latency_ms;
```

```js path=example_latency/widgets/example_latency_percentile/latency.js
// Each target's latency at a percentile, by nearest rank: the smallest sample at or above that
// share of the target's samples. A percentile is no aggregate a declarative query returns per
// member in one pass, so the script computes it.
exports.widget = {
  serve: function (host) {
    var window = host.request.window;
    var rank = host.params.integer("percentile");
    var limit = host.params.integer("limit");
    var rows = host.query.rows("widget_example_latency_samples", { from: window.from, to: window.to });
    var samples = {};
    rows.forEach(function (row) {
      (samples[row.target] = samples[row.target] || []).push(row.latency_ms);
    });
    var bars = Object.keys(samples).map(function (target) {
      var sorted = samples[target];
      var index = Math.max(0, Math.ceil(rank / 100 * sorted.length) - 1);
      return { target: target, samples: sorted.length, value: sorted[index] };
    });
    // Slowest first; equal values in the byte order of their names, as the core ranks a tie.
    bars.sort(function (a, b) { return b.value - a.value || host.figures.compare(a.target, b.target); });
    var shown = bars.slice(0, limit);
    var dropped = bars.slice(limit).reduce(function (sum, bar) { return sum + bar.samples; }, 0);
    host.envelope.cover(rows.length > 0 ? window : null);
    return {
      data: {
        percentile: rank,
        bars: shown,
        remainder: { members: bars.length - shown.length, samples: dropped }
      },
      has_rows: bars.length > 0
    };
  }
};
```

```js path=example_latency/tests/example_latency.test.js
// The instant the tests answer at, so every window and instant in an answer is the same each run.
var AT = 1790000400;

function sample(t, secondsAgo, target, latency) {
  t.sql("INSERT INTO plugin_example_latency.probe (measured_at, target, latency_ms) " +
    "VALUES (:at, :target, :latency)", { at: AT - secondsAgo, target: target, latency: latency });
}

test("each target is ranked by its 75th percentile, slowest first", function (t) {
  t.clock(AT);
  [12, 30, 18, 50].forEach(function (latency, index) { sample(t, 600 + index, "alpha", latency); });
  sample(t, 700, "beta", 75);
  sample(t, 90000, "gamma", 999);
  var answer = t.widget("example_latency_percentile", { percentile: 75, limit: 1 });
  t.equal(answer.status, 200, "the status");
  t.equal(answer.body.data.bars, [{ target: "beta", samples: 1, value: 75 }], "the slowest target");
  t.equal(answer.body.data.remainder, { members: 1, samples: 4 }, "what the limit dropped");
  t.golden("percentile_75.json", answer.raw);
});

test("a value out of range takes its default, and an unknown key is reported, never applied", function (t) {
  t.clock(AT);
  var answer = t.widget("example_latency_percentile", { percentile: 101, colour: "red" });
  t.equal(answer.body.parameters.percentile, 50, "the default");
  t.equal(answer.body.invalid_parameters[0].path, "percentile", "the invalid value");
  t.equal(answer.body.unknown_parameters, { colour: "red" }, "the unknown key");
  t.equal(answer.condition, "reachable_no_rows", "nothing was measured");
  t.equal(answer.body.window.coverage, "not_covered", "the window holds nothing");
});
```

```json path=example_latency/tests/percentile_75.json
{
  "type": "example_latency_percentile",
  "parameters": {
    "compact": false,
    "limit": 1,
    "percentile": 75,
    "period": "24h"
  },
  "unknown_parameters": {},
  "invalid_parameters": [],
  "missing_parameters": [],
  "unresolved_references": [],
  "window": {
    "requested": {
      "from": 1789914000,
      "to": 1790000400
    },
    "covered": {
      "from": 1789914000,
      "to": 1790000400
    },
    "coverage": "covered",
    "flow_horizon_at": 1782224400
  },
  "gaps": [],
  "availability": [],
  "condition": "reachable",
  "freshness": [],
  "aggregate_mode": "full",
  "withheld": [],
  "states": [],
  "data": {
    "percentile": 75,
    "bars": [
      {
        "target": "beta",
        "samples": 1,
        "value": 75
      }
    ],
    "remainder": {
      "members": 1,
      "samples": 4
    }
  }
}
```

## The interpreter and what it brings

The interpreter is goja (`github.com/dop251/goja`, MIT), a JavaScript engine written in Go, linked
into the binary. Its dependencies, every one linked into the binary with it:

| Module | Licence | Why |
|---|---|---|
| `github.com/dop251/goja` | MIT | The interpreter. |
| `github.com/dlclark/regexp2/v2` | MIT | JavaScript's regular expressions. |
| `github.com/go-sourcemap/sourcemap` | BSD-2-Clause | Source maps in error positions. |
| `github.com/google/pprof` | Apache-2.0 | The interpreter's profiler support. |
| `golang.org/x/text` | BSD-3-Clause | Case mapping and collation. |

`goja_nodejs`, `Masterminds/semver` and `goccy/go-yaml` appear in goja's own module graph for its
tests only and are not linked.
