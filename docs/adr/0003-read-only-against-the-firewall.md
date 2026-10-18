# 0003 — Read-only against the firewall, enforced by the broker

Status: Accepted
Decided by: the maintainer, step 1 (September 2026); the broker rule restated 11 October 2026

## Context

opnview observes a firewall it must never change. A firewall setting a source
needs (the resolver query log, Suricata's `eve.json`, a NetFlow export) is a
manual step the user performs on OPNsense, documented in the README; the product
never performs it. With plugins written by third parties, the guarantee cannot
rest on each author's good behaviour.

## Decision

- opnview uses the authenticated REST API and nothing else: no SSH, no file
  read on the host, nothing installed on OPNsense.
- The broker is the only path to the firewall, for plugins and for the core's
  own reads. It lets through **GET**, and **POST to a read command**: the fourth
  path element begins with `search`, `get`, `query` or `list`, or ends with
  `status`. **The list of known mutating commands is checked first**: it is what
  refuses `carp_status`, whose name ends in `status` but whose POST changes CARP
  state.
- Everything else is refused before a request exists, logged with the plugin's
  name and the code `write_refused`, and counted.
- The manifest is checked at load against the same rule; the broker checks every
  call, the core's included.
- A plugin never sees the API key, the base URL or an authorisation header. No
  host function returns one.
- The README recommends an API key with read rights on the OPNsense side, in
  addition to this rule.

## Consequences

- One named exception: the credential check on the settings page calls the
  firewall directly, because it tests credentials not yet saved, and its answer
  never enters the mirror.
- A new OPNsense command that mutates under a read-like name is added to the
  mutating list, with its source, before anything reads near it.
