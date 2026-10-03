// Package config is the only reader of configuration in opnview.
//
// There are exactly two sources, and no third: the `setting` table, and the
// defaults written down here. There is no environment variable, no
// configuration file and no `configure` subcommand — the firewall URL, the API
// key and secret and the MaxMind key are entered in opnview's own settings
// surface and nowhere else.
//
// Since cycle 4B this package also SURFACES THE CREDENTIALS TO THE COLLECTORS and
// notices a change to them without a restart. That is credentials.go: the
// `setting` rows holding the firewall URL and the API key, the decryption of the
// API secret from `encrypted_credential`, the three named states those can be in,
// and the live holder the OPNsense client reads on every call. live.go does the
// same for the rest of the configuration.
//
// Every default here is a duration, a mode or a page size. NO INTERFACE NAME,
// VLAN NAME, ADDRESS, CIDR OR COUNT OF THINGS ON THE NETWORK IS A DEFAULT, because
// every one of those is discovered at runtime through the API.
package config

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"
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

// The setting keys this package reads. Each poll interval is overridable by an
// optional row: the default is the cadence the survey justifies, and a busy or
// a quiet installation is entitled to a different one.
const (
	// KeyRetentionSeconds is the purge horizon in seconds. 0 means unlimited.
	KeyRetentionSeconds = "retention_seconds"
	// KeyAggregateMode is `full` or `no_domains`.
	KeyAggregateMode = "aggregate_mode"

	// KeyFirewallLogInterval overrides the filter-log poll interval.
	KeyFirewallLogInterval = "poll_interval_firewall_log_seconds"
	// KeySecurityEventInterval overrides the eve.json alert poll interval.
	KeySecurityEventInterval = "poll_interval_security_event_seconds"
	// KeyMeasurementInterval overrides the sampled-measurement interval.
	KeyMeasurementInterval = "poll_interval_measurement_seconds"
	// KeyDHCPLeaseInterval overrides the lease poll interval.
	KeyDHCPLeaseInterval = "poll_interval_dhcp_lease_seconds"
	// KeyDNSLookupInterval overrides the resolver poll interval.
	KeyDNSLookupInterval = "poll_interval_dns_lookup_seconds"
	// KeyDiscoveryInterval overrides the runtime-discovery refresh interval.
	KeyDiscoveryInterval = "refresh_interval_discovery_seconds"
	// KeyPurgeInterval overrides the retention-purge interval.
	KeyPurgeInterval = "purge_interval_seconds"
)

// The setting keys of the page sizes: how many records one request asks the
// firewall for, on the three paged reads.
//
// AN INTERVAL ALONE DOES NOT SIZE A PAGED READ. What a poll can lose is decided
// by the pair: a poll every ten seconds that asks for 500 records keeps up with a
// source producing fewer than fifty a second and silently drops the rest. The
// interval was configurable and the page was a constant, so half of the pair was
// out of the operator's reach; these rows put it back.
//
// The other two collectors take no page size, for reasons of the API rather than
// of opnview: the per-pair sampler reads a snapshot with no paging at all, and
// the resolver endpoint answers with its whole ring buffer up to a limit the
// firewall fixes.
//
// A page size above what the firewall serves is accepted here and reported on
// the collection surface; refusing it would hide the measurement that shows why
// it was typed.
const (
	// KeyFirewallLogPageSize overrides the filter-log page size.
	KeyFirewallLogPageSize = "page_size_firewall_log"
	// KeySecurityEventPageSize overrides the eve.json alert page size.
	KeySecurityEventPageSize = "page_size_security_event"
	// KeyDHCPLeasePageSize overrides the lease page size.
	KeyDHCPLeasePageSize = "page_size_dhcp_lease"
)

