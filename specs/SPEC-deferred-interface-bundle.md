# SPEC — Deferred interface bundle: interface networks and the link-local rule, collection settings, the Public Suffix List state, and visible source outages

Status: APPROVED
Cycle kind: interface

## Context

The code base is at `a1e2707` on `main`. Four backend cycles built storage, validation and write paths and sent their screens here: step 4, 5A, the 5A live corrections, and the resolver-cache cycle (`specs/SPEC-resolver-cache-attribution.md`, `-closing`, `-follow-ups`). A parallel standard cycle, `specs/SPEC-step-5b-widget-api.md`, builds the widget HTTP API and backend fixes at the same time.

### What exists today

**The web surface (`internal/web/`).**
- The route table is in `web.go`. Every row declares an access level, and a test enumerates the rows. The routes are `/setup`, `/sign-in`, `/sign-out`, `/settings`, `/collection`, `/recover` and `/asset/style.css`.
- Templates live under `template/`. `layout.gohtml` draws a fixed top bar with one link per signed-in surface, today *Settings* and *Collection*. Tabs over named canvases belong to step 7.
- There is one catalogue, `locale/en.json`. Handlers choose a `messageKey`, and templates resolve it. Tests strip tags and actions and fail on any bare text; a missing key fails the render.
- There are three themes: `system` (no `data-theme` attribute), `light` and `dark`. The five named palettes are step 7.

**The collection surface (`collection.go`, `collection.gohtml`).**
- There is one card per kind that `internal/sizing.Kinds()` registers and the `provider` table holds: today `firewall_log`, `security_event`, `dhcp_lease` and `dns_lookup`.
- Each card shows the pair in force, the measurement, the gaps, the suggestion, and a per-provider selection (`auto` / `on` / `off`).
- A save writes `setting` rows, reloads them through `config.Load`, and hands them to the live holder and to `Collector.Configure`. No restart is needed.
- Refusals are drawn against the field, with status 400.

**The settings surface (`settings.go`, `settings.gohtml`).**
- It has three cards: OPNsense, Geolocation and Appearance.
- Geolocation shows the MaxMind state: the licence-key state, `geoIPState` from the `geo_asn` probe, and the build in use from `GeoIP.Ready()`.

**Interface networks and the link-local rule.** The backend is done; there is no screen.
- `internal/store/network.go` has `InterfaceNetworks`, `AddInterfaceNetwork`, `ConfirmInterfaceNetwork`, `RemoveInterfaceNetwork`, `SetLinkLocalEvidence` and `ParseNetwork`.
- The refusals are named: `ErrMalformedNetwork`, `ErrLinkLocalNetwork`, `ErrUnknownInterface`, `ErrUpstreamInterface`, `ErrUnknownNetwork`. Overlaps are reported (`NetworkEdit.Overlaps`), not refused.
- Decision D1 (`specs/SPEC-step-5a-corrections.md`):
  - detected networks count until the operator overrides them;
  - an operator network always wins;
  - a removed network no longer counts.
- Every edit changes `OnLinkFingerprint`. The collector re-places addresses at its next derivation.
- **Terms.** The term is OPNsense's **network**. `interface_network.origin` (`detected` / `operator`) and `interface.link_local_evidence` (default no) are opnview's own.
- No store function lists `interface` rows for display.

**Settings without a screen.** All of them are in `internal/config/config.go`, validated by `config.Load`:
- `poll_interval_resolver_cache_seconds` (60 s) and `poll_interval_resolver_local_data_seconds` (300 s);
- `attribution_max_cache_answer_delay_seconds` (3600 s) and `attribution_max_delay_seconds` (5 s);
- `leg_pairing_window_seconds` and `lease_backend_failure_pass_limit` (3);
- `retention_seconds`, `aggregate_mode` and `refresh_interval_public_suffix_list_seconds`.

`resolver_cache` and `resolver_local_data` are not `sizing` kinds, so the existing card loop cannot draw them as it stands.

**The Public Suffix List.** The backend is done; there is no screen.
- `internal/publicsuffix/refresh.go` writes the `public_suffix` availability row. Its probe is one of `list_downloaded`, `list_not_modified`, `download_failed`, `download_malformed`, or `not_yet_probed`.
- The state is `reachable` exactly when a parsed copy is held.
- A failed refresh records a `collection_gap` with reason `download_failed`. `en.json` has no `gap_reason.download_failed` key.

