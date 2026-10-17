# Dashboard file format

A dashboard in `opnview` is a **file**. This document defines what that file
contains, how it is versioned, and what happens when it is carried to an
installation that does not have everything it mentions. It began as a draft for
arbitration; the questions it left open are now decided, and *Decided choices*
at the end records each decision with its reason. Format version 1 is
implemented: the server validates every document it stores
(`internal/dashboard`), and the front's document module (`front/core/document.js`)
validates, migrates and serialises it in the editor; both are held to the same
conformance fixtures (`testdata/dashboards/conformance`), so the two cannot
disagree without a test failing.

It is informed by prior art rather than invented. `docs/ui-references.md`,
*Family A*, sets out how eight products actually do this — Grafana's 24-column
grid and its one-way `schemaVersion` ladder, Home Assistant's single in-memory
model behind two editors and its per-card error element, Dashy's JSON Schema
that deliberately does not run in the load path, Kibana's `missing_references`
import error and its `replaceReferences` repair step, Datadog's fail-closed
structural validation. Where this document departs from all of them, it says so.

The five settled product decisions it is written against are restated in
`docs/ui-references.md`, section *The product as decided*: named canvases rather
than fixed screens; everything describable as code, in JSON and/or YAML, never
XML; portable dashboards where a widget with no local data renders as an
explained empty state rather than an error or a fabricated zero; the
maintainer's industrial palette by default with five named options; and the
theme following the operating system on first launch. Nothing below reopens or
contradicts one.

**No interface name, VLAN name, interface name, CIDR or address of any real
network appears in this document.** The example values below come from the
documentation ranges and from obviously invented labels, and are marked as
examples. Every such value in a real dashboard file is a value the user's own
installation discovered at runtime through the OPNsense API; none is ever
hardcoded in `opnview`.

## What a dashboard file contains

One file describes one **dashboard**: a set of named canvases, each holding
placed widgets. The whole document is one object with the following fields.

### Top level

| Field | Required | Type | Meaning |
|---|---|---|---|
| `format_version` | **yes** | integer | The version of *this format*, not of `opnview`. The current value is `1`. Its semantics are in *Versioning and compatibility*. A file without it is not a dashboard file and is refused. |
| `title` | no | string | A human name for the whole dashboard. Defaults to the file name on import when absent. |
| `description` | no | string | Free text shown in the dashboard list. Carries no behaviour. |
| `generated_by` | no | string | What wrote the file — `opnview 0.x`, a hand edit, a generator. Advisory only; never matched on, never used to change behaviour. |
| `generated_at` | no | integer | UTC epoch seconds, matching the schema convention that every instant is an integer epoch in seconds. Advisory only. |
| `canvases` | **yes** | array | One or more canvases, in the order the tabs are shown. An empty array is refused: a dashboard with no canvas is not a dashboard. |

### Canvas

| Field | Required | Type | Meaning |
|---|---|---|---|
| `id` | **yes** | string | A slug, unique within the file, matching `[a-z0-9][a-z0-9_-]*`. It is the tab's stable identity across an export and a re-import, and the target of a cross-canvas link. It is **not** globally unique and makes no claim to be. |
| `title` | **yes** | string | The tab label. |
| `description` | no | string | Free text shown when the canvas is empty or on hover. |
| `grid` | no | object | Layout parameters for this canvas: `columns` (integer, default `12`) and `row_height` (integer, the height of one row unit in CSS pixels, default `40`). Present so a canvas can be denser or airier than the default without a global setting. |
| `period` | no | string | The canvas's range: one of the widget API's periods, `1h`, `24h`, `7d` or `30d`; default `24h`. Every widget whose manifest takes a `period` inherits it unless it carries a `period` of its own, and its card then says "own range". |
| `scope` | no | object | The canvas's scope: up to three lists of reference objects, `interfaces`, `clients` and `owners`, each optional. See *The canvas period and scope* below. |
| `widgets` | **yes** | array | The placed widgets. **May be empty** — an empty canvas is a legitimate, useful state (a tab the user is about to fill), and is rendered with an invitation rather than an error. |

#### The canvas period and scope

`period` and `scope` were added to format version 1 in place, without a version
bump, because no released reader of the format existed (decision D7 of
`specs/SPEC-step-7-frontend.md`): a file written before them is still a valid
version-1 file, and a reader that does not know them keeps them as unknown keys.

- **A widget inherits them unless it carries its own value.** A widget whose
  `parameters` hold a `period` keeps it; a widget whose `parameters` already hold
  a reference list of a kind keeps it for that kind. Its card says "own range" or
  "own scope".
