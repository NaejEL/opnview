# Widget authoring

This is the reference for writing a widget: the manifest, the query, the twelve shapes' column
contracts, the refusals. `docs/extending.md` is the procedure with a worked example; read it
first. `docs/widget-catalogue.md` says what the twelve shapes draw and what the built-in presets
mean; `docs/widget-api.md` says what every response carries.

## What a widget is

**A widget is a directory.** It holds:

| File | What | Required |
|---|---|---|
| `manifest.json` | the widget's type, shape, bindings and parameters | yes |
| a query file (`query.sql`) | one read-only `SELECT` | for a declarative widget |
| `<name>.statements.sql` | named statements, which a widget's script runs by name | for a widget with a script |
| `<name>.js` | the script: the logic a shape cannot express | only when the shape cannot express the widget |

There are two places a widget directory can be:

* **`<plugin>/widgets/<type>/`** — a widget a plugin carries: `plugins/<plugin>/` in the repository
  for the built-ins, embedded in the binary, and `<data-dir>/plugins/<plugin>/` for anybody else's,
  read when opnview starts. It is declarative, or answered by its script. `docs/plugins.md`,
  *Widgets*, is the reference for the script, its statements and its host. Adding one is adding the
  directory; nothing is compiled and no file of `opnview` is edited.
* **`<data-dir>/widgets/<type>/`** — an operator widget, read when opnview starts. It is declarative:
  a manifest and a query. Adding one is writing two files and restarting.

A widget that breaks a rule below is **refused with a named reason, logged, and the service starts
without it**; it never stops the service, and it never replaces a built-in. A built-in that breaks
one is a defect of the build: `widget.New` returns an error naming it and the test suite fails.

The directory name is the widget's type, and the manifest repeats it; a manifest whose type differs
from its directory is refused (`type_mismatch_with_directory`).

Everything a widget needs sits in its directory: its manifest, its SQL, its script when it has one.
It reaches the core through exactly two doors: the manifest (read by the loader) and, for a widget
with a script, the widget host (`docs/plugins.md`, *The widget host*).

## The manifest

`manifest.json` is a JSON object. **An unknown field is refused**, so a misspelt key is an error and
not a silently ignored one.

```json
{
  "manifest_version": 1,
  "type": "client_volume_ranking",
  "shape": "ranked_bars",
  "providers": ["firewall_log", "dhcp_lease"],
  "primary": "firewall_log",
  "windowed": true,
  "no_domains": "none",
  "lower_bound": true,
  "measure": "logged",
  "parameters": [ ... ],
  "references": ["interface"],
  "queries": ["query.sql"],
  "renderer": { "reserved": true }
}
```