**Source availability.**
- `source_availability` holds one row per `provider`, with state `reachable` / `present_but_disabled` / `unavailable` and a probe.
- `detail` is free English text written by the collectors. The 401 / 403 distinction and the named absences exist only there today. The parallel 5B cycle adds a closed outcome code (its Group D1), and this cycle does not depend on it.
- `docs/widget-catalogue.md`, *Source availability*, fixes the binding and the condition rules.
- Today an outage is visible only inside a kind's collection card. Most kinds have no card.
- Nothing records an interval during which opnview itself was not running. The parallel 5B cycle adds that as a gap reason of its own (its Group D2).

### Why

opnview runs on a laptop, so collection gaps and source outages happen routinely, and today they are buried. The operator also has no way to set an interface's networks, the link-local rule, or the settings listed above, though classification and attribution depend on them.

## Scope

All work applies to the working tree on top of `a1e2707`. It is server-rendered HTML through the existing renderer, catalogue and stylesheet.

- **No widget engine, canvas or client-side script** is introduced; those are step 7.
- **No schema change** is made here.
- Read paths this cycle needs are new read-only `internal/store` functions in new files.

### 1. Interface networks, set by the operator, with the detected ones offered

A new authenticated surface, **Interfaces** (`PathInterfaces`), is a link on the top bar.

**The cards.**
- One card per `interface` row, in a stable order that interprets no name.
- Each card shows the OPNsense `description`, `identifier` and `device`, all as discovered data, and whether the interface is upstream.
- **An upstream interface** shows that state and no network or link-local control.

**On every other interface:**
- **Every stored network**, of both families, is listed with:
  - its origin (*detected* / *set by the operator*);
  - whether it is removed;
  - whether it **counts now** (`InterfaceNetwork.Counts`, D1).
- **Confirm.** Each detected network the operator has not confirmed carries a confirm control (`ConfirmInterfaceNetwork`), one network at a time. This is the "automatically detected range offered as a suggestion" of the maintainer's requirement. Detected networks keep counting until overridden, and the surface must not suggest otherwise.
- **Add.** A field adds a network the operator types (`AddInterfaceNetwork`). Refusals:
  - are drawn against the field, with the typed text kept;
  - answer 400 and store nothing;
  - have their own catalogue wording each: malformed, overlaps the IPv6 link-local prefix, unknown interface, upstream interface.
- **Overlaps** with another interface's counting network are reported after the save, naming the other interface and network.
- **Remove.** A remove control (`RemoveInterfaceNetwork`). A removed network stays listed with a restore control (`ConfirmInterfaceNetwork`).
- **The link-local rule.** One control (`SetLinkLocalEvidence`) shows the stored value, default *no*. Its labels use the vocabulary of `docs/data-model.md` (link-local per RFC 4291, evidence of membership).
- **No stored network** gives a named state, not an empty list.

**Effect.** Edits take effect at the collector's next derivation, and the surface does not claim the reclassification already happened.

**New read path.** A read-only function listing interfaces with `id`, `identifier`, `device`, `description`, `is_upstream` and `link_local_evidence`.

### 2. The settings on the collection surface

**Cards for `resolver_cache` and `resolver_local_data`.**
- Each card is drawn when, and only when, the `provider` table holds that kind.
- It shows the interval in force (the default when no row exists), each provider's availability state and selection (`config.KeySourceSelection`), and the interval field, validated exactly as `typedIntervalSeconds` validates.
- It has no measurement, suggestion or fill control.

**An *Attribution* card** holds:
- `attribution_max_delay_seconds` (the timing window, default 5 s);
- `attribution_max_cache_answer_delay_seconds` (the exact-answer cap, default 3600 s).

Each shows its value in force and is refused unless `config.Load` accepts it.

**The remaining settings** are each on a card where they belong, with the value in force and validation identical to `config.Load`. The Builder places them and justifies the placement:
- `leg_pairing_window_seconds`, with the 60 s upper bound once the 5B cycle lands it; until then, `config.Load`'s current rule;
- `lease_backend_failure_pass_limit`, on or near the `dhcp_lease` card;
- `retention_seconds`;
- `aggregate_mode`;
- `refresh_interval_public_suffix_list_seconds`, on the Public Suffix List card.

