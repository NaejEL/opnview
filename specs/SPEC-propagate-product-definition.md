# SPEC — Propagate the settled product definition into the instructions and the agents

Status: APPROVED

> **Amended after delivery, 2026-09-23.** This spec framed `factory-ergonomist`
> as an agent that *verifies conformance to the written design references*, and
> its criteria describe the Proposals section as a secondary requirement. The
> maintainer has since corrected the role: the ergonomist **steers product
> decisions toward something usable, understandable and simple**, conformance is
> the floor beneath that judgement, and proposals are its main output. The agent
> definition in `.claude/agents/factory-ergonomist.md` is authoritative. Do not
> regenerate the agents from this spec without carrying that correction over.

## Context

The interface research cycle delivered `docs/ui-references.md`,
`docs/widget-catalogue.md` and `docs/dashboard-format.md`. The product is now
defined far more precisely than anything that acts on that definition:

- `ROADMAP.md` still specifies seven fixed screens (step 7), a UniFi aesthetic
  with rounded cards, a light default, "dark mode available, never the default",
  "soft area charts" (step 3), "HTTP API for the screens, period selector
  everywhere" (step 5) and "Go, single binary" (step 4).
- `README.md` and `CLAUDE.md` both carry the unjustified "Single Go binary"
  rule.
- `factory-planner.md` and `factory-builder.md` restate the superseded
  aesthetic as a project constraint. `factory-builder.md` is backend-shaped
  throughout.
- `factory-verifier.md` verifies mechanically, has no browser, and nothing in
  its definition forbids it asserting how a rendered page looks — a rule it had
  to be taught by an orchestrator prompt after it did exactly that.
- `sql/schema-checks.sh` fails, because its provider-name grep scans all of
  `docs/` and `docs/ui-references.md` legitimately *names* ntopng and Zenarmor
  as surveyed products.

A first mockup was rejected once because the design brief was one line. A
specification that does not reach the agents executing it gets re-litigated
every cycle. This cycle closes that gap and produces **no product deliverable**.

## Decisions taken for this cycle

The maintainer delegated the lead through to the mockup. These are the answers
to the Planner's thirteen open questions; the ones that were his to take and
that he had already taken are marked as such.

1. **Names**: `factory-ui-builder` and `factory-ergonomist`; the YAML `name:`
   equals the filename stem, as for the three existing agents. No aliases.
2. **When the ergonomist runs**: the **spec declares the cycle's kind**, in a
   field the Planner fills. Mechanical, and it keeps the skill from becoming a
   decision tree.
3. **`factory-builder` and interface work**: it **defers** — it delivers the
   non-interface part and reports the interface part as undelivered, naming
   `factory-ui-builder`. Refusing a 95%-backend spec outright would waste a
   cycle.
4. **Agent selection in the skill**: the skill **reads the spec's declared
   kind**. One rule, no branching flow.
5. **The preference table lives in exactly one place** — the maintainer's
   decision, taken before he left: in `docs/ui-references.md`, and nowhere
   else. The agent definitions **name the document and its section, state that
   reading it is mandatory and that it is binding — and do not reproduce it.**
   Fewer documents, less maintenance, no divergence.
6. **The ergonomist's weight**: it returns a strict JSON verdict like
   `factory-verifier`. Its `critical` and `major` findings **re-enter the same
   three-iteration correction loop**, sharing the same counter. `minor`
   findings and all proposals are relayed to the maintainer and block nothing.
   **Both verifiers must pass for a cycle to be approved**; neither takes
   precedence, so a disagreement cannot deadlock — a blocking finding from
   either sends the cycle round again.
7. **No reference images on a clone**: the ergonomist **degrades to the prose
   descriptions and the image index in `docs/ui-references.md`, and records in
   its report that it compared against text rather than images.** It never
   silently skips the check and never refuses to run.
8. **Browser tools**: the definition **states the capability required** — render
   a local page, capture it, inspect the DOM and computed styles — **and names
   the current tool set as current**, so the definition survives the tools
   changing.
9. **Amendment item 6's three smaller edits are applied**: step 8's README
   screenshot list becomes representative canvases with their dashboard files
   committed as examples; step 4's setup wizard gains the theme; step 2's
   *Outcome* gains a sentence recording the ten model gaps.
