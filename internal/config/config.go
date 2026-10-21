package config

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/NaejEL/opnview/internal/collect/provider"
	"github.com/NaejEL/opnview/internal/store/flows"
)

// MaxIntervalSeconds is the longest interval, in seconds, that a time.Duration
// can hold. A setting row above it cannot be represented: multiplied into
// nanoseconds it would overflow and come out negative or small, and a loop
// scheduled on that value would run flat out instead of rarely. Load therefore
// refuses it by name rather than letting the arithmetic decide.
//
// Rebuilt after the loss of 2 October 2026 from the compiled package of that
// evening; the comments in this file are rewritten.
const MaxIntervalSeconds int64 = math.MaxInt64 / int64(time.Second)

// The setting keys this package reads of its own. A core kind's intervals and page size are not here:
// each kind's file in internal/collect/provider/kinds declares them, with their keys, defaults and the
// reasons for them, and Config reads them generically (Interval, PageSize).
const (
	// KeyRetentionSeconds is the purge horizon in seconds. 0 means unlimited.
	KeyRetentionSeconds = "retention_seconds"
	// KeyAggregateMode is `full` or `no_domains`.
	KeyAggregateMode = "aggregate_mode"

	// KeyDiscoveryInterval overrides the runtime-discovery refresh interval. The
	// store reads the same row as the margin of "held at an instant" for this
	// firewall's own addresses, so the key lives there.
	KeyDiscoveryInterval = flows.DiscoveryIntervalKey
	// KeyPurgeInterval overrides the retention-purge interval.
	KeyPurgeInterval = "purge_interval_seconds"
	// KeyAttributionMaxDelay is how long before a flow, in seconds, a resolver lookup
	// may have been made and still name it. The schema writes its default row.
	KeyAttributionMaxDelay = "attribution_max_delay_seconds"
	// KeyCacheAnswerDelay is how long before a flow, in seconds, a resolver lookup may
	// have been made and still name it through the resolver's cache (decision 2 of
	// specs/SPEC-resolver-cache-attribution.md). The schema writes its default row.
	KeyCacheAnswerDelay = "attribution_max_cache_answer_delay_seconds"
	// KeyLegPairingWindow is how far apart, in seconds, the two records of one
	// connection may have been logged and still be paired (decision 4 of the step-5A
	// live corrections). The schema writes its default row.
	KeyLegPairingWindow = flows.PairingWindowKey
	// KeyHeartbeatInterval is how often, in seconds, the running service records that
	// it is alive (SPEC-step-5b-widget-api.md, D2). The schema writes its default row.
	KeyHeartbeatInterval = "heartbeat_interval_seconds"
	// KeyDowntimeThreshold is how long, in seconds, the service must have been gone for
	// the interval to be recorded as opnview not running. The schema writes its default
	// row.
	KeyDowntimeThreshold = "downtime_threshold_seconds"
)

// The default intervals of the core's own loops. A kind's cadence is its kind file's, with the survey
// section that justifies it named beside it.
const (
	// DefaultDiscoveryInterval is 300 s. Survey, "Runtime discovery":
	// "Everything here is read at startup and refreshed periodically." The
	// survey gives no figure, so this one is opnview's own and is stated as
	// such: it matches the slowest collector, which is the fastest rate at
	// which a newly discovered interface could matter to an ingest.
	DefaultDiscoveryInterval = flows.DefaultDiscoveryIntervalSeconds * time.Second

	// DefaultPurgeInterval is 3600 s. No survey section bears on it: the purge
	// reads no endpoint. It is opnview's own, chosen so that the database is
	// bounded within an hour of a retention change without the delete
	// competing with ingestion every minute.
	DefaultPurgeInterval = 3600 * time.Second
)

// DefaultRetentionSeconds is 90 days, the documented default. It is written in
// the schema and read from the database; the constant here exists only so a
// database with no row still behaves.
const DefaultRetentionSeconds int64 = 7776000

// DefaultAttributionMaxDelaySeconds is 5 s, the maintainer's default for how long
// before a flow a resolver lookup may have been made and still name it. Like the
// retention horizon it is written in the schema and read from the database; the
// constant exists only so a database with no row still behaves.
const DefaultAttributionMaxDelaySeconds int64 = 5

