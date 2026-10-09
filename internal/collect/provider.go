package collect

import (
	"context"
	"errors"

	"github.com/NaejEL/opnview/internal/decode"
	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/store"
)

// The provider seam.
//
// THERE ARE NOT FIVE COLLECTORS. THERE ARE FIVE KINDS, AND SEVERAL OF THEM ALREADY HAVE
// MORE THAN ONE IMPLEMENTATION. The schema's registry says so and has since the
// provider-neutral pass: `dhcp_lease` holds Kea, Dnsmasq and the end-of-life ISC plugin,
// and `dns_lookup` holds Unbound and Dnsmasq. On the firewall the survey probed, Dnsmasq
// serves DHCP and Kea is disabled, while Unbound AND Dnsmasq are both running as
// resolvers. Somebody else runs Kea. So the seam is the kind, and it is required by data
// that exists today rather than by a step nobody has specified.
//
// The shape, and the test it has to pass: adding an implementation is ADDING ONE FILE,
// `<kind>_<product>.go`, that satisfies one of the interfaces below, plus one row in the
// schema's registry. Nothing selects an implementation at compile time, nothing branches on
// a product name outside that product's own file, and detection chooses among them at
// runtime from the firewall's own configuration.
//
// THIS FILE IS THE CONTRACT AND registry.go IS THE TRANSPORT. What is written here is what a
// connector must SAY: the records it yields, the probe result it reports, the questions the
// kind may ask it. Nothing here says how it says it, and that is deliberate — the transport
// is in-process today and is meant to become separate processes speaking a protocol, and this
// file is the half that should survive that unchanged.
//
// THE CONTRACT HAS TWO DIRECTIONS AND BOTH ARE WRITTEN HERE. The interfaces say what a
// connector must PRODUCE. `session`, immediately below, says what it may CONSUME to produce it,
// and it is deliberately the shorter list: what the port withholds — the store, the discovery
// snapshot, availability, activation — is what makes the seam a boundary rather than a naming
// convention.
//
// WHAT EACH INTERFACE IS. It is the narrowest thing the store needs written, expressed in
// the vocabulary of what the KIND produces rather than of any product: a lease source
// yields lease observations, a lookup source yields lookups. Everything that is the same
// for every implementation of a kind — resolving the two join keys, composing a client
// identity, detecting a gap, recording availability, choosing which implementation is
// active — lives in the kind's own file and is written once.
//
// WHY THERE IS NO INTERFACE FOR THE geo_asn KIND HERE. Its one implementation is the MaxMind
// dataset, which internal/maxmind acquires and reads; it is not a firewall source, and the
// port below, whose only outbound capability is the firewall, could not serve it.

// session is THE PORT: everything an implementation may ask of the host that runs it, and
// nothing else. It is the other half of the contract — the interfaces below say what a
// connector must PRODUCE, and this says what it may CONSUME to produce it.
//
// It is declared as an interface rather than passed as *Collector for one reason, and it is
// not testability: *Collector is the whole layer. It holds the store, the discovery snapshot,
// the availability writer, the identity cascade and every kind's coordinator, and an
// implementation handed it could write a row, mint a client identity or re-run a probe round.
// None of those is a connector's business, and none of them can cross a process boundary,
// which is where this seam is going. So the boundary is stated in code, where the compiler
// keeps it, instead of in a comment asking implementations to behave.
//
// WHAT IT DELIBERATELY DOES NOT GRANT, because each absence is the thing that makes the port
// worth having:
//
//   - THE STORE. No implementation reads or writes a row. It returns records; the kind
//     decides what becomes of them. This is what lets a connector run somewhere with no
//     database at all.
//   - DISCOVERY. The firewall's own configuration is the kind's to join against. The one
//     implementation that needs a snapshot — the sampler — is HANDED it as a parameter of
//     sample, so what it may see is decided per call by the coordinator rather than fetched
//     at will. Granting Discovery() here would let any implementation read the whole
//     snapshot at any time, and nothing needs that.
//   - AVAILABILITY. An implementation REPORTS a probeResult; it never writes source
//     availability. The distinction is what keeps a connector from declaring itself healthy.
//   - ACTIVATION. Nothing here answers which implementation of a kind is active. That is
//     read from the firewall's configuration by the probe round, and an implementation that
//     could ask would be one step from preferring itself.
//   - THE CLOCK. now() yields the instant as the epoch every column stores, and that is all.
//     No implementation schedules, sleeps or waits.
//
// AND NOTHING THAT SERVES ONE KIND. Reading a page of a lease table is common to the two
// readable DHCP backends, so it is written once — as a FREE FUNCTION over this port, in
// dhcplease.go, not as a method on it. The distinction is the whole discipline: a port that
// gains a method whenever two implementations share code ends up as wide as the layer it was
// meant to narrow, and every one of those methods is a verb somebody has to implement on the
// far side of a wire.
//
// WHEN CONNECTORS MOVE OUT OF PROCESS THIS IS THE PROTOCOL. Each method becomes a request the
// connector sends to its host and each interface method below becomes one the host sends to
// the connector. That is why the list is short and why it stays short: a method added here is
// a verb added to a protocol, and the cost of removing one later is a compatibility break
// rather than a rename.
type session interface {
	// call issues one request to the firewall API and returns what it answered. It is the
	// ONLY outbound capability the port grants, and the client that performs it is not
	// exposed: an implementation states an endpoint and reads a response. The refusal to
	// write to the firewall is enforced below this, in internal/opnsense, which has no
	// request for a mutating command.
	call(ctx context.Context, endpoint opnsense.Endpoint, options opnsense.RequestOptions) (
		opnsense.Response, error)

	// serviceState probes an OPNsense service status endpoint. Every implementation whose
	// product is an OPNsense service asks the same question of the same shape of answer, so
	// the host answers it once.
	serviceState(ctx context.Context, endpoint opnsense.Endpoint) (
		running bool, state store.AvailabilityState, detail string, err error)

	// readObject calls an endpoint and decodes a JSON object, reporting whether that
	// worked. An unreadable answer is not an error: it is an availability state, and the
	// caller says so.
	readObject(ctx context.Context, endpoint opnsense.Endpoint) (decode.Object, bool, error)

	// readCollection calls an endpoint and returns its rows, whatever collection shape it
	// used, looking under the wrapper keys the caller names.
	readCollection(ctx context.Context, endpoint opnsense.Endpoint, wrapperKeys ...string) (
		[]decode.Object, bool, error)

	// normaliser returns the timestamp normaliser for this instant. It carries the
	// reference time the one year-less timestamp shape needs, which an implementation
	// cannot compose for itself without reading a clock, and the firewall's offset
	// from UTC, which it measures first if nothing has yet.
	normaliser(ctx context.Context) (decode.Normaliser, error)

	// now is the current instant as the UTC epoch every column stores.
	now() int64
}

