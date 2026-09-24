# Interface and dashboard-format research

Step-3 preparation for `ROADMAP.md`. Fifteen products were studied: eight for
how a modular dashboard actually works, seven for what belongs inside the
widgets, and one palette family for the visual language. A fourth family, the
moodboard, collects interfaces worth stealing from wherever they came from.
Nothing here is a mockup and nothing here chooses the final look; this document
is the written reference the mockups will be judged against.

Companion documents produced in the same cycle: `docs/widget-catalogue.md`
(what the widgets are) and `docs/dashboard-format.md` (how a dashboard is
described as a file).

## Scope and method

**What was studied, and how.** For the factual half of this document — how a
product's dashboard system actually works, and what a palette's official values
are — each product's own documentation and, where it exists, its own source
repository were read while writing. Claims about mechanics (grid units, field
names, error strings, migration ladders) are taken from the page or file that
states them, and the URL is given next to the claim. Several of the most useful
findings below come from reading shipping source rather than prose, because the
prose was silent or wrong; where a vendor's documentation and a vendor's code
disagree, this document says so and cites both.

**This document does two different jobs, and they are held to two different
standards.** Conflating them is the failure this cycle exists to avoid.

1. **Factual claims about how a product works** — the seven Family A
   sub-headings, and the palette values of Family C — are what later cycles will
   build on. They keep the full discipline: a well-formed absolute HTTPS URL from
   the product's own documentation or repository, the `UNVERIFIED:` marker
   wherever that could not be had, and a *References* section matching the body
   in both directions.

2. **Visual inspiration is not held to that standard at all, anywhere in this
   document.** This is not a market study; it is a search for ideas. An image
   may come from anywhere: the vendor's own site by preference, but equally a
   blog post, an old blog post, a screenshot in a forum, a review, a design
   gallery, a project README. **Pure concepts that correspond to no shipping
   product are explicitly wanted** — a dashboard someone designed and never
   built is as useful here as a running product, sometimes more. There is **no
   domain allow-list for images**, no `UNVERIFIED:` marker on them, and no
   requirement that the thing pictured be purchasable. What is recorded next to
   each image is **provenance, not proof**: where it came from and when it was
   captured.

**The `UNVERIFIED:` marker.** Anything factual that could not be confirmed
against an admissible source carries the greppable marker `UNVERIFIED:`
immediately before or inside the claim, with the reason. This is the convention
`docs/opnsense-api-survey.md` uses. An `UNVERIFIED:` claim is a hypothesis for a
later cycle, not a fact this document stands behind. Several are arguments from
silence — a vendor that does not document a behaviour is not a vendor that lacks
it — and they say so. **The marker is never applied to an image.**

**Citation allow-list — and exactly what it governs.** The allow-list applies to
**factual claims about how a product works**: the seven Family A sub-headings,
and the palette values of Family C. It applies to nothing else, and it
explicitly **does not apply to images anywhere in this document**. For the
claims it does govern, an admissible citation has one of these domains:

| Family | Admissible domains for factual claims |
|---|---|
| Grafana | `grafana.com`, `github.com/grafana` |
| Home Assistant | `home-assistant.io`, `developers.home-assistant.io`, `github.com/home-assistant` |
| Netdata | `learn.netdata.cloud`, `netdata.cloud`, `github.com/netdata` |
| Datadog | `docs.datadoghq.com`, `github.com/DataDog` |
| Kibana | `elastic.co`, `github.com/elastic` |
| Homepage | `gethomepage.dev`, `github.com/gethomepage` |
| Homarr | `homarr.dev`, `github.com/homarr-labs` |
| Dashy | `dashy.to`, `github.com/Lissy93/dashy`, `raw.githubusercontent.com/Lissy93/dashy` |
| UniFi Network | `help.ui.com`, `ui.com`, `blog.ui.com` |
| Firewalla | `help.firewalla.com`, `firewalla.com`, `docs.firewalla.net` |
| NextDNS | `nextdns.io`, `help.nextdns.io`, `nextdns.github.io`, `github.com/nextdns` |
| Pi-hole | `docs.pi-hole.net`, `pi-hole.net`, `github.com/pi-hole` |
| ntopng | `ntop.org`, `home.ntop.org`, `github.com/ntop` |
| Zenarmor | `zenarmor.com` |
| Cloudflare Radar | `radar.cloudflare.com`, `developers.cloudflare.com`, `blog.cloudflare.com` |
| Palettes | the palette's own site or GitHub organisation, listed per palette in Family C |

A blog, forum, aggregator or content farm is **not** admissible for a factual
claim. Where one is the only available source it is cited, marked `UNVERIFIED:`,
and the fact that it is a community rather than a documentation source is
stated. Ubiquiti's `community.ui.com`, Firewalla's `help.firewalla.com/community`,
NextDNS's `help.nextdns.io/t/…` idea threads, Pi-hole's `discourse.pi-hole.net`
and the GitHub *Issues* and *Discussions* of the open-source projects are all
treated this way: the domain may be the vendor's, the content is not the
vendor's documentation.

**Research is not an outbound call.** Reading vendor documentation while writing
this document is authoring activity performed by the person and the tooling that
produced it. It has no bearing on the two outbound calls `ROADMAP.md` allows the
*application* — the firewall API and the MaxMind download. Nothing in this
document proposes that `opnview` fetch anything from any of the fifteen products
at runtime, and no font, script, stylesheet or palette named here is to be
loaded from a network at runtime: the palettes are reproduced as values
precisely so that nothing has to be fetched.

**Why the industrial palette is cited by absolute path.** The maintainer's
palette is a `style.css` living at
`C:\Users\fuzzz\Downloads\maestro-espidf-components-main\maestro-espidf-components-main\tb_http_server\www\style.css`,
outside this repository and outside the container bind mount. It has no
published URL, so it cannot be cited like the other sources and it cannot be
re-read by a reviewer who does not have that file. Family C therefore
**reproduces every custom property verbatim**, light and dark, so the document
remains checkable if the file moves or disappears. That reproduction is a record
of an external source, not a stylesheet for `opnview`.

**Screenshots are local-only and are not committed.** They live under
`docs/ui-references/screenshots/`, which this cycle added to `.gitignore`. They
do not survive a clone. Every reference below names where the image came from
and the date it was captured, so a reader can go and look. **Every claim stands
on its prose and its citation**; an image is an illustration and never the
evidence, so a missing image degrades to the sentence beside it — which is why
each image reference is accompanied by prose naming what the image shows and
what is worth taking from it.

**No image shows a real network's addressing, hostnames or device names.** Every
image was opened and looked at before being kept. Candidates exposing private
addressing, reviewer device names or a real site's hostnames were discarded
rather than cropped, with two deliberate exceptions noted where they occur:
images whose author had already redacted those values with black bars, and
images showing only a generic NIC driver name such as a FreeBSD `igc` interface,
which names a chip family rather than anyone's network. Where a public demo
exposed such values the capture was framed to exclude them or was not used, and
the entry says so.

**On live demos.** Public live demos were looked for rather than assumed, and
the search results are recorded honestly in the entries below because they are
themselves a finding: Cloudflare Radar and Dashy have working public demos and
were captured directly; NextDNS has something better than a demo (a functional
dashboard with no account); Grafana Play is live but its panel plugins would not
load in the capture browser; Home Assistant's demo renders but its cards would
not load in the same browser; Homarr's demo requires signing in; and **UniFi's
`demo.ui.com` now redirects to `ui.com`, Pi-hole declines to publish one as a
matter of policy, and no public demo could be confirmed for ntopng, Zenarmor,
Firewalla, Datadog or Homepage.** Where a live capture was impossible, real
product imagery was taken from wherever it could be found, which the image
standard above explicitly permits.

## The product as decided

Five decisions are settled. This research establishes how to do them well; it
does not reopen them, and no section below contradicts one.

1. **Named canvases, not fixed screens.** The user creates as many dashboards
   as they want and fills each with widgets they add, place and size. Tabs
   replace menus. There is no fixed seven-screen structure and no view reachable
   only through a submenu.
2. **Everything is describable as code, in JSON and/or YAML** — both, and
   never XML.
3. **Dashboards are portable.** A dashboard exported from one installation
   imports into another. Where the importing installation has no data for a
   widget, that widget renders as an explained empty state — never an error,
   never a fabricated zero.
4. **Palettes.** The maintainer's industrial palette is the default. Tokyo
   Night, Dracula, Nord, Rosé Pine and Catppuccin are offered as named options
   **at the values published on the maintainer's own site,
   https://lequellec.xyz** — that site, not each palette's upstream project, is
   the authoritative source for what those five names mean in `opnview`. See
   *The five named palettes* below, which records both and says which governs.
5. **Theme on first launch follows the operating system**, user-overridable
   afterwards. This replaces the ROADMAP's "light by default, dark never the
   default"; see *Proposed ROADMAP amendment*.

A sixth point is a maintainer decision taken during this cycle and recorded here
because the whole of *Conclusions for opnview* rests on it: `opnview` does
**not** attempt to invent a universal identifier that resolves magically on
import. It accepts that an imported file carries references the local
installation may not have, and makes each unresolved reference **visible and
repairable** through two coordinated surfaces — a JSON/YAML editor that
highlights in place the exact lines whose reference failed, and a no-code
selector whose dropdowns are populated from what is actually available on this
installation. Both edit the same model. Family A's *UI, file, or both* and
*Portability* sub-headings are the prior art for exactly that pair, and they are
why the decision is defensible rather than merely convenient.

## Family A — how modular dashboards actually work

Eight products, each under the same seven sub-headings. Grafana, Home Assistant
and Dashy get the deep treatment: that is where the useful prior art is, and
each of the three is deliberately several times the length of the other five.

### Grafana

The reference implementation of the whole category, and the one whose mistakes
are best documented because it has had the longest to make them.

#### Grid and reflow

