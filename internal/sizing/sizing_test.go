package sizing

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/store"
)

// The tests of the derivation. Rebuilt after the loss of 2 October 2026: the
// build of that evening recorded these seven test names, not their bodies, so
// the bodies below are rewritten from what the names state.

// kindNamed returns the registry entry of one kind, failing the test if there
// is none.
func kindNamed(t *testing.T, name string) Kind {
	t.Helper()
	for _, kind := range Kinds() {
		if kind.Kind == name {
			return kind
		}
	}
	t.Fatalf("the registry has no %s kind", name)
	return Kind{}
}

// measuredPeak is a measurement holding a peak of peakRecords in one bucket,
// over a covered span of a day: enough records and span for a suggestion.
func measuredPeak(peakRecords int64) store.RecordRateMeasurement {
	return store.RecordRateMeasurement{
		BucketSeconds:     BucketSeconds,
		RecordCount:       peakRecords + 100,
		CoveredFromAt:     1750000000,
		CoveredToAt:       1750000000 + NominalWindowSeconds - 1,
		HasRecords:        true,
		PeakBucketRecords: peakRecords,
		PeakRate:          store.Rate{PerSecond: float64(peakRecords) / float64(BucketSeconds), Known: true},
	}
}

// repositoryFile reads a file of the repository, from this package's
// directory.
func repositoryFile(t *testing.T, relative string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(relative)))
	if err != nil {
		t.Fatalf("reading %s: %v", relative, err)
	}
	return string(content)
}

// TestADegenerateMeasurementIsNeverRenderedAsAPair keeps "nothing to go on" from
// reaching the operator as a pair. Every state below is shown as itself on the
// collection surface; a pair of zeros, or a pair derived from one record, would
// read as a suggestion and be applied as one.
func TestADegenerateMeasurementIsNeverRenderedAsAPair(t *testing.T) {
	inForce := Pair{PageSize: config.DefaultFirewallLogPageSize, Interval: config.DefaultFirewallLogInterval}
	single := measuredPeak(1)
	single.RecordCount, single.CoveredToAt = 1, single.CoveredFromAt
	short := measuredPeak(5)
	short.CoveredToAt = short.CoveredFromAt + BucketSeconds - 1
	unknownPeak := measuredPeak(5)
	unknownPeak.PeakRate.Known = false

	for _, kind := range Kinds() {
		cases := []struct {
			name      string
			measured  store.RecordRateMeasurement
			collected bool
			want      Outcome
		}{
			{"an empty window", store.RecordRateMeasurement{BucketSeconds: BucketSeconds}, true, OutcomeNotYetMeasured},
			{"one record", single, true, OutcomeNotYetMeasured},
			{"a span shorter than one bucket", short, true, OutcomeNotYetMeasured},
			{"a peak that was not measured", unknownPeak, true, OutcomeNotYetMeasured},
			{"no bucket at all", store.RecordRateMeasurement{}, true, OutcomeNotYetMeasured},
			{"a source nobody collects", measuredPeak(5), false, OutcomeSourceNotCollected},
		}
		for _, testCase := range cases {
			t.Run(kind.Kind+"/"+testCase.name, func(t *testing.T) {
				suggestion := Suggest(kind, testCase.measured, inForce, testCase.collected)
				if suggestion.Outcome != testCase.want {
					t.Fatalf("the outcome is %q, want %q", suggestion.Outcome, testCase.want)
				}
				if suggestion.Pair != (Pair{}) || suggestion.RequiredRecordsPerPage != 0 {
					t.Fatalf("%s carries a pair: %+v", testCase.name, suggestion)
				}
				if suggestion.IntervalOnly != !kind.PageIsAdjustable() {
					t.Fatalf("the outcome does not say whether the page is a setting: %+v", suggestion)
				}
			})
		}
	}
}

