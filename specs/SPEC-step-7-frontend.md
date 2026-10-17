# SPEC — Step 7: the full frontend (canvases, widgets, dashboard files, themes, languages and front plugins)

Status: APPROVED — by the orchestrator, 17 October 2026, under the maintainer's instruction to deliver end to end without questions. The two open questions take their stated defaults.
Cycle kind: interface

## Context

### Corrections to the Planner brief, and why they apply here

The repository's own binding rules differ from three statements in the Planner brief. Where they differ, the repository wins:

- **Store.** The store is PostgreSQL with TimescaleDB, not SQLite (`ROADMAP.md`, *Rules that apply to every step*, decision of 11 October 2026). Any new table goes into `internal/store/schema.sql`. The database is rebuilt, so there is no migration (pre-POC).
- **Site names.** Suricata is not a source of site names. Site names come from the resolver alone (`CLAUDE.md`; `docs/opnsense-api-survey.md`). The rule that the attribution method is recorded per record and shown in the UI still holds. The widget API already carries the method (`domain_attribution.method`, served as `records[].method` and `rows[].methods`).
- **Observation-point limit.** The limit is documented, not printed: "The observation limit is documented, not printed" (`ROADMAP.md`; `CLAUDE.md`). Every figure carries `lower_bound` and `measure` in the payload. The UI does not recite the limit, and this spec does not ask it to.

### What exists today

- **Backend, complete for this step.**
  - The widget HTTP API (`docs/widget-api.md`): `GET /api/widget/{type}` for 26 served types (the three alert presets answer `unknown_widget_type` until step 6), the envelope, conditions, named states and live figures.
  - The data catalogue (`/api/datasets`, `/api/datasets/{name}/query`), the live strip (`/api/live/strip`), the live and NetFlow settings, and the plugin listing and settings (`/api/plugins`, `/api/plugins/{name}/settings`).
  - People and machines (`/api/owners*`, `/api/clients*`), labels and purposes (`/api/interfaces/{id}/label`, `/api/blocklists*`).
  - A JSON equivalent of every Go-rendered page (`docs/widget-api.md`, *Settings API*): `/api/session*`, `/api/account/*`, `/api/settings*`, `/api/collection*`, `/api/interface-networks`, `/api/sources`.
- **Widgets.** Widgets are plugin directories (`plugins/<plugin>/widgets/<type>/`: `manifest.json`, statements, a goja-run script), embedded through the root `plugins.go`. Operator widgets live in `<data-dir>/widgets/<type>/` (declarative, query only). Every manifest carries `"renderer": {"reserved": true}`, a slot reserved for this step (`docs/widget-authoring.md`). No built-in widget plugin has a `locale/` directory, so no widget title, question or parameter label exists in any catalogue.
- **Go-rendered interface.** `internal/web` holds the layout (`template/layout.gohtml`), one stylesheet (`asset/style.css`) and the page templates of five surfaces: `account` (setup, sign-in, recover), `settings`, `collection`, `interfaces`, `sources`.
  - `internal/web/theme.go` offers three values (`system`, `light`, `dark`) and says the palette registry belongs to step 7.
  - `GET /` redirects to `/settings`, with the comment "canvases are step 7".
  - Strings come from a server-side catalogue merged from `internal/web/locale/en.json`, each surface's `locale/` and each plugin's `locale/`. No endpoint serves it to a browser.
- **Missing.** There is no canvas or dashboard storage, no endpoint listing the loaded widgets with their manifests, no endpoint listing reference candidates (interfaces with their `user_label`, rules, countries, operators, sites), and no YAML library in `go.mod`.
- **Toolchain.** goja (`github.com/dop251/goja`) is already a dependency. The development container has no Node and no browser.
- **The mockup.** `docs/mockups/canvas-mockup.html` (12 500 lines, approved in step 3; `specs/SPEC-canvas-mockup.md`) is the visual and behavioural reference:
  - a two-row header (identity and liveness; palette, mode and edit-layout controls);
  - a rail (Canvases; Dashboard: editor and Sources; Installation: People), with a canvas bar holding the tabs and the range;
  - a 12/8/1-column grid derived from the container's width, cards with header chips, and an anchored detail popover;
  - one dock with three tabs (Document, This widget, Form), a YAML/JSON radio, one Apply and one Revert;
  - an add-widget picker, edit mode (drag plus keyboard buttons), an import report, and repair with in-place line marks;
  - six palettes times two modes as token blocks.
- **The mockup's defects**, listed in `ROADMAP.md` step 7, *What the step-3 mockup proved*: it rebuilds the whole canvas every 2 s; its only inspection path is editing; it has no canvas scope; it has no remove action and no undo; its picker gives no basis for choosing; some of its controls are inert.
- **Mockup harnesses.** `docs/mockups/check-*.js` encode four behaviours this step keeps as automated tests: rendering parameters are never inert; drawn parameters change the drawing; the form covers the catalogue; the connection tree reconciles across sides.

### Binding documents