**The `aggregate_mode` control** states its consequence in a word or two, not explanatory copy. It is not destructive: it changes what is stored from then on. The control must not imply it rewrites history.

**Every save** goes through `SetSetting`, then `applyConfiguration`, then `Collector.Reselect` for a selection change, with no restart. A changed setting is not retroactive, and the surface does not imply that it is.

### 3. The Public Suffix List state

A card on the settings surface, beside Geolocation, in the same form as the MaxMind state.
- **The last refresh outcome**, mapped from the probe to one catalogue key per value. An unknown probe reads as the failure state.
- **Whether a copy is in use, when it was last confirmed current, and its version and rule count as figures.**
  - These come from the refresher in process through an interface on `web.Options`, like `GeoIP`.
  - They are never parsed out of `detail`.
- **A failed refresh over a copy still in use** shows both facts.
- **Drawing the card contacts nothing.**

### 4. Source outages visible at a glance

**A new authenticated surface, Sources (`PathSources`)**, is a link on the top bar. It is the server-rendered counterpart of the *Source availability* preset and uses its binding and condition rules, so the step-7 widget can reuse the keys and the read.
- **Columns, one row per `provider` row:** kind, provider, availability, `is_active`, selection, checked instant, probe, and detail.
  - **Detail** is rendered **verbatim as data**, escaped, in a diagnostic column, and declared untranslated. It is replaced as the at-a-glance reading by the 5B outcome codes once they land.
- **Availability renders as the preset's values:**
  - *not yet probed* (`unavailable` + `not_yet_probed`);
  - *installed, not collecting* (`present_but_disabled`);
  - *healthy* (`reachable`);
  - *unavailable*;
  - *state unknown* when the availability row is missing, never healthy.
- **The collection cards** move onto the same catalogue keys.

**Recent gaps.** Every `collection_gap` row detected in the last 24 hours, newest first, with:
- provider;
- reason, as a catalogue key; every reason the schema allows has a key, including `download_failed`, and the "opnview not running" reason once the 5B cycle adds it;
- interval start and end;
- missed seconds;
- detail.

The read uses an index.

**A top-bar indicator** on every signed-in page shows the number of current outages and links to Sources. Zero renders as its own named state. The outage rule, per kind:
- a kind is in outage when at least one of its providers has a selection other than `off` and none of those is `reachable`;
- plus any provider whose selection is `on` and that is not `reachable`;
- rows still `not_yet_probed` are excluded.

**Labels.** Every registered kind and every `(kind, provider_key)` has a catalogue label.

**New read path.** A read-only function returning what `-- diagnostic: Source availability` returns, joined with the selection rows, plus the recent-gap read.

### Files expected

- **New:**
  - templates for Interfaces and Sources;
  - `interfaces.go`, `sources.go`;
  - read-only store files.
- **Amended:**
  - `web.go`: routes, paths, `Options` for the Public Suffix List state;
  - `view.go`;
  - `layout.gohtml`: links and the indicator;
  - `collection.go` / `.gohtml`;
  - `settings.go` / `.gohtml`;
  - `messages.go`;
  - `locale/en.json`;
  - `asset/style.css`;
  - `cmd/opnview` wiring;
  - tests.
- **Documents:** `docs/data-model.md` names the surfaces where networks, the link-local rule and each setting are set.

## Acceptance criteria

**Applies to every criterion:**
- every criterion runs through `docker compose run --rm checks`, whose `go test` wall time stays **under 60 s** (report the CPU count);
- `docker compose run --rm pre-deployment` exits 0;
- fixtures are synthesised: RFC 2606 / 6761 names, RFC 5737 / 3849 addresses, interface counts as parameters, both IP versions, and a provenance statement;
- no test is weakened, skipped or moved.

### Interfaces and networks

- [ ] AC1 — The route is in the route table as authenticated, and the enumeration test covers it. Without a session it redirects to sign-in. Every signed-in page's top bar links to it.
- [ ] AC2 — Fixture: one upstream interface and N non-upstream interfaces (N at 1, and at 3 or more).
  - There is exactly one card per `interface` row.
  - The upstream card has no network or link-local control.
  - Each non-upstream card lists every `interface_network` row of both families, with its origin, its removed flag and `Counts`, as `InterfaceNetworks` returns them.
