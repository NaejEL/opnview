package store

import "strconv"

// The row shapes the collectors write. Each field carries the name of the API
// field it comes from where there is one, unchanged, so the vocabulary of
// docs/data-model.md reaches the code without translation.
//
// One naming note, because the alternative would have been a coinage. The
// discovered entity is an INTERFACE, which is OPNsense's own word, and
// `interface` is a Go keyword, so the type below is `Interface` and the local
// variables are `iface`. `iface` is not opnview's invention either: OPNsense
// itself abbreviates the word that way, in query_alerts' `in_iface` and in
// get_arp's `intf`.

// LookupState is the modelled "not found" a join key can resolve to. A device
// name absent from interface_map, or a rid matching no rule, is normal and is a
// state on the row rather than a dropped row.
type LookupState string

// The three lookup states, matching the schema's CHECK.
const (
	// LookupResolved means the join key named a row that exists.
	LookupResolved LookupState = "resolved"
	// LookupNotFound means the join key named nothing. For a rid this is the
	// normal consequence of a rule having been removed.
	LookupNotFound LookupState = "not_found"
	// LookupPending means the lookup has not been attempted yet.
	LookupPending LookupState = "pending"
)

// TrafficScope is east-west or north-south, derived from interface membership
// and from nothing else — no address, no CIDR, no name, no assumed addressing
// plan.
type TrafficScope string

// The three scopes.
const (
	// ScopeEastWest is a flow whose both endpoints sit in a discovered interface.
	ScopeEastWest TrafficScope = "east_west"
	// ScopeNorthSouth is every other flow with no end that is this firewall.
	ScopeNorthSouth TrafficScope = "north_south"
	// ScopeThisFirewall is a flow with an end that is this firewall: OPNsense's
	// "This Firewall", pf's `self` (decision 6 of the step-5A live corrections).
	ScopeThisFirewall TrafficScope = "this_firewall"
)

// The traffic directions classified_flow derives. pf's own `dir` is per interface
// and is flow.direction; these say which way a flow crossed the firewall as a
// whole, and the last two that one end is this firewall.
const (
	DirectionOutbound         = "outbound"
	DirectionInbound          = "inbound"
	DirectionInterInterface   = "inter_interface"
	DirectionToThisFirewall   = "to_this_firewall"
	DirectionFromThisFirewall = "from_this_firewall"
)

// ScopeOf returns the traffic scope of a flow from its interface membership and
// from whether an end is this firewall. It is the one expression the schema's
// CHECK pins, written once here so a collector cannot disagree with the database.
func ScopeOf(srcInterfaceID, dstInterfaceID *int64, srcThisFirewall, dstThisFirewall bool) TrafficScope {
	if srcThisFirewall || dstThisFirewall {
		return ScopeThisFirewall
	}
	if srcInterfaceID != nil && dstInterfaceID != nil {
		return ScopeEastWest
	}
	return ScopeNorthSouth
}

// AvailabilityState is the modelled health of one provider.
type AvailabilityState string

// The three availability states, matching the schema's CHECK. An unavailable
// source is reported, never worked around, and never rendered as an absence of
// data.
const (
	// StateReachable means opnview asked and the source answered.
	StateReachable AvailabilityState = "reachable"
	// StatePresentButDisabled means the component is installed and switched off,
	// which is a configuration state and not a fault.
	StatePresentButDisabled AvailabilityState = "present_but_disabled"
	// StateUnavailable means opnview could not ask: absent, unauthorised, or
	// answering in a way that is not usable.
	StateUnavailable AvailabilityState = "unavailable"
)

// GapReason is why opnview knows it missed an interval. The vocabulary is
// opnview's own and closed, legitimately: these are opnview's own detections,
// not values any endpoint reports.
type GapReason string

