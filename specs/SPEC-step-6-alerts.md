# SPEC — Step 6: alerts and client correlation (Suricata)

Status: APPROVED — by the orchestrator, 18 October 2026, under the maintainer's instruction to deliver end to end without questions.
Cycle kind: interface

**Routing.** Lot A is standard work: `factory-builder`, verified by `factory-verifier`. Lot B is interface work: `factory-ui-builder`, verified by `factory-verifier` and `factory-ergonomist`.

## Context

**What exists today**

- **Ingestion.** Suricata alert ingestion already exists, as a JS provider over a core kind:
  - `plugins/suricata/` (`plugin.json`, `suricata.js`) implements the core kind `security_event`. Its endpoints are `query_alerts`, `alert_logs` and `status`. Its `probe` separates absent, stopped and running.
  - The contract is `internal/collect/provider/kinds/security_event.json`, rendered in `docs/plugin-kinds.md`. Its functions are `probe`, `rotation` and `alerts`.
  - The coordinator is `internal/collect/kinds/securityevents/securityevent.go`. It keeps the `(fileid, filepos)` watermark in `eve_ingest_cursor`, records lost rotations as `eve_rotation_lost` gaps, and inserts into `security_event`.
- **Correlation: only the source end.** Core classification places the event's source end: `classify_event_source` in `internal/store/flows/classify.statements.sql` writes `src_client_id`, `src_interface_id` and `src_is_this_firewall`.
- **The destination end is missing (G7).** Nothing places it (`docs/widget-catalogue.md`, *Gaps found*, G7). Many detections fire on the server-to-client packet, where the client is the destination. Such an alert currently names an address instead of a machine.
- **Severity and category.**
  - `provider_rule_info` exists in `internal/store/schema.sql`, provider-neutral and keyed `(provider_id, rule_identity)`. Nothing writes it, because no contract function can feed it.
  - Its `normalised_severity` is `NOT NULL`, so a rule without a severity cannot be cached.
  - The survey's live run got a 404 at `/api/ids/service/get_rule_info/<sid>` (`docs/opnsense-api-survey.md`, *Wrong paths*). The documented path is `/api/ids/settings/get_rule_info/<sid>` (Data source 2, gap 3). Its field names were never established.
  - The plugin endpoint `argument` vocabulary is `none | interface_names` only (`internal/plugin/contract/manifest.go`).
- **Widgets.**
  - The three alert presets (`security_alerts_over_time`, `alerts_by_signature`, `alerts_by_client_and_interface`; `docs/widget-catalogue.md`, *Security events*) have no directory, and answer `404 unknown_widget_type`.
  - Tests pin that absence: `internal/widget/widgets/catalogue_test.go` (`stepSixTypes`), `internal/widget/widgets/params_test.go`, `internal/web/surfaces/widgetapi/widget_test.go` and `internal/front/frontjs/editing_test.go`.
  - The step-7 default dashboard dropped them (F7-28).
- **Authoritative query.** `sql/queries/screens.sql`, *screen: Alerts*, left-joins `provider_rule_info` and returns `severity_state`. It is the query these widgets adapt.
- **Data catalogue.** The core dataset `security_event` (`internal/dataset/core/security_event/`) serves the event's own `normalised_severity`, which is NULL for every Suricata event. It carries no `rule_identity` and no destination end.

**Binding documents.**
- Data and API: `docs/widget-catalogue.md`, `docs/widget-api.md`, `docs/widget-authoring.md`, `docs/plugins.md`, `docs/plugin-kinds.md` and `docs/data-model.md`.
- Lot B also: `docs/ui-references.md`, `docs/dashboard-format.md`, `ROADMAP.md` steps 3 and 7, and `specs/SPEC-step-7-frontend.md` (F7-20 to F7-26).

**Repository rules that apply.**
- Zero hardcoded configuration.
- No secrets.
- Read-only against the firewall.
- Exactly three outbound calls; this cycle adds none.
- OPNsense and Suricata vocabulary: *alert*, *signature*, *sid*, *classtype*, *severity*, *interface*, *client*. Any coined term goes in `docs/data-model.md`, *Vocabulary*.
- Everything in English. No user-visible literal.
- A second `security_event` implementation is a directory and nothing else.
- Site names are out of this step.
- The observation-point limit is documented, not printed.