// The default poll intervals. Each one is the cadence the survey justifies, and
// the survey section that justifies it is named beside it — not paraphrased,
// because the reasoning is what makes the number defensible.
const (
	// DefaultFirewallLogInterval is 10 s. Survey, data source 1, "Sustainable
	// polling frequency": each poll is bounded by `limit`, so its cost is small,
	// and the risk runs the other way — a busy ruleset can produce more than
	// `limit` lines between two polls and the API offers no way to recover a
	// gap. The verified section adds the measurement that makes it safe: the log
	// runs at a few lines per second on an ordinary home network, so 10 s at a
	// generous limit leaves an order of magnitude of headroom, and it must be
	// measured per installation rather than assumed.
	DefaultFirewallLogInterval = 10 * time.Second

	// DefaultSecurityEventInterval is 60 s. Survey, data source 2, "Sustainable
	// polling frequency": alerts are low-volume and the backend re-reads the
	// file backwards from the end on every call, so polling more often costs
	// without benefit.
	DefaultSecurityEventInterval = 60 * time.Second

	// DefaultMeasurementInterval is 300 s. Survey, data source 3, "Sustainable
	// polling frequency": 300 s is the finest resolution at which the firewall
	// keeps per-source data and the retention of that resolution is one hour.
	// The verified section turns that from a cache cadence into a sampling
	// cadence — /api/diagnostics/traffic/top is a live snapshot and nothing
	// upstream answers for a past window, so this interval is the resolution of
	// opnview's own per-pair history rather than a refresh rate.
	DefaultMeasurementInterval = 300 * time.Second

	// DefaultDHCPLeaseInterval is 300 s. Survey, data source 4, "Sustainable
	// polling frequency": leases change on the scale of minutes to hours, the
	// table is small, and the Kea path costs a control-agent round trip.
	DefaultDHCPLeaseInterval = 300 * time.Second

	// DefaultDNSLookupInterval is 60 s. Survey, data source 5, "Sustainable
	// polling frequency": DNS lookups must be correlated with flows that follow
	// within seconds, so the window has to be short. The verified section
	// removes the windowing but not the urgency: the endpoint is a ring buffer
	// of the last 1000 lookups, so a slow poll loses the oldest of them
	// outright.
	DefaultDNSLookupInterval = 60 * time.Second

	// DefaultDiscoveryInterval is 300 s. Survey, "Runtime discovery":
	// "Everything here is read at startup and refreshed periodically." The
	// survey gives no figure, so this one is opnview's own and is stated as
	// such: it matches the slowest collector, which is the fastest rate at
	// which a newly discovered interface could matter to an ingest.
	DefaultDiscoveryInterval = 300 * time.Second

	// DefaultPurgeInterval is 3600 s. No survey section bears on it: the purge
	// reads no endpoint. It is opnview's own, chosen so that the database is
	// bounded within an hour of a retention change without the delete
	// competing with ingestion every minute.
	DefaultPurgeInterval = 3600 * time.Second
)

// The default page sizes. 500 is the figure the collectors carried as a
// constant before the page became a setting, and nothing more: it is a
// documented starting point, not a measurement and not a survey figure. What
// a given installation needs depends on how fast its sources write, which only
// that installation can measure — the collection surface measures it and
// suggests a pair.
//
// Each default is within what the firewall serves for that read, so a database
// with no row never asks for a page the firewall would cut short.
const (
	// DefaultFirewallLogPageSize is the filter-log page size with no row.
	DefaultFirewallLogPageSize = 500

	// DefaultSecurityEventPageSize is the eve.json alert page size with no
	// row. The alert feed is low-volume, so a page this size is far more than a
	// poll at the default interval returns.
	DefaultSecurityEventPageSize = 500

	// DefaultDHCPLeasePageSize is the lease page size with no row.
	DefaultDHCPLeasePageSize = 500
)

// PageSizes is the page-size half of the configuration, on its own, for the
// collectors: they size their reads and need nothing else from Config, and a
// test that builds a collector should not have to invent seven intervals to
// say how many records a page holds.
type PageSizes struct {
	FirewallLog   int
	SecurityEvent int
	DHCPLease     int
}

// DefaultPageSizes returns the page sizes of a database with no page-size row.
func DefaultPageSizes() PageSizes {
	return PageSizes{
		FirewallLog:   DefaultFirewallLogPageSize,
		SecurityEvent: DefaultSecurityEventPageSize,
		DHCPLease:     DefaultDHCPLeasePageSize,
	}
}

// DefaultRetentionSeconds is 90 days, the documented default. It is written in
// the schema and read from the database; the constant here exists only so a
// database with no row still behaves.
const DefaultRetentionSeconds int64 = 7776000

// Config is the whole of opnview's runtime configuration.
type Config struct {
	// RetentionSeconds is the purge horizon. 0 means unlimited.
	RetentionSeconds int64
	// AggregateMode is `full` or `no_domains`.
	AggregateMode string

	// FirewallLogInterval and the six below are the loop cadences.
	FirewallLogInterval   time.Duration
	SecurityEventInterval time.Duration
	MeasurementInterval   time.Duration
	DHCPLeaseInterval     time.Duration
	DNSLookupInterval     time.Duration
	DiscoveryInterval     time.Duration
	PurgeInterval         time.Duration

	// FirewallLogPageSize and the two below are the page sizes.
	FirewallLogPageSize   int
	SecurityEventPageSize int
	DHCPLeasePageSize     int
}

