# SPEC — Interface and dashboard-format research (documentation-only)

Status: APPROVED

## Context

The repository holds steps 1 and 2: a cited API survey pinned to OPNsense
26.7.3, an executable schema with 287 passing checks, and the documents that
explain them. **There is no interface document of any kind.** The entire
written brief for the view is three paragraphs of `ROADMAP.md` step 3 and the
seven-screen list of step 7.

A first mockup was attempted and **rejected outright**. The cause was not the
Builder's work: it was that the interface brief was one line ("UniFi Network as
the aesthetic reference") while the schema had a 45-criteria executable
specification. The view is the product — the tool is called `opnview` — and it
was treated as a finishing touch. This cycle is the correction. It produces
**documents only**, researched and cited to the standard of
`specs/SPEC-opnsense-api-survey.md`, so the mockups that follow are judged
against a written reference rather than against anyone's taste.

The maintainer has redefined the product since `ROADMAP.md` was written. **Five
decisions are already taken. They are not open, and the research must not
revisit them; it establishes how to do them well.**

1. **Named canvases, not fixed screens.** The user creates as many dashboards
   as they want, each filled with widgets they add, place and size. Tabs
   replace menus. No fixed seven-screen structure, no view buried in a submenu.
2. **Everything is describable as code, in JSON and/or YAML** — both, not XML.
3. **Dashboards are portable.** Export from one installation, import into
   another, same view. Where the importing installation has no data for a
   widget, that widget renders as an explained empty state — never an error,
   never a fabricated zero.
4. **Palettes:** the maintainer's industrial palette by default (the complete
   light and dark custom-property set in
   `C:\Users\fuzzz\Downloads\maestro-espidf-components-main\maestro-espidf-components-main\tb_http_server\www\style.css`),
   with named options — Tokyo Night, Dracula, Nord, Rosé Pine, Catppuccin — at
   their official published values, each cited.
5. **Theme on first launch follows the operating system**, user-overridable
   afterwards. This replaces the ROADMAP's "light by default, dark never the
   default".

Two model gaps are already known and must be recorded rather than discovered:
`docs/opnsense-api-survey.md` establishes that
`/api/unbound/overview/search_queries` returns a `blocklist` field, but
`dns_resolution` has no `blocklist` column — "blocked by which list" is
unanswerable today; and no query in `sql/queries/screens.sql` aggregates site
names, so "which site, by whom" has none.

## Decisions taken with the maintainer

1. **Three documents**, as the requirement names them. Cross-document
   consistency on portability is an acceptance criterion rather than a reason
   to merge.
2. **Two standards, because this document does two different jobs.**

   **Factual claims about how a product works** — portability, format,
   versioning, import validation — are what later cycles will build on, so they
   keep the full discipline: a well-formed `https://` URL from the product's own
   documentation or repository, References matching the body both ways, and the
   Verifier **opening 8 of those URLs at random** to compare each against the
   claim it backs. A blog or aggregator is admissible here only as a last resort
   and only marked `UNVERIFIED:`.

   **Visual inspiration is not held to that standard at all.** This is not a
   market study; it is a search for ideas. An image may come from anywhere: the
   vendor's own site by preference, but a blog post, an old blog post, a
   screenshot in a forum, a review, a design gallery. **Pure concepts that
   correspond to no shipping product are explicitly wanted** — a dashboard
   someone designed on Dribbble or Behance and never built is as useful here as
   a running product, sometimes more. There is no domain allow-list for images,
   no `UNVERIFIED:` marker on them, and no requirement that the thing pictured
   be purchasable. Record where each came from as provenance, not as proof.
3. **Deep on three, tight elsewhere.** Grafana, Home Assistant (Lovelace) and
   Dashy get the detailed treatment: that is where the useful prior art is. The
   other products get a tight paragraph per sub-heading.
4. **Images are the most important output of this cycle, and they are never
   committed.** UX cannot be judged from prose. They live in
   `docs/ui-references/screenshots/`, which `.gitignore` covers.

   **A screenshot of a documentation page is worthless** — it pictures an
   article about an interface, not an interface. The first attempt at this cycle
   failed exactly there. Capture the thing itself: a live public demo where one
   exists, otherwise an image of the real product from wherever it can be found,
   otherwise a concept. Never a doc page, never a marketing hero banner.

   Be generous. Thirty or more images is the target, not seven. An idea worth
   stealing is worth a file.
