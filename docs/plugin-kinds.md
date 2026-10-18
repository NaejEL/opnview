# Plugin kinds

The contracts of every core kind: what an implementation's script exports, and the fields of every
record it answers. It is the reference `docs/plugins.md` cites under *Kinds*. Every table between a
`contract:` marker and its close is rendered from the kind's file,
`internal/collect/provider/kinds/<kind>.json`, and a test of the repository fails when the two differ
(A13), so this document cannot drift from the code that reads a plugin.

The host converts what a script answers against exactly these tables. `flow_volume` is the core's
own and accepts no provider. A kind a plugin declares for itself is described by its manifest
(`docs/plugins.md`, *Kinds of the plugin's own*), not here.

Every function answers a `result`:

<!-- contract:shared -->
#### `result`

What a probe or a read concluded: the availability opnview records for the provider. Every function returns one, under the key `result`.

| Field | Type | Required | Unit or values | Description |
|---|---|---|---|---|
| `state` | text | no | `reachable`, `present_but_disabled`, `unavailable` | The availability state the firewall's answer implies; absent only beside an error raised before any answer was read. |
| `outcome` | text | no |  | Why, as one of the closed outcome codes of docs/data-model.md, `source_availability`; absent only beside an error, as state is. |
| `probe` | text | yes |  | The name of the declared endpoint whose answer decided it. |
| `detail` | text | no |  | What happened, in words, for a person reading the availability. |
| `separable` | boolean | no |  | Whether the firewall's own configuration marks this implementation as the one serving clients; the probe round activates only a separable one. |
<!-- /contract:shared -->

<!-- contract:dhcp_lease -->
### `dhcp_lease`

The lease table of a DHCP server: data source 4. Several providers may be active at once (one server per scope); the kind runs the client identity cascade on what they report.

| Function | Called with | Returns | When |
|---|---|---|---|
| `probe` | `host` | `probe_answer` | Called at every probe round: asks the firewall whether this implementation can be read and whether its configuration serves clients. |
| `leases` | `host, page_size` | `leases_answer` | Called at every lease pass: the server's current lease table. |

#### `probe_answer`

What `probe` returns.

| Field | Type | Required | Unit or values | Description |
|---|---|---|---|---|
| `result` | record of `result` | yes |  | The `result` record. |
| `error` | text | no |  | Why the probe could not finish, when it could not. |

#### `leases_answer`

What `leases` returns.

| Field | Type | Required | Unit or values | Description |
|---|---|---|---|---|
| `result` | record of `result` | yes |  | The `result` record. |
| `leases` | records of `dhcp_lease_record` | no |  | The `dhcp_lease_record` records read, absent when the read failed. |
| `error` | text | no |  | Why the read failed, when it did; the result then says what the firewall answered. |
| `unsupported` | text | no |  | Why this implementation's material can never be read, when that is so: a state, recorded with its reason, and not a failure. |

#### `dhcp_lease_record`

One lease generation, with the field-name differences between backends normalised away.

| Field | Type | Required | Unit or values | Description |
|---|---|---|---|---|
| `address` | text | yes |  | The leased address. |
| `mac` | text | no |  | The hardware address; the host lower-cases it and stores a value that is not six colon-separated octets as absent. |
| `hostname` | text | no |  | The host name the client sent. |
| `dhcp_client_id` | text | no |  | The DHCP client identifier. |
| `duid` | text | no |  | The DHCPv6 DUID. |
| `iaid` | text | no |  | The DHCPv6 IAID. |
| `vendor_hint` | text | no |  | The vendor the server names for the address. |
| `lease_state` | text | yes | `active`, `expired`, `reserved`, `unknown` | The lease's state. |
| `interface_identifier` | text | no |  | The interface's configuration key, when the server names it. |
| `interface_device` | text | no |  | The interface's network device, when the server names it. |
| `starts_at` | integer | no | UTC epoch seconds | The validity start, only when the server reports one; never substituted. |
| `expires_at` | integer | no | UTC epoch seconds | The expiry. |
<!-- /contract:dhcp_lease -->

<!-- contract:dns_lookup -->
### `dns_lookup`

The lookups a resolver answered, per querying client: data source 5, the sole source of site names. The kind never presents what it read as coverage of a window.

| Function | Called with | Returns | When |
|---|---|---|---|
| `probe` | `host` | `probe_answer` | Called at every probe round: asks the firewall whether this implementation can be read and whether its configuration serves clients. |
| `lookups` | `host, page` | `lookups_answer` | Called for pages 1 to page_bound at every lookup pass. |

Values the script exports beside the functions:

#### `values`

The exported values.

| Field | Type | Required | Unit or values | Description |
|---|---|---|---|---|
| `page_bound` | integer | yes |  | How many pages one pass may walk, 1 or more. |
| `requested_span_seconds` | integer | yes | seconds | The window the implementation asks for, 0 or more; never presented as coverage. |

#### `probe_answer`

What `probe` returns.

| Field | Type | Required | Unit or values | Description |
|---|---|---|---|---|
| `result` | record of `result` | yes |  | The `result` record. |
| `error` | text | no |  | Why the probe could not finish, when it could not. |

#### `lookups_answer`

What `lookups` returns.

| Field | Type | Required | Unit or values | Description |
|---|---|---|---|---|
| `result` | record of `result` | yes |  | The `result` record. |
| `lookups` | records of `dns_lookup_record` | no |  | The `dns_lookup_record` records read, absent when the read failed. |
| `error` | text | no |  | Why the read failed, when it did; the result then says what the firewall answered. |
| `unsupported` | text | no |  | Why this implementation's material can never be read, when that is so: a state, recorded with its reason, and not a failure. |

#### `dns_lookup_record`

One lookup, decoded and normalised.

| Field | Type | Required | Unit or values | Description |
|---|---|---|---|---|
| `deduplication_key` | text | yes |  | The lookup's identity, composed by the implementation; a lookup read twice is stored once. |
| `client_address` | text | yes |  | The querying client's address, or the host name the resolver logged for it. |
| `client_family` | integer | no |  | 4 or 6: the querying address's family. |
| `domain` | text | yes |  | The name looked up. |
| `action` | text | yes | `pass`, `block`, `drop`, `unknown` | The resolver's verdict. |
| `answer_source` | text | no |  | Where the answer came from. |
| `rcode` | text | no |  | The response code. |
| `dnssec_status` | text | no |  | The DNSSEC status. |
| `blocklist_name` | text | no |  | The list that refused it, when one did and the resolver names it. |
| `looked_up_at` | integer | yes | UTC epoch seconds | When the lookup was made. |
<!-- /contract:dns_lookup -->

<!-- contract:firewall_log -->
### `firewall_log`

Records of packets the firewall allowed or denied: data source 1. The kind keeps no server-side cursor; it reads the newest page and records a gap when the oldest record returned is newer than the newest stored.

| Function | Called with | Returns | When |
|---|---|---|---|
| `probe` | `host` | `probe_answer` | Called at every probe round: asks the firewall whether this implementation can be read and whether its configuration serves clients. |
| `records` | `host, page_size` | `records_answer` | Called at every filter-log pass: the newest page_size records, newest first. |

Values the script exports beside the functions:

#### `values`

The exported values.

| Field | Type | Required | Unit or values | Description |
|---|---|---|---|---|
| `state_timeout_tcp_first_seconds` | integer | no | seconds | pf's timeout of a TCP state after its first packet (pf.conf `tcp.first`): how long a connection that never left its first packet stays idle and keeps its state. |
| `state_timeout_tcp_established_seconds` | integer | no | seconds | pf's timeout of an established TCP state (pf.conf `tcp.established`). |
| `state_timeout_tcp_closing_seconds` | integer | no | seconds | pf's timeout of a TCP state once a FIN was sent (pf.conf `tcp.closing`). |
| `state_timeout_tcp_closed_seconds` | integer | no | seconds | pf's timeout of a TCP state once an RST was sent (pf.conf `tcp.closed`). |
| `state_timeout_udp_multiple_seconds` | integer | no | seconds | pf's timeout of a UDP state with packets both ways (pf.conf `udp.multiple`). |
| `state_timeout_icmp_first_seconds` | integer | no | seconds | pf's timeout of an ICMP state after its first packet (pf.conf `icmp.first`). |
| `state_timeout_other_multiple_seconds` | integer | no | seconds | pf's timeout of a state of any other protocol with packets both ways (pf.conf `other.multiple`). |

#### `probe_answer`

What `probe` returns.

| Field | Type | Required | Unit or values | Description |
|---|---|---|---|---|
| `result` | record of `result` | yes |  | The `result` record. |
| `error` | text | no |  | Why the probe could not finish, when it could not. |

#### `records_answer`

What `records` returns.

| Field | Type | Required | Unit or values | Description |
|---|---|---|---|---|
| `result` | record of `result` | yes |  | The `result` record. |
| `records` | records of `firewall_log_record` | no |  | The `firewall_log_record` records read, absent when the read failed. |
| `error` | text | no |  | Why the read failed, when it did; the result then says what the firewall answered. |
| `unsupported` | text | no |  | Why this implementation's material can never be read, when that is so: a state, recorded with its reason, and not a failure. |

#### `firewall_log_record`

One filter-log record, decoded and normalised.

| Field | Type | Required | Unit or values | Description |
|---|---|---|---|---|
| `digest` | text | yes |  | The record's identity; a record read twice is stored once. |
| `observed_at` | integer | yes | UTC epoch seconds | When the packet was logged. |
| `device` | text | yes |  | The network device the record names; the kind resolves it to an interface. |
| `src_address` | text | yes |  | The source address. |
| `dst_address` | text | yes |  | The destination address. |
| `src_port` | integer | no |  | The source port, 0 to 65535. |
| `dst_port` | integer | no |  | The destination port, 0 to 65535. |
| `protocol` | text | yes |  | The protocol's name; empty when the record names none. |
| `ip_version` | integer | yes |  | 4 or 6. |
| `action` | text | yes | `pass`, `block`, `reject`, `unknown` | The decision. |
| `direction` | text | yes | `in`, `out`, `unknown` | The direction the packet crossed the interface. |
| `log_reason` | text | no |  | The reason the record was logged. |
| `packet_bytes` | integer | yes | bytes | The logged packet length, 0 or more. |
| `rid` | text | no |  | The rule's label, which the kind resolves to a rule. |
| `ip_id` | integer | no |  | The IPv4 identification, which pairs the two records of one connection. |
| `tcp_seq` | integer | no |  | The TCP sequence number, the other pairing field. |
<!-- /contract:firewall_log -->

<!-- contract:ip_geolocation -->
### `ip_geolocation`

Places public addresses: each address's country, coordinates, autonomous system and operator. One provider is active at a time. The core decides which addresses need placing -- never a non-public one -- stores the answers in geo_asn and places every address again when the active provider or its build changes.

| Function | Called with | Returns | When |
|---|---|---|---|
| `status` | `host` | `dataset_answer` | Called at start and after every refresh: whether a dataset is held, and its build. |
| `refresh` | `host` | `dataset_answer` | Called at refresh_interval_geoip_seconds: acquire or update the dataset. |
| `lookup` | `host, addresses` | `lookup_answer` | Called at poll_interval_geo_lookup_seconds with a batch of public addresses. |

#### `dataset_answer`

What `status` and `refresh` return.

| Field | Type | Required | Unit or values | Description |
|---|---|---|---|---|
| `result` | record of `result` | yes |  | The `result` record; its probe is a word naming what the last refresh found. |
| `ready` | boolean | yes |  | Whether a dataset is held and answers lookups. |
| `build` | integer | no | UTC epoch seconds | The build of the dataset held, which every answer is recorded against. |
| `error` | text | no |  | Why the refresh failed, when it did. |

#### `lookup_answer`

What `lookup` returns.

| Field | Type | Required | Unit or values | Description |
|---|---|---|---|---|
| `build` | integer | yes | UTC epoch seconds | The build the answers come from. |
| `answers` | records of `geolocation` | yes |  | One `geolocation` record per address asked about. |
| `error` | text | no |  | Why the lookups failed, when they did. |

#### `geolocation`

What the dataset says about one address.

| Field | Type | Required | Unit or values | Description |
|---|---|---|---|---|
| `address` | text | yes |  | The address asked about. |
| `country_code` | text | no |  | The ISO 3166-1 country code; absent is a miss. |
| `country_name` | text | no |  | The country's English name. |
| `latitude` | number | no |  | The latitude. |
| `longitude` | number | no |  | The longitude. |
| `asn` | integer | no |  | The autonomous system number. |
| `operator` | text | no |  | The autonomous system's organisation. |
<!-- /contract:ip_geolocation -->

<!-- contract:measurement_sample -->
### `measurement_sample`

A numeric reading of a subject at an instant: the firewall's gauges, sampled rates. Several providers may be active at once. A reading that did not answer is named in `missing`, never written as a zero.

| Function | Called with | Returns | When |
|---|---|---|---|
| `probe` | `host` | `probe_answer` | Called at every probe round: asks the firewall whether this implementation can be read and whether its configuration serves clients. |
| `sample` | `host, discovery, now` | `sample_answer` | Called at every sampling pass. discovery holds the interfaces the firewall reported (identifiers, devices); now is the pass's instant. |

#### `probe_answer`

What `probe` returns.

| Field | Type | Required | Unit or values | Description |
|---|---|---|---|---|
| `result` | record of `result` | yes |  | The `result` record. |
| `error` | text | no |  | Why the probe could not finish, when it could not. |

#### `sample_answer`

What `sample` returns.

| Field | Type | Required | Unit or values | Description |
|---|---|---|---|---|
| `result` | record of `result` | yes |  | The `result` record. |
| `readings` | records of `measurement_reading` | no |  | The `measurement_reading` records. |
| `missing` | text_list | no |  | The readings that did not answer, each named in words for the availability detail. |
| `error` | text | no |  | Why the pass failed, when it did. |

#### `measurement_reading`

One reading.

| Field | Type | Required | Unit or values | Description |
|---|---|---|---|---|
| `subject_kind` | text | yes |  | What is measured: a lower-case word with no space (interface, interface_endpoint, gateway, firewall, or the plugin's own). |
| `subject_key` | text | yes |  | Which one, in the provider's own words: an identity the host never composes or parses; may be empty. |
| `interface_device` | text | no |  | The network device the reading was taken on, when the subject is read on one interface (interface_endpoint, interface_endpoint_pair). |
| `local_address` | text | no |  | The local address the reading is of, when the subject is an address read on one interface. |
| `peer_address` | text | no |  | The peer address the local address was read with, when the subject is a pair; the reading's inbound figures are what the peer sent. |
| `measure` | text | yes |  | What figure: a lower-case word with no space. |
| `unit` | text | yes |  | Its unit: a lower-case word with no space. |
| `value` | number | yes |  | The figure. |
| `sampled_at` | integer | yes | UTC epoch seconds | When it was read. |
| `attributed` | boolean | no |  | Whether the reading names this provider; false for the firewall reporting on itself. True when absent. |
<!-- /contract:measurement_sample -->

<!-- contract:neighbour -->
### `neighbour`

The firewall's neighbour tables, ARP for IPv4 and NDP for IPv6: a MAC, an address and the interface each neighbour was learned on. On a firewall whose DHCP is unreadable they are the only client identity there is; the kind runs the client identity cascade at the MAC level on what they report, never makes this firewall a client, and never makes a neighbour of an upstream interface one.

| Function | Called with | Returns | When |
|---|---|---|---|
| `probe` | `host` | `probe_answer` | Called at every probe round: asks the firewall whether this implementation can be read. |
| `neighbours` | `host` | `neighbours_answer` | Called at every neighbour pass: every neighbour the tables hold now. |

#### `probe_answer`

What `probe` returns.

| Field | Type | Required | Unit or values | Description |
|---|---|---|---|---|
| `result` | record of `result` | yes |  | The `result` record. |
| `error` | text | no |  | Why the probe could not finish, when it could not. |

#### `neighbours_answer`

What `neighbours` returns.

| Field | Type | Required | Unit or values | Description |
|---|---|---|---|---|
| `result` | record of `result` | yes |  | The `result` record. |
| `neighbours` | records of `neighbour_record` | no |  | The `neighbour_record` records read, absent when the read failed. |
| `error` | text | no |  | Why the read failed, when it did; the result then says what the firewall answered. |

#### `neighbour_record`

One neighbour: an address the firewall resolved to a hardware address on one of its interfaces.

| Field | Type | Required | Unit or values | Description |
|---|---|---|---|---|
| `address` | text | yes |  | The neighbour's address. |
| `mac` | text | no |  | The hardware address; the host lower-cases it and stores a value that is not six colon-separated octets as absent, and a neighbour with none creates no client. |
| `interface_device` | text | no |  | The network device the neighbour was learned on. |
| `vendor_hint` | text | no |  | The vendor the firewall names for the hardware address: context, never a basis for classifying a machine. |
<!-- /contract:neighbour -->

<!-- contract:reconciled_state -->
### `reconciled_state`

The complete set of things of one type as of one instant: a ban list, the peers of a tunnel. A thing present in one snapshot and absent from the next has left. Several providers may be active at once.

| Function | Called with | Returns | When |
|---|---|---|---|
| `probe` | `host` | `probe_answer` | Called at every probe round: asks the firewall whether this implementation can be read and whether its configuration serves clients. |
| `snapshot` | `host` | `snapshot_answer` | Called at the plugin's cadence: every set the provider reports, each complete. |

#### `probe_answer`

What `probe` returns.

| Field | Type | Required | Unit or values | Description |
|---|---|---|---|---|
| `result` | record of `result` | yes |  | The `result` record. |
| `error` | text | no |  | Why the probe could not finish, when it could not. |

#### `snapshot_answer`

What `snapshot` returns.

| Field | Type | Required | Unit or values | Description |
|---|---|---|---|---|
| `result` | record of `result` | yes |  | The `result` record. |
| `sets` | records of `state_set` | no |  | The `state_set` records. |
| `error` | text | no |  | Why the read failed, when it did. |

#### `state_set`

One complete set.

| Field | Type | Required | Unit or values | Description |
|---|---|---|---|---|
| `set_key` | text | yes |  | Which set: a lower-case word with no space. |
| `items` | records of `state_item` | yes |  | The `state_item` records; an empty list is a set that is now empty. |

#### `state_item`

One member of a set.

| Field | Type | Required | Unit or values | Description |
|---|---|---|---|---|
| `item_key` | text | yes |  | Its identity within the set. |
| `attributes` | object | no |  | The provider's own fields, displayed and never aggregated. |
| `valid_until_at` | integer | no | UTC epoch seconds | The end of its own validity, when the provider states one. |
<!-- /contract:reconciled_state -->

<!-- contract:registrable_domain -->
### `registrable_domain`

Groups site names by their registrable domain, at read time; no stored name is ever altered. One provider is active at a time; with none, no grouping is offered.

| Function | Called with | Returns | When |
|---|---|---|---|
| `status` | `host` | `list_answer` | Called at start and after every refresh: whether a list is held. |
| `refresh` | `host` | `list_answer` | Called at refresh_interval_public_suffix_list_seconds: acquire or update the list. |
| `group` | `host, names` | `group_answer` | Called while a widget is served, with the site names to group. |

#### `list_answer`

What `status` and `refresh` return.

| Field | Type | Required | Unit or values | Description |
|---|---|---|---|---|
| `result` | record of `result` | yes |  | The `result` record; its probe is a word naming what the last refresh found. |
| `ready` | boolean | yes |  | Whether a list is held. |
| `version` | text | no |  | The list's own version, when it carries one. |
| `rules` | integer | no |  | How many rules the list holds. |
| `confirmed_at` | integer | no | UTC epoch seconds | The last instant a refresh confirmed the list current. |
| `failed` | boolean | no |  | Whether this refresh failed, which the core records as a collection gap from confirmed_at. |
| `error` | text | no |  | Why the refresh failed, when it did. |

#### `group_answer`

What `group` returns.

| Field | Type | Required | Unit or values | Description |
|---|---|---|---|---|
| `groups` | records of `grouping` | yes |  | One `grouping` record per name. |
| `error` | text | no |  | Why the grouping failed, when it did. |

#### `grouping`

One name's registrable domain.

| Field | Type | Required | Unit or values | Description |
|---|---|---|---|---|
| `name` | text | yes |  | The site name, verbatim. |
| `registrable_domain` | text | no |  | Its registrable domain; absent when the name has none, which groups it on its own. |
<!-- /contract:registrable_domain -->

<!-- contract:resolver_cache -->
### `resolver_cache`

The records a resolver holds in its cache, with the seconds each has left. The kind stores A, AAAA and CNAME records as observations and counts every other type.

| Function | Called with | Returns | When |
|---|---|---|---|
| `probe` | `host` | `probe_answer` | Called at every probe round: asks the firewall whether this implementation can be read and whether its configuration serves clients. |
| `cache` | `host` | `dump_answer` | Called at every cache pass: every record the cache holds now. |

#### `probe_answer`

What `probe` returns.

| Field | Type | Required | Unit or values | Description |
|---|---|---|---|---|
| `result` | record of `result` | yes |  | The `result` record. |
| `error` | text | no |  | Why the probe could not finish, when it could not. |

#### `dump_answer`

What `cache` and `local_data` return.

| Field | Type | Required | Unit or values | Description |
|---|---|---|---|---|
| `result` | record of `result` | yes |  | The `result` record. |
| `records` | records of `resolver_record` | no |  | The `resolver_record` records, of every type. |
| `response_bytes` | integer | no | bytes | The size of the answer. |
| `not_records` | records of `not_record_count` | no |  | The `not_record_count` records: rows the implementation recognised as not being records at all. |
| `error` | text | no |  | Why the read failed, when it did. |
| `unsupported` | text | no |  | Why this resolver's records can never be read, when that is so. |

#### `resolver_record`

One record as the endpoint returned it.

| Field | Type | Required | Unit or values | Description |
|---|---|---|---|---|
| `owner_name` | text | yes |  | The owner name. |
| `ttl` | text | no |  | The TTL as text. |
| `ttl_shape` | text | yes | `absent`, `null`, `string`, `other` | The JSON shape the TTL arrived in. |
| `rrtype` | text | yes |  | The record type. |
| `value` | text | yes |  | The record's value. |

#### `not_record_count`

A number of rows that are not records.

| Field | Type | Required | Unit or values | Description |
|---|---|---|---|---|
| `count` | integer | yes |  | How many. |
| `wording` | text | yes |  | What they are, worded so that "skipped <count> <wording>" reads as a sentence. |
<!-- /contract:resolver_cache -->

<!-- contract:resolver_local_data -->
### `resolver_local_data`

The records a resolver serves from its local data, host overrides among them.

| Function | Called with | Returns | When |
|---|---|---|---|
| `probe` | `host` | `probe_answer` | Called at every probe round: asks the firewall whether this implementation can be read and whether its configuration serves clients. |
| `local_data` | `host` | `dump_answer` | Called at every local-data pass: every record of the local data now. |

#### `probe_answer`

What `probe` returns.

| Field | Type | Required | Unit or values | Description |
|---|---|---|---|---|
| `result` | record of `result` | yes |  | The `result` record. |
| `error` | text | no |  | Why the probe could not finish, when it could not. |

#### `dump_answer`

What `cache` and `local_data` return.

| Field | Type | Required | Unit or values | Description |
|---|---|---|---|---|
| `result` | record of `result` | yes |  | The `result` record. |
| `records` | records of `resolver_record` | no |  | The `resolver_record` records, of every type. |
| `response_bytes` | integer | no | bytes | The size of the answer. |
| `not_records` | records of `not_record_count` | no |  | The `not_record_count` records: rows the implementation recognised as not being records at all. |
| `error` | text | no |  | Why the read failed, when it did. |
| `unsupported` | text | no |  | Why this resolver's records can never be read, when that is so. |

#### `resolver_record`

One record as the endpoint returned it.

| Field | Type | Required | Unit or values | Description |
|---|---|---|---|---|
| `owner_name` | text | yes |  | The owner name. |
| `ttl` | text | no |  | The TTL as text. |
| `ttl_shape` | text | yes | `absent`, `null`, `string`, `other` | The JSON shape the TTL arrived in. |
| `rrtype` | text | yes |  | The record type. |
| `value` | text | yes |  | The record's value. |

#### `not_record_count`

A number of rows that are not records.

| Field | Type | Required | Unit or values | Description |
|---|---|---|---|---|
| `count` | integer | yes |  | How many. |
| `wording` | text | yes |  | What they are, worded so that "skipped <count> <wording>" reads as a sentence. |
<!-- /contract:resolver_local_data -->

<!-- contract:security_event -->
### `security_event`

Intrusion-detection events: data source 2. Several providers may be active at once; every event names the provider that reported it, and the kind keeps a watermark per file.

| Function | Called with | Returns | When |
|---|---|---|---|
| `probe` | `host` | `probe_answer` | Called at every probe round: asks the firewall whether this implementation can be read and whether its configuration serves clients. |
| `rotation` | `host` | `rotation_answer` | Called before every read: which of the feed's files still exist, so a lost file is recorded as a permanent gap. |
| `alerts` | `host, page_size` | `alerts_answer` | Called at every security-event pass: the newest page_size events, newest first. |

#### `probe_answer`

What `probe` returns.

| Field | Type | Required | Unit or values | Description |
|---|---|---|---|---|
| `result` | record of `result` | yes |  | The `result` record. |
| `error` | text | no |  | Why the probe could not finish, when it could not. |

#### `rotation_answer`

What `rotation` returns.

| Field | Type | Required | Unit or values | Description |
|---|---|---|---|---|
| `result` | record of `result` | yes |  | The `result` record. |
| `known` | boolean | yes |  | Whether the files could be listed; false declares nothing lost. |
| `present` | text_list | no |  | The file identifiers the feed still carries. |
| `highest_sequence` | integer | no |  | The current file's sequence, when the feed reports one. |
| `error` | text | no |  | Why the listing failed, when it did. |

#### `alerts_answer`

What `alerts` returns.

| Field | Type | Required | Unit or values | Description |
|---|---|---|---|---|
| `result` | record of `result` | yes |  | The `result` record. |
| `alerts` | records of `security_event_alert` | no |  | The `security_event_alert` records read, absent when the read failed. |
| `error` | text | no |  | Why the read failed, when it did; the result then says what the firewall answered. |
| `unsupported` | text | no |  | Why this implementation's material can never be read, when that is so: a state, recorded with its reason, and not a failure. |

#### `security_event_alert`

One event, decoded and normalised.

| Field | Type | Required | Unit or values | Description |
|---|---|---|---|---|
| `file_id` | text | yes |  | The file the event was read from; with byte_offset, the coordinate the watermark keeps. |
| `byte_offset` | integer | yes |  | Where in the file. |
| `occurred_at` | integer | yes | UTC epoch seconds | When the event happened. |
| `rule_identity` | text | yes |  | The rule or signature's identifier. |
| `signature` | text | yes |  | The signature's text; empty when the event carries none. |
| `event_action` | text | yes | `blocked`, `allowed`, `unknown` | What the engine did. |
| `normalised_severity` | text | no |  | The severity, when the event carries one. |
| `src_address` | text | yes |  | The source address. |
| `dst_address` | text | yes |  | The destination address. |
| `src_port` | integer | no |  | The source port, 0 to 65535. |
| `dst_port` | integer | no |  | The destination port, 0 to 65535. |
| `protocol` | text | no |  | The protocol. |
| `in_interface_device` | text | no |  | The device the event was seen on. |
<!-- /contract:security_event -->
