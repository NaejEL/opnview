# opnview

Inter-interface visibility for a VLAN-segmented network behind an OPNsense
firewall.

OPNsense already collects the raw material — filter logs, NetFlow, DHCP
leases, DNS lookups, the Suricata event log — but never cross-references it.
`opnview` does, and answers five questions:

- which interface talks to which interface, and for how much traffic;
- what is blocked, by which rule, and who tried;
- what each client consumes, with site names instead of bare IPs;
- where the data goes, to which countries and which operators;
- which machine is behaving abnormally.

The widget everything else hangs off is the **interface × interface matrix**:
volume exchanged, allowed connections, blocked connections, matching rules. It
makes it obvious when an interface that is supposed to be isolated has started
talking to another one.

The user interface is not a fixed set of screens. You create **named canvases** and
fill each with the widgets you want, moving between them with tabs rather than
menus; a canvas can be exported to a file and imported into another
installation.

**One service, installed and updated by a single command, with no external
data store to provision.** Storage engines are embedded libraries, not servers
— see the storage rule in [ROADMAP.md](ROADMAP.md), under *Rules that apply to
every step*, for what that permits and forbids. Local SQLite, frontend served
by the service. Nothing is installed on the firewall and nothing is changed on
it: everything goes through its REST API, read-only.

## Status

**Under construction.** Nothing is usable yet.
Progress follows [ROADMAP.md](ROADMAP.md) — eight steps, validation after each
one. Step 1 is done: [docs/opnsense-api-survey.md](docs/opnsense-api-survey.md).
Current step: 5, correlation, classification and the matrix. Step 4 was validated
against a live OPNsense on 3 October 2026.

`opnview` now has its own accounts, and that is where credentials enter it.
On first start it creates its database and its encryption key, prints a
**one-time setup token** to the console, and serves four surfaces: create the
first account with that token, sign in, enter the firewall URL, the API key
and secret, the MaxMind account ID and licence key and the theme, and size
collection. There is no environment variable, no configuration file and no
`configure` command, and a credential changed in the interface takes effect on
the next collection pass without a restart. There are no canvases and no widgets
yet: those are steps 5 and 7.

With a MaxMind account ID and licence key, the GeoLite2 City and ASN databases
are downloaded and the public addresses the flows carry are placed against them;
the settings surface says where the databases stand. The map that shows them is
step 7.

The settings surface can fetch the certificate the firewall presents and put
its SHA-256 fingerprint in the field, so a default OPNsense, which serves its
API under a self-signed certificate, can be pinned without copying the
fingerprint by hand. Fetching stores nothing; the save beside it does.

The collection surface shows, for each kind of source the installation has, the
pair that governs its polling — how often, and how many records one request
asks for — beside the rate `opnview` has measured from its own database over the
last 24 hours, and the pair it would suggest from that rate. A suggestion is
never applied on its own: the operator fills the fields with it and saves, and
the new pair takes effect without a restart. Measuring contacts nothing.

Which implementation of a source is read is the operator's decision first and
the probe round's only where the operator left it: each one is `auto` by
default, and can be turned `on` or `off` in its kind's card on the collection
surface (see `provider` in [docs/data-model.md](docs/data-model.md)). A saved
selection is applied at once from what the last probe round found, without
contacting the firewall; before the first round of a start, it waits for that
round.

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
IoT machine reaching a command-and-control server, an exposed container
scanning the network, an exfiltration, DNS tunnelling.

Read the alerts with that in mind, or you will be looking for the wrong thing.

## What leaves your installation

Exactly two things, and nothing else:

1. the calls to the firewall API, on your local network — the credential check
   `opnview` makes when you enter or correct your API key is one of these, to
   the same firewall, read-only, and not a third thing;
2. the MaxMind GeoLite2 database download, which contacts a third-party server
   with your MaxMind account ID and licence key. It asks `download.maxmind.com`,
   which redirects the download to the storage host MaxMind names for it; the
   credentials go to the first and never to the second, and no other host is
   contacted. Without a licence key and an account ID, nothing is requested.
   Once a day `opnview` asks whether a newer build exists — a request MaxMind
   does not count against the 30 downloads a day a GeoLite account is allowed —
   and downloads only when one does. The databases are kept in `geoip/` in the
   data directory, and a replaced build is deleted at once.

No telemetry, no version check, no resource loaded from a CDN. The interface
loads nothing from anywhere: its stylesheet is served by `opnview` itself and
its fonts are the ones already on your machine.