- **A scope applies only where the widget's manifest declares a parameter of that
  kind** (decision D8). An `interfaces` scope is sent to a widget under the key
  its manifest declares for an `interface` reference list, and to no other; a
  `reference` parameter (one value, not a list) receives a scope holding exactly
  one reference. A widget declaring no parameter of a kind in force is drawn
  unscoped for that kind and its card says "not scoped".
- **Nothing is expanded or inferred.** An `owners` scope is never turned into the
  clients the person owns: ownership is never inferred, and a widget that takes
  clients but not owners is "not scoped" by person.
- The references are ordinary reference objects (*The reference object*), with
  the same fail-open resolution: a scope reference this installation does not
  have is reported on the cards it reaches, never refused on save.

### Widget

| Field | Required | Type | Meaning |
|---|---|---|---|
| `id` | **yes** | string | Unique within its canvas, same slug shape as a canvas id. It is what a saved comment, a link or a deep-link URL points at. |
| `type` | no | string | The **preset** identifier — `interface_volume_ranking`, `passed_traffic_world_map`, `unified_blocked_feed` and so on. Each `###` entry in `docs/widget-catalogue.md`, Part 2, declares exactly one in its **Type** field; that document is the vocabulary and this one does not duplicate it. An unknown `type` is an unresolved reference, not a parse error; see *Unresolved references*. **Exactly one of `type` and `shape` is required.** |
| `shape` | no | string | The **shape** identifier — `figure`, `ranked_bars`, `time_series`, `table`, `feed`, `cards`, `map`, `donut`, `tree`, `sankey`, `graph`, `matrix`. Part 1 of `docs/widget-catalogue.md` is the vocabulary. A widget carrying `shape` and no `type` is a shape the reader bound themselves: its binding lives in `parameters`, and nothing in the catalogue has to have anticipated it. A widget carrying `type` **also** resolves to a shape — the preset names it — and stating both is allowed only where they agree; a disagreement is an unresolved reference. **Exactly one of `type` and `shape` is required.** |
| `title` | no | string | Overrides the catalogue's default heading for this instance. |
| `placement` | **yes** | object | Where the widget sits. See below. |
| `parameters` | no | object | The widget's settings. For a `type`, the keys are defined by that preset's **Parameters** field; absent means "every parameter at its catalogue default", which is a valid and common case. For a bare `shape`, the keys are the shape's **Shape parameters** *plus the binding* — which entity, which dimensions, which measures — and `parameters` is then **required**, because a shape with no binding has nothing to draw. |

### Placement

`placement` is an object of four non-negative integers, in the units of the
canvas grid.

| Field | Required | Type | Meaning |
|---|---|---|---|
| `x` | **yes** | integer | Column offset from the left edge, `0` to `columns - 1`. |
| `y` | **yes** | integer | Row offset from the top, in row units. |
| `w` | **yes** | integer | Width in columns, `1` to `columns`. A `w` greater than `columns` is clamped rather than overflowing — Home Assistant's `span min(...)` behaviour, which is strictly kinder than letting a widget escape the grid. |
| `h` | **yes** | integer | Height in row units. |

Two behaviours are part of the format rather than of the renderer, because they
decide what a re-export contains.

- **`y` is advisory.** Like Grafana's, the layout engine floats widgets upward
  into empty space, so a file whose `y` values leave gaps renders compacted and
  a subsequent export writes the compacted values back. A reader must not treat
  `y` as an assertion about vertical position; it is an ordering hint.
- **The narrow layout is never persisted.** Below a viewport breakpoint every
  widget is rendered full width in `y` order, and that reflow is **not** written
  back to the file. This is Grafana's `_skipOnLayoutChange` rule, and it exists
  because the alternative — opening a dashboard on a phone and thereby
  destroying its desktop layout — is a real and well-documented failure.

### Parameters

`parameters` is a flat object of scalar values, arrays of scalars, and
**reference objects**. The catalogue defines, per widget type, which keys are
accepted and what they mean; this document defines only the value shapes.

- A **scalar** is a string, an integer, a number or a boolean. Periods
  (`"24h"`), scopes (`"east_west"`), sort orders and limits are all scalars.
- An **array of scalars** is an unordered set unless the catalogue says
  otherwise — `"severities": ["critical", "high"]`.
- A **reference object** names something that exists on a particular
  installation. It is the only value shape that can fail to resolve, and it is
  the subject of the next two sections.
- An **ordered array of objects** is the fourth shape, and it exists because at
  least one widget genuinely needs it. The catalogue's *Custom chart* takes a
  `series` list, each entry an object naming what that series plots, which axis
  it belongs to, how it renders and how it aggregates — and the order matters,
  because it is the draw order and the legend order. Each entry is itself a
  parameter object obeying the rules above, so a series may carry a reference
  object of its own (an interface, a client, a rule) and that reference can
  fail to resolve independently of the rest of the chart. **This is the format's
  hardest case and it is in the worked example deliberately**: if a list of
  series cannot be written here, the format is wrong.

