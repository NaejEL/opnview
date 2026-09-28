// Package config is the only reader of configuration in opnview.
//
// There are exactly two sources, and no third: the `setting` table, and the
// defaults written down here. There is no environment variable, no
// configuration file and no `configure` subcommand — the firewall URL, the API
// key and secret and the MaxMind key are entered in the interface, which is
// cycle 4B, and this cycle stores none of them.
//
// Every default here is a duration or a mode. NO INTERFACE NAME, VLAN NAME,
// ADDRESS, CIDR OR COUNT IS A DEFAULT, because every one of those is discovered
// at runtime through the API.
package config

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"
)

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

// DefaultRetentionSeconds is 90 days, the documented default. It is written in
// the schema and read from the database; the constant here exists only so a
// database with no row still behaves.
const DefaultRetentionSeconds int64 = 7776000

// Config is the whole of opnview's runtime configuration in this cycle.
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
	}
}

// SettingReader is the one thing this package needs from storage. It is an
// interface so that config depends on no storage engine, and store depends on
// no configuration.
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
		*interval.target = time.Duration(seconds) * time.Second
	}

	return loaded, nil
}

// ErrNoDataDir is returned when --data-dir was not given. There is deliberately
// no default: a default would pre-empt the directory layout step 8 chooses for
// the LXC, and a program that silently writes a database somewhere plausible is
// worse than one that refuses to start.
var ErrNoDataDir = errors.New("config: --data-dir is required and has no default")
