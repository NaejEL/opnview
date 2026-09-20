# opnview

Inter-segment visibility for a VLAN-segmented network behind an OPNsense
firewall.

OPNsense already collects the raw material — filter logs, NetFlow, DHCP
leases, DNS lookups, the Suricata event log — but never cross-references it.
`opnview` does, and answers five questions:

- which segment talks to which segment, and for how much traffic;
- what is blocked, by which rule, and who tried;
- what each device consumes, with site names instead of bare IPs;
- where the data goes, to which countries and which operators;
- which machine is behaving abnormally.

The central screen is a **segment × segment matrix**: volume exchanged,
allowed connections, blocked connections, matching rules. It makes it obvious
when a segment that is supposed to be isolated has started talking to another
one.

Single Go binary, local SQLite, frontend served by the binary. Nothing is
installed on the firewall and nothing is changed on it: everything goes
through its REST API, read-only.

## Status

**Under construction.** Nothing is usable yet.
Progress follows [ROADMAP.md](ROADMAP.md) — eight steps, validation after each
one. Current step: 1, OPNsense API exploration.

## Data sources

Five, all read through the OPNsense REST API:

|Source|Feeds|Required|
|---|---|---|
|Filter logs|the matrix, blocked traffic|yes|
|Suricata `eve.json`|site names (primary), alerts|no|
|NetFlow / Insight|volumes per address pair|yes|
|DHCP leases|hostnames and MACs|yes|
|Resolver DNS lookups|site names (fallback)|yes|

The resolver may be Unbound or Dnsmasq, and the DHCP server likewise —
`opnview` detects which one is active. Suricata may be absent, installed but
stopped, or running on only some interfaces; the UI states what is covered and
what is not.

### Site names: observed, or inferred

With **Suricata** running in detection mode on the internal interfaces, site
names are *observed*: its `tls` events carry the SNI and its `dns` events
carry the query, both alongside the real pre-NAT source address. Nothing is
guessed.

Suricata logs only alerts by default. Enabling the `dns`, `tls` and `http`
event types is a manual step — the procedure will be documented here, and
`opnview` never performs it for you: it only ever reads.

Without Suricata, `opnview` falls back to a *heuristic*: correlating a
resolver lookup with the flow that follows it towards the resolved address.
Each attributed name records which of the two methods produced it, and the UI
shows the active one.

## Alerts — what an internal IDS is actually for

An IDS on internal interfaces, behind a router that is itself behind CGNAT,
is not there to repel attacks coming from the Internet: almost none arrive.
It is there to **detect a compromised machine inside your own network** — an
IoT device reaching a command-and-control server, an exposed container
scanning the network, an exfiltration, DNS tunnelling.

Read the alerts with that in mind, or you will be looking for the wrong thing.

## What leaves your installation

Exactly two things, and nothing else:

1. the calls to the firewall API, on your local network;
2. the MaxMind GeoLite2 database download, which contacts a third-party server
   with your licence key.

No telemetry, no version check, no resource loaded from a CDN.

## Limitations, stated up front

**The application only sees what crosses the router.** Two machines on the
same segment talking to each other are invisible: their traffic is switched
and never reaches the firewall. A hypervisor hosting its virtual machines
inside its own segment likewise hides all of their internal traffic. This is
not a defect, it is a property of the observation point.

**Without Suricata, domain attribution is a heuristic.** Correlating a DNS
lookup with the flow that follows it fails on client-side DNS caching, on
shared CDNs where a thousand domains sit behind one IP, and on encrypted DNS
that bypasses the resolver. Where the fallback is in use, an attribution rate
is displayed, and no domain is ever invented when the correlation fails — you
get the IP, the country and the operator instead. With Suricata covering an
interface, this limitation does not apply to that traffic.

**Suricata coverage is per interface.** An installation where only some
interfaces are monitored produces observed names for those and inferred names
for the rest. The UI distinguishes them rather than blending them.

## Data

The tool reconstructs per-device browsing history: that is its function, not a
side effect. It is complete by default. Configurable retention and aggregate
mode — volumes, segments, countries, operators, without domain names — are
options, not imposed guardrails.

In a workplace setting, informing the people concerned is a legal obligation
in most jurisdictions.

## Licence

[MIT](LICENSE).