Nesting stops there. A parameter value is a scalar, an array of scalars, a
reference object, or an array of objects each of which is a flat map of scalars
and reference objects. **No deeper nesting is defined**, so a reader never has
to recurse arbitrarily and a text editor's highlighting of a failed reference
can always name a concrete path such as `series[1].interface`.

A key the local build does not recognise is **kept, not dropped**: it is carried
through an import and written back on export, so a dashboard authored against a
newer `opnview` survives a round trip through an older one without silent data
loss. It is reported in the editor as an unknown key, and the widget renders
with its known parameters.

## JSON and YAML

**Both are accepted, over one model.** A dashboard file is a JSON document or a
YAML document describing the same object; YAML's mapping/sequence/scalar model
is a superset of JSON's for the constructs used here, so there is exactly one
data model and two surface syntaxes. The format defines no construct that YAML
can express and JSON cannot: no anchors, no aliases, no tags, no multi-document
streams, no non-string mapping keys. A YAML file using any of those is refused
with that as the reason, rather than being half-understood.

The consequence, which is stated rather than hidden: **a YAML file's comments do
not survive a write.** When `opnview` writes a dashboard back — because the user
edited it in the UI, or repaired a reference, or the layout compacted — it
serialises the model, and comments are not in the model. Grafana, Home Assistant
and Dashy all have this property, and Home Assistant's raw editor warns before
saving for exactly this reason. `opnview` warns in the same place: when the YAML
being applied holds a comment, the editor says before saving that the comments
will be lost. The words are the interface's own, from its string catalogue like
every other word on screen; this document fixes what the warning must say, not
its wording. It does not silently discard them.

**The YAML `opnview` reads and writes is a restricted subset**, parsed and
serialised by the front's own document module rather than by a general YAML
library (decision D12): block mappings and sequences, a mapping opening a
sequence item (`- id: ...`), one-line flow mappings and sequences
(`{ x: 0, y: 0, w: 12, h: 6 }`, `[a, b]`), plain, single-quoted and
double-quoted scalars, blank lines and comments. That is every construct of the
worked example below and of the serialiser's own output. A file using anything
else is refused with the line it is on, never half-read. The server stores
JSON only.

The syntax an **export** produces is the user's choice, YAML or JSON, made at
export (*Decided choices*, 1). **Import accepts both**, detected from the content rather than from the file extension: a
document whose first non-whitespace character is `{` is parsed as JSON, anything
else as YAML. An extension is a hint for the file picker and never a contract.

## References and portability

This is the part the whole format turns on, and the product decision behind it
is explicit:

> **`opnview` does not invent a universal identifier that resolves magically on
> import.** It accepts that a dashboard file carries references that may not
> exist on the importing installation, and makes the unresolved ones **visible
> and repairable** rather than pretending they resolved.

The reason is empirical, from *Family A*. Home Assistant's `device_id`,
`area_id` and user ids are opaque registry UUIDs minted locally; a dashboard
carrying them does not error on another installation — it **silently
mis-targets or silently hides content**, which is worse than a visible failure.
Grafana's answer, `${DS_*}` placeholders plus an `__inputs` block the importer
must satisfy, works but only for data sources, and an unsatisfied input is a
hard import failure. Kibana's answer is the best of the three and is the one
copied here: a reference is carried as a typed, named thing; a reference that
cannot be resolved is reported as a first-class condition; and the user repairs
it by pointing it at something that does exist.

### The reference object

Every reference is an object with the same four keys.

| Key | Required | Meaning |
|---|---|---|
| `kind` | **yes** | What sort of thing is being referenced: `interface`, `client`, `rule`, `owner`, `provider`, `country`, `operator`, `site`. Closed vocabulary; an unknown `kind` is an unresolved reference. |
| `by` | **yes** | Which natural key the `value` is expressed in. The accepted keys per kind are tabulated below. |
| `value` | **yes** | The key's value, as the importing installation would have to match it. |
| `label` | no | A human label from the **exporting** installation, carried purely so the repair UI can say *"this file was built against an interface labelled X"*. **It is never matched on.** An interface's nature is never inferred from what it is called, here or anywhere else in `opnview`. |

