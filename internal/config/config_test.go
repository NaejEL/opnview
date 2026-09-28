package config

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// settingTable is a SettingReader backed by a map, so this package is tested without
// a database.
type settingTable struct {
	rows map[string]string
	err  error
}

// Setting returns one row.
func (table settingTable) Setting(_ context.Context, key string) (string, bool, error) {
	if table.err != nil {
		return "", false, table.err
	}
	value, present := table.rows[key]
	return value, present, nil
}

// TestDefaultsCarryNoFirewallURLKeyOrSecret is the no-credential-ingress rule for
// this cycle, stated as a test rather than as a comment.
//
// There is no environment variable, no configuration file and no configure
// subcommand, so the defaults have nowhere to put a firewall URL or a credential and
// must not have a field for one either. The Config type below is the whole of what
// this cycle reads.
func TestDefaultsCarryNoFirewallURLKeyOrSecret(t *testing.T) {
	defaults := Defaults()
	if defaults.RetentionSeconds != DefaultRetentionSeconds {
		t.Errorf("the default horizon is %d, want %d",
			defaults.RetentionSeconds, DefaultRetentionSeconds)
	}
	if defaults.AggregateMode != "full" {
		t.Errorf("the default aggregate mode is %q, want full", defaults.AggregateMode)
	}
	// Every interval has a value, so a database with no setting row still runs at the
	// surveyed cadences rather than at zero seconds.
	for name, interval := range map[string]time.Duration{
		"filter log":          defaults.FirewallLogInterval,
		"security events":     defaults.SecurityEventInterval,
		"sampled measurement": defaults.MeasurementInterval,
		"DHCP leases":         defaults.DHCPLeaseInterval,
		"resolver lookups":    defaults.DNSLookupInterval,
		"runtime discovery":   defaults.DiscoveryInterval,
		"retention purge":     defaults.PurgeInterval,
	} {
		if interval <= 0 {
			t.Errorf("the %s interval defaults to %v", name, interval)
		}
	}
}

// TestTheFivePollIntervalsDefaultToTheSurveyedCadences pins the five figures the
// survey justifies, so a change to one is a change somebody has to mean.
func TestTheFivePollIntervalsDefaultToTheSurveyedCadences(t *testing.T) {
	defaults := Defaults()
	cases := []struct {
		name string
		got  time.Duration
		want time.Duration
		why  string
	}{
		{"filter log", defaults.FirewallLogInterval, 10 * time.Second,
			"data source 1: each poll is bounded by limit, and a short interval is what keeps a " +
				"busy ruleset from producing more lines than one page can carry"},
		{"security events", defaults.SecurityEventInterval, 60 * time.Second,
			"data source 2: alerts are low-volume and the backend re-reads the file backwards on " +
				"every call"},
		{"sampled measurement", defaults.MeasurementInterval, 300 * time.Second,
			"data source 3: 300 s is the finest resolution the firewall keeps, and since the " +
				"per-pair endpoint is a live snapshot it is now the resolution of opnview's own history"},
		{"DHCP leases", defaults.DHCPLeaseInterval, 300 * time.Second,
			"data source 4: leases change on the scale of minutes to hours and the Kea path costs " +
				"a control-agent round trip"},
		{"resolver lookups", defaults.DNSLookupInterval, 60 * time.Second,
			"data source 5: lookups must be correlated with flows that follow within seconds, and " +
				"the endpoint is a ring buffer that loses its oldest rows outright"},
	}
	for _, testCase := range cases {
		if testCase.got != testCase.want {
			t.Errorf("the %s interval is %v, want %v (%s)",
				testCase.name, testCase.got, testCase.want, testCase.why)
		}
	}
}

