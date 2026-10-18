# 0001 — Everything is a plugin: a directory of JSON, JavaScript and SQL

Status: Accepted
Decided by: the maintainer, 11 October 2026 (architecture target), restated 17 and 18 October 2026

## Context

opnview reads one installation's OPNsense, and no two installations are alike:
a resolver we do not run, a DHCP server we do not use, an OPNsense plugin nobody
here has installed are the normal case. The maintainer's own configuration is one
installation among many and must never be the shape of the code. A third party
must be able to add a source, a service, a widget, a theme or a language without
forking, without recompiling and without reading the Go.

## Decision

- A **kind of thing** (a lease source, a lookup source, a widget, a theme, a
  language, an external service) is a seam. An **implementation** of a kind is a
  directory under `plugins/` (built-in) or `<data-dir>/plugins/` (third party).
- A plugin is: `plugin.json` (its manifest: the endpoints it reads, the kinds it
  provides, its settings, its tables, its widgets), its JavaScript, its SQL, its
  `locale/`, its `testdata/`, and its tests in `tests/*.test.js`. **No Go file
  has a place in a plugin; a test of the repository fails if it finds one.**
- Adding a second implementation of a kind is adding a directory. If it means
  editing a file that ships with the product, the rule is not met.
- A plugin that fails to load is logged with its reason and ignored; the others
  start. On a name collision the built-in wins.
- Every plugin is tested by `opnview plugin test <dir>`: a fake firewall that
  answers only what the test prepared, a fresh database and data directory, a
  fake web for downloads.
- The built-in sources (pf, Suricata, Kea, Dnsmasq, ISC, Unbound, Insight,
  NetFlow), services (MaxMind, Public Suffix List) and widgets are plugins in
  this exact format, with no privilege a third party's plugin lacks.

## Consequences

- The plugin API (the host functions JavaScript may call, the manifest schema)
  is the product's only contract and is documented in `docs/plugins.md` and
  `docs/plugin-kinds.md`. It changes by ADR.
- Everything a plugin needs that the host does not offer is a defect of the
  host, reported as such; it is never worked around in Go (see 0002).
- `internal/modularity` keeps the tests that prove the rule: no Go in
  `plugins/`, no product name in the core's Go.