| `kind` | Accepted `by` | Resolves against | Portable across installations? |
|---|---|---|---|
| `interface` | `identifier`, `device`, `user_label` | `interface.identifier`, `interface.device`, `interface.user_label` | **No.** These are per-installation values discovered at runtime. |
| `client` | `mac`, `dhcp_client_id`, `hostname` | `client.identity_kind` + `client.identity_key`, `client.hostname` | **No**, and a `mac` reference additionally fails for a client whose MAC is randomised — such a client has no stable identity by design. |
| `rule` | `pf_label`, `description` | `rule.pf_label`, `rule.description` | **No.** A pf label is local, and a rule can be deleted after it logged. |
| `owner` | `display_name` | `owner.display_name` | **No, and worse than the three above.** See *Why `owner` travels worst*. |
| `provider` | `kind_and_key` | `provider.kind` + `provider.provider_key` | **Yes.** `dns_lookup/unbound` means the same thing on every installation; this is the one reference kind that is genuinely universal, because it names an implementation rather than an instance. |
| `country` | `iso_3166_1_alpha_2` | `geo_asn.country_code` | **Yes.** |
| `operator` | `asn` | `geo_asn.asn` | **Yes.** An AS number is globally assigned. |
| `site` | `domain` | `domain_attribution.site_name` | **Yes** as a string, though whether any traffic locally matches it is another matter. |

Three of the eight kinds are globally meaningful and five are not. That
asymmetry is the honest shape of the problem and the format does not paper over
it. A dashboard built entirely from portable references — *"blocked lookups by
list, everywhere"*, *"destinations by operator, everywhere"* — travels
perfectly. A dashboard scoped to one installation's interfaces, clients and
people does not, and cannot, and says so on arrival.

#### Why `owner` travels worst, and why it exists anyway

The eighth kind is the one that makes *"Bob's card"* writable in a file. Without
it a per-person widget can only render every person at once, because there is no
value shape in which "this one person" can be said — and a dashboard the user
cannot scope to one person is a dashboard that cannot answer the question the
`owner` entity was added for.

**The cost is real and is stated rather than softened.** The other seven kinds
reference something the firewall knows about: an interface it terminates, a
client it leased an address to, a rule it is running, an implementation, a
country, an AS number, a domain. **An owner is none of those.** It is typed into
`opnview` by one installation's user and exists nowhere else — the firewall has
no notion of a person, and `docs/data-model.md` records `owner` as the one entity
fed by no endpoint. So an `owner` reference does not merely fail to resolve on
another installation, as an `interface` reference does; it has **no chance** of
resolving, because the thing it names was invented locally and by hand. Even the
`by` key is weak: `display_name` is the only natural key an owner has, and two
installations agreeing on the string "Bob" would be a coincidence rather than a
match — and a coincidence that resolved would be worse than a failure, because it
would silently attribute one household's traffic to another household's person.

**The answer is the mechanism the format already defines, not a refusal of the
kind.** An unresolved reference keeps its widget, renders empty, names what is
missing and offers repair. That path was built for exactly this: a reference
whose target is local. An `owner` reference arriving on another installation
takes it, and says *"This widget is scoped to a person this installation does not
have: owner 'Bob', as named where the dashboard was built. Choose a local person
to repair it."* The user repairs it by pointing at somebody who does exist, or
removes the scope and gets every person. Nothing is guessed, nothing is silently
mis-targeted, and the failure is visible on arrival.

**It is never matched on loosely.** Resolution is an exact match on
`owner.display_name` and nothing else — no case folding beyond what the column
already enforces, no fuzzy match, no "did you mean". The rule the schema applies
to ownership applies here too: a name is not evidence, and a near-match that
resolved would be an inference nobody asked for.

### What actually travels

**Travels intact:** the format version, canvas structure, titles, grid
parameters, placement, widget types that both builds know, and every scalar
parameter — periods, scopes, measures, limits, sort orders, severities, the
provider/country/operator/site references above.

**Does not travel, and is reported:** interface, client, rule and owner
references; a
widget `type` the importing build does not implement; a parameter key it does
not recognise; and a parameter *value* outside the vocabulary it knows, such as
a period this build does not offer.

**Never travels, and is not in the file at all:** anything holding a credential.
No OPNsense URL, API key or secret, and no MaxMind licence key, appears in a
dashboard file in any form. Those are entered in the setup wizard and stored
outside the repository, and a dashboard file is meant to be pasted into a forum
post without a second thought.

## Unresolved references

An unresolved reference is a **normal condition of a portable format**, not an
error. The rule, stated once and applied everywhere:

> **The widget is kept. It renders empty, and the empty state names what is
> missing and offers to repair it. Neither an error screen nor a fabricated zero
> is acceptable.**

A widget whose reference did not resolve therefore shows: its own title, so the
canvas keeps its shape; a plain sentence naming the reference that failed, its
`kind`, its `by`, its `value`, and the exporting installation's `label` if the
file carried one; and a control to repair it. It does **not** show a chart with
no series, a table with no rows, a zero, or a red failure state. A zero would be
a lie — it asserts "we looked and there was none" when the truth is "we could
not look".

