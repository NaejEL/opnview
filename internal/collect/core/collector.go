package core

import (
	"sync"

	"github.com/NaejEL/opnview/internal/broker"
	"github.com/NaejEL/opnview/internal/collect/provider"
	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/store"
	"github.com/NaejEL/opnview/internal/store/vocab"
)

// Collector is the state every part of the collector shares: the one HTTP client, the
// store, the clock, and the discovery snapshot they join against.
type Collector struct {
	// Host is the session port an implementation is handed: the one outbound capability, the
	// normaliser and the clock, and nothing else (provider.Session). It is embedded so that the
	// collector IS the session of every implementation it runs.
	*provider.Host

	// sources are the implementations this collector reads: the built-in plugins and the
	// plugins of its data directory, as the plugin host loaded them (internal/collect/sources).
	SourceSet *provider.Set

	// Triggers routes the core notifications to the loops of the providers that named them
	// (notifications.go).
	Triggers triggers

	Client *opnsense.Client
	// broker is the one path to the firewall API: the collector's host, discovery and the probe
	// round ask it.
	APIBroker *broker.Broker
	// Cadences are the intervals in force, each a core kind's readers' max age.
	Cadences config.Config
	Store    *store.Store
	Clock    Clock

	// firewallOffsetSeconds is survey gap 7: the firewall's offset from UTC, needed
	// by the timestamps that carry no zone. It is zero until discovery has measured
	// it from the firewall's own clock, and measured again on every refresh; the
	// mutex below guards it.
	OffsetSeconds int
	// OffsetMeasured says whether it has been measured at least once.
	OffsetMeasured bool

	// pageSizes is how many records each paged read asks for. It is the operator's
	// setting, held here rather than read from the database on every pass, and
	// replaced by Configure when the collection surface saves a new one; the mutex
	// below guards it with the discovery snapshot. A collector reads it once per
	// pass, through pages.
	PageSizeSettings config.PageSizes

	// PushPaths are the source periods of the providers fed by two paths (pushpaths.go).
	PushPaths PushPaths

	Mutex             sync.RWMutex
	DiscoverySnapshot Discovery
	// Probed is what the last probe round found for each kind, kept so that a
	// selection the operator saves can be applied at once, without probing the
	// firewall again; see Reselect. The mutex above guards it.
	Probed map[string][]ProbedSource

	// MaxDelaySeconds is the attribution_max_delay_seconds setting: how long before a
	// flow a lookup may have been made and still name it. The mutex guards it.
	MaxDelaySeconds int64
	// CacheAnswerDelaySeconds is the attribution_max_cache_answer_delay_seconds
	// setting: how long before a flow, at most, a lookup may have been made and still
	// name it through the resolver's records. The mutex guards it.
	CacheAnswerDelaySeconds int64
	// ReadDetails holds, per provider of the two resolver-record kinds, what its last
	// read recorded in words -- the counts, the size, the time, or the absence -- so the
	// probe round's rewrite of the availability row restates it rather than erasing it.
	// The mutex guards it.
	ReadDetails map[int64]string
	// ReadOutcomes holds the outcome code of the same reads, beside their words.
	ReadOutcomes map[int64]vocab.AvailabilityOutcome
	// LeaseReadAt and localDataReadAt are the instants the last complete lease pass and
	// the last successful local-data pass of this run began, zero before either; and
	// localDataFailed whether the last local-data pass failed. The unresolved host-name
	// lookups are examined only once every source in use has been read after they were
	// ingested; see hostnameBound. The mutex guards all three.
	LeaseReadAt     int64
	LocalDataReadAt int64
	LocalDataFailed bool
	// LastRefreshAt is the instant of the last successful aggregate refresh, and
	// onLinkFingerprint what classification read of the interfaces themselves --
	// which are upstream, their link-local rule, the networks that count -- at the
	// last derivation that placed every address; the mutex guards both.
	LastRefreshAt     int64
	OnLinkFingerprint string
	// SampleAbsences holds, per measurement provider, what its last sampling pass could
	// not read, in words, so the probe round's rewrite of the availability row restates
	// it rather than erasing it. The mutex guards it.
	SampleAbsences map[int64]string
	// PairingWindowSeconds is the leg_pairing_window_seconds setting: how far apart
	// the two records of one connection may have been logged and still be paired. The
	// mutex guards it.
	PairingWindowSeconds int64
	// LeaseFailureLimit is the lease_backend_failure_pass_limit setting, and
	// leaseFailures how many lease passes in a row each lease backend has failed,
	// by provider id; see collectLeases. The mutex guards both.
	LeaseFailureLimit int64
	LeaseFailures     map[int64]int64
	// Ingesting holds the ingestion instant of every pass that has stamped rows with
	// it and has not yet finished writing them, keyed by a sequence number; the
	// mutex guards both. The refresh never moves its watermark past the oldest of
	// them, because rows stamped with that instant may commit after the refresh
	// has read; see beginIngest.
	Ingesting      map[uint64]int64
	IngestSequence uint64
	// DeriveMutex serialises the derivations, so two passes finishing together
	// never refresh one slot from two half-classified states.
	DeriveMutex sync.Mutex
	// PurgeMutex orders the retention purge against the passes: a pass holds it for
	// reading from before it stores its first row until its derivation has ended, and
	// the purge holds it for writing. It is taken before deriveMutex, never after; see
	// purge.go.
	PurgeMutex sync.RWMutex
	// Services is what the collector last heard from each service kind (services.go).
	Services Services
	// Hooks are the test hooks of the purge's ordering, zero outside a test.
	Hooks OrderingHooks
	// Live is the live sampling: the lease it runs under and when each live pass last ran
	// (live.go).
	Live LiveSampling
	// Netflow is the flow_record kind's state: the filter log's read-through instant, the join
	// tolerance, the capture histories and what is to be derived again (flowrecord.go).
	Netflow NetflowState
}
