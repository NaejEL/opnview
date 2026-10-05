# opnview — project rules

## Language — ABSOLUTE RULE

**Everything produced in this repository is written in English. No exception.**

This covers, without being limited to:

- source code, identifiers, comments, docstrings;
- commit messages, branch names, tags;
- `README.md`, `ROADMAP.md`, and every other document;
- specifications under `specs/`, and their status lines;
- agent prompts, skills, CI scripts, their log output and error messages;
- the web UI: every label, heading, tooltip, empty state and error message —
  English is the **source language**, not the only one the product speaks; see
  *The user interface* below;
- API payloads, field names, enum values;
- the GitHub repository description, topics, issues and pull requests.

The maintainer speaks French. **That is irrelevant to the artifacts.** A
conversation held in French still produces English output. If any file in this
repository is found written in another language, translating it is a defect to
fix, not a preference to debate.

## Vocabulary — ABSOLUTE RULE

**`opnview` uses the vocabulary of OPNsense and of the products it integrates
with. It never invents a term for something those products already name.**

When the right term is not known, it is researched — in
`docs/opnsense-api-survey.md`, on `docs.opnsense.org`, or in the product's own
documentation — or the maintainer is asked. It is never guessed, and never
embroidered to save a lookup. Where a product genuinely has no word for
something `opnview` needs, say so explicitly, choose the term deliberately, and
record why next to it.

**The reason this is written down: it was broken silently and nobody noticed
for three steps.** `segment` was invented in the first commit, before any
research existed, for the thing OPNsense calls an **interface** — while
`docs/opnsense-api-survey.md` had been carrying `get_interface_names`, `descr`
and `link_type` since step 1. `device` meant the network device in one place
and a client machine in another. Both are fixed. The term-by-term mapping, and
the terms that are `opnview`'s own because OPNsense has none, are in
`docs/data-model.md` under *Vocabulary*.

## Project

`opnview` gives inter-interface visibility on a VLAN-segmented network behind an
OPNsense firewall. **One service, installed and updated by a single command,
with no external data store to provision; storage engines are embedded
libraries, not servers** — the permitted and forbidden engines are listed in
`ROADMAP.md` under *Rules that apply to every step*, and that bullet is the
single place the rule is stated. Local SQLite, frontend embedded in the binary
and served by it.

## The user interface

**Modular at every layer, not only the frontend.** A kind of thing is a seam
and an implementation is a file behind it, selected at runtime by what the
installation has — a lease source, a lookup source, a language, a theme, a
widget. Adding a second implementation is adding a file and a registry row and
touching nothing else. The maintainer's own configuration is one installation
among many and never the shape of the code. `ROADMAP.md`, *Rules that apply to
every step*, carries this in full.

**The frontend is modular, and a third party can extend it.** A language is a
file, a theme is a file, a widget is a directory holding its manifest, its query
and its renderer. Somebody who has never read the source must be able to add any
of the three easily and naturally, from the documentation alone; if adding one
means editing a file that ships with the product, the rule is not met. **No
user-visible string is a literal** — every one comes from the string catalogue,
English is the source language, and the user switches language in the interface.
The step-3 mockup is a single file and is exempt, being a mockup. `ROADMAP.md`
step 7 carries the detail.

**Named canvases, not fixed screens.** The user creates canvases and composes
each from widgets, moving between them with **tabs rather than menus**. No view
is reachable only through a menu inside a menu. Dashboards are describable as
code, in JSON and/or YAML, and are portable between installations.

**Palette and theme.** The maintainer's industrial palette is the default;
Tokyo Night, Dracula, Nord, Rosé Pine and Catppuccin are offered as named
options **at the values published on the maintainer's own site,
https://lequellec.xyz**, reproduced in `docs/ui-references.md`. That site, not
each palette's upstream project, is the authoritative source for what those
five names mean here; the upstream values differ and must not be substituted. On first launch the theme follows
the operating system's preference; the user can override it afterwards and the
override persists.

Three documents are **binding** on any interface work, and a design decision
that contradicts one of them without saying so is a defect rather than a
variation:

- `docs/ui-references.md` — the visual language, the surveyed prior art and,
  in its section *The maintainer's recorded preferences*, the per-question
  preferences the mockup is judged against;
- `docs/widget-catalogue.md` — the twelve widget **shapes**, the **presets**
  that bind data to them, and the *Gaps found* table naming what the model
  cannot yet answer;
- `docs/dashboard-format.md` — the dashboard file format, its references, its
  versioning and its import behaviour.

It reads five sources through the OPNsense REST API: filter logs, the Suricata
`eve.json` event log (optional — IDS alerts only), NetFlow/Insight, DHCP
leases, and resolver DNS lookups (the sole source of site names).

Suricata was planned as the primary source of site names. The step-1 API
survey established that it cannot be: OPNsense exposes no `dns` event type,
and `tls` / `http` events, though they can be written to `eve.json`, cannot be
read back through the API. See `docs/opnsense-api-survey.md`. Site names are
inferred from resolver lookups, unconditionally.

## Development and test environment

No Go toolchain is installed on the host. The toolchain lives in a Debian
`trixie-slim` container with the Go version pinned and checksum-verified. The
four project commands run through one invocation, identical in PowerShell and
in bash:

```
docker compose run --rm checks
```

Use `docker compose run --rm dev <command>` for anything else in the same
environment. Never run `go`, `gofmt` or `sqlite3` on the host. Details, and the
list of what this environment does *not* prove, are in `ROADMAP.md` under
*Development and test environment*.

See `ROADMAP.md` for the eight-step plan and the cross-cutting engineering
rules (no hardcoded configuration, no secrets in the repo, read-only against
the firewall, exactly three outbound calls, the observation-point limit
documented rather than printed on screen).
