# 0007 — The core calls the firewall and nothing else

Status: Accepted
Decided by: the maintainer, step 1 (September 2026); amended 5A (Public Suffix List) and 11 October 2026 (downloads belong to plugins)

## Context

A monitoring tool that phones home, checks for updates or loads a script from a
CDN cannot be trusted on a segmented network. The outbound traffic of opnview
must be enumerable by its operator.

## Decision

- **The Go core makes network requests to the firewall's API and to nothing
  else.** No telemetry, no version check, no resource loaded from a CDN.
- Downloads from the Internet belong to the plugins that need them: the MaxMind
  GeoLite2 databases to the `maxmind` plugin, the Public Suffix List to the
  `publicsuffix` plugin. A third-party plugin's downloads are declared in its
  manifest. These never take a place in the firewall's queue.
- The Public Suffix List is refreshed daily and fetched conditionally (an
  unchanged list costs a 304); the previous good copy stays in use when a
  download fails. A copy frozen into the binary would group every suffix added
  after the build wrongly, with no way to tell.
- The connection to the installation's own database is not an outbound call: it
  goes to the PostgreSQL the installer set up, over a Unix socket or the
  installation's private container network, and to no other host.
- Images pulled at install or update time are installation-time downloads, not
  calls the product makes.
- Push listeners (syslog, NetFlow) are inbound: they receive what the user
  configured the firewall to send, accept only the firewall's own addresses, and
  make no outbound call.
- TimescaleDB's telemetry is off, and opnview refuses to start if it is not.

## Consequences

- The README section "What leaves your installation" lists exactly these.
- A plugin that downloads is visible as such in its manifest; the host offers it
  a fetch primitive and no other network access.