| Field | Type | Meaning | Default |
|---|---|---|---|
| `manifest_version` | integer | The format version. This build reads `1`; any other value is refused (`unsupported_manifest_version`) so a format change is never half understood. | required |
| `type` | string | The widget's identifier: `snake_case`, at most 64 characters, the last element of its API path `/api/widget/{type}`. | required |
| `shape` | string | One of the twelve catalogue shapes: `figure`, `ranked_bars`, `time_series`, `table`, `matrix`, `map`, `donut`, `tree`, `graph`, `sankey`, `feed`, `cards`. | required |
| `providers` | array of strings | The provider kinds the widget binds, from `firewall_log`, `security_event`, `flow_volume`, `dhcp_lease`, `dns_lookup`, `geo_asn`, `measurement_sample`, `reconciled_state`, `public_suffix`, `resolver_cache`, `resolver_local_data`. Each response carries the availability of every registered provider of each kind. | `[]` |
| `primary` | string | The bound kind whose condition is the widget's condition. Empty for a widget that reads only opnview's own records: its condition is then `reachable` or `reachable_no_rows`. | `""` |
| `windowed` | boolean | The widget takes a `period` parameter and answers over a rolling window ending now. Requires a `period` enum parameter drawn from `1h`, `24h`, `7d`, `30d`. A `time_series` must be windowed. | `false` |
| `no_domains` | `"none"` or `"whole"` | The aggregate mode `no_domains` withholds domain names. `whole`: the widget is built on domain names and is withheld entirely (condition `withheld_by_aggregate_mode`, no data, the query never runs). `none`: the widget carries no domain name. A widget that shows a domain name **must** say `whole`. | `"none"` |
| `lower_bound` | boolean | The widget's byte and connection figures are lower bounds: opnview sees only what crosses the firewall, and traffic between two clients behind one interface is invisible. Every figure the engine emits then carries `lower_bound: true` beside its `measure`. | `false` |
| `measure` | string | What the widget's figures measure, `snake_case`, carried beside every figure the engine emits. Built-ins use `logged`, `sampled` and `interface_counter`. | `"query"` |
| `volumes` | boolean | The widget serves Volume or byte Count figures. The core then gives it the parameter `volume_measure` -- `logged`, `netflow` or `best_available`, `best_available` by default -- which the manifest does not declare and may not, and answers it under the measure that parameter chooses (`docs/widget-api.md`, *Volumes and the observation-point limit*; `docs/plugins.md`, *Volume figures and their measure*). Only a windowed widget with a `script` declares it. | `false` |
| `parameters` | array | The keys the widget accepts (below). | `[]` |
| `references` | array of strings | The reference kinds the widget accepts: `interface`, `client`, `rule`, `owner`, `provider`, `country`, `operator`, `site`. Validated against the reference parameters: each must be accepted by some parameter, and every reference parameter's kind must be listed. (A `series` parameter accepts every kind.) | `[]` |
| `queries` | array of strings | The widget's query files, plain file names ending in `.sql`, in its own directory. A declarative widget lists exactly one. | `[]` |
| `script` | string | The JavaScript file, in the widget's own directory, that answers the widget; its `queries` are then statement files of named statements. Only a widget a plugin carries has one; `docs/plugins.md`, *Widgets*, documents it. | none |
| `renderer` | object | **Reserved for the interface work of step 7.** Declared and validated, not built. The only accepted value is `{"reserved": true}`, which is also what an absent field means. | `{"reserved": true}` |

### Parameters

Each entry of `parameters` declares one key the widget accepts. The accepted keys of a widget are
exactly these; a key the request carries that the widget does not declare is reported in the
response (`unknown_parameters`) and ignored, and an invalid value takes the default and is reported
(`invalid_parameters`). Values are never clamped.

| Field | Meaning |
|---|---|
| `key` | `snake_case`, unique within the widget. For a declarative widget it is also the name of the query's parameter `:key`, so `from`, `to` and `now` are reserved (`reserved_parameter_key`). |
| `kind` | one of the ten below (`unknown_parameter_kind` otherwise) |
| `values` | the vocabulary of an `enum` or `enum_set` (required there, forbidden elsewhere) |
| `default` | what an absent or invalid key takes; must itself be a valid value (`bad_default`). Reference kinds default to `null`: a reference names a row of one installation. |
| `min`, `max` | bounds of an `integer` or `number` |
| `reference` | the reference kind of a `reference` or `reference_list` |
| `class` | `data` (the API applies the key: two values give two different responses) or `rendering` (accepted and echoed, never applied). Absent means `data`. |
| `required` | the widget has nothing to answer without the key (condition `missing_required_parameter`) |

The ten kinds, and what a declarative query receives for each:

| Kind | JSON value | Bound to `:key` as |
|---|---|---|
| `enum` | a string from `values` | the text |
| `enum_set` | an array of strings from `values`, no duplicates | JSON array text, for `json_each(:key)` (`[]` when none selected) |
| `boolean` | `true`/`false` | `1`/`0` |
| `integer` | a whole number | the integer |
| `number` | a number | the real |
| `string_list` | an array of free strings | JSON array text, or `NULL` when empty (no restriction) |
| `reference` | a reference object `{"kind", "by", "value"}` (`docs/dashboard-format.md`) | the resolved row id; the text for a `country` or `site`; `NULL` when absent |
| `reference_list` | an array of reference objects | JSON array text of the resolved ids (texts for `country`, `site`), or `NULL` when empty (no restriction) |
| `series` | the ordered array of objects of `custom_chart` | not bound (built-in `custom_chart` only) |
| `object` | an object passed through untouched | not bound (rendering parameters whose structure belongs to the renderer) |

A reference is resolved by **exact match on its `by` key and on nothing else**; its `label` is carried
for the repair surface and never matched. A reference that matches nothing makes the widget answer
`unresolved_reference` with no data, never zeros. A `site` reference under `no_domains` is withheld,
whatever its value. The engine does this for you; the query only receives the resolved value.