5. **The widget catalogue is a first pass, labelled as such**, covering the
   maintainer's named questions plus whatever the mapping exercise turns up,
   and declaring itself extendable.
6. **`docs/dashboard-format.md` carries a worked example** — one canvas, two or
   three widgets — shown in both JSON and YAML, inside fenced blocks in the
   Markdown. No separate file.
7. **The industrial palette is recorded as a source only.** Naming `opnview`'s
   own semantic tokens is a design decision belonging to the mockup cycle.
8. **Each widget states which of the seven existing screen queries it reuses,
   adapts or replaces.** Cheap now, and it stops step 5 rediscovering it.
9. **The ROADMAP amendment proposal covers the four mandated items plus
   anything the research directly invalidates, and proposes no renumbering.**
   Renumbering has already cost this project once.
10. **A fourth family exists: visual inspiration.** It is a moodboard, not a
    survey. Dashboards and network-visibility interfaces that look good or do
    something clever, from any source, including concepts that were never
    built. Each entry is an image plus one or two lines saying what is worth
    taking from it. This is the section the maintainer cares most about.

## Scope

Create three documents under `docs/`, one screenshots directory that is not
committed, and one line in `.gitignore`. No Go, no SQL, no HTML, no CSS, no
JavaScript, no migration, no edit to `ROADMAP.md`.

### Deliverable 1 — `docs/ui-references.md`

Top-level (`##`) sections, in this order:

1. **Scope and method** — what was studied and how; what `UNVERIFIED:` means;
   why the industrial palette is cited by absolute path rather than URL and is
   therefore reproduced verbatim; that screenshots are local-only and not
   committed; and that reading vendor documentation while authoring is research
   activity by the Builder, not a third outbound call by the application.
2. **The product as decided** — the five settled decisions restated.
3. **Family A — how modular dashboards actually work.** One `###` subsection
   per product for all eight: Grafana, Home Assistant (Lovelace), Netdata,
   Datadog, Kibana, Homepage, Homarr, Dashy. Each carries the same seven
   `####` sub-headings: **Grid and reflow**; **Adding, configuring and removing
   a widget**; **Widget parameters**; **UI, file, or both**; **Portability**;
   **Format versioning**; **Import validation**. Grafana, Home Assistant and
   Dashy get the deep treatment.
4. **Family B — what belongs in the widgets.** One `###` subsection per product
   for all seven: UniFi Network, Firewalla, NextDNS, Pi-hole, ntopng, Zenarmor,
   Cloudflare Radar. Each carries: **Outbound traffic**; **World map of
   destinations**; **Per-device view**; **Blocked domain and what blocked it**;
   **Per-VLAN or per-subnet traffic**; **What it gets right**; **What it gets
   wrong, and where it buries a view**.
5. **Family C — the visual language.** The industrial palette reproduced in
   full; the five named palettes at their official values with a source each;
   and the plain statement that this language is angular, dense and
   high-contrast, that it is not the rounded light UniFi aesthetic step 3
   specifies, and that this document records options rather than choosing.
6. **Family D — visual inspiration.** A moodboard. Dashboards and
   network-visibility interfaces worth stealing from, wherever they come from,
   including designs that were never shipped. Each entry: the image, where it
   came from, and one or two lines on what is worth taking. Group loosely by
   what the idea is about — layout and density, the shape of a map, how a list
   of domains is presented, how a blocked thing is distinguished from an
   allowed one, how an empty widget is handled, how a theme picker is offered.
7. **Conclusions for opnview** — definite answers, not a survey.
8. **Proposed ROADMAP amendment.**
9. **References.**

### Deliverable 2 — `docs/widget-catalogue.md`

One `###` entry per widget, each carrying exactly these six bolded fields in
order: **Question**, **Shows**, **Parameters**, **Data**, **Existing query**,
**Empty state**. Plus a coverage table and a consolidated *Gaps found* section.

### Deliverable 3 — `docs/dashboard-format.md`

The first draft of the format, informed by Family A rather than invented, with
a worked example in JSON and YAML and an *Open choices* section.

## Acceptance criteria

### Files and project commands

- [ ] AC1 — The three documents exist, are valid Markdown, and are written
      entirely in English; no French word appears in any of them.
