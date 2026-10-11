package provider

import (
	"sort"
	"strings"
)

// The record contract of every kind, defined once.
//
// A KIND IS A CONTRACT, AND THIS IS THE CONTRACT WRITTEN DOWN AS DATA. Each kind a plugin may
// implement publishes here what an implementation hands it: the functions its script exports,
// what each takes and returns, and the fields of every record, with their types, units and
// whether they are required. The plugin host converts what a script returns against exactly
// these tables, so a field the contract does not hold is refused and a required one that is
// missing is refused, both with the closed code records_invalid, and nothing of that answer is
// stored. docs/plugins.md carries the same tables, and a test asserts they are identical, so the
// document a third party writes a plugin from cannot drift from the code that reads the plugin.
//
// WHAT IS NOT HERE: derivation. A firewall_log record becomes a flow, and pairing,
// classification, attribution and aggregation run on it, in internal/collect and
// internal/store, exactly as for a built-in. A contract says what a source reports; what opnview
// makes of it is the core's.
//
// A KIND A PLUGIN DECLARES has no contract here: its contract is its own tables (docs/plugins.md,
// "A kind of your own"), declared in its manifest, and a source set (internal/collect/sources)
// knows it beside the core kinds below.

// FieldType is the JSON type a record field takes, and how the host reads it.
type FieldType string

// The field types.
const (
	// FieldText is a JSON string.
	FieldText FieldType = "text"
	// FieldInteger is a JSON number with no fractional part.
	FieldInteger FieldType = "integer"
	// FieldNumber is any JSON number.
	FieldNumber FieldType = "number"
	// FieldBoolean is true or false.
	FieldBoolean FieldType = "boolean"
	// FieldTextList is an array of strings.
	FieldTextList FieldType = "text_list"
	// FieldObject is a JSON object, stored as its JSON text.
	FieldObject FieldType = "object"
	// FieldRecord is one record of another table of the same contract, named by Of.
	FieldRecord FieldType = "record"
	// FieldRecords is an array of records of another table of the same contract, named by Of.
	FieldRecords FieldType = "records"
)

// Field is one field of a record.
type Field struct {
	// Name is the field's name as a script writes it: snake_case.
	Name string
	// Type is its JSON type.
	Type FieldType
	// Required says the field must be present and not null. An optional field may be absent
	// or null, and the host then stores it as absent -- never as a zero.
	Required bool
	// Of names the table of a record or records field.
	Of string
	// Values, when not empty, is the closed vocabulary of a text field.
	Values []string
	// Unit is the unit of a numeric field, or its encoding ("UTC epoch seconds").
	Unit string
	// Description says what the field is, in a sentence.
	Description string
}

// Record is one table of a contract: a record a function returns, or an element of one.
type Record struct {
	// Name is the table's name in docs/plugins.md.
	Name string
	// Description says what one record is.
	Description string
	// Fields are its fields, in the order the document lists them.
	Fields []Field
}

// Function is one function a kind's script exports.
type Function struct {
	// Name is the exported function's name.
	Name string
	// Arguments describes what it is called with, after the host object.
	Arguments string
	// Returns names the record the function returns.
	Returns string
	// Description says when the host calls it.
	Description string
}

// Contract is one kind's whole contract.
type Contract struct {
	// Kind is the kind's registry name.
	Kind string
	// Description says what the kind is.
	Description string
	// Functions are what an implementation's script exports under the kind's name.
	Functions []Function
	// Values are the plain values the script may export beside the functions, as fields.
	Values []Field
	// Records are the record tables, the answer of each function first.
	Records []Record
}

// Record returns one of the contract's record tables by name.
func (c Contract) Record(name string) (Record, bool) {
	for _, record := range c.Records {
		if record.Name == name {
			return record, true
		}
	}
	return Record{}, false
}

// Field returns one of the record's fields by name.
func (r Record) Field(name string) (Field, bool) {
	for _, field := range r.Fields {
		if field.Name == name {
			return field, true
		}
	}
	return Field{}, false
}