// The three gap reasons, matching the schema's CHECK.
const (
	// GapDigestOutsideWindow is a filter-log pass whose newest stored line is
	// older than the oldest line the page returned: everything between them was
	// missed, and `digest` is not a server-side cursor that could recover it.
	GapDigestOutsideWindow GapReason = "digest_outside_returned_window"
	// GapEveRotationLost is a watermarked eve.json file that rotation discarded.
	// The loss is permanent.
	GapEveRotationLost GapReason = "eve_rotation_lost"
	// GapResolverWindowNotHonoured is the resolver ring buffer: the endpoint
	// ignores timeStart and timeEnd, so a pass whose oldest returned lookup is
	// newer than the newest stored one missed everything in between.
	GapResolverWindowNotHonoured GapReason = "resolver_window_not_honoured"
	// GapDownloadFailed is a reference list opnview downloads -- the Public Suffix
	// List -- that could not be fetched or did not parse. The copy in use stays in
	// use; the interval is how long it has gone without a refresh.
	GapDownloadFailed GapReason = "download_failed"
)

// LogReasons is the established value set of the filter log's `reason` field,
// flow.log_reason: the reason table of the `filterlog` daemon that writes the
// line (opnsense/ports 26.7.3, opnsense/filterlog/files/filterlog.c), which is
// FreeBSD pf's PFRES_NAMES (sys/netpfil/pf/pf.h) up to synproxy. Any other reason
// is written as "unknown(<n>)". No code matches a log reason outside this set,
// and a test fails if one does.
var LogReasons = []string{
	"match", "bad-offset", "fragment", "short", "normalize", "memory", "bad-timestamp",
	"congestion", "ip-option", "proto-cksum", "state-mismatch", "state-insert",
	"state-limit", "src-limit", "synproxy",
}

// LogReasonMatch is the reason a rule decided: the packet matched it. Every other
// established reason is a packet the firewall dropped for a reason no rule
// expresses.
const LogReasonMatch = "match"

// The values of the endpoint's `source` field a lookup must carry to name a flow:
// answered by recursion, from the cache, or from local data such as a host
// override. Established at opnsense/core 26.7.3, scripts/unbound/stats.py, which
// maps the stored source to Recursion, Local, Local-data and Cache; Local is a
// blocklist answer or a SERVFAIL, so it names nothing.
var AttributionAnswerSources = []string{"Recursion", "Cache", "Local-data"}

// SubjectKind is what a measurement sample measured.
//
// The constants below are the terms opnview's OWN sampler uses. They are not the
// whole vocabulary: measurement_sample is a provider kind, and a provider may
// declare a subject the schema did not ship with — HAProxy's subject is a
// backend, and a UPS is neither an interface nor an endpoint pair. The schema
// constrains the SHAPE of a term and not its membership of a list, so a new
// subject is a value and never a schema change.
type SubjectKind string

// The subject kinds opnview's own sampler uses.
const (
	// SubjectFirewall is the firewall as a whole. Its subject key is empty.
	SubjectFirewall SubjectKind = "firewall"
	// SubjectInterface is one interface. Its subject key is the network device
	// name, the token interface_map keys by.
	SubjectInterface SubjectKind = "interface"
	// SubjectInterfaceEndpointPair is one pair of addresses AS READ ON ONE
	// INTERFACE, local end first. Its subject key is the network device name, the
	// local address and the peer address, joined by single spaces. It replaces the
	// canonical endpoint_pair subject step 4 wrote, because that subject's
	// lexicographic key dropped two facts traffic/top states: the interface the record
	// was read on, and which address was the local one -- so one pair read on two
	// interfaces at one instant collided and one reading was lost. OPNsense has no
	// word for it; the term is opnview's own.
	SubjectInterfaceEndpointPair SubjectKind = "interface_endpoint_pair"
	// SubjectInterfaceEndpoint is one local address AS READ ON ONE INTERFACE, over
	// all its peers. Its subject key is the network device name and the local
	// address, joined by a single space. It carries the totals traffic/top reports
	// on the record itself -- rate_bits_in, rate_bits_out, cumulative_bytes_in and
	// cumulative_bytes_out -- because the local address's outbound figure exists
	// ONLY as that total: it is split per peer nowhere, so a pair carries it only
	// when the record has a single peer. Without this subject a client talking to
	// several peers would have no outbound figure at all. The term is opnview's own.
	SubjectInterfaceEndpoint SubjectKind = "interface_endpoint"
	// SubjectGateway is one gateway, keyed by the name /api/routes/gateway/status
	// reports for it.
	SubjectGateway SubjectKind = "gateway"
)