- [ ] AC3 — Confirm, remove and restore through the page.
  - Confirming makes `InterfaceNetworks` report the network as `operator` and counting.
  - Removing makes it removed and not counting, and it stays listed with a restore control.
  - Restoring makes it count again.
  - Each edit changes `OnLinkFingerprint`.
- [ ] AC4 — Adding a valid range of each family stores it as `operator`, counting. Each refusal below:
  - answers 400 and stores nothing;
  - keeps the typed text;
  - marks the field `aria-invalid`, with `aria-describedby` pointing at a refusal that has its own catalogue key.

  The refusals: malformed, host bits set, IPv4-mapped, overlapping the IPv6 link-local prefix, an upstream interface, an unknown interface.
- [ ] AC5 — A range overlapping another interface's counting network is stored, and the card names each overlap and its interface.
- [ ] AC6 — The link-local control shows *no* on a fresh interface. Setting it changes `OnLinkFingerprint` and stores `link_local_evidence` 1 on that interface only.
- [ ] AC7 — A non-upstream interface with no network renders a named catalogue state.
- [ ] AC8 — Every new POST without a valid CSRF token is refused by the existing middleware, with a test per route.

### Collection surface and settings

- [ ] AC9 — The `resolver_cache` and `resolver_local_data` cards are drawn exactly when the `provider` table holds that kind, and are absent when a test removes the rows. With no setting row they show 60 and 300.
- [ ] AC10 — Saving an interval writes its key. `config.Load` returns it, the live holder's generation increments, and `Configure` receives it, with no restart. A non-integer, 0, a negative number, or a value above `config.MaxIntervalSeconds` is answered 400 with nothing stored.
- [ ] AC11 — Every setting in scope 2 is shown with its value in force: the default when no row exists, the stored value otherwise.
  - Saving a valid value writes its key, and `config.Load` returns it.
  - Invalid values are refused 400 with nothing stored.
  - A table-driven test asserts, at the boundaries, that the form accepts exactly what `config.Load` accepts.
- [ ] AC12 — A selection saved on a resolver card writes `config.KeySourceSelection(kind, key)` and calls `Reselect` for that kind.
- [ ] AC13 — The resolver cards render no measurement group, suggestion group or fill control.

### Public Suffix List

- [ ] AC14 — The settings page carries the card. For each of the five probe values, and for an unknown one, a test asserts the catalogue key rendered. The unknown value renders the failure key.
- [ ] AC15 — With a copy in use, the version and rule count render as figures from the in-process interface.
  - A grep shows no code in `internal/web` reading `detail` to obtain them.
  - A failed refresh over a held copy renders both states.
- [ ] AC16 — Drawing the settings page and the two new surfaces issues no HTTP request. A counting transport asserts it, firewall and outbound alike.

### Sources and outages

- [ ] AC17 — Sources draws exactly one row per `provider` row, asserted against `SELECT count(*) FROM provider` on a fresh schema.
  - Every row carries its kind label, provider label, availability, activeness, selection, checked instant, probe and detail.
  - The checked instant is RFC 3339 UTC, or a named state when the row was never checked.
- [ ] AC18 — Each availability condition renders its own key. No condition renders as healthy except `reachable`. The collection cards render the same keys for the same states.
- [ ] AC19 — A 401 detail and a 403 detail on the same provider render distinguishable rows, through the verbatim detail column.
- [ ] AC20 — Every `collection_gap` row in the last 24 h renders with its reason key, and every reason the schema allows has a key. A row outside the window does not render. `EXPLAIN QUERY PLAN` shows an index search and no `SCAN collection_gap`.
- [ ] AC21 — The indicator appears on every signed-in page.
  - On a fixture, it shows the count the outage rule computes, recomputed by an independent test query.
  - It has a distinct named state at zero and links to Sources.
  - A `not_yet_probed` provider never counts.
- [ ] AC22 — Every `kind` and every `(kind, provider_key)` of a fresh schema has a catalogue label, so a provider added later without a label fails the test.

### Cross-cutting