- [ ] AC2 — `git status --porcelain` shows exactly five paths: the three added
      documents, the modified `.gitignore`, and this spec file. `ROADMAP.md`,
      `README.md`, `CLAUDE.md` and the three existing `docs/` files are
      unmodified. No `.go`, `.sql`, `.html`, `.css`, `.js`, `.json` or `.yaml`
      file is added, modified or deleted.
- [ ] AC3 — `.gitignore` gains an entry covering `docs/ui-references/screenshots/`,
      and `git status --porcelain --untracked-files=all` lists no file under it.
- [ ] AC4 — `docker compose run --rm checks` and
      `docker compose run --rm schema-checks` both exit 0, unchanged.

### `docs/ui-references.md`

- [ ] AC5 — The nine `##` sections listed in *Scope* are present, in order.
- [ ] AC6 — Family A contains one `###` subsection for each of the eight named
      products. A missing product fails.
- [ ] AC7 — Every Family A subsection contains all seven required `####`
      sub-headings. One missing sub-heading in one product fails. A sub-heading
      answered "not applicable" says why in a sentence.
- [ ] AC8 — Family B contains one `###` subsection for each of the seven named
      products, each with all seven required `####` sub-headings.
- [ ] AC9 — Grafana, Home Assistant and Dashy each carry substantially more
      material than the median product of their family, measured as line count:
      each of the three exceeds the median Family A product by at least half
      again. Depth is the decision; this is its mechanical proxy.
- [ ] AC10 — Every `###` product subsection in Families A and B contains at
      least one full `https://` URL, and every claim is either followed by a
      citation or carries `UNVERIFIED:`.
- [ ] AC11 — The domain allow-list applies to **factual claims about how a
      product works** — the seven Family A sub-headings and the palette values
      of Family C — and to nothing else. For those, every cited domain is the
      product's own documentation or repository, or the palette's own published
      source; a blog, forum or aggregator fails unless it is the only available
      source and is marked `UNVERIFIED:` with that stated. **The allow-list does
      not apply to images anywhere in the document**, and applying it to them
      fails this criterion.
- [ ] AC12 — Family C reproduces the industrial palette as a table listing
      every CSS custom property in the maintainer's `style.css` with its light
      and dark values, plus the `0.25rem` radius, the nav + sidebar + main
      layout, and the semantic status colours. The source is cited as the
      absolute path and *Scope and method* explains why.
- [ ] AC13 — Family C documents all five named palettes with their official
      values and at least one `https://` URL each to the palette's own source.
      Values taken from a third-party rendering carry `UNVERIFIED:`.
- [ ] AC14 — Family C states that the language is angular, dense and
      high-contrast, that it is not the ROADMAP's rounded light UniFi
      aesthetic, and that this cycle records options rather than choosing.
- [ ] AC15 — *Scope and method* defines `UNVERIFIED:`, states the
      research-is-not-an-outbound-call rule, declares the citation allow-list,
      and states that screenshots are local-only and not committed.
- [ ] AC16 — *Conclusions for opnview* answers all four questions in definite
      sentences, none deferred: what a widget references instead of a local
      identifier; what happens when a reference cannot be resolved locally; how
      the format is versioned; what an import does with a malformed or dangling
      file.
- [ ] AC17 — *Proposed ROADMAP amendment* is the last content section before
      *References*, covers at minimum the seven-screen structure of step 7, the
      "light by default" rule, the UniFi aesthetic brief of step 3 and the shape
      of step 5's HTTP API, gives for each the `ROADMAP.md` text it contradicts
      and a proposed replacement, proposes **no renumbering**, and states that
      `ROADMAP.md` was not edited and the maintainer arbitrates.
- [ ] AC18 — *References* lists every `https://` URL in the body with the title
      of what it points at, and contains no URL absent from the body. Both
      directions checked.

### Screenshots

- [ ] AC19 — At least **thirty** images exist under
      `docs/ui-references/screenshots/`, each referenced from the document by
      relative path. At least one covers each of Grafana, Home Assistant and
      Dashy; at least four cover Family B products; and at least **ten** belong
      to Family D, the inspiration moodboard.
- [ ] AC20 — **No image is a screenshot of a documentation page, a marketing
      hero banner, or a page of prose about an interface.** Every image shows an
      interface: a live demo, a real product in use, or a design concept. An
      image whose subject is an article fails this criterion, and the previous
      attempt at this cycle failed precisely on it.