// TestEveryCeilingCitesASectionOfTheSurveyThatExists holds every ceiling to its
// source. A ceiling is a property of the API, and the only place this project
// records properties of the API is the survey: a heading that is not in it is a
// figure with nothing behind it.
func TestEveryCeilingCitesASectionOfTheSurveyThatExists(t *testing.T) {
	headings := map[string]bool{}
	scanner := bufio.NewScanner(strings.NewReader(repositoryFile(t, "docs/opnsense-api-survey.md")))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "#") {
			headings[strings.TrimSpace(strings.TrimLeft(line, "#"))] = true
		}
	}
	for _, kind := range Kinds() {
		if kind.SurveyHeading == "" {
			t.Errorf("%s cites no survey section", kind.Kind)
			continue
		}
		if !headings[kind.SurveyHeading] {
			t.Errorf("%s cites %q, which is not a heading of the survey", kind.Kind, kind.SurveyHeading)
		}
	}
	// The figures themselves, so a ceiling changed in code without its
	// citation being reread fails here.
	for name, want := range map[string]int{
		"firewall_log": 1000, "security_event": 9999, "dns_lookup": 1000, "dhcp_lease": PageCeilingUnbounded,
	} {
		if got := kindNamed(t, name).PageCeiling; got != want {
			t.Errorf("the %s ceiling is %d, want %d", name, got, want)
		}
	}
}

// TestNoSuggestionEverExceedsTheCeilingAtAnyMeasuredPeak drives the derivation
// over a range of peaks, for every kind and several pairs in force, and checks
// every pair it suggests against the rules the derivation exists to respect. A
// pair above the ceiling would be a pair the firewall silently answers with
// fewer records than asked for.
func TestNoSuggestionEverExceedsTheCeilingAtAnyMeasuredPeak(t *testing.T) {
	intervals := []time.Duration{time.Second, 10 * time.Second, 60 * time.Second, 300 * time.Second, time.Hour}
	for _, kind := range Kinds() {
		for _, interval := range intervals {
			inForce := Pair{PageSize: 500, Interval: interval}
			for peak := int64(1); peak <= 20000; peak = peak*11/10 + 1 {
				suggestion := Suggest(kind, measuredPeak(peak), inForce, true)
				if suggestion.Outcome != OutcomeSuggested {
					if suggestion.Pair != (Pair{}) {
						t.Fatalf("%s, peak %d: a %q outcome carries a pair", kind.Kind, peak, suggestion.Outcome)
					}
					continue
				}
				pair := suggestion.Pair
				if pair.Interval < ShortestSuggestibleInterval {
					t.Fatalf("%s, peak %d: the interval %v is below the floor", kind.Kind, peak, pair.Interval)
				}
				ceiling := max(interval, ShortestSuggestibleInterval)
				if pair.Interval > ceiling {
					t.Fatalf("%s, peak %d: the interval grew from %v to %v", kind.Kind, peak, interval, pair.Interval)
				}
				page := pair.PageSize
				if !kind.PageIsAdjustable() {
					if page != 0 || !suggestion.IntervalOnly {
						t.Fatalf("%s, peak %d: a page was suggested for a read without one: %+v", kind.Kind, peak, suggestion)
					}
					page = kind.PageCeiling
				}
				if kind.HasPageCeiling() && page > kind.PageCeiling {
					t.Fatalf("%s, peak %d: the page %d exceeds the ceiling %d", kind.Kind, peak, page, kind.PageCeiling)
				}
				required, ok := requiredRecordsPerPage(peak, BucketSeconds, pair.Interval)
				if !ok || required > page || required != suggestion.RequiredRecordsPerPage {
					t.Fatalf("%s, peak %d: %d records per page are required at %v, the page holds %d (reported %d)",
						kind.Kind, peak, required, pair.Interval, page, suggestion.RequiredRecordsPerPage)
				}
			}
		}
	}
}