**Testing discipline (the maintainer's rule, binding).**
- While working, run only the tests of what you changed.
- Run `docker compose run --rm checks` once, before the commit.
- No `pre-deployment`, no seed rebuild, and no test-timing work as a goal.

## Scope

**The principle.** Suricata-specific work is plugin JavaScript: reading alerts, reading rule metadata, mapping severity, and the three presets' queries, scripts and renderers. The core changes only where the open API has a **generic** gap; each core change states the gap it closes.

### Lot A — backend (`factory-builder`)

- **S6-1 Rule metadata endpoint, verified.**
  - From the OPNsense **26.7.3** source, establish which read endpoint returns one rule's metadata by sid, and its exact field names. Start with `controllers/OPNsense/IDS/Api/SettingsController.php`: `get_rule_info`, and `search_installed_rules` as an alternative.
  - Explain the recorded 404, which was the `service` controller and not `settings`.
  - Record path, method, parameters, fields and citation in `docs/opnsense-api-survey.md`, Data source 2 and *Wrong paths*. Carry `survey_section` and `upstream_url` on the endpoint declaration in `plugins/suricata/plugin.json`.
  - Map severity, per the table in `docs/data-model.md` *Severity*, **only** from a field the cited source shows to be Suricata's rule severity. Otherwise severity is "not reported". It is never derived from `classtype`, ruleset name or signature wording.
  - Store `classtype` as the category. Store the raw value verbatim as `provider_severity`.
- **S6-2 Generic gap 1: positional endpoint argument.** This applies only if S6-1 shows that the fields are served solely by a path-positional endpoint.
  - Extend the endpoint `argument` vocabulary with one generic positional kind, a single opaque path segment that is URL-escaped.
  - Keep every existing read-only and undeclared-path refusal, and document it in `docs/plugins.md`.
  - If a declarable read endpoint serves the fields, use it and make no core change.
- **S6-3 Generic gap 2: the `security_event` contract feeds the rule-info cache.**
  - Add one function to `security_event.json`, for example `rule_info(host, rule_identities)`. Per identity asked it answers found (normalised severity or none, raw severity, category, rule source) or not found, plus `result`.
  - Regenerate `docs/plugin-kinds.md`.
  - The coordinator calls it after an alerts pass. It asks only for identities seen in that provider's events with no cache row, or whose row is older than a declared re-check age.
  - A declared per-pass bound limits each call. Both values carry a stated basis. What is not asked carries over to the next pass.
- **S6-4 The cache states its misses.** Edit `provider_rule_info` in place (pre-POC):
  - `normalised_severity` becomes nullable;
  - a closed lookup-state column distinguishes `found` from `not_found`; an absent row means "not fetched";
  - served alert rows carry `severity_state` (`resolved`/`unknown`) and, when unknown, a closed `severity_reason`: `not_fetched`, `rule_not_found` or `not_reported_by_provider`;
  - inline event severity takes precedence over the cache.

  Update `docs/data-model.md` and `sql/seed.sql`.
- **S6-5 A failed rule-info call fails nothing else.**
  - It never fails or rolls back the alert pass, and never changes the provider's availability.
  - It is logged with the provider and the outcome. The identities stay `not_fetched` and are retried.
  - No call is made with no event stored.
- **S6-6 Suricata implements `rule_info`** in `plugins/suricata/suricata.js`: mapping per S6-1, fixtures in `plugins/suricata/testdata/`, and tests for found, not found, no severity field, 404 and 403.
- **S6-7 Generic gap 3: the destination end (closes G7).**
  - Add `dst_client_id`, `dst_interface_id` and `dst_is_this_firewall` to `security_event`, mirroring the source triple and its check constraint, with indexes.
  - Extend classification (the per-pass derivation and full reclassification) to place the destination end by the same membership evidence. An outside address never becomes a client.
  - Move G7 to *Gaps closed*, and update the open-gap count.
- **S6-8 Observed interface.**
  - Served rows carry `in_interface_device`, resolved by device to a discovered interface at read time. A device that matches none, for example a netmap-suffixed device in IPS mode, is served raw with a named state.
  - Resolve survey S14 (`in_iface`) from source.
- **S6-9 Three widget plugins**, in `plugins/security_alerts_over_time/`, `plugins/alerts_by_signature/` and `plugins/alerts_by_client_and_interface/`.
  - Each holds `plugin.json`, `widgets/<type>/manifest.json`, statement files, a script, `locale/en.json` and `tests/*.test.js` with golden answers.
  - Shapes and parameters are exactly the catalogue entries:
    - `security_alerts_over_time`: `time_series` with `period`, `severities`, `interfaces`, `clients`, `bucket`, `providers`;
    - `alerts_by_signature`: `table` with `period`, `severities`, `interfaces`, `clients`, `limit`, `providers`, `sort` (`count`|`severity`|`recency`);
    - `alerts_by_client_and_interface`: `table` with `period`, `severities`, `interfaces`, `limit`, `sort` (`count`|`worst_severity`), `include_unmatched` (default true).
  - `severities` admits `unknown`. `providers` is a `reference_list` of kind `provider`.
  - Manifests: `providers` lists `security_event`, plus `dhcp_lease` for the client table; `primary` is `security_event`; `windowed: true`; `no_domains: "none"`; `lower_bound: true`; the measure is `security_events`.
  - Each statement file states which *screens.sql* query it adapts.
- **S6-10 Aggregation content.** Every row or band names its provider (kind, key, `display_name` from the registry).
  - *Over time*: one series per severity present, `unknown` included. Buckets per decision 8 and D6. The series sum to the total.
  - *By signature*: one row per `(provider, rule_identity)`: signature, count, last seen, normalised severity beside `provider_severity`, `severity_state` and `severity_reason`, category, rule source, and the clients and interfaces at either end.
  - *By client and interface*: one row per client at either end, with count split source/destination, worst severity, interfaces and top signatures.
    - A this-firewall end is its own row.
    - Events with no client end are rows labelled by source address under `include_unmatched`, never dropped.
    - The distinct-event total is served apart, and the rule that rows need not sum to it is documented.
  - Every limited table serves its remainder with a count.
- **S6-11 No Suricata at all.** The existing envelope rules apply:
  - 404/401/403 → `unavailable`;
  - stopped or disabled → `present_but_disabled`;
  - running with nothing in the window → `reachable_no_rows`;
  - degraded with history → the history under the condition;
  - nothing stored → `data: null`.

  With no registered `security_event` provider, each widget answers 200 with a non-reachable condition and `data: null`, never 500. Nothing throws or logs an error because Suricata is absent.
- **S6-12 Data catalogue.** In the core dataset `security_event`: `normalised_severity` serves the resolved severity, and it adds `severity_state`, `rule_identity`, `dst_client_id` and `dst_interface`. Update the doc.
- **S6-13 Tests and documents that pinned the absence.**
  - Update the listed Go tests so the served types equal the full preset index.
  - Update `docs/widget-api.md`, `docs/architecture.md`, `docs/widget-catalogue.md` Part 5 and `docs/plugins.md` (D4).
  - `ROADMAP.md` step 6 becomes "implemented, not yet validated live", not *Done*.
- **S6-14 Front harness stays green.** Lot A supplies the recorded answers the front harness needs for every served type (F7-AC17). Shape renderers draw the three types until Lot B.

### Lot B — interface (`factory-ui-builder`; verified by `factory-verifier` and `factory-ergonomist`)

- **S6-15 Renderers.** One `renderer.js` per widget, a pure function drawing its catalogue shape (F7-20 to F7-24).
  - `unknown` severity is its own band or value, naming its `severity_reason` through the catalogue.
  - The raw `provider_severity` is shown beside the normalised one.
  - An unmatched address is shown whole.
- **S6-16 Detail and pivot (F7-26).** Each signature row, client, interface, provider and severity band opens a detail. The detail pivots to related subjects, offers "scope this canvas to it" where a scope kind applies, and offers "add a widget about it". Opening and closing a detail leaves `revision` unchanged.
- **S6-17 Degraded states.** Every condition of S6-11 gets its F7-22 panel, with content per catalogue Part 3, row `security_event`, and no explanatory copy.
- **S6-18 Picker and default dashboard.** The picker offers the three types. `front/dashboards/default.json` regains the mockup's `alerts_by_signature` card at its mockup placement and parameters, with portable references only.

## Acceptance criteria

### Lot A
- [ ] **S6-AC1** — The survey records the rule-metadata endpoint actually used (path, method, parameters, every field read, cited to the 26.7.3 source or docs.opnsense.org) and why `/api/ids/service/get_rule_info` gave a 404. The declaration carries `survey_section` and `upstream_url`.
- [ ] **S6-AC2** — With the rule-metadata endpoint answering 404 or 403, a test asserts:
  - the alert pass stores its events;
  - availability is the alerts read's;
  - no `provider_rule_info` row is written;
  - the rows are served `unknown` / `not_fetched`;
  - no error is raised for the pass.
- [ ] **S6-AC3** — `docs/plugin-kinds.md` equals the rendering of the contract, which lists the rule-info function. A `security_event` test plugin without the function is refused `script_missing_function`.
- [ ] **S6-AC4** — A coordinator test with a fake implementation proves:
  - only missing or expired identities are asked;
  - no more than the per-pass bound is asked;
  - the remainder is asked next pass;
  - a pass storing no event makes no call.
- [ ] **S6-AC5** — Fixtures prove:
  - the severity field maps 1→`critical`, 2→`high`, 3→`medium`, 4→`low`, with the raw value in `provider_severity`;
  - a rule without the field is found with null severity and served `not_reported_by_provider`;
  - an unknown sid is cached `not_found` and served `rule_not_found`;
  - the category is `classtype` verbatim.
- [ ] **S6-AC6** — grep finds no mapping from `classtype`, ruleset or signature text to a severity. A test with a classtype and no severity serves unknown.
- [ ] **S6-AC7** — Store tests:
  - the schema applies on an empty database;
  - `normalised_severity` accepts NULL;
  - the lookup-state column accepts exactly its closed values;
  - the `dst_*` triple carries the source triple's constraint.
- [ ] **S6-AC8** — A classification test on a runtime-discovered fixture asserts:
  - outside source to a leased destination gets that client's `dst_*` and no outside client row;
  - a firewall destination gets `dst_is_this_firewall`;
  - full reclassification equals the per-pass derivation.
- [ ] **S6-AC9** — `in_interface_device` matching a discovered device names that interface. A non-matching one is served raw with its named state, and the row is kept.
- [ ] **S6-AC10** — `GET /api/widget/{type}` answers 200 for each of the three. Each manifest declares exactly its catalogue keys (`docs/mockups/check-form-covers-catalogue.js`). Served types equal the preset index.
- [ ] **S6-AC11** — `security_alerts_over_time` goldens:
  - one series per severity, `unknown` included;
  - buckets sum to the series, and series sum to the events;
  - every series names its provider;
  - each `severities` and `providers` value changes the answer.
- [ ] **S6-AC12** — `alerts_by_signature` goldens:
  - one row per `(provider, rule_identity)`, with rows plus remainder equal to the events;
  - every field of S6-10 is present;
  - each `sort` orders differently;
  - `limit` produces a counted remainder.
- [ ] **S6-AC13** — `alerts_by_client_and_interface` goldens:
  - an east-west event counts under both clients;
  - outside to client is attributed as destination;
  - a firewall end is its own row;
  - unmatched rows carry the full address under `include_unmatched` and are absent without it;
  - the distinct total equals the events;
  - each `sort` orders differently.
- [ ] **S6-AC14** — With no Suricata, for each type:
  - 404 gives `unavailable` and `data: null`;
  - stopped gives `present_but_disabled`;
  - running with no alert gives `reachable_no_rows`;
  - stored events then 404 give the history under `unavailable`.

  No script failure is logged. With no `security_event` provider registered, each type answers 200 with `data: null`.
- [ ] **S6-AC15** — A third-party `security_event` plugin under `internal/plugin/testdata/plugins/`, with probe, rotation, alerts and rule-info and some inline severities, is loaded beside suricata with no repository file changed. Its events appear in all three widgets under its own key and `display_name`. Its inline severities are resolved, and its rule-info answers fill rows keyed by its provider.
- [ ] **S6-AC16** — The `security_event` dataset returns the resolved severity and the new columns.
- [ ] **S6-AC17** — The new plugin directories hold no interface name, device, non-documentation address, or `Suricata` used as a label. Every user-visible word is a locale key.
- [ ] **S6-AC18** — The documents of S6-13 are updated. G7 is closed, the count is updated, and step 6 is not *Done*.
- [ ] **S6-AC19** — `docker compose run --rm checks` passes once before each commit. No new outbound destination.
- [ ] **S6-AC20** — Everything is in English.

### Lot B
- [ ] **S6-AC21** — Each widget has a renderer and a recorded answer, and passes F7-AC17: no throw, every rendering parameter changes the view, no inert key.
- [ ] **S6-AC22** — The renderer tests show:
  - `unknown` is drawn with its reason key;
  - `provider_severity` sits beside the normalised severity;
  - the unmatched address is rendered whole;
  - every remainder is drawn with its count.
- [ ] **S6-AC23** — Every S6-11 condition draws the F7-AC20 panel.
- [ ] **S6-AC24** — The picker lists the three types. The default dashboard has `alerts_by_signature` at the mockup placement, validates, carries no `interface`/`client`/`rule`/`owner` reference, and renders with no `unknown_widget_type` or `invalid_parameters`.
- [ ] **S6-AC25** *(ergonomist)* — Opening and closing a detail on a signature row, a client, an interface and a severity band: each offers a pivot, `revision` is unchanged, and "scope this canvas to it" on a client is undone by Undo.

### For maintainer review (not acceptance criteria)
1. Whether severity banding is useful if his rulesets report no severity.
2. Whether attributing an alert to a client at either end (D2) matches how he reads "which machine".
3. Whether showing signature text under `no_domains` (D7) is acceptable.
4. Whether the renderers read as the mockup's alert card.

## Out of scope
- G8, the interfaces a provider covers.
- Suricata over syslog (D4).
- Severity derived from classtype, ruleset or text.
- Suricata `dns`/`tls`/`http` events and site names.
- Any firewall change.
- Test-timing, seed-rebuild and pre-deployment work.
- Migrations.
- Edits to `sql/queries/screens.sql` beyond keeping it valid.
- Application classification (G12).

## Risks
- The rule-metadata endpoint may carry no severity: the likely live outcome is category resolved and severity unknown. That is correct.
- S6-1 may force S6-2, a core change whose read-only guarantees must be re-tested.
- The contract change breaks third-party `security_event` providers until they add the function. Only the built-in exists.
- `in_iface` in IPS mode may carry a netmap suffix; it is served raw.
- The live firewall produced no alert in seven days, so live validation needs an alert raised on purpose.
- The schema edits change the seed digest; the next pre-deployment rebuilds the seeds. That is not work of this cycle.
- `checks` has a 60 s budget: keep the fixtures small, and report an overrun.
- Signature text may contain domains (D7).

## Decided questions (by the orchestrator, 18 October 2026, on the Planner's recommendation; not the maintainer's decisions)
1. **D1:** rule-info resolution is a contract function the core coordinator calls, writing the provider-neutral `provider_rule_info`. The Suricata mapping lives in JS.
2. **D2:** an alert names a client at either end; G7 is closed in the core, and the catalogue binding is amended.
3. **D3:** with no severity field, severity is `unknown` with reason `not_reported_by_provider`, never inferred.
4. **D4:** Suricata over syslog is out of this cycle; `docs/plugins.md` says it is deferred beyond step 6.
5. **D5:** the re-check age and per-pass bound are declared with a stated basis, and apply to found and not_found alike.
6. **D6:** `bucket` takes `auto` (the decision-8 width, default), `1m`, `15m`, `1h` or `24h`. A width that would exceed 1,000 buckets is served at the finest width that does not, and the width used is stated.
7. **D7:** signature text is not withheld under `no_domains`; this is documented.
8. **D8:** the mockup's `alerts_by_signature` card returns to the default dashboard. Existing installations are not re-seeded.
9. **D9:** two lots, routed as stated at the top.
10. **D10:** `lower_bound: true`, stated in the payload and never printed.