- [ ] AC23 — The catalogue tests pass over every new and amended template, and no handler returns a user-visible literal.
- [ ] AC24 — No new template or stylesheet rule loads anything off-host, and no `<script>` is added. A test asserts it.
- [ ] AC25 — New stylesheet rules use only the industrial palette's custom properties, with no new colour literal, and cards use the existing radius token.
- [ ] AC26 — No interface name, VLAN name, address, CIDR or interface count appears as a literal in any product file.
- [ ] AC27 — `docs/data-model.md` names the surfaces where networks, the link-local rule and each setting are set.

## Rules

- **Language and vocabulary.** Everything is written in English.
  - The terms are OPNsense's: *interface*, *network*, *This Firewall*, *cache*, *local data*.
  - opnview's own terms are as `docs/data-model.md` *Vocabulary* records them. A new one gets its row, and the count sentence is updated.
- **No user-visible string is a literal.** Everything comes from `locale/en.json`. The verbatim `detail` column is the one declared exception: it is data.
- **Modularity.** Kinds and providers are read from the `provider` table at runtime. No template or handler enumerates them.
- **Tabs, not nested menus.** Every surface is one top-bar link, and the bar stays visible.
- **Theme and palette.** The industrial palette's tokens only.
- **No explanatory copy.** A state is named in a word or a short phrase.
- **Read-only against the firewall.**
  - No control changes a firewall setting.
  - Drawing a page contacts nothing.
  - The three outbound calls are unchanged.
- **Binding documents.** `docs/ui-references.md` (with *The maintainer's recorded preferences*), `docs/widget-catalogue.md`, `docs/dashboard-format.md`, `ROADMAP.md` steps 3 and 7, and the step-3 mockup `docs/mockups/canvas-mockup.html` are binding. Any departure is declared in the report. Known declarations:
  - the *Configuration interface* preference is open;
  - the detail column is rendered as data;
  - the top bar keeps links.
- **The test budget.** The everyday suite stays under 60 s.
- **The Ergonomist's review instance never touches the live service or its data.**
  - A live service runs on port 8080 from the `data` volume, and `docker-compose.serve.yml` pins `--data-dir /data`, so it **must not** be used.
  - The review runs from `dev` on another port with a scratch data directory inside the container, for example `docker compose run --rm -p 8081:8081 dev go run ./cmd/opnview --data-dir /tmp/opnview-review --listen 0.0.0.0:8081`. The orchestrator confirms `-p` works first.
  - The review loads a purpose-built synthesised fixture covering every state: every availability condition, upstream and non-upstream interfaces, removed and overlapping networks, and every gap reason.
  - No firewall credentials are entered.
- **No installation data in any file.**
- **Deletion and git.** Delete exact paths only, never by glob, and nothing destructive in the background. No git command that moves or discards work. No commit by an agent.
- **Parallel cycle.** The 5B cycle edits `web.go`, `messages.go`, `en.json` and `internal/store` too. Keep edits local and additive; the orchestrator sequences the merge.
- **Nothing is committed before live validation.** The orchestrator deploys, and the maintainer checks it on real data:
  - Sources agrees row for row with `-- diagnostic: Source availability`;
  - the indicator matches;
  - one network edit and its undo on a real interface are followed by a derivation, after which `live_this_firewall_unmarked` and `live_paired_counted_twice` stay 0.

## Out of scope

- The widget HTTP API (5B, the parallel cycle).
- Step 7: the widget engine, canvases, the dashboard format, the language picker, the five named palettes.
- Any schema change.
- `interface.user_label` editing.
- Suricata, step 6, and any change on the firewall.

## Decisions (maintainer, 10 October 2026)

All of the planner's recommendations were accepted.

| # | Question | Decision |
|---|---|---|
| 1 | `detail` | Rendered verbatim as data now. The closed outcome code is built in the 5B cycle (Group D1) and replaces it as the at-a-glance reading in a later pass. |
| 2 | Placement | Two new top-bar links, Interfaces and Sources. |
| 3 + 7 | Settings | Two resolver cards. An *Attribution* card holds the timing window and the cap. Every other listed setting is exposed (scope 2). |
| 4 | Outage rule | Per kind, as stated in scope 4. |
| 5 | Gaps | The last 24 hours. opnview's own downtime is recorded by the 5B cycle (Group D2). |
| 6 | Review data | A purpose-built synthesised fixture. |
| 8 | Confirmation | One network at a time. |