- [ ] AC21 — Every image reference states where it came from and the date it was
      captured, as provenance. For Family D that is the only requirement on its
      source: a blog, an old post, a forum, a design gallery and an unbuilt
      concept are all acceptable, and the document says so.
- [ ] AC22 — No image shows a real network's addressing, hostnames or device
      names. Where a public demo exposes such values the capture is framed to
      exclude them or the file is not used, and the document records that.
- [ ] AC23 — Every image reference is accompanied by prose naming what the image
      shows and what is worth taking from it, so the document carries its
      argument even where an image is missing — the images are not in the
      repository and will not survive a clone.

### `docs/widget-catalogue.md`

- [ ] AC22 — Every widget entry is a `###` heading whose body contains all six
      required bolded fields — **Question**, **Shows**, **Parameters**,
      **Data**, **Existing query**, **Empty state** — in that order.
- [ ] AC23 — Every **Data** field resolves, per need, either to `table.column`
      pairs that exist in `migrations/*.sql` and are verifiable by grep, or to
      the literal marker `MISSING FROM MODEL:` followed by what would have to
      be added. A need expressed only in prose, or naming a table or column
      that does not exist and is not marked missing, fails.
- [ ] AC24 — Every **Existing query** field names which of the seven queries in
      `sql/queries/screens.sql` the widget reuses, adapts or replaces, or states
      that none applies and why.
- [ ] AC25 — A coverage table maps each of the maintainer's named questions —
      site used and by whom; world map of passed **and** blocked traffic;
      blocked content unified across DNS advertising lists, DNS threat lists,
      Suricata and firewall rules; traffic by VLAN; traffic by MAC or IP;
      outbound / inbound / inter-VLAN as selectable scopes — to the entries
      answering it. Every named question has at least one entry.
- [ ] AC26 — The two known gaps appear with the fix each needs, cited to
      `docs/opnsense-api-survey.md`.
- [ ] AC27 — A *Gaps found* section collects every `MISSING FROM MODEL:` marker
      into one table; the marker count in the body equals the row count.
- [ ] AC28 — Every entry displaying a byte, packet or connection volume states,
      in the UI copy it specifies, the observation-point limit: the application
      sees only what crosses the router, so the figure is a lower bound.
- [ ] AC29 — Every entry displaying a site name states in its UI copy that the
      name is inferred from resolver correlation and not observed, and at least
      one entry surfaces the per-device attribution rate, referencing
      `-- diagnostic: Attribution rate per device`.
- [ ] AC30 — Every **Empty state** distinguishes at least three conditions with
      different copy: the source is `unavailable`; the source is
      `present_but_disabled`; the source is reachable and returned no rows.
      Neither of the first two is an error screen or a displayed zero. Entries
      fed by Suricata additionally state which segments its coverage includes
      and which it does not.
- [ ] AC31 — Every entry resting on an OPNsense source names the section of
      `docs/opnsense-api-survey.md` that established it. An entry needing
      material the survey does not cover is marked `MISSING FROM MODEL:` and
      carries either an official OPNsense citation or `UNVERIFIED:` — never an
      asserted endpoint. No entry proposes reading a file on the firewall, SSH,
      any access other than the authenticated REST API, or `opnview` changing a
      setting on the firewall.
- [ ] AC32 — The document states that it replaces the seven-screen structure of
      step 7, declares itself a **first pass** and extendable, and contains no
      section organising widgets into a fixed screen list.

### `docs/dashboard-format.md`

- [ ] AC33 — The file contains, as `##` headings: what a dashboard file
      contains; JSON and YAML; references and portability; unresolved
      references; versioning and compatibility; worked example; open choices.
- [ ] AC34 — The content section enumerates every field — format version,
      canvases, widgets, placement, parameters — stating per field whether it is
      required and what it means.
- [ ] AC35 — The document states that JSON and YAML are both accepted over one
      model, and either names which an export produces or lists that under
      *Open choices* with each option's cost.
- [ ] AC36 — The reference model is stated concretely and **matches**
      *Conclusions for opnview* in `docs/ui-references.md`. The two documents
      make no contradictory statement about portability; the Verifier checks
      this by reading both.
