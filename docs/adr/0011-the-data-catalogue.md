# 0011 — Every table is a dataset; queries are compiled server-side

Status: Accepted; the bounds are Proposed
Decided by: the maintainer, 11 October 2026 (architecture target, section "data catalogue"); bounds proposed by the orchestrator

## Context

"Any data, any widget": an advanced user must be able to plug a generic chart on
any dataset a plugin, the core or the mirror exposes, without the plugin author
having foreseen it, and without the browser ever sending SQL.

## Decision

- `GET /api/datasets` lists every dataset the core and each plugin expose: name,
  origin, time column, retention horizon, freshness, nature (**event**: a covered
  slot without a row is a true zero; **sample**: it is a gap), and for each
  column its type, role (`time`, `dimension`, `measure`), unit and admissible
  aggregations. Every plugin table and every mirror table is a dataset
  automatically.
- `GET /api/datasets/{name}/query` takes a period, an `x` (the time column with
  a bucket, or a dimension), a `y` (`sum`, `mean`, `min`, `max`, `last`,
  `count` of a measure), filters (`eq`, `ne`, `in`, `lt`, `le`, `gt`, `ge`,
  `is_null`, …), a `split_by` with a limit and a "rest" entry.
- The server compiles the request into **one parameterised `SELECT`**.
  Identifiers come only from the dataset's declaration; values are always bound.
  **No SQL text is accepted from the browser.** The query runs under a role that
  has `SELECT` only, no right on accounts, sessions, encrypted credentials or
  secret settings, in a read-only transaction. A query that would read a
  forbidden table is refused at load.
- Querying a dataset of the mirror renews the scheduler's interest in it (0005).
- The `custom_chart` widget accepts any dataset.

### Proposed bounds, each a documented constant

| Bound | Value |
|---|---|
| slots per series | 1 000 |
| series plus the rest | 20 |
| rows for a dimension `x` | 1 000 |
| query timeout | 5 s |

## Consequences

- A plugin declares its tables once, in its manifest, and gets a queryable
  dataset without writing a query API.
- The widget plugins' own statements remain theirs; the catalogue is the
  generic path beside them.
