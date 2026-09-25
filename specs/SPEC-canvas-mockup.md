# SPEC — Step 3 canvas mockup: named canvases, widget mechanics and the two editing surfaces

Status: APPROVED
Cycle kind: interface

## Context

The repository has **no frontend of any kind**. This cycle creates the first
interface artefact in the project.

`ROADMAP.md` step 3 is amended: its subject is a **representative canvas**, not
a fixed overview screen. Steps 5 and 7 are amended in the same direction — one
endpoint per widget type, the period a widget parameter, named canvases behind
tabs, and `docs/widget-catalogue.md` replacing the seven-screen list. Step 3's
status stays `To do`; only the maintainer flips it, after looking at the page.

A first mockup attempt was rejected outright. Four causes, all forbidden here by
construction: a one-line design brief, sterile documentation-range data
(`192.0.2.1`, `Zone A`), a banner announcing the data was fabricated, and a page
showing almost nothing the maintainer wanted. He has since said explicitly: make
the data look realistic, be creative, put no "generated data" disclaimer
anywhere.

The binding references are `docs/ui-references.md` (in particular *The
maintainer's recorded preferences*, *Complete image index*, *Conclusions for
opnview*), `docs/widget-catalogue.md`, `docs/dashboard-format.md`, and
`ROADMAP.md` steps 3, 5 and 7 with *Rules that apply to every step*. **This spec
cites them; it does not paraphrase them into a second list of rules.** The
builder reads them.

Two structural facts constrain the artefact rather than the product: the page
must render opened from disk with networking off, mirroring the two-outbound-call
rule; and the values invented for it are hardcoded by design, which has nothing
to do with the zero-hardcoded-configuration rule governing product code and must
never migrate into it.

## Decisions taken by the lead

The maintainer delegated the lead through to this mockup. The Planner's eleven
open questions are settled as follows, each as the Planner recommended.

1. **The connection tree is drawn** as a proposed twenty-sixth catalogue
   entry, visibly marked as not yet catalogued.
   He named it and his preference is recorded with two reference images; silently
   omitting something he asked for is precisely what sank the first attempt.
2. **Four canvases**: dense and general; blocked; firewall health; an imported
   dashboard carrying the repair case.
3. **A side panel** beside the live canvas, the two editing surfaces as tabs
   within it. A modal hides the rendering; inline expansion makes the
   highlighted-line repair case unreadable.
4. **Fifteen catalogue widgets minimum.** All twenty-five would produce the wall
   of density the brief bans.
5. **All six palettes.** The values are already recorded; once colours are
   tokens, a palette is a token block.
6. **Real country geometry with borders, embedded in the file.** Public-domain
   data, baked in at authoring time, never fetched at runtime. Its source,
   version, date and licence are recorded in a comment beside it.
   *(Amended 2026-09-25: the hand-authored outline and the vertex budget were a
   self-imposed constraint no project rule required.)*
7. **The catalogue wins over the format document's worked example**, which still
   shows one layered world map. The contradiction is recorded as a defect to fix
   later, not silently resolved.
8. **Telemetry is drawn and marked**, per gap identifier, in-widget. The builder
   must declare this resolution in its report so the ergonomist reads it as
   settled rather than as a critical finding.
9. **JSON and YAML behind a toggle, YAML by default.**
10. **Explicit move and resize controls must work**; pointer dragging is
    desirable but is not an acceptance criterion. What must be demonstrated is
    that the mechanic exists and writes through to the document.
11. **One HTML file.**

### Priority order, if the cycle runs long

This is the lead's decision and it exists because the maintainer wants something
to look at. Protect in this order; hand back with a lower item unfinished rather
than a higher one thinned:

1. **AC5, AC6, AC8, AC19** — the widgets he asked for, the two separate maps,
   the four degraded states, realistic self-consistent data. The first attempt
   died here.
2. **AC12, AC15** — the code view and the import-repair case. This is the
   product's differentiator.
3. **AC10, AC17, AC18** — unsmoothed charts, the palettes, tokenised colour.
4. **AC16** — the edit-mode mechanics writing through to the document.
5. **AC11, AC13** — the paired-axis chart and the nested `series` shape.

## Scope

No Go, no SQL. Two deliverables:

1. `docs/mockups/canvas-mockup.html` — one self-contained file: inline CSS,
   inline JavaScript, inline SVG.

### Structure

