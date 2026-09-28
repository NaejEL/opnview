# SPEC — The two kinds the model is short, and the constraint that is wrong

Status: APPROVED
Cycle kind: standard

## Why now

Research into what OPNsense installations actually run, recorded in
`docs/opnsense-api-survey.md` under *What the plugin ecosystem actually
exposes*, found that of ~34 data-producing sources:

- **8 of the 10 that fit an existing kind** fit `measurement_sample`, which is
  **not a kind** — no `provider_key`, no availability row, no place in the
  `provider.kind` CHECK;
- **10 of the 11 that fit nothing** are one single shape: a **reconciled set**,
  the current things of type X replaced wholesale on each poll rather than
  appended to;
- counting only the log-shaped kinds, **two sources in the whole ecosystem** add
  to them.

So the model is not wrong and it is not fine. It is short by one kind and a
promotion, and the connector protocol cannot be designed until that is fixed —
designing it around the log-shaped kinds would be designing for the 6 % case.

The maintainer chose to do this before 4B rather than after, so the interface is
not built on a model about to change.

## What changes

### 1. `measurement_sample` becomes a kind

A seventh value in `provider.kind`, a registry row, an availability row, and a
`provider_id` on the samples that have one. The firewall's own gauges keep a
null provider — the machine reporting on itself implements no external contract
— and that is recorded rather than worked around.

The `subject`, `measure` and `unit` vocabularies must be **extensible by a
provider**, because the sources this absorbs do not share one: a UPS reports
volts, SMART reports reallocated sectors, HAProxy's subject is a backend and
not an interface or a client.

### 2. A reconciled-state kind

The shape: *here is the complete set of things of this type, as of now.* Not an
append-only record. What it must carry, from the sources examined: an identity
within its set, the attributes, an optional validity end (CrowdSec decisions
carry a TTL and no timestamp at all), and the fact of the set being **complete
at an instant** — because that is what makes a departure detectable. A thing
that was in the set yesterday and is not in it today has *left*, and the model
must be able to say so rather than silently keeping it.

`interface` and `dhcp_lease` already are reconciled state, modelled as bespoke
tables. **Do not migrate them in this cycle** — they work, they are verified by
395 assertions, and the point here is to let *other* sources supply that shape.
Say in `docs/data-model.md` that they are the same shape modelled twice, and why
that was left alone.

### 3. `provider.is_active` stops meaning one per kind

People run Suricata, CrowdSec and Zenarmor together; the how-to corpus is people
stacking them. Three concurrent `security_event` providers is the normal case,
not the exotic one. The partial unique index over `kind WHERE is_active = 1`
goes.

What replaces it is a decision this cycle must take and record: *what, if
anything, is still exclusive.* A kind where two active providers would
double-count is different from one where they are complementary. Propose the
rule, implement it, and write the reasoning beside it.

### 4. `pair_volume_observation` is declared derived, not collected

The maintainer's ruling. No collector writes it: the only per-pair endpoint
carries neither port nor protocol, and `flow` carries both. It becomes **step
5's to compute from `flow`**, said plainly in `docs/data-model.md` so nobody
looks for the collector that fills it. The sampled per-pair volume stays in
`measurement_sample`, where 4A already puts it.

### 5. `dhcp_lease.starts_at` stops meaning two things

Kea reports a real start (`expire` minus `valid_lifetime`); Dnsmasq reports
none, and 4A uses the expiry as a generation discriminator, honest only because
a comment says so. Make the start **nullable**, add an explicit generation key,
and let a backend that cannot know say so instead of substituting.

## Acceptance criteria

- [ ] **AC1** — `docker compose run --rm checks` and `schema-checks` both exit 0,
      with **no assertion removed or relaxed**. Assertions about the index being
      dropped are restated against the rule that replaces it, never deleted.
- [ ] **AC2** — the schema stays one file. No migration, no `schema_version`.
- [ ] **AC3** — `measurement_sample` has a kind, a registry row and an
      availability row; a sample from a provider carries its `provider_id`, and
      a firewall gauge carries null, asserted both ways.
- [ ] **AC4** — a provider can introduce a `subject`, `measure` or `unit` the
      schema did not ship with, without a schema change. A test adds one and
      reads it back.
- [ ] **AC5** — the reconciled-state kind stores a complete set at an instant,
      and a test proves a **departure is detectable**: a thing present in one
      snapshot and absent from the next is recorded as having left, not silently
      dropped and not silently kept.
- [ ] **AC6** — two providers of one kind can both be active. A test activates
      two `security_event` providers and both stay active. The rule that
      replaces exclusivity is implemented and asserted.
- [ ] **AC7** — `pair_volume_observation` is documented as derived and no code
      path writes it. A test fails if one appears.
- [ ] **AC8** — `dhcp_lease.starts_at` is nullable; a Dnsmasq lease stores null
      and a Kea lease stores the computed start, both asserted, and the
      generation key keeps a re-poll idempotent.
- [ ] **AC9** — `sql/seed.sql` seeds the new kinds, and the seven screen queries
      still plan without scanning a growing table.
- [ ] **AC10** — every new term is `opnview`'s own only where OPNsense has none,
      and `docs/data-model.md`'s vocabulary section says which and why.

## Out of scope

- The connector protocol, the manifest, the plugin host. This cycle makes them
  designable; it does not design them.
- Migrating `interface` and `dhcp_lease` onto the new state kind.
- Any connector for any plugin. No CrowdSec, no Zenarmor, no WireGuard.
- 4B's accounts, sign-in and settings.
- Per-kind package directories — the maintainer chose to do those with the
  plugin engine.

## Risk

The reconciled-state kind is the one piece here designed from research rather
than from a working collector, and this project has been caught twice by
building against a document. Keep it as small as the evidence supports: a set,
an instant, an identity, attributes, an optional expiry. **Anything the examined
sources do not actually need does not go in.**