// The result every probe and read reports, shared by every kind.
var resultRecord = Record{
	Name: "result",
	Description: "What a probe or a read concluded: the availability opnview records for the provider. " +
		"Every function returns one, under the key `result`.",
	Fields: []Field{
		{Name: "state", Type: FieldText, Values: []string{"reachable", "present_but_disabled",
			"unavailable"}, Description: "The availability state the firewall's answer implies; absent only beside an " +
			"error raised before any answer was read."},
		{Name: "outcome", Type: FieldText, Description: "Why, as one of the closed outcome " +
			"codes of docs/data-model.md, `source_availability`; absent only beside an error, as state is."},
		{Name: "probe", Type: FieldText, Required: true, Description: "The name of the declared endpoint " +
			"whose answer decided it."},
		{Name: "detail", Type: FieldText, Description: "What happened, in words, for a person reading the " +
			"availability."},
		{Name: "separable", Type: FieldBoolean, Description: "Whether the firewall's own configuration marks " +
			"this implementation as the one serving clients; the probe round activates only a separable one."},
	},
}

// The answer of a read: its result, and either its records, an error, or the statement that the
// material cannot be read.
func answerRecord(name, recordsField, recordsTable, description string) Record {
	return Record{
		Name:        name,
		Description: description,
		Fields: []Field{
			{Name: "result", Type: FieldRecord, Of: "result", Required: true, Description: "The `result` record."},
			{Name: recordsField, Type: FieldRecords, Of: recordsTable, Description: "The `" + recordsTable + "` records read, " +
				"absent when the read failed."},
			{Name: "error", Type: FieldText, Description: "Why the read failed, when it did; the result then " +
				"says what the firewall answered."},
			{Name: "unsupported", Type: FieldText, Description: "Why this implementation's material can never " +
				"be read, when that is so: a state, recorded with its reason, and not a failure."},
		},
	}
}

// probeAnswer is what a probe returns.
var probeAnswer = Record{
	Name:        "probe_answer",
	Description: "What `probe` returns.",
	Fields: []Field{
		{Name: "result", Type: FieldRecord, Of: "result", Required: true, Description: "The `result` record."},
		{Name: "error", Type: FieldText, Description: "Why the probe could not finish, when it could not."},
	},
}

// probeFunction is the function every kind exports.
var probeFunction = Function{Name: "probe", Arguments: "host", Returns: "probe_answer",
	Description: "Called at every probe round: asks the firewall whether this implementation can be read and " +
		"whether its configuration serves clients."}

