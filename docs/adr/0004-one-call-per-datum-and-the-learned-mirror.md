# 0004 — One call per datum, and the learned mirror

Status: Accepted
Decided by: the maintainer, 11 October 2026 (architecture target, sections "broker" and "learned mirror")

## Context

Many plugins want the same data from the firewall at different freshness. Ten
plugins must not cost ten calls, and a plugin must not be written against a
hand-maintained Go model of each OPNsense endpoint: the core learns the API by
reading it.

## Decision

**The broker.** A plugin names a declared endpoint and the maximum age it
accepts (`host.call(name, {…, max_age_seconds})`). The broker answers from the
mirror when it is fresh enough, otherwise asks the scheduler for one call. For
one datum (an endpoint and its identifying parameters) it never makes two calls
at once and hands the same answer to everyone waiting. Every answer says where it
came from: `served_from` (`mirror` or `firewall`), `fetched_at`, `age_seconds`.
A failure (`not_found`, `forbidden`, …) counts for freshness without overwriting
data.

**The learned mirror.** On the first call of an endpoint the broker observes the
request and the response, infers a schema and stores the response in the
PostgreSQL schema `opnsense`. The plugin receives an **observed object**: every
field it reads is recorded; a field no loaded plugin reads is not stored. When a
plugin reads a new field, the column appears and fills from the next call; until
then the broker fetches the value from the firewall so that no plugin ever sees
a field missing because of the mirror.

- Inference covers: types (IP, MAC, timestamp, number, text, boolean), nature
  (snapshot or record stream), identity key (MAC, IP or id, never a nullable
  field), cursor (timestamp or monotonic id), parameters (window, page, other).
- The manifest corrects inference and always wins over it.
- **Lossless**: a field served from the mirror is byte-identical to the
  firewall's answer (leading zeros, `~`, `null` against absent, integers past
  2^53, row order). The inferred type serves the catalogue, never what the
  plugin reads.
- **Verified, never believed**: a recognised window parameter is checked on the
  rows returned; if not honoured it is marked `not_honoured` and the datum is
  read whole and filtered on the cursor.
- **Gaps, never zeros**: a response whose oldest row is newer than the last
  stored one records a gap; a failed call is an interval without an answer.
- **Evolves, never rebuilds**: new field, new column; type conflict, the column
  becomes JSONB; every change is a dated schema version with its cause.

## Consequences

- A record stream is read only after the cursor the plugin holds, with its gaps.
- The learned API catalogue (`host.catalogue()`, and JSON) describes what the
  mirror has seen: endpoints, parameters and roles, nature, key, cursor, schema
  versions, cost, freshness floor, streams, reading plugins, mirror hit rate,
  refusals. It never contains a field value, a credential or a URL.