// InterfaceEndpointPairKey composes the subject key of an interface endpoint pair.
func InterfaceEndpointPairKey(device, local, peer string) string {
	return device + " " + local + " " + peer
}

// InterfaceEndpointKey composes the subject key of an interface endpoint.
func InterfaceEndpointKey(device, local string) string {
	return device + " " + local
}

// SampleSeconds is how long one traffic/top sample covers. The endpoint runs
// iftop with `-s 2`, printing one text output after two seconds and quitting
// (opnsense/core 26.7.3, scripts/interfaces/traffic_top.py), so both its rates and
// its cumulative bytes describe that two-second capture and nothing longer. Bytes
// seen in samples are therefore bytes seen in this many seconds per sample, and
// are never extrapolated to a period.
const SampleSeconds = 2

// Measure is what was measured. The vocabulary is opnview's own, because the
// survey establishes the telemetry endpoints and not their field names: a measure
// named after a response key nobody has read would be an assertion about a shape
// nobody has seen.
//
// It is EXTENSIBLE, for the same reason SubjectKind is: a UPS reports volts and
// SMART reports reallocated sectors, and neither is in the list below. The
// constants are what opnview's own sampler reads; a provider's measure is an
// ordinary value beside them.
type Measure string

// The measures opnview's own sampler reads.
const (
	// MeasureCPUUseRatio is the fraction of the processor in use, 0 to 1.
	MeasureCPUUseRatio Measure = "cpu_use_ratio"
	// MeasureMemoryUseRatio is the fraction of memory in use, 0 to 1.
	MeasureMemoryUseRatio Measure = "memory_use_ratio"
	// MeasureTemperatureCelsius is one sensor's temperature.
	MeasureTemperatureCelsius Measure = "temperature_celsius"
	// MeasureDiskUseRatio is the fraction of a filesystem in use, 0 to 1.
	MeasureDiskUseRatio Measure = "disk_use_ratio"
	// MeasureUptimeSeconds is how long the firewall has been up.
	MeasureUptimeSeconds Measure = "uptime_seconds"
	// MeasureLoadAverage is a load average, which has no unit.
	MeasureLoadAverage Measure = "load_average"
	// MeasurePacketsIn and the three below are per-interface counters.
	MeasurePacketsIn Measure = "packets_in"
	// MeasurePacketsOut is the outbound packet counter.
	MeasurePacketsOut Measure = "packets_out"
	// MeasureBytesIn is the inbound byte counter.
	MeasureBytesIn Measure = "bytes_in"
	// MeasureBytesOut is the outbound byte counter.
	MeasureBytesOut Measure = "bytes_out"
	// MeasureCumulativeBytesIn and MeasureCumulativeBytesOut are the per-pair
	// figures traffic/top reports. They are a live snapshot: nothing upstream
	// answers "what did these two talk about last Tuesday", so opnview's own
	// history is built from these samples.
	MeasureCumulativeBytesIn Measure = "cumulative_bytes_in"
	// MeasureCumulativeBytesOut is the outbound half of the pair figure.
	MeasureCumulativeBytesOut Measure = "cumulative_bytes_out"
	// MeasureRateBitsIn and MeasureRateBitsOut are the instantaneous rates
	// traffic/top reports beside the cumulative counters.
	MeasureRateBitsIn Measure = "rate_bits_in"
	// MeasureRateBitsOut is the outbound half of the rate.
	MeasureRateBitsOut Measure = "rate_bits_out"
	// MeasureErrorsIn and MeasureErrorsOut are the per-interface error counters
	// /api/diagnostics/traffic/interface reports as `input errors` and
	// `output errors`.
	MeasureErrorsIn Measure = "errors_in"
	// MeasureErrorsOut is the outbound error counter.
	MeasureErrorsOut Measure = "errors_out"
	// MeasureDelayMilliseconds is a gateway's round-trip time, the `delay` field
	// of /api/routes/gateway/status.
	MeasureDelayMilliseconds Measure = "delay_milliseconds"
	// MeasureDelayStddevMilliseconds is the standard deviation of that round-trip
	// time, the `stddev` field.
	MeasureDelayStddevMilliseconds Measure = "delay_stddev_milliseconds"
	// MeasureLossRatio is a gateway's packet loss, the `loss` field, as a 0-to-1
	// ratio.
	MeasureLossRatio Measure = "loss_ratio"
	// MeasureSwapTotalBytes and MeasureSwapUsedBytes are one swap device's size and
	// use, the `total` and `used` fields of /api/diagnostics/system/systemSwap,
	// which swapinfo -k reports in KiB and the sampler stores in bytes. The subject
	// is the firewall, keyed by the swap device.
	MeasureSwapTotalBytes Measure = "swap_total_bytes"
	MeasureSwapUsedBytes  Measure = "swap_used_bytes"
)