// coreContracts are the contracts of the core kinds that accept plugin providers.
var coreContracts = []Contract{
	{
		Kind: "firewall_log",
		Description: "Records of packets the firewall allowed or denied: data source 1. The kind keeps no " +
			"server-side cursor; it reads the newest page and records a gap when the oldest record returned " +
			"is newer than the newest stored.",
		Functions: []Function{probeFunction, {Name: "records", Arguments: "host, page_size", Returns: "records_answer",
			Description: "Called at every filter-log pass: the newest page_size records, newest first."}},
		Records: []Record{
			probeAnswer,
			answerRecord("records_answer", "records", "firewall_log_record", "What `records` returns."),
			{Name: "firewall_log_record", Description: "One filter-log record, decoded and normalised.",
				Fields: []Field{
					{Name: "digest", Type: FieldText, Required: true, Description: "The record's identity; a record " +
						"read twice is stored once."},
					{Name: "observed_at", Type: FieldInteger, Required: true, Unit: "UTC epoch seconds",
						Description: "When the packet was logged."},
					{Name: "device", Type: FieldText, Required: true, Description: "The network device the record " +
						"names; the kind resolves it to an interface."},
					{Name: "src_address", Type: FieldText, Required: true, Description: "The source address."},
					{Name: "dst_address", Type: FieldText, Required: true, Description: "The destination address."},
					{Name: "src_port", Type: FieldInteger, Description: "The source port, 0 to 65535."},
					{Name: "dst_port", Type: FieldInteger, Description: "The destination port, 0 to 65535."},
					{Name: "protocol", Type: FieldText, Required: true, Description: "The protocol's name; empty " +
						"when the record names none."},
					{Name: "ip_version", Type: FieldInteger, Required: true, Description: "4 or 6."},
					{Name: "action", Type: FieldText, Required: true, Values: []string{"pass", "block", "reject",
						"unknown"}, Description: "The decision."},
					{Name: "direction", Type: FieldText, Required: true, Values: []string{"in", "out", "unknown"},
						Description: "The direction the packet crossed the interface."},
					{Name: "log_reason", Type: FieldText, Description: "The reason the record was logged."},
					{Name: "packet_bytes", Type: FieldInteger, Required: true, Unit: "bytes",
						Description: "The logged packet length, 0 or more."},
					{Name: "rid", Type: FieldText, Description: "The rule's label, which the kind resolves to a rule."},
					{Name: "ip_id", Type: FieldInteger, Description: "The IPv4 identification, which pairs the two " +
						"records of one connection."},
					{Name: "tcp_seq", Type: FieldInteger, Description: "The TCP sequence number, the other pairing " +
						"field."},
				}},
		},
	},
	{
		Kind: "security_event",
		Description: "Intrusion-detection events: data source 2. Several providers may be active at once; every " +
			"event names the provider that reported it, and the kind keeps a watermark per file.",
		Functions: []Function{probeFunction,
			{Name: "rotation", Arguments: "host", Returns: "rotation_answer",
				Description: "Called before every read: which of the feed's files still exist, so a lost file is " +
					"recorded as a permanent gap."},
			{Name: "alerts", Arguments: "host, page_size", Returns: "alerts_answer",
				Description: "Called at every security-event pass: the newest page_size events, newest first."}},
		Records: []Record{
			probeAnswer,
			{Name: "rotation_answer", Description: "What `rotation` returns.", Fields: []Field{
				{Name: "result", Type: FieldRecord, Of: "result", Required: true, Description: "The `result` record."},
				{Name: "known", Type: FieldBoolean, Required: true, Description: "Whether the files could be " +
					"listed; false declares nothing lost."},
				{Name: "present", Type: FieldTextList, Description: "The file identifiers the feed still carries."},
				{Name: "highest_sequence", Type: FieldInteger, Description: "The current file's sequence, when the " +
					"feed reports one."},
				{Name: "error", Type: FieldText, Description: "Why the listing failed, when it did."},
			}},
			answerRecord("alerts_answer", "alerts", "security_event_alert", "What `alerts` returns."),
			{Name: "security_event_alert", Description: "One event, decoded and normalised.", Fields: []Field{
				{Name: "file_id", Type: FieldText, Required: true, Description: "The file the event was read from; " +
					"with byte_offset, the coordinate the watermark keeps."},
				{Name: "byte_offset", Type: FieldInteger, Required: true, Description: "Where in the file."},
				{Name: "occurred_at", Type: FieldInteger, Required: true, Unit: "UTC epoch seconds",
					Description: "When the event happened."},
				{Name: "rule_identity", Type: FieldText, Required: true, Description: "The rule or signature's " +
					"identifier."},
				{Name: "signature", Type: FieldText, Required: true, Description: "The signature's text; empty when " +
					"the event carries none."},
				{Name: "event_action", Type: FieldText, Required: true, Values: []string{"blocked", "allowed",
					"unknown"}, Description: "What the engine did."},
				{Name: "normalised_severity", Type: FieldText, Description: "The severity, when the event carries one."},
				{Name: "src_address", Type: FieldText, Required: true, Description: "The source address."},
				{Name: "dst_address", Type: FieldText, Required: true, Description: "The destination address."},
				{Name: "src_port", Type: FieldInteger, Description: "The source port, 0 to 65535."},
				{Name: "dst_port", Type: FieldInteger, Description: "The destination port, 0 to 65535."},
				{Name: "protocol", Type: FieldText, Description: "The protocol."},
				{Name: "in_interface_device", Type: FieldText, Description: "The device the event was seen on."},
			}},
		},
	},
	{
		Kind: "dhcp_lease",
		Description: "The lease table of a DHCP server: data source 4. Several providers may be active at once " +
			"(one server per scope); the kind runs the client identity cascade on what they report.",
		Functions: []Function{probeFunction, {Name: "leases", Arguments: "host, page_size", Returns: "leases_answer",
			Description: "Called at every lease pass: the server's current lease table."}},
		Records: []Record{
			probeAnswer,
			answerRecord("leases_answer", "leases", "dhcp_lease_record", "What `leases` returns."),
			{Name: "dhcp_lease_record", Description: "One lease generation, with the field-name differences between " +
				"backends normalised away.", Fields: []Field{
				{Name: "address", Type: FieldText, Required: true, Description: "The leased address."},
				{Name: "mac", Type: FieldText, Description: "The hardware address; the host lower-cases it and " +
					"stores a value that is not six colon-separated octets as absent."},
				{Name: "hostname", Type: FieldText, Description: "The host name the client sent."},
				{Name: "dhcp_client_id", Type: FieldText, Description: "The DHCP client identifier."},
				{Name: "duid", Type: FieldText, Description: "The DHCPv6 DUID."},
				{Name: "iaid", Type: FieldText, Description: "The DHCPv6 IAID."},
				{Name: "vendor_hint", Type: FieldText, Description: "The vendor the server names for the address."},
				{Name: "lease_state", Type: FieldText, Required: true, Values: []string{"active", "expired",
					"reserved", "unknown"}, Description: "The lease's state."},
				{Name: "interface_identifier", Type: FieldText, Description: "The interface's configuration key, " +
					"when the server names it."},
				{Name: "interface_device", Type: FieldText, Description: "The interface's network device, when the " +
					"server names it."},
				{Name: "starts_at", Type: FieldInteger, Unit: "UTC epoch seconds", Description: "The validity " +
					"start, only when the server reports one; never substituted."},
				{Name: "expires_at", Type: FieldInteger, Unit: "UTC epoch seconds", Description: "The expiry."},
			}},
		},
	},
	{
		Kind: "dns_lookup",
		Description: "The lookups a resolver answered, per querying client: data source 5, the sole source of " +
			"site names. The kind never presents what it read as coverage of a window.",
		Functions: []Function{probeFunction, {Name: "lookups", Arguments: "host, page", Returns: "lookups_answer",
			Description: "Called for pages 1 to page_bound at every lookup pass."}},
		Values: []Field{
			{Name: "page_bound", Type: FieldInteger, Required: true, Description: "How many pages one pass may " +
				"walk, 1 or more."},
			{Name: "requested_span_seconds", Type: FieldInteger, Required: true, Unit: "seconds",
				Description: "The window the implementation asks for, 0 or more; never presented as coverage."},
		},
		Records: []Record{
			probeAnswer,
			answerRecord("lookups_answer", "lookups", "dns_lookup_record", "What `lookups` returns."),
			{Name: "dns_lookup_record", Description: "One lookup, decoded and normalised.", Fields: []Field{
				{Name: "deduplication_key", Type: FieldText, Required: true, Description: "The lookup's identity, " +
					"composed by the implementation; a lookup read twice is stored once."},
				{Name: "client_address", Type: FieldText, Required: true, Description: "The querying client's " +
					"address, or the host name the resolver logged for it."},
				{Name: "client_family", Type: FieldInteger, Description: "4 or 6: the querying address's family."},
				{Name: "domain", Type: FieldText, Required: true, Description: "The name looked up."},
				{Name: "action", Type: FieldText, Required: true, Values: []string{"pass", "block", "drop",
					"unknown"}, Description: "The resolver's verdict."},
				{Name: "answer_source", Type: FieldText, Description: "Where the answer came from."},
				{Name: "rcode", Type: FieldText, Description: "The response code."},
				{Name: "dnssec_status", Type: FieldText, Description: "The DNSSEC status."},
				{Name: "blocklist_name", Type: FieldText, Description: "The list that refused it, when one did " +
					"and the resolver names it."},
				{Name: "looked_up_at", Type: FieldInteger, Required: true, Unit: "UTC epoch seconds",
					Description: "When the lookup was made."},
			}},
		},
	},
	{
		Kind: "resolver_cache",
		Description: "The records a resolver holds in its cache, with the seconds each has left. The kind stores " +
			"A, AAAA and CNAME records as observations and counts every other type.",
		Functions: []Function{probeFunction, {Name: "cache", Arguments: "host", Returns: "dump_answer",
			Description: "Called at every cache pass: every record the cache holds now."}},
		Records: []Record{
			probeAnswer,
			dumpAnswer,
			dumpRecord,
			notRecordCount,
		},
	},
	{
		Kind:        "resolver_local_data",
		Description: "The records a resolver serves from its local data, host overrides among them.",
		Functions: []Function{probeFunction, {Name: "local_data", Arguments: "host", Returns: "dump_answer",
			Description: "Called at every local-data pass: every record of the local data now."}},
		Records: []Record{
			probeAnswer,
			dumpAnswer,
			dumpRecord,
			notRecordCount,
		},
	},
	{
		Kind: "measurement_sample",
		Description: "A numeric reading of a subject at an instant: the firewall's gauges, sampled rates. " +
			"Several providers may be active at once. A reading that did not answer is named in `missing`, " +
			"never written as a zero.",
		Functions: []Function{probeFunction, {Name: "sample", Arguments: "host, discovery, now",
			Returns: "sample_answer", Description: "Called at every sampling pass. discovery holds the interfaces " +
				"the firewall reported (identifiers, devices); now is the pass's instant."}},
		Records: []Record{
			probeAnswer,
			{Name: "sample_answer", Description: "What `sample` returns.", Fields: []Field{
				{Name: "result", Type: FieldRecord, Of: "result", Required: true, Description: "The `result` record."},
				{Name: "readings", Type: FieldRecords, Of: "measurement_reading", Description: "The `measurement_reading` records."},
				{Name: "missing", Type: FieldTextList, Description: "The readings that did not answer, each " +
					"named in words for the availability detail."},
				{Name: "error", Type: FieldText, Description: "Why the pass failed, when it did."},
			}},
			{Name: "measurement_reading", Description: "One reading.", Fields: []Field{
				{Name: "subject_kind", Type: FieldText, Required: true, Description: "What is measured: a " +
					"lower-case word with no space (interface, interface_endpoint, gateway, firewall, or the " +
					"plugin's own)."},
				{Name: "subject_key", Type: FieldText, Required: true, Description: "Which one; may be empty."},
				{Name: "measure", Type: FieldText, Required: true, Description: "What figure: a lower-case word " +
					"with no space."},
				{Name: "unit", Type: FieldText, Required: true, Description: "Its unit: a lower-case word with " +
					"no space."},
				{Name: "value", Type: FieldNumber, Required: true, Description: "The figure."},
				{Name: "sampled_at", Type: FieldInteger, Required: true, Unit: "UTC epoch seconds",
					Description: "When it was read."},
				{Name: "attributed", Type: FieldBoolean, Description: "Whether the reading names this provider; " +
					"false for the firewall reporting on itself. True when absent."},
			}},
		},
	},
	{
		Kind: "reconciled_state",
		Description: "The complete set of things of one type as of one instant: a ban list, the peers of a " +
			"tunnel. A thing present in one snapshot and absent from the next has left. Several providers may " +
			"be active at once.",
		Functions: []Function{probeFunction, {Name: "snapshot", Arguments: "host", Returns: "snapshot_answer",
			Description: "Called at the plugin's cadence: every set the provider reports, each complete."}},
		Records: []Record{
			probeAnswer,
			{Name: "snapshot_answer", Description: "What `snapshot` returns.", Fields: []Field{
				{Name: "result", Type: FieldRecord, Of: "result", Required: true, Description: "The `result` record."},
				{Name: "sets", Type: FieldRecords, Of: "state_set", Description: "The `state_set` records."},
				{Name: "error", Type: FieldText, Description: "Why the read failed, when it did."},
			}},
			{Name: "state_set", Description: "One complete set.", Fields: []Field{
				{Name: "set_key", Type: FieldText, Required: true, Description: "Which set: a lower-case word with " +
					"no space."},
				{Name: "items", Type: FieldRecords, Of: "state_item", Required: true, Description: "The `state_item` records; an " +
					"empty list is a set that is now empty."},
			}},
			{Name: "state_item", Description: "One member of a set.", Fields: []Field{
				{Name: "item_key", Type: FieldText, Required: true, Description: "Its identity within the set."},
				{Name: "attributes", Type: FieldObject, Description: "The provider's own fields, displayed and " +
					"never aggregated."},
				{Name: "valid_until_at", Type: FieldInteger, Unit: "UTC epoch seconds", Description: "The end of " +
					"its own validity, when the provider states one."},
			}},
		},
	},
}