// TestTheDerivationMovesThePageBeforeTheIntervalAndNeverPastTheCeiling pins the
// order of the derivation on figures that can be checked by hand. With a peak of
// p records in a 60-second bucket, a page at an interval of i seconds must hold
// ceil(p/60 × i × 4) records.
func TestTheDerivationMovesThePageBeforeTheIntervalAndNeverPastTheCeiling(t *testing.T) {
	filterLog := kindNamed(t, "firewall_log")
	inForce := Pair{PageSize: 500, Interval: 60 * time.Second}

	// 100 records at the peak need 400 per page at 60 s: within the ceiling, so
	// the interval stands and only the page moves.
	got := Suggest(filterLog, measuredPeak(100), inForce, true)
	if want := (Pair{PageSize: 400, Interval: 60 * time.Second}); got.Outcome != OutcomeSuggested || got.Pair != want {
		t.Errorf("a peak of 100 suggested %+v, want %+v", got, want)
	}

	// 500 at the peak need 2000 per page at 60 s, above the ceiling of 1000: the
	// page stops at the ceiling and the interval shortens to 30 s, where the
	// requirement is exactly 1000.
	got = Suggest(filterLog, measuredPeak(500), inForce, true)
	if want := (Pair{PageSize: 1000, Interval: 30 * time.Second}); got.Outcome != OutcomeSuggested || got.Pair != want {
		t.Errorf("a peak of 500 suggested %+v, want %+v", got, want)
	}

	// 2000 at the peak would need an interval of 7 s at the ceiling, below the
	// floor: no pair keeps up, and that is said rather than suggested.
	got = Suggest(filterLog, measuredPeak(2000), inForce, true)
	if got.Outcome != OutcomeRateNotCoverable || got.Pair != (Pair{}) {
		t.Errorf("a peak of 2000 gave %+v, want a rate that cannot be covered", got)
	}

	// The lease read has no ceiling: the page grows with the peak and the
	// interval never moves.
	leases := kindNamed(t, "dhcp_lease")
	got = Suggest(leases, measuredPeak(5000), Pair{PageSize: 500, Interval: 300 * time.Second}, true)
	if want := (Pair{PageSize: 100000, Interval: 300 * time.Second}); got.Pair != want {
		t.Errorf("an uncapped read suggested %+v, want %+v", got, want)
	}

	// The resolver's page is its ring buffer: only the interval is suggested.
	resolver := kindNamed(t, "dns_lookup")
	got = Suggest(resolver, measuredPeak(100), inForce, true)
	if want := (Pair{Interval: 60 * time.Second}); !got.IntervalOnly || got.Pair != want {
		t.Errorf("the resolver at a peak of 100 gave %+v, want the interval in force alone", got)
	}
	got = Suggest(resolver, measuredPeak(500), inForce, true)
	if want := (Pair{Interval: 30 * time.Second}); !got.IntervalOnly || got.Pair != want {
		t.Errorf("the resolver at a peak of 500 gave %+v, want %+v", got, want)
	}

	// An interval in force below the floor is raised to it before anything else.
	got = Suggest(filterLog, measuredPeak(60), Pair{PageSize: 500, Interval: time.Second}, true)
	if want := (Pair{PageSize: 40, Interval: ShortestSuggestibleInterval}); got.Pair != want {
		t.Errorf("an interval of 1 s in force gave %+v, want %+v", got, want)
	}
}

// TestTheMeasurementSQLIsTheDiagnosticInTheRepository keeps the figure on the
// screen reproducible by hand. The diagnostic named RecordRateDiagnosticName in
// sql/queries/diagnostics.sql must be, character for character, the
// measurement of every kind of the registry, in registry order, joined by UNION
// ALL, at the bucket width of this package.
func TestTheMeasurementSQLIsTheDiagnosticInTheRepository(t *testing.T) {
	branches := make([]string, 0, len(Kinds()))
	for _, kind := range Kinds() {
		branches = append(branches, store.RecordRateBranch(kind.Spec, BucketSeconds))
	}
	want := strings.Join(branches, "\nUNION ALL\n") + ";"

	file := repositoryFile(t, "sql/queries/diagnostics.sql")
	marker := "-- diagnostic: " + store.RecordRateDiagnosticName + "\n"
	start := strings.Index(file, marker)
	if start < 0 {
		t.Fatalf("sql/queries/diagnostics.sql has no %q diagnostic; it should read:\n%s",
			store.RecordRateDiagnosticName, want)
	}
	// The statement begins after the diagnostic's comment lines and ends at
	// the first semicolon at the end of a line.
	var statement []string
	for _, line := range strings.Split(file[start+len(marker):], "\n") {
		if len(statement) == 0 && strings.HasPrefix(line, "--") {
			continue
		}
		statement = append(statement, line)
		if strings.HasSuffix(line, ";") {
			break
		}
	}
	if got := strings.Join(statement, "\n"); got != want {
		t.Fatalf("the %q diagnostic is not the measurement; it should read:\n%s",
			store.RecordRateDiagnosticName, want)
	}
}