## Limitations, stated up front

**The application only sees what crosses the router.** Two machines on the
same interface talking to each other are invisible: their traffic is switched
and never reaches the firewall. A hypervisor hosting its virtual machines
behind its own interface likewise hides all of their internal traffic. This is
not a defect, it is a property of the observation point.

**Domain attribution is a heuristic, always.** Correlating a DNS lookup with
the flow that follows it fails on client-side DNS caching, on shared CDNs where
a thousand domains sit behind one IP, and on DNS-over-TLS or DNS-over-HTTPS,
which bypasses the resolver entirely and leaves no lookup to correlate. An
attribution rate is displayed per client, and no domain is ever invented when
the correlation fails — you get the IP, the country and the operator instead.
There is no observed-name path to fall back on: see
[Site names](#site-names-are-inferred-not-observed).

**Alert coverage is per interface.** Suricata may be absent, installed but
stopped, or running on only some interfaces. Alerts exist for the interfaces it
monitors and for no others; the UI states per interface whether it is covered,
rather than showing an empty panel.

**A measured record rate is a lower bound.** The collection surface sizes
polling from the records `opnview` stored. A record that was lost — lines a
filter-log page no longer reached, an `eve.json` file rotated away, lookups the
resolver's ring buffer dropped — was never stored and is counted nowhere, so the
rate the firewall's sources really produce can only be higher. The gaps
`opnview` detected are shown beside the rate; the ones it could not detect are
not.

**Volumes per address pair are daily on the firewall.** OPNsense keeps
per-address-pair traffic at a one-day resolution, for 62 days. Shorter periods
are built from `opnview`'s own history, so the 1 h and 24 h views are only as
deep as the time it has been running.

**The encryption key is a file beside the database, and the limit is stated
rather than dressed up.** Your OPNsense API secret and your MaxMind licence key
are encrypted at rest with authenticated encryption; the key that opens them is
created on first start, in the data directory, with permissions denying group
and other. **This protects a database that is copied, backed up, sent by mistake
or committed by accident, because the key file does not travel with it. It does
not protect against someone who already has the machine, who reads both.** No
scheme can, while the service starts and collects without a human — which it
must, or collection stops at every reboot until somebody signs in. That trade
was taken deliberately.

Two consequences follow, and neither is a defect:

- **back up the key file with the database, or separately, but do not lose it.**
  Without it the stored credentials cannot be read and have to be entered again;
  the history in the database is untouched either way;
- **a forgotten password is recovered by presenting that same key file**, and by
  nothing else. Whoever holds it already holds the decryptable secrets, so
  requiring it grants an attacker nothing they did not have — and it returns the
  installation without deleting the database.

**Replacing a credential leaves the previous one in the write-ahead log until a
checkpoint, and that is said rather than glossed over.** A credential is stored as
one row, replaced in place, so no row holds the previous value. But while the
service is running, `opnview.db-wal` beside the database holds the previous
ciphertext as well as the current one. It is encrypted under the same key file, so
it discloses nothing to anyone who does not hold that file, and only a
**superseded** secret to anyone who holds both — the same people the limitation
above already names. Forcing a full checkpoint after every credential write would
close it and was weighed against the cost; it was not taken. If you rotate a
secret because it leaked, revoke it on the firewall as well, which is what makes
the old value worthless wherever a copy of it sits.

**The session cookie carries no `Secure` flag.** `opnview` serves plain HTTP
today: `Secure` would make signing in impossible, and there is no TLS story
before the packaging step. The cookie is `HttpOnly` and `SameSite=Lax`, every
mutating form carries a token that a different session's token does not satisfy,
and the session token itself is stored only as a digest — so a copy of the
database hands over no usable session. **On a network where somebody may be
reading your traffic, put `opnview` behind a reverse proxy that terminates TLS.**

## Data

The tool reconstructs per-client browsing history: that is its function, not a
side effect. It is complete by default. Configurable retention and aggregate
mode — volumes, interfaces, countries, operators, without domain names — are
options, not imposed guardrails.

In a workplace setting, informing the people concerned is a legal obligation
in most jurisdictions.

## Licence

[MIT](LICENSE).

This product includes GeoLite Data created by MaxMind, available from
<https://www.maxmind.com>. The databases are downloaded on your own MaxMind
account, under MaxMind's GeoLite End User License Agreement, and are never part
of this repository.