10. **The soft-area-charts wobble is resolved** as the research document's item
    4 states: step 3 keeps generous spacing, a single accent colour and numbers
    brought forward; it **drops soft area charts**; area fills survive only as a
    means of separating stacked series, with the line drawn through real points.
11. **Restatements**: the aesthetic bullets in `factory-planner.md` and
    `factory-builder.md` are **replaced by a pointer** to the binding documents
    and the amended ROADMAP, so there is one source of truth. The rest of their
    constraint lists gets minimal surgery.
12. **Step 3's subject changes**: with canvases replacing screens, step 3
    becomes a mockup of a **representative canvas**, not of an "overview
    screen". Its deliverable cell changes; its number and position do not.
13. **`specs/` is named in the new comment** as deliberately out of the grep's
    scope, for the same reason as the research documents.

## Scope

No Go, no HTML, no CSS, no SQL, no migration, no schema change. Ten files: two
created, eight modified.

### Created

**`.claude/agents/factory-ui-builder.md`** — a Builder for interface work,
matching the structure, tone and front-matter conventions of the three existing
agents. It carries: the two binding documents and the rule that contradicting
one without saying so is a defect; angular cards with Homarr's layout as the
structural target and its rounding excepted; the industrial palette as default,
the five named palettes as options, the theme following the OS on first launch
with a persisting override; no resource from any host, ever; never promise what
the data model cannot produce; never claim to have judged appearance; a
**Proposals** section in every report; and the non-frontend project constraints
that bind every Builder.

**`.claude/agents/factory-ergonomist.md`** — a second verifier for interface
cycles, run in addition to `factory-verifier`, never instead of it. Its subject
is conformance to the written design references. It has browser tools and must
use them. It does not deliver the final aesthetic verdict.

### Modified

`.claude/agents/factory-planner.md`, `.claude/agents/factory-builder.md`,
`.claude/agents/factory-verifier.md`, `.claude/skills/factory-run/SKILL.md`,
`ROADMAP.md`, `README.md`, `CLAUDE.md`, `sql/schema-checks.sh`.

## Acceptance criteria

### Change set and project commands

- [ ] AC1 — `git status --porcelain` shows modifications to exactly:
      `.claude/agents/factory-planner.md`, `.claude/agents/factory-builder.md`,
      `.claude/agents/factory-verifier.md`,
      `.claude/skills/factory-run/SKILL.md`, `ROADMAP.md`, `README.md`,
      `CLAUDE.md`, `sql/schema-checks.sh`; plus two new files,
      `.claude/agents/factory-ui-builder.md` and
      `.claude/agents/factory-ergonomist.md`; plus this spec. The three research
      documents and `specs/SPEC-ui-and-dashboard-format-research.md` are
      byte-identical to their pre-cycle content.
- [ ] AC2 — No file with extension `.go`, `.sql`, `.html`, `.css`, `.js`,
      `.ts`, `.tmpl` or any image extension is created, modified or deleted,
      and `migrations/` is untouched. `sql/schema-checks.sh` is the single
      permitted exception and only for the change AC42–AC45 describe.
- [ ] AC3 — `docker compose run --rm checks` exits 0 and
      `docker compose run --rm schema-checks` exits 0, both from the repository
      root, with their output recorded in the Builder's report.
- [ ] AC4 — Every file created or modified is written in English throughout.

### `.claude/agents/factory-ui-builder.md`

- [ ] AC5 — The file exists, opens with YAML front matter whose `name` is
      `factory-ui-builder` with a `description`, and contains as `##` sections
      at minimum: an absolute language rule, the binding references, the project
      constraints, a required sequence, a *Proposals* requirement and a
      *Forbidden* section.
- [ ] AC6 — It names `docs/ui-references.md` and `docs/widget-catalogue.md`
      with the word **binding**, and states that a design decision contradicting
      one of them without saying so is a defect rather than a variation.
- [ ] AC7 — It names, by its exact heading, the section of
      `docs/ui-references.md` that holds the maintainer's recorded preferences,
      and states that reading that section before designing is mandatory. **It
      does not reproduce the table**: a copy of the preference rows in the agent
      file fails this criterion, because the maintainer requires exactly one
      table.
