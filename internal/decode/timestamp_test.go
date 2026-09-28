package decode

import (
	"os"
	"testing"
	"time"
)

// The timestamp tests.
//
// They are table-driven over the three shapes docs/data-model.md names, and the case
// that matters most is the December-to-January boundary: the filter log's year-less
// form carries no year, so a line written on 31 December and read on 1 January must
// not be stamped eleven months in the future.
//
// THEY RUN WITH TZ SET TO A NON-UTC ZONE. Nothing in normalisation may read the host's
// zone — the host is a container on somebody's laptop and the firewall is somewhere
// else — and a test that ran only in UTC would pass whether or not that were true.

// TestMain sets a non-UTC zone for every test in this package.
//
// A zone with a large offset and a southern-hemisphere summer-time rule is chosen
// deliberately: it is wrong in both directions at different times of year, so a
// normalisation that accidentally read it would land hours out rather than by a
// suspiciously round amount.
func TestMain(m *testing.M) {
	if err := os.Setenv("TZ", "Australia/Sydney"); err != nil {
		panic("decode_test: setting TZ: " + err.Error())
	}
	// Go reads TZ when it first needs the local zone, so the location is loaded here
	// to make the setting take effect for every test below.
	time.Local = mustLoadLocation("Australia/Sydney")
	os.Exit(m.Run())
}

// mustLoadLocation loads a zone or stops the test binary. A missing zone database would
// make every assertion below vacuous, which is worse than a failure.
func mustLoadLocation(name string) *time.Location {
	location, err := time.LoadLocation(name)
	if err != nil {
		panic("decode_test: the zone database does not carry " + name + ": " + err.Error())
	}
	return location
}

// TestTimestampNormalisationCoversTheThreeShapes is the table.
func TestTimestampNormalisationCoversTheThreeShapes(t *testing.T) {
	// The reference is the firewall's current time, read in the same API session, which
	// is what survey gap 7 prescribes for the shape that carries no year.
	reference := time.Date(2027, time.January, 1, 0, 30, 0, 0, time.UTC)
	normaliser := Normaliser{Reference: reference}

	cases := []struct {
		name  string
		shape string
		input any
		want  int64
		why   string
	}{
		{
			name: "epoch seconds as a JSON number", shape: "epoch",
			input: float64(1750000000), want: 1750000000,
			why: "the resolver time, the lease expiry and the aggregate last_seen are already epochs",
		},
		{
			name: "epoch seconds as a string", shape: "epoch",
			input: "1750000000", want: 1750000000,
			why: "a form-encoded body makes every value a string, so the same field arrives both ways",
		},
		{
			name: "a fractional epoch", shape: "epoch",
			input: "1750000000.75", want: 1750000000,
			why: "the schema stores seconds, so a fraction is truncated rather than refused",
		},
		{
			name: "the ISO form measured on a live firewall", shape: "iso",
			input: "2026-09-26T23:51:06",
			want:  time.Date(2026, time.September, 26, 23, 51, 6, 0, time.UTC).Unix(),
			why:   "the filter log returned exactly this, with the UTC offset stripped",
		},
		{
			name: "a full RFC 3339 timestamp", shape: "iso",
			input: "2026-09-26T23:50:00+00:00",
			want:  time.Date(2026, time.September, 26, 23, 50, 0, 0, time.UTC).Unix(),
			why:   "Suricata writes the zone, so there is nothing to assume",
		},
		{
			name: "an RFC 3339 timestamp in another zone", shape: "iso",
			input: "2026-09-27T09:50:00+10:00",
			want:  time.Date(2026, time.September, 26, 23, 50, 0, 0, time.UTC).Unix(),
			why:   "a timestamp that names its own offset is read as it stands, wherever the host is",
		},
		{
			name: "the year-less form, inside the reference year", shape: "syslog",
			input: "Jan  1 00:29:00",
			want:  time.Date(2027, time.January, 1, 0, 29, 0, 0, time.UTC).Unix(),
			why:   "the obvious case: the same year as the reference",
		},
		{
			name: "the year-less form across the December-to-January boundary", shape: "syslog",
			input: "Dec 31 23:59:00",
			want:  time.Date(2026, time.December, 31, 23, 59, 0, 0, time.UTC).Unix(),
			why: "a line written a minute before midnight and read half an hour after it must " +
				"be stamped in the year that just ended, not eleven months in the future",
		},
		{
			name: "the year-less form a little ahead of the reference", shape: "syslog",
			input: "Jan  1 00:31:00",
			want:  time.Date(2027, time.January, 1, 0, 31, 0, 0, time.UTC).Unix(),
			why: "a firewall whose clock is slightly ahead writes a line opnview has not " +
				"reached yet, and the nearest candidate year is still the reference one",
		},
		{
			name: "a single-digit day, which syslog pads with a space", shape: "syslog",
			input: "Jan  5 12:00:00",
			want:  time.Date(2027, time.January, 5, 12, 0, 0, 0, time.UTC).Unix(),
			why:   "the filterlog format space-pads the day",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			var (
				got int64
				err error
			)
			switch testCase.shape {
			case "epoch":
				got, err = NormaliseEpoch(testCase.input)
			case "iso":
				got, err = normaliser.NormaliseISO(testCase.input.(string))
			case "syslog":
				got, err = normaliser.NormaliseSyslog(testCase.input.(string))
			default:
				t.Fatalf("the case names the shape %q, which this table does not handle",
					testCase.shape)
			}
			if err != nil {
				t.Fatalf("normalising %v: %v", testCase.input, err)
			}
			if got != testCase.want {
				t.Fatalf("normalising %v gave %d, want %d (%s)",
					testCase.input, got, testCase.want, testCase.why)
			}
		})
	}
}

