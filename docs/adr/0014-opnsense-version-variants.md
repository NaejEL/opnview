# 0014 — A plugin supports several OPNsense versions by declared ranges

Status: Proposed
Proposed by the orchestrator from the maintainer's architecture target of 11 October 2026; the version endpoints are not yet verified in the API survey

## Context

An OPNsense upgrade renames a route or a field. A plugin author must not write
`if (version)` throughout a script, and a plugin for an OPNsense plugin
(os-zenarmor, for instance) faces a version that differs from the core's.

## Decision

- The engine reads the firewall's version at each discovery and selects, per
  endpoint, the variant whose declared range contains it. A variant is **routes**
  (path, method, parameters, field renaming, adapter) or **another script** (a
  whole file for that range), or both.
- A version outside every declared range gives a named availability state,
  `firewall_version_unsupported`, never the nearest range.
- Everything is optional: a plugin that declares no variant has no version
  handling and runs as is everywhere.
- Endpoint names in the manifest are stable; the variants carry the
  differences. A field renaming presents the response to the script under the
  stable names, so the logic is written once.
- A firewall upgrade seen at discovery switches the variant while running.
  Overlapping ranges are refused at load.
- A plugin's `testdata/` holds recorded responses per variant; its JavaScript
  tests play them all and expect the same records.

## Consequences

- Before anything is built on this: the endpoints giving the OPNsense version
  and an installed plugin's version must be found in `opnsense/core` 26.7.3 and
  recorded in `docs/opnsense-api-survey.md`.