// DefaultCacheAnswerDelaySeconds is 3600 s, the maintainer's default cap on how long
// before a flow a lookup may have been made and still name it through the resolver's
// cache (decision 2). It is written in the schema and read from the database; the
// constant exists only so a database with no row still behaves.
const DefaultCacheAnswerDelaySeconds int64 = 3600

// DefaultLegPairingWindowSeconds is 1 s, the maintainer's default for how far apart
// the two records of one connection may have been logged and still be paired
// (decision 4). It is written in the schema and read from the database; the constant
// exists only so a database with no row still behaves.
const DefaultLegPairingWindowSeconds int64 = flows.DefaultPairingWindowSeconds

// MaxLegPairingWindowSeconds is the largest pairing window accepted: 60 s, the
// maintainer's decision 13 of specs/SPEC-step-5b-widget-api.md.
//
// WHY A BOUND AT ALL. The two records of one connection are the same packet logged
// on two interfaces, written by pf within the same instant; the default of 1 s
// already allows for the filter log's whole-second timestamps. A window of minutes
// would pair records of DIFFERENT connections that merely share their two ends and
// port, and each such false pair drops a real connection from every total, which is
// a wrong figure that looks right. A minute is generous beyond anything a log
// timestamp needs and still short of that failure; a value above it is refused at
// load, naming the setting, rather than stored and quietly applied.
const MaxLegPairingWindowSeconds int64 = 60

// DefaultHeartbeatIntervalSeconds is 60 s. After a crash the last beat is the only bound
// opnview holds on when it stopped, so the recorded interval can start up to one heartbeat
// early; a minute is finer than every collection interval but the filter log's, and one
// single-row UPDATE a minute costs nothing measurable beside the collection's writes.
const DefaultHeartbeatIntervalSeconds int64 = 60

// DefaultDowntimeThresholdSeconds is 300 s: five heartbeats, so a beat delayed by a busy
// moment is never read as downtime, and the five minutes of the default lease and local-data
// intervals, so a restart for an update -- a few seconds -- records nothing, while an
// absence long enough for a source to have been left unread for a whole poll is recorded.
const DefaultDowntimeThresholdSeconds int64 = 300

// Config is the whole of opnview's runtime configuration.
type Config struct {
	// RetentionSeconds is the purge horizon. 0 means unlimited.
	RetentionSeconds int64
	// AggregateMode is `full` or `no_domains`.
	AggregateMode string

	// DiscoveryInterval and PurgeInterval are the cadences of the core's own loops.
	DiscoveryInterval time.Duration
	PurgeInterval     time.Duration
	// Intervals are the core kinds' cadences in force and PageSizes their page sizes, each by the
	// setting key its kind file declares (Interval, PageSize).
	Intervals map[string]time.Duration
	PageSizes map[string]int

	// AttributionMaxDelaySeconds is how long before a flow a resolver lookup may
	// have been made and still name it.
	AttributionMaxDelaySeconds int64
	// CacheAnswerDelaySeconds is how long before a flow, at most, a resolver lookup may
	// have been made and still name it through the resolver's cache.
	CacheAnswerDelaySeconds int64
	// LegPairingWindowSeconds is how far apart the two records of one connection may
	// have been logged and still be paired.
	LegPairingWindowSeconds int64
	// HeartbeatIntervalSeconds is how often the running service records that it is alive,
	// and DowntimeThresholdSeconds how long it must have been gone for the interval to be
	// recorded as opnview not running.
	HeartbeatIntervalSeconds int64
	DowntimeThresholdSeconds int64
	// LiveInterestSeconds is the lease a live-figure request renews, and
	// LiveSampleRetentionSeconds how long a fine sample is kept (liverate.go).
	LiveInterestSeconds        int64
	LiveSampleRetentionSeconds int64
}