This is the same distinction the schema already draws everywhere else:
`source_availability` exists precisely so that an absent source is a modelled
condition rather than an absence of rows, and a geo cache miss is a row whose
`lookup_state` is `miss` rather than a missing row. An unresolved reference is
that idea applied to the dashboard file.

### Two coordinated surfaces, one model

Repair happens through **two surfaces that edit the same model**, and neither is
the "real" one.

1. **The code editor.** The file as JSON or YAML, in a text editor, with the
   **exact lines whose reference failed highlighted in place**. Each highlighted
   line carries an inline "no data" notice naming the reference and why it did
   not resolve. The user can fix it by typing — it is a text editor over the
   real file — and the highlight clears when the reference resolves.

2. **The no-code selector.** The same widget in a form, where every field that
   holds a reference is a dropdown **populated from what this installation
   actually has**: the interfaces the firewall discovered, the clients seen in
   leases and flows, the rules in the running ruleset, the providers in the
   registry. The dropdown shows the unresolved value as the current selection,
   marked as not found, alongside the choices that do exist. Choosing one
   rewrites the reference.

The architectural point, taken directly from Home Assistant's raw editor and its
per-card YAML pane, is that **these are two renderers over one in-memory
document, not two formats with a converter between them**. An edit in either is
an edit to the same object; a save from either goes through the same path. The
cost of that choice is the comment loss stated under *JSON and YAML*, and it is
worth paying: the alternative — a UI copy and a file copy kept in step by
synchronisation — is exactly the design that produces Grafana's
`allowUiUpdates` trap, where a UI edit to a provisioned dashboard is silently
discarded the next time the file changes.

### What repair does not do

- **It does not rewrite references automatically.** No fuzzy matching on labels,
  no "closest interface", no heuristic remap. A wrong automatic match is worse
  than a visible failure, which is the whole lesson of Home Assistant's opaque
  ids.
- **It does not discard the original value.** Until the user repairs it, the
  file keeps exactly what it was imported with, byte for byte in that field. A
  dashboard imported, viewed and exported again without repair is unchanged in
  its references.
- **It does not block the import.** See the next section.

## Versioning and compatibility

`format_version` is a single integer at the top of the file. It versions the
*file format* and nothing else: not `opnview`'s release number, not the schema
version in `schema_version`, not the widget catalogue. The current value is `1`.

Each build declares two constants: the **current** format version it writes, and
the **minimum** format version it can read. Today both are `1`.

### An older file meeting a newer `opnview`

The file is **migrated forward on load**, through an ordered ladder of
one-version steps — `1 → 2 → 3 …` — each step a pure transformation of the
document. This is Grafana's `DashboardMigrator` shape, and it is chosen because
it is the only arrangement in which adding a version is a single new step rather
than an edit to every previous one.

Three properties of that ladder, decided here:

- **It is one-way.** A migrated document is not migrated back. A build that
  writes version *N* writes version *N*, and a user who needs the old file keeps
  the old file. Every product surveyed that tried to be bidirectional across a
  major format change either gave up or documented the result as a trapdoor.
- **It is applied in memory, not to the file.** Opening an old dashboard does
  not rewrite it on disk. The migrated version is written only when the user
  saves, and the save says which version it is writing.
- **There is a floor.** A file below the minimum supported version is
  **refused**, not guessed at, with a message naming the file's version, the
  minimum this build reads, and the last `opnview` release that could read it.
  Kibana's floor rule — same version, a newer minor of the same major, or the
  next major, never an older target — is the model.

### A newer file meeting an older `opnview`

The import is **refused**, cleanly and completely. Nothing is created, nothing
is partially applied, and the message names the file's `format_version`, the
highest this build understands, and the fact that an upgrade is the fix.

This is the deliberate opposite of the tolerant-reader posture the format takes
everywhere else, and the reason is that the two cases are not alike. An unknown
*parameter key* can be carried through safely: it is inert data in a widget that
otherwise works. An unknown *format version* is a statement that the document's
structure may have changed in ways this build cannot see — a field that moved, a
default that inverted, a placement unit that changed meaning. Reading it
optimistically risks rendering something confidently wrong, and a dashboard that
is confidently wrong about a firewall is worse than one that refuses to open.