// *Collector is the in-process host, and the only one today.
var _ session = (*Collector)(nil)

// ErrUnsupportedRead is returned by an implementation that is registered and probed but
// whose material opnview cannot read.
//
// It is a first-class outcome rather than a missing code path, and the two cases it covers
// are both measured. The end-of-life ISC plugin's lease endpoint answered 404 on the
// surveyed firmware and its contract is marked UNVERIFIED in the survey. The Dnsmasq
// resolver offers no structured query API at all: its per-client lookups exist only in the
// free-text line of the generic log endpoint, under a grammar that is dnsmasq's own rather
// than an OPNsense contract and that nobody has recorded. Writing either reader would mean
// guessing at a contract, so both are registered, probed and reported — and the reason
// travels with the refusal.
var ErrUnsupportedRead = errors.New("collect: this implementation is registered and its material cannot be read")

// probeResult is what one implementation's probe concluded.
type probeResult struct {
	// state is the availability state the survey names for what the firewall answered.
	state store.AvailabilityState
	// probe is the endpoint that determined it, recorded so a screen can say how the
	// conclusion was reached.
	probe opnsense.Endpoint
	// detail is the human-readable account, which is where the difference between
	// "switched off" and "unreachable" actually becomes legible.
	detail string
	// separable says whether the FIREWALL'S OWN CONFIGURATION marks this implementation as
	// the one serving clients. It is the only thing activation is allowed to read: when
	// two implementations of a kind are both separable the firewall has not chosen between
	// them, and opnview then reads neither rather than choosing for the user.
	separable bool
}

// logRecord is one filter-log record, decoded and normalised by its implementation and not
// yet joined to anything.
//
// The timestamp is already a UTC epoch and the action and direction are already the
// schema's vocabulary, because both are the implementation's business: the shapes a
// timestamp arrives in and the words a product uses for an action are facts about that
// product. Resolving the device and the rule is the kind's business.
type logRecord struct {
	Digest      string
	ObservedAt  int64
	Device      string
	SrcAddress  string
	DstAddress  string
	SrcPort     *int64
	DstPort     *int64
	Protocol    string
	IPVersion   int64
	Action      string
	Direction   string
	LogReason   *string
	PacketBytes int64
	Rid         *string
	// IPID and TCPSeq are the two fields verified to cross the firewall unchanged,
	// which pair the two records of one connection; nil where the record has none.
	IPID   *int64
	TCPSeq *int64
}

// firewallLogSource is one implementation of the firewall_log kind: a source of records of
// packets the firewall allowed or denied.
type firewallLogSource interface {
	// providerKey matches a provider_key in the schema's registry.
	providerKey() string
	// probe reports what the firewall says about this implementation.
	probe(ctx context.Context, host session) (probeResult, error)
	// records returns the most recent page, newest first, with the result of the call so
	// the kind can record it.
	records(ctx context.Context, host session, pageSize int) ([]logRecord, probeResult, error)
}