- [ ] AC8 — It states that cards are angular, and that Homarr's layout is the
      structural target while its rounding is explicitly excepted.
- [ ] AC9 — It states that the industrial palette is the default, the five named
      palettes are options, the theme follows the operating system on first
      launch, and the user override persists.
- [ ] AC10 — It states that no resource loads from any host, ever, and that
      inline is acceptable while external is not.
- [ ] AC11 — It carries the rule "never promise what the data model cannot
      produce", naming `docs/data-model.md`, `migrations/` and the *Gaps found*
      table of `docs/widget-catalogue.md` as ground truth.
- [ ] AC12 — It states the agent must never claim to have judged appearance: it
      may state what it built and what it intended; it may not state how the
      result looks.
- [ ] AC13 — It requires a dedicated **Proposals** section in every report,
      distinct from findings, and states that an out-of-scope idea is reported
      and never implemented, that the agent never acts on its own proposal, and
      that silence is not approval.

### `.claude/agents/factory-ergonomist.md`

- [ ] AC14 — The file exists with YAML front matter whose `name` is
      `factory-ergonomist` and whose `description` says it verifies conformance
      to the written design references.
- [ ] AC15 — It states it runs **in addition to** `factory-verifier` on
      interface cycles and never instead of it, and that its subject is
      conformance to written references rather than exit codes.
- [ ] AC16 — It states the browser capability it requires — render a local page,
      capture it, inspect the DOM and computed styles — names the current tool
      set as the current one, and names the four actions: open the artefact,
      render it, capture it, compare against `docs/ui-references/screenshots/`
      and the preference section.
- [ ] AC17 — Its checklist contains all five items: (a) charts granular rather
      than smoothed, cards angular, palette correct, theme following the OS on
      first open; (b) each widget against its catalogue entry's six fields;
      (c) nothing buried — a view reachable only through a menu inside a menu is
      a finding; (d) degraded and empty states present, explanatory, and
      distinguishable from an error or a zero; (e) nothing displayed that the
      model cannot produce.
- [ ] AC18 — It states it must not claim the final aesthetic verdict, that the
      verdict is the maintainer's, and that its report says what conforms and
      what does not and stops there.
- [ ] AC19 — It requires a **Proposals** section, states the ergonomist must not
      restrict itself to conformance, and states a proposal is not a finding: it
      does not fail a cycle, does not belong in the issues list, carries no
      verdict.
- [ ] AC20 — It states it modifies no file, and that an out-of-scope idea is
      reported and never implemented.
- [ ] AC21 — It states what it does when the reference images are absent — the
      screenshots are gitignored and do not survive a clone: it degrades to the
      prose descriptions and the image index in `docs/ui-references.md` and
      **records in its report that it compared against text rather than
      images**. It neither silently skips the check nor refuses to run.
- [ ] AC22 — It returns a strict JSON verdict in the same shape as
      `factory-verifier`, and states that its `critical` and `major` findings
      re-enter the correction loop while `minor` findings and proposals do not
      block.

### The three existing agents

- [ ] AC23 — `factory-planner.md` names all three research documents and states
      that specs are written against them.
- [ ] AC24 — `factory-planner.md` states that every spec declares the cycle's
      kind, and that an interface cycle uses `factory-ui-builder` and both
      verifiers. Both new agents are named.
- [ ] AC25 — `factory-builder.md` names `factory-ui-builder` and states that on
      interface work it **defers**: it delivers the non-interface part and
      reports the interface part as undelivered rather than attempting it.
- [ ] AC26 — `factory-builder.md` carries the "never promise what the data model
      cannot produce" rule, naming `docs/data-model.md` and `migrations/`,
      phrased so it applies beyond the frontend.
- [ ] AC27 — `factory-verifier.md` states, as a standing rule rather than an
      incident note, that an agent with no browser never asserts how a rendered
      page appears, and that such an assertion is outside its competence.
- [ ] AC28 — A case-insensitive grep over `.claude/` for `rounded card`,
      `soft area chart`, `never the default`, `light background by default`,
      `single binary` and `single Go binary` returns no line asserting them as
      the product's rule. Any survivor is an explicitly labelled citation of a
      superseded decision.