## The query contract

A declarative widget runs **one read-only `SELECT`** on the store's read connections.

* **One statement.** A second statement, however hidden, is refused (`query_multiple_statements`).
  The check reads tokens, so a semicolon inside a string, a quoted identifier or a comment is not a
  second statement, and one terminating semicolon is fine.
* **Read-only.** The text must be a `SELECT` or `WITH ... SELECT` (`query_not_read_only` otherwise:
  `INSERT`, `UPDATE`, `DELETE`, `REPLACE`, DDL, `PRAGMA`, `ATTACH`, `VACUUM`, `EXPLAIN`). SQLite then
  plans it at load, and a plan that opens a persistent table for writing is refused too — which is
  how `WITH x AS (...) DELETE ...`, a statement that starts like a read, is recognised. Beyond the
  check, the connections themselves are opened `query_only`: SQLite refuses a write whatever the text
  says, and a test proves it.
* **It plans.** The text is prepared and planned on the read connections at load
  (`query_does_not_plan` if SQLite cannot).
* **Named parameters only.** The query may use `:now`; `:from` and `:to` when the widget is windowed
  (the window `[from, to)` in UTC epoch seconds); and `:key` for each parameter the manifest declares
  (except `series` and `object`). Any other `:name`, and any `?`, `@name` or `$name`, is refused
  (`unknown_query_parameter`). A parameter the query does not use is simply not bound.
* **Sets arrive as JSON text.** Use `json_each`:
  `(:interfaces IS NULL OR f.src_interface_id IN (SELECT value FROM json_each(:interfaces)))`.
  `NULL` means "no restriction".
* **Do not `LIMIT` the rows the shape truncates.** Declare a `limit` integer parameter instead: the
  engine applies it and reports what it dropped as an explicit remainder, so the parts and the
  remainder make the whole. A `LIMIT` in the query would drop members silently.
* **Return the columns of the shape's contract** (below). A column the contract requires and the
  query does not return is refused (`missing_column`); extra columns are ignored by every shape
  except `table`, `feed` and `cards`, which carry them.
* **Be indexed.** The engine cannot make a query fast. A query that scans a growing table
  (`flow`, `security_event`, `dns_resolution`, ...) is slow on a large installation; bound it with
  `:from`/`:to` against an indexed time column, as the worked example does.

The check order is: empty, statement count, first word, parameters, plan (write opcodes), columns.
The first rule broken names the refusal.

## What every declarative widget gets from the core

Written once, per shape, and not the query author's to get wrong:

* **The envelope**: effective parameters, the requested and covered window, the gaps, the
  availability of every provider the manifest binds with the condition each maps to, the freshness,
  what could not be resolved or parsed, what the aggregate mode withheld, and the data
  (`docs/widget-api.md`).
* **Conditions**: the four of the catalogue (`not_yet_probed`, `unavailable`, `present_but_disabled`,
  `reachable`) plus `reachable_no_rows`, from `primary`. An unavailable source with nothing to show
  is **no data**, never a zero. The only zeros are the ones the query measured.
* **Figures**: every number a shape emits is `{"measure": <measure>, "lower_bound": <bool>, "value": n}`.
* **Truncation is never silent**: a `limit` produces an explicit remainder carrying its member count
  (and its figure where the shape has one), so `parts + remainder = total`.
* **Ties are broken by key, never by label**, so an order never leaks a name the aggregate mode
  withholds.
* **A NULL is not a zero**: a row whose `value` is NULL is counted as `unmeasured` and is in no part
  and no total.
* **Duplicates add**: two rows with one key (or one row/column, one bucket and series) are one
  member and their values add.

## Shape contracts

Each shape names the columns it reads, the payload (`data`) it produces, and what it does when
there is nothing to draw. `limit` is the `integer` parameter named `limit`, when declared. Every
payload below is the `data` member of the envelope. `Measured` is the figure object above; `Rest` is
`{"members": n, "value": Measured}` (`value` omitted by shapes that carry no figure).

### `figure` — one number