// alertRecord is one security event, decoded and normalised by its implementation.
type alertRecord struct {
	// FileID and ByteOffset are the ingestion coordinate this kind's cursor is keyed on.
	// They are not columns on the event: they answer where the reader stopped, which is a
	// different question from whether an event is already stored.
	FileID             string
	ByteOffset         int64
	OccurredAt         int64
	RuleIdentity       string
	Signature          string
	EventAction        string
	NormalisedSeverity *string
	SrcAddress         string
	DstAddress         string
	SrcPort            *int64
	DstPort            *int64
	Protocol           *string
	InInterfaceDevice  *string
}

// rotationView is what an implementation knows about the files its feed rotates through.
type rotationView struct {
	// known is false when the implementation could not ask. A watermarked file that is
	// merely unlisted because the call failed must not be declared lost.
	known bool
	// present holds the file ids the feed still carries.
	present map[string]struct{}
	// highestSequence names the current file, or is nil when the implementation does not
	// report a sequence.
	highestSequence *int64
}

// securityEventSource is one implementation of the security_event kind.
type securityEventSource interface {
	providerKey() string
	probe(ctx context.Context, host session) (probeResult, error)
	// rotation reports which of the feed's files still exist, which is how a permanent
	// loss is detected.
	rotation(ctx context.Context, host session) (rotationView, probeResult, error)
	// alerts returns the most recent events, newest first.
	alerts(ctx context.Context, host session, pageSize int) ([]alertRecord, probeResult, error)
}

// leaseObservation is one lease generation, with the field-name differences between
// backends already normalised away by the implementation that read it.
type leaseObservation struct {
	Address      string
	MAC          *string
	Hostname     *string
	DHCPClientID *string
	DUID         *string
	IAID         *string
	VendorHint   *string
	LeaseState   string
	// InterfaceIdentifier and InterfaceDevice are whichever of the interface's names the
	// backend reported; the kind resolves them against discovery. The DESCRIPTION the
	// backends also report is deliberately absent: an interface has one description and it
	// is discovered authoritatively elsewhere.
	InterfaceIdentifier string
	InterfaceDevice     string
	// StartsAt is the validity start, and nil when the backend reports none. It is a pointer
	// rather than a zero so that "the lease began at this instant" and "this backend cannot
	// say" are two different values: only Kea reports one, through `valid_lifetime`, and an
	// implementation that substituted anything here would be asserting a figure it read
	// nowhere. What keeps a re-poll idempotent is dhcp_lease.generation_key instead.
	StartsAt  *int64
	ExpiresAt *int64
}

// leaseSource is one implementation of the dhcp_lease kind.
type leaseSource interface {
	providerKey() string
	probe(ctx context.Context, host session) (probeResult, error)
	// leases returns the backend's current lease table, or ErrUnsupportedRead.
	leases(ctx context.Context, host session, pageSize int) ([]leaseObservation, probeResult, error)
}

// lookupRecord is one resolver lookup, decoded and normalised by its implementation.
type lookupRecord struct {
	// DeduplicationKey is composed by the implementation, because what can serve as a key
	// is a fact about the source. Unbound's own row identifier is null on every row a real
	// firewall returns, so its key is composed from the row's content.
	DeduplicationKey string
	ClientAddress    string
	Domain           string
	Action           string
	AnswerSource     *string
	Rcode            *string
	DNSSECStatus     *string
	// BlocklistName is empty when the source named no list, which for a blocked lookup is
	// its own state rather than an absence.
	BlocklistName string
	LookedUpAt    int64
}

// lookupSource is one implementation of the dns_lookup kind.
type lookupSource interface {
	providerKey() string
	probe(ctx context.Context, host session) (probeResult, error)
	// lookups returns one page, 1-based, or ErrUnsupportedRead.
	lookups(ctx context.Context, host session, page int) ([]lookupRecord, probeResult, error)
	// pageBound is how many pages one pass may walk. It is the implementation's to state:
	// Unbound's endpoint caps its buffer, so walking further re-reads what was already seen.
	pageBound() int
	// requestedSpanSeconds is the window the implementation asks for. Whether the source
	// honours it is a separate matter, and the kind never presents the answer as coverage
	// of it.
	requestedSpanSeconds() int64
}

// measurementSource is one implementation of the measurement_sample kind.
//
// The kind's material is a numeric reading of a subject at an instant. On OPNsense 26.7 no
// endpoint answers for a past window: the per-pair data is a live snapshot, so the
// implementation samples and opnview keeps the history. The same pass reads the firewall's own
// gauges, because a gauge and a sampled pair volume are the same five facts and one table
// carries both — which is why the kind exists and why eight of the ten surveyed sources that
// fit an existing shape fit this one.
type measurementSource interface {
	providerKey() string
	probe(ctx context.Context, host session) (probeResult, error)
	// sample returns the readings the firewall reports now, and the readings that did NOT
	// answer — named, so an absence is recorded rather than written as a zero.
	sample(ctx context.Context, host session, snapshot Discovery, providerID, now int64) (
		readings []store.MeasurementSample, missing []string, result probeResult, err error)
}