// Unit is the unit of a measurement value. It is mandatory: a number with no unit
// is a number nobody can put on a screen. It is extensible for the same reason
// Measure is — a volt is not in the list below and a UPS reports one.
type Unit string

// The units opnview's own sampler produces.
const (
	// UnitRatio is a fraction between 0 and 1.
	UnitRatio Unit = "ratio"
	// UnitCelsius is a temperature.
	UnitCelsius Unit = "celsius"
	// UnitSecond is a duration.
	UnitSecond Unit = "second"
	// UnitPacket is a packet count.
	UnitPacket Unit = "packet"
	// UnitByte is a byte count.
	UnitByte Unit = "byte"
	// UnitBitPerSecond is a rate.
	UnitBitPerSecond Unit = "bit_per_second"
	// UnitDimensionless is a figure with no unit, such as a load average.
	UnitDimensionless Unit = "dimensionless"
	// UnitMillisecond is a short duration, such as a round-trip time.
	UnitMillisecond Unit = "millisecond"
)

// Interface is one discovered OPNsense interface. Every field but UserLabel and
// LinkKind carries an `interfaces_info` field name unchanged.
type Interface struct {
	// Identifier is the configuration key, API field `identifier`. It is what
	// /api/diagnostics/traffic/top takes, and it is NOT the device name.
	Identifier string
	// Device is the network device, API field `device`. It is what the filter log
	// reports, and it is the wrong argument to traffic/top.
	Device string
	// Description is the user-given description, API field `description`.
	Description string
	// Status is the link state, API field `status`, stored verbatim.
	Status *string
	// Enabled is the administrative state, API field `enabled`, stored verbatim.
	Enabled *string
	// LinkType is the raw link type, API field `link_type`.
	LinkType string
	// LinkKind is opnview's normalisation of LinkType, derived from the link type
	// alone and never from a name.
	LinkKind string
	// VLANTag is API field `vlan_tag`.
	VLANTag *int64
	// IsUpstream is whether the response reported a non-empty `gateways[]` for the
	// interface, and nothing else.
	IsUpstream bool
}

// The five API fields an interface address is read from, stored verbatim in
// interface_address.source_field.
const (
	// SourceFieldAddr4 is the interface's primary IPv4 address, `addr4`.
	SourceFieldAddr4 = "addr4"
	// SourceFieldAddr6 is the interface's primary IPv6 address, `addr6`.
	SourceFieldAddr6 = "addr6"
	// SourceFieldIPv4 is one entry of `ipv4[]`, every IPv4 address the interface
	// holds, the primary one included.
	SourceFieldIPv4 = "ipv4"
	// SourceFieldIPv6 is one entry of `ipv6[]`, every IPv6 address the interface
	// holds, the primary one and its link-local address included.
	SourceFieldIPv6 = "ipv6"
	// SourceFieldGateways is one entry of `gateways[]`, a gateway address.
	SourceFieldGateways = "gateways"
)