Columns: `value` (required); `label`, `unit`, `previous` (optional). The first row is read.
Payload: `{"label", "unit", "figure": Measured|null, "previous": Measured|null}`.
No row, or a NULL `value`: `figure` is `null` and the widget is `reachable_no_rows`.

### `ranked_bars` — members ranked by a figure

Columns: `key`, `value` (required); `label` (optional).
Payload: `{"bars": [{"key", "label", "value": Measured}], "remainder": Rest, "total": Measured,
"unmeasured": n}`. Bars are ordered by value, largest first, then by key. `limit` truncates; the bars
plus `remainder` equal `total`.

### `donut` — parts of a whole

Columns: as `ranked_bars`.
Payload: `{"slices": [{"key", "label", "value", "share"}], "other": Rest, "total": Measured,
"unmeasured": n}`. `share` is the slice's fraction of the total, `null` when the total is zero.
`limit` folds the smallest members into `other`.

### `time_series` — values over the window

Requires `windowed`. Columns: `bucket` (UTC epoch seconds of any instant in the bucket), `value`
(required); `series` (optional; the series key, one line per distinct value).
Buckets are UTC-aligned multiples of their width: 1 minute for `1h`, 15 minutes for `24h`, 1 hour for
`7d`, 1 day for `30d`.
Payload: `{"bucket_seconds": n, "series": [{"key", "buckets": [{"from", "to", "gap", "value":
Measured|null}]}]}` — every bucket of the window, for every series, newest last. A bucket the query
returned no row for is `gap: true` with a **null** value, never a zero; a bucket with a row keeps its
value and is also `gap: true` when it overlaps an interval a bound provider is known not to have
covered. The window's `covered` interval is the span of the buckets the query returned. (Every other windowed
shape reports `covered` from where the primary provider's records begin, as the built-ins do; a widget
bound to no kind opnview stores records of reports none.)

### `table` — rows and columns

Columns: any. Payload: `{"columns": [names], "rows": [[cells]], "remainder": {"members": n}}`, in the
query's order. `limit` truncates and counts what it dropped.

### `feed` — events, newest first

Columns: `at` (UTC epoch seconds, required); anything else is carried.
Payload: `{"entries": [{column: value}], "remainder": {"members": n}}`, newest first. `limit`
truncates the oldest.

### `cards` — one card per member

Columns: `key` (required); `label` (optional); every other column becomes a field.
Payload: `{"cards": [{"key", "label", "fields": {column: value}}], "remainder": {"members": n}}`,
ordered by key.

### `matrix` — a figure at row × column

Columns: `row`, `column`, `value` (required); `row_label`, `column_label` (optional).
Payload: `{"rows": [{"key", "label"}], "columns": [{"key", "label"}], "cells": [{"row", "column",
"value": Measured}], "unmeasured": n}`. A cell the query did not produce is **absent**, never 0.

### `map` — a figure per country

Columns: `country` (ISO 3166-1 alpha-2, required), `value` (required); `label`, `latitude`,
`longitude` (optional). A row with no country is not dropped:
Payload: `{"points": [{"country", "label", "latitude", "longitude", "value"}], "unplaced": Rest,
"remainder": Rest, "unmeasured": n}`. `unplaced` counts the rows with no country and their figure;
`limit` truncates the placed points into `remainder`.

### `tree` — a hierarchy

Columns: `id`, `parent`, `value` (required; `parent` NULL for a root); `label` (optional).
Payload: `{"roots": [node], "remainder": Rest, "orphans": n, "cyclic": n}` with `node = {"id",
"label", "value": Measured|null, "children": [node], "remainder": Rest}`. Siblings are ordered by
value then id; `limit` applies to every sibling set and its remainder child counts what it dropped.
A node whose parent the query did not return is drawn as a root and counted in `orphans`; nodes whose
parents form a cycle are counted in `cyclic` and drawn nowhere.

### `sankey` — flows between nodes

Columns: `source`, `target`, `value` (required); `source_label`, `target_label` (optional).
Payload: `{"nodes": [{"key", "label"}], "ribbons": [{"source", "target", "value"}], "other": Rest,
"total": Measured, "unmeasured": n}`. Ribbons are ordered by value; `limit` folds the smallest into
`other`.

### `graph` — node-link data