**Four named canvases behind tabs**: dense and general (outbound traffic, sites,
who talks to whom); blocked (what was blocked and by what); firewall health
(the telemetry widgets, each marked as not yet backed by the model); imported
(the import report and the unresolved-reference repair case).

Chrome: the industrial palette's layout shape composed as Cloudflare Radar
composes it — left rail, scoped header carrying the selectors, angular cards
each with a one-line explanation under its title. Theme picker in the header.

### Widgets

Every widget on the page is an entry of `docs/widget-catalogue.md`, rendered
under the entry's own name, drawing what its *Shows* field requires including
the mandatory UI copy, exposing the parameters its *Parameters* field declares,
and in the degraded cases rendering the state its *Empty state* field specifies.
The minimum set maps the maintainer's named wants onto entries: *Sites by
device*, *Top sites*, *Attribution rate per device*, *Passed-traffic world map*,
*Blocked-traffic world map*, *Unified blocked feed*, *Blocked by firewall rule*,
*Blocked DNS lookups*, *Alerts by signature*, *Segment volume ranking*, *Segment
traffic matrix*, *Device volume ranking*, *Traffic over time by scope*,
*Firewall health overview*, *Interface throughput*, *Custom chart*, *Traffic
Sankey*, *Source availability*. Plus the connection tree, per decision 1.

### The two editing surfaces

Per `docs/dashboard-format.md`: a **code view** of the real dashboard document,
syntax-highlighted, Home Assistant's weight and not an IDE — no file tree, no
minimap, no command palette, no diff viewer; a **no-code selector** for the same
widget whose reference-bearing fields are dropdowns populated only from what
this invented installation has; both opening beside the live canvas with an
explicit apply step.

The **import-repair case** lives on canvas 4: an imported dashboard whose
`segment` reference does not resolve here and one of whose widget types is
unknown to this build. Affected widgets keep their footprint and render the
unresolved-reference state; the code view highlights the offending lines in
place with an inline "no data" notice naming `kind`, `by`, `value` and the
exporting installation's `label`; the no-code selector offers local equivalents
with the unresolved value shown as the current selection, marked not found.

### The invented network

One plausible RFC 1918 network, committed to and used consistently across every
widget: segment labels a person would type, device names in the shapes people
use, well-known public domains, operators and AS numbers as outbound
destinations. No real network's data. **No disclaimer banner anywhere.** Figures
reconcile wherever a reader could check them.

## Acceptance criteria

Each names the verifier that can check it. `factory-verifier` has no browser;
`factory-ergonomist` renders the page and inspects the DOM and computed styles.

- [ ] **AC1** — Both deliverables exist; no other file is created or modified
      outside `docs/mockups/`. *(verifier)*
- [ ] **AC2** — `docker compose run --rm checks` and
      `docker compose run --rm schema-checks` both exit 0; no change under
      `migrations/`, `sql/`, `internal/`, `cmd/` or `go.mod`. *(verifier)*
- [ ] **AC3** — The HTML loads no resource from any host: no `http://` or
      `https://` in any `src`, `href`, `@import`, `url()`, `fetch`,
      `XMLHttpRequest`, `WebSocket`, `EventSource` or `<link>`; no
      `<script src>`, no external stylesheet, no font file, no favicon, no
      remote image, no map tile. `http(s)` strings are permitted only inside the
      invented data and in prose, never in a fetching position. *(verifier by
      grep; ergonomist by confirming zero network requests from disk)*
- [ ] **AC4** — Exactly four canvases as tabs, each with a distinct title,
      reachable in one click. No canvas, widget, parameter control or editing
      surface is reachable only through a menu inside a menu. *(ergonomist)*
- [ ] **AC5** — At least **fifteen distinct catalogue widgets** appear, and every
      widget whose heading names a catalogue entry carries that entry's title.
      **No widget carries explanatory copy**: no question line, no caption
      describing the drawing, no observation-point sentence, no
      site-names-are-inferred sentence, no detection-coverage sentence, no
      marker, tooltip or disclosure carrying any of them. A title names, a figure
      states, a unit and an axis label qualify, and a degraded state is named in
      one or two words. *(ergonomist; verifier counts headings and greps for the
      withdrawn copy)*

      *Amended 2026-09-25: explanatory copy withdrawn from the interface.*
- [ ] **AC6** — Both world maps are present as **two separate widgets**, neither
      one map with two layers. Both render as point marks on a de-saturated
      basemap with detail in a popup: no choropleth, no arcs. *(ergonomist)*