// TestTheFilterLogTimestampAcceptsBothOfItsShapes is the one field that is either form
// depending on the syslog format in use. Only the ISO one was exercised on a live
// firewall, so both are handled and neither is assumed.
func TestTheFilterLogTimestampAcceptsBothOfItsShapes(t *testing.T) {
	reference := time.Date(2026, time.September, 27, 0, 0, 0, 0, time.UTC)
	normaliser := Normaliser{Reference: reference}

	iso, err := normaliser.NormaliseFilterLogTimestamp("2026-09-26T23:51:06")
	if err != nil {
		t.Fatalf("the ISO shape was refused: %v", err)
	}
	syslog, err := normaliser.NormaliseFilterLogTimestamp("Sep 26 23:51:06")
	if err != nil {
		t.Fatalf("the year-less shape was refused: %v", err)
	}
	if iso != syslog {
		t.Fatalf("the same instant normalised to %d from the ISO shape and %d from the "+
			"year-less one", iso, syslog)
	}
}

// TestTheHostZoneNeverReachesANormalisedInstant is the assertion the non-UTC TZ exists
// for. It compares a normalisation made under the package-wide zone with the same
// normalisation made after switching the zone again: the answer cannot move.
func TestTheHostZoneNeverReachesANormalisedInstant(t *testing.T) {
	reference := time.Date(2027, time.January, 1, 0, 30, 0, 0, time.UTC)
	normaliser := Normaliser{Reference: reference}

	first, err := normaliser.NormaliseSyslog("Dec 31 23:59:00")
	if err != nil {
		t.Fatalf("normalising under the first zone: %v", err)
	}

	previous := time.Local
	time.Local = mustLoadLocation("America/Los_Angeles")
	defer func() { time.Local = previous }()

	second, err := normaliser.NormaliseSyslog("Dec 31 23:59:00")
	if err != nil {
		t.Fatalf("normalising under the second zone: %v", err)
	}
	if first != second {
		t.Fatalf("the host's zone changed a normalised instant from %d to %d", first, second)
	}
}

// TestTheFirewallOffsetIsTheRecordedAssumptionAndIsApplied is survey gap 7's residue.
//
// The year-less form carries no zone either, and neither does the stripped ISO form, so
// something has to be assumed about the firewall's offset from UTC. It is a field rather
// than a constant, it defaults to zero, and confirming it against a live firewall is on
// the list for the end of cycle 4B. This test proves it is actually applied, so the
// assumption is correctable rather than baked in.
func TestTheFirewallOffsetIsTheRecordedAssumptionAndIsApplied(t *testing.T) {
	reference := time.Date(2026, time.September, 27, 0, 0, 0, 0, time.UTC)
	atUTC := Normaliser{Reference: reference}
	tenHoursAhead := Normaliser{Reference: reference, OffsetSeconds: 10 * 3600}

	base, err := atUTC.NormaliseISO("2026-09-26T23:51:06")
	if err != nil {
		t.Fatalf("normalising at UTC: %v", err)
	}
	shifted, err := tenHoursAhead.NormaliseISO("2026-09-26T23:51:06")
	if err != nil {
		t.Fatalf("normalising with an offset: %v", err)
	}
	if base-shifted != 10*3600 {
		t.Fatalf("a ten-hour offset moved the instant by %d seconds, want %d",
			base-shifted, 10*3600)
	}
}

// TestAnUnreadableTimestampIsAnErrorRatherThanAZero keeps a shape nobody anticipated
// from being ingested as the epoch.
func TestAnUnreadableTimestampIsAnErrorRatherThanAZero(t *testing.T) {
	normaliser := Normaliser{Reference: time.Date(2026, time.September, 27, 0, 0, 0, 0, time.UTC)}

	for _, input := range []string{"", "not a timestamp", "26/09/2026 23:51", "Foo 31 00:00:00"} {
		if _, err := normaliser.NormaliseFilterLogTimestamp(input); err == nil {
			t.Errorf("%q was accepted as a filter-log timestamp", input)
		}
	}
	for _, input := range []any{nil, "", "later", struct{}{}} {
		if _, err := NormaliseEpoch(input); err == nil {
			t.Errorf("%v was accepted as an epoch", input)
		}
	}
	// A year-less timestamp with no reference cannot be dated at all, and guessing the
	// current year from the host clock is exactly what this refuses.
	if _, err := (Normaliser{}).NormaliseSyslog("Dec 31 23:59:00"); err == nil {
		t.Error("a year-less timestamp was dated with no reference time")
	}
}