// InterfaceAddress is one address an interface holds, or one gateway behind it.
type InterfaceAddress struct {
	// InterfaceID is the interface.
	InterfaceID int64
	// SourceField is the API field the value was read from.
	SourceField string
	// Address is the address, with no prefix.
	Address string
	// PrefixLength is the prefix length of an interface address; nil for a
	// gateway.
	PrefixLength *int64
	// AddressFamily is 4 or 6.
	AddressFamily int64
}

// Rule is one discovered firewall rule.
type Rule struct {
	// PfLabel is `uuid`, which for legacy and automatic rules carries the pf
	// label — the token the filter log reports as `rid`.
	PfLabel string
	// Description is API field `description`.
	Description string
	// Action is read from the %-prefixed raw twin, never from the localised
	// field.
	Action string
	// Direction is read from the raw twin for the same reason.
	Direction *string
	// Interface is API field `interface`, verbatim: interface configuration keys
	// on a model rule, interface descriptions on a legacy one.
	Interface *string
	// Legacy is API field `legacy`, which says which of the two Interface holds.
	Legacy *bool
	// LogsMatches is API field `log`: whether the rule writes a filter-log line.
	// NULL means the source did not report the flag, which is not the same as
	// "does not log".
	LogsMatches *bool
	// Enabled is API field `enabled`; nil means not reported. A disabled rule passes
	// nothing.
	Enabled *bool
	// IsAutomatic is API field `is_automatic`.
	IsAutomatic bool
}

// ClientIdentity is one level of the client identity cascade.
type ClientIdentity struct {
	// Kind is `dhcp_client_id`, `mac` or `address_in_interface`.
	Kind string
	// Key is the value that level produced.
	Key string
}

// The three identity kinds, matching the schema's CHECK.
const (
	// IdentityDHCPClientID is the most stable level: the lease's own client
	// identifier.
	IdentityDHCPClientID = "dhcp_client_id"
	// IdentityMAC is the normalised hardware address.
	IdentityMAC = "mac"
	// IdentityAddressInInterface is the last level, available to a machine seen
	// only in flows.
	IdentityAddressInInterface = "address_in_interface"
)

// Client is one machine on the network.
type Client struct {
	// Identity is the cascade level that named it and the value that level
	// produced.
	Identity ClientIdentity
	// InterfaceID is the interface it was seen behind, when one is known.
	InterfaceID *int64
	// MAC is lower-cased and seventeen characters, or nil.
	MAC *string
	// Hostname is the lease's `hostname`.
	Hostname *string
	// VendorHint is the lease's `mac_info`, or get_arp's `manufacturer`. It is
	// context and is never a basis for classifying a machine.
	VendorHint *string
	// LastAddress is the most recent address it was seen at.
	LastAddress *string
}

// DHCPLease is one observed lease generation.
type DHCPLease struct {
	// ClientID is the client this lease named, once identity is resolved.
	ClientID *int64
	// Backend is `kea`, `dnsmasq` or `isc`.
	Backend string
	// Address is the leased address.
	Address string
	// MAC is `hwaddr` on the two current backends, `mac` on the legacy plugin,
	// normalised here.
	MAC *string
	// Hostname is `hostname`, uniform across the backends.
	Hostname *string
	// DHCPClientID is `client_id`, the most stable identity level.
	DHCPClientID *string
	// DUID is `duid`.
	DUID *string
	// IAID is `iaid`.
	IAID *string
	// VendorHint is `mac_info`.
	VendorHint *string
	// LeaseState is normalised across the backends into one vocabulary.
	LeaseState string
	// InterfaceID is the interface the lease was issued on.
	InterfaceID *int64
	// StartsAt is the validity start, and nil when the backend does not report
	// one. On Kea it is `expire` minus `valid_lifetime`, a real figure; Dnsmasq
	// reports no start at all and a reserved lease reports neither a start nor an
	// expiry, so both store nil rather than a substitute.
	StartsAt *int64
	// GenerationKey is the discriminator that makes one lease generation one row,
	// so a second poll of the same lease inserts nothing. It is composed by
	// GenerationKeyOf and names what it rests on, which is what keeps a
	// substitute from reading as a validity start.
	GenerationKey string
	// ExpiresAt is `expire`, or `ends` on the legacy plugin.
	ExpiresAt *int64
	// ObservedAt is when opnview read the lease.
	ObservedAt int64
}