- [ ] **AC7** — Each map shows its unplaced-volume counter beside the map, with
      a non-zero value on one and zero on the other, so the counter is shown even
      at zero; the blocked map additionally shows the DNS counter stating a
      refused lookup resolves to no address, linking to *Blocked DNS lookups*. On
      each map, placed + unplaced equals the stated total in the rendered
      numbers. *(ergonomist; verifier checks the arithmetic)*
- [ ] **AC8** — The four conditions each render as a designed panel keeping the
      widget's footprint and title, each distinguishable from the others and from
      an error: source unavailable; source present but switched off; source
      reachable and silent; a reference unresolved on import. **No panel displays
      a zero, a blank area or a red failure state**, and each names the condition
      in words. Each degraded widget links to *Source availability*.
      *(ergonomist; verifier checks the copy)*
- [ ] **AC9** — Suricata is shown degraded in at least one state.
      *(ergonomist)*

      *Amended 2026-09-25: the per-interface coverage statement is withdrawn.
      AC5 governs — the interface carries no explanatory copy.*
- [ ] **AC10** — **Charts are unsmoothed by default, and no interpolation ever
      invents or hides a sample.** On the delivered document every plotted series
      is an SVG `polyline`, or a `path` whose `d` contains only `M` and `L`. A
      `line_interpolation` control may offer `smooth`, provided the curve passes
      through every measured point and draws no point that was not measured;
      every series element carries `data-sample-count` equal to the number of
      points drawn, in every mode. Area fills appear only beneath stacked series,
      with the line passing through the same real points. *(verifier by grep on
      the delivered document, and by reading the interpolation code; ergonomist
      in the DOM)*

      **Amended 2026-09-25**, on the maintainer's decision. As approved, this
      criterion banned `C`, `Q`, `S`, `T` and `A` outright, which turned a
      preference into a prohibition: Grafana is the reference because it does not
      smooth *by default*, not because a curve is forbidden. A Catmull-Rom curve
      passes through every sample, so it hides no spike and invents no reading —
      which is the property the criterion exists to protect, and which the
      amended text now states directly instead of proxying it through a list of
      path commands. The default stays `linear` on every chart of the delivered
      document.
- [ ] **AC11** — At least one chart plots two unrelated series on paired axes,
      each legend entry stating its unit and axis, and a tooltip on a stacked
      chart listing every series at the hovered instant. *(ergonomist)*
- [ ] **AC12** — A code view of the dashboard document sits beside the rendered
      canvas, syntax-highlighted, showing a document that validates against
      `docs/dashboard-format.md`: `format_version: 1`, a `canvases` array, and
      per widget an `id`, a `type`, a `placement` of four integers and a
      `parameters` object. Every `type` is the snake_case form of a `###` heading
      in `docs/widget-catalogue.md`, or the entry's own **Type** field where it
      differs. Every parameter key is one the entry declares. No file tree,
      no minimap, no command palette, no diff viewer. *(verifier for the text;
      ergonomist for the rendering and the absent IDE furniture)*
- [ ] **AC13** — The code view includes at least one widget whose
      `parameters.series` is an ordered list of objects, one carrying a reference
      object of its own. *(verifier)*
- [ ] **AC14** — The no-code selector is present for the same widget and edits
      the same document: every reference-bearing field is a dropdown, every
      option exists in the invented installation, no free-text reference field.
      *(ergonomist)*
- [ ] **AC15** — The import-repair case renders: canvas 4 shows an import report
      grouped by canvas and widget; at least one widget renders the
      unresolved-reference state naming `kind`, `by`, `value` and the exporting
      `label`; the code view highlights the offending lines **in place**, each
      with an inline "no data" notice; the repair dropdown offers local
      equivalents with the unresolved value shown as current and marked not
      found. Nothing rewrites a reference automatically; nothing blocks the
      import. *(ergonomist; verifier checks copy and highlight markup)*
- [ ] **AC16** — Widget mechanics are demonstrated, not implied: an edit mode
      entered from the canvas; visible move and resize affordances; an explicit
      "add a widget here" target; an add-widget picker listing catalogue types
      with the entry's question as its description. Moving or resizing through
      the provided controls changes that widget's `placement` in the code view.
      *(ergonomist, interactively)*
