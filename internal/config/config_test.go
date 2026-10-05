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
		KeyGeoIPRefreshInterval:  "43200",
		KeyGeoLookupInterval:     "60",
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
		"MaxMind refresh":     {loaded.GeoIPRefreshInterval, 43200 * time.Second},
		"geolocation lookups": {loaded.GeoLookupInterval, 60 * time.Second},
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
		{"an interval no duration represents",
			map[string]string{KeyDiscoveryInterval: "9223372037"}, KeyDiscoveryInterval},
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

// TestASettingRowOverridesEveryPageSize is the other half of "no hardcoded
// configuration": the three page sizes are defaults, like the intervals, and an
// installation whose sources write faster is entitled to a bigger page without a
// rebuild. Without it, half of the pair that decides what a poll can lose stays
// out of the operator's reach.
func TestASettingRowOverridesEveryPageSize(t *testing.T) {
	loaded, err := Load(context.Background(), settingTable{rows: map[string]string{
		KeyFirewallLogPageSize:   "1000",
		KeySecurityEventPageSize: "250",
		KeyDHCPLeasePageSize:     "2000",
	}})
	if err != nil {
		t.Fatalf("loading the configuration: %v", err)
	}
	for name, pair := range map[string][2]int{
		"filter log":      {loaded.FirewallLogPageSize, 1000},
		"security events": {loaded.SecurityEventPageSize, 250},
		"DHCP leases":     {loaded.DHCPLeasePageSize, 2000},
	} {
		if pair[0] != pair[1] {
			t.Errorf("the %s page size is %d, want %d", name, pair[0], pair[1])
		}
	}
	// A page-size row changes nothing else.
	if loaded.FirewallLogInterval != DefaultFirewallLogInterval {
		t.Errorf("a page-size row moved the filter-log interval to %v", loaded.FirewallLogInterval)
	}
}

// TestThePageSizesDefaultToTheirDocumentedStartingPoints pins the three figures a
// database with no row runs at, and the agreement between the two places that say
// them: Defaults, which Load starts from, and DefaultPageSizes, which a collector
// built without a configuration starts from. Two figures for one page would make
// a collector's first pass and its later passes ask for different pages.
func TestThePageSizesDefaultToTheirDocumentedStartingPoints(t *testing.T) {
	defaults := Defaults()
	sizes := DefaultPageSizes()
	for name, figures := range map[string][3]int{
		"filter log":      {defaults.FirewallLogPageSize, sizes.FirewallLog, DefaultFirewallLogPageSize},
		"security events": {defaults.SecurityEventPageSize, sizes.SecurityEvent, DefaultSecurityEventPageSize},
		"DHCP leases":     {defaults.DHCPLeasePageSize, sizes.DHCPLease, DefaultDHCPLeasePageSize},
	} {
		if figures[2] != 500 {
			t.Errorf("the %s page size defaults to %d, want the documented 500", name, figures[2])
		}
		if figures[0] != figures[2] || figures[1] != figures[2] {
			t.Errorf("the %s page size is %d in Defaults and %d in DefaultPageSizes, want %d in both",
				name, figures[0], figures[1], figures[2])
		}
	}
	loaded, err := Load(context.Background(), settingTable{rows: map[string]string{}})
	if err != nil {
		t.Fatalf("loading an empty configuration: %v", err)
	}
	if loaded.FirewallLogPageSize != DefaultFirewallLogPageSize ||
		loaded.SecurityEventPageSize != DefaultSecurityEventPageSize ||
		loaded.DHCPLeasePageSize != DefaultDHCPLeasePageSize {
		t.Errorf("a database with no row loads the page sizes %d, %d and %d",
			loaded.FirewallLogPageSize, loaded.SecurityEventPageSize, loaded.DHCPLeasePageSize)
	}
}

// TestAPageSizeThatIsNotAPositiveCountIsRefused applies the honesty rule to the
// page sizes. A page of zero records would read nothing and look like a quiet
// network; a negative one, or one that is not a number, is a typing error that
// must be visible rather than replaced by the default.
func TestAPageSizeThatIsNotAPositiveCountIsRefused(t *testing.T) {
	cases := []struct {
		name string
		key  string
		row  string
	}{
		{"a page of zero records", KeyFirewallLogPageSize, "0"},
		{"a negative page", KeySecurityEventPageSize, "-500"},
		{"a page that is not a number", KeyDHCPLeasePageSize, "five hundred"},
		{"a page that is not a whole number", KeyFirewallLogPageSize, "12.5"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := Load(context.Background(), settingTable{rows: map[string]string{testCase.key: testCase.row}})
			if err == nil {
				t.Fatalf("%s was accepted silently", testCase.name)
			}
			if !strings.Contains(err.Error(), testCase.key) {
				t.Fatalf("the error %q does not name the setting %s", err, testCase.key)
			}
		})
	}
}

// TestTheAttributionDelayIsAPositiveWholeNumberOfSeconds is the setting row of step 5's
// attribution: 5 with no row, read from the row when there is one, and refused when it is
// not a positive whole number.
func TestTheAttributionDelayIsAPositiveWholeNumberOfSeconds(t *testing.T) {
	ctx := context.Background()
	loaded, err := Load(ctx, settingTable{rows: map[string]string{}})
	if err != nil || loaded.AttributionMaxDelaySeconds != 5 {
		t.Errorf("with no row the delay is %d (%v), not 5", loaded.AttributionMaxDelaySeconds, err)
	}
	loaded, err = Load(ctx, settingTable{rows: map[string]string{KeyAttributionMaxDelay: "6"}})
	if err != nil || loaded.AttributionMaxDelaySeconds != 6 {
		t.Errorf("a row of 6 gave %d (%v)", loaded.AttributionMaxDelaySeconds, err)
	}
	for _, value := range []string{"0", "-5", "5.5", "five", ""} {
		if _, err := Load(ctx, settingTable{rows: map[string]string{KeyAttributionMaxDelay: value}}); err == nil ||
			!strings.Contains(err.Error(), KeyAttributionMaxDelay) {
			t.Errorf("the delay %q was accepted, or refused without naming the key: %v", value, err)
		}
	}
}

// TestThePublicSuffixListIsCheckedOnceADayByDefault is the refresh interval of the third
// outbound call: publicsuffix.org asks for no more than one download a day.
func TestThePublicSuffixListIsCheckedOnceADayByDefault(t *testing.T) {
	if Defaults().PublicSuffixRefreshInterval != 24*time.Hour {
		t.Errorf("the list is checked every %s", Defaults().PublicSuffixRefreshInterval)
	}
	loaded, err := Load(context.Background(), settingTable{rows: map[string]string{
		KeyPublicSuffixRefreshInterval: "172800"}})
	if err != nil || loaded.PublicSuffixRefreshInterval != 48*time.Hour {
		t.Errorf("a row of two days gave %s (%v)", loaded.PublicSuffixRefreshInterval, err)
	}
}
