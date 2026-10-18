# Architecture decision records

One page per decision, numbered, never rewritten: a decision that changes gets
a new record that supersedes the old one. A record states who decided it and
when. **Decided by the maintainer** is final. **Proposed** was written by the
orchestrator from the maintainer's documents and is not binding until the
maintainer marks it accepted.

A change to this repository starts from an accepted record, names it in its
commit message, and ends with `docker compose run --rm checks` green and the
change validated on the maintainer's firewall. There are no specifications.

| # | Title | Status |
|---|---|---|
| [0001](0001-everything-is-a-plugin.md) | Everything is a plugin: a directory of JSON, JavaScript and SQL | Accepted |
| [0002](0002-the-go-core.md) | The Go core is the broker, the plugin loader and the scheduler | Accepted |
| [0003](0003-read-only-against-the-firewall.md) | Read-only against the firewall, enforced by the broker | Accepted |
| [0004](0004-one-call-per-datum-and-the-learned-mirror.md) | One call per datum, and the learned mirror | Accepted |
| [0005](0005-the-scheduler.md) | The scheduler: nearest deadline first, a real-time reserve | Accepted, defaults proposed |
| [0006](0006-configuration-accounts-and-secrets.md) | Zero hardcoded configuration, own accounts, encrypted secrets | Accepted |
| [0007](0007-outbound-calls.md) | The core calls the firewall and nothing else | Accepted |
| [0008](0008-postgresql-timescaledb.md) | PostgreSQL with TimescaleDB, pinned, Apache edition, one schema per plugin | Accepted |
| [0009](0009-vocabulary-and-language.md) | The vocabulary is OPNsense's; everything is written in English | Accepted |
| [0010](0010-what-the-data-can-and-cannot-say.md) | Site names from the resolver only; degrade, never guess; the observation limit | Accepted |
| [0011](0011-the-data-catalogue.md) | Every table is a dataset; queries are compiled server-side | Accepted, bounds proposed |
| [0012](0012-one-test-command.md) | One test command, under a minute, and no other gate | Accepted |
| [0013](0013-decisions-not-specifications.md) | Decisions, not specifications; no self-approved work | Accepted |
| [0014](0014-opnsense-version-variants.md) | A plugin supports several OPNsense versions by declared ranges | Proposed |
| [0015](0015-streams.md) | A stream is opened once and shared | Proposed |
| [0016](0016-the-front.md) | Named canvases, widgets as plugins, themes and languages as files | Accepted |
| [0017](0017-core-audit.md) | The package-by-package verdict on the existing Go | Accepted |
| [0018](0018-the-write-primitive.md) | The write primitive: a plugin owns its tables, views and writes in SQL | Accepted |
| [0019](0019-who-owns-the-flow-and-the-client.md) | Who owns the flow, the client and the classification; granted writes | Accepted |

## Template

```
# NNNN — Title

Status: Proposed | Accepted | Superseded by NNNN
Decided by: the maintainer, <date> | Proposed by the orchestrator, <date>

## Context
Two to six sentences: the situation and what forced a decision.

## Decision
What is decided, in the imperative. The rule, its exceptions, who may change it.

## Consequences
What follows, what it forbids, what it costs. What a reader must check.
```