- [ ] AC29 — The aesthetic bullets of `factory-planner.md` and
      `factory-builder.md` are replaced by a pointer to the binding documents
      and the amended ROADMAP rather than by a new restatement.

### `.claude/skills/factory-run/SKILL.md`

- [ ] AC30 — The skill states that a spec declares its cycle kind, that an
      interface cycle uses `factory-ui-builder` in the build phase and runs both
      `factory-verifier` and `factory-ergonomist` in the verify phase, and that
      every other cycle keeps the current two. Both new agent names appear.
- [ ] AC31 — The skill states that both verifiers must approve, that blocking
      findings from either re-enter the same three-iteration loop sharing one
      counter, and that neither verifier takes precedence over the other.
- [ ] AC32 — The skill states that proposals from any agent are relayed to the
      maintainer and never acted on by the orchestrator without his decision.
- [ ] AC33 — The mandatory human gate on the spec and the headless refusal
      remain present and unweakened, as does "never approve out of exhaustion".

### The ROADMAP amendment

- [ ] AC34 — The step table still has eight rows, numbered 1–8 in the same
      order with the same step names. Only the *Deliverable* cells of steps 3
      and 7 change.
- [ ] AC35 — "Single Go binary" / "Go, single binary" appears nowhere in
      `README.md`, `CLAUDE.md` or `ROADMAP.md` as a rule; all three carry the
      replacement — one service, installed and updated by a single command, no
      external data store to provision, storage engines are embedded libraries
      rather than servers. At least one records the reason and the permitted
      list (SQLite, DuckDB, a pure-Go time-series library) and the forbidden
      list (PostgreSQL, TimescaleDB, InfluxDB, VictoriaMetrics); the other two
      point at it.
- [ ] AC36 — `ROADMAP.md` step 7 no longer contains the seven-screen numbered
      list nor the phrase "the seven screens", and instead specifies named
      canvases composed from widgets with tabs rather than menus, referencing
      `docs/widget-catalogue.md` and `docs/dashboard-format.md`.
- [ ] AC37 — Step 7 states that the seven queries in `sql/queries/screens.sql`
      remain authoritative and are not deleted, and that file is unmodified.
- [ ] AC38 — `ROADMAP.md` no longer contains "Dark mode available, never the
      default" or "light background by default"; step 3 states the theme follows
      the operating system on first launch, the override persists, the
      industrial palette is the default and the five named palettes are options.
- [ ] AC39 — Step 3 no longer names UniFi Network as the aesthetic reference nor
      specifies rounded cards, and instead specifies the angular language: the
      industrial palette's `0.25rem` radius and system fonts, Cloudflare Radar
      as the compositional reference, Homarr as the structural reference with
      its rounding excepted. The three original bans — "cyber-defence"
      aesthetics, walls of dense tables, empty panels with no explanation —
      survive in substance.
- [ ] AC40 — "soft area chart" appears nowhere in `ROADMAP.md`, `README.md` or
      `CLAUDE.md`. Step 3 instead states that charts show the resolution the
      data actually has: no curve smoothing, no silent downsampling that can
      hide a single-sample spike, and where a series is aggregated to fit the
      pixels the widget says so. Generous spacing, a single accent colour and
      numbers brought forward survive; area fills survive only to separate
      stacked series, with the line through real points.
- [ ] AC41 — Step 3's subject is a representative **canvas**, not an "overview
      screen"; its deliverable cell reflects that.
- [ ] AC42 — Step 5 no longer reads "HTTP API for the screens, period selector
      everywhere" and instead specifies one endpoint per widget type taking that
      widget's declared parameters from `docs/widget-catalogue.md`, returning
      both the data and the source-availability state, with the period a widget
      parameter.
- [ ] AC43 — The three smaller amendment edits are applied: step 8's README
      screenshot list becomes representative canvases with their dashboard files
      committed as examples; step 4's setup wizard gains the theme; step 2's
      *Outcome* gains a sentence recording the ten model gaps.
- [ ] AC44 — `CLAUDE.md` names all three binding documents, states the interface
      is named canvases composed from widgets with tabs rather than menus, and
      states the palette and theme rules.