// The resolver-record tables, shared by the two resolver kinds.
var (
	dumpAnswer = Record{Name: "dump_answer", Description: "What `cache` and `local_data` return.", Fields: []Field{
		{Name: "result", Type: FieldRecord, Of: "result", Required: true, Description: "The `result` record."},
		{Name: "records", Type: FieldRecords, Of: "resolver_record", Description: "The `resolver_record` records, of every type."},
		{Name: "response_bytes", Type: FieldInteger, Unit: "bytes", Description: "The size of the answer."},
		{Name: "not_records", Type: FieldRecords, Of: "not_record_count", Description: "The `not_record_count` records: rows the " +
			"implementation recognised as not being records at all."},
		{Name: "error", Type: FieldText, Description: "Why the read failed, when it did."},
		{Name: "unsupported", Type: FieldText, Description: "Why this resolver's records can never be read, " +
			"when that is so."},
	}}
	dumpRecord = Record{Name: "resolver_record", Description: "One record as the endpoint returned it.",
		Fields: []Field{
			{Name: "owner_name", Type: FieldText, Required: true, Description: "The owner name."},
			{Name: "ttl", Type: FieldText, Description: "The TTL as text."},
			{Name: "ttl_shape", Type: FieldText, Required: true, Values: []string{"absent", "null", "string",
				"other"}, Description: "The JSON shape the TTL arrived in."},
			{Name: "rrtype", Type: FieldText, Required: true, Description: "The record type."},
			{Name: "value", Type: FieldText, Required: true, Description: "The record's value."},
		}}
	notRecordCount = Record{Name: "not_record_count", Description: "A number of rows that are not records.",
		Fields: []Field{
			{Name: "count", Type: FieldInteger, Required: true, Description: "How many."},
			{Name: "wording", Type: FieldText, Required: true, Description: "What they are, worded so that " +
				"\"skipped <count> <wording>\" reads as a sentence."},
		}}
)

