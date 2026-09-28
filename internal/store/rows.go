package store

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

// The two scopes.
const (
	// ScopeEastWest is a flow whose both endpoints sit in a discovered interface.
	ScopeEastWest TrafficScope = "east_west"
	// ScopeNorthSouth is every other flow.
	ScopeNorthSouth TrafficScope = "north_south"
)

// ScopeOf returns the traffic scope of a flow from its interface membership. It
// is the one expression the schema's CHECK pins, written once here so a
// collector cannot disagree with the database.
func ScopeOf(srcInterfaceID, dstInterfaceID *int64) TrafficScope {
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
)

// SubjectKind is what a measurement sample measured.
type SubjectKind string

// The three subject kinds, matching the schema's CHECK.
const (
	// SubjectFirewall is the firewall as a whole. Its subject key is empty.
	SubjectFirewall SubjectKind = "firewall"
	// SubjectInterface is one interface. Its subject key is the network device
	// name, the token interface_map keys by.
	SubjectInterface SubjectKind = "interface"
	// SubjectEndpointPair is two addresses. Its subject key is the pair in
	// lexicographic order joined by a space — the same canonical ordering
	// pair_volume_observation enforces.
	SubjectEndpointPair SubjectKind = "endpoint_pair"
)

// Measure is what was measured. The vocabulary is opnview's own and closed,
// because the survey establishes the telemetry endpoints and not their field
// names: a measure named after a response key nobody has read would be an
// assertion about a shape nobody has seen.
type Measure string

// The measures, matching the schema's CHECK.
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
)

// Unit is the unit of a measurement value, matching the schema's CHECK. It is
// mandatory: a number with no unit is a number nobody can put on a screen.
type Unit string

// The units.
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
	// LogsMatches is API field `log`: whether the rule writes a filter-log line.
	// NULL means the source did not report the flag, which is not the same as
	// "does not log".
	LogsMatches *bool
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
	// StartsAt is the validity start. On Kea it is `expire` minus
	// `valid_lifetime`.
	StartsAt int64
	// ExpiresAt is `expire`, or `ends` on the legacy plugin.
	ExpiresAt *int64
	// ObservedAt is when opnview read the lease.
	ObservedAt int64
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
}

// DNSResolution is one resolver lookup.
type DNSResolution struct {
	// LookupKey is the identity of the lookup, stored in the column of the same
	// name. It is NOT the endpoint's `uuid`: that field is null on every row the
	// firewall returns, measured 2026-09-27, so what a provider cannot supply
	// opnview composes from the row's own content. The composition, and the
	// under-count it accepts, are recorded on the column itself.
	LookupKey string
	// ClientAddress is `client`.
	ClientAddress string
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

// PairVolumeObservation is one daily per-address-pair volume, de-duplicated.
//
// The de-duplication key is direction-free and interface-free, because the
// firewall's own aggregate writes each flow once per interface AND once per
// direction with the endpoints swapped: a direction-bearing key would
// double-count every volume. EndpointLow and EndpointHigh are set by the write
// path, not by the caller, so the canonical ordering cannot be got wrong.
type PairVolumeObservation struct {
	// DayStartAt is the start of the day the volume belongs to.
	DayStartAt int64
	// EndpointA and EndpointB are the two addresses in either order; the write
	// path sorts them.
	EndpointA string
	// EndpointB is the other address.
	EndpointB string
	// ServicePort is the aggregate's own min(src_port, dst_port) heuristic, a
	// hint rather than a destination port.
	ServicePort int64
	// Protocol is the protocol the aggregate reported.
	Protocol string
	// Octets and Packets are REAL because the aggregate pro-rates flows spanning
	// a slice boundary.
	Octets float64
	// Packets is the packet measure.
	Packets float64
	// ObservedDirection is kept for provenance only and is not part of the key.
	ObservedDirection string
	// ObservedInterfaceDevice is kept for provenance only.
	ObservedInterfaceDevice *string
	// LastSeenAt is the aggregate's `last_seen`.
	LastSeenAt int64
	// IngestedAt is when opnview stored it.
	IngestedAt int64
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
