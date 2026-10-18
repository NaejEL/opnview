# 0002 — The Go core is the broker, the plugin loader and the scheduler

Status: Accepted
Decided by: the maintainer, 18 October 2026 ("apart from the broker, the plugin
loader and the scheduler, nothing should have been written in Go. No exception.")

## Context

On 18 October 2026 the repository held 85 000 lines of non-test Go. Of these,
19 000 were the broker and the plugin host; 56 000 were a store, a widget engine,
a collection engine, HTTP surfaces and push decoders, all product logic that the
plugin rule (0001) says belongs to plugins. An earlier specification had recorded,
as its decision 4, that store, classification, aggregation and the widget engine
would stay in Go. That decision was the orchestrator's, not the maintainer's, and
it contradicts this one. It is void.

## Decision

The Go of opnview is exactly:

1. **The broker** to the OPNsense API: the one path to the firewall, the
   read-only rule (0003), one call per datum and the learned mirror (0004).
2. **The plugin loader and host**: manifest parsing and validation, the registry,
   the embedded JavaScript engine, the host functions, the `plugin test` harness.
3. **The scheduler**: deadlines, the real-time reserve, backoff, the cadences
   (0005).

Plus the glue that cannot be anywhere else, and nothing in it that names a
source, a kind, a widget or an OPNsense concept:

- the PostgreSQL connection and the creation of a plugin's declared tables;
- a JSON API that serves what plugins declare (datasets, widgets, settings
  pages) without knowing any of them by name;
- accounts, login, the encryption key and the encrypted settings (0006);
- the embedded front's static files;
- generic protocol decoders whose cost in JavaScript is prohibitive (NetFlow v9
  / IPFIX, syslog framing), which decode bytes into records and know no product.

Everything else is a plugin. **No exception is left open in a brief.** A feature
that seems to need Go elsewhere is a defect of the core's design: it is reported
to the maintainer, never worked around in Go.

## Consequences

- The existing Go is judged package by package against this list in 0017, with
  one of three verdicts: keep, move to a plugin, delete. The maintainer decides
  each; nothing is cut before he does.
- The host-function contract is what the plugins depend on; the audit lists it
  so that moving a package does not silently break a plugin.
- `internal/modularity`'s product-token test is the guard: the allow-list of
  places that may name an OPNsense path may only shrink.
