# OPNsense REST API survey

Findings document for step 1 of `ROADMAP.md`. For each of the five data sources `opnview` depends on: which REST endpoints exist, what they
return, how long the firewall keeps the data, how often it can be polled, and how the application detects unavailability.

## Scope and method

**Pinned version.** This survey is written for **OPNsense 26.7.3**, series 26.7 "Xenial Xenops" (FreeBSD 15.1, OpenSSL 3.5, PHP 8.5 —
https://docs.opnsense.org/releases/CE_26.7.html). Repository citations point at the `26.7.3` tag of `opnsense/core`; claims known to differ
on earlier series are flagged inline.

**Admissible sources.** Two kinds of evidence and nothing else: the documentation on `docs.opnsense.org`, including its auto-generated core
API reference and the release notes; and the model XML, controller PHP, templates and backend Python scripts in the official `opnsense/core`
repository. The generated reference lists endpoint paths and their likely verb but documents neither request parameters nor response shapes,
so those were read from the controller and backend sources, the authoritative wire contract.

**Honesty rule.** Anything that could not be confirmed against an official source is marked with the greppable marker `UNVERIFIED:` rather
than asserted — a hypothesis for step 4 to confirm empirically, not a fact this document stands behind.

**Research is not an outbound call**, and nothing is hardcoded. Reading OPNsense documentation while writing this document is authoring
activity, not something the binary does; the three outbound calls the roadmap allows are unaffected. Endpoint paths and response keys appear
below because they are the API contract; interface names, descriptions, subnets, VLAN tags and addresses appear nowhere — they are
per-installation values discovered at runtime (see *Runtime discovery*), never embedded.

## Authentication and conventions

**Authentication.** HTTP Basic with an API key/secret pair (key as username, secret as password), created per user under System > Access >
Users; the secret is downloadable once. Requests are stateless and the URI is checked against the key owner's ACL, so the key must belong to
a least-privilege user whose privileges cover every endpoint `opnview` calls. `X-CSRFToken` applies only to GUI session calls.

**URL shape.** `{base}/api/<module>/<controller>/<command>[/<p1>/<p2>…]`, `{base}` being the firewall's HTTPS URL. Segments after the
command are passed positionally, and the router camelises each, so `search_rule` and `searchRule` reach the same handler. Since the 25.7
series the default ACLs spell the URLs `snake_case`; `opnview` uses that spelling.

**GET versus POST, and the read-only guarantee.** There is no framework-level method routing: any verb reaches the same handler, and write
actions self-gate on `isPost()`. A `POST` therefore does **not** imply a write — the search and get helpers contain no write path at all.
Every `POST` endpoint `opnview` uses is listed here, and each is a **read-only search or query call**:

| POST endpoint used by `opnview` | Why POST | Writes anything? | Citation |
|---|---|---|---|
| `/api/firewall/filter/search_rule` | grid search; pagination read from the body | No — search only | https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Firewall/Api/FilterController.php |
| `/api/kea/leases4/search` | grid search | No — reads the running Kea daemon | https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Kea/Api/Leases4Controller.php |
| `/api/dnsmasq/leases/search` | grid search | No — reads the lease file | https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Dnsmasq/Api/LeasesController.php |
| `/api/ids/service/query_alerts` | gated on `isPost()`, but only runs a read query | No — reads `eve.json` | https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/IDS/Api/ServiceController.php |
| `/api/diagnostics/log/<module>/<scope>` | grid search over a log file | No — reads syslog files | https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Diagnostics/Api/LogController.php |
| `/api/unbound/overview/search_queries` | needs a JSON body carrying **integer** `timeStart`/`timeEnd` (see below and data source 5) | No — reads the query-report database | https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Unbound/Api/OverviewController.php |

Each row is also documented in full in the data-source section that uses it. No other `POST` endpoint is called. `opnview` never calls `set`,
`add`, `del`, `toggle`, `reconfigure`, `start`, `stop`, `restart`, `clear`, `drop_alert_log`, `del_lease` or any other mutating command —
the enforceable form of the read-only rule in `ROADMAP.md`.

**Pagination envelope.** Grid searches take `current` (1-based page), `rowCount` (page size; `-1` means all rows), `searchPhrase` and
`sort`, and return `{"total": …, "rowCount": …, "current": …, "rows": [ … ]}`, `total` being the count after filtering. `opnview` sends
these in a form-encoded or JSON POST body, which both search helpers accept.

**Parameter types depend on the body encoding.** `Mvc\Request::get()` reads `$_REQUEST` uncast, so values from a query string or a
form-encoded body are always PHP **strings**; native integers reach `$_REQUEST` only through `ApiControllerBase::parseJsonBodyData()`, which
merges a decoded `Content-Type: application/json` body into `$_POST` and `$_REQUEST`. A controller validating a parameter with `is_int()`
therefore rejects it unless it came in a JSON body — see data source 5, where this decides whether the resolver query is windowed or
silently truncated.

**Status codes and error bodies.** 200 on success **and** on most wrong-verb write failures (`{"result":"failed", …}`); 400 `Invalid JSON
syntax`; 401 `Authentication Failed`; 403 `Forbidden` when the ACL denies the URI; 404 `{"errorMessage":"Endpoint not found", …}` for an
unknown module, controller or command; 500 for an uncaught error. **HTTP 405 is never emitted**, and model validation failures come back as
HTTP 200 with `{"result":"failed","validations":{…}}` — HTTP 200 is not proof of success. A 404 on a module endpoint is the normal signal
that an optional component is not installed: `opnview` uses it to separate "absent" from "present but disabled", never to conclude "no
data".

Sources: https://docs.opnsense.org/development/api.html · https://docs.opnsense.org/development/how-tos/api.html ·
https://docs.opnsense.org/releases/CE_25.7.html ·
https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Base/ApiControllerBase.php ·
https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Base/ApiMutableModelControllerBase.php ·
https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/library/OPNsense/Mvc/Router.php ·
https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/library/OPNsense/Mvc/Request.php ·
https://github.com/opnsense/core/blob/26.7.3/src/opnsense/www/api.php

## Data source 1 — Filter logs

### Endpoints

| Path | Method | Parameters | Citation |
|---|---|---|---|
| `/api/diagnostics/firewall/log` | GET | `limit` (default 1000), `digest` (default `""`) | https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Diagnostics/Api/FirewallController.php |
| `/api/diagnostics/firewall/log_filters` | GET | none | https://docs.opnsense.org/development/api/core/diagnostics.html |
| `/api/diagnostics/firewall/stream_log` | GET | none — Server-Sent Events | https://docs.opnsense.org/development/api/core/diagnostics.html |

`log` returns a JSON **array**, not a paginated envelope, newest record first. `digest` is the MD5 of a previously seen raw log line and the
only incremental primitive: the backend walks backwards from the end of the log and stops at that digest. There is no offset, cursor or page
parameter.

### Response shape

Fields are positional in the `filterlog` format and grow with IP version and protocol. Always present: `rulenr`, `subrulenr`, `anchorname`,
`rid`, `interface`, `reason`, `action`, `dir`, `ipversion`, plus the metadata keys `__digest__`, `__host__`, `__timestamp__`, `__spec__` and
the resolved `label`. IPv4 records add `tos`, `ecn`, `ttl`, `id`, `offset`, `ipflags`, `protonum`, `protoname`, `length`, `src`, `dst`; IPv6
records add `class`, `flow`, `hoplimit`, `protoname`, `protonum`, `length`, `src`, `dst`. TCP and UDP records add `srcport`, `dstport`,
`datalen`, TCP also `tcpflags`, `seq`, `ack`, `urp`, `tcpopts`.

| Required field | API field | Note |
|---|---|---|
| interface | `interface` | raw device name, **not** the user description — join via runtime discovery |
| action | `action` | pass, block, or a translation action |
| rule identity | `rid` + `label` | `rid` is the pf label; `label` is the description resolved from the running ruleset, empty if the rule is gone |
| source address | `src` | |
| destination address | `dst` | |
| source port | `srcport` | TCP/UDP records only |
| destination port | `dstport` | TCP/UDP records only |
| protocol | `protoname` / `protonum` | |
| timestamp | `__timestamp__` | **string, not epoch**; depending on the syslog format in use, either `"Mon dd HH:MM:SS"` (no year, no zone) or ISO with the UTC offset stripped |

All nine required fields are returned. Two consequences for step 4: the timestamp must be normalised and a year inferred on ingest, and
`interface` must be translated through the device-to-description map before it can name a segment. `log_filters` returns `{"action": […],
"interface_name": […], "dir": […]}`, read from the configuration, not from the log.

### Retention on the firewall

Plain-text syslog-ng files at `/var/log/filter/filter_<YYYYMMDD>.log`, one per day, governed by System > Settings > Logging: *Maximum
preserved files* (days, or file count when *Maximum file size* is used); local logging can also be disabled outright. `opnview` must keep
its own history and cannot backfill beyond what is on the firewall's disk at first run.

### Sustainable polling frequency

**10 seconds**, with `digest`. Each poll is bounded by `limit` (1000 by default; `limit=0` is silently coerced back to 1000), so its cost is
small. The risk is the other way: a busy ruleset can produce more than `limit` lines between two polls and the API offers no way to recover
a gap, so a short interval with a generous `limit` keeps the digest inside the returned window. Two quirks: the record matching the supplied
digest is **included** and must be deduplicated on `__digest__`; if that digest does **not** appear at all, a gap occurred and is recorded
as such. `stream_log` is unsuitable as the primary path — throttled, and closed by the server after about 60 seconds.

**Observation-point limit.** The filter log only records packets that traverse the firewall and match a rule with logging enabled. Traffic
between two devices inside the same segment never reaches the router and never appears here. Any screen built on these counts must say so.

### Which packets a logging rule writes, read for step 5

**A logging rule writes the packet that establishes a state, and not the rest of the connection.** FreeBSD's pf.conf(5) for 15.1-RELEASE,
which is the release OPNsense 26.7 ships on, under `log`: *"In addition to any action specified, log the packet. Only the packet that
establishes the state is logged, unless the no state option is specified."* and *"Use all to force logging of all packets for a connection.
This is not necessary when no state is explicitly specified."* (https://man.freebsd.org/cgi/man.cgi?query=pf.conf&sektion=5&manpath=FreeBSD+15.1-RELEASE).
OPNsense's per-rule option is the same flag: *"Log — Log packets that are handled by this rule"* (https://docs.opnsense.org/manual/firewall.html),
and the default block and default pass rules log or not according to *Firewall > Settings > Advanced* — *"Here the logging behaviour of the
default block/pass, automatic Source NAT as well as bogon and private network blocks can be adjusted. If disabled, only log directives from
your manual rules will be show in the firewall log."* (https://docs.opnsense.org/manual/firewall_settings.html, *Logging*).

So `length` on a pass record is the length of **one** packet, the one that created the state, and summing it gives **logged bytes**: a
lower bound of nothing in particular, never the volume of the conversation. A connection logged `in` on one interface and `out` on another
is two records. Every volume `opnview` computes from this source is named for what it is, and the data model says so beside each.

### The `reason` field's values, read for step 5

The `reason` field is written by OPNsense's `filterlog` daemon, which turns pf's numeric reason into a word through its own table
(https://github.com/opnsense/ports/blob/26.7.3/opnsense/filterlog/files/filterlog.c, `pf_reasons`), and `read_log.py` passes the field through
by position without mapping it. The table at 26.7.3 holds fifteen words, which are FreeBSD's `PFRES_NAMES`
(https://github.com/freebsd/freebsd-src/blob/releng/15.0/sys/netpfil/pf/pf.h) up to `synproxy`:

`match`, `bad-offset`, `fragment`, `short`, `normalize`, `memory`, `bad-timestamp`, `congestion`, `ip-option`, `proto-cksum`, `state-mismatch`,
`state-insert`, `state-limit`, `src-limit`, `synproxy`.

Any other reason — FreeBSD 15 adds `map-failed` (15) and `translate` (16), which filterlog's table does not carry — is written as
`unknown(<n>)`. `match` is a rule decision; every other word in the set is a packet pf dropped for a reason no rule expresses. A value outside
the set says nothing either way. The fixtures this repository records are synthesised and carry no real reason value, so the source is the
only evidence; **the set is to be confirmed against a live firewall at the validation of step 5**.

### Degradation

The endpoint returns HTTP 200 with `[]` when the log directory is absent or empty, so an empty array does **not** distinguish "local logging
disabled" from "no traffic matched a logging rule"; a backend failure or a wrong verb yields HTTP 200 with an *empty body*. Detection rules:
an empty or non-array body, or 401/403, means **unavailable** (backend, credentials or ACL); `[]` with a healthy body means **reachable but
silent**, surfaced as a warning state. The UI reports a distinct "filter log unavailable" state, and absence of records is never rendered as
an absence of traffic.

### Sources

Endpoint citations are in the table above. Additionally: https://docs.opnsense.org/manual/settingsmenu.html ·
https://github.com/opnsense/core/blob/26.7.3/src/opnsense/scripts/filter/read_log.py

## Data source 2 — Suricata `eve.json`

This section answers the open question of roadmap step 1; the answer is largely negative and is stated without softening.

### Endpoints

| Path | Method | Parameters | Citation |
|---|---|---|---|
| `/api/ids/service/query_alerts` | POST (read-only) | `rowCount` (default 9999), `current`, `searchPhrase`, `fileid` | https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/IDS/Api/ServiceController.php |
| `/api/ids/service/get_alert_info/<alertId>[/<fileid>]` | GET | `alertId` is the `filepos` byte offset | https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/IDS/Api/ServiceController.php |
| `/api/ids/service/get_alert_logs` | GET | none | https://github.com/opnsense/core/blob/26.7.3/src/opnsense/scripts/suricata/listAlertLogs.py |
| `/api/ids/service/status` | GET | none | https://docs.opnsense.org/development/api/core/ids.html |
| `/api/ids/settings/get` | GET | none | https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/IDS/Api/SettingsController.php |
| `/api/ids/settings/get_rule_info/<sid>` | GET | `sid` positional | https://docs.opnsense.org/development/api/core/ids.html |

`query_alerts` is the **only** API path that reads `/var/log/suricata/eve.json`. The generic log endpoint cannot reach it: its filename
resolver only ever builds candidate paths ending in `.log`.

### Response shape

`query_alerts` returns `{"filters": …, "rows": [ … ], "total_rows": N, "origin": "<log basename>"}`. Each row is the original `eve.json`
object plus `filepos` (byte offset — the only stable anchor), `fileid` (which rotated file it came from), `alert_sid` and `alert_action`.
The key `alert` is **overwritten** with the signature text: the nested alert object carrying category, severity and rule metadata is
destroyed by the backend. Surviving original keys include `timestamp`, `src_ip`, `src_port`, `dest_ip`, `dest_port`, `proto`, `in_iface`,
`flow_id`, `event_type`. `total_rows` counts records scanned so far. `get_alert_logs` returns one entry per rotated file with `filename`,
`size`, `modified` and `sequence` — how `fileid` values are enumerated.

### Retention on the firewall

`/var/log/suricata/eve.json` is rotated by `newsyslog` from an OPNsense template, on three parameters: archive count `AlertSaveLogs` (model
default **4**), a size trigger of **500000 KB** (about 0.51 GB per file, fixed in the template), and a time trigger `AlertLogrotate`, weekly
or daily. The firewall therefore holds at most the current file plus four archives — about **2.6 GB** at defaults; `opnview` must keep its
own history for alerts to outlive that window.

### Sustainable polling frequency

**60 seconds.** Alerts are low-volume and the endpoint re-reads the file backwards from the end on every call, so polling more often costs
without benefit. Because offsets are counted from the end of the file, any newly appended record shifts every offset, so
`current`/`rowCount` paging is **not** stable for resuming: the durable cursor is `(fileid, filepos)` — store the highest `filepos` seen per
`fileid`, read newest-first, stop at it.

### Degradation

| State | Detection |
|---|---|
| Suricata module absent or not permitted | `/api/ids/service/status` returns 404, 401 or 403 |
| Installed but not running | HTTP 200 with `status` of `stopped`, `disabled` or `unknown` |
| Running | HTTP 200 with `status` of `running` |

Which interfaces it covers comes from `/api/ids/settings/get` at `ids.general.interfaces`, an object keyed by interface identifier with
`{value, selected}` per entry; the covered set is the keys whose `selected` is truthy. `ids.general.enabled` gives the configured state
independently of the running state. An empty alert list is reported as "no alerts" only when `status` is `running`; when Suricata is absent
or stopped the Alerts screen says so instead of drawing an empty chart. Silence is never an absence of threats.

### Sources

Endpoint citations are in the table above. Additionally: https://docs.opnsense.org/manual/ips.html ·
https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Base/ApiMutableServiceControllerBase.php ·
https://github.com/opnsense/core/blob/26.7.3/src/opnsense/scripts/suricata/queryAlertLog.py ·
https://github.com/opnsense/core/blob/26.7.3/src/opnsense/service/templates/OPNsense/IDS/newsyslog.conf ·
https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/models/OPNsense/IDS/IDS.xml ·
https://github.com/opnsense/core/blob/26.7.3/src/opnsense/scripts/syslog/log_matcher.py

### (a) Reachability and incremental consumption

**`eve.json` is reachable through the API only as an alert feed, and incremental consumption is not natively supported: the application must
track the `(fileid, filepos)` byte offset itself and stop reading when it reaches the last offset it has already stored.**

Unpacking that: the only endpoint that opens `eve.json` is `/api/ids/service/query_alerts`, a paginated search rather than a streaming or
cursor endpoint. The backend returns a record **only if it carries a top-level `alert` key** — records of type `dns`, `tls`, `http`, `flow`,
`ssh` and `anomaly` are silently skipped, and no parameter changes that. There is no timestamp filter over HTTP: the controller hardcodes
the search phrase to a substring match over signature, action, source and destination address. And paging is offset-from-end-of-file.

The de-duplication strategy `opnview` must implement: read newest-first with a bounded `rowCount`; compute `(fileid, filepos)` for each row;
stop at the highest identity already persisted for that `fileid`; treat a rotation — observed through `get_alert_logs`, whose `sequence`
values shift — as a reset of the watermark for the affected files; deduplicate on that identity at insert time.

### (b) Enabling the `dns`, `tls` and `http` event types

**Answer up front: `http` and `tls` can be enabled from the web UI and the setting survives firmware upgrades. `dns` cannot be enabled at
all through any supported means — the IDS model has no `dns` field and the generated `suricata.yaml` has no `dns` output type. And even once
`http` and `tls` are enabled, no REST endpoint can read those records, because `query_alerts` filters them out and the generic log endpoint
cannot open `eve.json`.** Evidence: the `eveLog` node of the IDS model has exactly two children, `http` and `tls`; the `suricata.yaml`
template's eve-log `types:` list contains only `alert`, `anomaly`, `drop`, `ssh` and the conditional `http` and `tls` — **no `dns` output
type** (https://github.com/opnsense/core/blob/26.7.3/src/opnsense/service/templates/OPNsense/IDS/suricata.yaml). The template does contain a
`dns:` block, but under `app-layer` > `protocols`: that enables the DNS *protocol parser*, which feeds detection, and has no effect on
eve-log output.

Procedure for the user, for the part that is possible — the text destined for the README at step 8:

1. Go to **Services > Intrusion Detection > Administration**, tab **Settings**.
2. Turn on the **Advanced mode** toggle; the eve-log fields are advanced-only and hidden otherwise.
3. Tick **Enable eve TLS logging**. Optionally tick **Eve TLS extended logging** for full certificate metadata, or pick individual fields
   under **Eve TLS custom logging** (server name indication, subject, issuer, serial, fingerprint, validity dates, JA3/JA3S/JA4 hashes).
4. Optionally tick **Enable eve HTTP logging**, plus **Eve HTTP extended logging** or **Eve HTTP dump all headers**.
5. Click **Apply**. OPNsense regenerates `suricata.yaml` from its template and restarts the engine.
6. There is no step for DNS events. The option does not exist.

**Upgrade survivability.** The `http` and `tls` toggles are model fields stored in the firewall's `config.xml` and re-applied on every
configuration regeneration, so **they survive firmware upgrades**. Obtaining `dns` events would require hand-editing the `suricata.yaml`
template shipped inside the OPNsense package — a write to the firewall filesystem, reverted by any upgrade. It is unsupported, and this
document does not propose it.

**`opnview` never performs any of this.** Enabling Suricata event types is a manual operation carried out by the user in the web UI. The
application uses the OPNsense API strictly read-only: it never calls `/api/ids/settings/set` or `/api/ids/service/reconfigure`, and never
writes to the firewall in any other way. It detects the current state and reports it — the read-only rule of `ROADMAP.md` applied here.

### (c) Volume

The table below is an **estimate**, not a measurement: what it would cost to log every DNS lookup and every TLS handshake of a whole
network, for the hypothetical case where both event types were emitted. Assumptions, stated so they can be challenged: 5 000 DNS events per
device per day; 2 000 TLS handshakes per device per day; average `dns` record 350 bytes and average non-extended `tls` record 450 bytes, one
JSON object per line, uncompressed — so 2.65 MB per device per day with both enabled. `UNVERIFIED:` these per-device rates and record sizes.
Neither OPNsense nor Suricata publishes eve-log volume figures; the byte sizes are derived from the field sets Suricata emits and the rates
are the author's estimate. If they are wrong by an order of magnitude, the disk-sizing conclusion changes.

| Devices | GB/day | GB/month (30 d) | Days held by the firewall's own rotation |
|---|---|---|---|
| 10 | 0.027 | 0.80 | ~95 |
| 50 | 0.13 | 3.98 | ~19 |
| 200 | 0.53 | 15.9 | ~4.8 |

With **extended** TLS logging (roughly 1.0 kB per record) the figures become 0.038 / 0.19 / 0.75 GB per day, 1.13 / 5.63 / 22.5 GB per
month, and about 67 / 13 / 3.4 days held. The retention column applies the rotation parameters above: five files of about 0.51 GB.

**Conclusion.** At 200 devices with both event types the firewall would hold under five days of history while writing over half a gigabyte a
day to a disk not sized for logging. Continuous consumption is therefore mandatory in principle — but it does **not** keep the log off that
disk: reading through the API deletes nothing, and rotation, not `opnview`, bounds the file. The practical consequence is the opposite of
the one the roadmap anticipated: the risk is not falling behind, it is that `opnview` cannot read those records at all. **If the application
is offline for a period**, alerts are recoverable while the records are still inside the rotation window: on restart `opnview` resumes from
its stored `(fileid, filepos)` watermark. If rotation has discarded the file that watermark referred to — detectable because
`get_alert_logs` no longer lists a matching file — the gap is permanent and recorded as such.

## Data source 3 — NetFlow / Insight

NetFlow capture and the Insight reporting views are part of the base system, not a plugin.

### Endpoints

| Path | Method | Parameters | Citation |
|---|---|---|---|
| `/api/diagnostics/netflow/is_enabled` | GET | none | https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Diagnostics/Api/NetflowController.php |
| `/api/diagnostics/netflow/status` | GET | none | https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Diagnostics/Api/NetflowController.php |
| `/api/diagnostics/networkinsight/get_metadata` | GET | none | https://docs.opnsense.org/development/api/core/diagnostics.html |
| `/api/diagnostics/networkinsight/get_interfaces` | GET | none | https://docs.opnsense.org/development/api/core/diagnostics.html |
| `/api/diagnostics/networkinsight/top/<provider>/<from>/<to>/<fields>/<measure>/<max_hits>` | GET | positional; optional `filter_field` / `filter_value` in the query string | https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Diagnostics/Api/NetworkinsightController.php |
| `/api/diagnostics/networkinsight/timeserie/<provider>/<measure>/<from>/<to>/<resolution>/<fields>` | GET | positional | https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Diagnostics/Api/NetworkinsightController.php |

`provider` is an aggregate class name; `fields` is a single path segment holding a comma-separated list of that provider's aggregation
fields; `measure` is `octets` or `packets` for `top`, and additionally `octets_ps`, `packets_ps` or `bps` for `timeserie`; `from` and `to`
are Unix timestamps. **The endpoint returning per-address-pair volume** is `top` with the provider `FlowSourceAddrDetails` and a field list
containing both `src_addr` and `dst_addr`.

### Response shape

`top` returns one object per group containing the requested key fields plus **`total`** — the summed measure, the byte value when `measure`
is `octets` and the packet value when it is `packets`, there being no separate `octets` key in a `top` response — and **`last_seen`**, a
Unix timestamp. Rows beyond `max_hits` collapse into a single remainder record with empty key fields. `timeserie` returns a list of `{"key":
…, "values": [[epoch_ms, value], …]}`, with `interface` and `direction` added for `FlowInterfaceTotals`; missing slices are zero-filled. At
the storage layer each aggregate is a SQLite table with the columns `mtime`, `last_seen`, the provider's aggregation fields, **`octets`**
and **`packets`** — the byte and packet field names. Three behaviours matter for correctness: the details aggregate swaps source and
destination for the outbound direction and writes each flow once per interface and direction, so naive summation double-counts; the port
stored in the details and port aggregates is `min(src_port, dst_port)`, a heuristic rather than the real destination port; and flows
spanning a slice boundary are pro-rated, so `octets` are floating-point approximations.

### Retention on the firewall

Retention is hardcoded per aggregate class — no user-facing setting — and the data lives in SQLite files under `/var/netflow`, one per class
per resolution.

| Provider | Aggregation fields | Resolutions (s) | Retention per resolution |
|---|---|---|---|
| `FlowInterfaceTotals` | `if`, `direction` | 30, 300, 3600, 86400 | 1 day, 7 days, 31 days, 365 days |
| `FlowSourceAddrTotals` | `if`, `src_addr`, `direction` | 300, 3600, 86400 | 1 hour, 1 day, 365 days |
| `FlowDstPortTotals` | `if`, `protocol`, `dst_port` | 300, 3600, 86400 | 1 hour, 1 day, 365 days |
| `FlowSourceAddrDetails` | `if`, `direction`, `src_addr`, `dst_addr`, `service_port`, `protocol` | **86400 only** | **62 days** |

**This is the decisive finding for the matrix.** Per-address-pair volume exists, but only at **daily** resolution and only for 62 days;
there is no sub-daily per-pair data anywhere on the firewall, so the 1 h and 24 h period selectors the roadmap wants cannot be served from
Insight alone. Sub-daily volume can only be approximated from the per-source totals at 300 s resolution, whose one-hour retention makes
`opnview`'s own history mandatory. The backend also picks the resolution for `top` automatically; the caller cannot choose it.

### Sustainable polling frequency

**300 seconds for the time series, 900 seconds for the per-pair details.** The 300 s figure matches the finest resolution at which
per-source data is kept and the one-hour retention of that resolution: polling more often returns the same slice repeatedly, polling less
often than hourly loses it permanently. The per-pair aggregate only advances once per day, so 900 s is already far more often than the data
changes.

**Observation-point limit.** NetFlow is captured on the firewall's own interfaces, so volume between two devices inside the same segment is
never seen: every figure derived from Insight is a lower bound, and every screen presenting it must say so.

### Degradation

`is_enabled` returns `{"netflow": 0|1, "local": 0|1}`: `netflow` is 1 only when both capture targets and capture interfaces are configured,
`local` only when local collection is enabled. **Insight data exists only when `local` is 1** — a firewall exporting to an external
collector reports `netflow: 1, local: 0` and has no Insight data at all. Empty results are not a reliable signal: `timeserie` synthesises
zero-filled series when there is no data and `top` returns `[]` when the provider table is missing. Detection order: `is_enabled` must show
`local == 1`, otherwise report **NetFlow collection not enabled** and point the user at Reporting > NetFlow; then `get_metadata` must show a
non-zero `last_sync` and a populated aggregator map, otherwise report **NetFlow enabled but not yet aggregating**; only then are empty `top`
results genuinely no traffic. Both failure states are shown as their own condition, never as a flat zero line.

### Sources

Endpoint citations are in the table above. Additionally: https://docs.opnsense.org/manual/netflow.html ·
https://github.com/opnsense/core/blob/26.7.3/src/opnsense/scripts/netflow/lib/aggregates/__init__.py ·
https://github.com/opnsense/core/blob/26.7.3/src/opnsense/scripts/netflow/lib/aggregates/interface.py ·
https://github.com/opnsense/core/blob/26.7.3/src/opnsense/scripts/netflow/lib/aggregates/source.py ·
https://github.com/opnsense/core/blob/26.7.3/src/opnsense/scripts/netflow/lib/aggregates/ports.py

## Data source 4 — DHCP leases

Three DHCP backends exist in the 26.7 series: **Dnsmasq** (the default), **Kea**, and **ISC dhcpd**, end-of-life and, since the 26.1 series,
shipped as the `os-isc-dhcp` plugin. Each has its own lease endpoint; there is no unified one.

### Endpoints

| Path | Method | Parameters | Backend | Citation |
|---|---|---|---|---|
| `/api/kea/leases4/search` | POST (read-only) | `selected_interfaces` plus the pagination set | Kea DHCPv4 | https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Kea/Api/Leases4Controller.php |
| `/api/dnsmasq/leases/search` | POST (read-only) | `selected_interfaces`, `selected_protocol`, plus the pagination set | Dnsmasq | https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Dnsmasq/Api/LeasesController.php |
| `/api/dhcpv4/leases/searchLease` | GET | `inactive`, `selected_interfaces`, plus the pagination set | ISC dhcpd (plugin only) | https://docs.opnsense.org/manual/isc.html |

`UNVERIFIED:` the exact parameter and field contract of the ISC plugin endpoint — it lives in the plugin repository rather than in
`opnsense/core`, so `opnview` treats ISC as best-effort: detect it, read it if present, degrade cleanly if the shape differs.

### Response shape

The envelope is the standard `{rows, rowCount, total, current}`; Kea adds `stats` (active / inactive / total) and both current backends add
an `interfaces` map from interface identifier to description.

| Concept | Kea | Dnsmasq | ISC (plugin) |
|---|---|---|---|
| **hostname** | `hostname` | `hostname` | `hostname` |
| **MAC address** | `hwaddr` | `hwaddr` | `mac` |
| IP address | `address` (+ `prefix_len`) | `address` | `address` |
| lease state | `state` | `lease_type`, `is_reserved` | `state`, `status` |
| expiry | `expire`, `valid_lifetime` | `expire` | `starts`, `ends` |
| interface | `if_name`, `if_descr` | `if_name`, `if_descr` | `if`, `if_descr` |
| client identity | `client_id`, `duid`, `iaid` | `client_id`, `iaid` | — |
| vendor hint | `mac_info` | `mac_info` | `man` |

The hostname field is uniformly `hostname`. The MAC field is `hwaddr` on the two current backends and `mac` on the legacy plugin — the only
normalisation needed. `mac_info` carries the OUI vendor string, useful context but never a basis for classifying a device, and never a
segment.

### Retention on the firewall

Kea leases are read **live from the running daemon** through the Kea control agent, not from a file: if that agent is not enabled the lease
list comes back empty even though DHCP is working. Lifetime is the configured valid lifetime, expired leases being reclaimed on a schedule.
Dnsmasq leases are read from its lease file, lifetime set per range and capped by a global maximum. In both cases a device that goes quiet
disappears from the lease table once its lease expires, so `opnview` must persist device identity.

### Sustainable polling frequency

**300 seconds.** Leases change on the scale of minutes to hours, the table is small, and the Kea path costs a round trip to the control
agent. Five minutes keeps hostnames fresh enough to name a device without polling a daemon that has nothing new to say.

### Degradation

`opnview` probes both current backends and takes whichever is running.

| Check | Endpoint | Field | Citation |
|---|---|---|---|
| Kea service state | `/api/kea/service/status` | `status` | https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Kea/Api/ServiceController.php |
| Kea DHCPv4 enabled | `/api/kea/dhcpv4/get` | `dhcpv4.general.enabled` | https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Kea/Api/Dhcpv4Controller.php |
| Dnsmasq service state | `/api/dnsmasq/service/status` | `status` | https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Dnsmasq/Api/ServiceController.php |
| Dnsmasq DHCP configured | `/api/dnsmasq/settings/get` | non-empty `dnsmasq.dhcp_ranges` | https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/models/OPNsense/Dnsmasq/Dnsmasq.xml |
| ISC present at all | `/api/dhcpv4/service/status` | 404 means the plugin is absent | https://docs.opnsense.org/manual/dhcp.html |

Note the Dnsmasq enable flag is `dnsmasq.enable`, not `enabled`. When no backend reports `running`, `opnview` reports **DHCP source
unavailable** and falls back to naming devices by address, saying so in the UI. An empty lease list from a running backend is reported as
"no active leases" (for Kea with a hint about the control agent). Neither case is presented as an absence of devices.

### Sources

Endpoint citations are in the tables above. Additionally: https://docs.opnsense.org/manual/kea.html ·
https://docs.opnsense.org/manual/dnsmasq.html · https://docs.opnsense.org/releases/CE_26.1.html ·
https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Kea/Api/LeasesController.php

## Data source 5 — Resolver DNS lookups

Two resolvers are possible, **Unbound** and **Dnsmasq**, reachable in very different ways: only one of them offers structured data.

### Endpoints

| Path | Method | Parameters | Citation |
|---|---|---|---|
| `/api/unbound/overview/search_queries` | **POST (read-only), `Content-Type: application/json`** | `client` (string, an IP address), `timeStart` and `timeEnd` (**JSON integers**, Unix seconds), plus the pagination set | https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Unbound/Api/OverviewController.php |
| `/api/unbound/overview/is_enabled` | GET | none | https://docs.opnsense.org/development/api/core/unbound.html |
| `/api/unbound/overview/totals/<maximum>` | GET | `maximum` positional | https://docs.opnsense.org/development/api/core/unbound.html |
| `/api/diagnostics/log/core/resolver` | POST (read-only) | `rowCount`, `current`, `searchPhrase`, `severity`, `validFrom` | https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/models/OPNsense/Unbound/Menu/Menu.xml |
| `/api/diagnostics/log/core/dnsmasq` | POST (read-only) | same | https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Diagnostics/Api/LogController.php |

`search_queries` is the one endpoint here whose **call form is load-bearing**: the windowed branch is reachable only through a JSON request
body. The controller keeps `timeStart` and `timeEnd` only if `is_int()` holds; a query string or form-encoded body yields strings, so both
become `null` and it falls through to the unwindowed branch. Both branches are strictly read-only `configd` queries with no write path.

**Unbound is reachable in structured form. Dnsmasq is not.** Dnsmasq has no query-reporting API; the only way to obtain its per-client
lookups is for the user to enable `dnsmasq.log_queries` and for `opnview` to parse the free-text `line` field of the generic log endpoint —
a grammar that is dnsmasq's own rather than an OPNsense contract. **Turning that setting on is a manual user operation performed in the web
UI; `opnview` never performs it.** It never calls `/api/dnsmasq/settings/set` or any other mutating command: it reads `dnsmasq.log_queries`
to detect the state and reports it, exactly as for the Suricata toggles in section 4(b). The generic log endpoint's route shape is
`/api/diagnostics/log/<module>/<scope>`, with `/export` (CSV) and `/live` (SSE) sub-commands; for core scopes the resolver maps `<scope>` to
`/var/log/<scope>/<scope>_*.log`. The scope names `resolver` and `dnsmasq` come from the menu definitions each module ships.

### Response shape

`search_queries` returns the standard envelope, each row carrying `client` (the querying address), `domain`, `time` (Unix timestamp),
`action` (pass / block / drop), `source` (recursion / local / local-data / cache), `rcode`, `dnssec_status`, `blocklist` and `uuid`, plus a
derived `status` and category metadata — exactly the shape the site-name fallback needs. The generic log endpoint returns rows of
`{timestamp, severity, process_name, line}`, `line` being the resolver's own free text. `UNVERIFIED:` the grammar of Unbound's and Dnsmasq's
query-log lines — neither is a documented stable contract, and a regex over `line` is inherently fragile.

**The value sets, read for step 5.** The query report stores `action` and `source` as integers and `scripts/unbound/stats.py` maps them
(https://github.com/opnsense/core/blob/26.7.3/src/opnsense/scripts/unbound/stats.py): `action` is `Pass`, `Block` or `Drop`, and `source`
is `Recursion`, `Local`, `Local-data` or `Cache`, capitalised exactly so. The module that writes them,
`scripts/unbound-dnsbl/dnsbl_module.py`, says what each means: `Recursion` is a query the iterator resolved; `Cache` an answer from
Unbound's cache; `Local-data` an answer from a local zone or `local-data` entry — **a host override is one**, since OPNsense writes host
overrides as `local-data:` lines (`src/etc/inc/plugins.inc.d/unbound.inc`); and `Local` is a blocklist's answer (`Block`) or any SERVFAIL
(`Drop`), so a `Drop` is not always a policy decision. A lookup that names a site for `opnview` is therefore a `Pass` answered by
`Recursion`, `Cache` or `Local-data`. One more fact of the same code bears on correlation: `client` is **replaced by the reverse-resolved
host name** when the firewall has one (`stats.py`), so a lookup's `client` is not always an address.

*Read for the step-5A corrections.* The name comes from `scripts/unbound/logger.py`
(https://github.com/opnsense/core/blob/26.7.3/src/opnsense/scripts/unbound/logger.py), which resolves every querying address it logs with
`socket.gethostbyaddr` under a 10 ms timeout and stores the answer in its `client` table; the `details` query of `stats.py` joins the
logged address to that table and returns the host name in its place whenever one was found. A reverse lookup answers with the fully
qualified name. `opnview` resolves such a name back to an address through the DHCP leases valid at the lookup's instant — a lease under
that name or under its first label — and only when exactly one address answers; otherwise the lookup names no machine
(`docs/data-model.md`, `dns_resolution`). `UNVERIFIED:` how often a live firewall logs a host name rather than an address; it is on the
step-5 validation list in `ROADMAP.md`.

### Retention on the firewall

Unbound query reporting writes to an embedded database **truncated to the last 7 days**, hourly, with a periodic compaction pass — a hard
ceiling regardless of disk size, so `opnview` must keep its own history. Syslog-based query logs follow the generic logging retention
described for the filter logs.

### Sustainable polling frequency

**60 seconds.** DNS lookups must be correlated with flows that follow within seconds, so the window has to be short. Two constraints on
incrementality, both derived from the controller source cited above:

1. **The window is per client, and there is no global "everything since T" query.** `search_queries` runs the bounded backend command
   `unbound qstats query` **only when `client`, `timeStart` and `timeEnd` are all accepted**; otherwise it runs `unbound qstats details`
   with a fixed argument of 1000, returning the 1000 most recent records regardless of any parameter sent. `opnview` therefore iterates over
   the clients it knows — from DHCP leases and observed flows — asking for each one's window since its last stored timestamp, and
   deduplicates on the row `uuid`.
2. **The window only takes effect over a JSON body.** `timeStart` and `timeEnd` are accepted only if they are PHP integers, which they can
   be only when the request carried a `Content-Type: application/json` body (see *Authentication and conventions*). A `GET` with a query
   string, or a form-encoded `POST`, is **not rejected**: it silently degrades to the 1000-most-recent fallback, with HTTP 200 and no error.
   This is the most dangerous failure mode in the document, because it loses data without any signal. Step 4 must therefore issue this call
   as a JSON-body `POST` and must additionally assert incrementality at runtime: if a response contains rows outside the requested
   `[timeStart, timeEnd]` window, or ignores `client`, the window was not honoured — treat it as a collection fault and report it, rather
   than ingesting the fallback set as if it were the window.

### Degradation

| Check | Endpoint | Field | Citation |
|---|---|---|---|
| Unbound running | `/api/unbound/service/status` | `status` | https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Unbound/Api/ServiceController.php |
| Unbound enabled | `/api/unbound/settings/get` | `unbound.general.enabled` | https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/models/OPNsense/Unbound/Unbound.xml |
| Query reporting on | `/api/unbound/overview/is_enabled` | `enabled` | https://docs.opnsense.org/development/api/core/unbound.html |
| Dnsmasq query logging on | `/api/dnsmasq/settings/get` | `dnsmasq.log_queries` | https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/models/OPNsense/Dnsmasq/Dnsmasq.xml |

Dnsmasq's running state is checked with `/api/dnsmasq/service/status`, cited in data source 4. Three distinct unavailable states are
reported, never collapsed into "no domains seen": the **resolver is unreachable** (status endpoints 404 or the calls fail); the **resolver
is running but reporting or logging is disabled** (Unbound query reporting under Services > Unbound DNS > Reporting, or Dnsmasq query
logging — the UI says which setting to turn on); or the **resolver is running with reporting on and there are simply no rows**. Only the
third is an absence of data; the first two are configuration states, labelled as such, with the attribution rate dropping to zero. A fourth
state sits alongside them: **the query window was not honoured**, detected by the out-of-window / wrong-client assertion described above. It
is a collection fault, not an absence of lookups, and is surfaced as such — never counted as "this client made no queries".

### Sources

Endpoint citations are in the tables above. Additionally: https://docs.opnsense.org/manual/unbound.html ·
https://github.com/opnsense/core/blob/26.7.3/src/opnsense/scripts/unbound/stats.py ·
https://github.com/opnsense/core/blob/26.7.3/src/opnsense/scripts/unbound/logger.py

## Runtime discovery

Everything here is read at startup and refreshed periodically. None of it is ever hardcoded, defaulted, or inferred from a name.

**(i) Interfaces with their user-given description and subnet.** `/api/interfaces/overview/interfaces_info` (GET; optional positional
`details`) returns the standard search envelope. Per-row fields: `identifier` (configuration key), `description` (the **user-given**
description, falling back to the upper-cased identifier), `device`, `status`, `enabled`, `link_type`, `addr4` and `addr6` (primary address
in `address/prefix` form — the subnet source), `ipv4[]` and `ipv6[]` (each entry an `ipaddr` in the same form), `vlan_tag`, `gateways[]`,
`routes[]`, `macaddr`. `/api/diagnostics/interface/get_interface_names` (GET) returns a flat map from raw device name to description — the
join key that turns the filter log's `interface` field into a segment name. https://docs.opnsense.org/development/api/core/interfaces.html ·
https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Interfaces/Api/OverviewController.php ·
https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Diagnostics/Api/InterfaceController.php

*Read for step 5 from the same controller.* `addr4` and `addr6` are one string each, `"<address>/<prefix length>"` — the interface's own
primary address, not its network — or the empty string. `gateways[]` is a list of gateway **addresses**, at most one per address family:
for each family, the first enabled gateway configured on that interface that has an address, dynamic gateways included
(`Routing/Gateways.php`, `getInterfaceGateway`). It is not limited to gateways marked *Upstream Gateway* — OPNsense applies "upstream" to a
gateway, as a default-gateway candidate (https://docs.opnsense.org/manual/gateways.html) — and a LAN or VLAN interface with no gateway
reports `[]`. `opnview` calls an interface **upstream** exactly when its `gateways[]` is non-empty, and stores `addr4`, `addr6` and
`gateways[]` with the instants it saw them.

*Read for the step-5A corrections from the same controller.* Each entry of `ipv4[]` and `ipv6[]` is an object whose `ipaddr` is the
address and its `subnetbits` joined as `"<address>/<prefix length>"`; an entry for a CARP address also carries its `vhid`, and its
`status`, `advbase`, `advskew`, `peer` and `peer6` from the CARP details. The arrays list every address the interface holds, the primary
one included, and `ipv6[]` includes the interface's link-local address, which may carry its zone. `opnview` stores every entry under
`source_field` `ipv4` or `ipv6`, and proposes the range each covers, masked to its prefix length, as one of the interface's **networks**
— a link-local one excepted, because it is the same on every interface. *Network* is OPNsense's own word for that range: the rule editor
offers "LAN net", and the internal alias holding it stands for "LAN network" (https://docs.opnsense.org/manual/aliases.html, *internal
aliases*). The operator can confirm, remove or add networks in `opnview`; see `docs/data-model.md`, `interface_network`.

**(ii) Firewall rules with their label/description and identifier.** `/api/firewall/filter/search_rule` (POST, read-only) takes `interface`
(comma-separated configuration keys; omitted means all rules), `category`, `show_all`, plus the pagination set; `rowCount: -1` retrieves
everything. Per-row fields include `uuid`, `description`, `enabled`, `action`, `direction`, `interface`, `ipprotocol`, `protocol`,
`source_net`, `source_port`, `destination_net`, `destination_port`, `categories`, `log`, `sort_order`, `legacy`, `is_automatic`. In the 26.7
series rules default to the MVC/API model, and the search additionally merges legacy and auto-generated rules, for which `uuid` carries the
pf **label** — the same token the filter log exposes as `rid`, so `rid` joins to `uuid` for both. For legacy rows the human-facing fields
are localised while the `%`-prefixed twins (`%action`, `%direction`, `%protocol`, `%ipprotocol`) carry the raw values machine logic must
use. https://docs.opnsense.org/development/api/core/firewall.html ·
https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Firewall/Api/FilterController.php ·
https://github.com/opnsense/core/blob/26.7.3/src/opnsense/scripts/filter/list_non_mvc_rules.php

*Read for step 5 from the same controller.* A row's `interface` means two things. On a model rule it is the comma-separated list of interface
**configuration keys** (`Filter.xml`, an `InterfaceField` with `Multiple` set), with the descriptions in a `%interface` twin. On a legacy or
automatic row the controller **rewrites each key into the interface's description** in place and keeps no raw twin. An empty value is a
floating rule, which the controller treats as matching on every interface. `opnview` stores the field verbatim with `legacy` beside it,
resolves a model rule's keys by equality with the interface identifier, and leaves a legacy rule's interfaces unresolved rather than matching
on a description.

**(iii) Determining whether the resolver is Unbound or Dnsmasq.** `/api/unbound/service/status` and `/api/dnsmasq/service/status` (GET) —
the one reporting `status: "running"` is active; both can be running, in which case the configuration decides which serves clients. Confirm
with `/api/unbound/settings/get` → `unbound.general.enabled` and `/api/dnsmasq/settings/get` → `dnsmasq.enable`.
https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Unbound/Api/ServiceController.php ·
https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Dnsmasq/Api/ServiceController.php ·
https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/models/OPNsense/Unbound/Unbound.xml ·
https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/models/OPNsense/Dnsmasq/Dnsmasq.xml

**(iv) Determining which DHCP server is active.** `/api/kea/service/status` → `status`, confirmed by `/api/kea/dhcpv4/get` →
`dhcpv4.general.enabled`; `/api/dnsmasq/service/status` → `status`, confirmed by `/api/dnsmasq/settings/get` → non-empty
`dnsmasq.dhcp_ranges`; and `/api/dhcpv4/service/status`, where a 404 means the end-of-life ISC plugin is not installed, the expected case on
a 26.7 installation. https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Kea/Api/ServiceController.php ·
https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Kea/Api/Dhcpv4Controller.php ·
https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Dnsmasq/Api/ServiceController.php ·
https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/models/OPNsense/Dnsmasq/Dnsmasq.xml ·
https://docs.opnsense.org/manual/dhcp.html

**(v) Distinguishing Suricata absent, installed but stopped, and running — and on which interfaces.** `/api/ids/service/status` (GET): 404,
401 or 403 means **absent or not permitted**; HTTP 200 with `status` in `stopped` / `disabled` / `unknown` means **installed but not
running**; HTTP 200 with `status` of `running` means **running**. `/api/ids/settings/get` (GET) carries the interface list at
`ids.general.interfaces`, an object keyed by interface identifier with `{value, selected}` per entry — the interfaces Suricata actually runs
on are the keys whose `selected` is truthy. Also read `ids.general.enabled` and `ids.general.eveLog.tls.enable` /
`ids.general.eveLog.http.enable`. A mixed installation, covering only some interfaces, is fully representable, and the UI must state per
segment whether it is covered. https://docs.opnsense.org/development/api/core/ids.html ·
https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/IDS/Api/SettingsController.php

## Polling plan

| Source | Interval | Incremental mechanism | Why |
|---|---|---|---|
| Filter logs | 10 s | `digest` of the last seen line; drop the echoed record; a missing digest means a gap | No offset or cursor exists and each poll is capped at `limit` records, so a short interval keeps the digest inside the window |
| Suricata `eve.json` (alerts only) | 60 s | Application-side `(fileid, filepos)` watermark; offsets from end-of-file are unstable | Alerts are low-volume and the backend rescans the file each call; one minute bounds cost and staleness |
| NetFlow / Insight | 300 s (time series), 900 s (per-pair details) | Timestamp window per request; re-request and overwrite the current slice | 300 s is the finest stored resolution and its retention is one hour; the per-pair aggregate only advances daily |
| DHCP leases | 300 s | Full re-read with upsert on address plus MAC | Small, slow-changing table; the Kea path costs a control-agent round trip |
| Resolver DNS lookups | 60 s | Per-client `timeStart`/`timeEnd` window since the last stored timestamp, sent as **JSON integers in a read-only POST body**; deduplicate on row `uuid`; assert the returned rows fall inside the window | Correlation with subsequent flows needs a short window; no global "since T" query exists, and any other call form silently degrades to the 1000-most-recent fallback |

Each interval is the one justified in that source's *Sustainable polling frequency* subsection.

## Verified against a live firewall, 2026-09-27

Everything above this section was read from documentation and source. On
2026-09-27 the maintainer ran read-only probes against his own OPNsense
**26.7.3_11** — the exact version this document is pinned to, so nothing below
is version drift. **Where this section contradicts a claim above, this section
is right**: it was measured, the other was inferred.

### Wrong paths

| This document says | Reality on 26.7.3_11 |
|---|---|
| `/api/diagnostics/netflow/top` | **404.** The per-pair data is at `/api/diagnostics/traffic/top/<interface names>` |
| `/api/diagnostics/netflow/get_metadata` | **404** |
| `/api/dhcpv4/leases/searchLease` | **404** — ISC is gone |
| `/api/ids/service/get_rule_info/<sid>` | **404** — step 6 depends on this and must find the real path |
| `/api/diagnostics/netflow/aggregate`, `get_providers`, `settings/get` | **404** |

`/api/diagnostics/traffic/top/` takes **interface names** — `lan`, `wan`,
`opt1`, `opt2`, `opt3`. A device name (`igb0`, `vlan01`) returns `[]` with HTTP 200,
which is the trap: a wrong argument looks like an absence of traffic.

### The per-pair data is a live snapshot, not history

`traffic/top` returns, per interface, a `records[]` of local addresses each with
`rate_bits_in`/`out`, `cumulative_bytes_in`/`out` and a `details[]` of the peers
it talked to. Measured cumulative values were hundreds of bytes: this is the
live top-talkers view, and there is no endpoint exposing the flow aggregate over
an arbitrary past window — `netflow/aggregate` does not exist.

**Consequence, and it is architectural rather than a collector detail:
`opnview` builds its own per-pair history by sampling this endpoint.** Nothing
upstream will answer "what did these two talk about last Tuesday".

### What `traffic/top` measures, read from source for step 5

`TrafficController::topAction` maps the interface names to devices and runs the configd action `interface show top`, which is
`scripts/interfaces/traffic_top.py --interfaces <devices>`
(https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Diagnostics/Api/TrafficController.php,
https://github.com/opnsense/core/blob/26.7.3/src/opnsense/scripts/interfaces/traffic_top.py). That script runs, once per interface,
`/usr/local/sbin/iftop -nNb -i <device> -s 2 -t`: **one text output after two seconds, then quit**. So:

- `rate_bits_*` is iftop's rate over the last two seconds, in bits per second;
- `cumulative_bytes_*` is the bytes iftop counted **since its capture began — the same two seconds**;
- every figure the endpoint returns describes a single capture of about two seconds, taken when the call is made. Nothing covers the
  interval between two calls.

**Sampled bytes are therefore bytes seen in samples, two seconds per sample, and never a period volume.** They are carried with the seconds
sampled as their coverage and never extrapolated; a period with no sample is *not sampled*, never zero.

The shape, from the same script. Per interface, `records[]`, one per address iftop reported on the right-hand side of a line pair, each
with `address`, `rate_bits_in`, `rate_bits_out`, `cumulative_bytes_in`, `cumulative_bytes_out` (that address's totals over all its peers)
and `details[]`. Each `details` entry is the **peer's** half of one line pair — `address`, `rate_bits` and `cumulative_bytes` of what the peer
sent to the record's address — so per peer only the inbound figures are reported, and the record's own outbound figures are a total over
its peers. `opnview` stores a pair's outbound figures only when the record has exactly one peer, where the total is the pair's.

Three cautions, read from the code and not yet measured: iftop stops its text output at its default of ten lines per interface, as
OPNsense passes no `-L`, so the snapshot may hold only the ten busiest pairs; iftop prints sizes in base 1024 while the script multiplies
`Kb` by 1000; and which end iftop puts on the right-hand side, which the survey above calls the local address, was not traced in iftop's
own source. **All three are to be checked against a live firewall at the validation of step 5.**

### The resolver window is not honoured at all

Not "sometimes truncated" — **ignored**. Measured: a 5-minute window and a
24-hour window both returned the same ~410-second span; `total` is `1000`
whatever is asked; pages 2 and 5 walk further back (290 s, 202 s). It is a ring
buffer of the last 1000 lookups, paginated, and `timeStart`/`timeEnd` do
nothing.

**And `uuid` is `null` on every row**, so the de-duplication key this document
assumed does not exist. De-duplication must be composed from the row's own
content.

The JSON-body POST form is confirmed correct.

### The filter log's digest is not a server-side cursor

Measured twice: `?digest=<a digest from the current response>` returns byte-
identical output to the same call without it, and the supplied digest does not
appear in the response. Whether the parameter is ignored or honoured-but-scrolled
could not be separated, and for the product it does not matter.

**What works instead**, and the measurement that makes it safe: the log runs at
a few lines per second on an ordinary home network, so a 10-second poll at
`limit=500` leaves an order of magnitude of headroom. Measure it per
installation rather than assuming this one. Pull the most recent
N, discard what `__digest__` says has been seen — the uniqueness the schema
already has — and declare a gap when the oldest line returned is newer than the
newest line stored. The gap is then detected rather than assumed.

`__timestamp__` came back as `2026-09-26T23:51:06` — the ISO form, not the
year-less syslog form. Both remain possible per the syslog format in use, so
normalisation still needs to handle both; only one was exercised.

### Dnsmasq serves leases as structured JSON

`/api/dnsmasq/leases/search` returns rows carrying `hwaddr`, `address`,
`hostname`, `client_id`, `expire`, `is_reserved`, the vendor as `mac_info`, and
the interface under **all three of its names at once** — the device, the
`optN` identifier and the description. This document's claim that Dnsmasq
requires parsing a free-text log is wrong for leases. No parser is needed.

On the probed firewall Kea is `disabled` and Dnsmasq is `running`, and both
Unbound and Dnsmasq are running as resolvers — the provider-ambiguity case is
not hypothetical.

### Two client-identity sources this document did not name

- `/api/diagnostics/interface/get_arp` — one row per neighbour, each with `mac`, `ip`,
  `intf`, `intf_description`, `manufacturer`, `expired`, `permanent`.
- `/api/diagnostics/interface/get_ndp` — the IPv6 equivalent.

Both matter: without them, a firewall whose DHCP is unreadable has no client
identity at all.

### The telemetry the data model calls gaps G9 and G10 exists

All answered: `/api/diagnostics/system/systemResources`,
`/api/diagnostics/system/systemTemperature`,
`/api/diagnostics/system/systemTime`, `/api/diagnostics/system/systemDisk`,
`/api/diagnostics/activity/getActivity`, and
`/api/diagnostics/traffic/interface` — the last giving per-interface packet and
byte counters, errors, link state and line rate. The four system paths are
written out in full here rather than sharing a prefix, so each one can be checked
verbatim against the endpoint registry `opnview` ships; the probes and their
results are unchanged. **The gap is in the schema, which has no table for a
sampled gauge, not in the API.**

**`systemTime`, read on a live OPNsense 26.7 on 3 October 2026.** The answer is
one object of five fields, and none of them is a number:

| field | as it came back | what it is |
|---|---|---|
| `uptime` | `"17 days, 23:24:10"` | text: days, then hours, minutes and seconds |
| `datetime` | `"Sat Oct 3 21:25:37 CEST 2026"` | the firewall's **wall clock**, with a zone **abbreviation** |
| `boottime` | `"Tue Sep 15 22:01:27 CEST 2026"` | same shape |
| `config` | `"Thu Oct 1 23:30:53 CEST 2026"` | same shape: when the configuration was last saved |
| `loadavg` | `"0.07, 0.14, 0.15"` | text: the 1-, 5- and 15-minute averages |

`datetime` is what closes gap 7: read beside the UTC instant of the request, it
gives the firewall's offset from UTC. The abbreviation names no offset a parser
can trust, so the offset is the difference between the wall clock and the UTC
instant, rounded to the quarter hour. That same firewall, read with the offset
at zero, had every filter-log line stored 7 200 s ahead of UTC.

### Telemetry field names, read from source for step 5

**Interface errors.** `/api/diagnostics/traffic/interface` runs `interface show traffic`, which is `scripts/interfaces/traffic_stats.php`,
returning `interfaces.<interface name>` built by `legacy_interface_stats()` (https://github.com/opnsense/core/blob/26.7.3/src/etc/inc/interfaces.lib.inc):
it runs `ifinfo` and splits each printed line on `:` into a key and a **string** value. The keys are therefore `ifinfo`'s own labels
(https://github.com/opnsense/ports/blob/master/opnsense/ifinfo/files/ifinfo.c, read at the master branch, not at a 26.7.3 tag): among them
`bytes received`, `bytes transmitted`, `packets received`, `packets transmitted`, **`input errors`**, **`output errors`** and `collisions`.
`opnview` samples the two error counters under exactly those names.

**Swap.** `systemResources` returns **no swap figure**: it reads `hw.physmem` and the page counts and returns `memory.total`, `memory.used`
and, with ZFS, `memory.arc` (https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Diagnostics/Api/SystemController.php).
Swap is served by a separate endpoint, recorded under *Swap, read from source for step 5* below and sampled since the amendment of
4 October 2026.

All of the above is read from source; **the field names are to be probed against a live firewall at the validation of step 5.**

### Swap, read from source for step 5

| Path | Method | Parameters | Citation |
|---|---|---|---|
| `/api/diagnostics/system/systemSwap` | GET | none | https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Diagnostics/Api/SystemController.php |

Listed as `GET diagnostics system system_swap` in the core API reference (https://docs.opnsense.org/development/api/core/diagnostics.html).
`systemSwapAction` returns the JSON of the configd action `system show swapinfo` unchanged
(`src/opnsense/service/conf/actions.d/actions_system.conf`, `[show.swapinfo]`), which runs
`src/opnsense/scripts/system/swapinfo.py` (https://github.com/opnsense/core/blob/26.7.3/src/opnsense/scripts/system/swapinfo.py). The
script runs `/usr/sbin/swapinfo -k`, keeps the lines that contain `/dev/` — so the `Total` line is dropped — and prints
`{"swap": [{"device", "total", "used"}]}`, the second and third columns passed on **as strings**, in **KiB**. A firewall with no swap
device prints `{"swap": []}`.

`opnview` samples `total` and `used` per device, converted to bytes, as `swap_total_bytes` and `swap_used_bytes` under the firewall
subject keyed by the device. A figure that is not a number is no reading; an endpoint that does not answer, or a firewall with no swap
device, is recorded in the measurement provider's availability detail rather than as a zero. It is the second firewall endpoint step 5
adds, by the amendment of 4 October 2026. **Read from source only; the endpoint is to be probed against a live firewall at the
validation of step 5.**

### Gateway status, read from source for step 5

| Path | Method | Parameters | Citation |
|---|---|---|---|
| `/api/routes/gateway/status` | GET | none | https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Routes/Api/GatewayController.php |

Listed as `GET routes gateway status` in the core API reference (https://docs.opnsense.org/development/api/core/routes.html).
`statusAction` returns `{"items": [...], "status": "ok"}`, or `"failed"` with no item when the configd action `interface gateways status`
(`scripts/routes/gateway_status.php`) returned nothing. Each item carries `name`, `address`, `status`, `status_translated`, `loss`, `delay`,
`stddev` and `monitor`. The figures come from dpinger through `dpinger_status()` (`src/etc/inc/plugins.inc.d/dpinger.inc`):
`delay` and `stddev` are `sprintf('%0.1f ms', ...)` and `loss` is `sprintf('%0.1f %%', ...)`, and a gateway with no dpinger figure carries
`~` instead. `status` is `none` (online), `force_down`, `down`, `delay`, `loss` or `delay+loss`.

`opnview` samples `delay`, `stddev` and `loss` per gateway name, writes no reading for a `~`, and records an endpoint that does not answer
— or answers with no gateway — in the measurement provider's availability detail rather than as a zero. It is the first of the two firewall endpoints
step 5 adds. **Read from source only; the endpoint is to be probed against a live firewall at the validation of step 5.**

### Suricata, on that firewall

`enabled: 1`, running on two internal interfaces and not on WAN —
with `eve.json` present and recently modified, and `query_alerts` returning `[]`
for any window up to seven days.

Five rulesets are enabled and current, and all five are **abuse.ch indicator
feeds** (Feodo tracker, SSL blacklist, SSL IP blacklist, ThreatFox, URLhaus):
they fire only on contact with infrastructure already known to be malicious. The
ET Open signature categories are present and not enabled. So the empty result is
a true reading of a correctly working source, not a fault.

**The collector must not treat it as one.** A reachable IDS with a narrow
ruleset and nothing to report is a normal state, and distinguishing it from
"stopped", "no rules at all" and "we could not ask" is the whole point of
`source_availability`.

### On scale, without describing anyone's network

Two shapes matter to the design and neither needs an installation's figures.
The filter log runs fast enough that a 10-second poll must be sized per
installation rather than fixed. And a DNS blocklist can be large while blocking
almost nothing — a product built against the mockup's invented data expects a
busy blocked-DNS view, and a real install may have a handful of rows. Neither
the widget nor the collector may treat that as a fault.

**Nothing in this document records a real installation's interface labels,
device names, addresses or counts.** The probes that produced these findings
were run by the maintainer and their output was never committed.

## What the plugin ecosystem actually exposes, 2026-09-28

Read from `docs.opnsense.org`, the plugin and core sources on GitHub, and the
`configd` action files. **Facts only**; what they imply for the model is a
decision, not a finding, and is not recorded here.

Three limits, stated because they bound everything below. **OPNsense publishes
no install or download telemetry** — `pkg.opnsense.org` serves no counters and
the maintainers cite none in their own plugin-system thread — so no popularity
ranking is possible, including this one. Forum volume was used as a proxy and it
**measures friction, not adoption**: a plugin that installs and works generates
no threads. And Reddit was unreachable, so none of the usual community evidence
is in here.

### Four of the six kinds are fed by core, not by plugins

There is no `os-wireguard`, `os-suricata`, `os-unbound`, `os-dnsmasq` or
`os-kea`: WireGuard is core since 24.1, and the IDS, both resolvers and both
DHCP servers are core. Zenarmor ships as `os-sunnyvalley`; AdGuard Home has no
official package at all and is third-party, console-installed.

### The dominant pattern: configuration and service control, no data

Verified endpoint list by endpoint list. **ntopng, Maltrail, dnscrypt-proxy,
Bind, Caddy, Netdata, Telegraf, collectd and node_exporter expose no data
through the OPNsense API** — configuration and service lifecycle only. For
several of them the data exists on the box, in the tool's own store or its own
API; OPNsense is simply not a route to it. **A connector that can only be handed
an OPNsense session is ruled out of most of the ecosystem by construction.**

### What does produce data, and in what shape

- **CrowdSec** — `crowdsec/alerts/search` is event-shaped; `crowdsec/decisions/search`
  is a **current ban list with a TTL and no timestamp at all**. Both run
  `cscli … -l 0`: no limit, no `since`, so **every poll re-dumps the whole set**.
- **WireGuard** (core) — `wireguard/service/show`: per-peer state plus
  `transfer-rx`/`transfer-tx`, which are **monotonic per service lifetime**, not
  per interval.
- **HAProxy** — `statistics/counters`, keyed by frontend/backend/server, not by
  address pair.
- **nginx** — `logs/accesses` and `service/vts`: per-request HTTP records, an
  application-layer event shape whose dimensions are URL and status code.
- **vnStat** — per-interface volume per period, and **unusable**: the four
  documented endpoints return `vnstat -h|-d|-m|-y` as raw ASCII tables. A
  `--json` `configd` action exists and **no controller exposes it**.
- **FRR** — structured JSON for neighbours and routes; `bfdsummary` and
  `generalrunningconfig` are raw text.
- **LLDPd, NUT, apcupsd, SMART, Tor, Monit, acme-client** — state or measurement,
  several returning unparsed console output.

### Shapes the model has no room for

Two, and the first is not eleven problems but one. **A reconciled set**: the
current things of type X, each with attributes and sometimes a TTL, replaced
wholesale on each poll rather than appended to — WireGuard peers, CrowdSec
decisions, LLDP neighbours, FRR routes, certificates, Monit services, UPnP
mappings, Tor circuits. Every kind in this model is append-only. `interface` and
`dhcp_lease` *are* reconciled state, but as bespoke tables rather than as a
kind, so nothing else can supply that shape. The second is the application-layer
request log, which one source produces.

### Four findings that bear on the collectors already written

- **`provider.is_active` admits one provider per kind.** People run Suricata,
  CrowdSec and Zenarmor together; the how-to corpus is people stacking them.
  Three concurrent `security_event` providers is the normal case.
- **Nothing upstream supports an incremental read.** CrowdSec dumps everything;
  `ids/service/query_alerts` documents no time filter; vnStat returns the whole
  history. This is the same snapshot-versus-append distinction as the missing
  shape above, arriving from the transport side.
- **CrowdSec would double-count.** Its firewall bouncer enforces through pf
  tables and `block drop` rules. *Inferred, and load-bearing*: if those rules
  log, the drops are **already in `firewall_log`**, so emitting decisions as
  blocking events counts each block twice. Emit them as state.
- **Zenarmor cannot be mirrored.** Its own hardware page gives ~5 MB per hour per
  Mbit/s — about 168 GB a month on a 100 Mbit/s link. Aggregates only, and its
  API keys are minted in a cloud console with no published endpoints.

### And one about a source already depended on

The forum record for NetFlow/Insight is not questions but bug reports:
*no data available*, *excessive CPU and disk I/O*, *counting traffic twice*,
*OUT_BYTES and OUT_PACKETS always zero*. `flow_volume` already carries an
observation-point caveat; these are a second class, in the source itself, and
two of them are correctness rather than coverage.

### A population with no resolver source at all

Only Unbound and dnsmasq expose query logs. **Bind and dnscrypt-proxy expose
none** — both endpoint lists checked. So a real set of installations has no
`dns_lookup` source and therefore no site names. `source_availability` models
that correctly as a state rather than as emptiness; the point is that the
affected population is larger than it looks, and the interface copy for it
carries weight.

## Gaps and alternatives

Not everything the roadmap assumed is reachable. Each gap is followed by an alternative that stays inside the project rules. **No
alternative in this document involves reading a file on the firewall, SSH, or any access other than the authenticated REST API.**

1. **Suricata `dns` events do not exist and cannot be enabled.** The IDS model has no `dns` field and the generated Suricata configuration
   emits no `dns` output type. *Alternative:* the resolver query log (data source 5), already the fallback path, becomes the sole source of
   DNS-derived site names.
2. **Suricata `tls` and `http` events can be written to `eve.json` but cannot be read back through the API.** The only endpoint that opens
   `eve.json` returns records carrying a top-level `alert` key and silently discards every other event type, and the generic log endpoint
   cannot open a file not named `.log`. *Alternative:* none within the rules; reported to the maintainer as a roadmap-invalidating finding
   rather than worked around.
3. **Suricata alert records lose their structure.** The backend overwrites the nested alert object with the signature string, so category,
   severity and rule metadata are not returned. *Alternative:* store the signature id and text, and resolve severity and category through
   `/api/ids/settings/get_rule_info/<sid>` (https://docs.opnsense.org/development/api/core/ids.html) on a cache-miss basis.
4. **Per-address-pair volume exists only at daily resolution, for 62 days.** *Alternative:* derive sub-daily matrix volumes from `opnview`'s
   own accumulated history, seeded by the 300 s per-source totals, and use the daily per-pair aggregate for the 7 d and 30 d periods.
5. **NetFlow per-pair records are direction-doubled and port-approximated.** *Alternative:* deduplicate on direction at ingest, and treat
   the stored port as a hint rather than an authoritative destination port — the filter log carries the exact ports.
6. **Dnsmasq offers no structured query API.** *Alternative:* the generic log endpoint with `dnsmasq.log_queries` enabled **by the user in
   the web UI — `opnview` never enables it** — parsing the free-text line. The UI reports "text-parsed" provenance for such names and
   exposes the attribution rate.
7. **Filter-log timestamps carry no year and no timezone.** *Alternative:* normalise on ingest against the firewall's current time, obtained
   from the same API session, and record the assumption.
8. **DNS-over-TLS and DNS-over-HTTPS are a blind spot for data source 5.** A client using an external encrypted resolver never asks the
   firewall's resolver, so its lookups never appear in Unbound's or Dnsmasq's query data and its destinations can only be named by address,
   country and operator. Suricata's `tls` SNI would have covered part of this case, and per gap 2 it cannot be read. *Alternative:* none
   that recovers the names; expose the fallback attribution rate per device, so such a device shows up as poorly attributed rather than
   mis-attributed, and say in the UI that encrypted DNS is the reason.
9. **Traffic inside a segment is invisible.** Not an API gap but a physical one. *Alternative:* none; stated in the UI wherever volumes are
   shown.
10. **The resolver query window is reachable, but only through one call form, and the wrong form fails silently.** `search_queries` honours
    `timeStart`/`timeEnd` only when they arrive as JSON integers; any other encoding returns the 1000 most recent records with HTTP 200 and
    no diagnostic. Not a gap in the data — the windowed query does work — but a gap in the *feedback* the API gives. *Alternative:* call it
    as a JSON-body read-only POST, and verify incrementality on every response by checking the returned rows against the requested window
    and client, raising a collection fault when they disagree.
11. **No source can be distinguished from silence by an empty result alone.** Every source above returns an empty list both when disabled
    and when quiet. *Alternative:* the explicit availability probes documented per source, and a distinct "unavailable" UI state for each.

## Impact on the data model and the collectors

- **The `eve.json` ingestion cursor changes shape.** Not a generic cursor over event types but a per-`fileid` `filepos` watermark over an
  alert-only feed, plus rotation detection. Step 2 should model it as `(source, file_id, byte_offset, observed_at)`, unique on `(file_id,
  byte_offset)`, so a restart cannot double-count.
- **Site-name provenance loses one of its two values in practice.** The model must still carry `observed` versus `inferred` — a future
  release may expose `tls` events — but on 26.7 every attribution is `inferred`, from resolver lookups. The UI's "which method is active"
  indicator must be able to say "no observed source available", and the roadmap's claim that Suricata is the primary path needs revisiting.
- **Volume aggregates cannot be a thin cache over Insight.** Per-pair data is daily-only and per-source data at 300 s is kept for one hour,
  so `opnview`'s own pre-computed 1 h / 24 h / 7 d / 30 d aggregates are the primary store, not an optimisation.
- **Two join keys must be first-class:** raw device name to interface description (filter log), and pf label / rule uuid to rule description
  (`rid`). Both are runtime-discovered maps with their own refresh cycle and "not found" state — a `rid` whose rule no longer exists is
  normal and must render as an unknown rule, not a missing row.
- **Availability is a modelled state, not an absence of rows.** Each of the five sources needs a persisted health record — reachable,
  present-but-disabled, or unavailable — with a timestamp and the probe that determined it, read by every screen.
- **Timestamps need normalisation at three levels:** epoch seconds (resolver, NetFlow, leases), ISO strings (Suricata) and year-less syslog
  strings (filter log). The collector layer converts everything to UTC epoch at the boundary.
- **The scheduler runs at five intervals** — 10 s, 60 s, 300 s/900 s, 300 s, 60 s — each with its own failure isolation, so one unavailable
  source never stalls the others.

## References

Documentation — `docs.opnsense.org`:

- OPNsense API reference, introduction and conventions — https://docs.opnsense.org/development/api.html
- How to use the API (authentication, curl and Python examples) — https://docs.opnsense.org/development/how-tos/api.html
- Core API reference: Diagnostics — https://docs.opnsense.org/development/api/core/diagnostics.html
- Core API reference: Firewall — https://docs.opnsense.org/development/api/core/firewall.html
- Core API reference: Interfaces — https://docs.opnsense.org/development/api/core/interfaces.html
- Core API reference: Ids — https://docs.opnsense.org/development/api/core/ids.html
- Core API reference: Unbound — https://docs.opnsense.org/development/api/core/unbound.html
- Manual: Settings, including Logging retention — https://docs.opnsense.org/manual/settingsmenu.html
- Manual: DHCP overview and available backends — https://docs.opnsense.org/manual/dhcp.html
- Manual: ISC DHCP (end-of-life) — https://docs.opnsense.org/manual/isc.html
- Manual: Kea DHCP — https://docs.opnsense.org/manual/kea.html
- Manual: Dnsmasq DNS and DHCP — https://docs.opnsense.org/manual/dnsmasq.html
- Manual: Unbound DNS — https://docs.opnsense.org/manual/unbound.html
- Manual: Intrusion Prevention System — https://docs.opnsense.org/manual/ips.html
- Manual: NetFlow — https://docs.opnsense.org/manual/netflow.html
- Release notes: 26.7 "Xenial Xenops" series (pinned release; FreeBSD, OpenSSL and PHP versions) — https://docs.opnsense.org/releases/CE_26.7.html
- Release notes: 26.1 "Witty Woodpecker" series (ISC DHCP moved to a plugin) — https://docs.opnsense.org/releases/CE_26.1.html
- Release notes: 25.7 "Visionary Viper" series (API URLs switched to snake_case) — https://docs.opnsense.org/releases/CE_25.7.html

Source — `github.com/opnsense/core`, tag `26.7.3`:

- `ApiControllerBase.php` — authentication, ACL, CSRF exemption, `searchRecordsetBase` — https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Base/ApiControllerBase.php
- `ApiMutableModelControllerBase.php` — `searchBase` and `getBase` have no write path — https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Base/ApiMutableModelControllerBase.php
- `ApiMutableServiceControllerBase.php` — service `status` contract — https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Base/ApiMutableServiceControllerBase.php
- `Mvc/Router.php` — path-to-action mapping and positional parameters — https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/library/OPNsense/Mvc/Router.php
- `Mvc/Request.php` — `get()` reads `$_REQUEST` uncast; why only a JSON body yields integer parameters — https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/library/OPNsense/Mvc/Request.php
- `www/api.php` — front controller, HTTP status codes and error body — https://github.com/opnsense/core/blob/26.7.3/src/opnsense/www/api.php
- `Diagnostics/Api/FirewallController.php` — filter log, stats, stream — https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Diagnostics/Api/FirewallController.php
- `scripts/filter/read_log.py` — filterlog field specification and digest semantics — https://github.com/opnsense/core/blob/26.7.3/src/opnsense/scripts/filter/read_log.py
- `scripts/filter/list_non_mvc_rules.php` — legacy rules; `uuid` carries the pf label — https://github.com/opnsense/core/blob/26.7.3/src/opnsense/scripts/filter/list_non_mvc_rules.php
- `Firewall/Api/FilterController.php` — `search_rule` — https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Firewall/Api/FilterController.php
- `Interfaces/Api/OverviewController.php` — `interfaces_info` fields — https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Interfaces/Api/OverviewController.php
- `Diagnostics/Api/InterfaceController.php` — `get_interface_names` — https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Diagnostics/Api/InterfaceController.php
- `IDS/Api/ServiceController.php` — `query_alerts`, `get_alert_info` — https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/IDS/Api/ServiceController.php
- `IDS/Api/SettingsController.php` — `settings/get` — https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/IDS/Api/SettingsController.php
- `models/OPNsense/IDS/IDS.xml` — `eveLog` has only `http` and `tls`; rotation defaults — https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/models/OPNsense/IDS/IDS.xml
- `templates/OPNsense/IDS/suricata.yaml` — generated eve-log types; no `dns` — https://github.com/opnsense/core/blob/26.7.3/src/opnsense/service/templates/OPNsense/IDS/suricata.yaml
- `templates/OPNsense/IDS/newsyslog.conf` — `eve.json` rotation — https://github.com/opnsense/core/blob/26.7.3/src/opnsense/service/templates/OPNsense/IDS/newsyslog.conf
- `scripts/suricata/queryAlertLog.py` — alert-only filter, pagination, `filepos` — https://github.com/opnsense/core/blob/26.7.3/src/opnsense/scripts/suricata/queryAlertLog.py
- `scripts/suricata/listAlertLogs.py` — rotated file enumeration — https://github.com/opnsense/core/blob/26.7.3/src/opnsense/scripts/suricata/listAlertLogs.py
- `Diagnostics/Api/NetflowController.php` — `is_enabled`, `status` — https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Diagnostics/Api/NetflowController.php
- `Diagnostics/Api/NetworkinsightController.php` — `top` and `timeserie` signatures — https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Diagnostics/Api/NetworkinsightController.php
- `scripts/netflow/lib/aggregates/__init__.py` — aggregate table, `octets`/`packets`, `get_top_data` — https://github.com/opnsense/core/blob/26.7.3/src/opnsense/scripts/netflow/lib/aggregates/__init__.py
- `scripts/netflow/lib/aggregates/interface.py` — `FlowInterfaceTotals` — https://github.com/opnsense/core/blob/26.7.3/src/opnsense/scripts/netflow/lib/aggregates/interface.py
- `scripts/netflow/lib/aggregates/source.py` — `FlowSourceAddrTotals`, `FlowSourceAddrDetails` — https://github.com/opnsense/core/blob/26.7.3/src/opnsense/scripts/netflow/lib/aggregates/source.py
- `scripts/netflow/lib/aggregates/ports.py` — `FlowDstPortTotals` — https://github.com/opnsense/core/blob/26.7.3/src/opnsense/scripts/netflow/lib/aggregates/ports.py
- `Kea/Api/LeasesController.php` — abstract base: `searchAction`, the lease field list and the pagination handling — https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Kea/Api/LeasesController.php
- `Kea/Api/Leases4Controller.php` — concrete DHCPv4 subclass serving `/api/kea/leases4/search` — https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Kea/Api/Leases4Controller.php
- `Kea/Api/Dhcpv4Controller.php` — `dhcpv4.general.enabled` — https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Kea/Api/Dhcpv4Controller.php
- `Kea/Api/ServiceController.php` — Kea service status — https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Kea/Api/ServiceController.php
- `Dnsmasq/Api/LeasesController.php` — lease field list — https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Dnsmasq/Api/LeasesController.php
- `Dnsmasq/Api/ServiceController.php` — Dnsmasq service status and `enable` flag — https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Dnsmasq/Api/ServiceController.php
- `models/OPNsense/Dnsmasq/Dnsmasq.xml` — `enable`, `log_queries`, `dhcp_ranges` — https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/models/OPNsense/Dnsmasq/Dnsmasq.xml
- `Unbound/Api/OverviewController.php` — `search_queries`, `is_enabled`, `totals` — https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Unbound/Api/OverviewController.php
- `Unbound/Api/ServiceController.php` — Unbound service status — https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Unbound/Api/ServiceController.php
- `models/OPNsense/Unbound/Unbound.xml` — `general.enabled` and query logging — https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/models/OPNsense/Unbound/Unbound.xml
- `models/OPNsense/Unbound/Menu/Menu.xml` — log scope `resolver` — https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/models/OPNsense/Unbound/Menu/Menu.xml
- `Diagnostics/Api/LogController.php` — generic log endpoint route shape and scope `dnsmasq` — https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Diagnostics/Api/LogController.php
- `scripts/unbound/stats.py` — query detail fields — https://github.com/opnsense/core/blob/26.7.3/src/opnsense/scripts/unbound/stats.py
- `scripts/unbound/logger.py` — query store schema and 7-day truncation — https://github.com/opnsense/core/blob/26.7.3/src/opnsense/scripts/unbound/logger.py
- `scripts/syslog/log_matcher.py` — log filename resolution; why `eve.json` is unreachable — https://github.com/opnsense/core/blob/26.7.3/src/opnsense/scripts/syslog/log_matcher.py