// The service kinds: an external service's data a plugin acquires through the fetch facility and
// answers from. MaxMind's own term for its GeoLite2 products is "IP geolocation"; "registrable
// domain" is the Public Suffix List's and the URL Standard's.
var serviceContracts = []Contract{
	{
		Kind: "ip_geolocation",
		Description: "Places public addresses: each address's country, coordinates, autonomous system and " +
			"operator. One provider is active at a time. The core decides which addresses need placing -- " +
			"never a non-public one -- stores the answers in geo_asn and places every address again when the " +
			"active provider or its build changes.",
		Functions: []Function{
			{Name: "status", Arguments: "host", Returns: "dataset_answer",
				Description: "Called at start and after every refresh: whether a dataset is held, and its build."},
			{Name: "refresh", Arguments: "host", Returns: "dataset_answer",
				Description: "Called at refresh_interval_geoip_seconds: acquire or update the dataset."},
			{Name: "lookup", Arguments: "host, addresses", Returns: "lookup_answer",
				Description: "Called at poll_interval_geo_lookup_seconds with a batch of public addresses."},
		},
		Records: []Record{
			{Name: "dataset_answer", Description: "What `status` and `refresh` return.", Fields: []Field{
				{Name: "result", Type: FieldRecord, Of: "result", Required: true, Description: "The `result` record; " +
					"its probe is a word naming what the last refresh found."},
				{Name: "ready", Type: FieldBoolean, Required: true, Description: "Whether a dataset is held and " +
					"answers lookups."},
				{Name: "build", Type: FieldInteger, Unit: "UTC epoch seconds", Description: "The build of the dataset " +
					"held, which every answer is recorded against."},
				{Name: "error", Type: FieldText, Description: "Why the refresh failed, when it did."},
			}},
			{Name: "lookup_answer", Description: "What `lookup` returns.", Fields: []Field{
				{Name: "build", Type: FieldInteger, Required: true, Unit: "UTC epoch seconds",
					Description: "The build the answers come from."},
				{Name: "answers", Type: FieldRecords, Of: "geolocation", Required: true,
					Description: "One `geolocation` record per address asked about."},
				{Name: "error", Type: FieldText, Description: "Why the lookups failed, when they did."},
			}},
			{Name: "geolocation", Description: "What the dataset says about one address.", Fields: []Field{
				{Name: "address", Type: FieldText, Required: true, Description: "The address asked about."},
				{Name: "country_code", Type: FieldText, Description: "The ISO 3166-1 country code; absent is a miss."},
				{Name: "country_name", Type: FieldText, Description: "The country's English name."},
				{Name: "latitude", Type: FieldNumber, Description: "The latitude."},
				{Name: "longitude", Type: FieldNumber, Description: "The longitude."},
				{Name: "asn", Type: FieldInteger, Description: "The autonomous system number."},
				{Name: "operator", Type: FieldText, Description: "The autonomous system's organisation."},
			}},
		},
	},
	{
		Kind: "registrable_domain",
		Description: "Groups site names by their registrable domain, at read time; no stored name is ever " +
			"altered. One provider is active at a time; with none, no grouping is offered.",
		Functions: []Function{
			{Name: "status", Arguments: "host", Returns: "list_answer",
				Description: "Called at start and after every refresh: whether a list is held."},
			{Name: "refresh", Arguments: "host", Returns: "list_answer",
				Description: "Called at refresh_interval_public_suffix_list_seconds: acquire or update the list."},
			{Name: "group", Arguments: "host, names", Returns: "group_answer",
				Description: "Called while a widget is served, with the site names to group."},
		},
		Records: []Record{
			{Name: "list_answer", Description: "What `status` and `refresh` return.", Fields: []Field{
				{Name: "result", Type: FieldRecord, Of: "result", Required: true, Description: "The `result` record; " +
					"its probe is a word naming what the last refresh found."},
				{Name: "ready", Type: FieldBoolean, Required: true, Description: "Whether a list is held."},
				{Name: "version", Type: FieldText, Description: "The list's own version, when it carries one."},
				{Name: "rules", Type: FieldInteger, Description: "How many rules the list holds."},
				{Name: "confirmed_at", Type: FieldInteger, Unit: "UTC epoch seconds", Description: "The last " +
					"instant a refresh confirmed the list current."},
				{Name: "failed", Type: FieldBoolean, Description: "Whether this refresh failed, which the core " +
					"records as a collection gap from confirmed_at."},
				{Name: "error", Type: FieldText, Description: "Why the refresh failed, when it did."},
			}},
			{Name: "group_answer", Description: "What `group` returns.", Fields: []Field{
				{Name: "groups", Type: FieldRecords, Of: "grouping", Required: true,
					Description: "One `grouping` record per name."},
				{Name: "error", Type: FieldText, Description: "Why the grouping failed, when it did."},
			}},
			{Name: "grouping", Description: "One name's registrable domain.", Fields: []Field{
				{Name: "name", Type: FieldText, Required: true, Description: "The site name, verbatim."},
				{Name: "registrable_domain", Type: FieldText, Description: "Its registrable domain; absent when the " +
					"name has none, which groups it on its own."},
			}},
		},
	},
}