// Defaults returns the configuration of a database that carries no setting row
// at all. It holds no firewall URL and no credential, and it never will: those
// reach opnview through the interface.
func Defaults() Config {
	return Config{
		RetentionSeconds:           DefaultRetentionSeconds,
		AggregateMode:              "full",
		CacheAnswerDelaySeconds:    DefaultCacheAnswerDelaySeconds,
		DiscoveryInterval:          DefaultDiscoveryInterval,
		PurgeInterval:              DefaultPurgeInterval,
		Intervals:                  kindIntervalDefaults(),
		PageSizes:                  kindPageDefaults(),
		AttributionMaxDelaySeconds: DefaultAttributionMaxDelaySeconds,
		LegPairingWindowSeconds:    DefaultLegPairingWindowSeconds,
		HeartbeatIntervalSeconds:   DefaultHeartbeatIntervalSeconds,
		DowntimeThresholdSeconds:   DefaultDowntimeThresholdSeconds,
		LiveInterestSeconds:        DefaultLiveInterestSeconds,
		LiveSampleRetentionSeconds: DefaultLiveSampleRetentionSeconds,
	}
}

// SettingReader is the one thing this package needs from storage. It is an
// interface so that config depends on no storage engine, and store depends on
// no configuration; *store.Store satisfies it.
type SettingReader interface {
	// Setting returns the value of one key, and whether the row exists.
	Setting(ctx context.Context, key string) (string, bool, error)
}

// Load reads the configuration from the `setting` table, falling back to the
// defaults key by key. A row whose value cannot be read as the type the key
// requires is an error rather than a silent fallback: a setting somebody typed
// wrong must be visible, not quietly ignored.
func Load(ctx context.Context, reader SettingReader) (Config, error) {
	loaded := Defaults()

	if value, present, err := reader.Setting(ctx, KeyAggregateMode); err != nil {
		return loaded, err
	} else if present {
		if value != "full" && value != "no_domains" {
			return loaded, fmt.Errorf("config: %s is %q, which is neither full nor no_domains", KeyAggregateMode, value)
		}
		loaded.AggregateMode = value
	}

	// Every other core setting is a whole number within bounds, refused with its key otherwise:
	//   - the retention horizon, 0 or more seconds;
	//   - the attribution delay and the cache-answer cap, positive seconds;
	//   - the pairing window, 0 included -- the two records logged in the same second -- to its maximum;
	//   - the lease failure limit, one lease pass or more;
	//   - the heartbeat interval and the downtime threshold, positive seconds, the threshold longer than
	//     the interval: a running service leaves up to one interval between two beats, so a threshold at
	//     or below it would record the service's own cadence as downtime (SPEC-step-5b-widget-api.md, D2).
	for _, setting := range []struct {
		key      string
		min, max int64
		target   *int64
	}{
		{KeyRetentionSeconds, 0, math.MaxInt64, &loaded.RetentionSeconds},
		{KeyAttributionMaxDelay, 1, MaxIntervalSeconds, &loaded.AttributionMaxDelaySeconds},
		{KeyCacheAnswerDelay, 1, MaxIntervalSeconds, &loaded.CacheAnswerDelaySeconds},
		{KeyLegPairingWindow, 0, MaxLegPairingWindowSeconds, &loaded.LegPairingWindowSeconds},
		{KeyHeartbeatInterval, 1, MaxIntervalSeconds, &loaded.HeartbeatIntervalSeconds},
		{KeyDowntimeThreshold, 1, MaxIntervalSeconds, &loaded.DowntimeThresholdSeconds},
	} {
		if _, err := readWhole(ctx, reader, setting.key, setting.min, setting.max, setting.target); err != nil {
			return loaded, err
		}
	}
	if loaded.DowntimeThresholdSeconds <= loaded.HeartbeatIntervalSeconds {
		return loaded, fmt.Errorf("config: %s (%d s) must be longer than %s (%d s), or the service's "+
			"own heartbeat cadence would read as downtime", KeyDowntimeThreshold,
			loaded.DowntimeThresholdSeconds, KeyHeartbeatInterval, loaded.HeartbeatIntervalSeconds)
	}

	// The core's own loops and every core kind's cadences are read the same way: a kind's under the key
	// its kind file declares (internal/collect/provider/kinds), its default the file's. Each is a positive
	// number of seconds that a duration can hold.
	for _, interval := range []struct {
		key    string
		target *time.Duration
	}{
		{KeyDiscoveryInterval, &loaded.DiscoveryInterval},
		{KeyPurgeInterval, &loaded.PurgeInterval},
	} {
		if err := readInterval(ctx, reader, interval.key, interval.target); err != nil {
			return loaded, err
		}
	}
	for _, cadence := range provider.Cadences() {
		interval := cadence.Default()
		if err := readInterval(ctx, reader, cadence.Setting, &interval); err != nil {
			return loaded, err
		}
		loaded.Intervals[cadence.Setting] = interval
	}

	// The page sizes are read the same way. A page size that is not a positive count is refused; one
	// above what the firewall serves is not: it is stored and reported as above the ceiling on the
	// collection surface, where the operator can see that the firewall will not honour it.
	for _, pageSize := range provider.PageSizes() {
		var count int64
		if present, err := readWhole(ctx, reader, pageSize.Setting, 1, math.MaxInt32, &count); err != nil {
			return loaded, err
		} else if present {
			loaded.PageSizes[pageSize.Setting] = int(count)
		}
	}
	if err := loadLive(ctx, reader, &loaded); err != nil {
		return loaded, err
	}
	return loaded, nil
}