- [ ] AC45 — Every amendment item is applied in both directions: for each, the
      superseded string is absent from the file it was in and the replacement is
      present. A file-and-text mapping appears in the Builder's report. The
      Builder matches on text, never on line numbers.
- [ ] AC46 — No section of `ROADMAP.md`'s *Superseded decisions* is deleted, and
      the amendment is recorded there so the replaced rules do not resurface.

### `sql/schema-checks.sh`

- [ ] AC47 — The provider-name grep scans exactly `migrations/`, `sql/`,
      `docs/data-model.md` and `docs/architecture.md`, and no longer scans
      `docs/` as a directory.
- [ ] AC48 — The forbidden-name list is unchanged and the script's exclusion of
      itself from its own grep is retained.
- [ ] AC49 — A comment immediately above the check records why the scope is what
      it is — the property is a property of the *model*; research documents and
      `specs/` legitimately name surveyed products; widening the scope back
      would make the check assert a model property by grepping prose.
- [ ] AC50 — `git diff sql/schema-checks.sh` removes no call to `check`,
      `check_ge`, `pass` or `fail`, and changes no assertion other than this
      one's path list and its comment. The `sqlite_master` provider-name query
      is byte-identical.
- [ ] AC51 — `docker compose run --rm schema-checks` exits 0, and its
      `checks passed :` count is greater than or equal to the count from the
      last commit where it exited 0. The Builder records both numbers.
- [ ] AC52 — The narrowed check still bites: the Builder demonstrates that
      temporarily inserting a forbidden name into `docs/data-model.md` makes the
      check fail, records the output, reverts the edit, and shows
      `docs/data-model.md` unmodified at hand-back.

### Judged by the maintainer, not by a command

Recorded as "for maintainer review"; neither verifier may claim to have judged
them.

1. Whether the two new agent definitions are operable — whether an agent
   reading only that file could do the job without asking.
2. Whether pointing at the preference section, rather than reproducing it, is
   enough to make an agent actually read it.
3. Whether the ergonomist's boundary is the right one in practice.
4. Whether the amended steps 3, 5 and 7 read as one coherent product rather
   than an old text with patches applied.
5. Whether the new agents' tone and length match the existing three.
6. Whether the skill's expression of agent selection is workable.

## Out of scope

- Any product deliverable: no mockup, no HTML, CSS or JavaScript, no Go, no SQL
  migration, no schema change, no widget implementation.
- Closing any of the ten model gaps. G9 and G10 are reserved for the telemetry
  research cycle scheduled after the mockup.
- Rewriting, correcting or extending the three research documents, or amending
  `specs/SPEC-ui-and-dashboard-format-research.md`.
- Editing `docs/data-model.md`, `docs/architecture.md` or
  `docs/opnsense-api-survey.md`.
- Choosing a charting library, frontend framework, widget-rendering technology
  or storage engine. The storage rule is replaced; no engine is adopted.
- Resolving `docs/dashboard-format.md`'s open choices.
- Any call against a real OPNsense instance.

## Risks

- **Prompt quality is largely unassertable.** The criteria check that the
  required statements are present, not that the resulting agent behaves well.
  The maintainer-review list is the only defence and it is larger here than in
  previous cycles.
- **The single-table decision has a known cost.** An agent pointed at a section
  4539 lines into a document may not read it. The mitigation is that the
  ergonomist checks conformance against that same table, so a builder that
  skipped it gets caught. The maintainer takes the trade deliberately: fewer
  documents, no divergence.
- **The ergonomist is written now and proven later.** There is no frontend to
  point it at, and its value rests on browser tooling the environment may or may
  not expose.
- **Two verifiers can disagree.** The rule adopted — both must pass, blocking
  findings from either feed one shared counter — removes deadlock but makes an
  interface cycle harder to get through in three iterations.
- **Scope creep into the product.** Four of the ten files describe a frontend
  that does not exist. The temptation to sketch it while describing it is real,
  and AC2 is the only defence.
- **`docs/ui-references.md` contradicts itself on soft area charts** — item 3
  retains the clause, item 4 removes it. Decision 10 resolves it; the Builder
  must not transcribe the replacement paragraph verbatim without applying that
  resolution.