- [ ] AC37 — The unresolved-reference section states that the widget is kept,
      rendered empty, and carries an explanation naming what is missing, and
      that neither an error nor a fabricated zero is acceptable.
- [ ] AC38 — The versioning section answers both directions: an older file
      meeting a newer `opnview`, and a newer file meeting an older one.
- [ ] AC39 — The worked example shows one canvas and at least two widgets, in
      **both** JSON and YAML, in fenced blocks, and the two are equivalent —
      same canvas, same widgets, same parameters. Every field the example uses
      is one the content section defines.
- [ ] AC40 — *Open choices* presents at least three genuinely open decisions
      with options and costs, and the document declares itself a draft for
      arbitration.

### Cross-cutting

- [ ] AC41 — No interface name, VLAN name, segment name, CIDR or IP address
      from any real network appears in any document. Illustrative addresses come
      from the documentation ranges and are labelled as examples, with a
      statement that such values are discovered at runtime and never hardcoded.
- [ ] AC42 — Each document restates the five settled product decisions or
      references the section that does, and none reopens or contradicts one.
- [ ] AC43 — No mockup, wireframe, rendered layout, HTML fragment, CSS rule
      intended for `opnview`'s own stylesheet, or final look-and-feel decision
      appears. Colour values in Family C are records of an external source,
      labelled as such.

### Judged by the maintainer, not by a command

Listed honestly rather than dressed up as criteria. The Verifier records them
as "for maintainer review" and **must not claim to have judged them**:

1. Whether the Family A findings are deep enough to be useful — whether the
   portability sections explain what breaks rather than restating that things
   break.
2. Whether the Family B criticisms are fair and specific, especially "where it
   buries a view".
3. Whether the catalogue covers the questions the maintainer actually has,
   beyond the ones he named.
4. Whether the proposed reference model is the right trade for this product.
5. Whether the ROADMAP amendment cuts deeply enough.
6. Whether the screenshots actually help judge the UX.

## Out of scope

- Any HTML, CSS, JavaScript, mockup, wireframe or rendered layout.
- Any Go code, package, HTTP handler, endpoint or `go.mod` change.
- Any SQL, migration, query or schema change — including the fixes the
  catalogue identifies: this cycle **names** gaps, a later cycle implements.
- Editing `ROADMAP.md`, `README.md`, `CLAUDE.md`, `docs/data-model.md`,
  `docs/architecture.md` or `docs/opnsense-api-survey.md`.
- Choosing the final look, typography, iconography or the accent colour, and
  naming `opnview`'s own semantic tokens.
- Deciding the widget rendering technology, charting library or frontend
  framework.
- Any live call against a real OPNsense instance, and any real network's
  addressing.
- Implementing import/export, validation, or a JSON Schema artifact.

## Risks

- **Closed products.** Datadog, UniFi Network, Firewalla and Zenarmor document
  unevenly. Expect many `UNVERIFIED:` markers in Family B — correct behaviour,
  not a defect, but it caps how strong the conclusions can be.
- **Documentation drift.** Grafana's and Home Assistant's dashboard formats
  have changed materially across versions; a claim without a version is
  ambiguous, so citations should name the version or the page date.
- **The industrial palette lives outside the repository**, under
  `C:\Users\fuzzz\Downloads\...`, outside the working tree and the container
  bind mount. If it moves, Family C cannot be re-checked — which is why AC12
  requires the properties reproduced verbatim rather than referenced.
- **Screenshots are the weakest artefact.** They are uncommitted, so they do
  not survive a clone; they age faster than the prose; and a public demo may
  change under the same URL. AC20 and AC21 bound the damage.
- **Scope inflation.** Fifteen products × seven fields is a great deal of
  prose. The risk is a long document that is thin per entry; AC9 defends the
  three that matter most, and nothing defends the rest but the maintainer's
  review.
- **The catalogue may outrun the model.** Mapping the questions onto the schema
  will probably reveal more than the two known gaps. Each is a future
  migration, and the cumulative weight may amount to a significant amendment of
  step 2's output — a finding, not a failure, but expect it.
- **A drawn conclusion is not a validated one.** The portability model will only
  be proven by an actual export and import across two installations, which does
  not exist yet.
- **Mechanical checks cannot judge substance.** Every criterion above verifies
  presence and shape. A document can satisfy all forty-three and still be
  shallow. The maintainer-review list is the only defence and must not be
  skipped.