Forward compatibility is bought a different way, and it is worth being explicit
that this is a deliberate trade: **within a version, readers are tolerant.**
Unknown parameter keys are preserved, unknown widget types render as unresolved
rather than failing, and unknown parameter values fall back to the catalogue
default with a notice. So a great deal can be added without a version bump, and
the bump is reserved for changes that genuinely alter the structure. Neither
Home Assistant nor Dashy nor Homepage nor Datadog has a format version field at
all, and all four have survived years of evolution on tolerant readers plus
deprecation-in-place; `opnview` keeps the integer because an eventual structural
change is cheaper to handle with one than without, not because it expects to
turn it often.

### Import validation

Two kinds of validity, treated differently and deliberately.

**Structural validity is fail-closed.** A document that is not parseable, that
lacks `format_version` or `canvases`, that has an empty `canvases` array, that
has a canvas with no `id` or no `title`, that has a widget with no `id`, no
`type` or no `placement`, that has a duplicate id within its scope, or that has
a non-integer in `placement`, is **refused whole**. Nothing is imported, and the
message names the offending canvas index, widget index and field — Datadog's
positional error message is the model, and the improvement on it is naming the
field as well as the index. A partial import of a structurally broken file is
never performed, because the user cannot then tell what they have.

**Referential validity is fail-open.** Every unresolved reference — an unknown
widget `type`, an `interface`/`client`/`rule` reference matching nothing locally, an
unrecognised parameter key, a parameter value outside the known vocabulary — is
**collected and reported, and the import proceeds.** The result is a dashboard
the user can see, with the broken parts visibly broken and repairable through
the two surfaces above, rather than an error page and no dashboard. Kibana's
import does exactly this for `missing_references`, and it is the only product of
the eight surveyed whose import treats a dangling reference as a user-resolvable
condition rather than as a failure or as silence.

The import report, shown once after an import, lists every collected condition
grouped by canvas and widget, and links each to the widget it affects.

In `opnview` an import is a file chosen or a text pasted in the interface. A
structurally valid one is stored as a **new dashboard** whose origin is
`imported` and which keeps the file's name, and is never merged into an existing
one. The referential conditions are collected from what the widget API answers
for each widget (`unknown_parameters`, `invalid_parameters`,
`unresolved_references`, `unknown_widget_type`), from
`GET /api/references/resolve` for each canvas scope, and from bare `shape`
widgets, which this build draws as not implemented. Nothing is rewritten
automatically; exporting the dashboard again before any repair writes back the
imported members and references unchanged.

## Worked example

One canvas, four widgets, shown first as JSON and then as YAML. **The two are
equivalent** — the same canvas, the same four widgets, the same placements and
the same parameters — and every field used is one the sections above define.
The fourth widget is there to exercise the hardest value shape the format
supports: an ordered list of series objects, one of which carries a reference of
its own.

**Every `type` and every parameter key below is one the catalogue declares.**
That is a constraint on this example rather than a remark about it: it is the
first thing a reader copies, so a key invented here would propagate into files
`opnview` then has to report as unknown. In particular the map widget is
`passed_traffic_world_map`, one of the **pair** the catalogue defines — passed
traffic and blocked traffic are two widgets by the maintainer's decision, not
one map with two layers — so there is no `series` parameter selecting between
them. A canvas wanting both places both, at whatever sizes it likes.

The values in this example are invented for illustration. `main-floor` is a
canvas slug, not a network; `opt3` is an example OPNsense interface identifier
and `Workshop` an example user label, both of which a real file would carry
because that installation's firewall reported them at runtime. Neither appears
anywhere in `opnview`'s code, templates or defaults.

### JSON

