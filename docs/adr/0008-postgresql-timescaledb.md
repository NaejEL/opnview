# 0008 — PostgreSQL with TimescaleDB, pinned, Apache edition, one schema per plugin

Status: Accepted
Decided by: the maintainer, 11 October 2026 (engine); 16 October 2026 (Apache edition only)

## Context

SQLite imposed a single writer, a separate read pool, hand-built hourly, daily,
weekly and monthly rollups, and one namespace for every plugin's tables. An
earlier rule, written by the orchestrator and not the maintainer, forbade a
database server to protect the install story. The install story is kept; the
judgement on the cost changed.

## Decision

- The store is **PostgreSQL with the TimescaleDB extension**, run beside opnview
  as part of the same installation. **One command installs, re-running it
  updates, one documented command backs up** (`pg_dump` plus the key file). The
  user never creates a database, a role or a password.
- **Versions are pinned** by exact version and image digest, written once
  (`docker-compose.yml`), changed deliberately, never floated.
- **Apache-2.0 edition only.** No continuous aggregate, no TimescaleDB background
  job: every database runs with `timescaledb.license=apache`, the tests
  included, which proves nothing TSL is used. opnview stays MIT and neither links
  nor redistributes TimescaleDB. (This supersedes the 12 October decision to
  enable the Timescale License: no continuous aggregate was exactly equivalent
  to the table it would replace.)
- **Telemetry off**, and opnview refuses to start otherwise.
- **One schema per plugin**, created and purged by opnview. A declared table with
  a time column is a hypertable when its constraints keep their meaning. Each
  declared column carries a role (`time`, `dimension`, `measure`) and a unit. If
  a plugin's schema changes, the old table is moved to a schema set aside, never
  read; nothing is lost in silence. The learned mirror lives in schema
  `opnsense`; the core's own tables in the core schema.
- **Retention is exact and on opnview's clock**: chunks wholly past the horizon
  are dropped; the chunk straddling it is purged row by row.
- Text ordering is byte order: databases are created with `--locale=C`, and the
  store refuses one created otherwise.
- **Not permitted:** a second storage engine or data store without a maintainer
  decision; a database reachable through a published port; a database credential
  in the repository.

## Consequences

- Development and tests run against a TimescaleDB container the compose file
  starts; there is no SQLite path, no in-memory store.
- A restore is done into the same pinned TimescaleDB version, by the procedure
  that version documents.
- At start opnview waits a bounded time for the database, then stops naming it;
  it takes a session advisory lock so a second opnview on the same database
  refuses to start.
