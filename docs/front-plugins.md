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
