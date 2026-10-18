# 0010 — Site names from the resolver only; degrade, never guess; the observation limit

Status: Accepted
Decided by: the maintainer, step 1 survey (September 2026) and step 5A (October 2026)

## Context

Suricata was planned as the primary source of site names. The step-1 API survey
established that it cannot be: OPNsense exposes no `dns` event type, and `tls`
and `http` events, though they can be written to `eve.json`, cannot be read back
through the API, nor received over syslog (`docs/opnsense-api-survey.md`, S13).
Suricata may also be absent, installed but stopped, or running on only some
interfaces.

## Decision

- **Site names are inferred from resolver lookups, unconditionally**, and from
  no other source. A site name is grouped under its registrable domain using the
  Public Suffix List (0007).
- **Degrade, never guess.** A source that is absent, stopped or partial is shown
  as such; the interface never fills the gap with a guess. Which interfaces
  Suricata's alerts cover is a fact about the data, documented, not recited by
  the interface.
- **The observation limit is documented, not printed.** The application sees
  only what crosses the router, so every figure is a lower bound. The README
  says so once; no screen repeats it.
- **Gaps are never zeros.** A time slot without data because nothing was
  collected is a gap; a slot that was covered and holds no event is a true zero.
  A dataset declares which it is (event or sample, 0011).
- **NetFlow volumes are a second measure**, `netflow`, stated on every figure and
  never summed with the logged one; `best_available` names, per window, the
  first declared measure whose rule covers it.
- **This firewall is never a client.** The firewall's own addresses, discovered
  at runtime, are excluded from the client side of every flow.

## Consequences

- A widget that cannot answer says so (an empty state with its reason), rather
  than drawing a flat line.
- The Suricata plugin provides IDS alerts only.
