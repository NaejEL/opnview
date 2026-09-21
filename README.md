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
one. Step 1 is done: [docs/opnsense-api-survey.md](docs/opnsense-api-survey.md).
Current step: 2, data model and SQLite schema.

## Data sources

Five, all read through the OPNsense REST API:

|Source|Feeds|Required|
|---|---|---|
|Filter logs|the matrix, blocked traffic|yes|
|Suricata `eve.json`|alerts|no|
|NetFlow / Insight|volumes per address pair, daily|yes|
|DHCP leases|hostnames and MACs|yes|
|Resolver DNS lookups|site names|yes|

The resolver may be Unbound or Dnsmasq, and the DHCP server likewise —
`opnview` detects which one is active. Suricata may be absent, installed but
stopped, or running on only some interfaces; the UI states what is covered and
what is not.

### Site names are inferred, not observed

`opnview` names sites by correlating a resolver lookup with the flow that
follows it towards the resolved address. That is an inference, and the
limitations section below says where it fails.

It was meant to be a fallback. Suricata's `tls` events carry the SNI and its
`dns` events the query, both alongside the real pre-NAT source address —
*observed* names, nothing guessed. The step-1 API survey established that
neither is available on OPNsense 26.7:

- there is no `dns` event type to enable — the IDS model exposes only `http`
  and `tls`;
- `tls` and `http` events can be written to `eve.json`, but the only API
  endpoint that reads that file returns alert records and silently discards
  every other event type, and the generic log endpoint cannot open a file not
  named `.log`.

Suricata therefore contributes alerts and nothing else. There is no second
method to switch between, so the UI does not pretend there is one: it says
that names are inferred, and shows the attribution rate. The evidence is in
[docs/opnsense-api-survey.md](docs/opnsense-api-survey.md).

For the heuristic to work at all the resolver has to be logging its queries.
Turning that on is a manual step — the procedure will be documented here, and
`opnview` never performs it for you: it only ever reads.

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

**Domain attribution is a heuristic, always.** Correlating a DNS lookup with
the flow that follows it fails on client-side DNS caching, on shared CDNs where
a thousand domains sit behind one IP, and on DNS-over-TLS or DNS-over-HTTPS,
which bypasses the resolver entirely and leaves no lookup to correlate. An
attribution rate is displayed per device, and no domain is ever invented when
the correlation fails — you get the IP, the country and the operator instead.
There is no observed-name path to fall back on: see
[Site names](#site-names-are-inferred-not-observed).

**Alert coverage is per interface.** Suricata may be absent, installed but
stopped, or running on only some interfaces. Alerts exist for the interfaces it
monitors and for no others; the UI states per segment whether it is covered,
rather than showing an empty panel.

**Volumes per address pair are daily on the firewall.** OPNsense keeps
per-address-pair traffic at a one-day resolution, for 62 days. Shorter periods
are built from `opnview`'s own history, so the 1 h and 24 h views are only as
deep as the time it has been running.

## Data

The tool reconstructs per-device browsing history: that is its function, not a
side effect. It is complete by default. Configurable retention and aggregate
mode — volumes, segments, countries, operators, without domain names — are
options, not imposed guardrails.

In a workplace setting, informing the people concerned is a legal obligation
in most jurisdictions.

## Licence

[MIT](LICENSE).