// Defaults returns the configuration of a database that carries no setting row
// at all. It holds no firewall URL and no credential, and it never will: those
// reach opnview through the interface.
func Defaults() Config {
	return Config{
		RetentionSeconds:      DefaultRetentionSeconds,
		AggregateMode:         "full",
		FirewallLogInterval:   DefaultFirewallLogInterval,
		SecurityEventInterval: DefaultSecurityEventInterval,
		MeasurementInterval:   DefaultMeasurementInterval,
		DHCPLeaseInterval:     DefaultDHCPLeaseInterval,
		DNSLookupInterval:     DefaultDNSLookupInterval,
		DiscoveryInterval:     DefaultDiscoveryInterval,
		PurgeInterval:         DefaultPurgeInterval,
		FirewallLogPageSize:   DefaultFirewallLogPageSize,
		SecurityEventPageSize: DefaultSecurityEventPageSize,
		DHCPLeasePageSize:     DefaultDHCPLeasePageSize,
	}
}

// SettingReader is the one thing this package needs from storage. It is an
// interface so that config depends on no storage engine, and store depends on
// no configuration. *store.Store satisfies it, and so does any map a test
// builds.
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

	if value, present, err := reader.Setting(ctx, KeyRetentionSeconds); err != nil {
		return loaded, err
	} else if present {
		seconds, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return loaded, fmt.Errorf("config: %s is not an integer: %w", KeyRetentionSeconds, err)
		}
		if seconds < 0 {
			return loaded, fmt.Errorf("config: %s is negative", KeyRetentionSeconds)
		}
		loaded.RetentionSeconds = seconds
	}

	if value, present, err := reader.Setting(ctx, KeyAggregateMode); err != nil {
		return loaded, err
	} else if present {
		if value != "full" && value != "no_domains" {
			return loaded, fmt.Errorf("config: %s is %q, which is neither full nor no_domains", KeyAggregateMode, value)
		}
		loaded.AggregateMode = value
	}

	intervals := []struct {
		key    string
		target *time.Duration
	}{
		{KeyFirewallLogInterval, &loaded.FirewallLogInterval},
		{KeySecurityEventInterval, &loaded.SecurityEventInterval},
		{KeyMeasurementInterval, &loaded.MeasurementInterval},
		{KeyDHCPLeaseInterval, &loaded.DHCPLeaseInterval},
		{KeyDNSLookupInterval, &loaded.DNSLookupInterval},
		{KeyDiscoveryInterval, &loaded.DiscoveryInterval},
		{KeyPurgeInterval, &loaded.PurgeInterval},
	}
	for _, interval := range intervals {
		value, present, err := reader.Setting(ctx, interval.key)
		if err != nil {
			return loaded, err
		}
		if !present {
			continue
		}
		seconds, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return loaded, fmt.Errorf("config: %s is not an integer: %w", interval.key, err)
		}
		if seconds <= 0 {
			return loaded, fmt.Errorf("config: %s must be a positive number of seconds, got %d", interval.key, seconds)
		}
		if seconds > MaxIntervalSeconds {
			return loaded, fmt.Errorf("config: %s is %d seconds, which no duration represents (the largest is %d)",
				interval.key, seconds, MaxIntervalSeconds)
		}
		*interval.target = time.Duration(seconds) * time.Second
	}

	// The page sizes are read the same way. A page size that is not a positive
	// count is refused; one above what the firewall serves is not, see the
	// comment on the page-size keys.
	pageSizes := []struct {
		key    string
		target *int
	}{
		{KeyFirewallLogPageSize, &loaded.FirewallLogPageSize},
		{KeySecurityEventPageSize, &loaded.SecurityEventPageSize},
		{KeyDHCPLeasePageSize, &loaded.DHCPLeasePageSize},
	}
	for _, pageSize := range pageSizes {
		value, present, err := reader.Setting(ctx, pageSize.key)
		if err != nil {
			return loaded, err
		}
		if !present {
			continue
		}
		count, err := strconv.Atoi(value)
		if err != nil {
			return loaded, fmt.Errorf("config: %s is not an integer: %w", pageSize.key, err)
		}
		if count <= 0 {
			return loaded, fmt.Errorf("config: %s must be a positive number of records, got %d", pageSize.key, count)
		}
		*pageSize.target = count
	}

	return loaded, nil
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