// TestTheRegistryAgreesWithTheSchema holds the registry to the database it is
// spliced into. Every kind must be a kind of the provider registry, every
// table and column must exist — measured on a fresh database, which fails on a
// name that does not — and every setting key must be the one package config
// reads.
func TestTheRegistryAgreesWithTheSchema(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatalf("opening a database: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	registered, err := database.KindsInProviderTable(ctx)
	if err != nil {
		t.Fatalf("listing the registered kinds: %v", err)
	}
	keys := map[string][2]string{
		"firewall_log":   {config.KeyFirewallLogPageSize, config.KeyFirewallLogInterval},
		"security_event": {config.KeySecurityEventPageSize, config.KeySecurityEventInterval},
		"dhcp_lease":     {config.KeyDHCPLeasePageSize, config.KeyDHCPLeaseInterval},
		"dns_lookup":     {"", config.KeyDNSLookupInterval},
	}
	seen := map[string]bool{}
	for _, kind := range Kinds() {
		if seen[kind.Kind] {
			t.Errorf("%s is registered twice", kind.Kind)
		}
		seen[kind.Kind] = true
		if _, ok := registered[kind.Kind]; !ok {
			t.Errorf("%s is not a kind of the provider registry", kind.Kind)
		}
		if kind.Spec.Kind != kind.Kind {
			t.Errorf("%s measures the records of %s", kind.Kind, kind.Spec.Kind)
		}
		want, ok := keys[kind.Kind]
		if !ok {
			t.Errorf("%s has no expected setting keys in this test", kind.Kind)
		} else if kind.PageSizeKey != want[0] || kind.IntervalKey != want[1] {
			t.Errorf("%s uses the keys %q and %q, want %q and %q", kind.Kind,
				kind.PageSizeKey, kind.IntervalKey, want[0], want[1])
		}
		window := Window(time.Unix(1750000000, 0), config.DefaultRetentionSeconds)
		if _, err := database.MeasureRecordRate(ctx, kind.Spec, window, BucketSeconds); err != nil {
			t.Errorf("measuring %s on the schema: %v", kind.Kind, err)
		}
	}
	if len(seen) != len(keys) {
		t.Errorf("the registry holds %d kinds, this test expects %d", len(seen), len(keys))
	}
}

// TestTheWindowIsTwentyFourHoursBoundedByRetention pins the window: a day up to
// now, shortened to the retention horizon when that is shorter, and a day again
// when retention is unlimited.
func TestTheWindowIsTwentyFourHoursBoundedByRetention(t *testing.T) {
	now := time.Unix(1750086400, 0)
	cases := []struct {
		name      string
		retention int64
		want      int64
	}{
		{"the documented 90 days", config.DefaultRetentionSeconds, NominalWindowSeconds},
		{"an unlimited horizon", 0, NominalWindowSeconds},
		{"a horizon of one hour", 3600, 3600},
		{"a horizon of exactly a day", NominalWindowSeconds, NominalWindowSeconds},
		{"a horizon of a day and a second", NominalWindowSeconds + 1, NominalWindowSeconds},
	}
	for _, testCase := range cases {
		window := Window(now, testCase.retention)
		if window.EndAt != now.Unix() {
			t.Errorf("%s: the window ends at %d, want now", testCase.name, window.EndAt)
		}
		if window.Seconds() != testCase.want {
			t.Errorf("%s: the window is %d s long, want %d", testCase.name, window.Seconds(), testCase.want)
		}
	}
	if NominalWindowSeconds != 24*60*60 {
		t.Errorf("the nominal window is %d s, want 24 hours", NominalWindowSeconds)
	}
}