// The three forms a lease generation key takes, ordered by how much the backend
// actually knew. The prefix is part of the key, so two backends that happen to
// report the same number do not collapse into one generation, and a reader can
// tell a real start from a substitute without consulting the backend.
const (
	// GenerationFromStart is a backend that reported a validity start.
	GenerationFromStart = "start"
	// GenerationFromExpiry is a backend that reported only an expiry. The key
	// keeps a re-poll idempotent and makes a renewal a new generation; it is NOT
	// a claim about when the lease began.
	GenerationFromExpiry = "expiry"
	// GenerationFromObservedDay is a standing reservation, which reports neither.
	// A reservation is configuration rather than a generation, so its key is the
	// day it was seen and it yields one row per day rather than one per poll.
	GenerationFromObservedDay = "observed_day"
)

// GenerationKeyOf composes a lease generation key from the most specific instant
// the backend supplied. It is here rather than in a collector because the
// idempotence of the lease table rests on every backend composing it the same
// way.
func GenerationKeyOf(startsAt, expiresAt *int64, observedDayStart int64) string {
	switch {
	case startsAt != nil:
		return GenerationFromStart + ":" + strconv.FormatInt(*startsAt, 10)
	case expiresAt != nil:
		return GenerationFromExpiry + ":" + strconv.FormatInt(*expiresAt, 10)
	default:
		return GenerationFromObservedDay + ":" + strconv.FormatInt(observedDayStart, 10)
	}
}

// Flow is one filter-log record.
type Flow struct {
	// LogDigest is `__digest__`, the identity of the row. The endpoint echoes
	// back the record matching a supplied digest, and this uniqueness is what
	// makes the echo harmless.
	LogDigest string
	// ObservedAt is `__timestamp__`, normalised to UTC epoch seconds.
	ObservedAt int64
	// IngestedAt is when opnview stored it.
	IngestedAt int64
	// InterfaceDevice is the filter log's `interface`: a raw device name.
	InterfaceDevice string
	// InterfaceLookupState is whether that device name resolved.
	InterfaceLookupState LookupState
	// SrcInterfaceID and DstInterfaceID are the two ends' interfaces, and the
	// only thing TrafficScope derives from.
	SrcInterfaceID *int64
	// DstInterfaceID is the destination end's interface, nil for north-south.
	DstInterfaceID *int64
	// SrcClientID and DstClientID are the two ends' machines, when known.
	SrcClientID *int64
	// DstClientID is the destination machine, when known.
	DstClientID *int64
	// SrcAddress is `src`.
	SrcAddress string
	// DstAddress is `dst`.
	DstAddress string
	// SrcPort is `srcport`, present on TCP and UDP records only.
	SrcPort *int64
	// DstPort is `dstport`, present on TCP and UDP records only.
	DstPort *int64
	// Protocol is `protoname`.
	Protocol string
	// IPVersion is `ipversion`.
	IPVersion int64
	// Action is `action`.
	Action string
	// Direction is `dir`.
	Direction string
	// LogReason is `reason`: why the line was written, which is not the same
	// question as what was done to the packet. Stored verbatim.
	LogReason *string
	// PacketBytes is `length`.
	PacketBytes int64
	// Rid is `rid`, the pf label.
	Rid *string
	// RuleID is the rule that rid resolved to.
	RuleID *int64
	// RuleLookupState is whether it resolved. A rid matching no rule is normal.
	RuleLookupState LookupState
	// IPID is `id`, the IPv4 identification field; nil on an IPv6 record. It is
	// stored to pair the two records of one connection, and for nothing else.
	IPID *int64
	// TCPSeq is `seq`, the TCP sequence number; nil on a record that is not TCP.
	// Stored for the same pairing and nothing else.
	TCPSeq *int64
}