`docs/ui-references.md` (with *The maintainer's recorded preferences* and the palettes at the values of https://lequellec.xyz), `docs/widget-catalogue.md`, `docs/dashboard-format.md`, `ROADMAP.md` steps 3 and 7, and the mockup. This spec cites them and does not restate them. A design decision that contradicts one of them without declaring it is a defect.

### Two defects in the binding documents this step must fix

- **The worked example uses wrong metric names.** In `docs/dashboard-format.md` the `custom_chart` example uses metrics `cpu_percent` and `temperature_celsius`. The widget API's vocabulary is `cpu` and `temperature` (`docs/widget-api.md`, *Series of `custom_chart`*), so the example would come back as `invalid_parameters`.
- **Undocumented canvas `period`.** The mockup writes a canvas-level `period` that the format document does not define.

## Scope

The step is built in three lots, in order. Each ends with `docker compose run --rm checks` green and its own acceptance criteria met.

| Lot | Name | Content |
|---|---|---|
| 1 | **Seams, shell and installation views** | Front plugin discovery and serving; the shell; strings; appearance; the dashboard store and API; the widget registry and reference endpoints; the session views and the installation views; **removal of the Go-rendered pages**; the goja test harness |
| 2 | **Canvases and widgets** | The tabs and the grid; the canvas period and scope; the card chrome; every served preset's renderer and the twelve shape renderers; degraded states; detail and pivot; live refresh by reconciliation; the default dashboard |
| 3 | **Editing and portability** | Edit mode; the dock (document, widget, form); the code editor; the no-code form; add and remove with undo; canvas and dashboard management; import with report and repair; export; the documentation and worked examples of the front plugin contract |

### Architecture

**A1 — Where the front lives.**
- **Embedded parts.** The core front lives in a top-level `front/` tree, embedded in the binary the way `plugins/` is. It holds the shell document, the core scripts and stylesheet, the twelve shape renderers, the six built-in theme files, the basemap data file and the default dashboard. Built-in widget renderers live in their widget's directory: `plugins/<plugin>/widgets/<type>/<entry>.js`, the file named by `renderer.entry`. **The widget is one directory: manifest, statements, script and renderer together.**
- **Data-directory parts**, discovered **at start**:

| Part | Location | Form |
|---|---|---|
| Theme | `<data-dir>/themes/<id>.json` | one file |
| Language | `<data-dir>/languages/<code>.json` | one file |
| Renderer of a plugin widget | `<data-dir>/plugins/<plugin>/widgets/<type>/` (manifest `renderer.entry`) | a file in the widget's directory |
| Operator widget with a renderer | `<data-dir>/widgets/<type>/` (manifest, query, renderer) | a directory |
| Front-only widget | `<data-dir>/widgets/<type>/` (manifest and renderer, no query, no script) | a directory |

- **Precedence.** As for backend plugins, **the built-in wins every collision**. A data-directory part whose id, language code or type collides with a built-in or with another part is refused with a named code and listed, and the service starts without it.
- **Reload.** Parts are read at start only. A part added to a running service takes effect at the next restart, as a plugin does (`docs/plugins.md`, *Where plugins live*).
- **Adding a part edits no shipped file and needs no Go change.**

**A2 — The front plugin contract.** It is documented in a new `docs/front-plugins.md`, on the same footing as `docs/plugins.md`.
- **Theme file.**
  - Fields: `format_version` (1), `id` (equal to the file's base name, `[a-z0-9-]+`), `name` (an object keyed by language code; `en` required; a palette's proper name is data, as a plugin's `display_name` is), and `modes.light` and `modes.dark`, both required.
  - Each mode maps **exactly** the core's semantic colour-token set: the set the mockup's token blocks use, `--bg-app` … `--shadow-color`. A token is missing or unknown → refused.
  - A value must be a CSS hex, `rgb()` or `rgba()` colour, or `var(--<token of the set>)`.
  - Non-colour tokens (radius, fonts, spacing) belong to the core and cannot be themed.
- **Language file.**
  - The catalogue format: `{"language": "<BCP 47 code>", "strings": {...}}`. The code equals the file's base name, and `strings["language.name"]` (the language's name in itself) is required.
  - It may carry any key of any namespace: core, surface, plugin, widget.
- **Widget renderer.**
  - The manifest's `renderer` becomes `{"entry": "<file>.js"}`, a file in the widget's directory. An absent `renderer` means "drawn by the shape renderer from the shape contract payload" (`docs/widget-authoring.md`, *Shape contracts*). The reserved form `{"reserved": true}` is retired from every built-in manifest.
  - A renderer file is a **classic script** with no ES-module syntax and no `import`. It registers itself through the registration call the core documents, so the same file runs unchanged in the browser and in the Go test harness (A9).
  - A renderer is a **pure function**:
    - **Input:** the widget's envelope (or, for a front-only widget, its answers), the effective parameters (rendering-class ones included), the card's size in CSS pixels, a string lookup, the formatters (number, bytes, rate, instant, duration, by the active language), and the shape kit (layout and drawing helpers).
    - **Output:** a **view tree** of keyed nodes (element name, attributes, text, children), with declarative interactions: `detail` targets naming a subject, and `tooltip` subtrees.
    - **Forbidden:** inline event-handler strings, `innerHTML`, `style` attributes carrying colours, and literal colours. Colours are token references only.
- **Front-only widget.**
  - The manifest is the widget manifest with `renderer` and no `queries` or `script`.
  - Its renderer also exports a request function. It returns the read requests the core performs on its behalf, limited to `GET /api/widget/{type}`, `/api/datasets`, `/api/datasets/{name}/query` and `/api/live/strip`. Any other request is refused, and the card shows the renderer-failure state.
  - `GET /api/widget/{type}` for a front-only type answers `404` `{"condition": "front_only_widget", "type": ...}`.
- **Strings of a widget.**
  - A plugin widget's strings sit in its plugin's `locale/<lang>.json` under `plugin.<plugin>.widget.<type>.{title,question}`, `.parameter.<key>` and `.value.<key>.<value>`.
  - An operator or front-only widget carries `locale/<lang>.json` in its directory under `widget.<type>.*`. A key outside that namespace is refused (`string_outside_namespace`).
- **Refusals** are closed codes, listed by `GET /api/widgets` (A8): `theme_malformed`, `theme_token_missing`, `theme_token_unknown`, `theme_value_invalid`, `theme_id_mismatch`, `language_malformed`, `language_code_mismatch`, `language_name_missing`, `renderer_missing`, `renderer_invalid`, `name_collision`, `string_outside_namespace`, plus the existing widget refusals.

**A3 — Asset serving and the shell.**
- **The shell.** One shell document is served at every interface route: `/`, `/canvas/{dashboard}/{canvas}`, `/settings`, `/collection`, `/interfaces`, `/sources`, `/people`, `/blocklists`, `/setup`, `/sign-in`, `/recover`. It is public: it carries no data. It holds no inline script and no `style` attribute.
- **Assets** are served under `/front/…` from an in-memory index built at start. **Only files the index declares are served**: the core files, each theme, each declared renderer entry, the basemap. A backend script, a statement file, a manifest the front does not need, or any other file of a plugin or data directory is never served, and path traversal is impossible by construction.
- **Headers** on the shell and every asset:
  - `Content-Security-Policy: default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; font-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'`;
  - `X-Content-Type-Options: nosniff`;
  - `Cache-Control: no-cache` with a content `ETag`.
- **Outbound.** The front contacts no origin other than its own server. There is no CDN and no web font: the system font stack is used (`docs/ui-references.md`, Family C).

**A4 — Strings.**
- `GET /api/strings/{language}` is public. It answers the merged catalogue of the core, the surfaces, every loaded plugin, every widget's directory and the language file, as `{language, strings}`, with an `ETag`.
- **Lookup order:** the active language, then English, then the key itself marked as missing. A missing English key in a built-in is a test failure. A data-directory part's missing key is listed in its registry row.
- **No user-visible string is a literal** in any front file: text, `aria-label`, `title`, placeholder, option. Data values (names, addresses, figures) are data.

**A5 — State.**
- **Persisted on the server:** the dashboards (A7) and the appearance. Appearance is `palette`, `mode` (`system` / `light` / `dark`) and `language`, stored as installation settings (`appearance_palette`, `appearance_mode`, `appearance_language`; the `theme` row and `POST /api/settings/theme` are removed).
  - `GET /api/appearance` is public and answers the values, which are set, and the offered `palettes` and `languages`.
  - `POST /api/appearance` is an authenticated JSON write with the CSRF rule. Refusals: `palette_unknown`, `mode_unknown`, `language_unknown`.
  - An absent value means first-launch behaviour: the mode follows `prefers-color-scheme`, the palette is industrial, and the language is the first of the browser's `navigator.languages` that is installed, else English.
- **In the URL:** the route (dashboard and canvas), so a reload returns to the same tab.
- **In browser memory for the session:** the selected widget, the dock's tab and pending form, the undo stack, the open detail, expanded rows, popups and scroll positions.
- **In `localStorage`:** the dock's width and the syntax radio, and nothing else.

**A6 — Reconciliation.** One core reconciler applies view trees to the DOM:
- children are matched by key, attributes and text are updated in place, and a node is created or removed only when its key appears or disappears;
- **it never patches a subtree that holds the focus or a non-collapsed selection**, nor the dock, nor any form holding unapplied values; such a subtree is patched after the focus or selection leaves it;
- a data refresh never replaces a card; open details, expanded rows, popups, tooltips and scroll positions survive it;
- the mockup's `captureFocus`/`restoreFocus` workaround is not needed and is not ported.

**A7 — The canvas model and its persistence.**
- **The model** is `docs/dashboard-format.md` format version 1, amended in place with two optional canvas fields (decision D7; the format has no released reader yet):
  - `period`, one of the period vocabulary, default `24h`;
  - `scope`, an object of up to three reference lists: `interfaces`, `clients`, `owners`.
- **Table `dashboard`** in `schema.sql`, columns: `id`, `position`, `document`, `revision`, `origin` (`created` | `imported` | `default`), `source_name` (the imported file name or null), `created_at`, `updated_at`, with epoch-second instants.
  - The `document` column is of type `json`, not `jsonb`: members keep their order and unknown keys their bytes, so "exported again without repair is unchanged".
  - The purge never touches the table, the read role cannot read it, and `pg_dump` carries it.
- **Endpoints.** All are authenticated; every write is a JSON `POST` with the CSRF rule (`docs/widget-api.md`, *Write endpoints*):

| Route | Body | Answer |
|---|---|---|
| `GET /api/dashboards` | | `{dashboards: [{id, position, title, revision, origin, source_name, canvases: [{id, title}]}], format: {current: 1, minimum: 1}}` |
| `GET /api/dashboards/{id}` | | `{id, revision, origin, source_name, document}` |
| `POST /api/dashboards` | `{document, origin, source_name}` | the dashboard created |
| `POST /api/dashboards/{id}` | `{revision, document}` | the new revision; `409` `revision_conflict` with the current revision |
| `POST /api/dashboards/{id}/delete` | `{}` | the whole deleted dashboard, for undo |
| `POST /api/dashboards/restore` | a deletion's answer, unchanged | the dashboard, recreated |
| `POST /api/dashboards/order` | `{ids}` | the order |

- **Structural validation is fail-closed in Go**, following the format's list. Each refusal is `400` `{condition, canvas_index, widget_index, field, path}`, and nothing is written. The codes:
  - `malformed_body`, `format_version_missing`, `format_version_too_new` and `format_version_too_old` (each with `file_version`, `current`, `minimum`);
  - `canvases_missing`, `canvases_empty`, `canvas_id_missing`, `canvas_id_malformed`, `canvas_title_missing`;
  - `widget_id_missing`, `widget_type_and_shape_missing`, `placement_missing`, `placement_not_integer`, `duplicate_id`, `nesting_too_deep`;
  - `document_too_large` (above 1 MiB);
  - `unknown_dashboard` (`404`).
- **Referential validity is never checked on save.** It is fail-open, as the format requires.

**A8 — Registry and references.**
- `GET /api/front` (public): the themes `{id, name, url}`, the languages `{code, name}` and the core asset URLs.
- `GET /api/widgets` (authenticated):
  - every loaded widget type: `{type, shape, origin, plugin, backend, windowed, providers, primary, parameters, references, renderer_url, title_key, question_key}`, where `parameters` is exactly the manifest's, plus `volume_measure` where the core adds it;
  - the shape renderers;
  - every refused widget and front part, with its code and path.
- `GET /api/references/{kind}` (authenticated): the candidates of one reference kind. Each candidate carries every accepted `by` key of the format's table and a display label, with a `q` filter, a limit of 200 and a remainder count.
  - A client with no portable key (identity `address_in_interface`) is listed as not referenceable, with that reason.
  - `site` answers `withheld_by_aggregate_mode` under `no_domains`.
- `GET /api/references/resolve?references=<JSON list>` resolves each reference by the widget API's own rules (`docs/widget-api.md`, *References*). It answers per reference `resolved`, `no_match`, `ambiguous`, `unknown_kind`, `unknown_by` or `withheld_by_aggregate_mode`.
- Widget parameters resolve through the widget envelope's `unresolved_references`. No second resolver exists.

**A9 — Tests without a browser.**
- Front logic is tested by Go tests running the front's own files in goja: the reconciler (against a minimal test-only DOM shim), the document module (validation, migration, both serialisers, the YAML parser, mapping a path to a line), every renderer on recorded widget answers, and the theme and string loaders.
- **Recorded answers** are generated from the real widget API by an explicitly invoked generator and committed under `testdata/` with a provenance statement. The pre-deployment command re-records them on the 100 000-row seed and fails when a type's member paths differ from the recorded ones.

### Requirements

#### Lot 1 — seams, shell and installation views

- **F7-1 Front plugin discovery.** A1 and A2, with refusals listed and never fatal.
- **F7-2 Shell and assets.** A3.
- **F7-3 Strings.** A4.
- **F7-4 Appearance.**
  - A5. The six built-in theme files: industrial, Tokyo Night, Dracula, Nord, Rosé Pine, Catppuccin.
  - The site tokens take the values of `docs/ui-references.md`, *The values `opnview` uses*. The derived tokens are transcribed from the mockup's blocks.
  - Nord is light-capable, with no fallback.
  - The header carries the palette and mode selectors, as in the mockup. The language selector is shown **only when two or more languages are installed**: every control offered changes what is drawn.
- **F7-5 Dashboard store and API.** A7, with Go conformance fixtures shared with the JS validator (F7-26).
- **F7-6 Registry and references.** A8.
- **F7-7 Session views.**
  - Setup, sign-in, recover and sign-out run on the JSON account API, with the form token from `GET /api/session`.
  - Without a session the shell shows only these views. A `401` from any API returns the reader to sign-in and back to the route they were on afterwards.
- **F7-8 Layout.**
  - The mockup's chrome: a two-row header (identity, live state and outage indicator from `/api/sources` `outage_count` on every view; controls); a rail (Canvases; Dashboard: editor, import, export; Installation: Settings, Collection, Interfaces, Sources, People, Blocklists); the canvas bar.
  - **Header, rail and tabs stay on screen** on a page several screens tall.
  - Below the rail's breakpoint, the rail becomes the mockup's sheet.
  - No view is reachable only through a menu inside a menu.
- **F7-9 Settings view.**
  - The firewall card: URL, key, a write-only secret showing its state, certificate fetch and pinning.
  - The Public Suffix List card, and the appearance.
  - **One generic settings form** rendering every setting declaration the API exposes, from its declaration alone (type, default, bounds, choices, label, secret write-only with its state shown): every loaded plugin's settings, `maxmind` and cadence overrides included, from `GET /api/plugins`, plus `/api/live/settings` and `/api/netflow/settings`.
  - A listing of plugins and front parts, loaded and refused, with reasons.
- **F7-10 Collection view.** It covers every figure and action of the page, including the retention-shortening confirmation (`409` `retention_confirmation_required`), presented as a destructive action that confirms.
- **F7-11 Interfaces view.** It covers every figure and action of the page, plus the interface label (`POST /api/interfaces/{id}/label`). A label is never derived from a description.
- **F7-12 Sources view.** Providers, outages, recent gaps, `since` stated as a bounded interval (never one instant), and the `opnsense_pages` links. The `detail` text is shown verbatim as data.
- **F7-13 People view.**
  - The mockup's People page on `/api/owners*` and `/api/clients*`.
  - Deleting a person confirms or offers undo through `POST /api/owners/restore`, and the view reports `not_restored`.
  - Ownership is never inferred.
- **F7-14 Blocklists view.** Purpose assignment on `/api/blocklists*`. A purpose is never derived from a name.
- **F7-15 Page removal.** It follows the table below, at the end of Lot 1, once F7-7 to F7-14 pass.
- **F7-16 Test harness.** A9 for the parts this lot builds.

#### Lot 2 — canvases and widgets

- **F7-17 Tabs.**
  - One tab per canvas of every dashboard, in dashboard order then canvas order, one click each.
  - When more than one dashboard exists, the tabs are grouped under the dashboard titles in the tab bar itself, not in a menu.
- **F7-18 Grid.**
  - The mockup's column rule: 12, 8 or 1 columns from the canvas container's width, with the mockup's `MIN_CARD_WIDTH` and widest-card floor arithmetic.
  - `y` compaction.
  - The narrow layout and the derived columns are never written back to the document.
- **F7-19 Canvas period and scope.**
  - The canvas bar holds the range (`period`) and a scope control: interface, client, owner, with dropdowns from `/api/references/{kind}`.
  - Both write the canvas fields (A7).
  - A widget inherits them unless it carries its own value, and then its header says "own range" or "own scope".
  - A widget whose manifest accepts no parameter for a scope kind in force says "not scoped" for that kind. Nothing is expanded or inferred: an owner is never turned into client references.
- **F7-20 Card chrome.** As in the mockup:
  - the title (the instance's title, else the catalogue title through strings), with **no explanation line**;
  - header chips: own range, own scope, not scoped, stale (from `freshness`), each named state of `states` in a word or two, unapplied, kept and not understood, cut off at this size;
  - a configure control named by the card's heading.
- **F7-21 Measures on figures.** Every volume figure states its `measure` where measures can differ (`logged`, `netflow`, `sampled`, `interface_counter`), and two measures are never summed (`CLAUDE.md`).
- **F7-22 Degraded states.**
  - Rendered states: the four conditions, `reachable_no_rows`, `unresolved_reference`, `missing_required_parameter`, `withheld_by_aggregate_mode`, `no_owner_exists`, `unknown_widget_type`, `script_failed`, `read_failed`, and a renderer failure.
  - Each is a designed panel keeping the card's footprint and title, named in a word or two. None is a zero, a blank or a red failure. Each links to the Sources view.
  - Degradation is per series and per contributing source, wherever the payload carries it.
- **F7-23 Renderers.**
  - Every served type has a renderer drawing its catalogue shape from its documented payload, following every rule of `docs/widget-catalogue.md` Part 1 (truncation never silent, no fragment, four conditions, repeat, inline series) and its preset's entry.
  - Every type is a port of the mockup's `RENDER[type]` against the real payload.
  - A declarative or operator widget with no renderer is drawn by its shape's renderer.
- **F7-24 Charts.**
  - Linear by default. Where `line_interpolation` offers `smooth`, the curve passes through every measured point and draws no unmeasured one.
  - Every series element carries `data-sample-count`.
  - An area appears only to separate stacked series.
  - A gap is drawn as a labelled gap.
  - An `all_series` tooltip lists the whole stack at the hovered instant.
  - Aggregation to fit the pixels is stated.
  - Maps use the mockup's Natural Earth 1:110m geometry as an embedded data file with its provenance, point marks, no choropleth, no arcs, and the unplaced counter always shown.
- **F7-25 Site names.** The attribution method is shown per record (`client_traffic_detail` `method` and `correlation_delay`; `top_sites` `methods`), and `resolver_window_bounded` is shown as a named state.
- **F7-26 Detail and pivot.**
  - Every name and figure opens a detail anchored to its mark (a bottom sheet on a phone). This covers clients, interfaces, rules, sites, operators, countries, owners, matrix cells, tree nodes, map marks, bars, slices and chips.
  - The detail carries the subject's facts and pivots: related subjects (detail to detail), "scope this canvas to it", and "add a widget about it".
  - **Opening or closing a detail never changes the document.** A pivot that edits is an explicit, undoable edit.
  - A detail stays open across refreshes and is redrawn with fresh data.
- **F7-27 Live refresh.**
  - Windowed widgets refresh every 60 s. A widget carrying a live figure, and the live strip, refresh at the `cadence_seconds` the answer reports.
  - Only the visible canvas refreshes, and refreshing pauses while the document is hidden.
  - Every refresh goes through the reconciler (A6).
- **F7-28 Default dashboard.**
  - On the first signed-in start with no dashboard ever created (a setting marks that it was seeded), `front/dashboards/default.json` is stored with origin `default`.
  - Its content is the mockup's own three canvases, carrying **only portable references**. The changes from the mockup document:
    - `client_traffic_detail` widgets are removed, because they require a client;
    - interface references are dropped;
    - step-6 types are removed;
    - `destinations_per_owner` is dropped;
    - `custom_chart` series are rewritten to the API's vocabulary.
- **F7-29 Test harness.** Drawn-parameter, inert-parameter and tree-reconciliation checks ported from the mockup's harnesses (A9).

#### Lot 3 — editing and portability

- **F7-30 Edit mode.**
  - Entered from the header.
  - Pointer drag to move; right edge, bottom edge and corner to resize.
  - Keyboard buttons named by the card's heading, each writing `placement`.
  - An explicit "add a widget here" button.
- **F7-31 Picker.**
  - Lists every loaded type from `/api/widgets` with its title, its **question** and its shape: a chooser may explain.
  - Types of step 6 that are not served are not offered.
  - A new widget starts at its manifest defaults.
- **F7-32 Undo and confirmation.**
  - Every document edit is undoable through an undo control and `Ctrl`/`Cmd+Z` for the session: move, resize, add, remove, Apply, repair, scope, period, canvas and dashboard management.
  - Removing a widget, a canvas or a dashboard confirms or can be undone; restoring a dashboard uses `/api/dashboards/restore`.
- **F7-33 Canvas and dashboard management.**
  - Create, rename, reorder and remove canvases and dashboards.
  - An empty canvas renders an invitation (the format says so), not an error.
- **F7-34 The dock.**
  - The mockup's single panel: Document, This widget and Form tabs, a YAML/JSON radio (YAML by default), one Apply, one Revert, and a resizable width.
  - The card on the canvas is the preview and is marked "unapplied".
  - Home Assistant's weight: no file tree, no minimap, no command palette, no diff viewer.
- **F7-35 Code editor.**
  - A highlighted pane over a textarea.
  - A syntax or structural error is named with its line and stays marked.
  - Unresolved references, unknown types and kept keys are marked **in place** on their lines, with an inline notice naming `kind`, `by`, `value` and the exporting `label`. Marks follow the text as it is edited.
  - When the edited YAML holds comments, the comment-loss warning of the format document is shown before saving.
- **F7-36 Document module.**
  - One front module holds the model's structural validation, mirroring A7. It passes the same conformance fixtures, run in goja by a Go test.
  - It holds the migration ladder (current 1, minimum 1, one-way, in memory; newer and below-floor refusals naming the versions), the JSON serialiser, and a **first-party restricted YAML parser and serialiser**.
  - The YAML subset accepts every construct of the format's worked example and of the serialiser's own output. It refuses anchors, aliases, tags, multiple documents and non-string keys, naming the line.
  - It maps a document path to its line in either syntax.
  - It coerces scalars to the type the manifest declares and refuses a value that cannot be coerced.
- **F7-37 No-code form.**
  - Generated from the widget's manifest parameters: `enum` → select; `enum_set` → checkboxes; `boolean`; `integer` and `number` with their bounds; `reference` and `reference_list` → dropdowns from `/api/references/{kind}`, with an unresolved value shown as the current selection, marked not found, and never a free-text reference field; `series` → an ordered list editor for `custom_chart` series with the sources, measures and aggregations of `docs/widget-api.md`; `object` → edited in the code tabs only.
  - Labels come from strings. Unknown keys are listed as kept.
- **F7-38 Import.**
  - From a file picker or a paste. JSON or YAML is detected from the content.
  - Structural failure refuses the whole file with the canvas index, widget index and field.
  - Referential issues are collected from the widget envelopes (`unknown_parameters`, `invalid_parameters`, `unresolved_references`, `unknown_widget_type`), from `/api/references/resolve` for the canvas scope, and from bare `shape` widgets (shown as not implemented by this build). The import proceeds: the dashboard is stored as a new dashboard with origin `imported`, and an import report card is shown, grouped by canvas then widget, each row linking to Repair.
  - Nothing is rewritten automatically and nothing blocks.
- **F7-39 Repair** edits the stored document through either surface. The original file is never touched.
- **F7-40 Export.**
  - One dashboard per file, in YAML or JSON chosen at export (preset from the syntax radio), downloaded from memory with no network request.
  - Unknown keys and unresolved references are written back unchanged.
- **F7-41 Documentation.**
  - `docs/front-plugins.md`: the contract, refusals, precedence and reload, with four worked examples, each exercised by a test: a theme, a language, a front-only widget on the data catalogue, and a renderer carried by a plugin widget.
  - `docs/extending.md`: table and *Adding a surface* rewritten for JSON-only surfaces.
  - `docs/widget-authoring.md`: `renderer` and widget strings.
  - `docs/dashboard-format.md`: the canvas `period` and `scope`, the decided *Open choices*, and the corrected worked example.
  - `docs/widget-api.md`: the new endpoints, and the appearance route replacing the theme route.
  - `docs/architecture.md`: the front seam; surfaces without pages.
  - `docs/data-model.md`, *Vocabulary*: a row for each `opnview`-own term this step introduces (**canvas**, **dashboard**, **front plugin**, **renderer**, **theme**, **palette**, **mode**), each with its reason. OPNsense's own dashboard has widgets; "widget" keeps that meaning.
- **F7-42 Final checks.** No user-visible literal (F7-3), and no explanatory copy on the canvas: a title names, a figure states, a unit qualifies, a state is named in a word or two.

### Page-removal list (end of Lot 1)

| Removed | Replaced by |
|---|---|
| `GET`/`POST /setup`, template `account/template/setup.gohtml` | the shell's setup view on `GET /api/session` and `POST /api/account/setup` |
| `GET`/`POST /sign-in`, `signin.gohtml` | the sign-in view on `POST /api/session/sign-in` |
| `GET`/`POST /recover`, `recover.gohtml` | the recover view on `POST /api/account/recover` |
| `POST /sign-out` | the header control on `POST /api/session/sign-out` |
| `GET`/`POST /settings`, `settings/template/settings.gohtml` | the Settings view (F7-9) |
| `GET`/`POST /collection`, `POST /collection/settings`, `collection.gohtml` | the Collection view (F7-10) |
| `GET`/`POST /interfaces`, `interfaces.gohtml` | the Interfaces view (F7-11) |
| `GET /sources`, `sources.gohtml` | the Sources view (F7-12) |
| `GET /asset/style.css`, `internal/web/asset/style.css`, `internal/web/template/layout.gohtml` | the `/front/…` assets |
| `GET /` redirecting to `/settings` | the shell |
| `POST /api/settings/theme`, the `theme` member of `GET /api/settings`, `internal/web/theme.go`, the `theme.*` keys | `GET`/`POST /api/appearance` |

The paths `/setup`, `/sign-in`, `/recover`, `/settings`, `/collection`, `/interfaces` and `/sources` stay valid as shell routes, so bookmarks keep working.

Removed with the pages, because nothing would receive them any more:
- the page machinery: `render.go`'s renderer, `Base`, `NewBase`, `Render`, `RenderRefusal`, `RedirectWith*`, the flash cookie `opnview_notice`, `Page`, `Tab`, `Indicator`, `Notices` and `Refusals` of `web.Surface`;
- the form-POST handlers, while the validation functions the JSON routes share are kept;
- the template-only catalogue keys;
- `html_test.go`, `pages_golden_test.go` with `testdata/golden_pages.json.gz`, `style_test.go`, the bundle-pages tests, and the page tests of each surface, where a JSON test already covers the behaviour;
- the `webtest/examples/hello` template, rewritten as a JSON-route example.

Deletions name exact paths: no glob.

### Files expected

- **New:** `front/` (the shell, core scripts and stylesheet, `shapes/`, `themes/*.json`, the basemap data, `dashboards/default.json`) and its embedding Go file; a Go package for the front index, serving, appearance and strings; a surface each for dashboards and references; widget-registry routes; a `dashboard` table in `schema.sql`; `renderer.js` and `locale/en.json` in every built-in widget plugin; goja test files and `testdata/`; `docs/front-plugins.md`.
- **Amended:** `internal/widget` (the `renderer` field, front-only widgets, widget-directory strings), `internal/web` (core routes, the surface seam), `cmd/opnview` wiring, every built-in `manifest.json`, the documents of F7-41, `ci/pre-deployment.sh` (re-recording the answers).
- **Package limit.** A Go package directory holds at most 15 non-test source files (`docs/architecture.md`, *Map of the tree*).

## Acceptance criteria

These apply to every criterion:
- `docker compose run --rm checks` exits 0 with its `go test` wall time under 60 s (report the CPU count), and **the front's goja tests together take at most 8 s** of it, as `OPNVIEW_TEST_BREAKDOWN=1` reports.
- `docker compose run --rm pre-deployment` exits 0.
- Fixtures are synthesised: documentation-range addresses, RFC 2606 names, interface counts as parameters, and a provenance statement.
- No test is weakened, skipped or moved.
- "(ergonomist)" means checked in a real browser against a review instance on a scratch data directory and a synthesised fixture, never the live service (the deferred-bundle spec's rule).

### Lot 1

- [ ] **F7-AC1** — With an empty data directory, `GET /api/front` lists exactly the six built-in themes and English. Dropping a valid theme file and a valid language file into the data directory and restarting adds them, **with no file of the repository changed and no Go rebuild**. Removing them and restarting removes them. Each refusal code of A2 is produced by its own malformed fixture, the service still starts, and `GET /api/widgets` lists the refusal with its path.
- [ ] **F7-AC2** — A data-directory theme with id `industrial`, a language file `en.json`, and an operator widget whose type is a built-in's are each refused `name_collision`. The built-in is served unchanged.
- [ ] **F7-AC3** — A request for a file under `/front/…` that the index does not declare is answered `404`: a plugin's backend script, a `.sql` file, a `manifest.json`, a path with `..`, an encoded traversal. A test enumerates every built-in plugin's files and asserts that only renderer entries are served.
- [ ] **F7-AC4** — The shell and every asset carry the CSP of A3 verbatim, `nosniff` and an `ETag`. The shell contains no inline `<script>` body and no `style=` attribute. No file under `front/` and no renderer contains `http://` or `https://` in a fetching position (`src`, `href`, `url()`, `fetch`, `XMLHttpRequest`, `WebSocket`, `EventSource`, `import`), and no `@font-face` (grep test).
- [ ] **F7-AC5** — `GET /api/strings/en` holds every key a front file, a built-in renderer or a manifest-driven label uses. A Go test collects the keys used and fails on a missing one. Rendering every built-in view and every renderer with a pseudo-language that wraps each string in markers leaves **no text node, `aria-label`, `title`, `placeholder` or option text** that is neither marker-wrapped nor equal to a formatted data value of the input.
- [ ] **F7-AC6** — With no appearance row: the page's `data-theme` follows `prefers-color-scheme`, the palette is industrial, and the language is the browser's first installed one, else English. `POST /api/appearance` with each refusal answers its code and writes nothing. A saved palette and mode persist across a reload and across a second browser profile. *(Go test, plus ergonomist for the browser half.)*
- [ ] **F7-AC7** — Each of the six theme files' `--bg`, `--panel`, `--fg`, `--fg-strong`, `--fg-muted`, `--accent`, `--line` and `--vif` equivalents equals the `docs/ui-references.md` table, and industrial equals Family C, by a test diffing the files against the table parsed from the document. Every theme maps every token in both modes.
- [ ] **F7-AC8** — No colour literal appears in any front stylesheet or renderer outside the theme files. The computed card radius is `0.25rem`, and the font family is the Family C system stack. *(grep test; ergonomist for computed styles.)*
- [ ] **F7-AC9** — The dashboard API, in Go tests:
  - every refusal of A7 by fixture, each naming its indices and field;
  - a valid document carrying unknown keys stores and reads back **byte for byte, member order included**;
  - a stale `revision` answers `409`;
  - a write without a token, or with a cross-site token, answers `403` `csrf_refused`, and one without a session answers `401`;
  - delete then restore returns an identical document;
  - the read role has no privilege on `dashboard`;
  - `GET` changes nothing.
- [ ] **F7-AC10** — One conformance fixture directory (valid and invalid documents, each with its expected code and path) is run by the Go validator and, in goja, by the front document module. Both agree on every fixture.
- [ ] **F7-AC11** — `GET /api/widgets` answers every type `Service.Types()` serves plus every front-only type, each `parameters` equal to its manifest's, with `volume_measure` exactly on the types declaring `volumes`. `GET /api/widget/<front-only type>` answers `404` `front_only_widget`.
- [ ] **F7-AC12** — For each reference kind, `GET /api/references/{kind}` on a fixture lists every candidate with every accepted `by` key. An `address_in_interface` client is not referenceable, with its reason. `site` under `no_domains` is withheld. `GET /api/references/resolve` gives the same state the widget envelope gives for the same reference, for `resolved`, `no_match`, `ambiguous`, `unknown_kind` and `unknown_by`.
- [ ] **F7-AC13** — Every route in the page-removal table is absent from `Server.Routes()`, except where it is now a shell route answering the shell. Every listed file is gone, a grep finds no reference to the removed types, and the route-access enumeration test still passes over the new route table.
- [ ] **F7-AC14** — Each installation view performs every action its JSON routes offer. Driven through the front in goja against a fake API recording requests:
  - Settings saves the firewall card, fetches the certificate, saves the Public Suffix card, and saves one plugin setting of each declared type including a secret. The secret's value never appears in any answer or DOM; its state does.
  - Collection saves an interval, and a retention shortening confirms before sending `confirm`.
  - Interfaces adds, confirms, removes and restores a network, sets the link-local rule and a label.
  - People creates, renames, assigns, unassigns, and deletes then undoes.
  - Blocklists assigns and clears a purpose.
- [ ] **F7-AC15** — The generic settings form renders a declaration of each setting type (`integer` with bounds, `number`, `boolean`, `text`, `enum`, `secret`) from a synthetic plugin manifest, **with no front code naming that plugin**. A value out of bounds is refused in the form, and the server's refusal code is shown against its field.
- [ ] **F7-AC16** *(ergonomist)* — Without a session, every shell route lands on setup (no account yet) or sign-in. After sign-in, the reader is back on the route they asked for. A `401` mid-session returns them to sign-in.

### Lot 2

- [ ] **F7-AC17** — The goja renderer tests:
  - Every served type has a renderer and a recorded answer, and renders that answer without throwing.
  - **For every rendering-class parameter and each offered value, and absent, the view tree differs** (the drawn-parameter check).
  - Every rendering-class key appears in its renderer's reachable source (the inert-parameter check).
  - A test fails when a served type has no recorded answer.
- [ ] **F7-AC18** — In every rendered chart, each series element is a `polyline`, or a `path` whose `d` holds only `M` and `L`, by default. Every series element carries `data-sample-count` equal to its drawn points. An area appears only under `stacked: true`. A `smooth` rendering passes through every sample.
- [ ] **F7-AC19** — Reconciliation of figures:
  - The ranking bars and their remainder sum to `total`.
  - The map's placed plus unplaced equals its total, and the unplaced counter is present at zero.
  - The connection tree's right-side level-1 total equals the north-south total of the answer, and each expanded node's children sum to the node, over every exposed combination of `root`, `depth`, `measure`, `limit_per_level`, `sort` and `include_blocked` (the tree-reconciliation check, ported).
- [ ] **F7-AC20** — For each condition and state listed in F7-22, a recorded or synthesised answer renders a panel that keeps the title, names the condition through its own string key, links to the Sources view, and contains no `0` figure and no element styled with the error token.
- [ ] **F7-AC21** — Every volume figure node carries its `measure`. A `netflow` answer and a `logged` answer render distinguishable measure chips. No renderer sums two measures (a test feeds a payload with both).
- [ ] **F7-AC22** — `client_traffic_detail` renders `method` and `correlation_delay` per attributed record. `top_sites` renders per-row `methods`. `resolver_window_bounded: true` renders its named state.
- [ ] **F7-AC23** — The reconciler, in goja on the DOM shim:
  - Applying the same tree twice creates and removes zero nodes.
  - A changed figure updates text in place, with node identity preserved.
  - A subtree holding the focus or a selection is left untouched until both leave it.
  - Neither the dock nor a form holding unapplied values is touched by a refresh.
- [ ] **F7-AC24** *(ergonomist, the mockup's three critical failures)* — On a canvas refreshing live:
  - a name typed at ordinary speed across at least three refreshes loses no character;
  - a MAC selected in a table stays selected and copies correctly across a refresh;
  - an unapplied form edit survives refreshes;
  - an open detail, an expanded row and a focused map mark or tree node stay open or focused.
- [ ] **F7-AC25** *(ergonomist, inspecting is not editing)* — Each subject kind of F7-26 opens a detail with at least one pivot to another subject, and the dashboard's `revision` is unchanged after opening and closing details. "Scope this canvas to it" changes the scope and is undone by Undo.
- [ ] **F7-AC26** — The canvas `period` and `scope`:
  - Setting them writes the canvas fields.
  - A widget with its own value shows "own range" or "own scope".
  - A widget accepting no parameter for a scope kind shows "not scoped".
  - The widget requests carry the inherited values exactly where the manifest declares the key, and never otherwise (Go-run test on the request builder).
- [ ] **F7-AC27** — The refresh scheduler requests windowed widgets every 60 s and live-figure widgets at the reported `cadence_seconds`. It sends nothing while the document is hidden, and nothing for canvases not shown (goja test with a fake clock).
- [ ] **F7-AC28** — On first sign-in with an empty `dashboard` table, the default dashboard is stored once with origin `default`. Deleting every dashboard does not re-seed it. Its document passes structural validation and carries no `interface`, `client`, `rule` or `owner` reference. Rendering it against the fixture produces no `unknown_widget_type` and no `invalid_parameters`.
- [ ] **F7-AC29** *(ergonomist, against the mockup, at 1920, 1440, 900 and 390 CSS px)* — The two-row header, the rail (a sheet at narrow widths), the canvas bar with tabs, range and scope, the derived 12/8/1 columns and the card chrome read as the mockup's. Header, rail and tabs stay visible while scrolling three screens down. No canvas, widget, control or editing surface is reachable only through a nested menu. The network panel shows **zero requests to any other origin** over the whole walkthrough.

### Lot 3

- [ ] **F7-AC30** — Moving and resizing by pointer and by keyboard buttons each change that widget's `placement` in the Document tab immediately. The narrow layout and the derived column count never change a stored `placement`, checked by exporting before and after a resize of the browser window. *(goja for the write-through; ergonomist for the pointer.)*
- [ ] **F7-AC31** — The picker lists exactly the types of `/api/widgets`, each with its title, question and shape. The three step-6 types are absent. A newly added widget's `parameters` equal its manifest defaults.
- [ ] **F7-AC32** — Each undoable edit of F7-32, followed by Undo, restores the document byte for byte (goja). Removing a widget, a canvas or a dashboard either confirms first or offers undo.
- [ ] **F7-AC33** — The form for every type offers exactly its manifest's parameter keys (the form-covers-catalogue check, now against the manifests). Every `reference` or `reference_list` control is a dropdown. An unresolved value is shown as current and marked not found. No free-text reference field exists.
- [ ] **F7-AC34** — The YAML module:
  - It parses the worked example of `docs/dashboard-format.md` (as corrected) to a model equal to the JSON example's.
  - Serialising then parsing round-trips every conformance fixture in both syntaxes.
  - Anchors, aliases, tags, multiple documents and non-string keys are each refused, naming the line.
  - `period: 7d`, `columns: 12` and a value that cannot be coerced behave as the format's note requires.
- [ ] **F7-AC35** — Importing the corrected worked example and the mockup's `DOC_IMPORTED`, on a fixture lacking interface `opt7`:
  - the dashboard is stored;
  - the report lists the unresolved interface reference, the unknown type and the kept key, grouped by canvas and widget;
  - the affected cards keep their footprint with the unresolved panel;
  - the code view marks exactly those lines, with notices naming `kind`, `by`, `value` and `label`;
  - **exporting without repair returns the imported document's members and references unchanged**, in the chosen syntax.
- [ ] **F7-AC36** — Repairing through the form, then through the code tab, each rewrites only the chosen reference. The highlight clears after Apply. A structurally broken import (each code of A7) stores nothing and names the indices and field. A `format_version` of 2 is refused, naming 2 and 1.
- [ ] **F7-AC37** *(ergonomist)* — The dock has three tabs, one radio pair, one Apply and one Revert, and no file tree, minimap, command palette or diff viewer. The card shows "unapplied" while the form holds pending values. YAML with a comment triggers the comment-loss warning before saving.
- [ ] **F7-AC38** — The four worked examples of `docs/front-plugins.md` exist as files under test data. A test copies each into a scratch data directory exactly as the document instructs, starts a server, and reaches it: the theme in `/api/front`; the language in `/api/strings/{code}` and in the language picker's options; the front-only widget in `/api/widgets`, with its renderer drawing from a dataset answer; the plugin widget's renderer served and drawing. No file of the repository is changed.
- [ ] **F7-AC39** — The documents of F7-41 carry the content listed there. `docs/dashboard-format.md`'s *Open choices* record the decisions D1 to D6 below. A test parses the corrected worked example and finds every `type`, parameter key and series vocabulary item in `/api/widgets` and `docs/widget-api.md`.
- [ ] **F7-AC40** — Every user-visible string, identifier, comment and document of the cycle is English. `ROADMAP.md` step 7's status is not set to *Done* by this cycle.

### For maintainer review — not acceptance criteria

1. Whether the product reads as the mockup he approved, at his own screen sizes.
2. Whether the density and the card sizes of the default dashboard are right.
3. Whether the dock is still too much editor.
4. Whether the detail and pivot feel like inspecting rather than editing.
5. Whether the derived tokens of the five named palettes (status, series and code colours, which his site does not publish) are right.
6. Whether the Installation entries of the rail are the right set.

## Out of scope

- Step 6:
  - the alert backend and the three alert presets (`security_alerts_over_time`, `alerts_by_signature`, `alerts_by_client_and_interface`);
  - gaps G7, G8 and G12, with no `data-gap` marking;
  - any classification of applications.
- Bare-`shape` widgets: no endpoint serves them, so they render as not implemented by this build and are kept in the file.
- Named responsive layouts stored per breakpoint. The derived 12/8/1 rule stands.
- Live reload of front parts. Parts are read at restart.
- Running renderers in a Web Worker (decision D14).
- Shipping any language other than English.
- Per-account preferences, and more than one account.
- TLS and the `Secure` cookie, README screenshots and example dashboard files: step 8.
- Any change to the firewall, any new outbound call, any schema change beyond the `dashboard` table.

## Risks

- **Size.** Lot 2 ports 26 type renderers and 12 shape renderers onto real payloads. It is the likeliest lot to run long. Within it, protect first the types on the default dashboard.
- **goja limits.** goja runs ES5.1 and much of ES2015+, but not ES modules. The classic-script contract (A2) depends on that. A renderer using a browser-only API would pass in the browser and fail in tests, so the shape kit must provide every measurement a renderer needs (a text-width helper, with an approximation in tests).
- **Recorded answers drift** from the API. This is mitigated by re-recording before deployment (A9), not removed by it.
- **Two structural validators** (Go and JS) can drift. The conformance fixtures make drift a test failure, not an impossibility.
- **Front plugins run in the signed-in reader's page**, with that reader's privileges (decision D14). The CSP stops exfiltration to other origins, but not a malicious renderer calling write routes. A front plugin is trusted as an operator-installed backend plugin is. This is stated in `docs/front-plugins.md`.
- **A first-party YAML subset** may meet a hand-written file using legitimate YAML outside the subset. Such a file is refused with its line, never half-read.
- **`json` rather than `jsonb`** keeps member order but forbids indexing inside the document. Nothing queries inside it.
- **The test budget.** Goja tests over many renderers must fit in 8 s. Each test reuses one runtime, and recorded answers are small.
- **The package limit** of 15 source files per Go directory forces splitting the front's Go side into several packages.
- **Unverified telemetry field names** (`docs/widget-catalogue.md`, Part 5) mean the default dashboard's health canvas may show *absent* readings on a live firewall. That is correct behaviour, and it may read as broken.
- **Removing the pages at the end of Lot 1** leaves no fallback interface if the shell is broken on a live install. Nothing is committed to `main` before live validation (the maintainer's rule).

## Decided questions

Every one of these is **Decided by the orchestrator (17 October 2026)** on the Planner's recommendation, because the maintainer is unavailable. None is the maintainer's decision.

- **D1 — Export syntax.** The user chooses YAML or JSON at export, preset from the editor's syntax radio, YAML by default. *Why:* the step-3 decision 9 (YAML by default, a toggle), and the format document's note that the maintainer wanted YAML available.
- **D2 — Placement.** Absolute `x`, `y`, `w`, `h` as drafted. *Why:* the mockup's edit mode and Homarr's grid both need it.
- **D3 — Parameter validation.** At import, fail-open and reported. *Why:* already the widget API's behaviour, and the format document's forward-compatibility argument.
- **D4 — Recording a repair.** It rewrites the stored document; the user's original file is untouched; there is no local-bindings table. *Why:* the format document refuses a second source of truth. The cost (a re-imported file breaks again) is stated in the document.
- **D5 — File unit.** One dashboard per file, and export is per dashboard. *Why:* "one file, one thing".
- **D6 — Signing.** No checksum and no signature. *Why:* the file is never trusted input, and the format document recommends nothing.
- **D7 — Canvas fields.** Canvas `period` and `scope` are added to format version 1 without a version bump. *Why:* the mockup writes `period`, `ROADMAP.md` puts scope on the canvas, and no released reader exists.
- **D8 — Scope semantics.** A scope applies to a widget only where its manifest declares a parameter of that kind; elsewhere the card says "not scoped"; an owner is never expanded into client references. *Why:* every control must change what is drawn or say why it does not, and ownership is never inferred.
- **D9 — Appearance.** Palette, mode and language are installation settings on the server; first launch follows the operating system and the browser. *Why:* the existing `theme` row is per installation, the override must persist, and there is one account.
- **D10 — Theme tokens.** A theme carries exactly the mockup's semantic colour-token set; non-colour tokens are the core's. *Why:* the angular shape language (`0.25rem`, system fonts) is not a palette property.
- **D11 — Precedence and reload.** The built-in wins every collision, and parts are read at restart. *Why:* identical to `docs/plugins.md`, so one rule covers both.
- **D12 — YAML.** A first-party restricted YAML parser and serialiser in the front; the server persists JSON only; no Go YAML dependency and no vendored library. *Why:* the format refuses anchors, aliases, tags and multiple documents, and the editor needs line positions. A general library resolves aliases silently and adds a dependency to audit.
- **D13 — Validators.** Structural validation is implemented in Go (authoritative) and in JS (the editor), held together by shared conformance fixtures. *Why:* persistence must not depend on front assets, and the fixtures turn drift into a failing test.
- **D14 — Renderer contract.** Renderers are pure functions returning view trees, written as classic scripts, run in the page with no Worker. *Why:* a pure function is what makes reconciliation and goja tests possible. A Worker would add latency to hovers and tooltips for a trust boundary the operator already crosses by installing the plugin.
- **D15 — Charting and maps.** No third-party script; charts and maps are first-party SVG; the basemap is the mockup's Natural Earth geometry as a data file. *Why:* no vendored dependency is needed, and the geometry is public domain with its provenance recorded.
- **D16 — When the pages go.** The pages are removed at the end of Lot 1, and their paths become shell routes. *Why:* the replacing views are built in that lot, and keeping both doubles the maintenance.
- **D17 — Rail entries.** Blocklists is its own Installation entry in the rail. The plugin and front-part listing sits on the Settings view. *Why:* the purpose endpoints exist with no screen, and the rule is one click and no nested menu.
- **D18 — Live and NetFlow settings.** They are shown on Settings through the generic form. *Why:* they are declared settings with no screen, in the declaration shape the generic form already renders.
- **D19 — Language picker.** It is shown only when two or more languages are installed, and only English ships. *Why:* a control offered must change what is drawn.
- **D20 — Default dashboard.** It is seeded once, from the mockup's canvases restricted to portable references. *Why:* an empty first screen cannot be judged against the approved mockup. The seed is portable by construction and is an ordinary dashboard the user may delete.
- **D21 — Refresh cadence.** 60 s for windowed widgets, the reported live cadence for live figures, paused when hidden. *Why:* the response budgets and the live-interest lease (`docs/widget-api.md`, *Live figures*).
  - **Amended (18 October 2026).** Decided by the orchestrator on the maintainer's instruction (18 October 2026): the front is event-driven over SSE; polling is only a disconnected fallback. The page opens one `EventSource` on `GET /api/events` (`docs/widget-api.md`, *Events*) and asks again, at once, the widgets an event concerns -- at most one request per widget every 2 s -- reconnecting by itself with back-off, and the header shows whether the canvas is live or disconnected. While disconnected, windowed widgets are asked again every 15 s (the orchestrator's decision of the same day, which replaced the 60 s above and now applies to the fallback only). Nothing is asked while the page is hidden; on its return, one catch-up. Each card states the time of its last answer ("updated 00:42:10", through the string catalogue). F7-27 and F7-AC27 are read with this amendment. *Why:* the maintainer found the canvas "a frozen view I have to refresh by hand"; the backend refreshes the current slot after every collection pass (10 s by default) while the front asked once a minute and said nothing of its freshness.
- **D22 — Write method.** Dashboard writes are `POST` JSON with CSRF. *Why:* the project's write convention.
- **D23 — Document column.** `json`, not `jsonb`. *Why:* "exported unchanged" requires member order.

## Open questions

1. **The derived colour tokens of the five named palettes** (status, series, code, highlight, shadow). The maintainer's site publishes only eight tokens per mode. *Default:* transcribe the mockup's values, which he approved as part of the step-3 sign-off, and list them under *For maintainer review*.
2. **Whether the default dashboard keeps the Firewall health canvas**, while the telemetry field names are still unverified against a live firewall (`docs/widget-catalogue.md`, Part 5). *Default:* keep it. An absent reading is rendered honestly as *absent*.