The canvas is a **24-column grid**. Every panel carries a `gridPos` object of
four integers — `w` (width, 1–24), `h` (height in grid units), `x` (column
offset), `y` (row offset) — and each `h` unit is **30 pixels**
(https://grafana.com/docs/grafana/latest/dashboards/build-dashboards/view-dashboard-json-model/).
Those are literal constants in the source: `GRID_CELL_HEIGHT = 30`,
`GRID_CELL_VMARGIN = 8`, `GRID_COLUMN_COUNT = 24`
(https://github.com/grafana/grafana/blob/main/public/app/core/constants.ts). The
layout engine has "negative gravity that moves panels up if there is empty space
above a panel", so deleting or shrinking a panel floats everything below it
upward — **`y` is advisory, not authoritative** (same JSON-model page). Rendering
is `react-grid-layout` with those constants supplied directly
(https://github.com/grafana/scenes/blob/main/packages/scenes/src/components/layout/grid/SceneGridLayoutRenderer.tsx).

Mobile reflow is a **hard pixel breakpoint at 768 px**: below it every panel is
forced to `w: 24`, heights are left alone, `x` is not reset — and crucially the
mobile layout is flagged not to be persisted, so viewing a dashboard on a phone
never rewrites its stored `gridPos`
(https://github.com/grafana/scenes/blob/main/packages/scenes/src/components/layout/grid/SceneGridLayout.tsx).
Dragging is disabled below the same threshold, with the source comment naming
the reason: unintentionally moving panels on a touch device. Rows are a
collapsible grouping construct, and **repeat-by-variable** dynamically adds
panels, rows or tabs from a variable's values, horizontally with a max-per-row
or vertically
(https://grafana.com/docs/grafana/latest/dashboards/build-dashboards/create-dashboard/).
One historical wart worth knowing: before `schemaVersion` 16 dashboards were
row-based rather than grid-based, and `upgradeToGridLayout()` is the migration
that converted them
(https://github.com/grafana/grafana/blob/main/public/app/features/dashboard/state/DashboardMigrator.ts).

![Grafana's 24-column grid on a dense community dashboard](ui-references/screenshots/moodboard-layout-grafana-opnsense-dense-grid.png)
*Source: the screenshot published with the "OPNSense" community dashboard on
`grafana.com/grafana/dashboards` (dashboard 19366); captured 2026-09-22.* Four
narrow panels across, repeated down the page, each about a quarter of the
24-column width. What is worth taking: the grid is legible **as a grid** — the
eye reads four columns of equal width and equal height and compares across them,
which is only possible because the placement is explicit rather than flowed. The
only names visible are generic FreeBSD NIC driver names, which identify a chip
family rather than anyone's network.

#### Adding, configuring and removing a widget

A panel is "the basic building block in Grafana dashboards, composed of a query
and a visualization" (https://grafana.com/docs/grafana/latest/panels-visualizations/).
You add one from **Add new element → Panel** or by dragging it onto the canvas;
resizing is a drag on the lower-right corner, moving a drag on the title
(https://grafana.com/docs/grafana/latest/dashboards/build-dashboards/create-dashboard/).
The **panel editor** has four regions: a header with Back / Discard / Save; a
visualization preview with a table-view toggle, time range and refresh; a Data
section with *Queries*, *Transformations* and *Alert rules* tabs; and the
display-options pane. On open, Grafana *suggests* visualizations matching the
shape of the data
(https://grafana.com/docs/grafana/latest/panels-visualizations/panel-editor-overview/).

The **panel inspector** has five tabs — Data (post-transformation results, with
CSV download), Stats (query time, query count, rows returned), JSON (a selector
between panel JSON, panel data JSON and data-frame structure), Query (the actual
requests sent, copyable) and Error (only when a query fails)
(https://grafana.com/docs/grafana/latest/panels-visualizations/panel-inspector/).
**Explore** is the ad-hoc counterpart: query-first, no panel model, nothing
persisted as a dashboard object
(https://grafana.com/docs/grafana/latest/explore/). `UNVERIFIED:` the
round-trip buttons between Explore and a panel are widely used but are not
described on the Explore overview page, so they are not asserted here.

**Library panels** are reusable panels stored in folders: creating one converts
the source panel into a library panel, a change propagates to every instance,
and an instance can be unlinked to edit it locally
(https://grafana.com/docs/grafana/latest/dashboards/build-dashboards/manage-library-panels/).
In the JSON the reference is a `libraryPanel` field on the panel
(https://github.com/grafana/grafana/blob/main/public/app/features/library-panels/types.ts).

![The Grafana panel editor](ui-references/screenshots/familya-grafana-panel-editor.png)
*Source and provenance recorded in the image list at the end of Family D;
captured 2026-09-22.* The visualization on top, the query and options panes
beneath and beside it, the whole thing a modal over the dashboard. What is
worth taking: the preview is **the real widget, at something close to its real
size**, not a thumbnail — the user is editing the thing they are looking at.

#### Widget parameters

A panel's configuration is split along a deliberate seam. **`options`** holds
visualization-plugin-specific settings. **`fieldConfig`** holds per-field
presentation, itself split into `defaults` (every field) and `overrides` (an
array of rules). Each override is a *matcher* plus properties, and there are
five matcher kinds: fields with name, fields with name matching regex, fields
with type, fields returned by query, and fields with values
(https://grafana.com/docs/grafana/latest/panels-visualizations/configure-overrides/).

**`targets`** is the array of data-source queries. **`datasource`** is a
structured `{type, uid}` reference — and it was not always: `schemaVersion` 33 is
precisely the migration converting string datasource identifiers into structured
references, and 36 normalised them across annotations and variables
(https://github.com/grafana/grafana/blob/main/public/app/features/dashboard/state/DashboardMigrator.ts).
**`transformations`** is an ordered array of `{id, options}` running after the
query and before the visualization, where "each transformation creates a result
set that then passes on to the next", so order is semantically significant
(https://grafana.com/docs/grafana/latest/panels-visualizations/query-transform-data/transform-data/).
**Thresholds** are absolute or percentage, auto-sorted highest-first, honoured
by thirteen visualization types with a five-way rendering choice
(https://grafana.com/docs/grafana/latest/panels-visualizations/configure-thresholds/).
**Templating** supplies query, custom, text-box, constant, data-source, interval
and switch variables, with multi-value, include-all and per-data-source
interpolation formats
(https://grafana.com/docs/grafana/latest/dashboards/variables/add-template-variables/).
`__inputs` is not a runtime parameter at all — it is an export-time artefact,
covered under *Portability*.

#### UI, file, or both

**Both, and the way they are reconciled is the single most instructive thing in
Family A.** Grafana can be fed dashboards from disk by a YAML provider in
`provisioning/dashboards`, with `options.path` naming a directory of JSON files,
`foldersFromFilesStructure`, `updateIntervalSeconds` (above 10 s it polls, at or
below it watches the filesystem) and `disableDeletion`
(https://grafana.com/docs/grafana/latest/administration/provisioning/).

By default a file-provisioned dashboard is **effectively read-only in the UI**:
saving raises a *"Cannot save provisioned dashboard"* dialog. Setting
**`allowUiUpdates: true`** flips this — users can edit in the UI and Grafana
persists the changes to its database. But the two copies are **not merged, and
the file always wins**: "If you save a provisioned dashboard in the UI and then
later update the provisioning source, Grafana always overwrites the database
dashboard" with the file version, **ignoring version numbers** (same page). So
`allowUiUpdates: false` gives a viewer over a single source of truth, and
`allowUiUpdates: true` gives a last-writer-with-a-file-wins model in which UI
edits are silently discarded the moment the file changes. **There is no
automatic write-back from UI to disk.** That is the trap `opnview` must not
reproduce, and it is the reason *Conclusions* chooses one model with two editors
rather than two artefacts kept in step.

In-UI, the JSON model is reachable from **Dashboard options → Edit as code**,
formerly the *JSON Model* settings tab
(https://grafana.com/docs/grafana/latest/dashboards/build-dashboards/modify-dashboard-settings/).
Beyond file provisioning, the **Observability as Code** family covers Git Sync
(dashboards in a GitHub repo with branching and pull requests — the only listed
mechanism that genuinely round-trips UI edits back into files), the Grafana CLI,
the Foundation SDK, Terraform, the Grafana Operator and Crossplane
(https://grafana.com/docs/grafana/latest/observability-as-code/). Grizzly, the
older as-code tool, "has been marked for deprecation and will be superseded by
grafanactl" (https://github.com/grafana/grizzly).

![Grafana's "Edit as code" JSON model](ui-references/screenshots/familya-grafana-edit-as-code-json.jpg)
*Provenance in the image list; captured 2026-09-22.* The dashboard's own JSON in
a code editor inside the dashboard settings. What is worth taking: this is the
surface `opnview`'s code editor is modelled on, and the lesson is that it is a
**peer of the UI, in the same window**, not an export dialog — which is what
makes "one model, two renderers" believable to a user.

#### Portability

Export lives under **Export → Export as code**, with a model choice (Classic or
V2 Resource), a format choice for V2 (JSON or YAML), and a toggle — **"Share
dashboard with another instance"** — that strips instance-specific details
(https://grafana.com/docs/grafana/latest/dashboards/share-dashboards-panels/).

What that toggle does is visible in the exporter. The datasource input name is
built as `'DS_' + ds.name.replace(' ', '_').toUpperCase()`, each entry pushed
into `__inputs` as `{name, label, description, type: 'datasource', pluginId,
pluginName}`, and every panel, annotation and variable datasource reference is
rewritten to `{type: ds.meta.id, uid: '${DS_PROMETHEUS}'}`. `__requires` is
assembled from the Grafana version plus one entry per panel plugin and per
datasource plugin. **Library panels are lifted into a top-level `__elements`
record** keyed by UID. **Constant variables become `VAR_` inputs**
(https://github.com/grafana/grafana/blob/main/public/app/features/dashboard-scene/scene/export/exporters.ts;
the constant round trip is confirmed server-side in
https://github.com/grafana/grafana/blob/main/apps/dashboard/pkg/migration/conversion/export_inputs.go).

What breaks on import elsewhere, concretely: an un-exported dashboard hard-codes
datasource `uid`s that do not exist on the target, which is why the `${DS_*}`
indirection exists at all; a missing datasource plugin makes the import form
show **"No data sources of type ${pluginId} found"**
(https://github.com/grafana/grafana/blob/main/public/app/features/manage-dashboards/import/utils/inputs.ts);
a missing panel plugin renders an error tile rather than breaking the dashboard;
library panels are portable only through `__elements`; and folders and
permissions are chosen at import time rather than carried in the JSON.

#### Format versioning

`schemaVersion` is "the version of the JSON schema (integer), incremented each
time a Grafana update brings changes to said schema"
(https://grafana.com/docs/grafana/latest/dashboards/build-dashboards/view-dashboard-json-model/).
The ladder is `DashboardMigrator.updateSchema()`, a sequence of
`if (oldVersion < N …)` blocks; panel-level upgrades are accumulated and applied
at the end to all panels including those inside collapsed rows. Named rungs: 12
consolidated `hideVariable`/`hideLabel` into a numeric `hide`; **16** is
`upgradeToGridLayout()`; 27 removed legacy repeated-panel duplicates; **33**
converted string datasource names to `{uid, type}`; 36 normalised datasource
refs and defaulted nulls; 38 migrated table display mode to cell options; 40
forced `refresh` to a string; 42 migrated hide-from functionality
(https://github.com/grafana/grafana/blob/main/public/app/features/dashboard/state/DashboardMigrator.ts).

The current constant is **`DASHBOARD_SCHEMA_VERSION = 42`**, and the file says in
as many words that "Schema version 42 is the FINAL version for the v1 dashboard
API. DO NOT increment this number or add new schema migrations" — future
panel-shaped changes go into per-plugin migration handlers instead (same file).
A backend migration package now exists with "Latest version: v42", "Minimum
supported version: v13", sequential application, built-in plugin migrations
because the backend cannot load plugins, and failures classified as
schema-migration / conversion / minimum-version / **data-loss** errors
(https://github.com/grafana/grafana/blob/main/apps/dashboard/pkg/migration/README.md).
**Migration within v1 is one-way.**

The **v2 schema** ships as a Kubernetes-style resource under
`dashboard.grafana.app`, served at several API versions, modelling all dashboard
elements as Kubernetes kinds
(https://grafana.com/docs/grafana/latest/as-code/observability-as-code/schema-v2/,
https://grafana.com/docs/grafana/latest/developer-resources/api-reference/http-api/dashboard/).
Its design point is decoupling layout from panel configuration so git diffs are
readable (https://grafana.com/blog/observability-as-code-grafana-12/). It
arrived with Grafana v12 as experimental
(https://grafana.com/docs/grafana/latest/whatsnew/whats-new-in-v12-0/), and
migrating to it is a **trapdoor**: "once an existing dashboard is migrated to a
dynamic dashboard and using schema v2, it can't be migrated back"
(https://grafana.com/whats-new/2025-05-05-dashboard-v2-schema-and-dynamic-dashboards/).
Dynamic dashboards reached general availability in 2026
(https://grafana.com/whats-new/2026-04-08-dynamic-dashboards-is-now-generally-available/).

#### Import validation

Two endpoints. **`POST /api/dashboards/db`** takes `{dashboard, folderUid,
overwrite, message}` and returns 400 for "invalid json, missing or invalid
fields", and **412 Precondition Failed** with a `status` discriminator —
`version-mismatch`, `name-exists` or `plugin-dashboard`
(https://grafana.com/docs/grafana/v11.6/developers/http_api/dashboard/,
https://grafana.com/docs/grafana/v10.4/developers/http_api/dashboard/). So
optimistic concurrency is real: the server compares `dashboard.version` and
**rejects rather than merging**, with `overwrite: true` the explicit opt-out.

**`POST /api/dashboards/import`** requires either a plugin id or a dashboard,
returning 422 otherwise, 400 for a malformed body and 403 for exceeded quota
(https://github.com/grafana/grafana/blob/main/pkg/services/dashboardimport/api/api.go).
Template substitution matches inputs by type *and* name and treats an unsatisfied
`__inputs` entry as a **hard failure**: `"dashboard import failed: missing
dashboard input variable [inputName]"`
(https://github.com/grafana/grafana/blob/main/pkg/services/dashboardimport/utils/dash_template_evaluator.go,
https://github.com/grafana/grafana/blob/main/pkg/services/dashboardimport/service/service.go).
The import UI offers three entry paths — file upload, a grafana.com URL or id,
and pasted JSON — and lets the user change name, folder, UID and datasource
mapping before committing
(https://grafana.com/docs/grafana/latest/dashboards/build-dashboards/import-dashboards/).

Summarised: JSON parse errors and missing `__inputs` are **rejected**; uid, name
and version conflicts are **rejected with 412** unless overwritten; an old
`schemaVersion` is **silently repaired** by the ladder; and an **unknown panel
plugin is neither repaired nor fatal** — the panel renders `"Panel plugin not
found: {{id}}"` in a centred alert tile while every other panel keeps rendering
(https://github.com/grafana/grafana/blob/main/public/app/features/panel/components/PanelPluginError.tsx).
That last behaviour is the model for `opnview`'s unresolved-reference rendering.

![Grafana's import screen with datasource inputs](ui-references/screenshots/familya-grafana-import-dashboard.jpg)
*Provenance in the image list; captured 2026-09-22.* The import form, with the
`__inputs` entries rendered as pickers the user must satisfy. What is worth
taking: the import is a **conversation**, not a button — but note that Grafana
makes it a *blocking* conversation, and *Conclusions* deliberately does not.

### Home Assistant (Lovelace)

The counter-model to Grafana: no schema version at all, validation that is
almost nothing, and a per-widget failure mode that is the best in the family.

#### Grid and reflow

A Lovelace dashboard is a list of **views**, and the view type controls layout.
There are exactly four — `sections`, `masonry`, `panel` and `sidebar` — and
**`sections` is now the default**, not masonry as is widely repeated, though the
TypeScript config still documents `masonry` as the back-compat default for old
configs
(https://github.com/home-assistant/home-assistant.io/blob/current/source/dashboards/views.markdown).

**Masonry** "sorts cards in columns based on their card size and places the next
card below the smallest column", using each card's `getCardSize()` (one unit is
about 50 px) as the packing weight; its column count is purely viewport-driven
from `window.matchMedia` breakpoints at 300, 600, 900 and 1200 px, so on a phone
it collapses to one column **and the packing order changes**
(https://github.com/home-assistant/frontend/blob/dev/src/panels/lovelace/views/hui-masonry-view.ts,
https://github.com/home-assistant/home-assistant.io/blob/current/source/_dashboards/masonry.markdown).

**Sections** is the one with a real grid: each section is a CSS grid with
`--base-column-count: 12`, a 56 px row height and an 8 px column gap, and a card
occupies `grid-column: span min(var(--column-size,1),
var(--grid-column-count))` — so twelve columns per section, and **a card asking
for more columns than exist is clamped rather than overflowing**
(https://github.com/home-assistant/frontend/blob/dev/src/panels/lovelace/sections/hui-grid-section.ts).
Card size is per-card `grid_options: {columns, rows, max_columns, min_columns,
min_rows, max_rows}`, where `columns` may be the literal `"full"` and `rows` may
be `"auto"`; this replaced the deprecated `layout_options`
(https://github.com/home-assistant/frontend/blob/dev/src/data/lovelace/config/card.ts).
**`column_span` is a property of a section, not of a card**
(https://github.com/home-assistant/frontend/blob/dev/src/data/lovelace/config/section.ts).
The view-level column count is `max_columns` (default 4), clamped against a
resize controller computing `floor((width - padding + gap) / (minColumnWidth +
gap))` — which is exactly how a sections dashboard reflows to one column on
mobile **without any media query**
(https://github.com/home-assistant/frontend/blob/dev/src/panels/lovelace/views/hui-sections-view.ts).
`panel` renders one card full width and supports no badges; `sidebar` is two
columns with per-card `view_layout: {position: main|sidebar}` and, on mobile,
one column in YAML order
(https://github.com/home-assistant/home-assistant.io/blob/current/source/_dashboards/panel.markdown,
https://github.com/home-assistant/home-assistant.io/blob/current/source/_dashboards/sidebar.markdown).

![A Home Assistant sections-view dashboard](ui-references/screenshots/familya-homeassistant-sections-view.png)
*Provenance in the image list; captured 2026-09-22.* Cards grouped into titled
sections, each section a twelve-column grid, the sections themselves flowing
into as many view columns as the window allows. What is worth taking: **two
levels of grid** — sections that flow, cards that are placed — is a much kinder
model than one flat grid, because the thing the user drags between is a section
rather than a pixel coordinate.

#### Adding, configuring and removing a widget

Editing is modal: the pencil icon top-right enters edit mode, **Done** leaves it,
and undo/redo sit at the top while editing
(https://github.com/home-assistant/home-assistant.io/blob/current/source/dashboards/dashboards.markdown).
Where "Add card" lives depends on the layout — a `+` inside a section, or an
**Add card** button bottom-right in the other views
(https://github.com/home-assistant/home-assistant.io/blob/current/source/dashboards/cards.markdown).
The picker offers two modes: **By entity** (select entities, then choose among
previewed cards, with unassigned entities grouped) and **By card** (browse the
catalogue, with suggestions).

The per-card dialog is **tabbed**: a visual editor, a **Visibility** tab for
conditions, and a **Layout** tab for `grid_options` with a grid-size picker, a
"Full width card" toggle mapping to `columns: "full"`, a precise mode and a
restore-default icon (same page). Every card dialog has a **Show code editor**
toggle that swaps the form for a YAML pane scoped to that one card. Sections are
first-class: **Create section** adds one, the trash icon deletes one, and both
sections and cards are drag-rearrangeable — explicitly "not yet possible in
other views"
(https://github.com/home-assistant/home-assistant.io/blob/current/source/_dashboards/sections.markdown).
Badges are separate small elements, unsupported in sidebar and panel views, and
stored as `badges: (string | Partial<LovelaceBadgeConfig>)[]` — a bare entity id
string is accepted
(https://github.com/home-assistant/frontend/blob/dev/src/data/lovelace/config/view.ts).

![The Home Assistant card picker](ui-references/screenshots/familya-homeassistant-card-picker-by-card.jpg)
*Provenance in the image list; captured 2026-09-22.* Every card type shown as a
**live preview of itself with the user's own data**, not as a name in a list.
What is worth taking: this is the single best idea in Family A for a widget
catalogue UI, and it is directly applicable — `opnview` knows what data the
installation has, so the picker can render each widget populated before it is
placed.

#### Widget parameters

A card is a plain dict whose only structurally required key is `type`; the
interface is `{type: string; [key: string]: any; grid_options?; visibility?;
disabled?; view_layout?; index?; view_index?}` — and that index signature is why
Lovelace tolerates any extra key you invent
(https://github.com/home-assistant/frontend/blob/dev/src/data/lovelace/config/card.ts).
Beyond `type`, the common vocabulary is `entity` or `entities`, `title` and
`theme`. `visibility` is a list of conditions sharing the conditional card's
vocabulary — `state`, `numeric_state`, `screen`, `user`, `time`, `location`,
`view_columns`, plus logical `and`/`or`/`not`; all must pass, and an empty list
means always visible
(https://github.com/home-assistant/frontend/blob/dev/src/panels/lovelace/common/validate-condition.ts).
Interaction is `tap_action` / `hold_action` / `double_tap_action`, each a map
whose `action` is one of `more-info`, `toggle`, `perform-action`, `navigate`,
`url`, `assist`, `none`
(https://github.com/home-assistant/home-assistant.io/blob/current/source/dashboards/actions.markdown).

On templating the honest answer is that **core Lovelace card config is not
Jinja-templated**. The narrow exceptions are the Markdown card's `content`,
documented as "May contain templates", with `entity_id` to pin which entities
retrigger the render and `card_size` to help the masonry packer estimate a
templated card's height
(https://github.com/home-assistant/home-assistant.io/blob/current/source/_dashboards/markdown.markdown);
and the sections view header title, which supports Markdown and templating
precisely because that title *is* a Markdown card underneath (sections page,
cited above). Everything else is literal, which is the single biggest reason
third-party `card-mod` and `config-template-card` exist.

#### UI, file, or both

Two storage modes, **per dashboard**. In **storage mode** (the default) the
config lives in HA's `.storage` directory: `CONFIG_STORAGE_KEY_DEFAULT` is the
domain name and `CONFIG_STORAGE_KEY` is `"lovelace.{}"`, with the dashboard
registry in `.storage/lovelace_dashboards`
(https://github.com/home-assistant/core/blob/dev/homeassistant/components/lovelace/dashboard.py).
In **YAML mode** each dashboard is declared under `lovelace: dashboards:` with
`mode: yaml`, `filename:` and `title:`, the URL key required to contain a hyphen
(https://github.com/home-assistant/core/blob/dev/homeassistant/components/lovelace/__init__.py).
A currency note: the old top-level `lovelace: mode: yaml` switch that turned the
main Overview into `ui-lovelace.yaml` is now explicitly deprecated, carrying a
removal comment in the source and raising a repair issue when used (same file,
and https://github.com/home-assistant/core/blob/dev/homeassistant/components/lovelace/const.py).

A YAML-mode dashboard is **genuinely read-only from the UI, by construction**:
the abstract `LovelaceConfig.async_save` raises `HomeAssistantError("Not
supported")` and only the storage implementation overrides it, so the websocket
save command fails structurally rather than behind a UI guard; the frontend
mirrors this with "The edit UI is not available when in YAML mode"
(dashboard.py, and
https://github.com/home-assistant/frontend/blob/dev/src/translations/en.json).
A third, transient state is `MODE_AUTO`: a fresh dashboard is a *strategy*
dashboard maintained by HA, and **Take control** freezes the generated output
into an editable storage config — one-way, "Once you've taken control, you can't
get this specific dashboard back to update automatically" (const.py, and the
dashboards page).

![A Home Assistant card's YAML pane](ui-references/screenshots/familya-homeassistant-card-yaml-editor.jpg)
*Captured live from `https://demo.home-assistant.io/`, 2026-09-22. The demo
renders in the browser's own locale, so some chrome is not English; the
structure is identical.* One card's configuration dialog switched to its code
editor: a few lines of YAML on the left, **the card itself rendering live on the
right**, and a link back to the visual editor. **This is the maintainer's stated
calibration point for how much editor `opnview` needs** — syntax highlighting, a
live preview, a way back, and nothing else. No file tree, no minimap, no command
palette.

![Home Assistant's whole-dashboard raw configuration editor](ui-references/screenshots/familya-homeassistant-raw-config-editor.jpg)
*Same source and date.* The entire dashboard as YAML — title, views, badges,
sections, cards — with line numbers, fold arrows and a Save button. What is
worth taking: **the same idea at document scope**, and the way the config tree
maps one-to-one onto the rendered layout, so a reader can find the widget they
are looking at by scrolling the file.

![Home Assistant's edit mode as an overlay](ui-references/screenshots/familya-homeassistant-sections-edit-mode.jpg)
*Same source and date.* Edit mode drawn on top of the real dashboard: dashed
drop outlines, per-section drag handles and menus, a "+" placeholder inside
every section, "Add badge" affordances, and undo, redo and Done in the top bar.
What is worth taking: **edit mode as an overlay rather than a separate screen**,
so the user edits the thing they were looking at, and **every container grows an
explicit "add here" target** rather than relying on a toolbar button whose
effect lands somewhere unpredictable. The same affordances in English, on a
nearly empty view, are in
`ui-references/screenshots/familya-homeassistant-edit-mode-add-card.png` (Home
Assistant's own documentation, captured 2026-09-22), which is the better
reference for what an empty section should look like while being filled.

**The crucial design point**: the visual editor and the raw YAML editor are
**two renderers over one in-memory object, not two formats**. The three-dot
*Raw configuration editor* edits the same `LovelaceRawConfig` the card dialogs
mutate; both funnel through the same save path
(https://github.com/home-assistant/frontend/blob/dev/src/panels/lovelace/hui-editor.ts,
https://github.com/home-assistant/frontend/blob/dev/src/data/lovelace/config/types.ts).
The cost of that single model is that YAML is a *presentation* of it: the raw
editor warns "Your configuration might contain comments, these will not be
saved" before writing. Concurrency is handled by a toast, not a lock —
"Dashboard updated in another session. Refreshing will discard your unsaved
changes." (en.json).

#### Portability

Moving a Lovelace config between installations is a copy-paste into the raw
editor, and what survives depends entirely on what the config references by
name. **Entity ids** are the most portable class because they are
human-readable and often identical across installs; where one is missing, the
card still renders but the value slot shows `Entity not available: {entity}`,
with siblings for a missing attribute and an unavailable entity (en.json).

**This is the part that matters most for `opnview`.** `device_id`, `area_id`,
`floor_id`, `label_id` and user ids are **opaque registry UUIDs**, generated
locally at registration time and meaningless on any other installation. They
appear in service targets (actions page), in the area card's `area?: string`
(https://github.com/home-assistant/frontend/blob/dev/src/panels/lovelace/cards/types.ts),
and in per-view `visible: [{user: …}]`, which the docs describe as "a unique hex
value found on the Users configuration page" (views page). **A config carrying
these does not error on import; it silently mis-targets or silently hides
content**, which is far worse than a visible failure, and there is no remapping
step anywhere in the pipeline.

**Custom cards break loudly instead**: a `type: custom:foo-card` resolves
through `customElements`, and when the JS resource is absent the frontend
substitutes an error card reading **"Custom element doesn't exist: {tag}."**
(https://github.com/home-assistant/frontend/blob/dev/src/panels/lovelace/create-element/create-element-base.ts).
Themes are referenced by bare name and degrade silently to the default; media
and image paths such as `/local/background.png` point at per-install filesystem
state (views page).

#### Format versioning

**There is no dashboard schema version.** The canonical interface is
`LovelaceConfig { background?; views: LovelaceViewRawConfig[] }` — no `version`,
no `minimum_version`, no `schema` key
(https://github.com/home-assistant/frontend/blob/dev/src/data/lovelace/config/types.ts).
The only `version` in play is the storage-helper version: `dashboard.py` sets
`CONFIG_STORAGE_VERSION = 1` and `DASHBOARDS_STORAGE_VERSION = 1`, which write
HA's standard `.storage` envelope, so the `version` key in `.storage/lovelace`
describes the **wrapper** and the dashboard config sits opaquely inside. Both
constants have been `1` since the storage backend was introduced.

Evolution of the card format is handled instead by **tolerant readers and
deprecation in place**: `layout_options` is marked deprecated in favour of
`grid_options` but still read, `section.title` is deprecated in favour of a
heading card but still read, and `getLayoutOptions()` still coexists with
`getGridOptions()` (card.ts, section.ts, and
https://github.com/home-assistant/frontend/blob/dev/src/panels/lovelace/types.ts).
The one genuine migration mechanism is narrow and ad hoc: a "Configuration
incompatible" dialog with a **Migrate configuration** button that adds missing
ids to cards and views (en.json). **After years, HA has not had to reverse that
bet** — which is the strongest available argument that `opnview`'s format
version should be turned rarely.

#### Import validation

Validation at the raw-editor boundary is astonishingly shallow, and
deliberately so. `hui-editor.ts` declares exactly two superstruct shapes —
`type({ title: optional(string()), views: array(object()) })` and a strategy
equivalent — and asserts the config against one of them on save, showing "Your
configuration is not valid: {error}" on failure. **A top-level `views` that is a
list of objects is the entire contract**: card types, entity ids and
`grid_options` values are not checked at save time at all. YAML *syntax* is
caught earlier and blocks the save; comments trigger a separate confirmation; a
legacy `resources:` key produces an advisory alert (hui-editor.ts).

At *render* time the isolation is per-card and total. Element construction is
wrapped in try/catch and, on any failure — unknown type, `setConfig` throwing on
bad options, or a missing custom element — an `hui-error-card` is substituted
**in that card's slot only**, with badge equivalents for badges
(create-element-base.ts). **So one bad card never breaks the dashboard.** This
combination — accept almost anything on write, fail gracefully per widget on
read — is the single most transferable architectural lesson in Family A, and it
is the opposite of what a schema-first design produces.

### Netdata

#### Grid and reflow

Netdata has **two unrelated dashboard models**, and conflating them produces
nonsense. The **Agent dashboard** is *generated* from collected metrics and has
no user-facing grid at all; its structure comes from the menu tree described in
`dashboard_info.js`
(https://github.com/netdata/netdata/blob/v1.44.3/web/gui/README.md). **Netdata
Cloud custom dashboards** are the composed product: a free-form grid arranged by
hand, "Use drag & drop to move elements and resize them as needed", with a drag
handle at an element's top right and a resize corner at its bottom right
(https://learn.netdata.cloud/docs/dashboards-and-charts/tabs/dashboards). The
engine is `react-grid-layout`, declared in the manifest of the now-archived
shared component used in both Dashboard and Cloud
(https://github.com/netdata/custom-dashboard/blob/master/package.json). Netdata
publishes **no column count, row height or breakpoint rules**; the only
responsive statement in the docs is that dashboards are "mobile- and
touch-friendly" (https://learn.netdata.cloud/docs/dashboards-and-charts).

#### Adding, configuring and removing a widget

Entirely UI-driven and Room-scoped: from the Dashboards tab, `+`, name it,
`+ Add`, then **Add Chart** or **Add Text**, and **Save** after any change; each
element has a three-dot menu with Go-to-Chart, Rename and Remove
(https://learn.netdata.cloud/docs/dashboards-and-charts/tabs/dashboards). The
newer builder widens the catalogue to metric charts, live function views, logs
and events views, infrastructure summary cards and free-form text, with starter
templates (https://www.netdata.cloud/blog/new-custom-dashboards/). The whole
flow requires a Cloud login.

#### Widget parameters

For Cloud widgets the documented configuration surface is the **NIDL framework**
(Nodes / Instances / Dimensions / Labels) plus a name, and widgets are
node-scoped
(https://learn.netdata.cloud/docs/dashboards-and-charts/tabs/dashboards,
https://www.netdata.cloud/blog/new-custom-dashboards/). The only place Netdata
documents per-widget fields exhaustively is the legacy HTML dashboard, where
each chart is a `<div data-netdata="unique.id">` carrying `data-after`,
`data-before`, `data-width`, `data-height`, `data-host`, `data-chart-library`,
`data-points`, `data-method`, `data-dimensions`, `data-title`, `data-colors` and
more (https://github.com/netdata/netdata/blob/v1.44.3/web/gui/custom/README.md).
**No equivalent field reference is published for Cloud widget JSON.**

#### UI, file, or both

Both exist, share no storage, and are **never synced**. Cloud dashboards are
database rows: "All your infrastructure data, user settings, and custom
dashboards are stored in PostgreSQL"
(https://learn.netdata.cloud/docs/netdata-cloud-on-prem). The Agent side is
file-only: `dashboard_info.js` under the web directory, which must not be edited
in place because updates overwrite it, customised instead by copying the example
and pointing `netdata.conf` at it (agent README). There is a genuine vendor
inconsistency in the setting's name — the web-server reference spells it with a
space where the agent README spells it with an underscore
(https://learn.netdata.cloud/docs/netdata-agent/configuration/securing-agents/web-server-reference,
agent README).

#### Portability

**There is no documented export or import of a dashboard definition in Netdata
Cloud.** The Dashboards tab documentation covers create, add, rename, remove,
save and TV Mode and nothing else (dashboards tab page); the feature page's
"multiple export formats: PNG, CSV, JSON, REST API" refers to exporting *data*,
not layout
(https://www.netdata.cloud/features/visualization/custom-dashboards/). The
nearest thing ever shipped is the `.snapshot` file, which exports the data
embedded in a view over a chosen timeframe and imports as a static replica that
does not update — and it is "only available on v1 dashboards, it hasn't been
port-forwarded to v2"
(https://learn.netdata.cloud/docs/developer-and-contributor-corner/import-export-and-print-a-snapshot).
Legacy HTML dashboards are portable by copying the file and repointing
`data-host`.

#### Format versioning

No published schema-version field for a dashboard document. What Netdata versions
is the *application* and the *API*. The v1 dashboard repository is archived and
deprecated (https://github.com/netdata/dashboard), and the v2.2.0 notes say the
v1/v2 APIs and v0/v1 dashboards "remain available for now and will be removed in
a future release" (https://github.com/netdata/netdata/releases/tag/v2.2.0).
There is **no migration path for user content**: the request to carry custom HTML
dashboards forward to v2 was closed as not planned
(https://github.com/netdata/netdata/issues/16918).

#### Import validation

**Not applicable for Cloud dashboards, and the reason is the finding**: there is
no dashboard-definition import at all, so there is nothing to validate
(dashboards tab page). The only documented import is the v1 `.snapshot`, whose
validation is a human-readable pre-import confirmation showing the snapshot and
its source system rather than a schema check (snapshot page). For file-based
HTML dashboards there is no validation layer at all. `UNVERIFIED:` the practical
failure mode of a dangling chart id — empty box versus page error — is not
stated in Netdata's own documentation.

### Datadog

#### Grid and reflow

`layout_type` is an enum with exactly two values, `ordered` and `free`, sitting
alongside `widgets`, `title`, `tabs` and `template_variables`
(https://github.com/DataDog/datadog-api-client-go/blob/master/api/datadogV1/model_dashboard.go).
A widget's `layout` holds `x`, `y`, `width`, `height` plus `is_column_break` —
"Whether the widget should be the first one on the second column in high
density", of which "only one widget in the dashboard should have this property
set to `true`"
(https://github.com/DataDog/datadog-api-client-go/blob/master/api/datadogV1/model_widget_layout.go).
`reflow_type` is `auto` or `fixed` and is valid only with `ordered`: under
`fixed` every widget carries a layout, under `auto` widgets must **not**, and
supplying coordinates produces a backend "Additional properties are not allowed"
error (https://github.com/DataDog/terraform-provider-datadog/pull/1017). Grid
dashboards have a maximum width of 12 grid squares
(https://docs.datadoghq.com/dashboards/).

#### Adding, configuring and removing a widget

Create or edit, **Add Widget**, pick a type, configure data source and
visualization (https://docs.datadoghq.com/dashboards/widgets/). Groups and
individual widgets drag and drop; a widget is copied with `Cmd/Ctrl + C`, pasted
with `Cmd/Ctrl + V`, removed via **Delete** in its settings menu, and moved
between tabs from its share menu
(https://docs.datadoghq.com/getting_started/dashboards/). Copy and paste are
backed by the Datadog Clipboard, holding up to twenty copied graphs
(https://docs.datadoghq.com/monitors/incident_management/datadog_clipboard/).
Whole-dashboard JSON can be copied, exported and imported — and the
documentation is explicit that importing JSON **"overwrites all content"**
(https://docs.datadoghq.com/dashboards/configure/).

#### Widget parameters

Every widget is `{definition, layout}`; the `definition` carries `type`,
`requests`, `title`, `title_size`, `title_align`, `description`, `custom_links`
and a `time` object taking either a `live_span` or an absolute range
(https://docs.datadoghq.com/dashboards/widgets/timeseries/). Modern widgets
express data as `requests[].queries` plus `formulas` and a `response_format`
rather than a single query string (same page). Dashboard-level
`template_variables` entries have `name` (the only required field), `prefix`,
`available_values`, `defaults` and a deprecated singular `default` that cannot
be combined with `defaults`
(https://github.com/DataDog/datadog-api-client-go/blob/master/api/datadogV1/model_dashboard_template_variable.go);
they are referenced as `$<VARIABLE_NAME>` and reflected into the URL
(https://docs.datadoghq.com/dashboards/template_variables/).

#### UI, file, or both

Dashboards are **server-side objects** with a server-assigned `id` and `url`
plus `author_handle`, `created_at` and `modified_at` — read-back fields, not
authored ones (model_dashboard.go). The JSON is an export representation of that
object: "The schema displayed in the JSON editor mirrors the request body schema
of the Dashboard API"
(https://docs.datadoghq.com/dashboards/guide/graphing_json/). Because both paths
write the same object, a UI edit to a Terraform-managed dashboard is ordinary
drift that the next apply reverts; the inverse, permadrift where the API returns
fields the config never set, was a real reported defect
(https://github.com/DataDog/terraform-provider-datadog/issues/1848). Datadog
warns to pass `--no-sort` to `terraformer` for dashboards "where the order of
elements is meaningful"
(https://docs.datadoghq.com/containers/guide/how-to-import-datadog-resources-into-terraform/).

#### Portability

JSON export is the supported vehicle, including cross-organisation
(https://docs.datadoghq.com/account_management/org_settings/cross_org_visibility/).
What does not travel is everything keyed to the source organisation: the
dashboard's own `id` and `url`, `restricted_roles` as "a list of role
identifiers" and `notify_list` as user handles (model_dashboard.go); an
`alert_graph` widget's raw `alert_id`
(https://docs.datadoghq.com/dashboards/widgets/alert_graph/) and an SLO widget's
raw `slo_id` (https://docs.datadoghq.com/dashboards/widgets/slo/). Metric names,
hosts and tags are never rewritten by any import step, so
integration-specific metrics render empty in a target organisation.
`UNVERIFIED:` the usual migration recipe is a scripted find-and-replace of
monitor and SLO ids before posting — community practice, not a documented
procedure.

#### Format versioning

**There is no schema or format version field** on the Dashboard object; the
full field list is `id`, `title`, `description`, `widgets`, `layout_type`,
`reflow_type`, `tabs`, `template_variables`, `template_variable_presets`,
`notify_list`, `restricted_roles`, `is_read_only`, `tags`, `default_timeframe`,
`url`, `author_handle`, `author_name`, `created_at`, `modified_at`
(model_dashboard.go). Versioning is expressed by deprecating individual fields
and by `reflow_type`'s distinction between the old and new layouts
(pull/1017). Legacy Screenboards and Timeboards had their own now-retired
endpoints, each carrying a deprecation banner
(https://docs.datadoghq.com/dashboards/guide/screenboard-api-doc/,
https://docs.datadoghq.com/dashboards/guide/timeboard-api-doc/); the legacy
Timeboard described graphs with a `viz` key, replaced in the unified model by
`widgets[].definition.type`. Datadog documents **no automatic rewrite** of an old
dashboard's JSON.

#### Import validation

Structural validation is strict and **fail-closed**. The API validates each
widget `definition` against a JSON-Schema union, and a single bad field rejects
the whole request with `400 Bad Request` carrying a positional message naming
the widget's index and type but giving no JSON pointer — the reported example
was caused purely by one field being an empty string
(https://github.com/DataDog/terraform-provider-datadog/issues/1769).
Layout and reflow mismatches are likewise rejected server-side (pull/1017).

**Referential** validity, by contrast, is not checked at write time: `alert_id`
and `slo_id` are plain id fields with no documented existence check, and metric
names are never validated — a query with no data is a legitimate empty graph
(alert_graph and slo pages). `UNVERIFIED:` no Datadog-authored page states that
a dashboard referencing a *deleted* monitor or SLO still imports and errors only
inside that widget at view time; the documentation is silent and there is no
validate endpoint in the reference index. One UI hazard worth stating plainly:
**Import JSON "overwrites all content"**, so a failed or partial import is
destructive to the board it was run on (configure page).

### Kibana

#### Grid and reflow

Panels lay out on a **48-column** grid with fixed-height rows, snapping to
column and row boundaries, and new panels are created at half width — 24 of 48
columns (https://www.elastic.co/docs/explore-analyze/dashboards/arrange-panels).
Position is stored in a `gridData` object holding `x`, `y`, `w`, `h` and `i`,
which replaced the older `size_x`, `size_y`, `row`, `col` in the 7.3 dashboard
migrations (https://github.com/elastic/kibana/pull/39387/files); the same shape
is what the public Dashboards API accepts
(https://www.elastic.co/docs/api/doc/kibana/v9/operation/operation-create-dashboard).
Panels move by dragging the move handle or **by keyboard** — Tab to the move
action, Enter or Space to lock, arrows to reposition — and resize from the
bottom-right corner (arrange-panels page). From 9.1 dashboards support
collapsible **sections** that load content only when expanded, hold up to 1,000
panels each and carry section-scoped filter controls, within limits of 1,000
top-level items and 100 pinned controls (same two sources).

#### Adding, configuring and removing a widget

Edit mode offers two entry points — create a new visualization with Lens or
Maps, or add existing content from the Visualize Library — and "Panels added
from the library stay linked to it unless you explicitly unlink them"
(https://www.elastic.co/docs/explore-analyze/dashboards/create-dashboard).
**By-reference** panels are their own saved objects with the dashboard keeping
only the id; **by-value** panels carry everything needed to render inside the
dashboard, and one dashboard can mix both
(https://www.elastic.co/blog/new-in-kibana-how-we-made-it-easier-manage-visualizations-and-build-dashboards).
The per-panel context menu exposes Edit visualization, Convert to Lens,
Settings, Remove, **Save to library**, **Unlink from library**, Explore
underlying data and View Discover session
(https://www.elastic.co/docs/explore-analyze/visualize/manage-panels).
"Unlink from library" "creates a local copy and leaves the library version
unchanged" — by-reference becomes by-value (same page).

#### Widget parameters

Per panel the API exposes `panelIndex`, `type`, `version`, `gridData`,
`panelRefName` and a config carrying `title`, `description`, `hidePanelTitles`,
`savedObjectId`, `timeRange` and `enhancements` (create-dashboard API page). In
the stored saved object these live inside the **stringified `panelsJSON`**, where
`embeddableConfig` is the panel-type-specific blob
(https://github.com/elastic/kibana/pull/39387/files). At dashboard level the
mapped attributes are `title`, `description`, `panelsJSON`, `optionsJSON`,
`hits`, `version`, `timeRestore`, `timeFrom`, `timeTo`, a `refreshInterval`
object and `kibanaSavedObjectMeta.searchSourceJSON`, which holds the saved query
and filters (https://github.com/elastic/kibana/pull/63864/files). The API's
decoded `options` contains `hidePanelTitles`, `useMargins`, `syncColors`,
`syncCursor` and `syncTooltips` (create-dashboard API page).

#### UI, file, or both

**UI database only.** Saved objects are managed from the Saved Objects page and
stored by Kibana itself, not as files
(https://www.elastic.co/docs/explore-analyze/find-and-organize/saved-objects).
They live in the `.kibana` system index family, and Elastic states plainly: "Do
not write documents directly to the `.kibana` index"
(https://www.elastic.co/guide/en/kibana/current/saved-objects-api-resolve-import-errors.html).
NDJSON is purely transport: "An export produces an NDJSON file where each line
represents a single Saved Object document"
(https://www.elastic.co/docs/extend/kibana/saved-objects/export) — there is no
on-disk source of truth kept in step. Elastic's own engineering blog names the
gap: `panelsJSON` is a "JSON bag" with "no clean way to diff it, review it in a
pull request, or edit it programmatically", making "standard GitOps workflows
impossible"
(https://www.elastic.co/search-labs/blog/kibana-dashboards-as-code-terraform-api),
which is why the Dashboards API and Terraform provider exist
(https://www.elastic.co/docs/explore-analyze/dashboards/create-dashboards-programmatically).

#### Portability

Export is `POST /api/saved_objects/_export`, taking a `type` or an `objects`
array of up to 10,000 pairs plus `includeReferencesDeep`, returning NDJSON
(https://www.elastic.co/docs/api/doc/kibana/operation/operation-post-saved-objects-export).
By default the export includes child objects, so a dashboard carries its data
views along (saved-objects page). Linkage is carried by each document's
`references` array, and a by-reference panel points through `panelRefName`
(pull/39387).

**The classic breakage is data-view identity**: "Kibana assigns a randomly
generated ID to the data view saved object" by default, whereas a custom
human-readable id "makes the data view easier to recreate with the same ID
across spaces, deployments, or environments" so that "Dashboards and
visualizations that reference that ID keep working"
(https://www.elastic.co/docs/explore-analyze/find-and-organize/data-views).
That sentence is the best one-line argument in Family A for **human-readable
natural keys over generated ids**, and *Conclusions* takes it. Version skew is
the other hard limit — an export from a newer release cannot be imported into an
older one
(https://www.elastic.co/guide/en/kibana/8.19/managing-saved-objects.html) — and a
missing plugin surfaces as an `unsupported_type` error
(https://www.elastic.co/docs/api/doc/kibana/v9/operation/operation-post-saved-objects-import).

#### Format versioning

Documents carry `coreMigrationVersion` and `typeMigrationVersion`, which the
import and export APIs rely on for forward compatibility, and Elastic instructs
you to preserve them if you persist raw documents outside Kibana (export API
page). Type-level evolution is declared as **`modelVersions`** on the saved-object
type definition, which "decouple Saved Object versioning from the stack version"
and replace the deprecated `migrations` property that was "tied to stack
versions and did not meet backward-compatibility and zero-downtime
requirements"; each model version declares `changes`, `mappings_addition` and
`forwardCompatibility` schemas
(https://www.elastic.co/docs/extend/kibana/saved-objects/migrations,
https://www.elastic.co/docs/extend/kibana/key-concepts/saved-objects/structure).
On import, migrations are applied automatically — but **only forward**: objects
must come from the same version, a newer minor of the same major, or the next
major, never an older target (import API page). `UNVERIFIED:` the export-details
line's exact field names could not be confirmed on a fetched vendor page.

#### Import validation

This is the best import story in Family A and is described precisely because
*Conclusions* copies it. `POST /api/saved_objects/_import` takes a multipart form
with an NDJSON file and three mutually constrained options: **`overwrite`**
(default false), **`createNewCopies`** (regenerates each object id and resets the
origin, incompatible with `overwrite` and `compatibilityMode`), and
**`compatibilityMode`** (import API page).

The response carries `success`, `successCount`, `successResults[]`, `errors[]`
and `warnings[]`, and the documented error categories are **`conflict`,
`ambiguous_conflict`, `unsupported_type`, `missing_references` and `unknown`** —
each error carrying `id`, `type`, `title`, `meta` and a payload that may include
`destinationId`, `destinations` or the offending `references`, with **one object
able to raise several distinct errors requiring separate resolution steps**
(same page). Crucially the import is **not partially committed past unresolved
errors**: "Objects are created only when all resolvable errors are addressed,
including conflicts and missing references".

**For missing references the user is prompted to point the object at an existing
data view.** The programmatic equivalent is
`POST /api/saved_objects/_resolve_import_errors`, whose `retries[]` entries take
`type`, `id`, `overwrite`, `destinationId`, `createNewCopy`,
`ignoreMissingReferences` and — the interesting one — **`replaceReferences`**, an
array of up to 100 `{type, from, to}` objects naming the old reference id and
the id to substitute
(https://www.elastic.co/docs/api/doc/kibana/operation/operation-post-saved-objects-resolve-import-errors).
On an already-imported object the manual fix is to change the index name in the
object's `reference` array, with the caveat that "Validation is not performed
for object properties"
(https://www.elastic.co/guide/en/kibana/7.17/managing-saved-objects.html). On
malformed input the API returns HTTP 400 rather than importing partially
(import API page). `UNVERIFIED:` whether a single syntactically invalid NDJSON
*line* aborts the whole import or is reported per line is not confirmed on any
vendor page.

### Homepage

#### Grid and reflow

Layout is declared in `settings.yaml` under a top-level `layout:` block keyed by
group name, each group taking `style: row` with `columns: 4`, or the default
column style, plus `header: false` (https://gethomepage.dev/configs/settings/).
Documented knobs are `useEqualHeights`, `initiallyCollapsed` with global
`groupsInitiallyCollapsed` and `disableCollapse`, a group `icon`, `iconsOnly`,
and `tab:` assigning groups to tabs reachable by URL hash. Global width and
density come from `fullWidth`, `maxGroupColumns` (default 4, maximum 8) and
`maxBookmarkGroupColumns` (default 6), with `headerStyle` taking `underlined`,
`boxed`, `clean` or `boxedWidgets` (same page). **There is no documented
breakpoint or responsive rule anywhere**; the column caps are the only column
control exposed. Note too that `settings.yaml` changes are not picked up
passively: "you will need to regenerate the static HTML, this can be done by
clicking the refresh icon in the bottom right of the page" (same page).

#### Adding, configuring and removing a widget

**There is no in-app editor anywhere in the documentation**: every widget is
added, edited or deleted by hand-editing YAML, and the only editing guidance is
YAML hygiene — indentation matters, spaces not tabs, quote API keys, and
"validate your YAML with a linter before deploying"
(https://gethomepage.dev/configs/). The two kinds live in different files:
"Homepage has two types of widgets: info and service", info widgets in
`widgets.yaml` and service widgets inside a service entry in `services.yaml`
(https://gethomepage.dev/widgets/). Info widgets render in file order, except
that weather, search and datetime are aligned right, which affects the layout
(https://gethomepage.dev/configs/info-widgets/).

#### Widget parameters

A service entry carries `href`, `icon`, `description` and the optional monitors
`ping` and `siteMonitor`, the latter an HTTP HEAD falling back to GET that
"cannot authenticate through a reverse proxy"
(https://gethomepage.dev/configs/services/). `statusStyle` restyles status to
`dot` or `basic`, the default being response time in milliseconds (settings
page); `server` and `container` bind a service to a Docker instance
(https://gethomepage.dev/configs/docker/). The service widget itself is a
`widget:` block with `type`, `url`, `key`, `headers`, `fields` — "a list of which
fields should be visible… must be a valid YAML array of strings" — and
`highlight` (services page); the Custom API widget additionally documents
`username`/`password`, `method`, `requestBody`, `refreshInterval`, `display` and
a `mappings` list of `field`/`label`/`format`
(https://gethomepage.dev/widgets/services/customapi/).

#### UI, file, or both

**File on disk only — no database anywhere in the documentation.** The Docker
image mounts the config directory at `/app/config`
(https://gethomepage.dev/installation/docker/), and the shipped skeleton
contains `bookmarks.yaml`, `custom.css`, `custom.js`, `docker.yaml`,
`kubernetes.yaml`, `proxmox.yaml`, `services.yaml`, `settings.yaml` and
`widgets.yaml` (https://github.com/gethomepage/homepage/tree/main/src/skeleton).
Secrets stay out of the YAML by substitution: environment variables must be
prefixed and are interpolated into the config files (docker installation page).
There is nothing to keep in step, because there is only one artefact — which is
the cleanest answer in Family A to the UI-versus-file question, obtained by
declining to have a UI.

#### Portability

Since all state is the config directory, moving an install is a directory copy —
but several things sit outside it. Secrets referenced by the substitution
prefixes live in the environment or in mounted files and must be recreated
(docker installation page). Anything discovered from Docker breaks without the
same environment: `docker.yaml` names socket or host endpoints, services bind by
`server` plus `container`, and label discovery reads labels off the containers
themselves rather than off any file you copy
(https://gethomepage.dev/configs/docker/). Widget URLs are internal addresses,
and site monitoring is explicitly recommended against reverse-proxied URLs in
favour of internal ones (services page). Icons split the same way — named remote
icon sets travel, local icons need the mount recreated plus a restart
(https://gethomepage.dev/troubleshooting/) — and the target needs its own
allowed-hosts variable set (https://gethomepage.dev/installation/).

#### Format versioning

**Confirmed: no schema or format version field exists.** The configuration
documentation describes only YAML syntax rules and never mentions a version key
(https://gethomepage.dev/configs/), the settings reference documents no such
field (settings page), and the shipped skeleton `settings.yaml` is a doc comment
plus a providers block
(https://github.com/gethomepage/homepage/blob/main/src/skeleton/settings.yaml).
There is consequently no migration tool; breaking changes go in release notes,
and v1.0.0 lists as breaking a newly required environment variable, a framework
upgrade dropping an architecture, and a CSS framework move for which "custom CSS
may require updates"
(https://github.com/gethomepage/homepage/releases/tag/v1.0.0).

#### Import validation

The documentation never promises a friendly error for malformed YAML; it pushes
prevention instead — "validate your YAML with a linter before deploying", with
indentation, tabs and unquoted API keys named as the usual causes (configs
page). When something is wrong, troubleshooting sends you to
`config/logs/homepage.log` or the container logs (troubleshooting page).
Runtime widget failures *do* surface in the UI as a per-widget error you can
click, revealing whether the problem is connection or authentication (same
page), and those errors can be suppressed with `hideErrors` globally or per
widget (settings page). `UNVERIFIED:` whether a syntax error in one YAML file
aborts the whole boot or is skipped with the rest still rendering is not stated
on any vendor page.

### Homarr

#### Grid and reflow

Each dashboard is a **board**, "Customizable page which can be edited using the
edit mode. This is where apps, sections and widgets are placed"
(https://homarr.dev/docs/getting-started/glossary/). Edit mode is entered via
the cog at the board's top right and gives drag and drop across the available
columns, where column count "defines how many horizontal columns are available"
(https://homarr.dev/docs/management/boards/). Responsiveness is modelled as
**multiple named layouts per board**, each with its own column count and a
*breakpoint* minimum screen width, **each item's position and size stored per
layout**, with positions re-adjusted automatically when the column count changes
(same page). The schema makes this concrete: a `layout` row carries
`columnCount` and `breakpoint`, and `item_layout` is keyed on
`(itemId, sectionId, layoutId)` carrying offsets and sizes — **there is no
`x`/`y`/`w`/`h` on the item itself**
(https://github.com/homarr-labs/homarr/blob/dev/packages/db/schema/sqlite.ts).
That is a genuinely different answer to responsive layout from everyone else in
Family A, and a much more expensive one.

#### Adding, configuring and removing a widget

Edit mode is a "Temporary state that you can enter where you can drag and drop,
edit or customize your board" (glossary), toggled by the cog (boards page).
Items are added either from the **add-item modal** or by **right-clicking the
board**, and the picker offers apps, widgets and a new category. Each item has a
per-item settings modal; for the App widget the documented controls are choose
app, open in new tab, show app name, description display mode, layout direction
and enable status check (https://homarr.dev/docs/widgets/app/). Items are
repositioned by dragging or by editing their coordinates directly in the layout
settings, and new items are created 1×1 **on every layout** and dropped at the
first free position (boards page).

#### Widget parameters

Board settings map one-to-one onto the `board` table: page and meta title, logo
and favicon URLs, background image with attachment, repeat and size, primary and
secondary colours, opacity, custom CSS, icon colour, item radius, a
disable-status flag, plus visibility and creator (schema file), all exposed in
the UI under the same names (boards page). **Column count lives on the layout,
not the board.** Apps are a separate first-class entity with `name`,
`description`, `iconUrl`, `href` and a ping URL, where `href` supports custom URI
schemes while `javascript:` is blocked, and ping accepts only HTTP and HTTPS so
an internal address can be health-checked while an external one is linked
(https://homarr.dev/docs/management/apps/). Widget instances store config as a
`kind` plus serialised `options` columns rather than named SQL columns (schema
file), across more than fifty documented widget kinds
(https://homarr.dev/docs/category/widgets/); integrations keep secrets in a
separate table, "encrypted using a symmetric encryption and stored in the
database", never sent to the client, with a mandatory connection test before
saving (https://homarr.dev/docs/management/integrations/).

#### UI, file, or both

**UI only. There is no file, and there is no code representation of a card at
all.** "JSON configs have been removed in favour of relation databases", with
the schema managed by Drizzle
(https://homarr.dev/blog/2024/09/23/version-1.0/). SQLite is the default, with
MySQL and PostgreSQL also supported
(https://homarr.dev/docs/advanced/environment-variables/). Every board edit
lands in the database, **no file on disk is kept in step, and the
synchronisation question therefore never arises** — which is a finding rather
than an omission, and it is the reason Homarr cannot be a model for `opnview`'s
editing surface however good its grid is. Secrets are encrypted with a required
64-character hex key without which the container exits (same page).
`UNVERIFIED:` the pre-1.0 project instead persisted one JSON config file per
board, editable both through the UI and by hand — that is documented in the
legacy repository, which is not an admissible domain here.

**What Homarr does instead of a code editor**, because the question was asked
directly and the answer is the useful part: **typed forms, plus one rich-text
widget.** Creating or editing an app is a form — name, an icon picker searching
a large icon set, description, URL, and a separate ping URL
(https://homarr.dev/docs/management/apps/). Editing a widget is a per-item
settings modal of typed controls (https://homarr.dev/docs/widgets/app/). The one
place Homarr edits *content* rather than configuration is the Notebook widget,
and there the editor is a **word-processor toolbar** — bold, italic, headings,
lists, tables, colour — over formatted text, built on a rich-text framework and
described in its own documentation as bringing "a familiar editing experience to
regular users, be it markdown or office type editors"
(https://homarr.dev/docs/widgets/notebook/). **So there is no JSON view, no YAML
view, and no text representation of a board or of a card anywhere in the
product.**

![Homarr's "New app" form](ui-references/screenshots/familya-homarr-app-edit-form.png)
*Source: Homarr's own documentation at `homarr.dev`; captured 2026-09-22.* Name,
an icon picker, a description, a URL, and a separate ping URL behind a checkbox.
What is worth taking: **linking one address while health-checking another** is a
small, genuinely good idea. What is worth noting: this form *is* Homarr's answer
to "edit a card's content", and it is entirely graphical.

![Homarr's Notebook widget in edit mode](ui-references/screenshots/familya-homarr-notebook-edit.png)
*Source: Homarr's own documentation; captured 2026-09-22.* A rich-text toolbar
over formatted content, with the toolbar disappearing entirely in view mode
(`ui-references/screenshots/familya-homarr-notebook-view.png`, same source and
date). What is worth taking: **edit affordances that vanish outside edit mode**,
so the board is calm when it is being read.

![Homarr's responsive layout settings](ui-references/screenshots/familya-homarr-layout-settings.png)
*Source: Homarr's own documentation; captured 2026-09-22.* Named responsive
layouts — Small, Medium, Large — each with a column-count slider and a
breakpoint, and an "Add layout" control. What is worth taking: **this is the
mechanism behind the maintainer's preference for Homarr's tile placement.** The
user defines the breakpoints and the column count at each, and every item's
position and size are stored per layout, so "how it looks on a phone" is
authored rather than derived. It is the most expensive answer to responsive
layout in Family A and the only one that gives the user real control.

#### Portability

**There is no per-board import or export file format.** The feature request
asking to restore the pre-1.0 board export is closed
(https://github.com/homarr-labs/homarr/issues/2229), and what shipped instead is
a whole-instance **Backup & Restore**: an admin-only page exporting a ZIP
containing the SQLite database and a metadata file, covering "apps,
integrations, secrets, boards, users, groups, permissions, search engines,
custom widgets, and server settings"
(https://homarr.dev/docs/management/backup/). That ZIP is **SQLite-only** —
MySQL and PostgreSQL instances do not get the feature. Credentials are handled
explicitly across instances: if the target has a different encryption key,
"integration secrets are decrypted with the old key and re-encrypted with the
new one during import", so secrets survive rather than break (same page). The
one documented lossy path is board duplication, which copies settings and layout
but not access permissions, and renaming a board breaks existing links because
the name is the URL (boards page).

#### Format versioning

**No board-format version field** — the schema exports more than thirty tables
and none stores a schema or config version (schema file). Versioning is carried
by **Drizzle migrations** generated and applied through project scripts
(https://homarr.dev/docs/advanced/development/getting-started/), and the 1.0
schema is explicitly not backwards compatible with 0.x
(https://homarr.dev/blog/2024/09/23/version-1.0/). Backups carry the version
stamp instead of the boards: the metadata file records the Homarr version and
creation timestamp, "Backward compatibility is maintained for older backup
formats", and migrations are applied during restore (backup page). The
documented old-config import path is **first-boot only**, through the onboarding
wizard (https://homarr.dev/blog/2025/01/19/migration-guide-1.0/,
https://homarr.dev/docs/getting-started/after-the-installation/).

#### Import validation

Documentation here is thin and concerns the backup ZIP rather than per-board
files, which do not exist. "The import card validates that the file contains a
valid `db.sqlite` and metadata", and a preview step analyses the uploaded
database **in the browser** to show entity counts and board names *before* the
user commits (backup page). Destructive-action guarding is explicit — the user
must type a confirmation phrase to acknowledge that restoring replaces the
current database irreversibly. What the documentation does **not** cover:
behaviour on a corrupt or truncated archive, dangling foreign keys, specific
error messages, or recovery from a half-applied restore.

### Dashy

The most directly comparable product to `opnview`'s intended shape: one YAML
file, a visual editor and a code editor over it, and a validator that
deliberately does not run where you would expect it to.

#### Grid and reflow

Dashy's homepage is a list of `sections`, each holding `items` and `widgets`,
and the whole layout is CSS grid driven by two independent knobs: how sections
tile the page, and how items tile inside a section
(https://github.com/Lissy93/dashy/blob/master/docs/configuring.md).
`appConfig.layout` is `auto`, `horizontal`, `vertical` or `masonry`: `auto` uses
a responsive grid where each section's footprint is controlled by
`displayData.cols` and `displayData.rows`, `masonry` lets heights follow content
so shorter sections flow into gaps left by taller neighbours (`rows` ignored,
`cols` still honoured), and the two flex layouts ignore `rows` entirely (same
page).

Per-section footprint is `displayData.cols` (1–5, "clamped to the page's active
column count so that a section never exceeds the available grid width") and
`rows` (1–5), plus `cutToHeight` and `collapsed`. Inside a section,
`sectionLayout` picks `auto` or `grid`, and the two count knobs `itemCountX` and
`itemCountY` **switch the section to grid layout implicitly when set** — a nice
piece of API design worth stealing, because the user expresses intent rather than
mode. Item scale is `itemSize` (`small`, `medium`, `large`), settable globally or
per section, where the section value overrides the UI setting. Page-level
overrides are `appConfig.colCount` (1–8) and `contentMaxWidth`, with the
documentation warning that setting `colCount` "will override this behavior" —
defeating the responsive default (same page). **Responsiveness is emergent from
CSS grid plus clamping, not from declared breakpoints**, and layout and icon size
are additionally user-overridable at runtime and persisted per browser.

![The Dashy public demo, sections of differing footprints](ui-references/screenshots/demo-dashy-home.jpg)
*Captured from the live public demo at `https://demo.dashy.to/`, 2026-09-22.*
Sections of different widths and heights tiling one page, each holding a grid of
items. What is worth taking: the section, not the widget, is the unit of
composition — and because a section carries its own column count, a dense
section and an airy one can sit side by side without a global setting. Note the
interface language follows the browser's locale, which is why some labels in the
capture are not English; that is the product's behaviour, not this document's.

#### Adding, configuring and removing a widget

Dashy offers four editing routes and **ranks them itself** by reliability and
usability: editing `conf.yml` directly, the UI JSON editor, the UI visual
editor, and the REST API (configuring page). The visual route is a modal edit
state — "Enter the Interactive Editor" puts the app into **Edit Mode** with a
persistent banner: "You are in Edit Mode… you can make modifications to your
config, and preview the results, but until you save, none of your changes will
be preserved"
(https://github.com/Lissy93/dashy/blob/master/src/assets/locales/en.json). Then
"click any part of the page to edit".

Sections carry a "Click to Edit, or right-click for more options" affordance with
Add and Edit dialogs, name-uniqueness validation, a required-name check, and a
removal confirmation promising "This action can be undone later" (same file).
Widgets are edited through a dedicated dialog whose fields map one-to-one onto
the YAML: **Widget Type** (required), Label, Update Interval, Timeout, Use Proxy,
Allow Insecure, Ignore Errors, and a **Widget Options** section with an **Add
Option** button offering raw key/value pairs. **That key/value escape hatch is
the interesting bit**: because widget `options` are a free-form object per widget
type, the visual editor cannot render a typed form and falls back to an untyped
pair editor — exactly the problem `opnview` will hit if widgets carry arbitrary
options, and a reason the catalogue declares parameters per widget type rather
than leaving them open. The editor menu also offers **Edit Config as Code**, **Edit
Raw Config**, Edit Page Info, Edit App Config behind a "Proceed with Caution"
warning, and a navigation guard: "You have unsaved changes. Leave this page and
discard them?" (same file).

![Dashy's schema-driven configuration editor](ui-references/screenshots/familya-dashy-json-schema-editor.jpg)
*Captured live from the public demo at `https://demo.dashy.to/`, 2026-09-22.*
The interactive editor with `appConfig` expanded: **a typed control generated per
field** — a dropdown where the schema declares an enumeration, a checkbox where
it declares a boolean, a text field otherwise — with Save and Preview beside a
permanently visible green "configuration is valid" indicator. What is worth
taking: **the form is generated from the schema rather than hand-written**, so a
new option gets an editor for free; and **validation status is always on
screen**, not a thing you discover on save.

![The same editor collapsed to its tree](ui-references/screenshots/familya-dashy-config-editor-tree.jpg)
*Same source and date.* The top-level tree — `pageInfo`, `appConfig`, `sections`
— each node annotated with **how many children it has**, above a breadcrumb, a
tree-versus-text mode switch and a search box. What is worth taking: the child
counts turn the tree into a summary of the document, and the mode switch is the
same one-model-two-renderers idea Home Assistant uses, offered at document scope
rather than at card scope. Both captures show the demo's interface in the
browser's own locale, which is why some labels are not English; that is the
product's behaviour, not this document's.

#### Widget parameters

The config has four top-level keys — `pageInfo` (required), `sections`
(required), `appConfig` and `pages` (configuring page). `pageInfo` is
presentation metadata: `title` (required), `description`, `logo`, `favicon`,
`color`, a `footer` accepting inline HTML "sanitized before render", and up to
six `navLinks`. `appConfig` is the behaviour surface — roughly sixty keys
covering layout, theming (`theme`, `dayTheme`, `nightTheme`, `cssThemes`,
`customColors`, `externalStyleSheet`, `customCss`), `startingView`, status- and
ping-check families, authentication, and the config-locking trio
`preventWriteToDisk`, `preventLocalSave` and `disableConfiguration` (same page).

A `section` is `{name (required), icon, items[], widgets[], displayData}` and
"must include either 1 or more items, or 1 or more widgets". An `item` carries a
required `title` capped at eighteen characters, plus `url`, `localUrl`, `icon`,
`target`, `hotkey`, `tags[]`, colours, `subItems[]` and the full status-check and
ping-check blocks; item- and section-level `displayData` carry per-user and
per-group visibility controls. A `widget` is `{type (required), options,
updateInterval, useProxy, allowInsecure, timeout, ignoreErrors, label}` — **so
the transport concerns are uniform across all widget types and only `options`
varies**, which is a clean seam worth copying. Three widget types are generic
escape hatches: `iframe`, `embed` (carrying an explicit "Use with extreme
caution" warning), and the **API Response** widget, which fetches any JSON
endpoint and declaratively maps fields via `mappings[]` of `{field (dot-path),
label, format, scale, prefix, suffix, remap, locale}`
(https://github.com/Lissy93/dashy/blob/master/docs/widgets.md). That last one is
arguably the most relevant precedent in Family A for `opnview`: a generic,
declarative widget that turns an arbitrary JSON endpoint into a labelled stat
list, which removes most of the pressure to write bespoke widget types.

#### UI, file, or both — and how they stay in step

`/user-data/conf.yml` is the nominal single source of truth (configuring page).
But the actual precedence model is subtler, and it is a trap worth naming.

Saving is a deliberate two-button choice, **Save Locally** versus **Save to
Disk**: "Save config locally, to browser storage. This will not affect your
config file, but changes will only be saved on this device" versus "Save config
to the conf.yml file on disk. This will backup, and then over-write your
existing config", with a confirmation on the local path and distinct success
toasts (en.json). Disk saves go to a save endpoint guarded by an admin check,
producing "You cannot write changes to disk because you are not logged in as an
administrator" otherwise
(https://github.com/Lissy93/dashy/blob/master/services/app.js, en.json).

**The precedence rule is the part to internalise.** `ConfigAccumulator` reads
`conf.yml` into the store and then *overlays* localStorage on top of it: if a
localStorage key exists it is used, **otherwise** the file value is, and a
handful of settings have their own dedicated localStorage keys checked first
(https://github.com/Lissy93/dashy/blob/master/src/utils/config/ConfigAccumalator.js,
https://github.com/Lissy93/dashy/blob/master/src/utils/config/defaults.js). So a
local save does not merely shadow the file, **it wins outright for that key on
that browser** — an admin who saved locally and then edits `conf.yml` on disk
sees no change until they "Reset Local Settings", documented as "This will
remove all user settings from local storage, but won't affect your 'conf.yml'
file" (en.json). Two exceptions keep this from being a security hole:
authentication is force-copied from the file, and root-owned fields are stripped
from sub-page configs (ConfigAccumalator.js,
https://github.com/Lissy93/dashy/blob/master/src/utils/config/ConfigHelpers.js).
Note also that a disk save rewrites the whole file, with a backup kept beside
it — **comments and YAML anchors do not survive a UI write**, even though the
documentation encourages using them (configuring page).

#### Portability

Portability is by design: a Dashy dashboard *is* one YAML file, and the UI's
**Export Config** offers "Copy to Clipboard" and "Download as File", alongside
per-config rows showing title, path, a content summary and a schema status badge
(en.json). There is no matching "Import Config" button; the three actual import
paths are pasting YAML into the editor and saving, **Cloud Backup & Restore**
(AES-encrypted client-side, stored under a backup id, restoring "downloads,
decrypts and applies to local storage" — note, *local storage*, not disk)
(https://github.com/Lissy93/dashy/blob/master/docs/backup-restore.md), and
**Load Remote Config**, explicitly "Preview only. This won't make any changes on
disk", carrying a warning that "your browser will be able to execute any
client-side code specified within this config" (en.json).

What breaks across installations: **URLs**, since `url` and `localUrl` are
literal host addresses, so a LAN dashboard is worthless on another LAN.
**Icons** are the richest failure surface because Dashy supports nine schemes —
favicon resolution through a third-party API, several icon-set prefixes,
generative avatars, emoji, raw URLs, and **local icons resolved from a
`item-icons` directory**; that last class, plus a background image path, is the
one that hard-breaks on a move because the files live in a volume rather than in
the config
(https://github.com/Lissy93/dashy/blob/master/docs/icons.md). **Status-check
endpoints** carry the same host-locality problem and additionally require the
backend. **Authentication** embeds password hashes and SSO endpoints in the
config. **Themes and CSS**: `theme` is a bare name and — crucially — the
documentation notes "Styles overrides are only stored locally, so it is
recommended to make a copy of your CSS" (en.json).

#### Format versioning

**`conf.yml` carries no schema or format version.** Dashy ships a JSON Schema at
`src/utils/config/ConfigSchema.json`, also served live from any instance,
declared against draft-07 — but that is the *schema's* dialect version, not the
config's
(https://github.com/Lissy93/dashy/blob/master/src/utils/config/ConfigSchema.json).
The root object requires `sections` with `additionalProperties: false`, and it
accepts an optional `$schema` string described as "Optional reference to this
schema, used by external editors for validation and autocompletion. **Ignored by
Dashy**" — so the one version-ish field in the file is inert at runtime and
exists purely so an editor's language server can light up (same file, and the
configuring page). One deliberate extensibility hatch is worth noting:
`patternProperties` accepting `^x-`-prefixed keys past the strict
`additionalProperties: false`, the same convention OpenAPI uses. Backwards
compatibility is handled by value-level aliasing rather than versioning — a
legacy `startingView` value is documented as an accepted alias (configuring
page).

#### Import validation

The critical finding, and it contradicts the obvious assumption: **Dashy does not
schema-validate `conf.yml` at startup.** The store's fetch action parses YAML and
guards only against transport and syntax failures, committing a critical-error
message; **no validator call appears anywhere in the load path**
(https://github.com/Lissy93/dashy/blob/master/src/store.js). Schema validation is
real but lives in three *other* places: a shared validator compiled from the
schema, used by the JSON editor's diagnostics and by the export dialog's status
badges
(https://github.com/Lissy93/dashy/blob/master/src/utils/config/validateConfig.js);
a second instance inside the schema-driven form; and the CLI, which "will first
check that your YAML is valid, and then validates it against Dashy's schema" and
is the documented pre-deploy gate
(https://github.com/Lissy93/dashy/blob/master/docs/developing.md).

Consequently an **unknown key warns but does not reject at runtime** — the
validator would report it, with a whole vocabulary of friendly messages, but only
inside the editor, the export badges or the CLI; the running app never sees the
verdict (validateConfig.js). The editor surfaces this as a live status chip —
Valid, Invalid, a warning count, or a parse error (en.json). When loading
genuinely fails there is **no fallback to a demo dashboard**; instead a
dismissible overlay renders, listing the error and three checks, with an **Ignore
Critical Errors** button that sets a flag to suppress it permanently
(https://github.com/Lissy93/dashy/blob/master/src/components/PageStrcture/CriticalError.vue).
Behind that overlay the app still renders, because `ConfigAccumulator` exists
precisely to "ensure that any missing attributes are populated with defaults, and
the object is structurally sound, to avoid any error if the user is missing
something" (ConfigAccumalator.js). **Net: defaults-merging plus a non-blocking
banner, not rejection** — the same graceful-degradation philosophy as Home
Assistant's per-card error cards, reached by a different route.

## Family B — what belongs in the widgets

Seven products, each under the same seven sub-headings. The question running
through all of them is the maintainer's: **when something is blocked, does the
interface tell you what blocked it, by name?** The answers differ more than
anything else in this document.

Three of these vendors block automated fetching — `help.ui.com`,
`radar.cloudflare.com` and `help.firewalla.com` all refuse non-browser requests —
so those pages were read through a browser route against the same canonical
URLs, which are the ones cited. Several Zenarmor pages were reachable only as
search-engine summaries and are marked accordingly. **Expect many `UNVERIFIED:`
markers on the closed products. That is the honest outcome, not a gap in
effort**, and it caps how strong the conclusions drawn from them can be.

### UniFi Network

#### Outbound traffic

The primary view is **Insights → Flows**, a table of completed sessions through
the UniFi Gateway; each row carries source and destination, ports, service or
application, bytes, duration, a risk level, allowed or blocked, and the policy
that applied, with a property panel on click
(https://help.ui.com/hc/en-us/articles/32201256219799-Traffic-Flows-and-Traffic-Logging-in-UniFi-Network).
Four presets ship: All Flows, Blocked, Threats and an AI preset. **Retention is
expressed as a row cap, not a time window** — from "Up to 10,000" flow logs on
entry-level consoles to "Up to 20,000,000" on the largest — so real history
depends on how fast the ring buffer fills, and no hours-or-days figure is given
(same page). Separately, with Traffic Identification enabled, a Security
Insights area shows traffic types over a period
(https://help.ui.com/hc/en-us/articles/12570783535383-UniFi-Gateway-Traffic-and-Device-Identification).
Rollup behaviour for the classic statistics charts is **not documented**;
`UNVERIFIED:` community threads discuss an hourly window of about thirty days
(https://community.ui.com/questions/time-series-data-retention-settings/14bc5a8c-7c18-4825-a7b4-417d20fa0c0e
— community, not documentation).

#### World map of destinations

**No documented general "where does my traffic go" map.** What exists is the
**Threat Map**, tied to IDS/IPS, where sanitised addresses can be shown on
Ubiquiti's public threat map
(https://help.ui.com/hc/en-us/articles/360006893234-UniFi-Gateway-Intrusion-Detection-and-Prevention-IDS-IPS)
— a security artefact, partly a community aggregate, not a map of your own
egress. Mark type is **not documented**. `UNVERIFIED:` community consensus is
damning: users report it plots the *target* rather than the attack origin, so
everything piles into one bubble over the user's own country, with no time
slider
(https://community.ui.com/questions/But-what-does-Threat-Map-tell-me/66e36d7f-6959-4aed-88af-142bf8c5b195
— community). Geography exists as *policy* instead: Country Restriction blocks
by country in both directions
(https://help.ui.com/hc/en-us/articles/12567758783383-UniFi-Gateway-Country-Restriction),
with no map rendering of what it blocked. Blocked security events get a poor
map; allowed outbound traffic by destination country gets none.

#### Per-device view

Clients are first-class. With Device Identification on — which silently depends
on Traffic Identification being on first — the Client Devices section shows
manufacturer, model and operating system
(https://help.ui.com/hc/en-us/articles/12570783535383-UniFi-Gateway-Traffic-and-Device-Identification).
A Client Inspector exists for real-time WiFi diagnostics
(https://help.ui.com/hc/en-us/articles/32064585817495-WiFi-Troubleshooting-Guide).
The sharpest per-device traffic story is filtering Insights → Flows by source
device. Beyond that, the contents and layout of a single client's detail page
are **not documented** on `help.ui.com`, even though the official API confirms
the data exists
(https://help.ui.com/hc/en-us/articles/30076656117655-Getting-Started-with-the-Official-UniFi-API).

#### Blocked domain and what blocked it

The answer splits by engine and is worth being exact about. For **IDS/IPS**
attribution is good: the alert names the affected client, the threat source
address, the protocol, the **signature**, the **threat category** and the
timestamp, with a risk level on the Flows page; detections live at System Log →
Security Detection and at Insights → Inspection (IDS/IPS page), with the
enhanced tier drawing on commercial category feeds
(https://help.ui.com/hc/en-us/articles/25930305913751-UniFi-CyberSecure-Enhanced-by-Proofpoint-and-Cloudflare).
For **firewall and policy** blocks the Flows table records the action and the
policy that applied, and the system log carries a discrete "Blocked by Firewall"
event type
(https://help.ui.com/hc/en-us/articles/33349041044119-UniFi-System-Logs-SIEM-Integration).

**The gap is DNS and content filtering.** The Content and Domain Filtering
article documents the engines — off, basic and enhanced, plus ad blocking from
an internal database, safe search and custom lists — but it is purely a
configuration article and **documents no view naming which category, which
blocklist or which custom entry caused a given block, nor where such blocks are
logged**
(https://help.ui.com/hc/en-us/articles/12568927589143-Content-and-Domain-Filtering-in-UniFi).
Content Filtering does appear as the applied *policy* on a flow, so the engine
is named; whether the specific offending category is named is **not
documented**. Verdict: signature-and-category precise for IDS/IPS, policy-name
precise for firewall, **engine-level only for DNS and content filtering**.

#### Per-VLAN or per-subnet traffic

Yes, via filtering rather than a dedicated page. Ubiquiti's own release post
states flows let you "identify and categorize traffic crossing local VLANs"
(https://blog.ui.com/article/releasing-unifi-network-9-1 — first-party but
promotional). Filters apply per column via the magnifying-glass icon in the
column header, and filtered views can be saved (Flows page); `UNVERIFIED:`
third-party reporting names zone and network among the filterable columns
(https://unifinerds.com/unifi-network-9-1-enhancing-network-management-with-visibility-and-control/
— independent blog). Policy is zone-shaped via the Zone-Based Firewall
(https://help.ui.com/hc/en-us/articles/115003173168-Zone-Based-Firewalls-in-UniFi).
What is **not documented** is a per-VLAN *aggregate*: no bytes-per-network chart
over time, no VLAN matrix.

#### What it gets right

**One flow table with saved views and per-column filters**, instead of separate
"logs", "blocked" and "threats" screens: the same rows, four presets, arbitrary
columns, saveable custom views. That is the right shape, and it is the shape
`opnview`'s canvases should make cheap — "blocked" and "IDS alert" as *filters*
rather than as separate pages, so a user goes from "everything" to "what this
interface sent abroad and got blocked for" without changing screens. Second, the
**drill-down-to-action loop on a named detection**: from an alert you can
suppress the signature, exclude the source address or block the connection, and
that materialises as a real firewall rule (IDS/IPS page).

![The UniFi Network dashboard](ui-references/screenshots/familyb-unifi-dashboard.png)
*Source: Ubiquiti's own release post for UniFi Network 9.1,
`https://blog.ui.com/article/releasing-unifi-network-9-1`; captured 2026-09-22.
The site shown is Ubiquiti's demo site, not a real network.* A left rail of
small fact cards beside one wide throughput chart, with a row of per-application
icons beneath. What is worth taking: the per-application icon strip is a dense
"what is on this network" summary that costs one row and no table — and the
whole composition is exactly the light, rounded, generous-spacing aesthetic
`ROADMAP.md` step 3 named as the reference, which the maintainer has since moved
away from; see *The maintainer's recorded preferences*.

![UniFi Insights, Top Destinations and Top Clients](ui-references/screenshots/familyb-unifi-insights-overview.png)
*Same source and date.* The Overview / Flows / Activity / Analyzer / Viewer tab
strip, with Top Destinations listing a country flag beside each domain, Top
Clients beside it, and a map below. What is worth taking: **the country flag
next to each destination row** is the cheapest possible geographic cue, needs no
map, and works in a table — directly applicable to `opnview`'s top-sites and
top-operators widgets.

#### What it gets wrong, and where it buries a view

The single most important visibility switch — **Traffic Identification is buried
at Settings → System → Advanced**, three levels into a general settings page,
and Device Identification silently depends on it; a user staring at an empty
Client Devices list gets no on-screen hint (Traffic and Device Identification
page). Security detections are **split across at least three homes** — System
Log → Security Detection, Insights → Inspection, and Insights → Flows →
Threats — with the documentation itself inconsistent about which is current.
**Retention is a row count, not a time span**, so the interface cannot answer
"how far back can I look?" at all. The Threat Map is the textbook chart that
looks like insight and carries none (`UNVERIFIED:`, community, above). And
content and DNS filtering have rich configuration with **no documented reporting
whatsoever** — which is precisely the opening `opnview` exists to fill.

### Firewalla

#### Outbound traffic

The box records "network flows (same concept as NetFlows)" giving "a
comprehensive history of all inbound and outbound traffic"; each flow carries
name, source and destination address and port, timestamp, direction, the
**outbound interface** it left through, a flow count, duration, uploaded and
downloaded bytes, device name, MAC, vendor, destination domain, region and
category
(https://help.firewalla.com/hc/en-us/articles/24739086338323-Firewalla-Feature-Network-Flows).
Repeated same-source, same-destination flows are **aggregated into one record
with a flow count** rather than listed individually
(https://help.firewalla.com/hc/en-us/articles/1500007220942-Firewalla-Blocked-Flows).
Bandwidth graphs cover 60 minutes, 24 hours and 30 days
(https://help.firewalla.com/hc/en-us/articles/115004304054-Device-Management).
**Retention is the hard limit**: the box keeps roughly 24 hours of flow detail —
"If you require complex reporting and across more than 24 hours of flows, please
see Firewalla MSP" — and the cloud product stores each flow for 30 days,
extendable at extra cost
(https://help.firewalla.com/hc/en-us/articles/4409866753427-Firewalla-Managed-Security-Portal-MSP-Introduction).

#### World map of destinations

**No map could be confirmed in official documentation.** A knowledge-base search
for "map" returns no results
(https://help.firewalla.com/hc/en-us/search?query=map&content_tags=01HZPPFXMJ6XSWPHW8GTMZJBHJ
— negative evidence, cited as such). Region exists as a *list* dimension rather
than a cartographic one: every flow carries a two-letter country code
(https://docs.firewalla.net/data-models/flow/); "Top Blocked" shows ranked top
regions and top destinations (blocked-flows page); the web interface "shows
traffic by region" and lets you filter blocked flows from a named country
(https://help.firewalla.com/hc/en-us/articles/360049374514-A-Secure-and-Better-Network-with-Firewalla-Part-1-Visibility);
and the cloud product has a "Top Regions by Blocked Flows" module
(https://help.firewalla.com/hc/en-us/articles/27174165629971-Firewalla-MSP-Reports).
Blocked traffic is very much included — the *primary* region view is the blocked
one. `UNVERIFIED:` a community feature request implies some map-like element in
the mobile app, but no official page confirms it
(https://help.firewalla.com/hc/en-us/community/posts/5780686426899-Geo-IP-Map-Display
— community).

#### Per-device view

Tapping a device opens a detail page in four sections — Devices, Network Flow,
Control, Information (device-management page). Network Flow gives the three
bandwidth graphs, top uploads and downloads, full flow history, live throughput
when on-LAN, and a **Local Flows** block. Control gives one-tap category blocks,
feature toggles and a **Rules** entry that "brings up all rules applied to the
device or the group/network this device belongs to". Information holds name,
address, local domain, MAC, vendor, allocation mode, online status and a ports
list split into forwarded and not forwarded. **The same page shape is reused for
Groups, Users and Networks**, which is what makes per-VLAN views possible at
all — one page template, four subject types.

![Firewalla's device list, grouped by network](ui-references/screenshots/familyb-firewalla-devices-list.png)
*Source: the Firewalla app listing on Google Play, vendor demo data;
captured 2026-09-22.* Local, online and offline counts; a
Networks / Groups / All segmentation toggle; devices grouped under the access
point they sit on, each with a name, an address, signal information and a type
tag. What is worth taking: **grouping devices by the network they sit on, with a
per-device type tag**, is exactly the segment-aware device list `opnview` needs,
and the Networks/Groups/All toggle is the cheapest possible implementation of
"traffic by VLAN or by device" as one widget rather than two.

![Firewalla's per-device Activities and Flows toggle](ui-references/screenshots/familyb-firewalla-activities-flows.png)
*Source: the Firewalla app listing on Google Play, vendor demo data; captured
2026-09-22.* One subject — here a person rather than a machine — with an
**Activities | Flows** switch: a stacked hourly time chart above, then
per-application time spent with bars. **The maintainer has named this one of his
preferences**; see *The maintainer's recorded preferences*. What is worth
taking: the same subject and the same time window rendered twice, once as
human-readable behaviour and once as raw connections, with a single toggle
between them — for `opnview` that is "this device's sites and volumes" against
"this device's flow records", which the catalogue currently splits across two
widgets and probably should not.

#### Blocked domain and what blocked it

**The best answer in Family B.** A blocked flow's detail page shows **Block
Type** ("IP Filtering or DNS Filtering") and **Blocked By** — "if the flow was
blocked by Ad Block or Active Protect, the feature name will be shown here"
(network-flows page). The attribution is a **tappable cross-reference**: "If the
flow was blocked by a Rule or Feature, it'll display it under 'Rule Matched' or
'Feature Matched.' Tap on this to go directly to the Rule or Feature page"
(blocked-flows page). Microsegmentation blocks name their own mechanism.

**Crucially, attribution is not guaranteed, and there is an honest fallback**:
"If the flow was blocked by something else, you can tap Diagnose to find out what
rule or policy was the cause". The reverse link also exists — a rule shows a hit
count and a last-hit time, and "If the flow was within the last 24 hours, you can
tap View Flow to go directly to the flow detail page"
(https://help.firewalla.com/hc/en-us/articles/360008521833-Manage-Rules). The
cloud product is stronger still: a **"Matched By" column** shows any rule or
feature related to the flow, and flows can be filtered by it (MSP reports page).

Two caveats, stated because they matter for schema design. The public flow data
model documents only a block boolean and a block type with **no documented
block-reason or rule-id field** (https://docs.firewalla.net/data-models/flow/);
and `UNVERIFIED:` a community post reports that the cloud API's blocked-by value
returns opaque identifiers while noting "The UI does a great job of showing very
clearly what each blocked flow is blocked by" — i.e. the human-readable naming is
a UI-side mapping, not a data-model contract
(https://help.firewalla.com/hc/en-us/community/posts/38629064411795-Understanding-the-blockedby-Flow-property-from-MSP-API
— community).

![Firewalla's home screen: flows and blocked, as one pair](ui-references/screenshots/familyb-firewalla-home-flows-blocked.png)
*Source: the Firewalla app listing on Google Play, vendor demo data;
captured 2026-09-22.* "Flows in last 24 hours" and "Blocked" as a single
headline pair with a percentage ring, above a 24-hour upload and download chart,
with a separate **Local Flows** panel beneath. What is worth taking: the
flows-and-blocked pair with a percentage is the clearest framing anywhere of
"how much of what I saw was stopped" — and the separate treatment of local
(inter-segment) flows is precisely `opnview`'s east-west versus north-south
split, given a home on the landing page rather than buried.

#### Per-VLAN or per-subnet traffic

Networks are first-class objects in the device list alongside devices and
groups, each with the same detail page: own flow graphs, flow list, live
throughput and rules (device-management page). Firewalla states it plainly:
where data goes, how much, ingress versus egress, allowed versus blocked and why
"is available by device, device Group, and network segment"
(https://help.firewalla.com/hc/en-us/articles/4410153017619-Firewalla-s-Deep-Insights).
**Local Flows** is the genuine inter-VLAN view, recording traffic "between
devices on different LANs or VLANs" — but it needs router mode with more than one
local network, is unavailable in bridge mode, and detailed history is
unsupported on the smaller hardware for memory reasons. It is blind to east-west
traffic that never crosses Firewalla hardware — **exactly the observation-point
limit an OPNsense-based tool faces**, in a shipping product, which is useful
corroboration that the limit is inherent rather than a defect of this design.

#### What it gets right

**The blocked-flow → blocker → back-to-flow round trip.** A block is never a
dead end: the flow names the rule or feature, that name is a link into the rule
page, and the rule's last hit links back to the flow. The **Diagnose** button as
a fallback for "we cannot attribute this one" is honest UX worth stealing
verbatim, because it admits the limit instead of showing a blank field. Second,
**"exclude system noise" as a first-class list filter**: flow lists can hide
inbound flows, blocked flows, system noise ("ads, tracking, telemetry, software
updates, analytics, NTP, and public cloud services") and specific devices
(visibility page). Any raw firewall-log view drowns in noise, and a curated "hide
the boring 90%" toggle is the difference between a usable page and a log tail.
Worth copying too: the "Report Incorrect Region / Report Incorrect Category"
affordance, which turns enrichment errors into a feedback loop instead of silent
wrongness.

![Firewalla rules, addressed to a scope](ui-references/screenshots/familyb-firewalla-rules.webp)
*Source: an independent review at `virtualizationhowto.com`, vendor demo data;
captured 2026-09-22.* Rules grouped by the target they apply to — all devices,
an access point, a tag, a single device — with per-target counts, and each rule
readable as one sentence. What is worth taking: **a rule stated as a sentence
addressed to a scope**, rather than as a row in a firewall-rule table, is a
model for how `opnview` should render the rule that blocked something.

#### What it gets wrong, and where it buries a view

Retention plus paywall: about 24 hours on the box, longer only through the paid
cloud. **You cannot search flows in the app at all**, and Firewalla says why —
"Firewalla Network Flows on your Firewalla are optimized for security lookups,
and there is no relational database to support 'flexible' search. Doing linear
searches via the app often will slow down your network processing time"
(network-flows page) — a direct warning for any design storing flows in embedded
SQLite **on the firewall itself**, and an argument for `opnview` keeping its
database off the firewall, which it does. **There is no local web UI**: every web
interface is cloud-hosted and reached by pairing from the phone app
(https://help.firewalla.com/hc/en-us/articles/360052779253-Firewalla-MSP-Lite-previously-my-firewalla-com).

Buried paths, concretely: data-usage history is at *box main page → scroll to
bottom → More → Data Usage*; "which flows did this rule block?" is *blocked flow
→ detail → scroll to bottom → Diagnose*, so **the single most useful question
about a rule has no button on the rule itself in the app**; and blocked flows
are at *main page → Flows in the last 24 hrs → View Blocked*, with Top Blocked
one level deeper. The geographic story is weak: ranked lists only, and the cloud
region module covers **blocked flows only**, so there is no documented "where
does my allowed outbound traffic go" view anywhere in the product.

### NextDNS

#### Outbound traffic

DNS-only: no packet capture, no NetFlow, no byte counts — a "destination" is a
resolved domain, not an observed connection. The product offers "in-depth
analytics and real-time logs" across an Analytics tab and a Logs tab
(https://nextdns.io/). Retention is a first-class setting: logs kept "from one
hour up to two years", or logging disabled entirely, with the storage location
chosen among four jurisdictions (same page). Analytics is computed *from* the
logs and "gracefully degrades depending on the level of logging configured down
to no analytics at all if logging is disabled", and the retention choice is
pushed to edge servers so unwanted data is never shipped
(https://help.nextdns.io/t/y4hmvar/does-nextdns-collect-and-store-personal-data).
The API mirrors the interface, with an analytics endpoint per dimension and a
time-series twin for each (https://nextdns.github.io/api/). `UNVERIFIED:`
community reports the signup defaults
(https://help.nextdns.io/t/p8hpdsa/navigation-logs-retention — community).

#### World map of destinations

Yes, and the API pins down exactly what it plots: the destinations endpoint
returns a country code with a query count per entry — **the country of the
resolved destination, not of the client** — and a second mode buckets
destinations by tech-giant ownership (https://nextdns.github.io/api/). The
downloadable CSV carries a matching per-row destination country, so the map is a
roll-up of a per-query attribute. **Whether blocked queries appear on that map is
not documented, and this document will not guess**: a blocked query never gets a
resolved address, so there is logically no destination to geolocate, but no
`nextdns.io` or `nextdns.github.io` page states this either way. `UNVERIFIED:`
the CSV field list is corroborated only by a community thread
(https://help.nextdns.io/t/x2httxp/download-logs-missing-the-resolved-domain-ip
— community).

#### Per-device view

Devices are a first-class dimension: the devices endpoint returns per-device
statistics and every log entry carries a device object with an id, a name and a
model (API page). Four attributes can be attached per query — device id "unique
to this profile", device name, device model and the device's private LAN
address — passed via the encrypted-DNS hostname, the DoH URL path or HTTP
headers; **the device id exists specifically so a renamed device does not split
into two rows**
(https://help.nextdns.io/t/x2h76ay/device-information-log-enrichment). The
catch: this only works with encrypted-DNS clients or the vendor's own CLI. With
plain forwarding through a router and a linked address, every host collapses
into one public address and the Devices view is empty or shows numbered
placeholders (same page; `UNVERIFIED:` community corroboration at
https://help.nextdns.io/t/g9hts3n/identify-devices-when-using-nextdns-on-router).

#### Blocked domain and what blocked it

**Yes, by name, in the API contract — and this is the sharpest thing in Family
B.** Every log entry has a status and a **`reasons` array of objects each
carrying both a machine `id` and a human `name`**. The documented example is
unambiguous: a blocked domain with
`"reasons":[{"id":"blocklist:nextdns-recommended","name":"NextDNS Ads & Trackers
Blocklist"}]` (https://nextdns.github.io/api/). The id namespace distinguishes a
subscribed blocklist from a personal denylist entry, and a dedicated reasons
endpoint ranks block reasons by query count. **`reasons` being an *array*
matters**: one query can be attributed to several causes at once, which a single
"blocked by" column cannot express.

Two honest caveats. No page enumerating reason ids for the security features or
the parental-control categories could be found, so their exact strings are not
asserted here. And `UNVERIFIED:` community idea threads show that filtering the
Logs tab *by* blocked reason was a wish-list item rather than a shipped feature
(https://help.nextdns.io/t/m1hs38m/filter-logs-by-blocked-reasons,
https://help.nextdns.io/t/y4hlrxb/blocked-reason-in-logs — both community). So
the reason is shown *per entry*, but slicing the whole log by reason has been
weak.

![NextDNS naming the list that blocked a domain](ui-references/screenshots/familyb-nextdns-app-blocked-by-blocklist.png)
*Source: the NextDNS application's own App Store listing images, with the phone
frame cropped away locally; captured 2026-09-22.* A domain detail sheet opened
from the blocked-queries list: a banner reading "Blocked by NextDNS Ads &
Trackers Blocklist.", then the device, the protocol the lookup arrived over, the
time, the root domain, a category description, and one **Allow Domain** button.
**This is the single most valuable image in this document.** What is worth
taking: the verdict names the list *in plain language*, sits beside its
provenance (which device, which protocol, which root domain), and offers exactly
one remediation — and all of it is one tap from the log row, not on another
page.

![NextDNS's blocklist catalogue](ui-references/screenshots/familyb-nextdns-blocklists.png)
*Source: an independent review thread at `theprepared.com`; captured
2026-09-22.* Each subscribed list as a card with its name, a one-line purpose, a
link to its source, an entry count and "updated N ago". What is worth taking:
**provenance, scale and freshness on one line per source** — directly reusable
for `opnview`'s own source-availability panel, which has exactly the same job of
telling the user what is feeding the numbers and how stale it is.

#### Per-VLAN or per-subnet traffic

**Not a concept.** No subnet or VLAN dimension appears anywhere in the API; the
groupings are device, client address, domain, reason, protocol, query type,
address family, DNSSEC status and destination country (API page). Two coarse
workarounds exist: give each segment its own profile, each being a separate
configuration with its own analytics and logs; or lean on the per-device LAN
address and infer the subnet yourself — which needs the enriched clients above
and dies behind a plain-forwarding router.

#### What it gets right

**The named block reason on the log row.** Pairing a stable machine id with a
display name is exactly the right schema — carry rule id *and* rule name, never
one without the other — and it turns "why is this broken?" from an investigation
into a glance. Second, **retention and data location as explicit, user-visible
settings that analytics degrades against gracefully**: one hour to two years,
four jurisdictions, or nothing at all. A corollary worth copying: every analytics
endpoint has a time-series twin, so each aggregate gets a "how did this evolve"
counterpart for free — a cheap way to make every widget in `opnview` answer both
"how much" and "how is it trending".

#### What it gets wrong, and where it buries a view

The device dimension — the one most people want — **silently collapses to nothing
in the most common deployment** (a router plus a linked address), and the fix is
documented in a how-to guide about log enrichment rather than surfaced at the
empty Devices view itself. The second buried view is the `reasons` data: rich,
structured and rankable through its own endpoint, yet `UNVERIFIED:` community
reports say the Logs tab could not be filtered by it, pushing people to export
CSV and grep. Structurally, DNS-only means a device that hardcodes addresses,
uses third-party DoH, or talks to a literal address is **invisible** — and
nothing in the Analytics tab warns you about that observation-point limit, which
is precisely the omission `ROADMAP.md` forbids `opnview` from making.

### Pi-hole

#### Outbound traffic

Also strictly DNS. The interface shows "the domains being queried on your
network", the time queries were initiated, how many were blocked, the upstream
server queries were sent to, and the query type (https://github.com/pi-hole/web).
No flows, no bytes. Two storage tiers: an in-memory recent-queries view feeding
the Query Log, and a long-term SQLite database whose `queries` view sits over a
storage table plus lookup tables for domain, client, forward destination and
additional info — integer-id normalisation done explicitly to keep the file small
(https://docs.pi-hole.net/database/query-database/).

**A real documentation inconsistency, recorded rather than papered over:** the
query-database page documents the retention default as **365 days** (same page)
while the configuration-file page says **91 days**
(https://docs.pi-hole.net/ftldns/configfile/). One is stale; treat the number as
something to verify against your own installation.

#### World map of destinations

**No.** There is no geographic map anywhere and no country or geolocation field
in the query database — the stored dimensions are domain, client, forward
destination, query type, status, reply type and time, and an additional-info
column (query-database page). Neither the documentation overview
(https://docs.pi-hole.net/), the web README, nor the v6 launch post
(https://pi-hole.net/blog/2025/02/18/introducing-pi-hole-v6/) mentions
geography. Pi-hole never learns the answer's geography anyway: it records *which
upstream resolver it forwarded to*, not where the resolved address lives. So on
this axis Pi-hole gives nothing to copy — only confirmation that a map is a
genuine differentiator rather than table stakes.

#### Per-device view

Yes, by client address, and reasonably good. The dashboard has a "Clients over
time" graph and a "Top Clients" panel, both allowed and blocked — named
explicitly in the privacy-levels documentation, which lists what each level
disables: one level kills "Top Domains, Top Ads, Top Clients, and Clients over
time", and the next kills the Query Log and long-term logging entirely
(https://docs.pi-hole.net/ftldns/privacylevels/). Clients are normalised into a
lookup table, so per-client history survives restarts. v6 rebuilt the interface
with settings split into basic and expert modes and gave the query log
server-side pagination, which is what makes filtering a large log by client
responsive (v6 launch post). `UNVERIFIED:` clicking a client from the
*blocked-only* Top Clients panel has been reported to show all its queries rather
than only the blocked ones
(https://discourse.pi-hole.net/t/all-clients-queries-listed-when-clicking-on-individual-client-in-dashboard/11816
— community). Structural limit: identity is the client address, so a device that
changes address becomes a different client.

![The Pi-hole v6 Query Log](ui-references/screenshots/familyb-pihole-query-log.png)
*Source: the `pi-hole/web` project's published screenshot set at
`pi-hole.github.io/graphics`; captured 2026-09-22.* Time, a status icon, type,
domain, client, reply time, and per-row Allow and Deny buttons, over server-side
pagination. What is worth taking: **the per-row verdict icon plus a one-click
allow or deny** is the cheapest "why was this blocked, and change it"
affordance — but note what is *not* in the table, which is the subject of the
next sub-heading.

#### Blocked domain and what blocked it

**The engine yes, the list no** — and this distinction is the whole lesson of
Family B.

The Query Log's status column names the **blocking engine**, stored as a closed
set of documented integer codes: gravity, regex denylist, exact denylist, and
separately the same three outcomes reached through **CNAME inspection**, where
the record also stores the domain that was the reason for killing the whole
CNAME chain (query-database page). For regex and exact-denylist blocks the log
goes one step further and stores the id of the corresponding entry in the domain
list, giving attribution to the specific rule (same page).

**But for gravity — which covers the overwhelming majority of blocks — the query
log says only that it was a gravity block. It does not name the adlist.** The
information exists, but in a *different* database: the gravity table carries an
adlist id linking to the adlist table, with uniqueness enforced on the pair so
that "domains can be added multiple times, however, only when they are
referencing different lists as their origins"
(https://docs.pi-hole.net/database/domain-database/). Recovering the list name
needs a **separate manual lookup**: Tools → Query Lists in the web interface, or
a command that "will query your allowlist, denylist, wildcards and subscribed
lists for a specified domain"
(https://docs.pi-hole.net/main/pihole-command/). So the workflow is: see the
block, copy the domain, navigate to another page, paste, search. **It is not a
join in the log.**

`UNVERIFIED:` a developer pull-request discussion indicates doing that join live
was considered and rejected on cost, since scanning the whole adlist table could
mean seconds to minutes of daemon downtime
(https://github.com/pi-hole/FTL/pull/1532 — developer thread, not
documentation); `UNVERIFIED:` a long-standing community request confirms the gap
(https://discourse.pi-hole.net/t/show-which-blacklist-blocked-a-query-in-recent-queries-instead-of-gravity/62760
— community).

#### Per-VLAN or per-subnet traffic

**No.** Client address is the only network dimension in the schema, with no
subnet, VLAN or interface column (query-database page). Group management lets you
assign clients to groups with different adlists and rules
(https://docs.pi-hole.net/database/gravity/groups/), but that is a *policy*
grouping applied at resolution time, and no documentation was found showing that
the dashboard or query log can be sliced by group after the fact. Realistically:
sort by client address and read the subnets off the addresses yourself.

#### What it gets right

**The status taxonomy is a small closed set of documented integers that
separates mechanism from path** — gravity versus regex versus exact, each
doubled for CNAME inspection, with the chain-triggering domain retained. That
"what blocked it, and did it get there indirectly?" pair is exactly the
distinction needed for anything firing on an indirect match, and the
closed-integer-with-documented-label pattern keeps a filter interface honest.
Second, **privacy levels as a documented ladder where each rung names precisely
which views it kills** (privacy-levels page): being explicit that a privacy
setting *removes named views* rather than vaguely anonymising is worth stealing
for `opnview`'s own aggregate mode.

![Pi-hole's Top Domains and Top Blocked Domains, side by side](ui-references/screenshots/familyb-pihole-top-domains-clients.png)
*Source: the `pi-hole/web` published screenshot set; captured 2026-09-22.* Two
panels, allowed and blocked, each a domain-and-count list with an inline
frequency bar. What is worth taking: **the inline bar inside the table row**
does the work a separate chart would take, and the deliberate pairing of
"top queried" with "top blocked" as adjacent panels makes the comparison without
a legend.

#### What it gets wrong, and where it buries a view

The buried view is exact and it is the one that matters most: **"which of my
blocklists is responsible for this block" lives at Tools → Query Lists, a
completely different page from the Query Log where you hit the problem** — copy
the domain out, paste it into a separate search tool, and the answer may be
"this domain is on four of your lists", which still does not tell you which one
actually matched. The transferable lesson is a schema decision, not a UI one:
**decide at ingest time whether to store the matching rule's id on the event
row — retrofitting that join later is what forces a product into a second,
disconnected "go look it up" page.** That is exactly the decision
`docs/widget-catalogue.md` records as gap G1. Secondary weaknesses: no geography
at all; the self-contradicting documented retention default; and a dashboard
whose panel inventory is essentially undocumented — it could only be enumerated
by reading which views the privacy-levels page disables, a documentation smell
in its own right.

### ntopng

#### Outbound traffic

ntopng splits every host into **local** versus **remote**: local hosts get
visited websites, DNS requests and historical layer-7 time series; remote hosts
get none of that and no time series at all
(https://www.ntop.org/guides/ntopng/basic_concepts/hosts.html). Direction is
first-class but crude: "TX traffic is depicted in blue and RX in green. Observed
traffic sent by a local host to a remote host is considered TX, whereas
everything else is considered RX" (same page). Live granularity is per-flow with
layer-7 application labels
(https://www.ntop.org/guides/ntopng/user_interface/network_interface/index.html),
fed by packet capture or by NetFlow and sFlow through a probe
(https://www.ntop.org/guides/ntopng/flows/exporters.html). Retention is two-tier:
**time series** to RRD by default, or InfluxDB or ClickHouse, with interface
traffic usually at one second and host layer-7 protocols at five minutes
(https://www.ntop.org/guides/ntopng/basic_concepts/timeseries.html); and **raw
historical flows**, which require ClickHouse and are licence-gated
(https://www.ntop.org/guides/ntopng/flow_dump/clickhouse/historical_flows.html).

#### World map of destinations

There is a dedicated **Geo Map** page: "a world map where hosts are arranged
according to their geographical position", requiring geolocation to be enabled
(https://www.ntop.org/guides/ntopng/user_interface/network_interface/maps/geo_map.html).
The documentation does not describe the rendering, so the page template was
read: it is a **point-marker map**, one marker per host with a popup carrying
score, AS name, nation, alerted-flow and blacklisted-flow counts, bytes sent and
received and total flows, with a dropdown toggling active against alerted hosts.
**No arcs, no choropleth** — and hosts without geolocation fall back to a
hardcoded default coordinate with a warning banner
(https://github.com/ntop/ntopng/blob/dev/httpdocs/templates/pages/hosts_geomap.template).
Geolocation is local-file only, "with no cloud access whatsoever"
(https://www.ntop.org/guides/ntopng/basic_concepts/geolocation.html), which is
the same posture `opnview` takes with MaxMind. Country and AS aggregation live on
separate *list* pages rather than on the map
(https://www.ntop.org/guides/ntopng/user_interface/network_interface/interface/countries.html,
https://www.ntop.org/guides/ntopng/user_interface/network_interface/interface/autonomous_systems.html).
On blocked traffic: ntopng is a passive monitor, so the closest thing is a
blacklisted-flow marker — *flagged*, not dropped; actual blocking belongs to the
inline sibling product (https://www.ntop.org/guides/nedge/). **So the world map
is a map of observed destinations, not of denied ones.**

![ntopng's host geo map](ui-references/screenshots/familyb-ntopng-geomap.png)
*Source: the ntopng user's guide at `ntop.org`; captured 2026-09-22. The
documentation's own capture has the host address redacted.* A muted grey
basemap, one small dot per remote peer, and a popup carrying the interesting
values. **The maintainer has named this his preferred world map**; see *The
maintainer's recorded preferences*. What is worth taking: **the map is
background and the data is foreground** — a de-saturated basemap with small
marks reads far better than a choropleth when the question is "which handful of
places", and every attribute lives in the popup rather than cluttering the
canvas.

#### Per-device view

Host Details is the densest screen in Family B. The documented tabs are Home
(MAC, address, hostname, location, first and last seen, traffic breakdown, a
six-hour activity map), Traffic, Packets, Ports, Applications, DNS, TLS, SSH,
HTTP, Sites, Flows, SNMP, Talkers (a Sankey of top peers), Geomap (this host's
peers), Alerts Configuration and Statistics
(https://www.ntop.org/guides/ntopng/user_interface/network_interface/hosts/hosts.html).
**Score** is a host-level numeric risk attribute surfaced both on the host page
and in the geo-map popup. Remote hosts get a much thinner version.

![ntopng host details](ui-references/screenshots/familyb-ntopng-host-details.png)
*Source: the ntopng user's guide; captured 2026-09-22.* MAC and vendor, address
and subnet, name with local and private badges, alerts, score, first and last
seen, an as-client versus as-server split, and an "Additional Host Names" row
whose **Source is named as DHCP**. What is worth taking: the as-client /
as-server split, and above all **naming the source of a host's name in the row
that shows the name** — which is exactly what `opnview` must do for site names
inferred from resolver correlation.

#### Blocked or flagged domain and what flagged it

**The strongest technical answer in Family B, in three stacked layers.**

First, **the check that fired is named**: every detection is a behavioural check
with a human name, individually toggleable, grouped by entity
(https://www.ntop.org/guides/ntopng/user_interface/shared/alerts/others/available_alerts.html),
and the check name becomes a filterable alert type
(https://www.ntop.org/guides/ntopng/user_interface/shared/alerts/others/alerts_filters.html).
Second, **protocol risks are named, scored and each has a documented
remediation** — a severity, a numeric score, a plausible threat and a concrete
remediation sentence per risk
(https://www.ntop.org/guides/ntopng/user_interface/shared/alerts/remediations/ndpi_flow_risks.html).
Third, **the blacklist itself is named in the alert message**: the prose
documentation never says so
(https://www.ntop.org/guides/ntopng/user_interface/shared/settings/blacklists.html),
but the English locale file contains alert templates with an explicit blacklist
name slot
(https://github.com/ntop/ntopng/blob/dev/scripts/locales/en.lua), filled from the
matched list file by the alert definition
(https://github.com/ntop/ntopng/blob/dev/scripts/lua/modules/alert_definitions/flow/alert_blacklisted_server_contact.lua).

Honest caveat: the *generic* blacklisted-flow alert emits only a category
statement with **no list name**
(https://github.com/ntop/ntopng/blob/dev/scripts/lua/modules/alert_definitions/flow/alert_flow_blacklisted.lua),
so attribution quality depends on which of the overlapping blacklist checks
fires, and ntop have an open issue to deduplicate them
(https://github.com/ntop/ntopng/issues/8833). ntopng can also ingest **Suricata
`eve.json` alerts over syslog**, so the IDS signature becomes another named
source
(https://www.ntop.org/guides/ntopng/third_party_integrations/suricata.html) —
directly relevant to `opnview`, which has the same source and reaches it a
different way.

#### Per-VLAN or per-subnet traffic

VLAN-aware: "Ntopng is VLAN aware, hence if several VLANs are detected, traffic
is accounted also on a VLAN basis"
(https://www.ntop.org/guides/ntopng/user_interface/network_interface/interface/interfaces.html).
There is a real **VLANs page**, each VLAN drilling into a host list, historical
time series, alerts and configuration
(https://github.com/ntop/ntopng/blob/dev/scripts/lua/vlan_stats.lua,
https://github.com/ntop/ntopng/blob/dev/scripts/lua/vlan_details.lua). Subnets
are separate: the **Networks** page lists networks discovered or declared, with
host counts, alert counts, traffic breakdown and throughput, plus rename and
label
(https://www.ntop.org/guides/ntopng/user_interface/network_interface/interface/networks.html).
Two catches, from the menu source: **the VLANs entry is hidden when no tagged
traffic has been seen**, and the Networks entry is hidden on the paid tiers
because it is superseded by another dashboard
(https://github.com/ntop/ntopng/blob/dev/scripts/lua/modules/menu_definition.lua).

#### What it gets right

**Named, individually-togglable detections with a documented remediation.** Every
alert traces back to a check with a stable name; the check list is a first-class
configuration page; each risk has its own page with severity, score and a
concrete remediation. That turns "this flow is suspicious" into "this flow is
*this named thing*, at this score, and here is what to do".

Second, and this is **the single most directly transferable idea in the whole
document**: **the menu hides entries that cannot have data, and says why.** Menu
items carry a hidden predicate plus a structured reason and suggestion — no
VLANs seen, no geolocation database, viewing an aggregate interface — so an
empty section is *explained* rather than silently blank (menu_definition.lua).
That is `ROADMAP.md`'s "the observation limit is displayed, not hidden" rule,
already implemented by somebody else, in a way `opnview` can copy directly.

![ntopng's Top Flow Talkers Sankey](ui-references/screenshots/familyb-ntopng-top-flow-talkers.png)
*Source: the ntopng user's guide; captured 2026-09-22.* Source hosts on the
left, destinations on the right, ribbon width proportional to bytes. What is
worth taking: this is the canonical who-talks-to-whom visual, and it is a
serious alternative to `opnview`'s segment matrix for the same question —
**ribbon width makes inter-segment relationships legible at a glance**, where a
matrix makes them precise. The two answer different questions and could
reasonably both exist as widgets.

![ntopng's live flows table](ui-references/screenshots/moodboard-domains-ntopng-flows.png)
*Source: the ntopng user's guide; captured 2026-09-22.* A row of dropdown
filters, one per dimension, above a live flow table whose central column renders
a whole flow as one cell — host badge, port, a bidirectional arrow, host badge,
port. **The maintainer has named ntopng's flow view outstanding**; see *The
maintainer's recorded preferences*. What is worth taking: **rendering a flow as
one cell rather than four columns** halves the width of the table and makes the
pairing readable, and the filter bar as a dropdown-per-dimension row is a
better query builder than a search box.

#### What it gets wrong, and where it buries a view

The free dashboard is thin — a talkers Sankey and two pies — while top local
talkers, top destinations and historical comparison are paid
(https://www.ntop.org/guides/ntopng/user_interface/network_interface/dashboard/dashboard.html),
and the whole raw-flow history story is gated behind a paid tier plus an
external database. Concrete buried views: **the VLANs page is invisible until
tagged traffic is observed**, and **the Geo Map, Countries and Autonomous
Systems pages all vanish if the geolocation database is missing**
(menu_definition.lua) — a fresh install without the geolocation data package
simply has no world map and no country page. Per-flow storage settings are buried
in an expert-view preferences pane, and the historical explorer admits it "does
not provide all the analysis features which are available for live traffic".
**And the geo map's fallback of pinning un-geolocatable hosts to one fixed
coordinate is a genuine visual lie** for a LAN full of private addresses — a
mistake `opnview` must not repeat, and the reason the catalogue's map widget
carries an explicit unresolved bucket instead. Note finally that ntopng 7.0
rebuilt the frontend with new chart libraries including a geomap
(https://www.ntop.org/welcome-to-ntopng-7-0-modern-gui-nassistant-sites-observability-bgp-bmp-pqc-wazuh/),
so some of the dense-UI criticism above may be dated.

### Zenarmor

The closest neighbour of all: a reporting module that lives **inside the
OPNsense web interface**, answering many of the same questions from the same
box.

#### Outbound traffic

Two surfaces: a node **Dashboard** and **Reports / Live Session Explorer**. The
OPNsense dashboard is explicitly a *current-day*, near-real-time view —
interface throughput from "the last 20 throughput values retrieved from the
node", three pies and service health
(https://www.zenarmor.com/docs/opnsense/viewing-node-status/dashboard). History
lives in Reports, whose time selector offers 30 minutes through a week plus a
custom range, with a reporting size of top 5 to top 100 per chart
(https://www.zenarmor.com/docs/opnsense/reporting-analytics/report-view-configuration).
Retention is a configurable reporting period, **default 7 days**; shrinking it
deletes older data after a confirmation prompt
(https://www.zenarmor.com/docs/opnsense/configuring/setting-store-duration-of-reporting-data).
The backend is pluggable
(https://www.zenarmor.com/docs/opnsense/configuring/configuring-reporting-database-backend).
`UNVERIFIED:` a search summary of the database-management page states that the
recommended period is about seven days on one backend but only two to three on
another
(https://www.zenarmor.com/docs/configuring/managing-reporting-database) — not
confirmable from the page body. **On a free-edition retention cap specifically,
none could be verified**: the edition-comparison pages describe the free tier as
"limited to a few preset reporting options" but state **no retention figure at
all**
(https://www.zenarmor.com/docs/guides/zenarmor-free-vs-home-editions,
https://www.zenarmor.com/docs/introduction/zenarmor-editions).

#### World map of destinations

**No world map on the OPNsense node dashboard** — the documented panels are
throughput, three pies and system health (dashboard page). Geography appears as
*charts* inside report views: the Connections view has a "Top Destination
Locations Heatmap" and the Threats view a "Top Countries" chart
(https://www.zenarmor.com/docs/opnsense/reporting-analytics/report-view). A
genuine interactive map exists **only in the cloud portal**: an organisation
dashboard plotting detected threats as clustered circles with a count inside —
not a choropleth, not arcs — which split into points on zoom, with a side panel
giving region name and threat count
(https://www.zenarmor.com/docs/organization-management/accessing-organization-dashboard).
Whether policy-blocked, non-threat sessions appear on it is **not stated**; the
documentation says only "detected threats". Honest verdict: **Zenarmor's map is
a threat map, not a destination map**, and the destination geography you would
actually want is a flat heatmap chart.

#### Per-device view

Per-host is first-class: report views segment by top devices, top device
categories and top egress and ingress users, and the Blocks view carries
"Blocked Local Hosts and Reasons" and "Blocked Local Hosts Over Time"
(report-view page). `UNVERIFIED:` host identity is enriched by reverse DNS, by
real-time multicast-name harvesting, and by an auto-classifying device inventory
(search summaries of
https://www.zenarmor.com/docs/configuring/configuring-dns-for-reports and
https://www.zenarmor.com/docs/devices/device-identification-overview). Note that
**reverse-DNS lookup for reports is a paid feature**, so on the free tier the
per-host charts largely read as bare addresses (free-versus-home page);
`UNVERIFIED:` a community post reports exactly that symptom
(https://help.zenarmor.com/hc/en-us/community/posts/33820508772627-Why-Top-Local-Hosts-show-IPs-vs-predefined-hostnames
— community).

#### Blocked domain and what blocked it

**Partially, at policy and category level.** The Blocks tab of the Live Session
Explorer "provides you with viewing the details of the blocked connections in
your network according to your policy rules", and its documented per-session
fields include a **block message** carrying subcategory information, a **block
category**, a **block signature**, and the **policy** name and details,
alongside hostnames, addresses, ports, protocol, device, user, application and
interface
(https://www.zenarmor.com/docs/opnsense/reporting-analytics/live-session-explorer).
So a blocked session names **which policy** matched and **which category** was
hit. `UNVERIFIED:` a search summary of the reports-overview page suggests the
reason field reads as a category access statement
(https://www.zenarmor.com/docs/opnsense/reporting-analytics/reports-overview).

**What the documentation does not establish is the thing you most want: it does
not say the detail pane names the specific *rule* within the policy, nor the
specific threat-intelligence feed or blocklist that supplied the verdict.** The
aggregate equivalent is the "Blocked Local Hosts and Reasons" chart. Net:
Zenarmor answers "which policy, which category" and leaves "which rule, from
which feed" ambiguous in public documentation — **the clearest gap for `opnview`
to beat**, and the item most worth confirming by hands-on installation.

![Zenarmor's blocks report: host against reason](ui-references/screenshots/familyb-zenarmor-blocks-report.png)
*Source: the Zenarmor documentation's own product screenshots at
`zenarmor.com/docs`; captured 2026-09-22.* "Blocked Local Hosts and Reasons" as
stacked bars, a top-blocks doughnut with named reasons, a blocked-conversations
heatmap, blocked-over-time lines and an interface and VLAN split. What is worth
taking: **blocking presented as host × reason rather than as a flat event list**
— the stacked bar per host answers "who is generating all this" and "what kind
of thing is it" in one mark, and the reason vocabulary is a model for
`opnview`'s four engine kinds.

#### Per-VLAN or per-subnet traffic

Every report view can be broken down by **interfaces and VLANs** and by
**policies** (report-view page). `UNVERIFIED:` documented best practice is to
select the **physical parent interface** rather than VLAN sub-interfaces,
because the parent already inspects all sub-interfaces and selecting both causes
duplicated packet processing *and duplicated reporting* (search summary of
https://www.zenarmor.com/docs/guides/best-practices-for-zenarmor-deployment) — a
real caution, and a direct warning that naive per-VLAN counting double-counts.
`UNVERIFIED:` each protected interface can be tagged with a security zone used
as a grouping key (same source), and deployment modes include a passive,
reporting-only mode
(https://www.zenarmor.com/docs/guides/deployment-modes) directly analogous to
`opnview`'s read-only posture. Exempted VLANs and networks can be declared to
keep them out of inspection
(https://www.zenarmor.com/docs/configuring/configuring-exempted-vlans-and-networks-on-zenconsole).

#### What it gets right

**The block record is a structured tuple, not a log line**: policy name, block
category, block message, block signature, application and application category,
all attached to the same row as the ordinary connection fields — so "what was
blocked" and "why" are one click apart rather than two systems apart. Second,
**click-to-filter propagation**: selecting an item in any chart applies that
value as a filter to *every* chart in the view simultaneously, with several
operators, and the filter set can be saved or reset (reports-overview page).
That is a genuinely good answer to the problem a canvas of widgets creates —
one selection, one coordinated view.

![Zenarmor reporting inside the OPNsense chrome](ui-references/screenshots/familyb-zenarmor-reports-live-sessions.png)
*Source: the Zenarmor documentation's own product screenshots; captured
2026-09-22.* The full OPNsense window with Zenarmor's own menu inside it, and a
Connections report of six doughnuts with a hover "Filter | Exclude" control and a
time-range and download toolbar. What is worth taking: this is the reference for
what a reporting module looks like **inside** OPNsense — and the hover
Filter/Exclude on any legend entry is the interaction to copy, because it turns
every chart into a query builder without adding a form.

#### What it gets wrong, and where it buries a view

**The best view is buried deepest.** The interactive map is **not on the node
dashboard at all** — it exists only at the cloud portal's organisation
dashboard, requiring registration and a cloud dependency **to see a map of your
own traffic**. On the box, geography is demoted to a chart panel inside
*Reports → Connections → Top Destination Locations Heatmap*, and blocked-session
forensics sits at *Live Sessions → Blocks → magnifying-glass → session details*
— three levels below anything a dashboard shows. Feature gating compounds it:
the free tier has a few preset reports, no custom reports, no export and **no
reverse DNS**, so per-host views degrade to raw addresses. And the dashboard's
hard scoping to "the current day" with a fixed twenty-sample throughput strip
means **the landing page can never answer a question about yesterday**.

### Cloudflare Radar

Included not as a competitor but as the best public example of a
network-visibility interface that is honest about what it cannot see — which is
`opnview`'s own central problem. It is also, per *The maintainer's recorded
preferences*, the general-layout reference.

#### Outbound traffic

Radar is an aggregate view of the Internet as Cloudflare sees it, not of
anyone's egress. It is built on NetFlows, HTTP requests and DNS queries to a
public resolver, with third-party data for AS metadata and population estimates
(https://developers.cloudflare.com/radar/investigate/,
https://developers.cloudflare.com/radar/investigate/netflows/,
https://developers.cloudflare.com/radar/glossary/). Crucially, Radar **almost
never shows raw volume**: values are percentages or min-max normalised series,
with the applied method **declared in the response payload**
(https://developers.cloudflare.com/radar/concepts/normalization/). Time ranges
are shortcuts, each paired with a "control" variant covering the immediately
preceding period, so this period and the previous one are drawn on the same axis
(https://developers.cloudflare.com/radar/get-started/making-comparisons/).

#### World map of destinations

Radar runs **several distinct maps with different marks, which is itself the
lesson.** The **Outage Center** map plots detected anomalies as circles,
double-bordered where a circle aggregates multiple countries, resolving into
per-country counts on zoom, with **a timeline below the map** you can hover to
read the outage a dot belongs to
(https://blog.cloudflare.com/traffic-anomalies-notifications-radar/). The
accompanying table carries type, entity, start time, duration and a
**verification status**, where "verified" means corroborated across multiple
datasets, and unverified entries are openly flagged as possible false positives
or collection artefacts. The **security and attack maps** are a different animal:
a near-real-time **directional arc map** drawing attack lines both ways, plus
Sankey diagrams of country-to-country attack flow, with an explicit caveat that
the geolocated device is not necessarily where the attacker is
(https://blog.cloudflare.com/attack-maps-now-available-on-radar/). On blocked
traffic: Radar's attack maps *are* the mitigated traffic — blocked is the whole
dataset, not an overlay
(https://developers.cloudflare.com/radar/investigate/application-layer-attacks/).

![The Cloudflare Radar worldwide overview](ui-references/screenshots/demo-cloudflare-radar-overview.jpg)
*Captured live from `https://radar.cloudflare.com/`, 2026-09-22. Radar is itself
public, so this is the live product rather than a published image.* A left
navigation rail, a scoped header with a location selector and a period selector,
then a wide traffic-trends chart with its previous-period line drawn alongside,
a protocol share gauge, and a row of three explained panels beneath. **The
maintainer has named this composition his general-layout preference**, for its
angular cards; see *The maintainer's recorded preferences*. What is worth
taking: **every panel carries a one-line explanation of what it is**, and the
comparison against the previous period is drawn on the chart by default rather
than being an option — so "is this normal?" is answered without the user asking.

#### Per-device view

**Not applicable — Radar has no device concept**, because it observes the
Internet rather than a network. The analogues are entity pages: AS pages giving
traffic trends for one autonomous system, location pages leading with normalised
traffic for a country or AS, and domain pages ranked by **the size of the user
population looking a domain up** rather than by request count
(https://developers.cloudflare.com/radar/glossary/,
https://developers.cloudflare.com/radar/investigate/domain-ranking-datasets/,
https://blog.cloudflare.com/radar-domain-rankings/). That ranking choice is
worth noting for `opnview`'s top-sites widget: **ranking by how many distinct
devices reached a site tells a different and often more useful story than
ranking by request count**, and the catalogue's entry offers both.

#### Blocked domain and what blocked it

**Largely not applicable for Radar itself**: it publishes what was attacked and
mitigated in aggregate, never "this domain was blocked by this list for this
user" (application-layer-attacks page). The named-attribution behaviour lives in
a *different* Cloudflare product, and is worth copying from there: when a
category selector causes a DNS block, the response carries an **extended DNS
error code together with a field holding an array of the matched categories** —
the block is self-describing **on the wire**, not only in a log
(https://developers.cloudflare.com/cloudflare-one/traffic-policies/dns-policies/,
https://developers.cloudflare.com/cloudflare-one/traffic-policies/domain-categories/).
Activity logs name the matching policy, and global policies are **prefixed so a
row states where the rule came from**
(https://developers.cloudflare.com/cloudflare-one/insights/logs/gateway-logs/,
https://developers.cloudflare.com/cloudflare-one/traffic-policies/global-policies/).
That prefix convention — the displayed rule name encoding its own provenance —
is a cheap, directly copyable idea for `opnview`'s unified blocked feed.

#### Per-VLAN or per-subnet traffic

**Not applicable.** Radar has no notion of a VLAN, subnet, zone or internal
network; its smallest units are the autonomous system and the location
(glossary page), with anomaly detection scoped to a location or an AS
(traffic-anomalies post). There is no internal-topology dimension to borrow —
which is precisely why it is in this document as a contrast rather than as a
model.

#### What it gets right

**Every comparison is drawn against its own control period by construction**, so
"is this normal?" is answered on the same chart instead of from memory. Second,
and most important here, **Radar renders its own uncertainty**: the Outage
Center marks each anomaly verified or unverified, names the corroborating
third-party dataset when there is one, and states plainly that unverified rows
may be false positives or collection artefacts; the attack maps carry the
geolocation caveat on the page rather than in a footnote. Third, normalisation is
**declared in the payload**, so a chart can always say what transform it
applied — the natural analogue for `opnview`'s stated observation-point limit and
its inferred site names.

![Cloudflare Radar's loading skeletons](ui-references/screenshots/moodboard-empty-cloudflare-radar-skeleton.jpg)
*Captured live from `https://radar.cloudflare.com/traffic`, 2026-09-22, during
load.* The same panels as above, each showing a grey placeholder in the exact
shape of the content that is coming. What is worth taking: **a panel that has no
data yet keeps its size and its title**, so the page does not reflow when the
data lands — directly applicable to `opnview`'s empty states, which must occupy
the widget's full footprint rather than collapsing it.

#### What it gets wrong, and where it buries a view

The central limitation is structural and is worth stating in `opnview`'s own
interface as a contrast: **Radar cannot tell you anything about your own
network** — no host, VLAN, device or user — and because it is normalised it
usually cannot even give an absolute byte count. That normalisation is a genuine
usability trap: the documentation has to warn that the two lines of one chart are
normalised **independently** and their relative sizes are therefore not
comparable, and that cross-series comparison is only valid when the series are
requested together (making-comparisons page) — **a chart whose caveat lives in
developer documentation rather than on the chart**. Visibility is bounded by
vantage point: an "Internet outage" is really a drop in traffic Cloudflare can
see (https://blog.cloudflare.com/detecting-internet-outages/). On burying: the
attack maps are documented as living "on Radar" with **no navigation path stated
anywhere in the announcement**, and the Outage Center was introduced as a
separate destination rather than as a layer on the traffic pages
(https://blog.cloudflare.com/announcing-cloudflare-radar-outage-center/) — so
"is something broken right now?" and "what does traffic look like?" sit in two
different places.

## Family C — the visual language

This section **records options; it does not choose one.** Naming `opnview`'s own
semantic tokens, picking an accent, choosing typography and iconography and
deciding the final look are design decisions belonging to the mockup cycle. Every
colour value below is a **record of an external source, reproduced so the
document remains checkable** — none of it is a stylesheet for `opnview`, and no
rule here is intended for `opnview`'s own CSS.

**The language these palettes share is angular, dense and high-contrast.** The
industrial palette's corner radius is `0.25rem` — four pixels at a default root
size, which reads as a chamfer rather than a curve. Its light background is a
near-white grey rather than white, its dark background a near-black grey rather
than black, and its text sits at the far end of the contrast range in both. The
five named palettes are, without exception, developer colour schemes designed
for dense text on a dark ground. **This is not the rounded, light, airy UniFi
aesthetic that `ROADMAP.md` step 3 specifies**, and the two cannot both be the
reference. That contradiction is recorded here and carried into *Proposed
ROADMAP amendment*; the maintainer has since resolved it in favour of the
angular language (see *The maintainer's recorded preferences*), but this section
still records rather than decides.

### The industrial palette

Source: `C:\Users\fuzzz\Downloads\maestro-espidf-components-main\maestro-espidf-components-main\tb_http_server\www\style.css`,
an absolute path outside this repository and outside the container bind mount.
*Scope and method* explains why it is cited that way and why every property is
reproduced rather than referenced. The file declares its own intent in two
comments: "Industrial high-contrast palette" for the light set and "Industrial
low-light high-contrast palette" for the dark set.

**Non-colour properties.** These are declared once, on `:root`, and are not
overridden in the dark block.

| Custom property | Value |
|---|---|
| `--font-family` | `-apple-system, BlinkMacSystemFont, "Segoe UI", "Roboto", "Oxygen", "Ubuntu", "Cantarell", "Fira Sans", "Droid Sans", "Helvetica Neue", Arial, sans-serif` |
| `--font-mono` | `"SF Mono", "Monaco", "Inconsolata", "Fira Mono", "Droid Sans Mono", "Source Code Pro", "Consolas", "Liberation Mono", monospace` |
| `--line-height` | `1.6` |
| `--font-weight` | `500` |
| `--font-weight-bold` | `700` |
| `--font-size-base` | `1rem` |
| `--spacing` | `1rem` |
| `--spacing-sm` | `0.5rem` |
| `--spacing-md` | `1rem` |
| `--spacing-lg` | `2rem` |
| **`--border-radius`** | **`0.25rem`** |
| `--border-width` | `1px` |
| `--border-width-thick` | `0.125rem` |

Note the font stack: **entirely system fonts**, with no web font anywhere. That
is not incidental — it is exactly what `ROADMAP.md`'s "no resource loaded from a
CDN" rule requires, and it means the industrial palette can be adopted whole
without adding a single byte of downloaded typography. The body weight of `500`
rather than `400` is the other deliberate choice: the file's own comment calls
it "Industrial bold system fonts for maximum readability".

**Colour and effect properties**, light and dark. Every property in the file is
listed; the dark column is the value from the `@media (prefers-color-scheme:
dark)` block, and a value identical in both is shown in both columns rather than
elided, so the table can be diffed against the source mechanically.

| Custom property | Light | Dark |
|---|---|---|
| `--bg-primary` | `#f8f9fa` | `#1c1c1e` |
| `--bg-secondary` | `#e9ecef` | `#2c2c2e` |
| `--text-primary` | `#212529` | `#f5f5f7` |
| `--text-secondary` | `#495057` | `#d1d1d6` |
| `--text-muted` | `#6c757d` | `#8e8e93` |
| `--border-color` | `#ced4da` | `#48484a` |
| `--input-bg` | `#ffffff` | `#2c2c2e` |
| `--input-border` | `#adb5bd` | `#636366` |
| `--button-bg` | `#2c5f8d` | `#3a7ca5` |
| `--button-hover-bg` | `#1e4164` | `#5a9cc8` |
| `--button-text` | `#ffffff` | `#ffffff` |
| `--button-color` | `#ffffff` | `#ffffff` |
| `--link-color` | `#2c5f8d` | `#5a9cc8` |
| `--link-hover` | `#1e4164` | `#7eb8db` |
| `--accent-focus` | `rgba(44, 95, 141, 0.25)` | `rgba(58, 124, 165, 0.35)` |
| `--status-success` | `#28a745` | `#30d158` |
| `--status-error` | `#dc3545` | `#ff453a` |
| `--status-warning` | `#fd7e14` | `#ff9f0a` |
| `--status-error-dark` | `#c0392b` | `#c0392b` |
| `--section-header-bg` | `#495057` | `#5a8fab` |
| `--section-header-border` | `#1e4164` | `#3a7ca5` |
| `--on-accent` | `#ffffff` | `#ffffff` |
| `--shadow-inset` | `rgba(0, 0, 0, 0.06)` | `rgba(0, 0, 0, 0.5)` |
| `--shadow-md` | `rgba(0, 0, 0, 0.15)` | `rgba(0, 0, 0, 0.35)` |
| `--shadow-lg` | `rgba(0, 0, 0, 0.3)` | `rgba(0, 0, 0, 0.45)` |
| `--shadow-lg-hover` | `rgba(0, 0, 0, 0.2)` | `rgba(0, 0, 0, 0.55)` |
| `--overlay-bg` | `rgba(0, 0, 0, 0.7)` | `rgba(0, 0, 0, 0.75)` |
| `--modal-surface-overlay` | `rgba(0, 0, 0, 0.2)` | `rgba(0, 0, 0, 0.35)` |
| `--spinner-border` | `rgba(255, 255, 255, 0.3)` | `rgba(255, 255, 255, 0.35)` |
| `--pico-red` | `#ee402e` | `#ee402e` |
| `--pico-green` | `#62af9a` | `#62af9a` |
| `--pico-orange` | `#f0a844` | `#f0a844` |
| `--ota-gradient-start` | `#667eea` | `#667eea` |
| `--ota-gradient-end` | `#764ba2` | `#764ba2` |
| `--ota-progress-bg` | `#e3f2fd` | `rgba(227, 242, 253, 0.18)` |
| `--counter-safe` | `#666666` | `#a1a1aa` |
| `--counter-warn` | `#f59e0b` | `#fbbf24` |
| `--counter-critical` | `#ef4444` | `#f87171` |
| `--notice-info` | `#3b82f6` | `#60a5fa` |
| `--notice-success` | `#10b981` | `#34d399` |
| `--notice-error` | `#ef4444` | `#f87171` |
| `--notice-info-bg` | `rgba(59, 130, 246, 0.1)` | `rgba(96, 165, 250, 0.12)` |
| `--notice-success-bg` | `rgba(16, 185, 129, 0.1)` | `rgba(52, 211, 153, 0.12)` |
| `--notice-error-bg` | `rgba(239, 68, 68, 0.1)` | `rgba(248, 113, 113, 0.12)` |
| `--modal-surface` | `#f2f4f6` | `#1f2328` |
| `--modal-surface-alt` | `#ffffff` | `#24282f` |
| `--modal-row-bg` | `#ffffff` | `#262b33` |
| `--modal-row-border` | `#d8dee4` | `#3a414c` |
| `--button-secondary-bg` | `#f1f3f5` | `#2b313a` |
| `--button-secondary-text` | `#334155` | `#e5e7eb` |
| `--button-secondary-border` | `#cbd5e1` | `#3a414c` |
| `--button-cancel-bg` | `#f8f9fb` | `#232831` |
| `--button-cancel-text` | `#475569` | `#d1d5db` |
| `--button-cancel-border` | `#d7dee7` | `#3a414c` |
| `--button-cancel-hover-bg` | `#eef2f6` | `#2b313a` |
| `--button-cancel-hover-text` | `#334155` | `#f3f4f6` |
| `--log-level-warn-text` | `#111111` | `#111111` |
| `--log-level-debug-bg` | `#6b7280` | `#6b7280` |
| `--log-level-raw-bg` | `#64748b` | `#64748b` |
| `--log-highlight-bg` | `rgba(255, 193, 7, 0.35)` | `rgba(255, 193, 7, 0.35)` |
| `--sse-connected-color` | `#4caf50` | `#66bb6a` |
| `--sse-connected-shadow` | `rgba(76, 175, 80, 0.6)` | `rgba(102, 187, 106, 0.6)` |
| `--sse-disconnected-color` | `#f44336` | `#ef5350` |
| `--sse-disconnected-shadow` | `rgba(244, 67, 54, 0.6)` | `rgba(239, 83, 80, 0.6)` |

**The semantic status colours**, separated out because they are the ones a
network tool uses constantly and the ones a later cycle will have to map onto
`opnview`'s own vocabulary:

| Meaning | Light | Dark |
|---|---|---|
| Success / healthy | `--status-success` `#28a745` | `#30d158` |
| Error / blocked | `--status-error` `#dc3545` | `#ff453a` |
| Warning / degraded | `--status-warning` `#fd7e14` | `#ff9f0a` |
| Severe error | `--status-error-dark` `#c0392b` | `#c0392b` |
| Counter, safe | `--counter-safe` `#666666` | `#a1a1aa` |
| Counter, warning | `--counter-warn` `#f59e0b` | `#fbbf24` |
| Counter, critical | `--counter-critical` `#ef4444` | `#f87171` |
| Notice, informational | `--notice-info` `#3b82f6` | `#60a5fa` |
| Notice, success | `--notice-success` `#10b981` | `#34d399` |
| Notice, error | `--notice-error` `#ef4444` | `#f87171` |
| Live connection up | `--sse-connected-color` `#4caf50` | `#66bb6a` |
| Live connection down | `--sse-disconnected-color` `#f44336` | `#ef5350` |

Two observations, recorded as observations rather than as recommendations.
First, the palette has **three overlapping red vocabularies** (`--status-error`,
`--counter-critical`, `--notice-error`, plus `--pico-red`) and **three greens**;
whatever `opnview` adopts will have to pick one per meaning rather than carrying
all of them. Second, the file uses the OS-preference media query and **has no
manual override mechanism at all** — it follows the operating system and offers
the user no switch. `opnview`'s settled decision 5 is *follow the OS on first
launch, user-overridable afterwards*, which is a superset: the same default,
plus a control this file does not have.

**Layout: navigation bar, sidebar, main.** The file's own layout, reproduced as
a record because it is the shape the palette was designed for.

| Element | Rules, verbatim in substance |
|---|---|
| `nav` | `position: fixed; top: 0; left: 0; right: 0; height: 60px; z-index: 200; background-color: var(--bg-secondary); padding: var(--spacing-sm) var(--spacing); border-bottom: 1px solid var(--border-color)` |
| `.nav-wrapper` | `display: flex; align-items: center; justify-content: flex-start; gap: var(--spacing); height: 100%` |
| `.page-layout` | `display: flex; min-height: calc(100vh - 60px)` |
| `.sidebar` | `width: 200px; position: fixed; left: 0; top: 60px; height: calc(100vh - 60px); overflow-y: auto; z-index: 100; background-color: var(--bg-secondary); border-right: 1px solid var(--border-color); padding: var(--spacing); display: flex; flex-direction: column; gap: var(--spacing-sm); transition: transform 0.3s ease` |
| `.sidebar.hidden` | `transform: translateX(-100%)` |
| `.sidebar-button` | `background-color: var(--bg-primary); border: 1px solid var(--border-color); color: var(--text-primary); padding: var(--spacing); border-radius: var(--border-radius); text-align: left; width: 100%` — and, when `.active`, the button background and border become `var(--button-bg)` with `var(--button-text)` at `var(--font-weight-bold)` |
| `.main-content` | `flex: 1; margin-left: 200px; padding: var(--spacing-lg) var(--spacing); max-width: 100%; transition: margin-left 0.3s ease` |
| `.main-content > *` | `max-width: 1200px; margin-left: auto; margin-right: auto` |
| `.main-content.expanded` | `margin-left: 0` |
| Headings | `font-weight: var(--font-weight-bold); line-height: 1.2`, sized `h1: 2rem` down to `h6: 1rem` |

The shape is: **a fixed 60-pixel bar across the top, a collapsible 200-pixel
sidebar beneath it on the left, and a main column whose content is capped at
1200 pixels and centred.** Both the bar and the sidebar sit on `--bg-secondary`,
so the page reads as a lighter content area inset into a slightly darker frame —
which is the same structural idea as Cloudflare Radar's left rail and scoped
header, and is why the maintainer's two stated preferences are consistent with
each other rather than in tension.

### The five named palettes

**Which source governs.** `opnview` takes these five palettes from the
maintainer's own site, **https://lequellec.xyz**, where they are already
implemented as a seven-token set per palette — `--bg`, `--panel`, `--fg`,
`--fg-strong`, `--fg-muted`, `--accent`, `--line` — in a light and a dark
variant. Those are the values the product uses, and they are authoritative.

The upstream tables that follow are **not** those values. They are the record of
where each name comes from, kept because a palette's own project is the right
place to check a name, a role or a disputed hex. They are reference, not
implementation. The two differ — the site's Tokyo Night puts `#16161e` at the
ground and `#1a1b26` at the surface, where the upstream tables invite the
opposite — and substituting the upstream values produced a washed-out rendering
that the maintainer caught and rejected. Do not take these tables as the
palette.

#### The values `opnview` uses

Read from the maintainer's site. Seven tokens carry the interface; `--vif` is
the site's accent-of-last-resort and is recorded with them because the mockup
uses it for the one series that must not be mistaken for another.

| Palette | Mode | `--bg` | `--panel` | `--fg` | `--fg-strong` | `--fg-muted` | `--accent` | `--line` | `--vif` |
|---|---|---|---|---|---|---|---|---|---|
| Tokyo Night | dark | `#16161e` | `#1a1b26` | `#c0caf5` | `#e6ebff` | `#8b93ba` | `#7aa2f7` | `#2a2e42` | `#f7768e` |
| Tokyo Night | light | `#e1e2e7` | `#d8d9df` | `#343b58` | `#1c2033` | `#565a6e` | `#2a54c0` | `#bcbec9` | `#8c1f4f` |
| Dracula | dark | `#21222c` | `#282a36` | `#f8f8f2` | `#ffffff` | `#a9aed0` | `#bd93f9` | `#3b3d4d` | `#ff79c6` |
| Dracula | light | `#fffbeb` | `#f6f0d8` | `#2a2823` | `#1f1f1f` | `#5f5940` | `#5a3fc0` | `#ddd6bd` | `#a3144d` |
| Nord | dark | `#2e3440` | `#3b4252` | `#d8dee9` | `#eceff4` | `#a9b4c6` | `#88c0d0` | `#4c566a` | `#c9a3c4` |
| Nord | light | `#eceff4` | `#e5e9f0` | `#3b4252` | `#2e3440` | `#4c566a` | `#2f5f88` | `#d0d6e0` | `#94357f` |
| Rosé Pine | dark | `#191724` | `#1f1d2e` | `#e0def4` | `#f2f0ff` | `#a9a4c9` | `#c4a7e7` | `#302c48` | `#eb6f92` |
| Rosé Pine | light | `#faf4ed` | `#f2e9e1` | `#575279` | `#3d3a55` | `#68647a` | `#7a4d94` | `#dfd6cd` | `#a03a58` |
| Catppuccin | dark | `#181825` | `#1e1e2e` | `#cdd6f4` | `#eff1f5` | `#a6adc8` | `#cba6f7` | `#313244` | `#f38ba8` |
| Catppuccin | light | `#eff1f5` | `#e6e9ef` | `#4c4f69` | `#3a3c52` | `#5c5f77` | `#7c2fd4` | `#ccd0da` | `#a02a45` |

Two consequences, both of which contradict something written elsewhere before
this table existed and both of which this table settles:

- **Ground and surface are ordered.** `--bg` is the page, `--panel` is the card,
  and `--bg` is the darker of the two in every dark variant. Inverting them is
  what produced the rejected rendering.
- **Nord has a light variant here.** Upstream Nord publishes none, and the
  earlier text therefore specified a fallback for a light-preferring system with
  Nord selected. The maintainer's site defines Nord light, so no fallback is
  needed and none should be built.

Each upstream entry below is at its official published values, with at least one
URL to the palette's own source. Where a value could only be read from a
generated artefact rather than a hand-authored one, that is stated. **These are
records of external sources, reproduced so `opnview` never has to fetch anything
at runtime** — which is the whole reason for reproducing them rather than
linking them.

#### Dracula

Sources: the official specification at https://spec.draculatheme.com/ and the
palette table in the canonical repository's README at
https://raw.githubusercontent.com/dracula/dracula-theme/master/README.md. The
two agree. The scheme named "Dracula" is **dark-only**; the project also
publishes **Alucard**, an official light counterpart, in the same README table.
Dracula publishes **no machine-readable palette file** — the palette is a
Markdown table and prose.

| Role | Dracula (dark) | Alucard (light) |
|---|---|---|
| Background | `#282a36` | `#fffbeb` |
| Current Line | `#44475a` | `#6c664b` |
| Selection | `#44475a` | `#cfcfde` |
| Foreground | `#f8f8f2` | `#1f1f1f` |
| Comment | `#6272a4` | `#6c664b` |
| Cyan | `#8be9fd` | `#036a96` |
| Green | `#50fa7b` | `#14710a` |
| Orange | `#ffb86c` | `#a34d14` |
| Pink | `#ff79c6` | `#a3144d` |
| Purple | `#bd93f9` | `#644ac9` |
| Red | `#ff5555` | `#cb3a2a` |
| Yellow | `#f1fa8c` | `#846e15` |

Note, because it is the most common transcription error in circulation: **Current
Line and Selection are the same value, `#44475a`**, and `#6272a4` is Comment,
not Current Line.

#### Nord

Sources: the official documentation at
https://www.nordtheme.com/docs/colors-and-palettes and the canonical style-guide
file at https://raw.githubusercontent.com/nordtheme/nord/develop/src/nord.css,
which carries the same sixteen values as annotated CSS custom properties. Nord
is **dark-only**: there is one palette and no official light variant — Snow
Storm is the *text* group, not a light theme. There is **no JSON or YAML
definition**; the machine-readable forms are CSS, Less, Sass and Stylus in the
same source directory. `nord8` is designated the primary accent.

| Group | Token | Value |
|---|---|---|
| Polar Night | `nord0` | `#2e3440` |
| | `nord1` | `#3b4252` |
| | `nord2` | `#434c5e` |
| | `nord3` | `#4c566a` |
| Snow Storm | `nord4` | `#d8dee9` |
| | `nord5` | `#e5e9f0` |
| | `nord6` | `#eceff4` |
| Frost | `nord7` | `#8fbcbb` |
| | `nord8` | `#88c0d0` |
| | `nord9` | `#81a1c1` |
| | `nord10` | `#5e81ac` |
| Aurora | `nord11` | `#bf616a` |
| | `nord12` | `#d08770` |
| | `nord13` | `#ebcb8b` |
| | `nord14` | `#a3be8c` |
| | `nord15` | `#b48ead` |

#### Tokyo Night

**Stated honestly: Tokyo Night has no published style-guide page.** Its
canonical definition is the colour files in the `folke/tokyonight.nvim`
repository — specifically
https://raw.githubusercontent.com/folke/tokyonight.nvim/main/lua/tokyonight/colors/storm.lua,
which holds the base table and every literal value;
https://raw.githubusercontent.com/folke/tokyonight.nvim/main/lua/tokyonight/colors/night.lua,
which deep-copies Storm and overrides **only three background keys**;
https://raw.githubusercontent.com/folke/tokyonight.nvim/main/lua/tokyonight/colors/moon.lua,
a full literal table of its own; and
https://raw.githubusercontent.com/folke/tokyonight.nvim/main/lua/tokyonight/colors/day.lua,
which is **not a literal table at all** but a function inverting the Night
palette programmatically. Day's concrete values therefore exist only in
generated output, and the values below were read from the repository's own
generated artefact,
https://raw.githubusercontent.com/folke/tokyonight.nvim/main/extras/lua/tokyonight_day.lua
— still first-party, but generated rather than hand-authored, and flagged as
such. The derived colour assembly lives in
https://raw.githubusercontent.com/folke/tokyonight.nvim/main/lua/tokyonight/colors/init.lua.

Tokyo Night ships **both**: Night, Storm and Moon are dark, and **Day is the
official light variant**. There is **no JSON or YAML palette file**; the Lua
tables are the definition, with generated exports for many targets alongside.

| Key | Storm | Night | Moon | Day (light) |
|---|---|---|---|---|
| `bg` | `#24283b` | `#1a1b26` | `#222436` | `#e1e2e7` |
| `bg_dark` | `#1f2335` | `#16161e` | `#1e2030` | `#d0d5e3` |
| `bg_dark1` | `#1b1e2d` | `#0C0E14` | `#191B29` | `#c1c9df` |
| `bg_highlight` | `#292e42` | `#292e42` | `#2f334d` | `#c4c8da` |
| `fg` | `#c0caf5` | `#c0caf5` | `#c8d3f5` | `#3760bf` |
| `fg_dark` | `#a9b1d6` | `#a9b1d6` | `#828bb8` | `#6172b0` |
| `fg_gutter` | `#3b4261` | `#3b4261` | `#3b4261` | `#a8aecb` |
| `comment` | `#565f89` | `#565f89` | `#636da6` | `#848cb5` |
| `blue` | `#7aa2f7` | `#7aa2f7` | `#82aaff` | `#2e7de9` |
| `cyan` | `#7dcfff` | `#7dcfff` | `#86e1fc` | `#007197` |
| `green` | `#9ece6a` | `#9ece6a` | `#c3e88d` | `#587539` |
| `teal` | `#1abc9c` | `#1abc9c` | `#4fd6be` | `#118c74` |
| `magenta` | `#bb9af7` | `#bb9af7` | `#c099ff` | `#9854f1` |
| `purple` | `#9d7cd8` | `#9d7cd8` | `#fca7ea` | `#7847bd` |
| `orange` | `#ff9e64` | `#ff9e64` | `#ff966c` | `#b15c00` |
| `red` | `#f7768e` | `#f7768e` | `#ff757f` | `#f52a65` |
| `red1` (error) | `#db4b4b` | `#db4b4b` | `#c53b53` | `#c64343` |
| `yellow` | `#e0af68` | `#e0af68` | `#ffc777` | `#8c6c3e` |
| `terminal_black` | `#414868` | `#414868` | `#444a73` | `#b4b5b9` |

**Night equals Storm in every key except the three background keys.** That is the
single most important fact when reproducing these, because "Tokyo Night"
colloquially means the Night variant, whose accent colours are literally
Storm's. `UNVERIFIED:` Day's `yellow` value `#8c6c3e` was read from the rainbow
array of the generated Day artefact rather than from a top-level key in a
hand-authored file; every other value in the table came from a literal table or
its direct generated equivalent.

#### Rosé Pine

Sources: the hand-authored definition at
https://raw.githubusercontent.com/rose-pine/palette/main/source/index.ts and the
generated output carrying all three variants at
https://raw.githubusercontent.com/rose-pine/palette/main/dist/css/rose-pine.css,
with the official palette page at https://rosepinetheme.com/palette/. Rosé Pine
ships **both**: Rosé Pine and Rosé Pine Moon are dark, and **Rosé Pine Dawn is
the official light variant**. It **does** publish machine-readable definitions —
JSON, YAML, TOML, CSS and a Tailwind form — at
https://github.com/rose-pine/palette/tree/main/dist/json and its siblings.

| Role | Rosé Pine (dark) | Moon (dark) | Dawn (light) |
|---|---|---|---|
| base | `#191724` | `#232136` | `#faf4ed` |
| surface | `#1f1d2e` | `#2a273f` | `#fffaf3` |
| overlay | `#26233a` | `#393552` | `#f2e9e1` |
| muted | `#6e6a86` | `#6e6a86` | `#9893a5` |
| subtle | `#908caa` | `#908caa` | `#797593` |
| text | `#e0def4` | `#e0def4` | `#575279` |
| love | `#eb6f92` | `#eb6f92` | `#b4637a` |
| gold | `#f6c177` | `#f6c177` | `#ea9d34` |
| rose | `#ebbcba` | `#ea9a97` | `#d7827e` |
| pine | `#31748f` | `#3e8fb0` | `#286983` |
| foam | `#9ccfd8` | `#9ccfd8` | `#56949f` |
| iris | `#c4a7e7` | `#c4a7e7` | `#907aa9` |
| highlight low | `#21202e` | `#2a283e` | `#f4ede8` |
| highlight med | `#403d52` | `#44415a` | `#dfdad9` |
| highlight high | `#524f67` | `#56526e` | `#cecacd` |

**One genuine discrepancy inside the official repository, recorded so a reviewer
who trips over it does not conclude this table is wrong.** The repository-root
file https://raw.githubusercontent.com/rose-pine/palette/main/palette.json gives
Dawn's `text` as `#464261`, whereas the hand-authored source and everything
generated into `dist/` give `#575279`. That root file was added in a commit
describing itself as temporary and, unlike the real definition, omits the three
highlight roles entirely — it is an incomplete side artefact. **`#575279` is the
value used above**, because it is what the authored source, all generated
output and the official palette page carry.

#### Catppuccin

Source: the single machine-readable source of truth from which all Catppuccin
ports are generated,
https://raw.githubusercontent.com/catppuccin/palette/main/palette.json, with the
rendered tables at
https://raw.githubusercontent.com/catppuccin/catppuccin/main/README.md. Values
below are from palette version 1.8.0. Catppuccin ships **both**: **Latte is the
official light flavour**, and Frappé, Macchiato and Mocha are dark in increasing
darkness. The JSON carries an explicit dark flag per flavour, plus `hex`, `rgb`,
`hsl`, `oklch` and an accent boolean per colour — **the best machine-readable
definition of the five**, and the only one `opnview` could consume mechanically
if it ever wanted to.

| Name | Mocha (dark) | Latte (light) |
|---|---|---|
| rosewater | `#f5e0dc` | `#dc8a78` |
| flamingo | `#f2cdcd` | `#dd7878` |
| pink | `#f5c2e7` | `#ea76cb` |
| mauve | `#cba6f7` | `#8839ef` |
| red | `#f38ba8` | `#d20f39` |
| maroon | `#eba0ac` | `#e64553` |
| peach | `#fab387` | `#fe640b` |
| yellow | `#f9e2af` | `#df8e1d` |
| green | `#a6e3a1` | `#40a02b` |
| teal | `#94e2d5` | `#179299` |
| sky | `#89dceb` | `#04a5e5` |
| sapphire | `#74c7ec` | `#209fb5` |
| blue | `#89b4fa` | `#1e66f5` |
| lavender | `#b4befe` | `#7287fd` |
| text | `#cdd6f4` | `#4c4f69` |
| subtext1 | `#bac2de` | `#5c5f77` |
| subtext0 | `#a6adc8` | `#6c6f85` |
| overlay2 | `#9399b2` | `#7c7f93` |
| overlay1 | `#7f849c` | `#8c8fa1` |
| overlay0 | `#6c7086` | `#9ca0b0` |
| surface2 | `#585b70` | `#acb0be` |
| surface1 | `#45475a` | `#bcc0cc` |
| surface0 | `#313244` | `#ccd0da` |
| base | `#1e1e2e` | `#eff1f5` |
| mantle | `#181825` | `#e6e9ef` |
| crust | `#11111b` | `#dce0e8` |

**Frappé** and **Macchiato** are published in the same `palette.json` under their
own keys, and in the same rendered README tables; their twenty-six values are:

*Frappé* — rosewater `#f2d5cf`, flamingo `#eebebe`, pink `#f4b8e4`, mauve
`#ca9ee6`, red `#e78284`, maroon `#ea999c`, peach `#ef9f76`, yellow `#e5c890`,
green `#a6d189`, teal `#81c8be`, sky `#99d1db`, sapphire `#85c1dc`, blue
`#8caaee`, lavender `#babbf1`, text `#c6d0f5`, subtext1 `#b5bfe2`, subtext0
`#a5adce`, overlay2 `#949cbb`, overlay1 `#838ba7`, overlay0 `#737994`, surface2
`#626880`, surface1 `#51576d`, surface0 `#414559`, base `#303446`, mantle
`#292c3c`, crust `#232634`.

*Macchiato* — rosewater `#f4dbd6`, flamingo `#f0c6c6`, pink `#f5bde6`, mauve
`#c6a0f6`, red `#ed8796`, maroon `#ee99a0`, peach `#f5a97f`, yellow `#eed49f`,
green `#a6da95`, teal `#8bd5ca`, sky `#91d7e3`, sapphire `#7dc4e4`, blue
`#8aadf4`, lavender `#b7bdf8`, text `#cad3f5`, subtext1 `#b8c0e0`, subtext0
`#a5adcb`, overlay2 `#939ab7`, overlay1 `#8087a2`, overlay0 `#6e738d`, surface2
`#5b6078`, surface1 `#494d64`, surface0 `#363a4f`, base `#24273a`, mantle
`#1e2030`, crust `#181926`.

Note the inversion in Latte: `base` is the *lightest* and `crust` the *darkest*,
while in the dark flavours `base` is dark and `crust` darker still, with the
text, overlay and surface ramps inverting accordingly. Any adoption has to map
by **role**, not by position.

### What this means for the five-palette decision

Recorded as an observation, not as a choice. Of the five named palettes, **three
ship an official light variant** (Tokyo Night's Day, Rosé Pine's Dawn,
Catppuccin's Latte), **one ships a light counterpart under a different name**
(Dracula's Alucard) and **one is dark-only** (Nord). Settled decision 5 —
"theme on first launch follows the operating system" — therefore has a concrete
consequence: **on a machine set to light, a user who has chosen Nord has no
light variant to follow the OS into.** That is a real question the mockup cycle
must answer, and it is named here rather than discovered later. Two obvious
options, neither chosen here: fall back to the industrial light palette while
keeping Nord's accents, or state plainly in the theme picker that Nord is
dark-only.

Two of the five publish a **machine-readable palette file** (Catppuccin's
`palette.json`, Rosé Pine's `dist/`), which matters because it decides whether
the values in this document are the source of truth for `opnview` or merely a
record. Given `ROADMAP.md`'s no-outbound-calls rule, **they are a record either
way**: the values ship inside the binary, and nothing is fetched at runtime.

## Family D — visual inspiration

A moodboard, not a survey. These are interfaces worth stealing from, grouped by
**the idea they carry** rather than by product. **Everything in this section is
exempt from the citation allow-list**: an image here may come from a vendor's
site, a project README, a blog post, an old blog post, a forum, a review, a
design gallery, or a concept that was never built. What is recorded is
provenance — where it came from and when it was captured — not proof.

**Concepts that were never shipped are explicitly wanted**, and there are some
here. Two notes on what was *not* found: Dribbble and Behance both sit behind
challenge pages that refuse automated fetching, so unbuilt-concept material
comes from a Figma-concept gallery and a curated empty-state gallery instead;
and the live public demos that would have supplied more were largely
unavailable, as recorded under *Scope and method*.

Every image below was opened and looked at before being kept. Candidates
exposing a real network's addressing, hostnames or device names were discarded
rather than cropped. **The complete index at the end of this section lists every
image file in `docs/ui-references/screenshots/`**, with its source, its capture
date, what it shows and what is worth taking — so the document's argument
survives the fact that the images themselves are not committed and will not
survive a clone.

### Layout and density

![Arkime's session browser](ui-references/screenshots/moodboard-layout-arkime-sessions.png)
*Source: the screenshot set published on `arkime.com`; captured 2026-09-22.* A
brushable time histogram and a small choropleth sharing one header strip, above
a dense session table. **Worth taking: putting the time filter and the
geography in the same row above the table**, so the two controls that scope
everything else are adjacent and the table is the only thing that scrolls.

![Glance's three-column widget dashboard](ui-references/screenshots/moodboard-layout-glance-main.png)
*Source: the README image in the `glanceapp/glance` repository; captured
2026-09-22.* Three columns of stacked widgets — calendar, feeds, markets — in a
monospace dark theme with quiet uppercase micro-headings. **Worth taking: a
column-based rather than grid-based layout**, where the user chooses column
widths once and widgets simply stack, which is far cheaper to place than a
free grid and reads as deliberately plain rather than as an unfinished
dashboard. The monospace numerals are the other idea: figures line up
vertically without any alignment work.

![Malcolm's security overview](ui-references/screenshots/moodboard-layout-malcolm-security-overview.png)
*Source: the screenshot set published in the `cisagov/Malcolm` repository;
captured 2026-09-22.* A long dark dashboard mixing a category bar chart, a
summary table, protocol and vulnerability tables and a word cloud. **Worth
taking: its panel grammar** — every panel has a plain title bar, exactly one
chart or one sortable table, and an export link at its foot. A canvas of mixed
widgets holds together when every widget obeys the same three rules.

![Malcolm's sensor host dashboard](ui-references/screenshots/moodboard-layout-malcolm-host-resources.png)
*Source: the `cisagov/Malcolm` repository screenshot set; captured 2026-09-22.*
Arc gauges and oversized numerals down the left, matching time series down the
right. **Worth taking: the left/right split of "state now" against "state over
time" for the same metrics**, which answers both questions without a period
selector and without two widgets.

![ntopng's infrastructure dashboard](ui-references/screenshots/moodboard-layout-ntopng-infra.png)
*Source: the ntopng user's guide at `ntop.org`; captured 2026-09-22.* Six KPI
tiles, one wide traffic chart, then a row of three top-N list panels. **Worth
taking: the rhythm — tiles, one hero chart, a row of lists.** That is almost
exactly the shape a single-page `opnview` overview wants, and it arrives at it
without any drag-and-drop.

![Uptime Kuma's list rail and detail pane](ui-references/screenshots/moodboard-layout-uptimekuma.jpg)
*Source: the light-theme screenshot on `uptime.kuma.pet`; captured 2026-09-22.*
A monitor list on the left, each row carrying an uptime badge and a compact
heartbeat bar, next to a detail pane. **Worth taking: the heartbeat bar as a
per-row history** — twenty or so coloured ticks say more about a device's
recent behaviour than a percentage does, and they cost one table column.

![An unbuilt analytics-dashboard concept](ui-references/screenshots/moodboard-layout-uidd-statistics-1121.png)
*Source: `uidesigndaily.com`, a gallery of Figma concepts that were never
shipped; captured 2026-09-22.* White cards with **one deliberately inverted dark
card** among them, a metric row, a top-pages list and an inline "no alerts set"
strip. **Worth taking: a single inverted card to make one figure dominant
without making it bigger** — and note that the empty state here is a full-width
inline strip with its own call to action rather than a hole in the layout.

### The shape of a map

![ntopng's host geo map](ui-references/screenshots/moodboard-map-ntopng-geo.png)
*Source: the ntopng user's guide; captured 2026-09-22.* **The maintainer's
stated preference for the world map.** A pale de-saturated basemap with small
orange dots and a click-out card carrying the address, the resolved name, the
country and city and the host's scores. **Worth taking: the map is background
and the data is foreground** — no choropleth, no arcs, nothing competing with
the marks.

![Malcolm's geographic map of destination bytes](ui-references/screenshots/moodboard-map-malcolm-latlon.png)
*Source: the `cisagov/Malcolm` repository screenshot set; captured 2026-09-22.*
Dots sized and coloured by volume, with the dashboard's navigation list pinned
in a left rail beside the map. **Worth taking: the persistent left rail of
sibling dashboards beside the map**, so the map is one view of a set rather than
a destination page you have to navigate back out of.

![Malcolm's mirrored connection trees](ui-references/screenshots/moodboard-map-malcolm-conn-tree.png)
*Source: the `cisagov/Malcolm` repository screenshot set; captured 2026-09-22.*
**A maintainer preference.** The same root host drawn twice, as a
"from destination" and a "from source" hierarchy, with root, tree height and
node colour exposed as controls below. **Worth taking: two mirrored trees from
one root**, and **tree depth as a user control** — the single most effective
defence against the clutter that sinks Vizceral.

![Arkime's force-directed connections graph](ui-references/screenshots/moodboard-map-arkime-connections.png)
*Source: the `arkime.com` screenshot set; captured 2026-09-22.* **A maintainer
preference.** Nodes sized and coloured by a weight the user picks from a
toolbar, with a docked node-detail panel on the left. **Worth taking: naming the
weight in the toolbar** so the graph states what its sizes mean, and **docking
the detail panel** so values are readable rather than hover-only.

![NetBox topology views, dark and light](ui-references/screenshots/moodboard-map-netbox-topology-dark.png)
*Source: the `netbox-community/netbox-topology-views` repository documentation
images; captured 2026-09-22.* A device-icon node graph with the active filters
shown as **removable chips directly above the canvas**. **Worth taking: filters
as dismissible chips on the canvas itself**, so a diagram always states what it
is currently showing — which is the graph equivalent of `opnview`'s obligation
to state its observation-point limit.

![The same NetBox topology in the light theme](ui-references/screenshots/moodboard-map-netbox-topology-light.png)
*Same source and date.* The identical screen with only the canvas and node fills
swapped. **Worth taking: proof that one link-colour palette can survive both
themes** if the theme changes the ground and the fills and leaves the semantic
colours alone — directly relevant to shipping six palettes over one layout.

![Netflix's Vizceral traffic graph](ui-references/screenshots/moodboard-map-vizceral.png)
*Source: the example image in the `Netflix/vizceral` repository; captured
2026-09-22.* **Recorded as a rejection.** Around a hundred services with
animated particles flowing along every edge. **Worth avoiding: at this density
the animation stops carrying information and becomes texture** — you cannot
follow one flow, compare two edges, or read a value. The idea is sound at ten
nodes and useless at a hundred.

### How a list of domains is presented

![Arkime's SPIView field explorer](ui-references/screenshots/moodboard-domains-arkime-spiview.png)
*Source: the `arkime.com` screenshot set; captured 2026-09-22.* Collapsible
field categories, each field expanding into an inline cloud of values with
occurrence counts. **Worth taking: top values as inline wrapped chips with
superscript counts, instead of one table per field** — enormous density in very
little height, and a strong candidate for how a small `opnview` widget shows top
domains or top ports.

![Arkime's SPIGraph small multiples](ui-references/screenshots/moodboard-domains-arkime-spigraph.png)
*Source: the `arkime.com` screenshot set; captured 2026-09-22.* One row per
destination address, each with **its own timeline and its own miniature world
map**. **Worth taking: small multiples** — repeating an identical timeline-plus-
map pair per host makes hosts visually comparable in a way no single combined
chart can.

![ntopng's live flows table](ui-references/screenshots/moodboard-domains-ntopng-flows.png)
*Source: the ntopng user's guide; captured 2026-09-22.* **A maintainer
preference: ntopng's flow view.** A dropdown-per-dimension filter row above a
table whose central column renders an entire flow as one cell. **Worth taking:
one cell per flow, and a filter bar built of dropdowns rather than a search
box.**

![ntopng's per-host DNS breakdown](ui-references/screenshots/moodboard-domains-ntopng-host-dns.png)
*Source: the ntopng user's guide; captured 2026-09-22.* A per-host DNS table
with **inline two-tone proportion bars in the last column**. **Worth taking:
the inline ratio bar inside a table cell** — cheap to build, instantly readable,
and exactly what a "queries allowed versus blocked, per device" column wants.

![Malcolm's DNS dashboard](ui-references/screenshots/moodboard-domains-malcolm-dns.png)
*Source: the `cisagov/Malcolm` repository screenshot set; captured 2026-09-22.*
Client, server and answer tables, query-type and response-code tables, a
query-to-answer table and a raw log grid, in the light theme. **Worth taking:
ranking queried names by a computed randomness score** — a derived column that
turns a domain list into a detector, and a reminder that a good column can be
worth more than a chart.

![Pi-hole's audit log, allowed and blocked as mirrored tables](ui-references/screenshots/moodboard-blocked-pihole-allowed-vs-blocked.png)
*Source: the Pi-hole project's own blog media at `pi-hole.net`; captured
2026-09-22.* Two side-by-side domain tables with opposite per-row actions.
**Worth taking: two mirrored tables instead of a status column**, each row
offering the action that flips its state.

### How blocked is distinguished from allowed

![EveBox's alert inbox](ui-references/screenshots/moodboard-blocked-evebox-inbox.png)
*Source: `evebox.org`; captured 2026-09-22.* One alert per row, **the whole row
tinted by severity** rather than badged, with an aggregation count and a
one-click Archive. **Worth taking: tinting the entire row**, which survives
peripheral vision in a way a badge does not, and **treating the alert list as a
work queue with an action per row** rather than as a report. EveBox reads the
same Suricata `eve.json` `opnview` reads, so this is a close analogue.

![Pi-hole's dashboard: blocked as a share of the whole](ui-references/screenshots/moodboard-blocked-pihole-dashboard.png)
*Source: the Pi-hole project's own blog media; captured 2026-09-22.* Four
coloured KPI tiles — total, blocked, percent blocked, blocklist size — over
stacked 24-hour query charts. **Worth taking: blocked-versus-allowed as a
stacked series inside one timeline, plus a dedicated percentage tile**, with one
colour convention carried across both so the eye learns it once.

![Technitium's counter strip](ui-references/screenshots/moodboard-blocked-technitium-dashboard.png)
*Source: `technitium.com`; captured 2026-09-22.* A full-width strip of eleven
colour-coded counter tiles — total, no error, NXDOMAIN, refused, cached,
blocked, dropped, clients — above a multi-series chart. **Worth taking: counters
that carry both an absolute number and its share** ("12 — 0.17%"), and a legend
that doubles as the series toggle.

![AdGuard Home's dashboard](ui-references/screenshots/moodboard-blocked-adguard-home.gif)
*Source: AdGuard's own published walkthrough animation, linked from the
`AdguardTeam/AdGuardHome` repository; captured 2026-09-22. An animated GIF
rather than a still.* KPI tiles with a sparkline behind the number, a general
statistics list, top clients, and **top queried and top blocked domains as
adjacent panels**. **Worth taking: a sparkline behind a KPI number** costs no
extra space and turns a figure into a trend.

![Malcolm's Suricata alerts dashboard](ui-references/screenshots/moodboard-blocked-malcolm-suricata-alerts.png)
*Source: the `cisagov/Malcolm` repository screenshot set; captured 2026-09-22.*
Alert count, alerts over time, a tag word cloud, an alert-category bar chart and
target and name tables. **Worth taking: ranking alert *categories* horizontally
so the long tail stays readable**, rather than a pie that turns the tail into
slivers.

![Malcolm's severity dashboard](ui-references/screenshots/moodboard-blocked-malcolm-severity.png)
*Source: the `cisagov/Malcolm` repository screenshot set; captured 2026-09-22.*
Severity tags with a count column *and* a worst-case severity column, plus a
bucketed severity-score histogram. **Worth taking: keeping both a count and a
worst case per row**, so a frequent-but-benign category cannot hide a rare
severe one — directly applicable to `opnview`'s alerts-by-signature widget.

### How an empty widget is handled

This group is the one `opnview` needs most, because decisions 3 and 6 make the
explained empty state a load-bearing part of the product rather than a polish
item. All but one of these come from a curated gallery of empty states rather
than from network tools, which is deliberate: the network tools in this document
are mostly bad at it.

![Cloudflare Radar's loading skeletons](ui-references/screenshots/moodboard-empty-cloudflare-radar-skeleton.jpg)
*Captured live from `https://radar.cloudflare.com/traffic`, 2026-09-22, during
load.* Each panel holds a grey placeholder **in the exact shape of the content
that is coming**. **Worth taking: a panel with no data yet keeps its size and
its title**, so nothing reflows when the data lands.

![A reports page with no data in the selected range](ui-references/screenshots/moodboard-empty-getresponse-stats.png)
*Source: `emptystat.es`, a curated gallery of empty states; captured
2026-09-22.* An illustration plus "No messages found in the selected time range
/ Use a different time range", with the time range still visible above. **Worth
taking: naming the *cause* rather than saying "no data"** — the fix is in the
sentence, and the control that caused it is still on screen. This is precisely
what `opnview`'s "reachable and returned no rows" state should read like.

![A task list with nothing in it](ui-references/screenshots/moodboard-empty-reclaim-tasks.png)
*Source: `emptystat.es`; captured 2026-09-22.* A heading, one calm sentence, and
a large flat illustration filling the panel. **Worth taking: heading first,
illustration second** — the text reads before the art, and the empty area is
owned rather than left blank.

![A drafts view with no drafts](ui-references/screenshots/moodboard-empty-slack-drafts.png)
*Source: `emptystat.es`; captured 2026-09-22.* A small monochrome glyph, one
sentence explaining what would appear here, and the single action that would
fill it. **Worth taking: the minimal version** — glyph, one sentence, one
action — which is the right weight for a small widget rather than a whole page.

![An empty detail pane beside a populated list](ui-references/screenshots/moodboard-empty-messages-web.png)
*Source: `emptystat.es`; captured 2026-09-22.* The list rail is full while the
detail pane holds an illustration and a note about a precondition. **Worth
taking: an empty state that explains a *precondition* rather than apologising**
— which is exactly the shape of "Suricata is installed but not running".

![A profile with nothing published](ui-references/screenshots/moodboard-empty-figma-nothing.png)
*Source: `emptystat.es`; captured 2026-09-22.* A dashed-outline placeholder that
**mimics the shape of the missing content**, with "Nothing published yet" and an
Explore button. **Worth taking: the dashed placeholder shaped like the thing
that is missing**, which tells the user what would go there without a sentence.

![A first-run window as a drop target](ui-references/screenshots/moodboard-empty-tower-repos.png)
*Source: `emptystat.es`; captured 2026-09-22.* An empty sidebar and a dashed
drop target reading "Drop Folder or URL to Add…", with the three ways in spelled
out beneath. **Worth taking: turning the empty area itself into the affordance**
— for `opnview` that is an empty canvas whose blank space is the "add a widget"
target.

![An empty screens list](ui-references/screenshots/moodboard-empty-invision-screens.png)
*Source: `emptystat.es`; captured 2026-09-22.* A project with no screens yet,
rendered as a centred prompt inside the frame the content will occupy. **Worth
taking: the empty state sized to the container it is standing in**, rather than
a message floating in an oversized void.

### How a theme picker is offered

![Dashy's eighteen themes as a contact sheet](ui-references/screenshots/moodboard-theme-dashy-themes.png)
*Source: the theming documentation image in the `Lissy93/dashy` repository,
hosted on an image host; captured 2026-09-22.* The same dashboard rendered in
every theme, tiled. **Worth taking: presenting themes as live previews of the
user's own page**, not as swatches and not as names — the user picks by
recognising their own dashboard.

![Dashy's theme configurator in use](ui-references/screenshots/moodboard-theme-dashy-configurator.gif)
*Source: the `Lissy93/dashy` theming documentation; captured 2026-09-22. An
animated GIF.* A theme picker living in the page header, recolouring the page
live. **Worth taking: no save-and-preview round trip** — the picker is a
control on the page it affects, which is the only honest way to choose a theme.

![Six Glance themes as a grid](ui-references/screenshots/moodboard-theme-glance-themes.png)
*Source: the `glanceapp/glance` repository documentation images; captured
2026-09-22.* Three dark, one sepia and two light renderings of one dashboard,
tiled three by two. **Worth taking: a theme set defined by a handful of tokens —
background, surface, text, accent — where the layout never moves between
variants.** That is exactly the constraint `opnview` needs if six palettes are
to ship over one design.

![Flame's four themes in one diagonally sliced screenshot](ui-references/screenshots/moodboard-theme-flame-themes.png)
*Source: the `pawelmalak/flame` repository documentation images; captured
2026-09-22.* One start page sliced diagonally into four themes, with no seams or
captions. **Worth taking: the comparison device itself** — four variants of one
screen in a single image is a better argument for a palette than four separate
screenshots, and it is how this document should present `opnview`'s own palettes
when the mockup cycle produces them.

### Four more, added after the maintainer's review

These four were not in the original fifteen. They were added because the
maintainer, looking at the first set of captures, named three of them as
preferences and one as a thing to avoid. Each gets a short entry rather than the
full seven sub-headings, because what is wanted from them is a specific idea
rather than a survey — and because two of them are flow-analysis tools rather
than composable-dashboard products, so most of the Family A questions would be
answered "not applicable" without saying anything useful.

#### Akvorado

A flow-collection and visualisation project — NetFlow, IPFIX and sFlow in,
ClickHouse behind, a web console in front
(https://github.com/akvorado/akvorado). Its console builds queries over flow
dimensions and renders them as time series, stacked areas and **Sankey
diagrams**, with the dimensions chosen by the user rather than fixed
(https://demo.akvorado.net/docs/intro). It is the closest thing in this document
to what `opnview`'s matrix and operator widgets are trying to be, built on the
same kind of data `opnview` gets from Insight.

![Akvorado's source-AS to destination-AS Sankey](ui-references/screenshots/moodboard-layout-akvorado-asn-sankey.png)
*Source: the screenshot published with the "Akvorado NetFlow" community
dashboard on `grafana.com/grafana/dashboards` (dashboard 24189); captured
2026-09-22.* Destination autonomous systems down the left, source autonomous
systems down the right, ribbons between them sized by volume, each end labelled
with the AS number and the operator name. **The maintainer has named this
specifically**; see *The maintainer's recorded preferences*. What is worth
taking: **the Sankey answers "who talks to whom" with the operator names
already resolved**, so the reader never has to look up a number — and the
collapsed "Other" band at the top is an honest way to keep a long tail from
making the diagram unreadable, which a matrix cannot do.

#### Malcolm

A network-traffic-analysis tool suite built around Arkime and OpenSearch
dashboards, published by an agency of the US government
(https://github.com/cisagov/Malcolm). Its contribution here is its **dashboard
vocabulary**: a set of purpose-built dashboards over parsed protocol data, one
per question, each mixing a chart, a summary table and a raw log grid under a
plain title bar.

![Malcolm's IP connections tree](ui-references/screenshots/moodboard-map-malcolm-conn-tree.png)
*Source: the screenshot set published in the Malcolm repository; captured
2026-09-22.* The same root host drawn **twice** — once as a "from destination"
hierarchy and once as a "from source" hierarchy — with root, tree height and
node-colour controls beneath. **The maintainer has named Malcolm's connection
tree as very good**; see *The maintainer's recorded preferences*. What is worth
taking: **two mirrored trees from one root**, answering "who talks to it" and
"who it talks to" side by side, and exposing tree depth as a control so the user
decides how much sprawl they want — which is precisely the lever Vizceral does
not give them.

![Malcolm's DNS dashboard](ui-references/screenshots/moodboard-domains-malcolm-dns.png)
*Source: the Malcolm repository screenshot set; captured 2026-09-22.* Client,
server and answer tables; query-type and response-code tables; a query-to-answer
table; and a raw log grid — in the light theme. What is worth taking: ranking
queried names by a **computed randomness score** column turns a domain list into
a detector, and it is a reminder that a derived column can be worth more than a
chart.

#### Arkime

A full-packet-capture indexing and search system with its own web interface
(https://arkime.com/, https://github.com/arkime/arkime). Two of its screens are
directly relevant.

![Arkime's session browser](ui-references/screenshots/moodboard-layout-arkime-sessions.png)
*Source: the screenshot set published on `arkime.com`; captured 2026-09-22.* A
brushable time histogram and a small world map in a header strip, above a dense
session table. What is worth taking: **the header band that pairs a time
histogram with a small map**, so the time filter and the geography live in the
same row above the table and filtering by either re-draws the other.

![Arkime's connections graph](ui-references/screenshots/moodboard-map-arkime-connections.png)
*Source: the `arkime.com` screenshot set; captured 2026-09-22.* A force-directed
source-and-destination graph with a node-detail panel docked to the left and
graph controls in a toolbar. **The maintainer has named Arkime's connection tree
as very good**; see *The maintainer's recorded preferences*. What is worth
taking: **node size and colour driven by a user-chosen weight**, named in the
toolbar, plus click-to-inspect in a docked panel rather than a hover tooltip — a
graph you can actually read values off, rather than one you can only look at.

![Arkime's SPIView field explorer](ui-references/screenshots/moodboard-domains-arkime-spiview.png)
*Source: the `arkime.com` screenshot set; captured 2026-09-22.* Collapsible
field categories, each field expanding into an inline cloud of values with
occurrence counts. What is worth taking: **showing a "top values" list as inline
wrapped chips with counts, instead of one table per field**, which fits an
enormous amount of information into very little height — a strong candidate for
how `opnview` renders "top domains" and "top ports" inside a small widget.

#### Vizceral — recorded as a negative preference

Netflix's service-topology visualisation: services as nodes in a left-to-right
traffic graph, with **animated particles flowing along every edge** to represent
request volume, and a region breadcrumb acting as the zoom control
(https://github.com/Netflix/vizceral).

![Vizceral's animated traffic graph](ui-references/screenshots/moodboard-map-vizceral.png)
*Source: the example image published in the Vizceral repository; captured
2026-09-22.* Around a hundred services and their edges, all animated at once.
**The maintainer has rejected this one, and the reason is recorded because
knowing what to avoid is as useful as knowing what to copy: it is too
cluttered.** What is worth *avoiding*: at this node count the particle animation
stops carrying information and becomes texture — the eye cannot follow a
particular flow, cannot compare two edges, and cannot read a value off anything.
The underlying idea, encoding direction and volume as motion on the edge rather
than as an arrowhead and a label, is sound at ten nodes and useless at a
hundred; `opnview` has both a matrix and a Sankey for that question, and neither
degrades this way.


### Complete image index

Every file in `docs/ui-references/screenshots/`, with provenance and its
argument, so this document stands without the images. **All were captured on
2026-09-22.** None shows a real network's addressing, hostnames or device names;
where a published capture already carried redaction bars or blurring, that is
noted.

| File | Group | Source | What it shows | What is worth taking |
|---|---|---|---|---|
| [`demo-cloudflare-radar-overview.jpg`](ui-references/screenshots/demo-cloudflare-radar-overview.jpg) | Family B, layout | Live capture of `https://radar.cloudflare.com/` | Radar's worldwide overview: left rail, scoped header, traffic chart with a previous-period line, protocol gauge, explained panels | The maintainer's general-layout preference: angular cards, a one-line explanation under every title, comparison drawn by default |
| [`demo-dashy-home.jpg`](ui-references/screenshots/demo-dashy-home.jpg) | Family A, layout | Live capture of `https://demo.dashy.to/` | Dashy's public demo: sections of differing column and row footprints, each holding a grid of items | The section, not the widget, as the unit of composition; per-section density without a global setting |
| [`familya-dashy-config-editor-tree.jpg`](ui-references/screenshots/familya-dashy-config-editor-tree.jpg) | Family A | Live capture of the Dashy demo's Configurator | The config editor collapsed to its top-level tree, with node counts, a breadcrumb and a tree/text mode switch | Node counts in the tree, and the explicit "apply to file / apply locally" choice beneath it |
| [`familya-dashy-json-schema-editor.jpg`](ui-references/screenshots/familya-dashy-json-schema-editor.jpg) | Family A | Live capture of the Dashy demo | The schema-driven form: a typed control per field, plus a green "configuration is valid" indicator | Generating form controls from a schema, and keeping validation status permanently on screen |
| [`familya-datadog-timeseries-widget.png`](ui-references/screenshots/familya-datadog-timeseries-widget.png) | Family A | Datadog's own documentation media | Two stacked timeseries widgets with an event-overlay band above the plot | The event-overlay band — a model for drawing IDS alerts onto a flow timeline |
| [`familya-datadog-widget-edit-panel.png`](ui-references/screenshots/familya-datadog-widget-edit-panel.png) | Family A | Datadog's own documentation media | The widget editor: live preview, numbered steps, a visualization tab strip, and Edit / **JSON** / Share tabs on the query | A numbered-step editor, and a per-widget JSON tab sitting beside the visual editor |
| [`familya-grafana-dashboard-view-mode.jpg`](ui-references/screenshots/familya-grafana-dashboard-view-mode.jpg) | Family A | Live capture of `play.grafana.org` | A dashboard in view mode: breadcrumb, time picker, Edit button, panel frames | The read-only chrome a dashboard has to carry, and how little of it there is |
| [`familya-grafana-edit-as-code-json.jpg`](ui-references/screenshots/familya-grafana-edit-as-code-json.jpg) | Family A | Live capture of `play.grafana.org` | The "Edit as code" drawer: dashboard JSON in a code editor with a JSON/YAML toggle, "Show diff" and "Apply changes", **with the live dashboard still rendering beside it** and one panel reading "No data" | The single most load-bearing reference for `opnview`'s two coordinated surfaces: code and rendering on one screen, an explicit apply step, and a diff |
| [`familya-grafana-import-dashboard.jpg`](ui-references/screenshots/familya-grafana-import-dashboard.jpg) | Family A | Live capture of `play.grafana.org` | The import screen: a file dropzone, a catalogue-id field, and a paste-JSON textarea | Three parallel import paths on one page, with pasting treated as first-class |
| [`familya-grafana-import-options-mapping.jpg`](ui-references/screenshots/familya-grafana-import-options-mapping.jpg) | Family A | Live capture of `play.grafana.org` | The second import step: name, folder and UID with a "Change uid" control before anything is written | The remapping step between "here is a file" and "it exists" — `opnview`'s repair surface has the same job |
| [`familya-grafana-panel-editor.png`](ui-references/screenshots/familya-grafana-panel-editor.png) | Family A | Grafana's own documentation media | The panel editor at full size: preview, Queries and Transformations tabs, and collapsible option groups on the right | The canonical three-region split, and option groups that collapse so the pane stays navigable |
| [`familya-grafana-panel-editor-live.jpg`](ui-references/screenshots/familya-grafana-panel-editor-live.jpg) | Family A | Live capture of `play.grafana.org` | The same editor in real proportions, with query rows A–D stacked | How much vertical space real query rows take, which the documentation image understates |
| [`familya-homarr-app-edit-form.png`](ui-references/screenshots/familya-homarr-app-edit-form.png) | Family A | Homarr's own documentation | The "New app" form: name, an icon picker searching fourteen thousand icons, description, URL, and a separate ping URL | **The answer to "how does Homarr edit a card's content": a typed form, not a code editor.** The separate ping URL is a good idea — link one address, health-check another |
| [`familya-homarr-board-layout-large.png`](ui-references/screenshots/familya-homarr-board-layout-large.png) | Family A, layout | Homarr's own documentation | A v2 board: heterogeneous widget sizes on one grid, plus a collapsible category section | The maintainer's stated layout preference; the category section as the board's structural unit |
| [`familya-homarr-layout-settings.png`](ui-references/screenshots/familya-homarr-layout-settings.png) | Family A | Homarr's own documentation | The Layout pane: **named responsive layouts**, each with a column-count slider and a breakpoint, and an "Add layout" control | The mechanism behind the maintainer's preference for Homarr's tile placement: the user defines the breakpoints and the column count per breakpoint, and every item's position is stored per layout |
| [`familya-homarr-notebook-edit.png`](ui-references/screenshots/familya-homarr-notebook-edit.png) | Family A | Homarr's own documentation | The Notebook widget in edit mode: a **rich-text toolbar** — bold, italic, headings, lists, tables, colour — over formatted content | The other half of the Homarr content answer: where Homarr does edit content, it is a word-processor toolbar, deliberately not a code editor |
| [`familya-homarr-notebook-view.png`](ui-references/screenshots/familya-homarr-notebook-view.png) | Family A | Homarr's own documentation | The same widget in view mode, with the toolbar gone | Edit affordances that disappear entirely outside edit mode, so the board is calm when it is being read |
| [`familya-homeassistant-card-picker-by-card.jpg`](ui-references/screenshots/familya-homeassistant-card-picker-by-card.jpg) | Family A | Live capture of `demo.home-assistant.io` | The card picker with By entity / By card tabs, a search field, and every card type rendered as a **live preview with real data** | The best idea in Family A for a widget catalogue: preview each widget populated before it is placed |
| [`familya-homeassistant-card-yaml-editor.jpg`](ui-references/screenshots/familya-homeassistant-card-yaml-editor.jpg) | Family A | Live capture of `demo.home-assistant.io` | One card's dialog switched to its YAML pane: a few lines of YAML on the left, the card rendering live on the right, and a link back to the visual editor | **The maintainer's stated preference for content editing.** Syntax highlighting and a live preview, and nothing else — no file tree, no minimap, no command palette |
| [`familya-homeassistant-edit-mode-add-card.png`](ui-references/screenshots/familya-homeassistant-edit-mode-add-card.png) | Family A | Home Assistant's own documentation | Edit mode in English: an empty "New section" with an "Add card" target and a "Create section" drop zone | How an empty container advertises itself during editing |
| [`familya-homeassistant-overview-dashboard.png`](ui-references/screenshots/familya-homeassistant-overview-dashboard.png) | Family A, layout | Home Assistant's own documentation | A dense masonry dashboard: button grid, gauges, sparkline sensor cards, a history graph | Mixed card sizes in a masonry column layout, and how a sparkline-plus-min/max card reads at small size |
| [`familya-homeassistant-raw-config-editor.jpg`](ui-references/screenshots/familya-homeassistant-raw-config-editor.jpg) | Family A | Live capture of `demo.home-assistant.io` | The whole-dashboard Raw configuration editor: full-screen YAML with line numbers and fold arrows, and a Save button | The "edit the whole document" escape hatch, and how the config tree maps one-to-one onto the rendered layout |
| [`familya-homeassistant-sections-edit-mode.jpg`](ui-references/screenshots/familya-homeassistant-sections-edit-mode.jpg) | Family A | Live capture of `demo.home-assistant.io` | Edit mode as an overlay on the real dashboard: dashed drop outlines, per-section handles, "+" placeholders, undo/redo and Done | Edit mode as an overlay rather than a separate screen — every container grows an explicit "add here" target |
| [`familya-homeassistant-sections-view.png`](ui-references/screenshots/familya-homeassistant-sections-view.png) | Family A | Home Assistant's own documentation | A sections-view dashboard in view mode: a badge row and six titled sections of tile cards | Two levels of grid — sections that flow, cards that are placed |
| [`familya-kibana-dashboard-overview.png`](ui-references/screenshots/familya-kibana-dashboard-overview.png) | Family A, layout | Elastic's own documentation | A dashboard in view mode: a query bar with a filter pill, a time picker, a KPI row with delta badges, charts with in-panel legend tables, a detail table | KPI row, charts with embedded legend tables, detail table — all under one filter bar; close to what `opnview` needs |
| [`familya-kibana-import-missing-references.png`](ui-references/screenshots/familya-kibana-import-missing-references.png) | Family A | An issue thread in the `elastic/kibana` repository | The import flyout reporting reference conflicts: a table of missing references with an affected-object count, a sample of the objects, a remap dropdown per reference, and "Confirm all changes" | **The best reference anywhere for the unresolved-reference repair flow**: name what is missing, count what it affects, sample it, offer a remap, and write nothing until confirmed |
| [`familya-kibana-saved-objects.png`](ui-references/screenshots/familya-kibana-saved-objects.png) | Family A | Elastic's own documentation | The Saved Objects management table with Import and Export actions, type and tag filters, and a per-row menu | Import and export as a first-class management screen rather than a hidden menu item |
| [`familya-netdata-custom-dashboard-composer.jpg`](ui-references/screenshots/familya-netdata-custom-dashboard-composer.jpg) | Family A | Live capture of a public Netdata demo agent | The dashboard composer: four templates, **each annotated with how many of its charts the current data can actually fill** ("5/7 available") | Directly relevant: a template that advertises its own coverage instead of importing and failing silently — the friendliest possible form of `opnview`'s unresolved-reference report |
| [`familya-netdata-metrics-dashboard.jpg`](ui-references/screenshots/familya-netdata-metrics-dashboard.jpg) | Family A, layout | Live capture of a public Netdata demo agent | The generated metrics view: a gauge row, stat cards, a full-width stacked chart with a per-dimension footer, and a metric tree in the right sidebar | The per-dimension legend strip under a chart, and a navigable metric tree as the primary chart selector |
| [`familya-homepage-instance.png`](ui-references/screenshots/familya-homepage-instance.png) | Family A, layout | The `gethomepage/homepage` repository | A real instance: a resource strip, service cards each with a status pill and inline metric tiles, then plain bookmark rows | The service card as icon + name + description + status + a small row of live metrics, and its graceful fall-through to a plain row when nothing feeds it |
| [`familyb-firewalla-activities-flows.png`](ui-references/screenshots/familyb-firewalla-activities-flows.png) | Family B | The Firewalla app listing on Google Play, vendor demo data | One subject with an Activities / Flows toggle: a stacked hourly chart, then per-application time with bars | **A maintainer preference.** One subject, one window, two renderings behind one toggle |
| [`familyb-firewalla-devices-list.png`](ui-references/screenshots/familyb-firewalla-devices-list.png) | Family B | The Firewalla app listing on Google Play, vendor demo data | Devices grouped by the network they sit on, with a Networks / Groups / All toggle and a per-device type tag | The segment-aware device list, and segmentation as a toggle rather than as separate pages |
| [`familyb-firewalla-home-flows-blocked.png`](ui-references/screenshots/familyb-firewalla-home-flows-blocked.png) | Family B | The Firewalla app listing on Google Play, vendor demo data | Flows and blocked as one headline pair with a percentage ring, a 24-hour chart, and a separate Local Flows panel | The clearest framing of "how much of what I saw was stopped", and inter-segment traffic given its own panel |
| [`familyb-firewalla-rules.webp`](ui-references/screenshots/familyb-firewalla-rules.webp) | Family B | An independent review at `virtualizationhowto.com`, vendor demo data | Rules grouped by the scope they apply to, each readable as one sentence | A rule stated as a sentence addressed to a scope, rather than as a row in a firewall table |
| [`familyb-nextdns-app-blocked-by-blocklist.png`](ui-references/screenshots/familyb-nextdns-app-blocked-by-blocklist.png) | Family B | The NextDNS App Store listing images, phone frame cropped away | A blocked domain's detail sheet: "Blocked by NextDNS Ads & Trackers Blocklist.", the device, the protocol, the time, the root domain, one Allow action | **The most valuable image in this document**: the list named in plain language, beside its provenance, with exactly one remediation |
| [`familyb-nextdns-app-dashboard-blocked-queries.png`](ui-references/screenshots/familyb-nextdns-app-dashboard-blocked-queries.png) | Family B | The NextDNS App Store listing images | A total-and-blocked double line on one sparkline with a period toggle, over a live blocked-queries feed with favicons | Total and blocked as two lines on one chart, and a live feed beneath it — a very compact "is anything unusual right now" |
| [`familyb-nextdns-blocklists.png`](ui-references/screenshots/familyb-nextdns-blocklists.png) | Family B | An independent review thread at `theprepared.com` | Each subscribed list as a card with name, purpose, source link, entry count and "updated N ago" | Provenance, scale and freshness on one line per source — reusable for `opnview`'s source-availability panel |
| [`familyb-nextdns-blocklists-catalog-dark.png`](ui-references/screenshots/familyb-nextdns-blocklists-catalog-dark.png) | Family B, theme | A review post on `substack.com` | The same catalogue in the dark theme with more lists enabled | Proof that the source-card pattern survives both themes unchanged |
| [`familyb-nextdns-security-settings.png`](ui-references/screenshots/familyb-nextdns-security-settings.png) | Family B | An independent review thread at `theprepared.com` | The Security tab: feature cards each with explanatory prose and a single toggle, under a Setup / Security / Privacy / Parental / Denylist / Allowlist / Analytics / Logs tab strip | The "explain, then toggle" card for options that are otherwise opaque — the right shape for `opnview`'s own source toggles |
| [`familyb-ntopng-dashboard.png`](ui-references/screenshots/familyb-ntopng-dashboard.png) | Family B | The ntopng user's guide | The free dashboard: a talkers Sankey, two doughnuts, and a fourth panel sitting in a real **"No Data Found"** empty state beside the populated ones | A genuine example of an empty panel living next to full ones without looking broken |
| [`familyb-ntopng-geomap.png`](ui-references/screenshots/familyb-ntopng-geomap.png) | Family B, map | The ntopng user's guide; the published capture has the host address redacted | A muted basemap, one dot per remote peer, everything else in the popup | The maintainer's preferred world map: map as background, data as foreground |
| [`familyb-ntopng-host-details.png`](ui-references/screenshots/familyb-ntopng-host-details.png) | Family B | The ntopng user's guide | A host's home view: MAC and vendor, address, badges, alerts, score, first and last seen, an as-client / as-server split, and an "Additional Host Names — Source: DHCP" row | Naming the *source* of a host's name in the row that shows the name |
| [`familyb-ntopng-hosts-list.png`](ui-references/screenshots/familyb-ntopng-hosts-list.png) | Family B | The ntopng user's guide | The live-hosts table: a filter bar, then address, name, flows, alerts, score, seen-since, a sent/received split bar, throughput and total bytes | The sent/received split bar inside a table row, and a **score** column that ranks hosts by "worth looking at" rather than by volume |
| [`familyb-ntopng-top-flow-talkers.png`](ui-references/screenshots/familyb-ntopng-top-flow-talkers.png) | Family B | The ntopng user's guide | A full-width Sankey: source hosts left, destinations right, ribbon width proportional to bytes | The canonical who-talks-to-whom visual, and a serious alternative to a matrix for the same question |
| [`familyb-pihole-advanced-filter.png`](ui-references/screenshots/familyb-pihole-advanced-filter.png) | Family B | The `pi-hole/web` published screenshot set | The Query Log's advanced-filter panel: a date range plus selects for domain, client by address, client by name, upstream, type, status, reply and DNSSEC | A compact, honest facet list — exactly the filter vocabulary a log view needs, with a warning about the slow ones |
| [`familyb-pihole-dashboard.png`](ui-references/screenshots/familyb-pihole-dashboard.png) | Family B | The `pi-hole/web` published screenshot set | The v6 dashboard: four KPI tiles, a 24-hour query chart, a client-activity chart, and query-type and upstream doughnuts | A small fixed KPI row plus one dense timeline carrying the whole "what happened today" story |
| [`familyb-pihole-query-log.png`](ui-references/screenshots/familyb-pihole-query-log.png) | Family B | The `pi-hole/web` published screenshot set | The Query Log: time, status icon, type, domain, client, reply time, and per-row Allow and Deny buttons over server-side pagination | The per-row verdict icon plus one-click remediation — and, by omission, the missing blocklist name that Family B's central lesson turns on |
| [`familyb-pihole-top-domains-clients.png`](ui-references/screenshots/familyb-pihole-top-domains-clients.png) | Family B | The `pi-hole/web` published screenshot set | Top Domains and Top Blocked Domains side by side, each with an inline frequency bar | The inline bar inside the row, and the deliberate adjacency of allowed and blocked |
| [`familyb-unifi-dashboard.png`](ui-references/screenshots/familyb-unifi-dashboard.png) | Family B, layout | Ubiquiti's own release post for UniFi Network 9.1, demo site | A left rail of fact cards, one wide throughput chart, a per-application icon row, and connectivity rates | The per-application icon strip; and the rounded, light aesthetic `ROADMAP.md` step 3 currently specifies, shown so the amendment can be argued against something concrete |
| [`familyb-unifi-insights-overview.png`](ui-references/screenshots/familyb-unifi-insights-overview.png) | Family B | Ubiquiti's own release post, demo site | The Insights tab strip with Top Destinations (country flag plus domain) and Top Clients, over a map | A country flag beside each destination row as the cheapest geographic cue that needs no map |
| [`familyb-unifi-switch-ports.png`](ui-references/screenshots/familyb-unifi-switch-ports.png) | Family B | Ubiquiti's own release post, demo site | A colour-coded port map with the connected device above each port, a legend, filter chips, and a per-port table | The same data as a spatial map *and* as a sortable table, with chips as filters instead of a form |
| [`familyb-zenarmor-blocks-report.png`](ui-references/screenshots/familyb-zenarmor-blocks-report.png) | Family B | Zenarmor's own documentation screenshots | "Blocked Local Hosts and Reasons" as stacked bars, a top-blocks doughnut with named reasons, a heatmap, and an interface and VLAN split | Blocking presented as host × reason rather than as a flat event list |
| [`familyb-zenarmor-dashboard.webp`](ui-references/screenshots/familyb-zenarmor-dashboard.webp) | Family B | Zenarmor's own documentation screenshots | The node dashboard inside OPNsense: a one-sentence summary above the charts, three doughnuts, per-interface throughput, and engine and database health | **The plain-sentence headline above the charts**, and the fact that engine health sits on the same page as the data — a direct precedent for stating the observation-point limit in the UI |
| [`familyb-zenarmor-reports-connections.png`](ui-references/screenshots/familyb-zenarmor-reports-connections.png) | Family B | Zenarmor's own documentation screenshots | New connections by application and by source over time, unique host counts, an egress heatmap, and a "Facts" key-value panel of exact counts | The Facts panel — a plain key/value block of exact numbers beside the charts, as an honest counterweight to graphs |
| [`familyb-zenarmor-reports-live-sessions.png`](ui-references/screenshots/familyb-zenarmor-reports-live-sessions.png) | Family B | Zenarmor's own documentation screenshots | The full OPNsense window with Zenarmor's menu inside it, and a six-doughnut Connections report with a hover Filter / Exclude control | What a reporting module looks like inside OPNsense; click-to-filter on any legend entry |
| [`grafana-community-opnsense-country-tagcloud.png`](ui-references/screenshots/grafana-community-opnsense-country-tagcloud.png) | Family A, blocked | The screenshot published with community dashboard 13383 on `grafana.com`; the author redacted addresses with black bars | Pass and block over time, events by interface, and destination and source countries as tag clouds | A tag cloud as a country summary when precision does not matter, beside a precise pass/block timeline |
| [`grafana-community-opnsense-firewall-worldmap.png`](ui-references/screenshots/grafana-community-opnsense-firewall-worldmap.png) | Family A, map | The screenshot published with community dashboard 13383 on `grafana.com` | An OPNsense firewall dashboard: "pass 5321" and "block 129" as oversized numerals, then **two world maps side by side, one for destinations and one for sources**, with a graduated legend | The closest existing thing to `opnview`'s own map widget: allowed and blocked as two maps rather than two overlaid series, with the counts brought forward above them |
| [`grafana-community-opnsense-ids-ips.png`](ui-references/screenshots/grafana-community-opnsense-ids-ips.png) | Family A | The screenshot published with community dashboard 17547 on `grafana.com`; source addresses blurred by the author | Firewall log and IDS event counts as headline numbers, pass/block pies, a source-address bar list, and a classification-and-priority table | Firewall and IDS on one canvas, and a classification table carrying a priority column beside the count |
| [`grafana-community-opnsense-suricata-signatures.png`](ui-references/screenshots/grafana-community-opnsense-suricata-signatures.png) | Family A | The screenshot published with community dashboard 13384 on `grafana.com`; the author redacted one signature | Suricata signatures over time as a stacked bar chart with a hover tooltip listing every series, beside a category doughnut | A tooltip that lists the full stacked breakdown at one instant — which is how a signature timeline becomes readable |
| [`grafana-community-opnsense-system-overview.png`](ui-references/screenshots/grafana-community-opnsense-system-overview.png) | Family A, layout | The screenshot published with community dashboard 13386 on `grafana.com` | An OPNsense system dashboard: a stat row, then five gauges, then paired CPU charts, under collapsible row headings | Collapsible rows as the grouping device, and a stat row that answers the whole question before any chart is read |
| [`grafana-community-sflow-netflow-analytics.png`](ui-references/screenshots/grafana-community-sflow-netflow-analytics.png) | Family A | The screenshot published with community dashboard 20502 on `grafana.com`; addresses blurred by the author | A flow-analytics dashboard: protocol and version doughnuts, an inbound/outbound split, and four paginated top-N tables of ASNs and addresses | Paginated top-N tables side by side, and an explicit inbound-versus-outbound share panel |
| [`grafana-community-traffic-analysis-asn-light.png`](ui-references/screenshots/grafana-community-traffic-analysis-asn-light.png) | Family A, layout | The screenshot published with community dashboard 11206 on `grafana.com` | A traffic-analysis dashboard in **Grafana's light theme**: inbound and outbound by AS as soft area charts, over Top Inbound and Top Outbound ASN tables | The maintainer's charting preference shown in light: soft area charts with many overlapping series still readable, and ASN tables that name the operator |
| [`moodboard-blocked-adguard-home.gif`](ui-references/screenshots/moodboard-blocked-adguard-home.gif) | Blocked | AdGuard's published walkthrough animation | KPI tiles with sparklines, top clients, and top queried beside top blocked | A sparkline behind a KPI number; the deliberate adjacency of queried and blocked |
| [`moodboard-blocked-evebox-inbox.png`](ui-references/screenshots/moodboard-blocked-evebox-inbox.png) | Blocked | `evebox.org` | A Suricata alert inbox: whole rows tinted by severity, an aggregation count, an Archive action per row | Tinting the whole row; treating alerts as a work queue rather than a report |
| [`moodboard-blocked-malcolm-severity.png`](ui-references/screenshots/moodboard-blocked-malcolm-severity.png) | Blocked | The `cisagov/Malcolm` repository screenshot set | Severity tags with both a count and a worst-case column, plus a score histogram | Keeping count and worst case per row, so a frequent benign category cannot hide a rare severe one |
| [`moodboard-blocked-malcolm-suricata-alerts.png`](ui-references/screenshots/moodboard-blocked-malcolm-suricata-alerts.png) | Blocked | The `cisagov/Malcolm` repository screenshot set | Alert count, alerts over time, a tag cloud, a horizontal category chart, and target tables | Ranking alert categories horizontally so the long tail stays readable |
| [`moodboard-blocked-pihole-allowed-vs-blocked.png`](ui-references/screenshots/moodboard-blocked-pihole-allowed-vs-blocked.png) | Domains, blocked | The Pi-hole project's own blog media | Allowed and blocked queries as two mirrored tables with opposite per-row actions | Two mirrored tables instead of a status column |
| [`moodboard-blocked-pihole-dashboard.png`](ui-references/screenshots/moodboard-blocked-pihole-dashboard.png) | Blocked | The Pi-hole project's own blog media | Four KPI tiles over stacked 24-hour query charts | Blocked as a stacked series in one timeline plus a dedicated percentage tile |
| [`moodboard-blocked-technitium-dashboard.png`](ui-references/screenshots/moodboard-blocked-technitium-dashboard.png) | Blocked | `technitium.com` | Eleven colour-coded counter tiles above a multi-series chart | Counters carrying both an absolute number and its share; a legend that doubles as a series toggle |
| [`moodboard-domains-arkime-spigraph.png`](ui-references/screenshots/moodboard-domains-arkime-spigraph.png) | Domains | The `arkime.com` screenshot set | One row per destination, each with its own timeline and its own miniature map | Small multiples: identical marks repeated per host make hosts comparable |
| [`moodboard-domains-arkime-spiview.png`](ui-references/screenshots/moodboard-domains-arkime-spiview.png) | Domains | The `arkime.com` screenshot set | Collapsible field categories, each expanding into an inline cloud of values with counts | Top values as wrapped chips with counts rather than a table per field |
| [`moodboard-domains-malcolm-dns.png`](ui-references/screenshots/moodboard-domains-malcolm-dns.png) | Domains | The `cisagov/Malcolm` repository screenshot set | A light-theme DNS dashboard: client, server and answer tables, query-type and response-code tables, and a raw log grid | Ranking queried names by a computed randomness score — a derived column that turns a list into a detector |
| [`moodboard-domains-ntopng-flows.png`](ui-references/screenshots/moodboard-domains-ntopng-flows.png) | Domains | The ntopng user's guide | A dropdown-per-dimension filter row above a live flow table whose central column is one whole flow | **A maintainer preference.** One cell per flow; dropdowns instead of a search box |
| [`moodboard-domains-ntopng-host-dns.png`](ui-references/screenshots/moodboard-domains-ntopng-host-dns.png) | Domains | The ntopng user's guide | A per-host DNS breakdown table with inline two-tone proportion bars | The inline ratio bar inside a table cell |
| [`moodboard-empty-cloudflare-radar-skeleton.jpg`](ui-references/screenshots/moodboard-empty-cloudflare-radar-skeleton.jpg) | Empty | Live capture of `https://radar.cloudflare.com/traffic` during load | Every panel holding a grey placeholder in the shape of the content to come | An empty panel keeps its size and its title, so nothing reflows when data lands |
| [`moodboard-empty-figma-nothing.png`](ui-references/screenshots/moodboard-empty-figma-nothing.png) | Empty | `emptystat.es` | A dashed-outline placeholder shaped like the missing content, with "Nothing published yet" | A placeholder that mimics the shape of what is missing |
| [`moodboard-empty-getresponse-stats.png`](ui-references/screenshots/moodboard-empty-getresponse-stats.png) | Empty | `emptystat.es` | "No messages found in the selected time range / Use a different time range", with the range still visible above | Naming the *cause* instead of saying "no data" |
| [`moodboard-empty-invision-screens.png`](ui-references/screenshots/moodboard-empty-invision-screens.png) | Empty | `emptystat.es` | An empty project rendered as a centred prompt inside the frame the content will occupy | The empty state sized to its container rather than floating in a void |
| [`moodboard-empty-messages-web.png`](ui-references/screenshots/moodboard-empty-messages-web.png) | Empty | `emptystat.es` | A populated list rail beside a detail pane holding an illustration and a precondition note | An empty state that explains a precondition rather than apologising |
| [`moodboard-empty-reclaim-tasks.png`](ui-references/screenshots/moodboard-empty-reclaim-tasks.png) | Empty | `emptystat.es` | A heading, one sentence, and a large flat illustration filling the panel | Heading first, illustration second |
| [`moodboard-empty-slack-drafts.png`](ui-references/screenshots/moodboard-empty-slack-drafts.png) | Empty | `emptystat.es` | A small glyph, one explanatory sentence, and the single action that would fill the view | The minimal version, sized right for a small widget |
| [`moodboard-empty-tower-repos.png`](ui-references/screenshots/moodboard-empty-tower-repos.png) | Empty | `emptystat.es` | A first-run window whose empty area is itself a dashed drop target, with three ways in beneath | Turning the empty area into the affordance |
| [`moodboard-layout-akvorado-asn-sankey.png`](ui-references/screenshots/moodboard-layout-akvorado-asn-sankey.png) | Layout, map | The screenshot published with community dashboard 24189 on `grafana.com` | Destination AS down one side, source AS down the other, ribbons sized by volume, each end labelled with number and operator | **A maintainer preference.** Operator names resolved on the diagram itself, and a collapsed "Other" band for the long tail |
| [`moodboard-layout-arkime-parliament.png`](ui-references/screenshots/moodboard-layout-arkime-parliament.png) | Layout | The `arkime.com` screenshot set | Cluster cards grouped under headings, each carrying KPIs plus stacked coloured issue banners with an acknowledge button | Severity as a coloured banner stacked inside the card it belongs to, with the action in the same place |
| [`moodboard-layout-arkime-sessions.png`](ui-references/screenshots/moodboard-layout-arkime-sessions.png) | Layout | The `arkime.com` screenshot set | A brushable time histogram and a small choropleth in a header strip above a dense session table | Time filter and geography in the same row above the table |
| [`moodboard-layout-glance-main.png`](ui-references/screenshots/moodboard-layout-glance-main.png) | Layout | The `glanceapp/glance` repository | Three columns of stacked widgets in a monospace dark theme with uppercase micro-headings | Column-based rather than grid-based layout; monospace numerals that align themselves |
| [`moodboard-layout-grafana-opnsense-dense-grid.png`](ui-references/screenshots/moodboard-layout-grafana-opnsense-dense-grid.png) | Layout | The screenshot published with community dashboard 19366 on `grafana.com`; only generic NIC driver names are visible | A dense Grafana grid: four equal panels across, repeated down the page, with a variable picker row at the top | The grid read *as* a grid, which only explicit placement gives you; and a variable row as the canvas-wide scope control |
| [`moodboard-layout-homepage-demo.png`](ui-references/screenshots/moodboard-layout-homepage-demo.png) | Layout | The `gethomepage/homepage` repository | Translucent service rows over a wallpaper, each row showing two to four labelled metric cells | The service-row pattern, and translucency used to keep a dense grid calm |
| [`moodboard-layout-malcolm-host-resources.png`](ui-references/screenshots/moodboard-layout-malcolm-host-resources.png) | Layout | The `cisagov/Malcolm` repository screenshot set | Arc gauges and oversized numerals left, matching time series right | "State now" against "state over time" for the same metrics, side by side |
| [`moodboard-layout-malcolm-security-overview.png`](ui-references/screenshots/moodboard-layout-malcolm-security-overview.png) | Layout | The `cisagov/Malcolm` repository screenshot set | A long dark dashboard mixing a bar chart, summary and protocol tables and a word cloud | A panel grammar every panel obeys: plain title bar, one chart or one table, an export link at the foot |
| [`moodboard-layout-ntopng-dashboard.png`](ui-references/screenshots/moodboard-layout-ntopng-dashboard.png) | Layout | The ntopng user's guide | Three saturated full-width KPI banners over paired time-series and doughnut panels | A KPI banner whose colour itself encodes "nothing wrong" or "look at this" |
| [`moodboard-layout-ntopng-infra.png`](ui-references/screenshots/moodboard-layout-ntopng-infra.png) | Layout | The ntopng user's guide | Six KPI tiles, one wide traffic chart, then a row of three top-N lists | The tiles → hero chart → row-of-lists rhythm, which is the shape an overview wants |
| [`moodboard-layout-uidd-statistics-1121.png`](ui-references/screenshots/moodboard-layout-uidd-statistics-1121.png) | Layout | `uidesigndaily.com` — **an unbuilt Figma concept** | White cards with one inverted dark hero card, a metric row, a list, and an inline "no alerts set" strip | One inverted card to make a figure dominant without enlarging it; an empty state as a full-width inline strip |
| [`moodboard-layout-uptimekuma.jpg`](ui-references/screenshots/moodboard-layout-uptimekuma.jpg) | Layout | `uptime.kuma.pet` | A monitor list rail with per-row uptime badge and heartbeat bar, beside a detail pane | The heartbeat bar as compact per-row history; list rail plus detail pane for "many objects, one selected" |
| [`moodboard-map-arkime-connections.png`](ui-references/screenshots/moodboard-map-arkime-connections.png) | Map | The `arkime.com` screenshot set | A force-directed connection graph with a docked node-detail panel and a weight selector in the toolbar | **A maintainer preference.** Node size and colour driven by a named, user-chosen weight; a docked panel instead of tooltips |
| [`moodboard-map-malcolm-conn-tree.png`](ui-references/screenshots/moodboard-map-malcolm-conn-tree.png) | Map | The `cisagov/Malcolm` repository screenshot set | The same root drawn twice, as a from-destination and a from-source tree, with depth and colour controls | **A maintainer preference.** Two mirrored trees from one root; tree depth exposed as a control |
| [`moodboard-map-malcolm-latlon.png`](ui-references/screenshots/moodboard-map-malcolm-latlon.png) | Map | The `cisagov/Malcolm` repository screenshot set | A geo map of destination bytes, dots sized and coloured by volume, with a left rail of sibling dashboards | The persistent left rail beside the map, so the map is one view of a set |
| [`moodboard-map-netbox-topology-dark.png`](ui-references/screenshots/moodboard-map-netbox-topology-dark.png) | Map, theme | The `netbox-community/netbox-topology-views` repository | A device-icon node graph in the dark theme with active filters as removable chips above the canvas | Filters as dismissible chips on the canvas, so a diagram states what it is showing |
| [`moodboard-map-netbox-topology-light.png`](ui-references/screenshots/moodboard-map-netbox-topology-light.png) | Map, theme | The `netbox-community/netbox-topology-views` repository | The identical topology in the light theme | One link-colour palette surviving both themes when only ground and fills change |
| [`moodboard-map-ntopng-geo.png`](ui-references/screenshots/moodboard-map-ntopng-geo.png) | Map | The ntopng user's guide | A pale basemap with small dots and a click-out card carrying address, name, country and scores | **The maintainer's preferred world map.** Map as background, data as foreground |
| [`moodboard-map-vizceral.png`](ui-references/screenshots/moodboard-map-vizceral.png) | Map | The `Netflix/vizceral` repository | About a hundred services with animated particles on every edge, and a region breadcrumb | **A maintainer rejection.** Avoid: at this density, motion stops carrying information and becomes texture |
| [`moodboard-theme-dashy-configurator.gif`](ui-references/screenshots/moodboard-theme-dashy-configurator.gif) | Theme | The `Lissy93/dashy` theming documentation | The theme configurator recolouring a dashboard live from the page header | A theme picker as a control on the page it affects, with no preview round trip |
| [`moodboard-theme-dashy-themes.png`](ui-references/screenshots/moodboard-theme-dashy-themes.png) | Theme | The `Lissy93/dashy` theming documentation | Eighteen themes as a contact sheet of the same dashboard | Themes presented as live previews of the user's own page |
| [`moodboard-theme-flame-themes.png`](ui-references/screenshots/moodboard-theme-flame-themes.png) | Theme | The `pawelmalak/flame` repository | One start page sliced diagonally into four themes | The comparison device itself: four variants of one screen in one image |
| [`moodboard-theme-glance-themes.png`](ui-references/screenshots/moodboard-theme-glance-themes.png) | Theme | The `glanceapp/glance` repository | Six themes as a three-by-two grid of identical dashboards | A theme set defined by a handful of tokens, with the layout never moving between variants |

## The maintainer's recorded preferences

**This section is the point of the cycle.** The first mockup attempt was lost
because the design brief was one line and the judgement was therefore a coin
flip. What follows is the maintainer's own stated preference, per question,
recorded so the mockup cycle has a written reference to be judged against rather
than a taste to guess at. Each preference names the image it refers to and one
line on what is worth taking — including, for the one rejection, what is worth
avoiding.

| Question | Preference | Image | What to take from it |
|---|---|---|---|
| **Charts, histograms, pie charts** | **Grafana, clearly above the others**, in quality and in granularity. It is the reference for anything that plots a value. The reason, stated in his own terms: **Grafana is the only one that does not have that over-smoothed quality, and it is very granular.** | `ui-references/screenshots/grafana-community-traffic-analysis-asn-light.png`, `ui-references/screenshots/familya-grafana-panel-editor.png`, `ui-references/screenshots/grafana-community-opnsense-suricata-signatures.png` | **Show the resolution the data actually has.** Most dashboard products round their curves and thin their series; Grafana draws what is there, and that granularity is the point rather than a side effect. Practically: no curve smoothing, no silent downsampling that hides a spike, a tooltip that lists the full stacked breakdown at one instant, and `fieldConfig.overrides`-class control over how each series renders. |
| **Flow visualisation** | **ntopng's flow view is outstanding**, and so is **Akvorado**, in particular its ASN Sankey. | `ui-references/screenshots/moodboard-domains-ntopng-flows.png`, `ui-references/screenshots/familyb-ntopng-top-flow-talkers.png`, `ui-references/screenshots/moodboard-layout-akvorado-asn-sankey.png` | ntopng: a flow rendered as **one cell** (host : port ⇄ host : port) under a dropdown-per-dimension filter bar. Akvorado: a Sankey whose ends carry the **resolved operator name**, with an honest collapsed band for the tail. |
| **Per-device view** | **Firewalla's per-device map of activities and flows** — liked a lot. | `ui-references/screenshots/familyb-firewalla-activities-flows.png`, `ui-references/screenshots/familyb-firewalla-devices-list.png` | One subject, one time window, two renderings behind a single toggle: human-readable *activity* on one side, raw *flows* on the other. The catalogue currently splits this across two entries and probably should not. |
| **World map** | **ntopng's**, over every other map captured. | `ui-references/screenshots/familyb-ntopng-geomap.png`, `ui-references/screenshots/moodboard-map-ntopng-geo.png` | A de-saturated basemap with small point marks, everything interesting in the popup, no choropleth and no arcs. The map is background; the data is foreground. |
| **Connection tree** | **Malcolm and Arkime are both very good.** | `ui-references/screenshots/moodboard-map-malcolm-conn-tree.png`, `ui-references/screenshots/moodboard-map-arkime-connections.png` | Malcolm: two mirrored trees from one root, with tree depth exposed as a control. Arkime: node size and colour driven by a named, user-chosen weight, with a docked detail panel instead of tooltips. |
| **Connection tree — rejected** | **Vizceral: liked less. Too cluttered.** | `ui-references/screenshots/moodboard-map-vizceral.png` | **Avoid**: animated particles on every edge at high node count stop carrying information and become texture. Motion-as-volume works at ten nodes and fails at a hundred; do not adopt it for the segment graph. |
| **Placing, moving and resizing tiles** | **Homarr's approach** — more freedom, and less childish than the alternatives. This is the reference for the canvas mechanics. | `ui-references/screenshots/familya-homarr-board-layout-large.png`, `ui-references/screenshots/familya-homarr-layout-settings.png` | Heterogeneous widget sizes on one grid, collapsible category sections as the structural unit, and — the mechanism — **named responsive layouts, each with its own breakpoint and column count, with every item's position stored per layout**. |
| **Homarr's layout, as a shape** | **Too rounded for his taste — but close to what he wants to reach.** | same two images | **Take the structure, not the shape language.** The arrangement, the density and the grid are close to the target; the corner radius is not. |
| **Editing a card's content** | **Home Assistant's minimalism.** Syntax highlighting is enough; a full IDE embedded in a browser is not wanted. | `ui-references/screenshots/familya-homeassistant-card-yaml-editor.jpg`, `ui-references/screenshots/familya-homeassistant-raw-config-editor.jpg` | A small highlighted text pane with a live preview beside it and a link back to the visual editor. **No file tree, no minimap, no command palette, no diff viewer.** This is the calibration point for how much editor is enough. |
| **General layout** | **Cloudflare's** — and the reason matters: **angular cards, like his own personal projects.** | `ui-references/screenshots/demo-cloudflare-radar-overview.jpg`, `ui-references/screenshots/moodboard-empty-cloudflare-radar-skeleton.jpg` | A left navigation rail, a scoped header carrying the selectors, and angular cards with a one-line explanation under each title. Consistent with the industrial palette's `0.25rem` radius, and against the rounded UniFi aesthetic `ROADMAP.md` step 3 specifies. |
| **Configuration interface** | **Open. He is waiting for the next pass before judging.** | — | Nothing is decided. `docs/dashboard-format.md` proposes the two coordinated surfaces; whether that is the right shape is not yet answered, and this document does not pretend it is. |

**The split that matters.** Two of these preferences point at two different
products for two different jobs, and that is deliberate rather than
inconsistent: **Homarr for the grid, Home Assistant for the editor.** `opnview`
takes its layout interaction from one product and its editing interaction from
another. The consequence is worked through in *Conclusions for opnview*, because
it is more than a preference — it is a combination nobody in this survey has
actually built.

## Conclusions for opnview

Definite answers, not a survey. Four questions, each answered in sentences that
commit.

### What a widget references instead of a local identifier

**A typed reference object carrying a `kind`, the name of the natural key it is
expressed in, and that key's value — never a local database id, and never a
generated universal identifier.**

Concretely, `{"kind": "interface", "by": "identifier", "value": …}`,
with the full vocabulary and the per-kind key list in
`docs/dashboard-format.md`. Seven kinds exist. Three of them —
`provider` (the pair of a provider kind and a provider key, such as a resolver
implementation), `country` (an ISO 3166-1 alpha-2 code) and `operator` (an AS
number) — are **globally meaningful and travel intact**. Four of them —
`interface`, `client`, `rule` and `site` — name things that exist on one
installation and may not exist on another, and `opnview` **does not pretend
otherwise**.

The reasoning is Family A's, not this document's invention. Kibana states it
directly: a randomly generated saved-object id makes a dashboard fragile, while
a human-readable id "makes the data view easier to recreate with the same ID
across spaces, deployments, or environments" so that dashboards referencing it
keep working
(https://www.elastic.co/docs/explore-analyze/find-and-organize/data-views). Home
Assistant is the counter-example that proves it: `entity_id` travels because a
human wrote it, while `device_id`, `area_id` and user ids are opaque local
UUIDs, and a dashboard carrying them **does not error — it silently mis-targets
or silently hides content**
(https://github.com/home-assistant/home-assistant.io/blob/current/source/dashboards/views.markdown).
A silent wrong answer about a firewall is worse than a visible failure, so
`opnview` uses natural keys and accepts that some of them will not resolve.

A reference may also carry a `label` — the exporting installation's own name for
the thing — **for display only.** It is never matched on. `opnview` never infers
an interface's nature from what it is called, and that rule holds in the dashboard
format exactly as it holds in the schema.

### What happens when a reference cannot be resolved locally

**The widget is kept. It renders empty, in its full footprint, and the empty
state names the reference that failed and offers to repair it. Neither an error
screen nor a fabricated zero is acceptable.**

A zero would be a lie: it asserts "we looked and there was none" when the truth
is "we could not look". This is the same distinction the schema already draws —
`source_availability` exists precisely so an absent source is a modelled
condition rather than an absence of rows, and a geolocation cache miss is a row
whose `lookup_state` is `miss` rather than a missing row. An unresolved
reference is that idea applied to the dashboard file.

**Repair happens through two coordinated surfaces that edit the same model, and
neither is the "real" one.**

1. **A JSON/YAML editor that highlights the offending lines in place.** The file
   as text, with the exact lines whose reference failed marked, each carrying an
   inline "no data" notice naming the reference and why it did not resolve. The
   highlight clears when the reference resolves.

2. **A no-code selector whose dropdowns are populated from what this
   installation actually has** — the interfaces the firewall discovered, the
   clients seen in leases and flows, the rules in the running ruleset, the
   providers in the registry. The unresolved value is shown as the current
   selection, marked as not found, beside the choices that do exist.

**How much editor is enough is now settled, and it is not much.** The
maintainer's stated preference is Home Assistant's minimalism: a small
syntax-highlighted pane with a live preview beside it and a link back to the
visual editor. **No file tree, no minimap, no command palette, no diff viewer,
no embedded IDE.** Grafana's "Edit as code" drawer is the right *shape* — code
and rendering on one screen with an explicit apply step
(https://grafana.com/docs/grafana/latest/dashboards/build-dashboards/modify-dashboard-settings/)
— but it is heavier than wanted; Home Assistant's per-card YAML pane is the
right *weight*
(https://github.com/home-assistant/frontend/blob/dev/src/panels/lovelace/hui-editor.ts).

The repair flow itself copies Kibana, which is the only product in Family A
whose import treats a dangling reference as a first-class, user-resolvable
condition: it reports `missing_references` with the affected objects, prompts the
user to point each at something that exists, and refuses to commit until they
do
(https://www.elastic.co/docs/api/doc/kibana/v9/operation/operation-post-saved-objects-import,
https://www.elastic.co/docs/api/doc/kibana/operation/operation-post-saved-objects-resolve-import-errors).
`opnview` takes the reporting and the remapping and **deliberately declines the
refusal to commit**: the dashboard imports, broken parts visibly broken, because
a user who cannot see the dashboard cannot repair it.

Three things repair does **not** do, each stated because the temptation is real:
it does not rewrite references automatically by fuzzy-matching labels; it does
not discard the authored value before the user chooses a replacement; and it
does not block the import.

### How the format is versioned

**A single integer `format_version` at the top of the file, currently `1`,
versioning the file format and nothing else** — not `opnview`'s release number,
not the schema version in `schema_version`, not the widget catalogue. Each build
declares the version it writes and the minimum it can read.

**An older file meeting a newer `opnview`** is migrated forward on load, through
an ordered ladder of one-version steps, **one-way**, **in memory rather than on
disk**, with a **floor** below which a file is refused rather than guessed at.
That is Grafana's `DashboardMigrator` shape
(https://github.com/grafana/grafana/blob/main/public/app/features/dashboard/state/DashboardMigrator.ts)
with Kibana's version floor
(https://www.elastic.co/docs/api/doc/kibana/v9/operation/operation-post-saved-objects-import).

**A newer file meeting an older `opnview`** is refused, cleanly and completely,
with a message naming the file's version, the highest this build understands and
the fact that an upgrade is the fix. That is the deliberate opposite of the
tolerant posture taken everywhere else, and the reason is that an unknown
*parameter key* is inert data in a widget that otherwise works, whereas an
unknown *format version* says the document's structure may have changed in ways
this build cannot see.

**The integer will be turned rarely, and the evidence says it can be.** Four of
the eight Family A products carry no format version at all — Home Assistant,
Dashy, Homepage and Datadog — and all four have evolved for years on tolerant
readers plus deprecation in place. Home Assistant is the sharpest case: its
canonical config interface has no `version` key, and the only versioned artefact
is the storage envelope, which has never been migrated
(https://github.com/home-assistant/frontend/blob/dev/src/data/lovelace/config/types.ts,
https://github.com/home-assistant/core/blob/dev/homeassistant/components/lovelace/dashboard.py).
Kibana is the sole sophisticated exception, versioning **per saved-object type**
through `modelVersions` explicitly decoupled from the product version
(https://www.elastic.co/docs/extend/kibana/saved-objects/migrations).
`opnview` keeps the integer because an eventual structural change is cheaper to
handle with one than without — not because it expects to use it often.

### What an import does with a malformed or dangling file

**Two kinds of validity, treated deliberately differently.**

**Structural validity is fail-closed.** A document that is not parseable, that
lacks `format_version` or `canvases`, that has a canvas with no id or title, a
widget with no id, type or placement, a duplicate id within its scope, or a
non-integer placement, is **refused whole** — nothing imported, with a message
naming the offending canvas index, widget index and field. Datadog's positional
error is the model
(https://github.com/DataDog/terraform-provider-datadog/issues/1769), improved by
naming the field as well as the index. A partial import of a structurally broken
file is never performed.

**Referential validity is fail-open.** Every unresolved reference — an unknown
widget type, an `interface`, `client` or `rule` reference matching nothing locally,
an unrecognised parameter key, a parameter value outside the known vocabulary —
is **collected and reported, and the import proceeds**. The result is a
dashboard the user can see, with the broken parts visibly broken and repairable.
Grafana's per-panel behaviour is the precedent: an unknown panel plugin renders
an error tile in its own slot while every other panel keeps working
(https://github.com/grafana/grafana/blob/main/public/app/features/panel/components/PanelPluginError.tsx),
as is Home Assistant's per-card error element
(https://github.com/home-assistant/frontend/blob/dev/src/panels/lovelace/create-element/create-element-base.ts).

One import report is shown afterwards, listing every collected condition grouped
by canvas and widget, each linking to the widget it affects. Netdata's
dashboard-template chooser, which annotates each template with how many of its
charts the current data can actually fill, is the friendliest version of that
idea and is worth copying for the "what will this dashboard actually show me?"
question before an import rather than after
(https://learn.netdata.cloud/docs/dashboards-and-charts/tabs/dashboards).

### The design `opnview` is actually building, and the risk in it

**Eight things taken from seven products, and none of them copied whole:**

- **Homarr's layout structure and grid interaction** — heterogeneous tiles on a
  real grid, named responsive layouts with per-breakpoint column counts, items
  positioned per layout. The maintainer's reference for adding, moving and
  resizing. Its rounding is explicitly excepted.
- **Cloudflare Radar's composition, and its angularity** — a left rail, a scoped
  header carrying the selectors, angular cards each with a one-line explanation.
- **The industrial palette's shape language and tokens** — a `0.25rem` radius,
  system fonts only, high contrast in both themes, and a fixed bar plus
  collapsible sidebar plus centred main column.
- **Home Assistant's minimal editor** — a small highlighted text pane with a
  live preview, and nothing else.
- **Grafana's granular, unsmoothed charting** — the resolution the data actually
  has, no rounded curves, no silent downsampling, and per-series rendering
  control.
- **ntopng's flow view and world map** — a flow as one cell under a
  dropdown-per-dimension filter bar; a de-saturated basemap with point marks and
  the detail in a popup.
- **Firewalla's per-device activity map** — one subject, one window, activity
  and flows behind a single toggle.
- **Malcolm's or Arkime's connection tree**, and **Akvorado's ASN Sankey** —
  mirrored trees with a depth control, a named weight driving node size, and a
  Sankey whose ends carry resolved operator names.

**Rejected, and recorded as such: Vizceral's animated particle graph, as too
cluttered.**

### Grafana's query model, on one local database

The maintainer's charting preference has a structural consequence that is worth
working out here rather than discovering in step 5, because it decides the shape
of the HTTP API.

**A Grafana panel is a visualization over `targets`, an array of queries** — one
panel, several series, each series its own query, each query naming its own data
source as a `{type, uid}` reference
(https://grafana.com/docs/grafana/latest/dashboards/build-dashboards/view-dashboard-json-model/).
That is exactly the shape `docs/widget-catalogue.md`'s *Custom chart* needs: the
maintainer's example of plotting temperature and CPU against interface
throughput on one chart is three series from three different places, and a
widget that hard-codes which places would not be able to express it.

**The difference is that `opnview` has one data source, not many, and it is a
local SQLite file.** Three things follow, and none of them is a problem.

- **The `{type, uid}` datasource reference disappears entirely.** Grafana needs
  it because a dashboard can target Prometheus, Loki and Elasticsearch at once;
  it is also, per *Portability* above, the single largest thing that breaks when
  a Grafana dashboard moves between installations, and the whole `__inputs` and
  `${DS_*}` machinery exists to paper over it. `opnview` has nothing to name, so
  **a whole class of portability failure simply does not exist for it** — a
  series names *what* it plots, never *where from*. That is a real, unearned
  advantage of being a single binary over one database, and it is worth
  recognising rather than accidentally reintroducing.
- **What replaces the datasource reference is the `source` field of a series** —
  `firewall_metric`, `interface_throughput`, `flow_volume`, `security_event` and
  so on: a closed vocabulary of *what kind of thing* is being plotted, resolved
  by `opnview` to a table and a projection it owns. The user never writes SQL,
  and the dashboard file never contains any, which also means **a dashboard file
  is never executable input**: it is pasteable into a forum post without anyone
  having to think about it.
- **One query per series, joined on the time bucket at the presentation layer.**
  The catalogue says this explicitly for *Custom chart*, and the reason is that
  a telemetry sample table and a volume aggregate have no join key in common
  beyond time. That is only safe if **both families bucket on the same
  boundaries**, which is why gap G9's roll-up design is specified to align with
  the existing volume aggregates' period boundaries rather than inventing its
  own. Getting that wrong would produce charts that are subtly, unfalsifiably
  wrong about simultaneity, which is worse than charts that refuse to draw.

The consequence for step 5's API is in the amendment below: **one endpoint per
widget type taking that widget's declared parameters**, which for a multi-series
chart means the endpoint accepts a list of series and returns a list of series,
each carrying its own availability state so a single missing source degrades one
line rather than the whole chart.

**And here is the finding that matters most, stated here rather than buried in a
product subsection: no product in this survey does what `opnview` has decided to
do.** Homarr has the free grid and **no code representation of a card at all** —
its configuration is entirely graphical, there is no file, no JSON view and no
YAML view, and it has therefore **never had to solve the consistency problem
between a graphical editor and a text one**
(https://homarr.dev/blog/2024/09/23/version-1.0/). Home Assistant has both
surfaces over one model and is the only one of the two that has actually faced
keeping them in step — including where it went wrong, which is worth naming: a
YAML-mode dashboard is read-only from the UI by construction
(https://github.com/home-assistant/core/blob/dev/homeassistant/components/lovelace/dashboard.py),
a UI write silently discards YAML comments, and concurrent edits are handled by
a toast rather than a lock
(https://github.com/home-assistant/frontend/blob/dev/src/panels/lovelace/hui-editor.ts).
But its grid is far more constrained than Homarr's.

**`opnview` is combining a Homarr-class free grid with a Home-Assistant-class
code representation of every widget, plus a no-code selector, all over one
model. That combination is unproven prior art.** The nearest thing to it is
Grafana's "Edit as code" drawer beside a live dashboard — and Grafana pays for
it with a `schemaVersion` ladder forty-two rungs long, a v2 schema that is a
one-way trapdoor
(https://grafana.com/whats-new/2025-05-05-dashboard-v2-schema-and-dynamic-dashboards/),
and a provisioning model in which file and database silently fight
(https://grafana.com/docs/grafana/latest/administration/provisioning/). **The
risk of the combination lands on this project, not on a reference that can be
copied.** That is not an argument against the decision; it is the honest price
of it, and it is recorded so nobody is surprised later.

Two things follow practically. First, **the model has to be designed before
either surface is**, because both are renderers over it and the failure mode of
getting that backwards is Grafana's provisioning trap. Second, **the format's
tolerant-reader posture is doing more work here than in any of the references**,
because a free grid plus arbitrary per-widget parameters is a larger surface to
keep compatible than either product faces alone.

## For maintainer review, not claimed as verified

Listed honestly rather than dressed up. These are judgements this document
cannot make about itself, and a reviewer should not read its own confidence as
evidence.

1. Whether Family A's findings are deep enough to be useful — whether the
   portability sections explain what breaks rather than restating that things
   break.
2. Whether Family B's criticisms are fair and specific, especially "where it
   buries a view".
3. Whether the catalogue covers the questions the maintainer actually has,
   beyond the six he named.
4. Whether the proposed reference model is the right trade for this product —
   and in particular whether accepting unresolvable references is better than
   the alternatives.
5. Whether the ROADMAP amendment cuts deeply enough.
6. Whether the screenshots actually help judge the user experience.
## Proposed ROADMAP amendment

`ROADMAP.md` **was not edited by this cycle**, and nothing below is applied. The
spec puts editing it out of scope, and the maintainer arbitrates. **No
renumbering is proposed**: renumbering has already cost this project once, and
every amendment below is a change of content inside an existing step, never a
change of step numbers or of step count. The plan stays at eight steps, in the
same order, with the same numbers.

### 1. Step 7's seven-screen structure

**Contradicted text**, `ROADMAP.md`, step 7 — "The seven screens, served by the
binary", followed by the numbered list Overview, Matrix, Segment, Device,
Blocked, Alerts, Map, and the step-table deliverable "7 screens served by the
binary".

**Why it is contradicted.** Settled decision 1 replaces fixed screens with named
canvases the user composes from widgets. A fixed seven-screen list is not a
smaller version of that; it is a different product.

**Proposed replacement.** Step 7 becomes *"The canvas frontend, served by the
binary"*, delivering: the widget catalogue of `docs/widget-catalogue.md`
implemented as placeable widgets; named canvases with tabs; the dashboard file
format of `docs/dashboard-format.md` with import, export and the two repair
surfaces; and the theme system of Family C. The seven queries in
`sql/queries/screens.sql` **remain authoritative and are not deleted** — they are
the query work step 7 was really carrying, and every widget states which of them
it reuses, adapts or replaces. The step-table deliverable becomes *"Canvases,
widgets, dashboard import/export"*.

### 2. Step 3's "light by default, dark never the default"

**Contradicted text**, `ROADMAP.md`, step 3 — "light background by default …
Dark mode available, never the default."

**Why it is contradicted.** Settled decision 5 is that the theme on first launch
**follows the operating system**, user-overridable afterwards. Family C adds a
detail that makes the old rule actively wrong: the maintainer's own industrial
palette implements exactly the OS-following behaviour and has **no manual
override at all**, so "dark never the default" contradicts the default palette's
own code.

**Proposed replacement.** *"On first launch the theme follows the operating
system's preference. The user can override it afterwards, and the override
persists. The maintainer's industrial palette is the default; Tokyo Night,
Dracula, Nord, Rosé Pine and Catppuccin are offered as named options at the
values published on the maintainer's own site, https://lequellec.xyz. Nord's upstream
project publishes no light variant, but the maintainer's site defines one, so
Nord is light-capable here."*

### 3. Step 3's UniFi aesthetic brief

**Contradicted text**, `ROADMAP.md`, step 3 — "UniFi Network as the aesthetic
reference: light background by default, **rounded cards**, generous spacing, a
single accent colour, soft area charts, numbers brought forward."

**Why it is contradicted — and it is now contradicted twice over, which is why
this is the strongest of the four amendments.**

*First, by the maintainer's own palette.* Family C reproduces it: the corner
radius is **`0.25rem`**, four pixels at a default root size, which is a chamfer
rather than a curve. The palette's own comments call it an "Industrial
high-contrast palette". Making that palette the default while specifying
rounded UniFi cards asks the product to be two things at once.

*Second, by the maintainer's stated preferences, recorded above.* He has now
seen both aesthetics side by side and chosen: **the general-layout preference is
Cloudflare Radar's, explicitly for its angular cards, "like his own personal
projects"**. And when shown Homarr's layout — which he otherwise names as his
structural reference — his objection was precisely that **it is too rounded**,
while being "close to what he wants to reach". A rounded aesthetic has therefore
been rejected once in the abstract and once against a specific product he
otherwise likes.

**Proposed replacement.** *"The visual reference is angular, dense and
high-contrast: the maintainer's industrial palette, whose corner radius is
`0.25rem`, whose fonts are system fonts only, and whose layout is a fixed top
bar, a collapsible sidebar and a centred main column. Cloudflare Radar is the
compositional reference — a left rail, a scoped header carrying the selectors,
and angular cards each with a one-line explanation under its title. Homarr is
the structural reference for how tiles are arranged and resized, its rounding
excepted. Generous spacing, a single accent colour, soft area charts and numbers
brought forward are retained from the original brief. Banned, unchanged:
'cyber-defence' aesthetics, walls of dense tables, and empty panels with no
explanation."* The last of those three bans is reinforced rather than
weakened — Family D's empty-state group exists to make it achievable. One clause
of the original brief is **not** retained; see the next amendment.

### 4. Step 3's "soft area charts"

**Contradicted text**, `ROADMAP.md`, step 3 — "…a single accent colour, **soft
area charts**, numbers brought forward."

**Why it is contradicted. This is the third time step 3's visual brief is
contradicted, and by a different argument from the other two.** The maintainer's
charting preference, recorded above, is Grafana, and his reason is specific:
**Grafana is the only one that does not have that over-smoothed quality, and it
is very granular.** "Soft area charts" is precisely the smoothed treatment he is
rejecting. Most dashboard products round their curves and thin their series to
look calm; the cost is that a one-sample spike disappears, and a one-sample
spike in a firewall log is often the whole story. **Granularity is what he
wants, and softness is what removes it.**

**Proposed replacement.** *"Charts show the resolution the data actually has.
No curve smoothing, and no silent downsampling that can hide a single-sample
spike — where a series is aggregated to fit the pixels available, the widget
says so. Area fills are retained as a way of separating stacked series, but the
line on top of a fill is drawn through its real points. A tooltip on a stacked
chart lists the full breakdown at the hovered instant rather than the top series
only. Grafana is the reference for this, and for the degree of per-series
rendering control a panel should expose."*

### 5. The shape of step 5's HTTP API

**Contradicted text**, `ROADMAP.md`, step 5 — "HTTP API for the screens, period
selector everywhere."

**Why it is contradicted.** "For the screens" presupposes the seven fixed
screens amendment 1 removes. A canvas of user-composed widgets needs an API
shaped per **widget type and parameter set**, not per screen; and the period is
a *widget parameter* carried in the dashboard file rather than a global control,
because two widgets on one canvas will legitimately show different periods.

**Proposed replacement.** *"HTTP API for the widget catalogue: one endpoint per
widget type, taking that widget's declared parameters — period, scope,
interface, client and the rest — as documented in `docs/widget-catalogue.md`, and returning
both the data and the source-availability state each widget must render. A
canvas is served by issuing one request per widget. The period is a parameter of
a widget rather than a property of a screen."*

### 6. Anything else this research directly invalidates

Three smaller items, listed because the spec asks for whatever the research
invalidates beyond the four mandated ones.

- **Step 8's README screenshot list.** Step 8 promises "README: problem solved,
  screenshots". With canvases replacing screens there is no canonical set of
  screens to screenshot. Proposed: *"README screenshots show representative
  canvases, with the dashboard files that produced them included in the
  repository as examples."* That has a second benefit — the example files
  exercise import on every clone.

- **Step 4's setup wizard.** It lists "URL, API key/secret, MaxMind key" and
  says nothing about the theme. Given decision 5, proposed: *"and the theme,
  which defaults to following the operating system and can be changed at any
  time from the interface rather than only in the wizard."*

- **`sql/schema-checks.sh`'s PN-AC26 now fails, and it is a scope defect rather
  than a real violation. This cycle could not fix it without breaking its own
  change set, so it is reported instead of worked around.** That check greps
  `migrations/`, `sql/` **and the whole of `docs/`** for the strings
  `crowdsec|zenarmor|sensei|snort|wazuh|ntopng`, and its own comment states its
  purpose: "nothing anywhere is named for a provider that is not installed and
  surveyed today", the list being "every security or visibility product the
  roadmap discussion raised and the survey did not cover". Its intent is to stop
  a **speculative provider abstraction** being built for a product nobody has
  surveyed — a real and valuable guard, and `docs/architecture.md` is explicit
  about why it exists.

  The collision is that `specs/SPEC-ui-and-dashboard-format-research.md`
  **requires** this document's Family B to carry one subsection for each of
  seven named products, ntopng and Zenarmor among them. Naming a product one is
  studying is the opposite of building an abstraction for it: **nothing in this
  cycle adds a table, a column, an index, a `CHECK` value, a registry row or a
  provider key for either product**, and PN-AC7, PN-AC20 and the rest of the
  provider-neutrality checks all still pass. The check is asserting a property
  of the *model* by grepping *prose*, and it was written when `docs/` held only
  model documents.

  Two candidate fixes, neither applied here. The narrow one: exclude
  `docs/ui-references.md` from that grep, the same way the script already
  excludes itself from its own grep for exactly this reason — "it is the file
  that has to name those strings in order to forbid them". The better one: drop
  `docs/` from the grep entirely and assert the property where it actually
  lives — the live schema's object and column names, which PN-AC20 already
  queries directly from `sqlite_master`. **This cycle applied neither**, because
  `specs/SPEC-ui-and-dashboard-format-research.md` AC2 pins the change set to
  five paths and `sql/schema-checks.sh` is not one of them. It is the
  maintainer's call, and until it is made `docker compose run --rm
  schema-checks` exits 1 on this branch while `docker compose run --rm checks`
  exits 0.

- **The unstated assumption that a screen equals a query.** Now that every widget
  states which of the seven queries it reuses, adapts or replaces, the
  catalogue's *Gaps found* table names **eight** model gaps — the two the spec
  already knew about plus six the mapping turned up. Proposed as a new sentence
  in step 2's *Outcome*: *"Step 3's widget catalogue subsequently identified
  eight gaps between the model and the questions the widgets ask; they are
  tabulated in `docs/widget-catalogue.md` and each is a future migration."*

### 7. A decision already taken: the "single Go binary" rule is replaced

**This one is not a proposal.** The maintainer has taken the decision; it is
recorded here so that whoever applies it knows exactly what changes and where,
and because it directly unblocks the telemetry gaps G9 and G10 that
`docs/widget-catalogue.md` opens.

**Contradicted text, in three places, all saying the same thing and none of them
justifying it:** `README.md` line 21, `CLAUDE.md` line 26, and `ROADMAP.md`
line 212 — the rule that `opnview` is a *single Go binary*. It is an inherited
assumption rather than a reasoned constraint, and it was on the point of
deciding the storage engine for telemetry for no stated reason.

**Its replacement, which keeps what the rule actually protected:**

> One service, installed and updated by a single command, with no external data
> store to provision. Storage engines are embedded libraries, not servers.

**What that permits and forbids**, stated so the rule is enforceable rather than
a slogan:

- **Permitted.** SQLite, unchanged and still the store for everything the
  current schema holds. **DuckDB becomes admissible** — it is a library rather
  than a server, and it is not a speculative choice: **OPNsense itself already
  ships it**, as the store behind the Unbound query report, which
  `docs/opnsense-api-survey.md`'s own cited source demonstrates — the logger
  imports `duckdb`, writes to `/var/unbound/data/unbound.duckdb`, and performs a
  periodic export-and-reimport because "duckdb database files don't like records
  to be deleted over time"
  (https://raw.githubusercontent.com/opnsense/core/26.7.3/src/opnsense/scripts/unbound/logger.py).
  A Prometheus-style time-series store written in pure Go is admissible on the
  same test.
- **Forbidden.** PostgreSQL, TimescaleDB, InfluxDB and VictoriaMetrics. Not
  because of their merits, but because each would make someone installing a
  network-visibility tool into an unprivileged LXC administer a separate
  database — which is precisely the burden the original rule existed to
  prevent, and the only part of it worth keeping.

**The distinction that made this tractable, and the reason it is not a loosening
of discipline: several storage engines is not several processes.** SQLite and
DuckDB are libraries linked into the binary. The install story, the update
story, the systemd unit and the backup story are all unchanged; what changes is
that a columnar or time-series engine may sit beside the row store for data
whose shape suits it — which is exactly what the telemetry family described in
`docs/widget-catalogue.md` needs, and why the existing four volume aggregates are
the wrong vehicle for it.

The containerised-toolchain cycle left this open deliberately rather than by
accident: `CGO_ENABLED` was pointedly not set to `0` and a C toolchain is in the
image, so the driver choice stayed available. That decision is now used.

**One consequence for scope, also decided.** Telemetry storage gets **its own
research cycle, after the mockup**, covering the API survey of the health
endpoints and the storage-engine choice together. So the health widgets in
`docs/widget-catalogue.md` **stay marked as missing from the model** — this
cycle names G9 and G10 precisely enough to give that later cycle a starting
point, and deliberately does not resolve them.

**Nothing was edited.** `ROADMAP.md`, `README.md` and `CLAUDE.md` are untouched
by this cycle, as the spec requires and as `git status` will show; the three
file-and-line references above are the change set for whoever applies it.

### What is explicitly not proposed

- **No renumbering**, and no change to the step count or order.
- **No change to the five data sources, the polling plan or the read-only
  rule.** Nothing in this research touches them.
- **No change to the observation-point rule.** It is reinforced: Family B shows
  Firewalla hitting the same limit, and ntopng's practice of explaining an empty
  menu section is the concrete way to satisfy it.
- **No edit to `ROADMAP.md` itself.** The maintainer arbitrates every item
  above.


## References

Every `https://` URL that appears in the body of this document, with the title
of what it points at, and nothing that does not appear in the body. **Both
directions were checked mechanically**: the list was generated from the body's
own URLs, so a reference here that is not cited above cannot exist, and a
citation above that is missing here cannot exist either.

Three notes on what is in this list. **Source files are titled by their
filename and repository**, because that is what they are and because a page
title would be less useful for finding the line a claim rests on. **Entries
marked `UNVERIFIED` are community or third-party sources**, admitted only where
no vendor documentation covers the claim, and flagged as such at the point of
use as well as here. And **the image sources of Family D are deliberately not
in this list**: images are exempt from the citation discipline, their provenance
is recorded next to each image and in the complete image index, and mixing them
in here would blur exactly the distinction *Scope and method* draws.

### Grafana
- `api.go` — source in grafana/grafana — https://github.com/grafana/grafana/blob/main/pkg/services/dashboardimport/api/api.go
- `constants.ts` — source in grafana/grafana — https://github.com/grafana/grafana/blob/main/public/app/core/constants.ts
- `dash_template_evaluator.go` — source in grafana/grafana — https://github.com/grafana/grafana/blob/main/pkg/services/dashboardimport/utils/dash_template_evaluator.go
- `DashboardMigrator.ts` — source in grafana/grafana — https://github.com/grafana/grafana/blob/main/public/app/features/dashboard/state/DashboardMigrator.ts
- `export_inputs.go` — source in grafana/grafana — https://github.com/grafana/grafana/blob/main/apps/dashboard/pkg/migration/conversion/export_inputs.go
- `exporters.ts` — source in grafana/grafana — https://github.com/grafana/grafana/blob/main/public/app/features/dashboard-scene/scene/export/exporters.ts
- `inputs.ts` — source in grafana/grafana — https://github.com/grafana/grafana/blob/main/public/app/features/manage-dashboards/import/utils/inputs.ts
- `PanelPluginError.tsx` — source in grafana/grafana — https://github.com/grafana/grafana/blob/main/public/app/features/panel/components/PanelPluginError.tsx
- `README.md` — source in grafana/grafana — https://github.com/grafana/grafana/blob/main/apps/dashboard/pkg/migration/README.md
- `SceneGridLayout.tsx` — source in grafana/scenes — https://github.com/grafana/scenes/blob/main/packages/scenes/src/components/layout/grid/SceneGridLayout.tsx
- `SceneGridLayoutRenderer.tsx` — source in grafana/scenes — https://github.com/grafana/scenes/blob/main/packages/scenes/src/components/layout/grid/SceneGridLayoutRenderer.tsx
- `service.go` — source in grafana/grafana — https://github.com/grafana/grafana/blob/main/pkg/services/dashboardimport/service/service.go
- `types.ts` — source in grafana/grafana — https://github.com/grafana/grafana/blob/main/public/app/features/library-panels/types.ts
- Add template variables — https://grafana.com/docs/grafana/latest/dashboards/variables/add-template-variables/
- Configure overrides — https://grafana.com/docs/grafana/latest/panels-visualizations/configure-overrides/
- Configure thresholds — https://grafana.com/docs/grafana/latest/panels-visualizations/configure-thresholds/
- Create dashboard — https://grafana.com/docs/grafana/latest/dashboards/build-dashboards/create-dashboard/
- Dashboard — https://grafana.com/docs/grafana/latest/developer-resources/api-reference/http-api/dashboard/
- Dashboard — https://grafana.com/docs/grafana/v10.4/developers/http_api/dashboard/
- Dashboard — https://grafana.com/docs/grafana/v11.6/developers/http_api/dashboard/
- Dashboard v2 schema and dynamic dashboards — https://grafana.com/whats-new/2025-05-05-dashboard-v2-schema-and-dynamic-dashboards/
- Dynamic dashboards is now generally available — https://grafana.com/whats-new/2026-04-08-dynamic-dashboards-is-now-generally-available/
- Explore — https://grafana.com/docs/grafana/latest/explore/
- Import dashboards — https://grafana.com/docs/grafana/latest/dashboards/build-dashboards/import-dashboards/
- Manage library panels — https://grafana.com/docs/grafana/latest/dashboards/build-dashboards/manage-library-panels/
- Modify dashboard settings — https://grafana.com/docs/grafana/latest/dashboards/build-dashboards/modify-dashboard-settings/
- Observability as code — https://grafana.com/docs/grafana/latest/observability-as-code/
- Observability as code grafana 12 — https://grafana.com/blog/observability-as-code-grafana-12/
- Panel editor overview — https://grafana.com/docs/grafana/latest/panels-visualizations/panel-editor-overview/
- Panel inspector — https://grafana.com/docs/grafana/latest/panels-visualizations/panel-inspector/
- Panels visualizations — https://grafana.com/docs/grafana/latest/panels-visualizations/
- Provisioning — https://grafana.com/docs/grafana/latest/administration/provisioning/
- Repository grafana/grizzly — https://github.com/grafana/grizzly
- Schema v2 — https://grafana.com/docs/grafana/latest/as-code/observability-as-code/schema-v2/
- Share dashboards panels — https://grafana.com/docs/grafana/latest/dashboards/share-dashboards-panels/
- Transform data — https://grafana.com/docs/grafana/latest/panels-visualizations/query-transform-data/transform-data/
- View dashboard json model — https://grafana.com/docs/grafana/latest/dashboards/build-dashboards/view-dashboard-json-model/
- Whats new in v12 0 — https://grafana.com/docs/grafana/latest/whatsnew/whats-new-in-v12-0/

### Home Assistant
- Home Assistant — the official public interactive demo — https://demo.home-assistant.io/
- `__init__.py` — source in home-assistant/core — https://github.com/home-assistant/core/blob/dev/homeassistant/components/lovelace/__init__.py
- `actions.markdown` — source in home-assistant/home-assistant.io — https://github.com/home-assistant/home-assistant.io/blob/current/source/dashboards/actions.markdown
- `card.ts` — source in home-assistant/frontend — https://github.com/home-assistant/frontend/blob/dev/src/data/lovelace/config/card.ts
- `cards.markdown` — source in home-assistant/home-assistant.io — https://github.com/home-assistant/home-assistant.io/blob/current/source/dashboards/cards.markdown
- `const.py` — source in home-assistant/core — https://github.com/home-assistant/core/blob/dev/homeassistant/components/lovelace/const.py
- `create-element-base.ts` — source in home-assistant/frontend — https://github.com/home-assistant/frontend/blob/dev/src/panels/lovelace/create-element/create-element-base.ts
- `dashboard.py` — source in home-assistant/core — https://github.com/home-assistant/core/blob/dev/homeassistant/components/lovelace/dashboard.py
- `dashboards.markdown` — source in home-assistant/home-assistant.io — https://github.com/home-assistant/home-assistant.io/blob/current/source/dashboards/dashboards.markdown
- `en.json` — source in home-assistant/frontend — https://github.com/home-assistant/frontend/blob/dev/src/translations/en.json
- `hui-editor.ts` — source in home-assistant/frontend — https://github.com/home-assistant/frontend/blob/dev/src/panels/lovelace/hui-editor.ts
- `hui-grid-section.ts` — source in home-assistant/frontend — https://github.com/home-assistant/frontend/blob/dev/src/panels/lovelace/sections/hui-grid-section.ts
- `hui-masonry-view.ts` — source in home-assistant/frontend — https://github.com/home-assistant/frontend/blob/dev/src/panels/lovelace/views/hui-masonry-view.ts
- `hui-sections-view.ts` — source in home-assistant/frontend — https://github.com/home-assistant/frontend/blob/dev/src/panels/lovelace/views/hui-sections-view.ts
- `markdown.markdown` — source in home-assistant/home-assistant.io — https://github.com/home-assistant/home-assistant.io/blob/current/source/_dashboards/markdown.markdown
- `masonry.markdown` — source in home-assistant/home-assistant.io — https://github.com/home-assistant/home-assistant.io/blob/current/source/_dashboards/masonry.markdown
- `panel.markdown` — source in home-assistant/home-assistant.io — https://github.com/home-assistant/home-assistant.io/blob/current/source/_dashboards/panel.markdown
- `section.ts` — source in home-assistant/frontend — https://github.com/home-assistant/frontend/blob/dev/src/data/lovelace/config/section.ts
- `sections.markdown` — source in home-assistant/home-assistant.io — https://github.com/home-assistant/home-assistant.io/blob/current/source/_dashboards/sections.markdown
- `sidebar.markdown` — source in home-assistant/home-assistant.io — https://github.com/home-assistant/home-assistant.io/blob/current/source/_dashboards/sidebar.markdown
- `types.ts` — source in home-assistant/frontend — https://github.com/home-assistant/frontend/blob/dev/src/data/lovelace/config/types.ts
- `types.ts` — source in home-assistant/frontend — https://github.com/home-assistant/frontend/blob/dev/src/panels/lovelace/cards/types.ts
- `types.ts` — source in home-assistant/frontend — https://github.com/home-assistant/frontend/blob/dev/src/panels/lovelace/types.ts
- `validate-condition.ts` — source in home-assistant/frontend — https://github.com/home-assistant/frontend/blob/dev/src/panels/lovelace/common/validate-condition.ts
- `view.ts` — source in home-assistant/frontend — https://github.com/home-assistant/frontend/blob/dev/src/data/lovelace/config/view.ts
- `views.markdown` — source in home-assistant/home-assistant.io — https://github.com/home-assistant/home-assistant.io/blob/current/source/dashboards/views.markdown

### Netdata
- `package.json` — source in netdata/custom-dashboard — https://github.com/netdata/custom-dashboard/blob/master/package.json
- `README.md` — source in netdata/netdata — https://github.com/netdata/netdata/blob/v1.44.3/web/gui/custom/README.md
- `README.md` — source in netdata/netdata — https://github.com/netdata/netdata/blob/v1.44.3/web/gui/README.md
- Custom dashboards — https://www.netdata.cloud/features/visualization/custom-dashboards/
- Dashboards — https://learn.netdata.cloud/docs/dashboards-and-charts/tabs/dashboards
- Dashboards and charts — https://learn.netdata.cloud/docs/dashboards-and-charts
- Import export and print a snapshot — https://learn.netdata.cloud/docs/developer-and-contributor-corner/import-export-and-print-a-snapshot
- Issue #16918 — netdata/netdata — https://github.com/netdata/netdata/issues/16918
- Netdata — release notes v2.2.0 — https://github.com/netdata/netdata/releases/tag/v2.2.0
- Netdata cloud on prem — https://learn.netdata.cloud/docs/netdata-cloud-on-prem
- New custom dashboards — https://www.netdata.cloud/blog/new-custom-dashboards/
- Repository netdata/dashboard — https://github.com/netdata/dashboard
- Web server reference — https://learn.netdata.cloud/docs/netdata-agent/configuration/securing-agents/web-server-reference

### Datadog
- `model_dashboard.go` — source in DataDog/datadog-api-client-go — https://github.com/DataDog/datadog-api-client-go/blob/master/api/datadogV1/model_dashboard.go
- `model_dashboard_template_variable.go` — source in DataDog/datadog-api-client-go — https://github.com/DataDog/datadog-api-client-go/blob/master/api/datadogV1/model_dashboard_template_variable.go
- `model_widget_layout.go` — source in DataDog/datadog-api-client-go — https://github.com/DataDog/datadog-api-client-go/blob/master/api/datadogV1/model_widget_layout.go
- Alert graph — https://docs.datadoghq.com/dashboards/widgets/alert_graph/
- Configure — https://docs.datadoghq.com/dashboards/configure/
- Cross org visibility — https://docs.datadoghq.com/account_management/org_settings/cross_org_visibility/
- Dashboards — https://docs.datadoghq.com/dashboards/
- Dashboards — https://docs.datadoghq.com/getting_started/dashboards/
- Datadog clipboard — https://docs.datadoghq.com/monitors/incident_management/datadog_clipboard/
- Graphing json — https://docs.datadoghq.com/dashboards/guide/graphing_json/
- How to import datadog resources into terraform — https://docs.datadoghq.com/containers/guide/how-to-import-datadog-resources-into-terraform/
- Issue #1769 — DataDog/terraform-provider-datadog — https://github.com/DataDog/terraform-provider-datadog/issues/1769
- Issue #1848 — DataDog/terraform-provider-datadog — https://github.com/DataDog/terraform-provider-datadog/issues/1848
- Pull request #1017 — DataDog/terraform-provider-datadog — https://github.com/DataDog/terraform-provider-datadog/pull/1017
- Screenboard api doc — https://docs.datadoghq.com/dashboards/guide/screenboard-api-doc/
- Slo — https://docs.datadoghq.com/dashboards/widgets/slo/
- Template variables — https://docs.datadoghq.com/dashboards/template_variables/
- Timeboard api doc — https://docs.datadoghq.com/dashboards/guide/timeboard-api-doc/
- Timeseries — https://docs.datadoghq.com/dashboards/widgets/timeseries/
- Widgets — https://docs.datadoghq.com/dashboards/widgets/

### Kibana and Elastic
- Arrange panels — https://www.elastic.co/docs/explore-analyze/dashboards/arrange-panels
- Create dashboard — https://www.elastic.co/docs/explore-analyze/dashboards/create-dashboard
- Create dashboards programmatically — https://www.elastic.co/docs/explore-analyze/dashboards/create-dashboards-programmatically
- Data views — https://www.elastic.co/docs/explore-analyze/find-and-organize/data-views
- Export — https://www.elastic.co/docs/extend/kibana/saved-objects/export
- Kibana dashboards as code terraform api — https://www.elastic.co/search-labs/blog/kibana-dashboards-as-code-terraform-api
- Manage panels — https://www.elastic.co/docs/explore-analyze/visualize/manage-panels
- Managing saved objects — https://www.elastic.co/guide/en/kibana/7.17/managing-saved-objects.html
- Managing saved objects — https://www.elastic.co/guide/en/kibana/8.19/managing-saved-objects.html
- Migrations — https://www.elastic.co/docs/extend/kibana/saved-objects/migrations
- New in kibana how we made it easier manage visualizations and build dashboards — https://www.elastic.co/blog/new-in-kibana-how-we-made-it-easier-manage-visualizations-and-build-dashboards
- Operation create dashboard — https://www.elastic.co/docs/api/doc/kibana/v9/operation/operation-create-dashboard
- Operation post saved objects export — https://www.elastic.co/docs/api/doc/kibana/operation/operation-post-saved-objects-export
- Operation post saved objects import — https://www.elastic.co/docs/api/doc/kibana/v9/operation/operation-post-saved-objects-import
- Operation post saved objects resolve import errors — https://www.elastic.co/docs/api/doc/kibana/operation/operation-post-saved-objects-resolve-import-errors
- Pull request #39387 — elastic/kibana — https://github.com/elastic/kibana/pull/39387/files
- Pull request #63864 — elastic/kibana — https://github.com/elastic/kibana/pull/63864/files
- Saved objects — https://www.elastic.co/docs/explore-analyze/find-and-organize/saved-objects
- Saved objects api resolve import errors — https://www.elastic.co/guide/en/kibana/current/saved-objects-api-resolve-import-errors.html
- Structure — https://www.elastic.co/docs/extend/kibana/key-concepts/saved-objects/structure

### Homepage
- `settings.yaml` — source in gethomepage/homepage — https://github.com/gethomepage/homepage/blob/main/src/skeleton/settings.yaml
- Configs — https://gethomepage.dev/configs/
- Customapi — https://gethomepage.dev/widgets/services/customapi/
- Docker — https://gethomepage.dev/configs/docker/
- Docker — https://gethomepage.dev/installation/docker/
- Homepage — release notes v1.0.0 — https://github.com/gethomepage/homepage/releases/tag/v1.0.0
- Homepage — the shipped `src/skeleton` configuration directory — https://github.com/gethomepage/homepage/tree/main/src/skeleton
- Info widgets — https://gethomepage.dev/configs/info-widgets/
- Installation — https://gethomepage.dev/installation/
- Services — https://gethomepage.dev/configs/services/
- Settings — https://gethomepage.dev/configs/settings/
- Troubleshooting — https://gethomepage.dev/troubleshooting/
- Widgets — https://gethomepage.dev/widgets/

### Homarr
- `sqlite.ts` — source in homarr-labs/homarr — https://github.com/homarr-labs/homarr/blob/dev/packages/db/schema/sqlite.ts
- After the installation — https://homarr.dev/docs/getting-started/after-the-installation/
- App — https://homarr.dev/docs/widgets/app/
- Apps — https://homarr.dev/docs/management/apps/
- Backup — https://homarr.dev/docs/management/backup/
- Boards — https://homarr.dev/docs/management/boards/
- Environment variables — https://homarr.dev/docs/advanced/environment-variables/
- Getting started — https://homarr.dev/docs/advanced/development/getting-started/
- Glossary — https://homarr.dev/docs/getting-started/glossary/
- Integrations — https://homarr.dev/docs/management/integrations/
- Issue #2229 — homarr-labs/homarr — https://github.com/homarr-labs/homarr/issues/2229
- Migration guide 1.0 — https://homarr.dev/blog/2025/01/19/migration-guide-1.0/
- Notebook — https://homarr.dev/docs/widgets/notebook/
- Version 1.0 — https://homarr.dev/blog/2024/09/23/version-1.0/
- Widgets — https://homarr.dev/docs/category/widgets/

### Dashy
- `app.js` — source in Lissy93/dashy — https://github.com/Lissy93/dashy/blob/master/services/app.js
- `backup-restore.md` — source in Lissy93/dashy — https://github.com/Lissy93/dashy/blob/master/docs/backup-restore.md
- `ConfigAccumalator.js` — source in Lissy93/dashy — https://github.com/Lissy93/dashy/blob/master/src/utils/config/ConfigAccumalator.js
- `ConfigHelpers.js` — source in Lissy93/dashy — https://github.com/Lissy93/dashy/blob/master/src/utils/config/ConfigHelpers.js
- `ConfigSchema.json` — source in Lissy93/dashy — https://github.com/Lissy93/dashy/blob/master/src/utils/config/ConfigSchema.json
- `configuring.md` — source in Lissy93/dashy — https://github.com/Lissy93/dashy/blob/master/docs/configuring.md
- `CriticalError.vue` — source in Lissy93/dashy — https://github.com/Lissy93/dashy/blob/master/src/components/PageStrcture/CriticalError.vue
- `defaults.js` — source in Lissy93/dashy — https://github.com/Lissy93/dashy/blob/master/src/utils/config/defaults.js
- `developing.md` — source in Lissy93/dashy — https://github.com/Lissy93/dashy/blob/master/docs/developing.md
- `en.json` — source in Lissy93/dashy — https://github.com/Lissy93/dashy/blob/master/src/assets/locales/en.json
- `icons.md` — source in Lissy93/dashy — https://github.com/Lissy93/dashy/blob/master/docs/icons.md
- `store.js` — source in Lissy93/dashy — https://github.com/Lissy93/dashy/blob/master/src/store.js
- `validateConfig.js` — source in Lissy93/dashy — https://github.com/Lissy93/dashy/blob/master/src/utils/config/validateConfig.js
- `widgets.md` — source in Lissy93/dashy — https://github.com/Lissy93/dashy/blob/master/docs/widgets.md
- Dashy — public live demo — https://demo.dashy.to/

### Akvorado, Malcolm, Arkime and Vizceral
- Akvorado — console and documentation introduction — https://demo.akvorado.net/docs/intro
- Arkime — project site and screenshot set — https://arkime.com/
- Repository akvorado/akvorado — https://github.com/akvorado/akvorado
- Repository arkime/arkime — https://github.com/arkime/arkime
- Repository cisagov/Malcolm — https://github.com/cisagov/Malcolm
- Repository Netflix/vizceral — https://github.com/Netflix/vizceral

### UniFi Network
- Content and Domain Filtering in UniFi — https://help.ui.com/hc/en-us/articles/12568927589143-Content-and-Domain-Filtering-in-UniFi
- Getting Started with the Official UniFi API — https://help.ui.com/hc/en-us/articles/30076656117655-Getting-Started-with-the-Official-UniFi-API
- Releasing unifi network 9 1 — https://blog.ui.com/article/releasing-unifi-network-9-1
- Traffic Flows and Traffic Logging in UniFi Network — https://help.ui.com/hc/en-us/articles/32201256219799-Traffic-Flows-and-Traffic-Logging-in-UniFi-Network
- UniFi CyberSecure Enhanced by Proofpoint and Cloudflare — https://help.ui.com/hc/en-us/articles/25930305913751-UniFi-CyberSecure-Enhanced-by-Proofpoint-and-Cloudflare
- UniFi Gateway Country Restriction — https://help.ui.com/hc/en-us/articles/12567758783383-UniFi-Gateway-Country-Restriction
- UniFi Gateway Intrusion Detection and Prevention IDS IPS — https://help.ui.com/hc/en-us/articles/360006893234-UniFi-Gateway-Intrusion-Detection-and-Prevention-IDS-IPS
- UniFi Gateway Traffic and Device Identification — https://help.ui.com/hc/en-us/articles/12570783535383-UniFi-Gateway-Traffic-and-Device-Identification
- UniFi System Logs SIEM Integration — https://help.ui.com/hc/en-us/articles/33349041044119-UniFi-System-Logs-SIEM-Integration
- UNVERIFIED — Ubiquiti community thread on time-series data retention — https://community.ui.com/questions/time-series-data-retention-settings/14bc5a8c-7c18-4825-a7b4-417d20fa0c0e
- UNVERIFIED — Ubiquiti community thread, "But what does Threat Map tell me?" — https://community.ui.com/questions/But-what-does-Threat-Map-tell-me/66e36d7f-6959-4aed-88af-142bf8c5b195
- WiFi Troubleshooting Guide — https://help.ui.com/hc/en-us/articles/32064585817495-WiFi-Troubleshooting-Guide
- Zone Based Firewalls in UniFi — https://help.ui.com/hc/en-us/articles/115003173168-Zone-Based-Firewalls-in-UniFi

### Firewalla
- A Secure and Better Network with Firewalla Part 1 Visibility — https://help.firewalla.com/hc/en-us/articles/360049374514-A-Secure-and-Better-Network-with-Firewalla-Part-1-Visibility
- Device Management — https://help.firewalla.com/hc/en-us/articles/115004304054-Device-Management
- Firewalla Blocked Flows — https://help.firewalla.com/hc/en-us/articles/1500007220942-Firewalla-Blocked-Flows
- Firewalla Feature Network Flows — https://help.firewalla.com/hc/en-us/articles/24739086338323-Firewalla-Feature-Network-Flows
- Firewalla Managed Security Portal MSP Introduction — https://help.firewalla.com/hc/en-us/articles/4409866753427-Firewalla-Managed-Security-Portal-MSP-Introduction
- Firewalla MSP Lite previously my firewalla com — https://help.firewalla.com/hc/en-us/articles/360052779253-Firewalla-MSP-Lite-previously-my-firewalla-com
- Firewalla MSP Reports — https://help.firewalla.com/hc/en-us/articles/27174165629971-Firewalla-MSP-Reports
- Firewalla s Deep Insights — https://help.firewalla.com/hc/en-us/articles/4410153017619-Firewalla-s-Deep-Insights
- Flow — https://docs.firewalla.net/data-models/flow/
- Geo IP Map Display — https://help.firewalla.com/hc/en-us/community/posts/5780686426899-Geo-IP-Map-Display
- Manage Rules — https://help.firewalla.com/hc/en-us/articles/360008521833-Manage-Rules
- Search — https://help.firewalla.com/hc/en-us/search?query=map&content_tags=01HZPPFXMJ6XSWPHW8GTMZJBHJ
- Understanding the blockedby Flow property from MSP API — https://help.firewalla.com/hc/en-us/community/posts/38629064411795-Understanding-the-blockedby-Flow-property-from-MSP-API

### NextDNS
- Api — https://nextdns.github.io/api/
- Blocked reason in logs — https://help.nextdns.io/t/y4hlrxb/blocked-reason-in-logs
- Device information log enrichment — https://help.nextdns.io/t/x2h76ay/device-information-log-enrichment
- Does nextdns collect and store personal data — https://help.nextdns.io/t/y4hmvar/does-nextdns-collect-and-store-personal-data
- Download logs missing the resolved domain ip — https://help.nextdns.io/t/x2httxp/download-logs-missing-the-resolved-domain-ip
- Filter logs by blocked reasons — https://help.nextdns.io/t/m1hs38m/filter-logs-by-blocked-reasons
- Identify devices when using nextdns on router — https://help.nextdns.io/t/g9hts3n/identify-devices-when-using-nextdns-on-router
- Navigation logs retention — https://help.nextdns.io/t/p8hpdsa/navigation-logs-retention
- NextDNS — product site — https://nextdns.io/

### Pi-hole
- Configfile — https://docs.pi-hole.net/ftldns/configfile/
- Docs.pi hole.net — https://docs.pi-hole.net/
- Domain database — https://docs.pi-hole.net/database/domain-database/
- Groups — https://docs.pi-hole.net/database/gravity/groups/
- Introducing pi hole v6 — https://pi-hole.net/blog/2025/02/18/introducing-pi-hole-v6/
- Pihole command — https://docs.pi-hole.net/main/pihole-command/
- Privacylevels — https://docs.pi-hole.net/ftldns/privacylevels/
- Pull request #1532 — pi-hole/FTL — https://github.com/pi-hole/FTL/pull/1532
- Query database — https://docs.pi-hole.net/database/query-database/
- Repository pi-hole/web — https://github.com/pi-hole/web
- UNVERIFIED — Pi-hole community forum, client-click filtering report — https://discourse.pi-hole.net/t/all-clients-queries-listed-when-clicking-on-individual-client-in-dashboard/11816
- UNVERIFIED — Pi-hole community forum, request to name the blocking list in Recent Queries — https://discourse.pi-hole.net/t/show-which-blacklist-blocked-a-query-in-recent-queries-instead-of-gravity/62760

### ntopng
- `alert_blacklisted_server_contact.lua` — source in ntop/ntopng — https://github.com/ntop/ntopng/blob/dev/scripts/lua/modules/alert_definitions/flow/alert_blacklisted_server_contact.lua
- `alert_flow_blacklisted.lua` — source in ntop/ntopng — https://github.com/ntop/ntopng/blob/dev/scripts/lua/modules/alert_definitions/flow/alert_flow_blacklisted.lua
- `en.lua` — source in ntop/ntopng — https://github.com/ntop/ntopng/blob/dev/scripts/locales/en.lua
- `hosts_geomap.template` — source in ntop/ntopng — https://github.com/ntop/ntopng/blob/dev/httpdocs/templates/pages/hosts_geomap.template
- `menu_definition.lua` — source in ntop/ntopng — https://github.com/ntop/ntopng/blob/dev/scripts/lua/modules/menu_definition.lua
- `vlan_details.lua` — source in ntop/ntopng — https://github.com/ntop/ntopng/blob/dev/scripts/lua/vlan_details.lua
- `vlan_stats.lua` — source in ntop/ntopng — https://github.com/ntop/ntopng/blob/dev/scripts/lua/vlan_stats.lua
- Alerts filters — https://www.ntop.org/guides/ntopng/user_interface/shared/alerts/others/alerts_filters.html
- Autonomous systems — https://www.ntop.org/guides/ntopng/user_interface/network_interface/interface/autonomous_systems.html
- Available alerts — https://www.ntop.org/guides/ntopng/user_interface/shared/alerts/others/available_alerts.html
- Blacklists — https://www.ntop.org/guides/ntopng/user_interface/shared/settings/blacklists.html
- Countries — https://www.ntop.org/guides/ntopng/user_interface/network_interface/interface/countries.html
- Dashboard — https://www.ntop.org/guides/ntopng/user_interface/network_interface/dashboard/dashboard.html
- Exporters — https://www.ntop.org/guides/ntopng/flows/exporters.html
- Geo map — https://www.ntop.org/guides/ntopng/user_interface/network_interface/maps/geo_map.html
- Geolocation — https://www.ntop.org/guides/ntopng/basic_concepts/geolocation.html
- Historical flows — https://www.ntop.org/guides/ntopng/flow_dump/clickhouse/historical_flows.html
- Hosts — https://www.ntop.org/guides/ntopng/basic_concepts/hosts.html
- Hosts — https://www.ntop.org/guides/ntopng/user_interface/network_interface/hosts/hosts.html
- Index — https://www.ntop.org/guides/ntopng/user_interface/network_interface/index.html
- Interfaces — https://www.ntop.org/guides/ntopng/user_interface/network_interface/interface/interfaces.html
- Issue #8833 — ntop/ntopng — https://github.com/ntop/ntopng/issues/8833
- Ndpi flow risks — https://www.ntop.org/guides/ntopng/user_interface/shared/alerts/remediations/ndpi_flow_risks.html
- Nedge — https://www.ntop.org/guides/nedge/
- Networks — https://www.ntop.org/guides/ntopng/user_interface/network_interface/interface/networks.html
- Suricata — https://www.ntop.org/guides/ntopng/third_party_integrations/suricata.html
- Timeseries — https://www.ntop.org/guides/ntopng/basic_concepts/timeseries.html
- Welcome to ntopng 7 0 modern gui nassistant sites observability bgp bmp pqc wazuh — https://www.ntop.org/welcome-to-ntopng-7-0-modern-gui-nassistant-sites-observability-bgp-bmp-pqc-wazuh/

### Zenarmor
- Accessing organization dashboard — https://www.zenarmor.com/docs/organization-management/accessing-organization-dashboard
- Best practices for zenarmor deployment — https://www.zenarmor.com/docs/guides/best-practices-for-zenarmor-deployment
- Configuring dns for reports — https://www.zenarmor.com/docs/configuring/configuring-dns-for-reports
- Configuring exempted vlans and networks on zenconsole — https://www.zenarmor.com/docs/configuring/configuring-exempted-vlans-and-networks-on-zenconsole
- Configuring reporting database backend — https://www.zenarmor.com/docs/opnsense/configuring/configuring-reporting-database-backend
- Dashboard — https://www.zenarmor.com/docs/opnsense/viewing-node-status/dashboard
- Deployment modes — https://www.zenarmor.com/docs/guides/deployment-modes
- Device identification overview — https://www.zenarmor.com/docs/devices/device-identification-overview
- Live session explorer — https://www.zenarmor.com/docs/opnsense/reporting-analytics/live-session-explorer
- Managing reporting database — https://www.zenarmor.com/docs/configuring/managing-reporting-database
- Report view — https://www.zenarmor.com/docs/opnsense/reporting-analytics/report-view
- Report view configuration — https://www.zenarmor.com/docs/opnsense/reporting-analytics/report-view-configuration
- Reports overview — https://www.zenarmor.com/docs/opnsense/reporting-analytics/reports-overview
- Setting store duration of reporting data — https://www.zenarmor.com/docs/opnsense/configuring/setting-store-duration-of-reporting-data
- Why Top Local Hosts show IPs vs predefined hostnames — https://help.zenarmor.com/hc/en-us/community/posts/33820508772627-Why-Top-Local-Hosts-show-IPs-vs-predefined-hostnames
- Zenarmor editions — https://www.zenarmor.com/docs/introduction/zenarmor-editions
- Zenarmor free vs home editions — https://www.zenarmor.com/docs/guides/zenarmor-free-vs-home-editions

### Cloudflare
- Announcing cloudflare radar outage center — https://blog.cloudflare.com/announcing-cloudflare-radar-outage-center/
- Application layer attacks — https://developers.cloudflare.com/radar/investigate/application-layer-attacks/
- Attack maps now available on radar — https://blog.cloudflare.com/attack-maps-now-available-on-radar/
- Cloudflare Radar — Traffic Worldwide (live) — https://radar.cloudflare.com/traffic
- Cloudflare Radar — Worldwide Overview (live) — https://radar.cloudflare.com/
- Detecting internet outages — https://blog.cloudflare.com/detecting-internet-outages/
- Dns policies — https://developers.cloudflare.com/cloudflare-one/traffic-policies/dns-policies/
- Domain categories — https://developers.cloudflare.com/cloudflare-one/traffic-policies/domain-categories/
- Domain ranking datasets — https://developers.cloudflare.com/radar/investigate/domain-ranking-datasets/
- Gateway logs — https://developers.cloudflare.com/cloudflare-one/insights/logs/gateway-logs/
- Global policies — https://developers.cloudflare.com/cloudflare-one/traffic-policies/global-policies/
- Glossary — https://developers.cloudflare.com/radar/glossary/
- Investigate — https://developers.cloudflare.com/radar/investigate/
- Making comparisons — https://developers.cloudflare.com/radar/get-started/making-comparisons/
- Netflows — https://developers.cloudflare.com/radar/investigate/netflows/
- Normalization — https://developers.cloudflare.com/radar/concepts/normalization/
- Radar domain rankings — https://blog.cloudflare.com/radar-domain-rankings/
- Traffic anomalies notifications radar — https://blog.cloudflare.com/traffic-anomalies-notifications-radar/

### OPNsense, cited for the storage-engine decision
- `logger.py` — source in opnsense/core at tag 26.7.3, the Unbound query logger, which imports `duckdb` and writes to `/var/unbound/data/unbound.duckdb` — https://raw.githubusercontent.com/opnsense/core/26.7.3/src/opnsense/scripts/unbound/logger.py

### Palettes
- `README.md` — source in dracula/dracula-theme, the canonical Dracula and Alucard palette tables — https://raw.githubusercontent.com/dracula/dracula-theme/master/README.md
- `day.lua` — source in folke/tokyonight.nvim — https://raw.githubusercontent.com/folke/tokyonight.nvim/main/lua/tokyonight/colors/day.lua
- `index.ts` — source in rose-pine/palette — https://raw.githubusercontent.com/rose-pine/palette/main/source/index.ts
- `init.lua` — source in folke/tokyonight.nvim — https://raw.githubusercontent.com/folke/tokyonight.nvim/main/lua/tokyonight/colors/init.lua
- `json` — source in rose-pine/palette — https://github.com/rose-pine/palette/tree/main/dist/json
- `moon.lua` — source in folke/tokyonight.nvim — https://raw.githubusercontent.com/folke/tokyonight.nvim/main/lua/tokyonight/colors/moon.lua
- `night.lua` — source in folke/tokyonight.nvim — https://raw.githubusercontent.com/folke/tokyonight.nvim/main/lua/tokyonight/colors/night.lua
- `nord.css` — source in nordtheme/nord — https://raw.githubusercontent.com/nordtheme/nord/develop/src/nord.css
- `palette.json` — source in catppuccin/palette — https://raw.githubusercontent.com/catppuccin/palette/main/palette.json
- `palette.json` — source in rose-pine/palette — https://raw.githubusercontent.com/rose-pine/palette/main/palette.json
- `README.md` — source in catppuccin/catppuccin — https://raw.githubusercontent.com/catppuccin/catppuccin/main/README.md
- `rose-pine.css` — source in rose-pine/palette — https://raw.githubusercontent.com/rose-pine/palette/main/dist/css/rose-pine.css
- `storm.lua` — source in folke/tokyonight.nvim — https://raw.githubusercontent.com/folke/tokyonight.nvim/main/lua/tokyonight/colors/storm.lua
- `tokyonight_day.lua` — source in folke/tokyonight.nvim — https://raw.githubusercontent.com/folke/tokyonight.nvim/main/extras/lua/tokyonight_day.lua
- Colors and palettes — https://www.nordtheme.com/docs/colors-and-palettes
- Palette — https://rosepinetheme.com/palette/
- Dracula — official syntax-highlighting specification — https://spec.draculatheme.com/

### Third-party, admitted as a last resort
- Unifi network 9 1 enhancing network management with visibility and control — https://unifinerds.com/unifi-network-9-1-enhancing-network-management-with-visibility-and-control/