// DNSResolution is one resolver lookup.
type DNSResolution struct {
	// LookupKey is the identity of the lookup, stored in the column of the same
	// name. It is NOT the endpoint's `uuid`: that field is null on every row the
	// firewall returns, measured 2026-09-27, so what a provider cannot supply
	// opnview composes from the row's own content. The composition, and the
	// under-count it accepts, are recorded on the column itself.
	LookupKey string
	// ClientAddress is the querying address: `client` when it logged an address,
	// the address the logged host name resolved to through the DHCP leases, or the
	// logged host name verbatim when it resolved to no single address.
	ClientAddress string
	// ClientHostname is `client` when it logged a host name rather than an
	// address; nil otherwise.
	ClientHostname *string
	// ClientResolution says which: one of the ClientResolution constants. Empty is
	// ClientResolutionLoggedAddress.
	ClientResolution string
	// ClientID is the machine that address resolved to, when one is known.
	ClientID *int64
	// Domain is `domain`.
	Domain string
	// Resolver is `unbound` or `dnsmasq`.
	Resolver string
	// Action is `action`.
	Action string
	// AnswerSource is `source`.
	AnswerSource *string
	// Rcode is `rcode`.
	Rcode *string
	// DNSSECStatus is `dnssec_status`, stored verbatim. NULL is "not reported",
	// which is not "unvalidated".
	DNSSECStatus *string
	// BlocklistName is `blocklist`, resolved to a blocklist row at ingest. Empty
	// means the endpoint named no list, which for a blocked lookup is its own
	// state: blocked, list not recorded.
	BlocklistName string
	// LookedUpAt is `time`.
	LookedUpAt int64
	// IngestedAt is when opnview stored it.
	IngestedAt int64
}

// What a lookup's `client` field held, stored in dns_resolution.client_resolution.
const (
	// ClientResolutionLoggedAddress is a `client` that was an address.
	ClientResolutionLoggedAddress = "logged_address"
	// ClientResolutionLeased is a host name exactly one address held a DHCP
	// lease under at the lookup's instant.
	ClientResolutionLeased = "lease_hostname"
	// ClientResolutionAmbiguous is a host name several addresses held a
	// lease under at that instant.
	ClientResolutionAmbiguous = "ambiguous_hostname"
	// ClientResolutionUnknown is a host name neither a lease nor the resolver's
	// local data named at that instant.
	ClientResolutionUnknown = "unknown_hostname"
	// ClientResolutionLocalData is a host name no lease named and exactly one
	// address answered for in the resolver's local data held at that instant. The
	// term is opnview's own (docs/data-model.md, Vocabulary).
	ClientResolutionLocalData = "local_data_hostname"
	// ClientResolutionThisFirewall is a host name that names this firewall:
	// `localhost`, what a reverse lookup of a loopback address returns. A protocol
	// constant (decision 11 of the step-5A live corrections), not configuration.
	ClientResolutionThisFirewall = "this_firewall_hostname"
)

// SecurityEvent is one event from a provider of the security_event kind.
type SecurityEvent struct {
	// ProviderEventKey is the key the provider guarantees stable. For Suricata
	// it is composed from the eve file id and the byte offset inside it.
	ProviderEventKey string
	// OccurredAt is `timestamp`, normalised from the ISO string.
	OccurredAt int64
	// IngestedAt is when opnview stored it.
	IngestedAt int64
	// RuleIdentity is `alert_sid`, kept as text so a provider whose rules are
	// named rather than numbered fits.
	RuleIdentity string
	// Signature is the `alert` key, which the backend has already overwritten
	// with the signature text.
	Signature string
	// EventAction is `alert_action`, normalised.
	EventAction string
	// NormalisedSeverity is nil for every Suricata event: the backend destroys
	// the nested alert object, so severity is resolved through the rule-info
	// cache and from nowhere else.
	NormalisedSeverity *string
	// SrcAddress is `src_ip`.
	SrcAddress string
	// SrcPort is `src_port`.
	SrcPort *int64
	// DstAddress is `dest_ip`.
	DstAddress string
	// DstPort is `dest_port`.
	DstPort *int64
	// Protocol is `proto`.
	Protocol *string
	// InInterfaceDevice is `in_iface`.
	InInterfaceDevice *string
	// SrcClientID and SrcInterfaceID are resolved from the source address.
	SrcClientID *int64
	// SrcInterfaceID is the interface the event's source sits behind.
	SrcInterfaceID *int64
}