Columns: `source`, `target` (required); `value`, `source_label`, `target_label` (optional).
Payload: `{"nodes": [{"key", "label"}], "edges": [{"source", "target", "value": Measured|null}],
"remainder": Rest}`, in the query's order. An edge with no value has a null one. `limit` truncates
the edges.

## Built-in widgets

Every built-in widget is a widget plugin, a directory `plugins/<plugin>/widgets/<type>/` in the
repository, embedded in the binary and loaded by the same loader as a third party's. A built-in whose
payload is not what its shape's column contract produces answers through its script, which runs its
own named statements and the shared figures of the widget host (`docs/plugins.md`, *The widget
host*): site rows, attribution, buckets and series, rates, peers and their geolocation, remainders
and lower bounds. The script returns the data and whether it holds any row -- the difference between
`reachable` and `reachable_no_rows`, and between a degraded widget that still has history to show
and one that has nothing -- and the core writes the envelope around it.

Every built-in returns a payload that `docs/widget-api.md` documents and that tests assert, built
with reconciliation rules a single query cannot express: a paired connection counted once across its
two legs, an unplaced bin per geolocation state, name attribution methods, per-field withholding under
`no_domains`, a name-free tie-break. Re-expressing one declaratively would change its payload; the
shape engine is what a *new* widget with no script uses.

## Refusals

A widget that breaks a rule is refused with one of these codes, in `Service.Refusals()` and in the
log; the reason is specific to the rule, and the other widgets still load.

| Code | The rule broken |
|---|---|
| `not_a_directory` | the widgets directory itself cannot be read |
| `manifest_unreadable` | the directory has no readable `manifest.json` |
| `manifest_invalid` | not valid JSON, an unknown field, a bad `type`, `primary`, `no_domains`, `measure` or query file name, a duplicate parameter, a windowed widget with no `period`, a `time_series` that is not windowed |
| `unsupported_manifest_version` | `manifest_version` is not 1 |
| `unknown_shape` | `shape` is not one of the twelve |
| `unknown_provider_kind` | a `providers` entry is not a provider kind |
| `unknown_parameter_kind` | a parameter `kind` is not one of the ten |
| `parameter_vocabulary` | an enum with no values, values on a non-enum, a bad `class`, an unknown reference kind, `min` above `max`, a `period` outside the periods |
| `bad_default` | a default that its own parameter would reject |
| `reserved_parameter_key` | a key the engine binds itself (`from`, `to`, `now`), or `volume_measure`, which the core gives a widget declaring `volumes` |
| `references_mismatch` | `references` and the reference parameters disagree |
| `renderer_invalid` | `renderer` is not the reserved slot |
| `type_mismatch_with_directory` | the manifest's type is not its directory's name |
| `type_collision` | the type is already served by a built-in |
| `query_missing` | no query declared, the file is absent, or it holds no statement |
| `query_unreadable` | the query file cannot be read |
| `too_many_queries` | more than one query declared |
| `query_multiple_statements` | the query holds more than one statement |
| `query_not_read_only` | the query is not a `SELECT`, or SQLite plans it as a write |
| `query_does_not_plan` | SQLite cannot prepare the query |
| `unknown_query_parameter` | the query uses a parameter nothing binds |
| `missing_column` | the query lacks a column its shape requires |
| `statement_registration_failed` | the query's statement name was already taken |
| `query_reads_credential` | a query reads `account`, `session`, `encrypted_credential`, `setting` or `plugin_state`, which hold credentials, sessions and settings |
| `statement_name_invalid` | a script widget's statement is not named `widget_<snake_case>`, or is named twice |
| `script_missing` | the script the manifest names is not in the widget's directory |
| `script_does_not_compile` | the script does not compile, throws when it is run to read its exports, or exports no `widget.serve` |

## Statements of a built-in

A built-in's statements live next to the code that runs them: `<name>.statements.sql` in the widget's
plugin directory for a statement only that widget runs, and `<core file>.statements.sql` in
`internal/widget` for the ones the core itself reads, such as the `widget_earliest_*` instants a
widget is covered from. Each statement is preceded by `-- statement: <name>` and ends with a
semicolon; the names (`widget_...`) are global, and are what a script passes to `host.query`.
`sql/schema-checks.sh` finds every such file by walking the tree and plans every statement at the
100 000- and 1 000 000-row seeds; none may scan a growing table.
