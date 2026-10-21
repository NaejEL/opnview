# Architecture — the target

This page is the English rendering of the maintainer's architecture pages of
11 October 2026. It describes the architecture **aimed at**; where the code
differs, the code is wrong and `docs/adr/0017-core-audit.md` says what is done
about it. Every rule here is an ADR under `docs/adr/`; this page only draws the
picture.

## The path of a datum, from the firewall to the screen

```
 OPNsense ◄── read-only ──┐
 (REST API)               │
                     ┌────┴───────────────────────────────┐
                     │  GO CORE                            │
                     │  read-only rule → scheduler → broker│
                     └────┬───────────────▲────────────────┘
                          │ writes the    │ endpoint + max age
                          │ fields read   │ observed object
          ┌───────────────▼────┐    ┌─────┴──────────────────┐
          │ PostgreSQL +       │    │ PLUGINS (JS)            │
          │ TimescaleDB        │◄───┤ sources · services ·    │
          │ schema opnsense    │    │ widgets                 │
          │ one schema/plugin  │    │ no API key, no Go       │──► Internet
          │ core schema        │    └─────────────────────────┘    (their own
          └───────────┬────────┘                                    downloads)
                      │ SELECT-only role
          ┌───────────▼────────┐    ┌─────────────────────────┐
          │ DATA CATALOGUE     ├───►│ FRONT (JS, embedded)    │
          │ GET /api/datasets  │    │ canvases · widgets ·    │
          │ compiled query     │    │ themes · languages      │
          └────────────────────┘    └─────────────────────────┘
```

A plugin never contacts OPNsense itself. It names a declared endpoint and the
maximum age it accepts; the broker answers from the mirror when fresh enough,
otherwise asks the scheduler for one call, which passes the read-only rule
before leaving. The orange arrow of the original drawing, from the core to the
firewall, is the only outbound traffic of the core (ADR 0007). The plugins' and
the mirror's tables are datasets the widgets query (ADR 0011).

## The four things, and what each is

**The Go core** (ADR 0002) is the broker (ADR 0003, 0004), the plugin loader
and host (ADR 0001), the scheduler (ADR 0005), and the glue that cannot live
elsewhere: the database connection, a JSON API that serves what plugins declare,
accounts and encrypted settings (ADR 0006), the embedded front's files, and
byte-level protocol decoders (NetFlow v9 / IPFIX, syslog framing). It names no
source, no kind, no widget.

**A plugin** (ADR 0001) is a directory: `plugin.json`, its JavaScript, its SQL,
`locale/`, `testdata/`, `tests/*.test.js`. Built-in or dropped into
`<data-dir>/plugins/`, same format, same rights. Sources (pf, Suricata, Kea,
Dnsmasq, ISC, Unbound, Insight, NetFlow), services (MaxMind, Public Suffix
List) and widgets are all plugins. `opnview plugin test <dir>` runs its tests
against a fake firewall and a fresh database.

**The store** (ADR 0008) is PostgreSQL with TimescaleDB, in a second container
of the same installation, no port published, pinned by digest, Apache edition,
telemetry off. Schema `opnsense` is the learned mirror; each plugin has its own
schema; the core has its own.

**The front** (ADR 0016) is a JavaScript application embedded in the binary:
named canvases in tabs, widgets as plugins, themes and languages as files,
dashboards as JSON/YAML.

## Deployment: two containers, one command

```
 always-on host · Debian or Proxmox LXC (nesting) · docker compose
 ┌──────────────────────────┐     ┌──────────────────────────┐
 │ opnview container        │────►│ database container       │
 │ Go binary · embedded     │     │ PostgreSQL + TimescaleDB │
 │ front · built-in plugins │     │ pinned image             │
 │ --data-dir (required)    │     │ telemetry off            │
 │ database URL (required)  │     │ no published port        │
 └────────────┬─────────────┘     └────────────┬─────────────┘
              │                                │
        volume /data                     database volume
        key file · third-party           password generated at
        plugins · plugin data            first start, mode 0600
```

One command installs Docker Engine and compose if missing, writes the compose
file and a generated database password, and starts both containers. Re-running
it updates: pinned images pulled, restart, volumes kept. A backup is `pg_dump`
plus the key file. At start opnview waits a bounded time for the database, takes
a session advisory lock (a second opnview on the same database refuses to
start), and refuses to start if TimescaleDB's telemetry is on.

## The transition, written down

Until the migration of ADR 0020 ends, two things are temporarily untrue of
the picture above, and both are listed so their removal is a step and not a
discovery:

- **The bridge.** opnview's own connections and the read role resolve an
  unqualified table name through a search path that includes the schemas of
  the built-in plugins holding tables or relations, set from their manifests
  (`internal/store/sqldb/searchpath.go`, `internal/store/builtintables.go`).
  It exists for the Go that still names those tables and is scheduled for
  deletion; the plugin role never has it, and a plugin's SQL names every
  moved table by its qualified name.
- **Dropped foreign keys.** A core table that references a moved table
  carries no `REFERENCES` to it until its own wave moves it; the Go writers
  keep the integrity meanwhile (ADR 0020).

## What this is not

- Not a firewall manager: nothing is written to OPNsense, ever (ADR 0003).
- Not a packet capture: it sees what crosses the router and what OPNsense logs,
  so every figure is a lower bound (ADR 0010).
- Not a framework: the plugin API is the one extension point, and a feature
  that needs Go outside the core is a defect of the core (ADR 0002).