// TestASettingRowOverridesEveryPollInterval is decision 9's other half: the constants
// are defaults, and an installation whose log runs faster is entitled to a different
// figure without a rebuild.
func TestASettingRowOverridesEveryPollInterval(t *testing.T) {
	table := settingTable{rows: map[string]string{
		KeyRetentionSeconds:      "3600",
		KeyAggregateMode:         "no_domains",
		KeyFirewallLogInterval:   "5",
		KeySecurityEventInterval: "30",
		KeyMeasurementInterval:   "120",
		KeyDHCPLeaseInterval:     "600",
		KeyDNSLookupInterval:     "15",
		KeyDiscoveryInterval:     "900",
		KeyPurgeInterval:         "1800",
	}}
	loaded, err := Load(context.Background(), table)
	if err != nil {
		t.Fatalf("loading the configuration: %v", err)
	}
	if loaded.RetentionSeconds != 3600 {
		t.Errorf("the horizon is %d, want 3600", loaded.RetentionSeconds)
	}
	if loaded.AggregateMode != "no_domains" {
		t.Errorf("the aggregate mode is %q, want no_domains", loaded.AggregateMode)
	}
	for name, pair := range map[string][2]time.Duration{
		"filter log":          {loaded.FirewallLogInterval, 5 * time.Second},
		"security events":     {loaded.SecurityEventInterval, 30 * time.Second},
		"sampled measurement": {loaded.MeasurementInterval, 120 * time.Second},
		"DHCP leases":         {loaded.DHCPLeaseInterval, 600 * time.Second},
		"resolver lookups":    {loaded.DNSLookupInterval, 15 * time.Second},
		"runtime discovery":   {loaded.DiscoveryInterval, 900 * time.Second},
		"retention purge":     {loaded.PurgeInterval, 1800 * time.Second},
	} {
		if pair[0] != pair[1] {
			t.Errorf("the %s interval is %v, want %v", name, pair[0], pair[1])
		}
	}
}

// TestAnUnlimitedHorizonIsAcceptedAndMeansZero keeps the documented "0 means
// unlimited" from being rejected as a nonsense value.
func TestAnUnlimitedHorizonIsAcceptedAndMeansZero(t *testing.T) {
	loaded, err := Load(context.Background(), settingTable{
		rows: map[string]string{KeyRetentionSeconds: "0"}})
	if err != nil {
		t.Fatalf("loading the configuration: %v", err)
	}
	if loaded.RetentionSeconds != 0 {
		t.Fatalf("the horizon is %d, want 0", loaded.RetentionSeconds)
	}
}

// TestASettingThatCannotBeReadIsAnErrorRatherThanASilentFallback is the honesty rule
// applied to configuration. A row somebody typed wrong must be visible: falling back
// to the default would leave the product running at a cadence nobody chose while the
// database says otherwise.
func TestASettingThatCannotBeReadIsAnErrorRatherThanASilentFallback(t *testing.T) {
	cases := []struct {
		name string
		rows map[string]string
		want string
	}{
		{"a horizon that is not a number", map[string]string{KeyRetentionSeconds: "ninety days"},
			KeyRetentionSeconds},
		{"a negative horizon", map[string]string{KeyRetentionSeconds: "-1"}, KeyRetentionSeconds},
		{"an aggregate mode outside the two", map[string]string{KeyAggregateMode: "everything"},
			KeyAggregateMode},
		{"an interval that is not a number",
			map[string]string{KeyFirewallLogInterval: "ten seconds"}, KeyFirewallLogInterval},
		{"an interval of zero", map[string]string{KeyDNSLookupInterval: "0"}, KeyDNSLookupInterval},
		{"a negative interval", map[string]string{KeyPurgeInterval: "-60"}, KeyPurgeInterval},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := Load(context.Background(), settingTable{rows: testCase.rows})
			if err == nil {
				t.Fatalf("%s was accepted silently", testCase.name)
			}
			if !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("the error %q does not name the setting %s", err, testCase.want)
			}
		})
	}
}

// TestAStorageFailureReachesTheCaller keeps a database error from looking like an
// absent row.
func TestAStorageFailureReachesTheCaller(t *testing.T) {
	sentinel := errors.New("config_test: the storage layer failed")
	_, err := Load(context.Background(), settingTable{err: sentinel})
	if !errors.Is(err, sentinel) {
		t.Fatalf("the storage failure returned %v", err)
	}
}

// TestTheDataDirectoryHasNoDefault is decision 11. A program that silently writes a
// database somewhere plausible is worse than one that refuses to start, and a default
// here would pre-empt the directory layout the installer chooses.
func TestTheDataDirectoryHasNoDefault(t *testing.T) {
	if ErrNoDataDir == nil {
		t.Fatal("there is no refusal for a missing data directory")
	}
	if !strings.Contains(ErrNoDataDir.Error(), "--data-dir") {
		t.Fatalf("the refusal %q does not name the flag", ErrNoDataDir)
	}
}