```json
{
  "format_version": 1,
  "title": "Interface review",
  "description": "Weekly look at what leaves each interface and what was stopped.",
  "generated_by": "opnview 0.1",
  "generated_at": 1758499200,
  "canvases": [
    {
      "id": "main-floor",
      "title": "Main floor",
      "description": "Volume, destinations and blocks for one interface.",
      "grid": { "columns": 12, "row_height": 40 },
      "widgets": [
        {
          "id": "volume-by-interface",
          "type": "interface_volume_ranking",
          "title": "Where the traffic is",
          "placement": { "x": 0, "y": 0, "w": 12, "h": 6 },
          "parameters": {
            "period": "7d",
            "measure": "bytes",
            "scope": "all",
            "limit": 10,
            "include_unlabelled": true
          }
        },
        {
          "id": "world-map",
          "type": "passed_traffic_world_map",
          "placement": { "x": 0, "y": 6, "w": 8, "h": 9 },
          "parameters": {
            "period": "7d",
            "scope": "north_south",
            "size_by": "bytes",
            "show_unplaced": true,
            "interfaces": [
              {
                "kind": "interface",
                "by": "identifier",
                "value": "opt3",
                "label": "Workshop"
              }
            ]
          }
        },
        {
          "id": "what-was-blocked",
          "type": "unified_blocked_feed",
          "title": "Blocked, and by what",
          "placement": { "x": 8, "y": 6, "w": 4, "h": 9 },
          "parameters": {
            "period": "7d",
            "engines": [
              "firewall_rule",
              "dns_advertising_list",
              "dns_threat_list",
              "security_engine"
            ],
            "group_by": "engine",
            "limit": 50
          }
        },
        {
          "id": "load-against-throughput",
          "type": "custom_chart",
          "title": "Load against throughput",
          "placement": { "x": 0, "y": 15, "w": 12, "h": 7 },
          "parameters": {
            "period": "24h",
            "tooltip": "all_series",
            "legend": "table",
            "series": [
              {
                "label": "CPU",
                "source": "firewall_metric",
                "metric": "cpu",
                "axis": "left",
                "render": "line",
                "aggregation": "mean"
              },
              {
                "label": "Temperature",
                "source": "firewall_metric",
                "metric": "temperature",
                "axis": "left",
                "render": "line",
                "aggregation": "max"
              },
              {
                "label": "Workshop throughput out",
                "source": "interface_throughput",
                "measure": "bits_per_second",
                "axis": "right",
                "render": "area",
                "aggregation": "mean",
                "interface": {
                  "kind": "interface",
                  "by": "identifier",
                  "value": "opt3",
                  "label": "Workshop"
                }
              }
            ],
            "axes": {
              "left": { "scale": "linear" },
              "right": { "unit": "bits_per_second", "scale": "linear" }
            }
          }
        }
      ]
    }
  ]
}
```

### YAML

```yaml
format_version: 1
title: Interface review
description: Weekly look at what leaves each interface and what was stopped.
generated_by: opnview 0.1
generated_at: 1758499200
canvases:
  - id: main-floor
    title: Main floor
    description: Volume, destinations and blocks for one interface.
    grid:
      columns: 12
      row_height: 40
    widgets:
      - id: volume-by-interface
        type: interface_volume_ranking
        title: Where the traffic is
        placement: { x: 0, y: 0, w: 12, h: 6 }
        parameters:
          period: 7d
          measure: bytes
          scope: all
          limit: 10
          include_unlabelled: true

      - id: world-map
        type: passed_traffic_world_map
        placement: { x: 0, y: 6, w: 8, h: 9 }
        parameters:
          period: 7d
          scope: north_south
          size_by: bytes
          show_unplaced: true
          interfaces:
            - kind: interface
              by: identifier
              value: opt3
              label: Workshop

      - id: what-was-blocked
        type: unified_blocked_feed
        title: Blocked, and by what
        placement: { x: 8, y: 6, w: 4, h: 9 }
        parameters:
          period: 7d
          engines:
            - firewall_rule
            - dns_advertising_list
            - dns_threat_list
            - security_engine
          group_by: engine
          limit: 50

      - id: load-against-throughput
        type: custom_chart
        title: Load against throughput
        placement: { x: 0, y: 15, w: 12, h: 7 }
        parameters:
          period: 24h
          tooltip: all_series
          legend: table
          series:
            - label: CPU
              source: firewall_metric
              metric: cpu
              axis: left
              render: line
              aggregation: mean
            - label: Temperature
              source: firewall_metric
              metric: temperature
              axis: left
              render: line
              aggregation: max
            - label: Workshop throughput out
              source: interface_throughput
              measure: bits_per_second
              axis: right
              render: area
              aggregation: mean
              interface:
                kind: interface
                by: identifier
                value: opt3
                label: Workshop
          axes:
            left: { scale: linear }
            right: { unit: bits_per_second, scale: linear }
```

One note on the YAML, because it is a real trap rather than a stylistic
preference: `period: 7d` is a string in YAML because `7d` is not a number, but
`period: 1h` is likewise a string and `columns: 12` is an integer. The format
requires the reader to coerce a scalar to the type the catalogue declares for
that key rather than trusting YAML's inference, and to reject a value that
cannot be coerced rather than guessing.

### What this example demonstrates

- **A portable widget and a non-portable one on the same canvas.**
  `what-was-blocked` carries only scalars and resolves anywhere.
  `world-map` carries an `interface` reference and will not resolve on an
  installation that has no interface identified as `opt3` — on arrival it keeps
  its place, renders empty, and says *"This widget is scoped to an interface this
  installation does not have: interface identifier `opt3`, labelled 'Workshop'
  where the dashboard was built. Choose a local interface to repair it."*
- **Placement as an explicit contract.** Four widgets in a 12-column grid: one
  full-width band, an 8/4 split beneath it, then another full-width band.
- **Absent optional fields.** `world-map` has no `title` and takes the
  catalogue's; `what-was-blocked` has no `interfaces` and is therefore
  network-wide; the canvas carries neither `period` nor `scope`, so its range is
  `24h` and it scopes nothing.
