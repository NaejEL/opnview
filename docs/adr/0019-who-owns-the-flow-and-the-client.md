# 0019 — Who owns the flow, the client and the classification

Status: Accepted
Proposed by the orchestrator, 18 October 2026, after reading `internal/store/flows` for step (c) of ADR 0017; accepted by the maintainer the same day

## Context

ADR 0017 says "pf first": move `internal/store/flows` (2 900 lines of Go, 800
of SQL) into the pf plugin as relations and SQL-only derivations. Reading the
code before cutting shows that the sentence names the wrong owner for two of its
three parts, and that doing it as written would break the rule of ADR 0018 that
a plugin writes in its own schema alone.

**What `flows` actually holds, and who it belongs to:**

| Part | Lines | What it is | Writes | Natural owner |
|---|---|---|---|---|
| `write.go` | 1 086 | The row writers of every core kind: `flow`, `dhcp_lease`, `dns_resolution`, `security_event`, `measurement_sample`, `state_snapshot`, plus providers, interfaces, rules, clients | every core table | the kinds' sources |
| `pair.go`, `pairchunk.go` | 517 + 98 SQL | The two legs of one pf connection: `out` record paired with its only `in` candidate when each is the other's only candidate; `not_paired` for an unpaired upstream `out` sourced by this firewall | `flow.pair_outcome`, `flow.paired_flow_id` | **pf**: the rule cites pf.conf(5) and OPNsense's NAT order; no other source has legs |
| `classify.go` | 600 + 616 SQL | Which interface an address belongs to and which client stands behind it: lease > client identity > operator or detected network (longest prefix) > most recent sighting; this-firewall first; an incremental fingerprint per address; minting of address-level clients | `flow.src_*`/`dst_*` end columns, `client` (minting and purge), `address_classification`, `dns_resolution` and `security_event` ends | **not pf**: the evidence comes from leases (kea, dnsmasq, isc), neighbour tables, discovery's interface networks and the firewall's own addresses; the flows are only the sightings and the rows being placed |
| `thisfirewall.go` | 160 + 37 SQL | Whether an address was this firewall's at an instant, from `interface_address` | reads only | discovery (the installation model) |
| `network.go` | 408 | The operator's and the detected interface networks (decision D1) | `interface_network` | the installation model |
| `ruleinfo.go` | 88 + 51 SQL | The Suricata rule-info cache | `provider_rule_info` | suricata |

**What the plugins read**, by qualified name once tables leave the core: `flow`
41 files, `client` 60, `interface` 76, `rule` 44, `provider` 38,
`classified_flow` 7, `blocked_decision` 6.

**The blocker.** Classification writes into three owners' tables at once: the
flow's end columns (pf's), `client` (the installation's, also written by the
lease coordinator's identity cascade), and the ends of `dns_resolution` and
`security_event` (unbound's and suricata's). ADR 0018 forbids a plugin to
write another's schema, and that is right: it is what makes a plugin's writes
auditable. So either classification stays in Go, which ADR 0002 forbids, or a
plugin can be **granted** a write on a named table and named columns of
another plugin, by that plugin's declaration, enforced by PostgreSQL.

## Decision

### 1. One small extension of ADR 0018: granted writes

A table declaration may name who else may write it, and on which columns:

```json
{"name": "flow", "columns": [...],
 "writable_by": [{"plugin": "clients", "columns": ["src_interface_id", "src_client_id", "src_is_this_firewall",
                                                   "dst_interface_id", "dst_client_id", "dst_is_this_firewall",
                                                   "traffic_scope"]}]}
```

The loader turns it into `GRANT UPDATE (columns) ON plugin_pf.flow TO
opnview_plugin_clients` (and `INSERT`/`DELETE` only when `columns` is absent,
which grants the whole table). PostgreSQL's column-level privileges enforce
it; a statement of the granted plugin is planned under its role at load as
every statement is. Nothing else of ADR 0018 changes. The grant is the owner's
declaration, in the owner's manifest: pf says who may classify its flows.

### 2. Three built-in plugins where the core had one package

- **`installation`**: the model of what the installation is. Tables `interface`,
  `interface_address`, `interface_map`, `interface_network`, `rule`, `client`,
  `owner`; the view `this_firewall_address`; the interface-network edits
  (today `network.go`). Written by discovery (core Go, until discovery itself
  moves) and, by grant, by the plugins that mint or update clients.
- **`pf`** (existing): gains the table `flow` with its classification and
  pairing columns and its indexes, the relations `classified_flow` and
  `blocked_event`, and the SQL-only derivation `pair`, after each
  `firewall_log` pass. It grants the `clients` plugin the end columns.
- **`clients`** (new): the classification, as SQL-only derivations after the
  passes of `firewall_log`, `dhcp_lease`, `neighbour`, `dns_lookup` and after
  `pf/pair`. Its own table `address_classification`. Granted: the end columns
  of `pf.flow`, `installation.client` (insert, update, delete of the
  address-level identities it mints and purges), and the end columns of
  unbound's `dns_resolution` and suricata's `security_event` once those move.

The lease sources (kea, dnsmasq, isc) share one kind and one table,
`dhcp_lease`: a fourth built-in, **`leases`**, owns it and grants the three
their inserts. The same shape serves `dns_resolution` (unbound), `security_event`
(suricata) and `measurement_sample` (insight), each owned by its single source.

### 3. The classification is rewritten set-based, in SQL, with the same result

The Go loop (one address at a time, one statement per step) becomes one
SQL-only derivation of five statements over every address that changed since
the run's `:since`: place (the four evidences in order, longest prefix with
`inet` operators, a tie to the lowest interface id), mint (one `INSERT ... ON
CONFLICT` for the addresses whose flow end has no client at all, in address
order, so the ids are the ones the loop would have minted), set the ends of
flows, events and lookups, record the fingerprints, purge the unreferenced
clients. The properties the Go states and tests stated hold by construction:
the result depends on the evidence held and not on the order of arrival, a
run over unchanged evidence writes nothing. The pairing becomes two statements:
candidates counted with window functions, a pair where both counts are one.

### 4. How it is validated: a diff on a copy of the live database

Before the cut, `pg_dump` of the maintainer's database. The old Go classifies
and pairs it; the new plugins classify and pair a second copy from the raw
rows. The two copies are compared row by row on `flow`'s end and pairing
columns and on `client`. A difference is a defect of the rewrite until
explained. Only then does the change reach the running service, and `main`
after the maintainer has looked.

## Consequences

- `internal/store/flows` is deleted entirely, `collect/derive`'s sequence
  becomes `after_derivations` chains, and the four core-kind coordinators lose
  their row writers to the plugins' push and pass outputs (step (c) of ADR
  0017 continues with them, one at a time).
- Every plugin SQL file names the tables by their qualified names
  (`plugin_pf.flow`, `plugin_installation.client`): a mechanical rename in one
  commit, covered by the plugins' own tests and the widget goldens.
- The covering indexes of `flow` move with the table, declared in pf's
  manifest, so the widgets' index-only plans are kept.
- **Size and order of work**, honestly: the grant extension (one session),
  `installation` and `pf.flow` with the rename (one session), the pairing in
  SQL (one session), the classification in SQL and the diff validation (two to
  three sessions), then `leases`, unbound's and suricata's tables. Each one a
  commit on the `wip/` branch, the live diff before anything reaches `main`.
- Not decided here: discovery, which writes the installation model from the
  firewall and stays core Go until the kind that reads
  `interfaces_info` is a plugin (ADR 0017, MOVE).
