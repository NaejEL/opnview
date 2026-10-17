# Front plugins

The front -- the interface a browser draws -- is extended the way the backend is: a part is a file
or a directory dropped into the data directory, read when `opnview` starts. Adding one edits no file
of the repository and needs no Go change. This document is the contract
(`specs/SPEC-step-7-frontend.md`, A1 and A2); `docs/plugins.md` is its backend counterpart, and the
same rules of precedence and reload hold for both.

## Where the parts live

| Part | Location | Form |
|---|---|---|
| Theme | `<data-dir>/themes/<id>.json` | one file |
| Language | `<data-dir>/languages/<code>.json` | one file |
| Renderer of a plugin widget | `<data-dir>/plugins/<plugin>/widgets/<type>/`, named by the manifest's `renderer.entry` | a file of the widget's directory |
| Operator widget with a renderer | `<data-dir>/widgets/<type>/` (manifest, query, renderer) | a directory |
| Front-only widget | `<data-dir>/widgets/<type>/` (manifest and renderer, no query, no script) | a directory |

The built-in parts live in the repository: the core front in `front/` (the shell, the core scripts
and stylesheet, the views, the six theme files, the colour-token set `front/tokens.json` and the
front's own strings `front/locale/en.json`), and each built-in widget's renderer, when it has one,
in its own directory of `plugins/<plugin>/widgets/<type>/`.

**Precedence.** The built-in wins every collision. A part whose id, language code or type a
built-in or another part already takes is refused `name_collision`, listed, and the service starts
without it.

**Reload.** Parts are read at start only: a part added to a running service takes effect at the next
restart.

**Refusals are never fatal.** A part that breaks a rule is left out with a closed code and the path
of its file, written to the service's log and listed by `GET /api/widgets` under `refused`; the
Settings view shows the listing.

## Theme file

```json
{
  "format_version": 1,
  "id": "example-sepia",
  "name": { "en": "Example sepia" },
  "modes": {
    "light": { "bg-app": "#f8f9fa", "...": "every token of the set" },
    "dark":  { "bg-app": "#1c1c1e", "...": "every token of the set" }
  }
}
```

- `id` equals the file's base name and is lower-case letters and digits joined by hyphens.
- `name` is keyed by language code; `en` is required. A palette's proper name is data.
- `modes.light` and `modes.dark` are both required, and each maps **exactly** the colour tokens of
  `front/tokens.json` -- the set the step-3 mockup's palette blocks use, `bg-app` to
  `shadow-color` -- written without their leading dashes.
- A value is a CSS hex colour, `rgb()`, `rgba()`, or `var(--<token of the set>)`; a chain of `var()`
  that comes back on itself is refused.
- Non-colour tokens -- the radius, the fonts, the spacing -- are the core's and cannot be themed.

The browser never reads a theme file to draw: at start every accepted theme becomes two blocks of the
generated stylesheet `/front/palettes.css`, selected by the document's `data-theme` attribute,
`<id>-light` and `<id>-dark`.

| Code | The rule broken |
|---|---|
| `theme_malformed` | not JSON, an unknown member, a `format_version` other than 1, a bad `id`, no English name, a missing mode |
| `theme_token_missing` | a mode leaves out a token of the set |
| `theme_token_unknown` | a mode maps a token outside the set |
| `theme_value_invalid` | a value is not a colour this build writes |
| `theme_id_mismatch` | the `id` is not the file's base name |
| `name_collision` | the `id` is already taken |

## Language file

```json
{ "language": "fr", "strings": { "language.name": "Français", "settings.title": "Réglages" } }
```

The format of every catalogue file of the product. The code equals the file's base name, and
`strings["language.name"]`, the language's name in itself, is required. The file may carry any key
of any namespace: the core's, a surface's, a plugin's, a widget's. A key it leaves out falls back to
English in the browser; the keys it leaves out are listed on its row of `GET /api/widgets`.

| Code | The rule broken |
|---|---|
| `language_malformed` | not JSON, an unknown member, a code that is not a language tag, a value that is not a string |
| `language_code_mismatch` | the code is not the file's base name |
| `language_name_missing` | no `language.name` |
| `name_collision` | the language is already installed (`en.json` among them) |

The language selector is shown only when two or more languages are installed.

## Widget renderer

The manifest's `renderer` is `{"entry": "<file>.js"}`, a file of the widget's own directory
(`docs/widget-authoring.md`). Absent, the widget is drawn by its shape's renderer.

A renderer is a **classic script**: no `import`, no `export`, no module syntax, so the same file runs
in the browser and in the interpreter the front's tests run it in. Every front file follows one
shape:

```js
(function (root) {
  "use strict";
  var opnview = root.opnview || (root.opnview = {});
  /* register the renderer through the call the core documents */
})(this);
```

It is a pure function from the widget's answer, its parameters, its size, a string lookup, the
formatters and the shape kit to a **view tree** of keyed nodes. It never writes markup as text, never
sets an inline event handler, never writes a `style` attribute and never writes a colour: a colour is
a class of the stylesheet, a token.

### The registration call

```js
(function (root) {
  "use strict";
  var opnview = root.opnview || (root.opnview = {});
  opnview.widgets.register({
    type: "example_widget",
    render: function (input) {
      var kit = input.kit, h = kit.h, t = input.t;
      return h("div", {}, [
        h("p", {}, t("plugin.example.widget.example_widget.heading")),
        kit.value(input.data.figure, "bytes")
      ]);
    }
  });
})(this);
```

**What a file may register is taken from the URL the core loaded it from, never from what it
says.** The core publishes a widget's renderer at `/front/widgets/<type>/<entry>` and a shape's at
`/front/shapes/<shape>.js`: a widget's renderer registers its own `type` and nothing else, and never a
`shape`; a shape's renderer registers its own `shape` and nothing else. A registration naming anything
else, one made after the file has run (from a timer, say), and a second registration of a type or a
shape already drawn are refused, listed by `opnview.widgets.refusedRegistrations()` with their reason
(`not_its_own_type`, `not_its_own_shape`, `already_registered`, `not_loaded_by_the_core`) and reported
in the browser's console. The first registration wins, as the built-in wins every collision on the
server.

`input` holds:

| Member | What it is |
|---|---|
| `answer`, `data` | the widget API's whole answer and its `data` member (`docs/widget-api.md`) |
| `parameters` | the effective parameters, rendering-class ones included |
| `row` | the widget's row of `GET /api/widgets`: its shape and its manifest's parameters |
| `size` | the card body's `{width, height}` in CSS pixels |
| `t`, `tp`, `tk`, `td` | the string lookup (`front/core/strings.js`): a literal key, a plural, a composed key, a key the data carries |
| `format` | the formatters of the active language: `number`, `bytes`, `rate`, `percent`, `instant`, `duration`, `tick` |
| `kit` | the shape kit, made for this one drawing (`front/core/kit.js`) |

It answers a view tree built with `kit.h(tag, attributes, children)`. A renderer that throws, or answers
anything else, is drawn as the renderer-failure panel; the card keeps its footprint and title.

A renderer is drawn only when its answer has data to draw. The conditions with nothing to draw -- the
four source conditions, no rows in the period, an unresolved reference, a missing parameter, a
withheld answer, a failed read or script -- are panels the core draws, never the renderer's.

### The shape kit

| Function | What it draws |
|---|---|
| `kit.value(figure, unit, {field})` | one figure in its unit (`bytes`, `bits_per_second`, `share`, `seconds`), carrying its measure; a missing one is a dash, never 0 |
| `kit.none(reason)`, `kit.add(a, b)` | the dash of a figure that does not exist; the sum of two figures of one measure, `null` across measures |
| `kit.measure(m)`, `kit.measures(figures)` | the chip naming a measure |
| `kit.table(columns, rows, {remainder, total})` | a table whose dropped rows are a row of their own |
| `kit.bars(rows, {remainder, total})` | ranked bars, each `data-bar`/`data-value`, the remainder and the total rows |
| `kit.chart(spec)` | a time series at the card's size: linear by default, `smooth` through every sample, every series element with `data-sample-count`, an area only when stacked, gaps labelled, `single` or `all_series` tooltips, an aggregation to fit the pixels stated |
| `kit.spark(values, cls)` | a line of samples in a cell, one mark per sample |
| `kit.donut(slices, options)`, `kit.map(points, {cluster})`, `kit.matrix(rows, columns, cell)`, `kit.sankey(spec)`, `kit.feed(columns, entries, {sources, remainder})`, `kit.chips(chips, remainder)` | the other shapes' drawings |
| `kit.pick(subject)` | marks a node as opening a detail about a subject: `{kind, key, title, facts, related, reference, about}`; `reference` offers "scope this canvas to it", `about` "add a widget about it" |
| `kit.tip(nodes)` | gives a node a tooltip |
| `kit.join(items)`, `kit.expand(key, summary, content)`, `kit.legend(items)`, `kit.note(text)`, `kit.remainder(members, value)` | values side by side, a row the reader expands, a legend, a note, a remainder |

Colours are the stylesheet's classes only: `s1`..`s6` strokes, `f1`..`f6` fills, `sw-1`..`sw-6`
swatches, `fallowed`, `fblocked`, `fother`. A renderer that needs a word adds it to its own catalogue
file (*Strings of a widget*) and asks for it by its literal key.

A renderer runs in the signed-in reader's page, with that reader's privileges. The page's policy stops
it reaching any other origin, but not calling a write route of this server: a front plugin is trusted
as an operator-installed backend plugin is.

| Code | The rule broken |
|---|---|
| `renderer_invalid` | the slot is not `{"entry": "<file>.js"}`, the entry is not a plain `.js` file other than the script, or the file does not compile as a classic script |
| `renderer_missing` | the directory does not hold the entry |

## Front-only widget

A widget manifest with a `renderer` and neither `queries` nor a `script`. Nothing on the server
answers it: `GET /api/widget/{type}` answers `404` `{"condition": "front_only_widget"}`, and its
renderer asks the core for what it draws, limited to `GET /api/widget/{type}`, `/api/datasets`,
`/api/datasets/{name}/query` and `/api/live/strip`.

Its renderer registers a `requests` function beside `render`. `requests({parameters})` answers the
list of paths to read, each one of the four routes above with its query string; the core reads
them on this server, on the canvas's schedule, and calls `render` with `input.data` holding one
`{path, status, body}` per path, in the order asked. A path is matched as written: a `.` or `..`
segment, or any escape (`%`) in the route, is outside the four routes whatever it would resolve to,
and a dataset's name is dot-separated words. A path outside the four routes, a `requests`
that throws or answers something other than a list, and the card draws its failure panel without
reading anything. The parameters are the widget's manifest parameters, the canvas's period and
scope applied as for any widget.

## Worked examples

Four complete parts, one of each kind. Each is a set of files under `testdata/front-plugins/` in the
repository, and a test copies each one where this section says, starts a server over that data
directory and reaches it (`internal/web/surfaces/frontend/workedexamples_test.go`). Adding any of
them is copying files and restarting `opnview`: nothing is compiled and no file of `opnview` is
edited.

### A theme

Copy `example-sepia.json` to `<data-dir>/themes/example-sepia.json`. It is the industrial theme's
file with its `id`, its `name` and a few colours changed: every token of the set is still mapped,
in both modes, since a theme that leaves one out is refused.

```json
{
  "format_version": 1,
  "id": "example-sepia",
  "name": { "en": "Example sepia" },
  "modes": {
    "light": { "bg-app": "#f4ecd8", "accent": "#7a4b1e", "...": "every token of the set" },
    "dark":  { "bg-app": "#231c14", "accent": "#d9a066", "...": "every token of the set" }
  }
}
```

After the restart, `GET /api/front` lists `example-sepia`, the palette picker offers *Example
sepia*, and `/front/palettes.css` holds its two blocks.

### A language

Copy `en-GB.json` to `<data-dir>/languages/en-GB.json`:

```json
{
  "language": "en-GB",
  "strings": {
    "language.name": "English (United Kingdom)",
    "appearance.palette": "Colour palette",
    "canvas.range": "Time range"
  }
}
```

After the restart, `GET /api/front` and the appearance's `languages` list `en-GB`, so the header
shows the language picker -- two languages are installed -- and `GET /api/strings/en-GB` answers the
three keys. Every key the file leaves out is drawn in English, and is listed on the language's row
of `GET /api/widgets` under `missing_strings`.

### A front-only widget on the data catalogue

Copy the directory `example_lookups_by_hour/` to `<data-dir>/widgets/example_lookups_by_hour`. It
holds three files and no query or script:

```text
example_lookups_by_hour/
  manifest.json        {"manifest_version": 1, "type": "example_lookups_by_hour", "shape": "table",
                        "windowed": true, "parameters": [{"key": "period", ...}],
                        "renderer": {"entry": "renderer.js"}}
  renderer.js          requests and render
  locale/en.json       widget.example_lookups_by_hour.title, .question, .column.*, .parameter.period
```

The renderer asks for one read of the core's `dns` dataset, summed per hour over the widget's
period, and draws one table row per bucket -- the hour and its lookups, a bucket the collection did
not cover being a dash and never a zero:

```js
opnview.widgets.register({
  type: "example_lookups_by_hour",
  requests: function (input) {
    var query = { period: input.parameters.period || "24h",
      x: { column: "looked_up_at", bucket: 3600 }, y: [{ column: "lookups", aggregation: "sum" }] };
    return ["/api/datasets/dns/query?parameters=" + encodeURIComponent(JSON.stringify(query))];
  },
  render: function (input) {
    var kit = input.kit, t = input.t, format = input.format;
    var read = (input.data || [])[0] || {};
    var series = read.body && read.body.data && read.body.data.series ? read.body.data.series : [];
    var rows = (series.length ? series[0].buckets : []).map(function (bucket) {
      return { key: String(bucket.from), cells: [format.instant(bucket.from),
        bucket.value === null ? kit.none() : kit.value(bucket.value, "count")] };
    });
    return kit.table([
      { key: "hour", label: t("widget.example_lookups_by_hour.column.hour") },
      { key: "lookups", label: t("widget.example_lookups_by_hour.column.lookups"), num: true }
    ], rows, { empty: t("table.no_rows") });
  }
});
```

After the restart, `GET /api/widgets` lists it with `backend: false` and its `renderer_url`, the
picker offers it, and `GET /api/widget/example_lookups_by_hour` answers `404` `front_only_widget`.

### A renderer carried by a plugin widget

Copy the directory `example_latency/` to `<data-dir>/plugins/example_latency`. It is a widget plugin
(`docs/plugins.md`, *Widgets*): a table of probe latencies, and one widget whose script answers the
slowest sample per target and whose own renderer draws it as ranked bars.

```text
example_latency/
  plugin.json                           the table "probe", "widgets": ["example_latency_targets"]
  locale/en.json                        plugin.example_latency.widget.example_latency_targets.*
  widgets/example_latency_targets/
    manifest.json                       "queries", "script" and "renderer": {"entry": "renderer.js"}
    targets.statements.sql              the slowest sample per target in the window
    targets.js                          the script: {data: {targets: [...]}}
    renderer.js                         ranked bars; show_samples, a rendering parameter
```

The manifest's `renderer` is what this section adds to a widget plugin; everything else is the
plugin contract. The renderer reads `input.data.targets` and `input.parameters.show_samples`, and
asks its plugin's catalogue for its one word, the samples line, by its literal key:

```js
opnview.widgets.register({
  type: "example_latency_targets",
  render: function (input) {
    var kit = input.kit, tp = input.tp;
    var showSamples = input.parameters.show_samples !== false;
    return kit.bars(((input.data && input.data.targets) || []).map(function (row) {
      return { key: row.target, label: row.target, value: row.worst_ms,
        valueNode: kit.value(row.worst_ms / 1000, "seconds"),
        sub: showSamples ? tp("plugin.example_latency.widget.example_latency_targets.samples", row.samples) : null };
    }));
  }
});
```

After the restart, `GET /api/widget/example_latency_targets` answers the plugin's data, `GET
/api/widgets` lists the type with `renderer_url` `/front/widgets/example_latency_targets/renderer.js`,
and the card is drawn by this renderer rather than by the `ranked_bars` shape's.

## Strings of a widget

- A plugin widget's strings are its plugin's `locale/<lang>.json`, under
  `plugin.<plugin>.widget.<type>.title`, `.question`, `.parameter.<key>` and `.value.<key>.<value>`.
- An operator or front-only widget carries `locale/<lang>.json` in its own directory, every key under
  `widget.<type>.`. A file defining a key outside that namespace is refused
  `string_outside_namespace`, and the widget is served without its strings.

## What the browser is served

Only what the front's index declares: the core's scripts and stylesheet, the generated palette
stylesheet, each theme file, each renderer entry. A widget's server-side script, a statement file, a
manifest or any other file of a plugin or of the data directory is never served; any other path
under `/front/` answers `404`. Every asset and the shell carry the policy
`default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; font-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'`,
`X-Content-Type-Options: nosniff`, `Cache-Control: no-cache` and a content `ETag`. The front loads
nothing from any other origin: no CDN, no web font, no remote icon.