// The kinds no plugin may implement, and why: each is opnview's own machinery rather than a
// source.
var coreOnlyKinds = map[string]string{
	"flow_volume": "derived by opnview from the filter log; nothing collects it",
}

// Contracts returns the core kinds' contracts, ordered by kind.
func Contracts() []Contract {
	contracts := append(append([]Contract(nil), coreContracts...), serviceContracts...)
	sort.Slice(contracts, func(i, j int) bool { return contracts[i].Kind < contracts[j].Kind })
	return contracts
}

// ContractOf returns a core kind's contract.
func ContractOf(kind string) (Contract, bool) {
	for _, contract := range append(append([]Contract(nil), coreContracts...), serviceContracts...) {
		if contract.Kind == kind {
			return contract, true
		}
	}
	return Contract{}, false
}

// CoreOnly reports whether a kind is a core kind no plugin may implement, and why.
func CoreOnly(kind string) (string, bool) {
	reason, found := coreOnlyKinds[kind]
	return reason, found
}

// CoreKinds returns every core kind, sorted: those that accept a plugin provider and those only the
// core implements. A source set adds the kinds its plugins declared (internal/collect/sources).
func CoreKinds() []string {
	kinds := make([]string, 0, len(coreContracts)+len(serviceContracts)+len(coreOnlyKinds))
	for _, contract := range Contracts() {
		kinds = append(kinds, contract.Kind)
	}
	for kind := range coreOnlyKinds {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	return kinds
}

// IsCoreKind reports whether a kind is a core kind, implementable by a plugin or not.
func IsCoreKind(kind string) bool {
	if _, found := ContractOf(kind); found {
		return true
	}
	_, found := coreOnlyKinds[kind]
	return found
}

// TermShapeValid reports whether a term a provider declares -- a measure, a unit, a subject
// kind, a set key -- has the shape the schema constrains: non-empty, lower-case, no space.
func TermShapeValid(term string) bool {
	return term != "" && term == strings.ToLower(term) && !strings.Contains(term, " ")
}

// SharedRecords are the record tables every kind shares: the result.
func SharedRecords() []Record { return []Record{resultRecord} }

// LookupRecord returns a record table a contract names: its own, or a shared one.
func LookupRecord(contract Contract, name string) (Record, bool) {
	if record, found := contract.Record(name); found {
		return record, true
	}
	for _, record := range SharedRecords() {
		if record.Name == name {
			return record, true
		}
	}
	return Record{}, false
}

// IsServiceKind reports whether a kind is a service kind: an external service's data a plugin
// acquires and answers from, rather than a firewall source.
func IsServiceKind(kind string) bool {
	for _, contract := range serviceContracts {
		if contract.Kind == kind {
			return true
		}
	}
	return false
}
