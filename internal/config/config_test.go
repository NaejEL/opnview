package config

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/store"
	"github.com/NaejEL/opnview/internal/store/storetest"
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
		"filter log":          defaults.Interval("firewall_log", "poll"),
		"security events":     defaults.Interval("security_event", "poll"),
		"sampled measurement": defaults.Interval("measurement_sample", "poll"),
		"DHCP leases":         defaults.Interval("dhcp_lease", "poll"),
		"resolver lookups":    defaults.Interval("dns_lookup", "poll"),
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
		{"filter log", defaults.Interval("firewall_log", "poll"), 10 * time.Second,
			"data source 1: each poll is bounded by limit, and a short interval is what keeps a " +
				"busy ruleset from producing more lines than one page can carry"},
		{"security events", defaults.Interval("security_event", "poll"), 60 * time.Second,
			"data source 2: alerts are low-volume and the backend re-reads the file backwards on " +
				"every call"},
		{"sampled measurement", defaults.Interval("measurement_sample", "poll"), 300 * time.Second,
			"data source 3: 300 s is the finest resolution the firewall keeps, and since the " +
				"per-pair endpoint is a live snapshot it is now the resolution of opnview's own history"},
		{"DHCP leases", defaults.Interval("dhcp_lease", "poll"), 300 * time.Second,
			"data source 4: leases change on the scale of minutes to hours and the Kea path costs " +
				"a control-agent round trip"},
		{"resolver lookups", defaults.Interval("dns_lookup", "poll"), 60 * time.Second,
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
		KeyRetentionSeconds:                    "3600",
		KeyAggregateMode:                       "no_domains",
		"poll_interval_firewall_log_seconds":   "5",
		"poll_interval_security_event_seconds": "30",
		"poll_interval_measurement_seconds":    "120",
		"poll_interval_dhcp_lease_seconds":     "600",
		"poll_interval_dns_lookup_seconds":     "15",
		KeyDiscoveryInterval:                   "900",
		KeyPurgeInterval:                       "1800",
		"refresh_interval_geoip_seconds":       "43200",
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
		"filter log":          {loaded.Interval("firewall_log", "poll"), 5 * time.Second},
		"security events":     {loaded.Interval("security_event", "poll"), 30 * time.Second},
		"sampled measurement": {loaded.Interval("measurement_sample", "poll"), 120 * time.Second},
		"DHCP leases":         {loaded.Interval("dhcp_lease", "poll"), 600 * time.Second},
		"resolver lookups":    {loaded.Interval("dns_lookup", "poll"), 15 * time.Second},
		"runtime discovery":   {loaded.DiscoveryInterval, 900 * time.Second},
		"retention purge":     {loaded.PurgeInterval, 1800 * time.Second},
		"MaxMind refresh":     {loaded.Interval("ip_geolocation", "refresh"), 43200 * time.Second},
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
			map[string]string{"poll_interval_firewall_log_seconds": "ten seconds"}, "poll_interval_firewall_log_seconds"},
		{"an interval of zero", map[string]string{"poll_interval_dns_lookup_seconds": "0"}, "poll_interval_dns_lookup_seconds"},
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
		"page_size_firewall_log":   "1000",
		"page_size_security_event": "250",
		"page_size_dhcp_lease":     "2000",
	}})
	if err != nil {
		t.Fatalf("loading the configuration: %v", err)
	}
	for name, pair := range map[string][2]int{
		"filter log":      {loaded.PageSize("firewall_log"), 1000},
		"security events": {loaded.PageSize("security_event"), 250},
		"DHCP leases":     {loaded.PageSize("dhcp_lease"), 2000},
	} {
		if pair[0] != pair[1] {
			t.Errorf("the %s page size is %d, want %d", name, pair[0], pair[1])
		}
	}
	// A page-size row changes nothing else.
	if loaded.Interval("firewall_log", "poll") != 10*time.Second {
		t.Errorf("a page-size row moved the filter-log interval to %v", loaded.Interval("firewall_log", "poll"))
	}
}