// readWhole reads one setting that is a whole number from min to max into target, which keeps its
// value when no row holds the setting, and reports whether one did. A value outside that is refused
// with the setting's key.
func readWhole(ctx context.Context, reader SettingReader, key string, min, max int64, target *int64) (bool, error) {
	value, present, err := reader.Setting(ctx, key)
	if err != nil || !present {
		return false, err
	}
	number, err := strconv.ParseInt(value, 10, 64)
	if err != nil || number < min || number > max {
		return true, fmt.Errorf("config: %s must be a whole number from %d to %d, got %q", key, min, max, value)
	}
	*target = number
	return true, nil
}

// readInterval reads one setting that is a positive number of seconds into a duration.
func readInterval(ctx context.Context, reader SettingReader, key string, target *time.Duration) error {
	seconds := int64(*target / time.Second)
	if _, err := readWhole(ctx, reader, key, 1, MaxIntervalSeconds, &seconds); err != nil {
		return err
	}
	*target = time.Duration(seconds) * time.Second
	return nil
}

// ErrNoDataDir is returned when --data-dir was not given. There is deliberately
// no default: a default would pre-empt the directory layout step 8 chooses for
// the LXC, and a program that silently writes a database somewhere plausible is
// worse than one that refuses to start.
var ErrNoDataDir = errors.New("config: --data-dir is required and has no default")

// Selection is the operator's decision about one source: whether opnview reads
// it. The word is opnview's own; OPNsense has none, as nothing there decides it.
//
// IT SEPARATES A DECISION FROM A CLAIM. Before it, provider.is_active was written
// by the probe round and by nothing else, and three things followed from that,
// all the same mistake — an algorithm deciding what a person should decide:
//
//   - one failed probe dropped a source that works, until the next round;
//   - a source that was merely quiet when probed looked exactly like one that
//     was absent, and nothing let an operator say which it was;
//   - where two implementations of an exclusive kind both answered, the round
//     read neither, and no person could break the tie.
//
// A selection is stored per registry row, as a `setting` row whose key
// KeySourceSelection composes. Availability is untouched by it: what the
// firewall reported stays recorded as reported, so a source kept on while it
// reports unavailable is a legitimate state, and the gap and availability rows
// then say what actually happened.
//
// A `setting` row rather than a column on `provider`: that table is where this
// project keeps configuration, and a column would need a migration on a
// database that holds real data — which ROADMAP.md makes a threshold decision
// rather than something to slip into an unrelated change.
//
// Turning one source on is also what breaks the exclusive-kind ambiguity: that
// rule exists because the firewall's configuration did not separate two
// implementations, and a person saying which one they want is precisely the
// separation it was missing.
type Selection string

const (
	// SelectionAuto leaves the decision to the probe round, which reads the
	// firewall's own configuration. It is what a missing row means, so an
	// installation nobody configured behaves as it always did.
	SelectionAuto Selection = "auto"

	// SelectionOn reads the source whatever the probe concluded, including
	// a probe that failed.
	SelectionOn Selection = "on"
	// SelectionOff never reads the source, whatever the probe concluded; the
	// probe still records what the firewall reports.
	SelectionOff Selection = "off"
)