- [ ] **AC17** — With no stored override the page follows `prefers-color-scheme`
      in both settings; selecting a palette persists across a reload and takes
      precedence afterwards. Six palettes offered by name at the values recorded
      in Family C, Nord included in both modes. *(ergonomist; verifier diffs the
      values against Family C)*

      **Amended 2026-09-24**, on the maintainer's decision. As approved, this
      criterion required the picker to state that Nord has no light variant and
      what happens on a light-preferring system. That is true of the upstream
      Nord project and false here: the maintainer's own site, which
      `docs/ui-references.md` now records as the authoritative source for the
      five named palettes, defines a light Nord. The clause described a fallback
      that must not be built.
- [ ] **AC18** — Every colour used by a rule is a `var(--…)`; no literal colour
      outside the palette token blocks. The card radius resolves to `0.25rem` as
      a computed style, and the font stack is the system stack from Family C with
      no web font. *(ergonomist for computed styles; verifier by grep)*
- [ ] **AC19** — The data is plausible, internally consistent and carries no
      disclaimer. No address from the documentation ranges appears as a local
      address; local addressing is RFC 1918; no string states or implies the data
      is generated, sample, fake, demo, placeholder or illustrative. Every
      derived figure reconciles with its parts in the rendered values: a
      ranking's rows sum to its total; a matrix's margins agree with its cells;
      an attribution rate equals attributed ÷ total for the same device.
      *(verifier by grep and arithmetic; ergonomist on the page)*
- [ ] **AC20** — Nothing is presented as model-backed that the model cannot
      produce. Every widget drawing on a gap in *Gaps found* is marked on screen
      as not backed by the model, and carries the gap identifier in a `data-gap`
      attribute. The identifier is not printed to the reader: it is a code they
      cannot look up. *(ergonomist; verifier checks the attributes)*
- [ ] **AC22** — Every user-visible string, identifier, class name, custom
      property and comment in both deliverables is in English. *(verifier)*
- [ ] **AC23** — Step 3's status in `ROADMAP.md` still reads `To do`.
      *(verifier)*

      **Amended 2026-09-24**, on the maintainer's decision. As approved, this
      criterion required `ROADMAP.md` to be unchanged. The orchestrator amended
      its palette clause during this cycle, out of scope; rather than hide that,
      the correction has been committed as a documentation change of its own,
      separate from the mockup, and this criterion now asserts what it was
      protecting — that the mockup cycle does not move the plan — instead of
      asserting that one file's bytes are untouched.

### For maintainer review — not acceptance criteria

No agent may make these judgements, and none may claim to have made them.

1. Whether the page looks like something he wants to open every day.
2. Whether the density is right — useful, short of the wall-of-tables ban.
3. Whether the invented network reads as a real installation.
4. Whether the code view is the right weight, or still too much editor.
5. Whether the edit-mode affordances feel like Homarr's freedom rather than a
   toy.
6. Whether four canvases is the right number and split.
7. Whether the connection tree earns a place as a twenty-sixth entry.

## Out of scope

- Any Go, any HTTP handler, any template in the binary, any `embed`.
- Any change to `migrations/`, `sql/` or the data model. The ten gaps stay open.
- Adding a catalogue entry for the connection tree. Proposals go in the
  builder's *Proposals* section.
- Implementing the dashboard format: no parser, serialiser, validator or import
  code. The code view renders a document; it does not implement one.
- Choosing a charting library, framework or rendering technology for the
  product.
- Any real geographic dataset, real capture or real installation's data.
- Flipping step 3's status.

## Risks

- **The world map with nothing loadable.** The basemap must be authored inline.
  A detailed or third-party outline is both a weight and a provenance problem.
- **`docs/dashboard-format.md` contradicts the catalogue** on the world map.
  Decision 7 resolves it for this cycle; the document still needs fixing.
- **The catalogue defines no widget type identifiers.** The snake_case rule is
  this spec's convention, not the catalogue's, and a later cycle may differ.
- **A static file demonstrating a dynamic interaction.** AC16 is the largest
  single piece of work and the most likely to be cut short — which is why it is
  fourth in the priority order rather than first.
- **Arithmetic consistency across fifteen widgets over one invented network is
  genuinely hard** and is the likeliest source of a correction round. Derive
  every figure from one small dataset held in one place in the file rather than
  writing numbers per widget.
- **Six palettes multiply the check surface** across two `prefers-color-scheme`
  settings; one literal colour anywhere shows up as a theme that does not
  change.
- **Reference screenshots are gitignored** and may be absent for the ergonomist,
  weakening visual conformance to text comparison. Expected, not a defect.
