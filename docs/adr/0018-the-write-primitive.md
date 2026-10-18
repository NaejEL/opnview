# 0018 — The write primitive: a plugin owns its tables, its views and its writes in SQL

Status: Proposed
Proposed by the orchestrator, 18 October 2026, as the first step of the cut the maintainer ordered in ADR 0017

## Context

A plugin can already declare tables (one PostgreSQL schema per plugin,
`plugin_<name>`), have rows written for it from JavaScript (a pass, a push
batch, a derivation's returned rows), read any table with `host.query` and any
dataset with `host.datasets`, and have its tables purged by retention. What it
cannot do is what 56 000 lines of core Go do today: run SQL that **writes**
(classification of flows, leg pairing, site attribution, the rollups), publish
a **view or a function** other plugins read (`classified_flow`,
`blocked_decision`, `canonical_address()` live in `schema.sql` and 21 plugin
files read them), and say it **depends** on another plugin's tables. Without
these three, store, collection and the CRUD surfaces cannot move (ADR 0017).

The design adds to what exists; it replaces nothing a plugin uses today.

## Decision

### 1. Declared write statements

- A plugin's manifest lists statement files: `"statements": ["pf.statements.sql"]`.
  A file holds named statements, each under a marker line `-- statement: <name>`,
  with `:name` parameters, exactly as a widget's statements do today.
- A statement may read anything the read role reads, and may **INSERT, UPDATE
  or DELETE only in the plugin's own schema**. This is enforced by PostgreSQL,
  not by parsing: every plugin gets a role, `<database>_plugin_<name>`, with the
  read role's SELECT grants and INSERT, UPDATE, DELETE on `plugin_<name>` alone;
  a statement runs under `SET LOCAL ROLE` for that plugin. At load, every
  statement is planned (`EXPLAIN`) under that role, so a statement that writes
  elsewhere or reads a credential table refuses the plugin, with the closed code
  `statement_refused` and the statement's name.
- A statement is run by a **derivation**, in the derivation's single
  transaction:
  - a derivation with a script function returns, beside `rows`, `measures` and
    `intervals`, a list `statements: [{name, parameters}]`, run in that order
    before its rows are written;
  - a derivation with **no function** and a `statements` list in its declaration
    is **SQL-only**: the loader runs the named statements in order, binding
    `:since` and `:now` (the run's), and nothing of JavaScript is involved.
- A derivation's declaration names what it may run (`outputs.statements`); a
  statement it did not declare refuses the run, as an undeclared table does
  today.
- Everything else of a derivation stays: triggers (`after_passes`,
  `after_push`, `cadence_seconds`), one transaction, "a run that fails writes
  nothing", the availability outcomes.

### 2. Declared relations: views and functions

- A plugin's manifest lists relation files: `"relations": ["pf.relations.sql"]`.
  A file holds `CREATE OR REPLACE VIEW` and `CREATE OR REPLACE FUNCTION`
  statements and nothing else; every object created is in the plugin's own
  schema (`plugin_pf.classified_flow`). Anything else in the file refuses the
  plugin (`relation_refused`).
- The loader runs the file at every start, after the plugin's tables are
  ensured, under the plugin's role, which has CREATE on its own schema and
  nowhere else. A view's digest is recorded; an unchanged file does nothing.
- The read role is granted SELECT on the views as on the tables, so any plugin
  and the data catalogue read them by their qualified name. A view is a dataset
  of the catalogue when its columns carry declared roles; otherwise it is only
  readable by SQL.
- A function is SQL or PL/pgSQL, `STABLE` or `IMMUTABLE`; a `VOLATILE` function
  or one with `SECURITY DEFINER` is refused.

### 3. Declared dependencies

- `"requires": ["pf"]` names the plugins whose schema this plugin's SQL reads.
  The loader loads plugins in dependency order (built-ins first, then by
  `requires`, a cycle refusing both). A required plugin that is absent or
  refused gives the dependent plugin the availability state
  `requires_missing` naming it, and the dependent plugin's derivations and
  widgets do not run; nothing else of the installation is affected.
- A relation of plugin A read by plugin B is A's contract: renaming a column of
  it is a breaking change of A, stated in A's changelog. opnview does not
  version relations beyond that.

### 4. Derivations may follow derivations

- `after_derivations: ["pf/classify"]` is a fourth trigger: the derivation runs
  after the named derivation of a required plugin has run successfully. The
  existing `plugin_pass` notification carries it; no new channel.

### Vocabulary

`statement`, `view`, `function`, `role` and `schema` are PostgreSQL's words.
`derivation` and `requires` are opnview's own, chosen in ADR 0017's audit and
here because neither PostgreSQL nor OPNsense names the thing.

## Consequences

- **What is built in Go**, all inside the loader and the plugin-table store
  (ADR 0002 allows it): the per-plugin role and its grants; the statement file
  parsing reusing the widget statements' registry; the `EXPLAIN` check at load;
  the SQL-only derivation; `statements` in a derivation's outputs; the relation
  file runner and its checks; `requires` and the load order;
  `after_derivations`. Estimate: 600 to 900 lines, plus the harness: `t.derive
  (name)` runs a derivation in a test and `t.sql` reads the result.
- **A worked example**, `docs/examples/example_derived_sql`, with no JavaScript
  at all: a table, a SQL-only derivation on a cadence, a view another example
  reads through `requires`, and its tests. It is the proof of ADR 0001 for this
  primitive.
- **Then** the first MOVE of ADR 0017: `internal/store/flows` (classification,
  pairing, This-Firewall) becomes the pf plugin's relations and SQL-only
  derivations, which is the step "(c) pf first".
- `docs/plugins.md` gains *Statements*, *Relations* and *Requires* sections and
  amends *Derivations*. The plugin API version goes to 3; a version-2 plugin
  still loads, since nothing is removed.
- Not decided here: a plugin-declared HTTP surface, which the three CRUD
  surfaces (interfaces, labels, people) need to move. That is the next ADR.