// KeySourceSelection is the `setting` key holding the selection of one registry
// row, identified by its kind and its provider key.
//
// The key is composed from the registry row, so no provider, product or kind is
// enumerated here: a provider added to the registry has a selection without a
// line of this package changing. The same provider key under two kinds — the
// dnsmasq lease reader and the dnsmasq resolver — is two selections.
func KeySourceSelection(kind, providerKey string) string {
	return "source_selection_" + kind + "_" + providerKey
}

// ParseSelection reads a stored selection. Only the three words are accepted,
// exactly as written: a value somebody typed wrong is an error to report, not a
// guess to make. The selection returned with an error is auto and means
// nothing.
func ParseSelection(value string) (Selection, error) {
	switch selection := Selection(value); selection {
	case SelectionAuto, SelectionOn, SelectionOff:
		return selection, nil
	}
	return SelectionAuto,
		fmt.Errorf("config: %q is not auto, on or off", value)
}

// LoadSourceSelection reads the selection of one registry row; no row is auto. A
// row that cannot be read is an error naming the key, never auto.
func LoadSourceSelection(ctx context.Context, reader SettingReader, kind, providerKey string) (Selection, error) {
	// The key names the row in every error, so the operator can find it.
	key := KeySourceSelection(kind, providerKey)
	value, present, err := reader.Setting(ctx, key)
	if err != nil {
		return SelectionAuto, err
	}
	if !present {
		return SelectionAuto, nil
	}
	selection, err := ParseSelection(value)
	if err != nil {
		return SelectionAuto, fmt.Errorf("config: %s: %w", key, err)
	}
	return selection, nil
}

// kindIntervalDefaults are the core kinds' cadences with no setting row, by setting key.
func kindIntervalDefaults() map[string]time.Duration {
	defaults := map[string]time.Duration{}
	for _, cadence := range provider.Cadences() {
		defaults[cadence.Setting] = cadence.Default()
	}
	return defaults
}

// kindPageDefaults are the core kinds' page sizes with no setting row, by setting key.
func kindPageDefaults() map[string]int {
	defaults := map[string]int{}
	for _, page := range provider.PageSizes() {
		defaults[page.Setting] = page.Default
	}
	return defaults
}

// Interval is a core kind's cadence of a role in force: the setting row its kind file names, or the
// file's default. A kind with no such cadence answers zero, and its caller supplies its own.
func (c Config) Interval(kind, role string) time.Duration {
	cadence, found := provider.CadenceOfKind(kind, role)
	if !found {
		return 0
	}
	if interval, set := c.Intervals[cadence.Setting]; set {
		return interval
	}
	return cadence.Default()
}

// PageSize is a core kind's page size in force: the setting row its kind file names, or the file's
// default. A kind whose page is not a setting answers zero.
func (c Config) PageSize(kind string) int {
	contract, found := provider.KindOf(kind)
	if !found || contract.PageSize == nil {
		return 0
	}
	if size, set := c.PageSizes[contract.PageSize.Setting]; set {
		return size
	}
	return contract.PageSize.Default
}

// KindSettingKeys are the setting keys the core kinds' files declare: every cadence's and every page
// size's, by kind.
func KindSettingKeys() []string {
	var keys []string
	for _, cadence := range provider.Cadences() {
		keys = append(keys, cadence.Setting)
	}
	for _, page := range provider.PageSizes() {
		keys = append(keys, page.Setting)
	}
	return keys
}

// WithInterval is the configuration with one core kind's cadence of a role set, the receiver untouched:
// its maps are copied, so a Config handed to Live.Set is never changed by a later edit of another.
func (c Config) WithInterval(kind, role string, interval time.Duration) Config {
	cadence, found := provider.CadenceOfKind(kind, role)
	if !found {
		return c
	}
	intervals := make(map[string]time.Duration, len(c.Intervals)+1)
	for key, value := range c.Intervals {
		intervals[key] = value
	}
	intervals[cadence.Setting] = interval
	c.Intervals = intervals
	return c
}

// WithPageSize is the configuration with one core kind's page size set, the receiver untouched.
func (c Config) WithPageSize(kind string, size int) Config {
	contract, found := provider.KindOf(kind)
	if !found || contract.PageSize == nil {
		return c
	}
	sizes := make(map[string]int, len(c.PageSizes)+1)
	for key, value := range c.PageSizes {
		sizes[key] = value
	}
	sizes[contract.PageSize.Setting] = size
	c.PageSizes = sizes
	return c
}
