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

A built-in plugin is `plugins/<name>/` in the repository, embedded in the binary; yours is
`<data-dir>/plugins/<name>/`. **Both are discovered at every start**: drop the directory in and restart.
**A plugin that cannot be loaded is refused, logged with a closed reason, and skipped**; the others load,
and the plugin listing (`GET /api/plugins`, `docs/widget-api.md`) reports every plugin with its reason.

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
| `sample_metrics` | no | Which subject kind and which measures of a kind's readings serve a health metric of the catalogue (`cpu`, `memory`...): `{name, provider_kind, subject_kind, measures}`, each a snake_case word, one measure at least and none twice, `provider_kind` one the plugin provides, and a metric declared by one plugin only, else `manifest_invalid`. `host.samples.metrics` answers them; `plugins/insight/plugin.json` declares the built-in ones. |

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
measurement interval throughout, whether anyone looks or not. The `insight` plugin's manifest declares
one (`plugins/insight/plugin.json`, its provider's `live` and the setting `live_cadence_seconds`).

| Key | What it is |
|---|---|
| `function` | The function the script exports under the kind's name, called as `sample` is -- `(host, discovery, now)` -- and answering a `sample_answer`. |
| `cadence_setting` | A declared `integer` setting with a `default` and a `min` of at least 1: the live cadence, in seconds. The cadence in force is never below that `min`, nor below the `floor_seconds` of the endpoints the pass reads. |
| `endpoints` | The only endpoints the live pass may read, by their names; a stream is refused. |

The live pass's requests carry the live cadence as their max age, so the broker coalesces every reader
of one datum into one call. **Stamp each reading with the answer's `fetched_at`**, not with `now`: when
the baseline pass falls due during live mode the broker serves it the live pass's answer from the
mirror, and the same capture stamped with the same instant is promoted to a baseline sample rather than
stored twice. A live pass declared wrongly is refused (`live_pass_invalid`). In a plugin's own tests,
`t.call(kind, function, ...)` calls it as the service does: under its default cadence, reading only its
endpoints.

### Notifications

The core publishes a notification when a pass commits, on a closed list of PostgreSQL channels:

| Channel | Published when | Payload |
|---|---|---|
| `opnview_collection_pass` | A collection pass of a core kind ended without an error, once per provider it read. | `{"kind": ..., "provider_key": ...}` |
| `opnview_plugin_pass` | A pass of a plugin's own kind stored its rows and state; published inside that transaction, so a pass that rolls back publishes nothing. | `{"plugin": ..., "provider": ...}` |
| `opnview_aggregate_refresh` | An aggregate refresh rewrote slots. It follows every derivation, and so every filter-log pass and every pushed batch, because the current slot of each period is rewritten every time. | `{"periods": [...], "ranges": [{"period": ..., "from": ..., "to": ...}], "families": [...]}`, each range spanning the slots of one period rewritten |
| `opnview_live_sample` | A live pass of a `measurement_sample` provider stored fine samples (`docs/widget-api.md`, *Live figures*). | `{"kind": "measurement_sample", "provider_key": ..., "sampled_at": ...}` |

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
"tables": [{"name": "readings", "nature": "sample",
  "columns": [{"name": "sampled_at", "type": "integer", "role": "time"}, {"name": "meter", "type": "text"},
              {"name": "value", "type": "real", "role": "measure", "unit": "watt"}],
  "primary_key": ["meter", "sampled_at"], "retention": {"column": "sampled_at", "seconds": 2592000}}]
```

| Key | What it is |
|---|---|
| `name` | The table's name: a lower-case snake_case word. |
| `columns` | Each `name`, `type` (a word of the type vocabulary: `integer`, `real`, `text`, `boolean`, `ip`, `mac` or `instant`; see *Push sources*, *The type vocabulary*), and optionally `nullable`, `carries_domain` (the column holds a domain name, withheld under `aggregate_mode` = `no_domains`), `role` (`time`, `dimension` or `measure`; a dimension by default, time for the retention column), `unit` (a measure's lower-case word, such as `byte`) and `aggregations` (what the data catalogue admits on it; the role's defaults otherwise). |
| `primary_key`, `indexes` | The keys. A row written again with the same primary key replaces the first. |
| `retention` | The column holding each row's instant -- an `instant`, or an `integer` of UTC epoch seconds -- and how long a row is kept after it; `seconds` 0 follows the installation's retention; `setting` names one of the plugin's integer settings which, when the operator set it above zero, is how long a row is kept instead. A push output table's is an `instant`. |
| `nature` | `event` or `sample`: what a bucket of the data catalogue with no row means -- a true zero, or a gap. `sample` by default, which never draws a zero it did not read. |
| `counters` | `true` makes it a table of counters, which only a derivation writes (*Derivations*): it has a primary key, every other column is a NOT NULL number, and a row written with a key already held adds its figures to the held row's, in one batched write, instead of replacing it; rows of one key written together are added first, and a row whose figures all come back to zero is removed. A counter outlives the purge of whatever it counts. |
| `identity` (a column) | `true` on a NOT NULL integer column: the database numbers each row, and no row written carries it (ADR 0019). |
| `generated` (a column) | An SQL expression over the row's other columns the column is computed from and stored; no row written carries it. Never the time column nor part of the primary key. |
| `references` (a column) | A foreign key: `table(column)` of the plugin's own, or `plugin.table(column)` of a plugin the manifest `requires`, which loads first. A table with a time column may reference, and is never referenced. |
| `checks` | The table's CHECK constraints, each `{"name", "expression"}`, named `<table>_<name>` in the database. |
| `where` (an index) | The predicate of a partial index. |
| `nulls_not_distinct` (an index) | `true` on a unique index: two NULLs count as equal, so a key with a nullable column is one holding per value, NULL included. |
| `default` (a column) | The SQL literal a row written without the column takes: `"false"`, `"0"`, `"'none'"`. |
| `writable_by` | The other plugins that may write the table (ADR 0019), each `{"plugin", "columns"}`: with `columns`, UPDATE of those columns alone; without, INSERT, UPDATE and DELETE of the whole table. It is the owner's declaration, turned into PostgreSQL's own grants on the other plugin's role at every start, and what the owner no longer grants is revoked. The other plugin `requires` the owner and names the table by its qualified name; its statements are planned under its role when it loads, so one that writes a column it was not granted refuses it by name (`statement_refused`). |

An expression a declaration carries -- a check, a generated column, a partial index's predicate --
is one expression over the row: no statement terminator, no parameter, no `SELECT` and no word a
statement that writes uses; PostgreSQL then refuses what does not parse, and the plugin with it
(`table_invalid`).

A table with a retention column is a TimescaleDB hypertable chunked by day on it, purged by whole
chunks and then to the exact second. A hypertable cannot hold a unique key that leaves out its time
column, so a primary key or a unique index without it is refused (`unique_key_without_time`).

**A declaration that changed is a rebuild, never a refusal.** The old table moves, with its rows,
into the schema `opnview_aside` under a name beginning `aside_`; the new one starts empty; the
plugin's durable state is reset; the listing reports the move. A set-aside table is never read by a
widget or the data catalogue, and is purged by its retention and dropped once empty. A plugin
removed from the data directory keeps its schema and rows, and the plugin listing names them.

**Every table is a dataset of the data catalogue** (ADR 0011), named
`<plugin>.<table>`, labelled by the plugin's own string `plugin.<plugin>.dataset.<table>`, and dated
by the plugin's provider of its own kind. A chart can then read it -- a `custom_chart` series whose
`source` is its name -- with no file of `opnview` changed. A table the catalogue could not serve --
a measure of type text, a unit on a dimension, an aggregation its role cannot admit -- is refused
(`table_invalid`).

### Statements

A plugin that writes in SQL -- a classification, a rollup, a join it would rather express as one
statement than as rows a script returns -- lists statement files in its manifest:

```json
"statements": ["totals.statements.sql"]
```

A file holds named statements, each under a marker line `-- statement: <name>`, with `:name`
parameters, exactly as a widget's statements (*Widgets*, *Statements*). A statement name is a
snake_case word, once across the plugin's files. A derivation runs them (*Derivations*).

**A statement writes in the plugin's own schema and nowhere else, and that is PostgreSQL's decision,
not a parser's.** Every plugin has a role, `opnview_plugin_<name>`, that cannot log in, reads what a
widget query reads -- the core schema, every plugin's schema, never a credential table -- and holds
INSERT, UPDATE and DELETE on `plugin_<name>` alone. A derivation's statements run as that role, inside
the derivation's one transaction. When the plugin loads, every statement is planned under the role
with its parameters bound to NULL, so one that would write elsewhere, read a credential table, or not
plan at all refuses the plugin, naming the statement (`statement_refused`). Where a parameter stands
in a place PostgreSQL cannot type, cast it, as in a widget's statement.

### Relations

A plugin publishes views and functions in its own schema by listing relation files:

```json
"relations": ["totals.relations.sql"]
```

A relation file holds `CREATE OR REPLACE VIEW` and `CREATE OR REPLACE FUNCTION` statements and nothing
else; every object is schema-qualified in the plugin's own schema (`plugin_example.meter_totals`). A
function is `STABLE` or `IMMUTABLE`; a `VOLATILE` one, or one with `SECURITY DEFINER`, refuses the
file (`relation_refused`). The files are applied at every start, in name order, as the plugin's role,
once the plugin's tables are ensured; `CREATE OR REPLACE` makes an unchanged file a no-op. The read
role is granted what was created, so any plugin's SQL, any widget's statement and the data catalogue
read a view by its qualified name. A view the plugin's role could not read -- one that names a
credential table -- cannot be created.

A relation another plugin reads is a contract of the plugin publishing it: renaming one of its columns
is a breaking change, said in the plugin's changelog. opnview versions nothing beyond that.

### Requires

A plugin whose SQL reads another plugin's schema says so:

```json
"requires": ["example_derived_sql"]
```

Required plugins load first, built-ins then the data directory's, each in name order among those that
are ready. A required plugin that is absent, refused, or requiring this one back refuses this one
(`requires_missing`), and nothing else of the installation is affected. A plugin's tests load the
plugins it requires before it: each from the sibling directory of that name, or from the built-ins.

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
| `collect(host, state)` | `{result, rows, state, error, unsupported}`: `rows` an object from each table the plugin declares to the rows to store, each row checked against its columns; `state` the provider's **durable state**, an object of strings handed to the next pass's `collect` as its `state`. The rows and the state are stored in one transaction. A provider whose push entries carry a `sequence_bound` returns `bounds`, `{<value>: {counters, changed}}`: how many sequence counters the sender now runs by what the pass read, and whether its configuration changed (*Push sources*, *Liveness and gaps*). |

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
broker. A script never needs to know this, but four things follow from it.

- **An answer may come from the mirror.** The broker keeps, in the `opnsense` schema, the fields of
  each answer some reader read, and serves an answer from there while it is younger than the caller's
  **max age**: `max_age_seconds` on the call, else the endpoint's, else the cadence of the pass making
  the call (`served_from`, `fetched_at`, `age_seconds` say so). Two plugins asking at once cause one
  call. A request asking for fresher than the endpoint's **freshness floor** -- its `floor_seconds`, or
  the median time the firewall takes to answer -- is served at the floor.
- **The answer's `value` is an observed object**, read as `JSON.parse(body)` would be: the mirror
  stores the fields read, and a field it does not hold makes the call run again from the firewall,
  transparently. Reading `body` or `json()` stores the whole answer.
- **A record stream is read after a cursor.** For log lines or lookups, `after` returns only the rows
  past the script's cursor, with `gaps` the firewall had already dropped, each `{from, to, reason}`: a
  gap is never zero rows.
- **A stream is shared** by every subscriber with the same parameters, recorded while a loaded plugin
  reads it, reconnected with back-off, its silence a gap. A stream declared with `replaces` suspends
  the polling of the endpoint it replaces while it runs (`served_from: "stream"`).

### Corrections

The broker infers, from each answer, its **nature** (`snapshot`, such as leases, or `record_stream`,
read on a cursor), its **row set**, each row's **key**, a stream's **cursor**, each field's **type**,
which fields carry domain names, which objects are keyed by values, and each request parameter's
**role**. Where it infers wrongly, the manifest corrects it:

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
learned of every endpoint it called -- parameters and roles, each datum's nature, key, cursor and
schema versions by OPNsense version, readers and the fields each read, costs, freshness floor,
streams, and endpoints no survey section cites (`uncited`) -- and never a field value, a credential,
the firewall's address or an authorisation header.

### Helpers

The global `opnview` carries the readers the built-in plugins share with the core, so a plugin
reads a firewall field exactly as the core would: `opnview.decode.string(row, key)`, `int`,
`float`, `epoch`, `port`, `flag`, `nested`, `nestedString`, `textField`, `rows(body)`,
`lower(text)`; `opnview.sha256Hex(text)`, `opnview.base64(text)`. What a backend's fields mean -- a
lease state, a resolver's action, an Insight subject -- is its plugin's to decode, in its own script
(`plugins/kea`, `plugins/unbound`, `plugins/insight`); the core keeps only what every record of a kind
needs, such as normalising a contract record's MAC. Two standards: `opnview.ip(text)`, an IP address read as a value --
`{address, version, key, loopback}`, the address in its canonical text (an IPv4-mapped one as the IPv4 address it
maps), a `key` whose text order is the addresses' order -- or `null`; and `opnview.protocolNumber(name)`, an IANA
protocol keyword as its number, case aside, or `null`.

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
| `leg` | Every logged connection leg -- each filter-log record, the second leg of a paired connection included -- by `flow_id`, with its `pair_outcome` and `paired_flow_id`, pf's `direction`, its `device` and `interface`, its `action`, the tuple pf logged and its two addresses in their canonical text (`canonical_src_address`, `canonical_dst_address`, which a key read by `device` and the two serves from an index), whether either end is *This Firewall*, and when the core stored it (`ingested_at`). Timed by `observed_at`. |
| `interface` | The firewall's interfaces as discovery holds them: `identifier`, `device`, `description`, `link_type`, `link_kind`, `vlan_tag`, `is_upstream`. |
| `this_firewall_address` | This firewall's own addresses as discovery holds them, each with its `interface`, `first_seen_at`, `last_seen_at` and `is_current` (held at `t` when `first_seen_at - margin <= t` and `is_current = 1` or `t <= last_seen_at + margin`, the margin the discovery interval). |
| `provider_value` | The values the active providers export (*Values another plugin reads*). |
| `read_through` | How far each provider's source has been read: every record before `read_through` is stored, or in a gap the provider recorded. The filter log's is what a derivation joining another source to the legs waits for. |
| `collection_gap` | The intervals each provider (`kind`, `provider_key`) recorded as not covered, both ends included, with the `reason`. |
| `retention_purge` | How far back the retention purge removed the legs: `purged_before`, no row before the first purge. |

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
| `after_passes` | Collection passes that run it: a core kind or a kind one of the plugin's providers implements, or such a kind and one of its providers (`kind/provider`). |
| `after_push` | The plugin's own push entries whose stored batches run it. |
| `cadence_seconds` | Runs it on a cadence too; 0 runs it on its other triggers alone. At least one trigger is named. |
| `datasets` | The datasets it reads, each also under the manifest's `datasets` (*Datasets a plugin reads*). |
| `after_derivations` | Derivations of required plugins that run it, each `<plugin>/<derivation>`, after each successful run of theirs (*Requires*). |
| `statements` | Makes the derivation **SQL-only**: the plugin's statements it runs, in this order, each handed `:since` and `:now`, inside its one transaction, with no script function at all (*Statements*). Exclusive with `function`. |
| `outputs` | What it may write: tables of the plugin's own, each with a primary key (counter tables among them, *Tables*), measures the plugin declares (*Measures*), and `statements` a script run may return to run. |

A script derivation may return, beside its rows, `statements: [{name, parameters}]`: each one the
declaration's `outputs.statements` names, run in that order, as the plugin's role, before the rows are
written, in the same transaction. A SQL-only derivation needs no script: `docs/examples/example_derived_sql`
totals readings per meter with one `INSERT ... ON CONFLICT` and no line of JavaScript.

```js
exports.derivations = {
  summarise: function (host, run) {
    // run: {derivation, now, since, changes: [{trigger, at, kind, provider, entry, rows, from, to, reason, scope}]}
    return {
      rows: { summary: [/* rows */], tallies: [/* counters to add */] },
      measures: { example_rate: [{ connection: 1, connection_start_at: 0, direction: "forward",
        state: "measured", bytes: 0, packets: 0, records: 0, markers: [], due_at: null }] },
      intervals: { example_rate: [{ starts_at: 0, ends_at: null, state: "covered" }] }
    };
  }
};
```

**A run is handed what changed, never "everything".** `changes` are the passes (`kind`, `provider`) -- of a
core kind, or of a kind of the plugin's own -- the stored batches (`entry`, `rows`, the reception span
`from`..`to`), the conditions the decoder detected on a stream one of its entries is fed by, which no row says
(`entry`, `reason` -- such as `netflow_exporter_restarted` -- and `scope`, the stream), and, with nothing else
noted, the cadence tick, since its last successful run (`since`, null before the first); `now` is the host's clock. They are
handed in an order of what they are, never of when they arrived, so the same changes make the same run.
**Runs of one derivation never overlap**: each has one loop, and changes arriving together make one run.

**A run is one transaction.** What it returns is checked against its declaration -- a table or measure it
did not declare, a column or field that is not one, a state or marker its measure does not declare, refuse
the whole run -- and written together, or not at all. A row replaces the held row of its key only when a
value differs, a measure row only when a figure, its state or its markers differ, and a measure's
`intervals` (its window states for the plugin's provider of the measure's kind) are rewritten only when
they differ: **a run over the same inputs writes nothing.** A counter adds, which is why a run is handed
only new changes. A measure row's `connection` is the logged connection's first leg (*Measures*);
`{connection, direction, remove: true}` removes the held row of a connection the measure no longer gives a
figure; the aggregate slots of every connection whose row changed are refreshed after the run.

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

`host.fetch({url, method, headers, file, max_bytes, conditional, check})` sends a `GET` (the default)
or a `HEAD`, its `headers` to the host asked and never to one a redirect leads to. A GET streams the
body to `file` in the plugin's data directory, under `max_bytes`; `conditional: true` sends the copy
held's validators, and a 304 keeps it; the body replaces the copy held only once complete and
`check(temporary)` answers `{ok: true}`, so a failed download leaves the previous copy in use. It answers `{outcome, status_code, detail, last_modified, etag}`, the outcome one of `fetched`,
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
`opnview plugin test <plugin directory>` prints one line per test and exits non-zero when one failed.
It touches no data directory and contacts nothing; the repository runs the built-ins' tests the same
way. A push entry is tested by delivering recorded messages (*Push sources*, *The catalogue and the tests*).

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
| `push_provider_missing` | A push entry writes a table of the plugin's own, and the plugin declares not exactly one provider with `source` push -- or, with none, not exactly one provider of a kind of its own declared `push`. |
| `push_column_unfilled` | A NOT NULL column of a push entry's table, or a required field of the kind record its field mapping fills, is filled by nothing. |
| `push_parse_missing` | A push entry names a parse function the script does not export under `push`. |
| `dataset_not_declared` | A script reads, through `host.datasets`, a dataset the manifest does not name under `datasets`. Refused at load when the script names it by a literal; a name computed at run time is refused at the call, a `TypeError` carrying the code. |
| `derivation_invalid` | A derivation names a function the script does not export, no trigger, a pass of a kind neither core nor its own, a push entry the plugin lacks, a dataset its manifest does not declare, an output that is not one of its tables (with a primary key) or measures, a name twice; or the plugin declares derivations and no provider. |
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

An endpoint renamed in 26.7, whose hardware address is renamed and whose rows are wrapped in a
`data` object there, is one endpoint with a variant; the script reads `hwaddr` and `rows` on every
version, and its tests answer one recorded fixture per route (`t.firewallVersion`) and assert the
same records from each. `internal/plugin/harness/versions_test.go` holds the complete plugin.

```json
{"name": "leases", "path": "/api/examplelease/leases/search", "method": "GET",
 "versions": {"from": "26.1", "before": "26.7"},
 "variants": [{"versions": {"from": "26.7"}, "path": "/api/examplelease/leases4/search", "method": "GET",
               "fields": {"hwaddr": "hw_address"}, "adapter": "unwrap"}]}
```

## Widgets

A **widget** is what an interface draws and what `GET /api/widget/{type}` answers
(`docs/widget-api.md`). A plugin carries widgets in its `widgets/` directory, one directory per
widget, holding its `manifest.json`, its statement files and its script, and lists them in its
manifest's `widgets`. `docs/examples/example_latency` is a complete one.

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

A widget's statement files hold named statements, each beginning with a marker line,
`-- statement: widget_<name>`:

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

The script exports `widget.serve`, handed the host and answering `{data, has_rows}`:

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

The host is one object per area of the core, `host.<area>.<function>`: the widget's own statements
(`host.query`), the parsed request (`host.request`, `host.params`, `host.refs`), the envelope's states,
coverage and conditions (`host.envelope`), and the figures every widget shares -- volumes, flows,
sites, peers, rates, samples, series, the data catalogue (`host.datasets`) and the rest. It grants no
firewall call, no fetch and no write. A function called wrongly throws a `TypeError`. Every area is
documented, function by function, in `docs/widget-host.md`, which a test holds to the areas the host
has. A row or figures a host function hands is read from the core's own value: its members are read,
set, added and deleted as any object's, but it takes no symbol, cannot be frozen, and a member cannot
become an accessor or read-only (`Object.assign({}, row)` is a plain copy). Two rules hold for every area:

- **A cell of a statement is typed for the script**: a `numeric` whose value is whole -- an integer sum
  is one -- is an integer and any other `numeric` a number with a fraction, so a script never meets
  PostgreSQL's numeric typing; a `BOOLEAN` column is a boolean, and `CAST(x AS INTEGER)` reads one as
  `0` or `1`. The connection itself converts nothing: this is the plugin host's conversion, made for a
  plugin's and a widget's statements only (decided, question 6 of the plugin-API amendment).
- **A widget that reads domain names withholds them.** Under `aggregate_mode` = `no_domains`, no answer
  carries a domain name: a widget built on them declares `"no_domains": "whole"`, and one showing a name
  beside other figures sets it to `null` and records it with `host.envelope.withhold`. For a widget that
  is not built in the core enforces it: a statement carrying a domain name is not run, the script is
  stopped, and the answer is withheld whole (condition `withheld_by_aggregate_mode`).

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
`host.figures` and keeps what the core read whole: it sums with `host.figures.add`, which keeps
`measure` and `measure_parts` -- or, for many figures at once, `host.figures.sum(list)`, the same sum in
one call; it counts with `host.figures.count(n)` and `byteCount(figures)`
(`loggedCount(n)` stays a count of logged records); a number drawn on its own -- a node of a tree --
takes `host.figures.measure` as its measure, and its figures are handed to `host.figures.cover`, which
also takes an array of figures and judges each. And:

- a statement of the widget's own that reads volume columns declares ONE twin, `<name>_measured`,
  which serves every declared measure: the same parameters and the same columns, with the measure's
  bytes in place of the logged ones, plus `measure` (`CAST(:measure AS TEXT) AS measure`) and the
  measure's parts as `measure_parts`. A twin reading the flows joins each flow to the measure's
  figures, `flow_measure(f.id, f.pair_outcome, f.paired_flow_id, f.action, CAST(:measure AS TEXT))
  AS n`, sums `n.bytes`, and sums the parts with `measure_parts_total(n.parts, n.not_known_index,
  n.part_width) AS measure_parts`: no JSON per flow, which a 30-day read costs. `flow_measures`
  (every measure's parts in one object, summed with `measure_parts_sum` and read with
  `measure_total_bytes` and `measure_bytes`) answers the same figures, for the aggregate refresh and
  the purge. The built-in `owner_activity`, `traffic_composition` and `traffic_sankey` statement
  files show a twin over the flows; `internal/widget/traffic/buckets.statements.sql` one over an
  aggregate family's `measure_parts`.
- **a twin binds exactly its statement's parameters and `:measure`**, and is a period template exactly
  when its statement is: the core runs it in the statement's place with the statement's parameters and
  the measure's name. A widget whose twin does not is refused when it loads, with
  `measured_twin_mismatch` (`docs/widget-authoring.md`, *Refusals*).

**Nothing wrong is ever served.** A run in a declared measure that builds a figure the core did not
read in it, or that fails, is not served in that measure: `best_available` answers the logged answer
with the measure's not-served state (for netflow, `netflow_not_served_by_type`), and an explicit
`volume_measure` naming the measure answers no figure and the same state.

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

A refused declaration is `measure_invalid`. `internal/plugin/testdata/plugins/example_reporter` declares a
measure as a third party would, and `internal/plugin/thirdparty_test.go` checks every figure it serves.

### Testing a widget

A plugin that carries widgets is tested against a PostgreSQL database of its own, as one that
declares tables is: the test instance `OPNVIEW_TEST_DATABASE` names, with `opnview`'s schema
applied. Besides the functions of *Testing a plugin*:

| Function | What it does |
|---|---|
| `t.sql(statement, parameters)` | Runs one statement on the test's database, to arrange the rows a widget reads. |
| `t.query(text, parameters)` | The rows a `SELECT` returns, on the read role, instants as epoch seconds: what a derivation wrote, what a view serves. |
| `t.derive(name)` | Runs one of the plugin's derivations now, as a cadence tick would, and writes as the collector writes: `{ran, written, error}`. |
| `t.widget(type, parameters)` | Asks a widget as `GET /api/widget/{type}` asks it: `{status, condition, body, raw}`, `body` parsed and `raw` the exact bytes. A script that failed answers its reason in `body.reason`, which only a test sees. |
| `t.golden(file, raw)` | Compares an answer with `tests/<file>`, byte for byte once both are compact. Run the tests once with `OPNVIEW_WRITE_GOLDEN=1` in the environment to write the file instead, then read it before keeping it. |

The clock is the test's (`t.clock`), so a window and every instant in an answer are the same on
every run.

## The plugin API version

`api_version` is the version of everything this document describes: the manifest, the host, the
contracts and the test API. A change a plugin written for the current version would not survive
raises it, and a build reads a range of versions. A plugin targeting a version outside the range is
refused, naming both, so a format change is never half understood. This build reads versions 2 and 3:
version 2 made kinds, datasets, derivations, measures and pages declarations, and removed the
source-specific host helpers, so a version-1 plugin would not survive it, and no compatibility code
reads one; version 3 (ADR 0018) added `statements`, `relations`, `requires` and `after_derivations`
and removed nothing, so a version-2 plugin loads as it did.

## Kinds

A kind is a contract: what an implementation's script exports, and the fields of every record it
answers. **A core kind is a file**, `internal/collect/provider/kinds/<kind>.json`, found by walking
that directory: its description, its attributes (`concurrent`, `push`, `bindable`), the reasons of the
collection gaps its coordinator records (`gap_reasons`, written to the database's `gap_reason` registry,
which `collection_gap.reason` references), its cadences and page sizes, what sizing measures, its
functions, its exported values and its records. Adding a core
kind's contract is adding a file. Its records are stored in the core's own tables, so a core kind with
a poll cadence is collected only once a coordinator -- a directory under `internal/collect/kinds/`
that registers its pass with `core.RegisterPass` -- stores them; a file alone gets no loop. A kind a
plugin declares for itself needs neither (*Kinds of the plugin's own*). Every contract is rendered, table by table, in `docs/plugin-kinds.md`, which a
test holds equal to the files. The host converts what a script answers against exactly those tables:
a missing required field, or a field the record does not have, is `records_invalid`.

## Push sources

Most of what a plugin reads it **asks** the firewall for, through the broker. A push source is data
the firewall **sends** to `opnview` once the user has configured it to: syslog lines (RFC 5424 and
RFC 3164, over UDP and TCP), NetFlow records (version 9, RFC 3954; IPFIX, RFC 7011, over UDP), and
messages the core reads nothing in -- a UDP datagram (`datagram`), or a line of text over TCP or UDP
(`line`) -- which the subscribing plugin's own decode function reads. `datagram` and `line` are
`opnview`'s own terms (`docs/data-model.md`, *Vocabulary*). `opnview` never configures the export:
what the user does on OPNsense, and what it sends, is in `docs/opnsense-api-survey.md`, *Push
sources*, and in the README. Receiving is inbound: a listener makes no outbound call.

**The core knows the protocols and nothing of any program.** It holds the listeners, one decoder per
protocol -- the syslog header; NetFlow and IPFIX templates, every field named and typed by the IANA
IPFIX Information Elements registry -- and the routing. A message is decoded **once**, whoever
subscribes; a syslog line no plugin subscribes to is counted (`no_subscriber`) and dropped, having run
no regular expression and no script.

### Listening

A listener is opened by a start parameter and by nothing else, and **has no default**: a port is one
installation's configuration. `--push-listen <protocol>/<transport>=<host:port>` is optional and
repeatable -- `syslog/tcp`, `syslog/udp`, `netflow/udp`, `datagram/udp`, `line/tcp`, `line/udp`. With
none, every push declaration's availability reads `listener_not_configured`: nothing can arrive, and the
silence is not an absence of data.

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
| `filter` | Syslog: `{"program": [...], "facility": [...], "severity": [...]}`, the program (RFC 3164's TAG, RFC 5424's APP-NAME) by exact name and required. NetFlow: `{"require": [...], "where": [...]}`, Information Elements by registry name, and conditions `{element, operator, value or values}` with the data catalogue's operators; a record lacking a required element is skipped and counted (`netflow_required_element_missing`). `datagram` and `line`: `{}`, or `{"prefix": "..."}`. |
| `transports` | `udp`, `tcp`: every one its protocol is received over by default. |
| `pattern` | Syslog: an RE2 regular expression over the message (no backreference, no lookaround: the input is network data, and RE2 matches in linear time); each named group fills the column of its name. A message it does not match is counted (`pattern_unmatched`). |
| `parse` | Syslog: a function the script exports under `push`, for a message no regular expression expresses. Never with `pattern`. |
| `fields` | Columns -- or, for a `kind` entry, the kind record's fields -- filled from fields every record carries: a syslog header field (`facility`, `severity`, `timestamp`, `hostname`, `program`, `process_id`, `message_id`, `structured_data`, `message`), or a NetFlow Information Element by registry name, one or a list whose first present one wins. |
| `decode` | `datagram` and `line` only, and required there: a function the script exports under `push`, called with one message. Never with `pattern`, `parse` or `fields`. |
| `sequence_bound` | `{"value": "<name>"}`: the value, recorded over time, that bounds the sequence counters the sender runs (*Liveness and gaps*). |
| `output_table`, `kind` | Where the rows go: one of the plugin's tables, or a core kind the plugin provides (*Replacing polling*). Exactly one. |
| `replaces` | One of the plugin's endpoints, whose polling this entry replaces while it is alive. |
| `liveness_seconds` | How long the entry stays alive with no message: 120 by default, 10 at least. |
| `survey_section` | The section of the survey that establishes what the firewall sends; a built-in carries it. |

A row's values come from its pattern's groups and its `fields`, or from its function; the table's time
column, when nothing names it, takes the record's own instant; a NOT NULL column filled by nothing is
refused (`push_column_unfilled`). **An `output_table` entry belongs to a provider**: the plugin declares a
kind of its own with `push` (*Kinds of the plugin's own*) and one provider of it with `"source": "push"`,
whose availability and gaps are the entries' -- `listener_not_configured`, `no_message_received` within
the liveness span, or healthy.

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

There is no JSON type. A push output table's time column is an `instant` (`push_table_without_instant`
otherwise), named `*_at`, and the table is a hypertable chunked by day on it; the JSON API still serves
every instant as UTC epoch seconds. A column with no `role` takes the one its type implies -- an
`instant` is time, a number a measure, anything else a dimension -- and the dataset's `nature` is
`event`. A value that does not convert is null in a nullable column and refuses the row otherwise,
counted as `push_value_invalid`, never coerced. A NetFlow field is typed by its IANA abstract type:
`unsigned*` and `signed*` are `integer`, `float*` `real`, `ipv4Address` and `ipv6Address` `ip`,
`macAddress` `mac`, `dateTime*` `instant`, `string` `text`, `boolean` `boolean`, and `octetArray`,
`unsigned256` and the list types `text` in lower-case hexadecimal; an enterprise element is an octet
array under `enterprise_<number>_<id>`. Every NetFlow record also carries `exporterIPv4Address` or
`exporterIPv6Address`, `observationDomainId`, `exportProtocolVersion`, `collectionTimeMilliseconds`, and
`flowStartMilliseconds` / `flowEndMilliseconds` computed from the header and v9's switched times or
IPFIX's time elements. `ingressInterface` and `egressInterface` are the exporter's indexes, kept raw:
what an index names is the exporter's to say, read from its own endpoints by the plugin that needs it.
A `fields` mapping of any protocol may name `delivery` (the SHA-256 of the datagram or frame) and
`delivery_position`; with both in a primary key, a datagram delivered twice stores its records once.

### The parse and decode functions

```js
exports.push = {
  logins: function (messages) { return messages.map(function (m) { /* one row, or null */ }); },
  readings: function (message) { return [/* the rows, or the kind's records, it held */]; }
};
```

A **parse** function is called with a batch of at most 500 decoded syslog messages -- the header's
fields with `sender`, `transport` and `received_at`, instants in UTC epoch seconds with their fraction --
and answers a list of the batch's length: a row of the output table (or a record of the entry's `kind`)
per message, or `null` to skip it. A **decode** function is called once per `datagram` or `line`
message, `{bytes, text, sender, transport, received_at}`, and answers a list, empty for a message
holding nothing. A `line` over TCP is one frame per line; a `line` datagram may hold several lines. A
call that throws, runs past its bound or answers anything else fails that batch or message alone:
counted (`script_failed`, `script_timeout`, `records_invalid`) and recorded as a `push_script_failed`
gap. Neither `datagram` nor `line` carries a sequence number the core reads, so their loss is detectable
over TCP alone, and a `kind` entry of either is received over TCP alone. A plugin mapping OPNsense
versions to scripts takes its functions from the script of the firewall's version.

**Instants.** An RFC 5424 timestamp, or an ISO one carrying its offset, is read as it stands; an RFC
3164 timestamp and an ISO one with no offset are read with the firewall's measured offset from UTC and
the filter log's year rule. The reception instant is recorded for liveness and gaps, and never
substituted for a header instant that parsed.

### Senders

**Only the configured firewall may send.** The accepted addresses are derived at run time, every
minute: those the configured base URL's host resolves to, and every address the firewall's interfaces
hold as discovery read them. Any other sender is refused before it is decoded and counted per address
in the API catalogue. **This is a source-address check: it does not authenticate the firewall, and a
forged source address on the path passes it.** `--push-extra-senders <address>[,<address>...]`
(`OPNVIEW_PUSH_EXTRA_SENDERS`), empty by default, accepts more, for a network that rewrites the source
of a published port, as Docker Desktop's port publishing does; **naming such a gateway accepts whatever
reaches the published port**, so the host running `opnview` then filters the port to the firewall's
address.

### Liveness and gaps

An entry is **alive** while messages it accepts have arrived within its liveness span, and stops being
alive after the span and half of it again. The interval it was not alive is a gap of its provider
(`push_not_alive`) -- an entry that never received anything since its listener opened included -- so a
bucket there is a gap, never a zero. **Every queue is bounded, and every drop is a gap**, merged with
the drops of the same reason less than ten seconds apart:

| Reason | What it records |
|---|---|
| `push_buffer_overflow` | `opnview`'s own queues were full: a datagram found its listener's decode queue full, or a record its entry's queue |
| `push_store_failed` | a batch that could not be committed after its retries |
| `push_listener_down` | the interval during which the listener was not listening, `opnview`'s own downtime between runs included |
| `push_script_failed` | a parse or decode function call that failed, over its messages' reception instants |
| `push_not_alive` | the entry received nothing for longer than its liveness span |
| `netflow_sequence_gap` | a jump in an exporter's sequence numbers, between the export times on either side of it |
| `netflow_template_missing` | NetFlow data whose template never arrived within the bound |

The last two are the NetFlow decoder's own: a protocol declares the gap reasons it records (`GapReasons()`), and the core composes their catalogue keys, `gap_reason.<reason>`, from that declaration.

**Loss on the wire.** Syslog over UDP carries no sequence number, so every dataset a push entry feeds
carries `loss_detectable`: true when every transport it accepts detects loss (TCP syslog, NetFlow).
One NetFlow observation domain may carry several sequence counters -- OPNsense runs one per exporting
interface (survey, *Push sources*, S11) -- and the decoder follows several. A `netflow_sequence_gap` is
asserted only once more counters are open than the sender runs, which the entry's `sequence_bound`
names (the netflow plugin's `collect` returns it under `bounds`); with the number unknown no gap is
asserted (`sequence_gap_not_asserted`, the records' `loss_detectable` false). A restart of the exporter
-- its uptime going backwards -- reaches the subscribers' derivations as the condition
`netflow_exporter_restarted`.

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

An entry with `kind` delivers records of a core kind the plugin provides -- built by its function or
filled by its `fields` mapping, checked as a pulled answer is -- and only over a transport whose loss is
detectable. When a polled function of the kind can return the same `<kind>_record` table (today
`firewall_log`'s `records`), the entry must name the endpoint it `replaces`
(`push_kind_without_replacement` otherwise); a push-only kind takes none. The check reads the contract,
never the plugin.

**Nothing is counted twice.** The provider's history is cut into **source periods** that never overlap,
and a record is stored only if its instant lies in a period of the path that delivered it; the other
path's records are **superseded**, counted and not stored. When the entry becomes alive, polling goes on
while the pushed records are held, then closes its period at a boundary the push path covers and stops;
when the entry stops being alive, its period closes at the last second it delivered and polling resumes
at once, a gap it detects starting at the boundary. The periods, the mode and the superseded count are
in the entry's catalogue entry. A built-in declares `replaces` only when a parity test passes: the same
recorded lines, pulled and pushed, give records equal in every field but the identity.

**A polled kind that also receives records.** A kind of the plugin's own declared `push` may have one
provider its script polls (`probe`, `collect`) whose push entries write the plugin's tables; its
availability is the read's and the entries' together. The built-in `netflow` plugin is the example
(`plugins/netflow`): its entry maps each record's Information Elements into `netflow.flow_record` with
no script, its `collect` records the exporter's capture configuration and returns the sequence bound,
and its derivation `join` maps each record's indexes, joins it to the logged leg they name, keeps the
unmatched records in counter tables, and counts each direction of a logged connection once as the
measure `netflow` (`docs/data-model.md`, *NetFlow volumes*). Nothing of it is core: a second exporter is
a plugin of its own, with its own kind, tables and measure.

### What an export changes for a widget

Every row of `docs/widget-api.md`, *Coverage of the mockup*, names in its last column one of these
classes: what a push export changes for it, and what is served without the export.

| Class | Re-examination |
|---|---|
| `none` | The row reads nothing a push export carries: the samples of `traffic/top` and `traffic/interface`, the firewall's telemetry, discovery, the rule table, or `opnview`'s own records. No export changes it. |
| `filter-log` | The filter log may arrive by remote syslog: the `pf` plugin's entry `filter_log` receives program `filterlog` over TCP (survey, *Push sources*, S1 to S4) and replaces the poll of `/api/diagnostics/firewall/log` while it is alive (*Replacing polling*). Its records equal the polled ones but for the identity, so the row is unchanged; a decision series' `complete_until` then trails reception rather than the poll. Without the export: polled, the same answer. |
| `netflow` | **Export-only second measure.** With a NetFlow v9 export to the `netflow/udp` listener (*Reporting > NetFlow*; 26.7.3 offers v5 or v9 and no IPFIX, S5) every interface may be captured: each record is joined from its own interface indexes and each direction of a connection counted once, and the row's Volumes answer in the `netflow` measure (`volume_measure` `netflow`, or `best_available` when every counted connection is covered): `measure: "netflow"`, `packets`, `coverage`. Without it: the `logged` figures under `best_available`, or no figure (`data: null`) under `netflow`, with one named state: `netflow_not_configured`, `capture_configuration_unknown`, `netflow_not_covering_window` or `netflow_not_served_by_type`. A direction NetFlow cannot measure carries its state in `coverage.direction_states` (`ingress_not_captured`, `interface_not_exported`). Ingress is captured before pf and egress after it (S11): a packet pf blocks on ingress is exported, one it blocks on egress is not. |
| `site-names` | Site names come **only from resolver lookups** (the `unbound` plugin: `search_queries`, the resolver cache). S13 is negative: Suricata's syslog output carries type `alert` only, so `tls.sni` and `http.hostname` cannot be received over syslog on 26.7.3, and no push source names a site. |
| `unbound-log` | Unbound's query and reply lines (S8) reach a syslog target only with no program filter (S3) and carry **no verdict and no blocklist**. They are stored as the dataset `unbound.query_log` (kind `unbound_query_log`) and replace nothing: refusals, lists and lookups still come from the polled `search_queries`. |
| `suricata` | Suricata can send **alerts only** to syslog, as eve JSON, program `suricata` (S7). Receiving them is deferred to step 6; nothing is built. Without it: the polled `query_alerts`. |

### The catalogue and the tests

The API catalogue carries a `push` part: each listener with its protocol, transport, state, counts, the
programs or templates it has seen and its drops; each entry with its plugin, destination, columns,
transports, listener state, liveness, stored rows and counters; the accepted-sender set's size and the
refused senders. It never carries a message, a field value, a credential or the base URL.

A push entry is tested by delivering recorded messages through the real decoder and router, never by
calling its function directly:

| Function | What it does |
|---|---|
| `t.deliverSyslog(lines, {transport, sender})` | Delivers syslog messages, one per line; TCP by default, from the test world's firewall address. |
| `t.deliverPackets(protocol, packets, {sender})` | Delivers datagrams, each hexadecimal text, over UDP through the named protocol's decoder (`netflow`, `datagram`, `line`). |
| `t.acceptSender(address)` | Adds an address to the accepted senders. |
| `t.pushed(entry)` | What the entry stored: rows (instants as epoch seconds, addresses as text), or records of its kind. |
| `t.pushCounters(entry)`, `t.listenerCounters(protocol, transport)` | The counters. |
| `t.fixture(file)` | The body of a recorded fixture of `testdata/`, such as a list of lines. |

## Worked examples

Each example is a complete plugin directory: copied as it is into `<data-dir>/plugins/`, it loads and
passes its own tests, and a test of the repository does exactly that for every directory of
`docs/examples/`. Read the files; the tests state what each one does.

| Example | What it shows |
|---|---|
| `docs/examples/example_leases` | A source plugin of a core kind (`dhcp_lease`) declared with no line of code: a `probe` checking a service status and a `read` mapping each row's fields. |
| `docs/examples/example_domains` | A service plugin (`registrable_domain`) that downloads a list of suffixes with `host.fetch`, keeps the copy in its data directory, and asks for it again only when it changed. |
| `docs/examples/example_latency` | A widget computing what a query cannot -- each target ranked by a percentile of its samples -- with its statements, its script and a golden answer written once with `OPNVIEW_WRITE_GOLDEN=1`. |
| `docs/examples/example_logins` | A syslog program into a table with a regular expression, and no script. |
| `docs/examples/example_pairs` | A syslog parse function, for messages of `key=value` pairs no regular expression of fixed groups expresses. |
| `docs/examples/example_derived_sql` | The write primitive (ADR 0018) with no JavaScript at all: readings received over syslog, a SQL-only derivation totalling them per meter in its own schema, and a relation file publishing a view and a function. |
| `docs/examples/example_requires` | A plugin that `requires` the one above and whose widget reads its view by its qualified name; its tests load the required plugin first. |
| `docs/examples/example_flows` | A NetFlow subscriber keeping each flow record's volume. A packet can be exported more than once -- on the interface it arrived by, translated on the one it left by, again by its VLAN's parent (survey, *Push sources*, S5 and S11) -- so a sum of this table counts such a packet once per copy; the netflow plugin's `netflow.flow_record` names each copy. |
| `internal/plugin/testdata/plugins/example_reporter` | The third-party proof of PA20: a `datagram` push entry decoded by its script, a derivation run after each batch and on a cadence that reads `leg`, `this_firewall_address` and `interface`, a measure with a covered and an uncovered state and a marker, a settings page, and a widget declaring `volumes` whose statement declares its `_measured` twin. |
| `internal/plugin/testdata/example_decoded` | `datagram` and `line` entries decoded by a JavaScript `decode` function. |
| `internal/collect/testdata/example_derived` | A derivation writing tables and counters after passes, batches and a cadence. |
| `plugins/<name>/` | Every built-in, written exactly as a third party writes one: `kea` and `dnsmasq` for leases, `unbound` for lookups and a syslog entry into a table of its own, `pf` for a syslog `kind` entry replacing polling (`replaces`), `netflow` for a measure, its derivation and a polled kind that also receives records, `pf_states` for durable state. |

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