- **A list of series, with a reference inside one of them.** `load-against-
  throughput` carries three series across two axes, each an object with its own
  label, render and aggregation, and the third scoped to an interface. That last
  point matters for repair: **a reference nested inside a series can fail on its
  own**, so the editor highlights `series[2].interface` and the chart renders its
  other two series normally with the third drawn as a labelled gap. A widget is
  not all-or-nothing.
- **Two different periods on one canvas.** The first three widgets show seven
  days and the fourth shows twenty-four hours: each carries its own `period`,
  which wins over the canvas's range, so each card says "own range". A widget
  with no `period` of its own would take the canvas's.

## Decided choices

The draft listed six questions as open, each with its options and their costs.
Each is decided below, with its reason. **None of them is the maintainer's own
decision**: they were decided by the orchestrator on 17 October 2026, on the
Planner's recommendation, while the maintainer was unavailable
(`specs/SPEC-step-7-frontend.md`, *Decided questions*, D1 to D6). The options
weighed are kept, shortened, so the cost of each decision stays visible.

### 1. Which syntax an export produces — the user chooses at export (D1)

**The user chooses YAML or JSON at export**, preset from the editor's syntax
radio, which is YAML by default. Why: the step-3 decision put YAML first with a
toggle, and the maintainer wanted YAML available. The cost accepted is the one
the option carried: two serialisers kept in step, and two sets of round-trip
tests, which the conformance fixtures run in both syntaxes. Rejected: JSON only
(unpleasant to hand-edit, no comments), YAML only (quoting surprises such as
`7d` versus `7` and `no` versus `false`), and both files from one action (two
files where one was expected, and a question of which is authoritative).

### 2. What `placement` means — absolute coordinates (D2)

**Absolute `x`, `y`, `w`, `h` in grid units, as drafted.** Why: edit mode moves
and resizes a card by pointer and by keyboard on a grid (Homarr's mechanics), and
both need a position the reader chose. A hand-edited overlap or hole is handled
by the rules of *Placement*: `y` is advisory and compacted, `w` is clamped.
Rejected: ordered flow with a size only (the reader cannot place a widget), and
both behind a discriminator (two layout engines).

### 3. When parameters are validated against the catalogue — at import, fail-open, reported (D3)

**At import, fail-open and reported, as drafted.** Why: it is already the widget
API's behaviour — an unknown key is answered in `unknown_parameters`, an invalid
value in `invalid_parameters` and replaced by its default — and it is what the
forward-compatibility argument of *Versioning and compatibility* needs. The cost
accepted: a misspelt key is inert until the reader looks; the import report and
the card's "kept, not understood" chip are where it is shown. Rejected:
fail-closed (a newer file could not be opened at all), and at render only (no
import report).

### 4. How a repaired reference is recorded — the stored document is rewritten (D4)

**A repair rewrites the stored document; the user's original file is never
touched, and there is no table of local bindings.** Why: a bindings table is a
second source of truth, the very thing this format refuses (*Two coordinated
surfaces, one model*). The cost, stated rather than hidden: re-importing an
updated version of the same file breaks the same references again, so a shared
dashboard is repaired on each installation each time it is imported. Rejected:
local bindings beside the file, and a sibling field keeping the authored value.

### 5. One dashboard per file (D5)

**One dashboard per file, and export is per dashboard.** Why: one file, one
thing — a dashboard is a unit that can be pasted into a message. Rejected: a
bundle (ids unique across the bundle, and partial import as a new question).

### 6. Signing or checksumming an export — neither (D6)

**No checksum and no signature.** Why: a dashboard file is never trusted input
and never executed, and the draft recommended no option. A checksum detects
nothing deliberate, and a signature brings key management the project has no
use for.

### And one addition: the canvas `period` and `scope` (D7)

Two optional canvas fields, `period` and `scope` (*The canvas period and
scope*), were added to format version 1 **without a version bump**. Why: the
step-3 mockup already wrote a canvas `period`, `ROADMAP.md` puts the scope on the
canvas, and no released reader of the format existed that a bump would protect.

## What this document does not do

- It defines **no JSON Schema artifact**. The structural validation is code,
  written twice and held together by shared fixtures: in Go, authoritative, on
  every save (`internal/dashboard`), and in the front's document module, for the
  editor (`front/core/document.js`).
- It chooses **no rendering technology**: how a widget is drawn is
  `docs/front-plugins.md`'s contract (first-party SVG, no charting library).
- It fixes **no UI wording**. The sentences quoted above fix what the interface
  must say, not how it reads; the words are the interface's string catalogue's.