// EveCursor is one per-file byte-offset watermark over the alert feed.
type EveCursor struct {
	// FileID is `fileid`.
	FileID string
	// ByteOffset is the highest `filepos` read in that file.
	ByteOffset int64
	// FileSequence is get_alert_logs' `sequence`, which is how rotation is
	// detected.
	FileSequence *int64
	// RotationState is `current`, `rotated` or `lost`. `lost` is a permanent
	// loss and is recorded as a gap as well.
	RotationState string
	// ObservedAt is when the watermark was written.
	ObservedAt int64
}

// There is deliberately NO row shape for pair_volume_observation. That table is
// DERIVED and not collected -- the maintainer's ruling, recorded on the table
// itself -- and its rows are computed from `flow` by the refresh, through the
// statement insert_pair_volume of derive.sql, without passing through Go values.

// StateSnapshot is one provider's complete set of things of one type, as of one
// instant, with the things in it.
//
// The instant is what makes the set a set: because it is asserted COMPLETE at
// CapturedAt, a thing present in one snapshot and absent from the next has left.
type StateSnapshot struct {
	// ProviderID is the provider that reported the set.
	ProviderID int64
	// SetKey names which set, because one provider commonly reports several.
	SetKey string
	// CapturedAt is the instant at which the set was complete.
	CapturedAt int64
	// Items are the members. An empty set is a legitimate snapshot and is written
	// as one: a provider whose ban list is now empty has reported a departure for
	// every item it used to carry, and dropping the snapshot would hide that.
	Items []StateItem
}

// StateItem is one member of a reconciled set.
type StateItem struct {
	// ItemKey is the identity within the set, supplied by the provider and stored
	// verbatim. It is the token a departure is computed on.
	ItemKey string
	// Attributes is the provider's own fields as a JSON object, displayed and
	// never aggregated, filtered on or joined. Nil is a thing with no attributes.
	Attributes *string
	// ValidUntilAt is the end of the thing's own validity when the provider states
	// one. It is not how a departure is detected.
	ValidUntilAt *int64
}

// StateDeparture is one thing that was in the previous complete snapshot of a set
// and is not in the latest one.
type StateDeparture struct {
	// ProviderID and SetKey name the set it left.
	ProviderID int64
	// SetKey names which set.
	SetKey string
	// ItemKey is the thing that left.
	ItemKey string
	// Attributes are the attributes it carried when it was last present.
	Attributes *string
	// ValidUntilAt is the validity end it carried, when it carried one.
	ValidUntilAt *int64
	// LastPresentAt is the instant of the last snapshot that held it.
	LastPresentAt int64
	// AbsentSinceAt is the instant of the first snapshot that did not.
	AbsentSinceAt int64
}

// MeasurementSample is one numeric reading of one subject at one instant.
type MeasurementSample struct {
	// ProviderID names the provider that supplied the reading.
	ProviderID *int64
	// SubjectKind and SubjectKey name what was measured.
	SubjectKind SubjectKind
	// SubjectKey is the device name, the canonical pair, or the empty string.
	SubjectKey string
	// Measure is what was measured.
	Measure Measure
	// Unit is mandatory.
	Unit Unit
	// Value is the reading.
	Value float64
	// SampledAt is the instant of the reading.
	SampledAt int64
}

// CollectionGap is one interval opnview knows it did not cover.
type CollectionGap struct {
	// ProviderID is the provider whose data is missing.
	ProviderID int64
	// IntervalStartAt and IntervalEndAt bound what is missing.
	IntervalStartAt int64
	// IntervalEndAt is the end of the missing interval.
	IntervalEndAt int64
	// Reason is why, from the closed vocabulary.
	Reason GapReason
	// Detail is the free-text account.
	Detail *string
	// DetectedAt is when opnview noticed.
	DetectedAt int64
}