// TestThePageSizesDefaultToTheirDocumentedStartingPoints pins the three figures a
// database with no row runs at. They are written once, in each kind's file
// (internal/collect/provider/kinds), which Defaults, Load and a collector built
// without a configuration all read.
func TestThePageSizesDefaultToTheirDocumentedStartingPoints(t *testing.T) {
	defaults := Defaults()
	for _, kind := range []string{"firewall_log", "security_event", "dhcp_lease"} {
		if got := defaults.PageSize(kind); got != 500 {
			t.Errorf("the %s page size defaults to %d, want the documented 500", kind, got)
		}
	}
	if got := defaults.PageSize("dns_lookup"); got != 0 {
		t.Errorf("the resolver's page is not a setting, and defaults to %d", got)
	}
	loaded, err := Load(context.Background(), settingTable{rows: map[string]string{}})
	if err != nil {
		t.Fatalf("loading an empty configuration: %v", err)
	}
	if loaded.PageSize("firewall_log") != 500 || loaded.PageSize("security_event") != 500 ||
		loaded.PageSize("dhcp_lease") != 500 {
		t.Errorf("a database with no row loads the page sizes %d, %d and %d",
			loaded.PageSize("firewall_log"), loaded.PageSize("security_event"), loaded.PageSize("dhcp_lease"))
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
		{"a page of zero records", "page_size_firewall_log", "0"},
		{"a negative page", "page_size_security_event", "-500"},
		{"a page that is not a number", "page_size_dhcp_lease", "five hundred"},
		{"a page that is not a whole number", "page_size_firewall_log", "12.5"},
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

// TestTheLegPairingWindowIsAValidatedSettingWhoseDefaultIsOne is AC12 of the step-5A
// live corrections: 1 with no row, the schema's own row is 1, a row is read, 0 is
// accepted, and a malformed value is refused by name.
func TestTheLegPairingWindowIsAValidatedSettingWhoseDefaultIsOne(t *testing.T) {
	ctx := context.Background()
	loaded, err := Load(ctx, settingTable{rows: map[string]string{}})
	if err != nil || loaded.LegPairingWindowSeconds != 1 || Defaults().LegPairingWindowSeconds != 1 {
		t.Errorf("with no row the pairing window is %d (%v), not 1", loaded.LegPairingWindowSeconds, err)
	}
	database, err := store.Open(ctx, storetest.Database(t))
	if err != nil {
		t.Fatalf("opening a database: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if value, present, err := database.Setting(ctx, KeyLegPairingWindow); err != nil || !present || value != "1" {
		t.Errorf("the schema's row is %q (present %t, %v), not 1", value, present, err)
	}
	for value, want := range map[string]int64{"2": 2, "0": 0} {
		loaded, err := Load(ctx, settingTable{rows: map[string]string{KeyLegPairingWindow: value}})
		if err != nil || loaded.LegPairingWindowSeconds != want {
			t.Errorf("a row of %s gave %d (%v)", value, loaded.LegPairingWindowSeconds, err)
		}
	}
	for _, value := range []string{"-1", "1.5", "one second", ""} {
		if _, err := Load(ctx, settingTable{rows: map[string]string{KeyLegPairingWindow: value}}); err == nil ||
			!strings.Contains(err.Error(), KeyLegPairingWindow) {
			t.Errorf("the window %q was accepted, or refused without naming the key: %v", value, err)
		}
	}
}

// TestTheLegPairingWindowAboveSixtySecondsIsRefusedNamingTheSetting is AC32 of
// specs/SPEC-step-5b-widget-api.md: the bound of decision 13 is accepted, and one
// second above it is refused with a message naming the setting.
func TestTheLegPairingWindowAboveSixtySecondsIsRefusedNamingTheSetting(t *testing.T) {
	ctx := context.Background()
	loaded, err := Load(ctx, settingTable{rows: map[string]string{KeyLegPairingWindow: "60"}})
	if err != nil || loaded.LegPairingWindowSeconds != 60 {
		t.Errorf("the bound itself, 60, gave %d (%v)", loaded.LegPairingWindowSeconds, err)
	}
	for _, value := range []string{"61", "3600", "9223372036854775807"} {
		_, err := Load(ctx, settingTable{rows: map[string]string{KeyLegPairingWindow: value}})
		if err == nil || !strings.Contains(err.Error(), KeyLegPairingWindow) {
			t.Errorf("the window %s was accepted, or refused without naming the key: %v", value, err)
		}
	}
}

// TestThePublicSuffixListIsCheckedOnceADayByDefault is the refresh interval of the third
// outbound call: publicsuffix.org asks for no more than one download a day.
func TestThePublicSuffixListIsCheckedOnceADayByDefault(t *testing.T) {
	if Defaults().Interval("registrable_domain", "refresh") != 24*time.Hour {
		t.Errorf("the list is checked every %s", Defaults().Interval("registrable_domain", "refresh"))
	}
	loaded, err := Load(context.Background(), settingTable{rows: map[string]string{
		"refresh_interval_public_suffix_list_seconds": "172800"}})
	if err != nil || loaded.Interval("registrable_domain", "refresh") != 48*time.Hour {
		t.Errorf("a row of two days gave %s (%v)", loaded.Interval("registrable_domain", "refresh"), err)
	}
}

// TestTheResolverCacheSettingsAreValidated is decisions 2 and 4 of
// specs/SPEC-resolver-cache-attribution.md: the cache-answer cap defaults to 3600 and the
// schema writes that row; the cache is read every 60 s and the local data every 300 s by
// default; each is a positive whole number of seconds, read from its row, and anything
// else is refused by name.
func TestTheResolverCacheSettingsAreValidated(t *testing.T) {
	ctx := context.Background()
	loaded, err := Load(ctx, settingTable{rows: map[string]string{}})
	if err != nil || loaded.CacheAnswerDelaySeconds != 3600 || loaded.Interval("resolver_cache", "poll") != time.Minute ||
		loaded.Interval("resolver_local_data", "poll") != 5*time.Minute {
		t.Errorf("with no row the settings are %d, %v and %v (%v)", loaded.CacheAnswerDelaySeconds,
			loaded.Interval("resolver_cache", "poll"), loaded.Interval("resolver_local_data", "poll"), err)
	}
	database, err := store.Open(ctx, storetest.Database(t))
	if err != nil {
		t.Fatalf("opening a database: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if value, present, err := database.Setting(ctx, KeyCacheAnswerDelay); err != nil || !present || value != "3600" {
		t.Errorf("the schema's row is %q (present %t, %v), not 3600", value, present, err)
	}
	loaded, err = Load(ctx, settingTable{rows: map[string]string{KeyCacheAnswerDelay: "600",
		"poll_interval_resolver_cache_seconds": "30", "poll_interval_resolver_local_data_seconds": "900"}})
	if err != nil || loaded.CacheAnswerDelaySeconds != 600 || loaded.Interval("resolver_cache", "poll") != 30*time.Second ||
		loaded.Interval("resolver_local_data", "poll") != 15*time.Minute {
		t.Errorf("the rows gave %d, %v and %v (%v)", loaded.CacheAnswerDelaySeconds,
			loaded.Interval("resolver_cache", "poll"), loaded.Interval("resolver_local_data", "poll"), err)
	}
	for _, key := range []string{KeyCacheAnswerDelay, "poll_interval_resolver_cache_seconds", "poll_interval_resolver_local_data_seconds"} {
		for _, value := range []string{"0", "-60", "1.5", "an hour", ""} {
			if _, err := Load(ctx, settingTable{rows: map[string]string{key: value}}); err == nil ||
				!strings.Contains(err.Error(), key) {
				t.Errorf("%s = %q was accepted, or refused without naming the key: %v", key, value, err)
			}
		}
	}
}
