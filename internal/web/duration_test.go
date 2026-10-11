package web

import (
	"math"
	"strconv"
	"strings"
	"testing"
)

// Durations in days, hours, minutes and seconds.
//
// PROVENANCE: SYNTHESISED. Every figure is a length of time chosen here at a unit's
// boundary.

// durationText is the markup a duration is drawn as, built from the catalogue: what a
// test compares a rendered page against.
func durationText(t *testing.T, seconds int64) string {
	t.Helper()
	var parts []string
	for _, part := range spelledDuration(seconds) {
		parts = append(parts, `<span class="duration-part"><span class="figure">`+part.Figure+
			"</span> "+catalogueText(t, part.UnitKey)+"</span>")
	}
	return strings.Join(parts, " ")
}

// TestADurationIsSpelledExactly: every unit it holds, longest first, nothing rounded,
// one and many told apart.
func TestADurationIsSpelledExactly(t *testing.T) {
	t.Parallel()
	for seconds, want := range map[int64]string{
		0:       "0 seconds",
		1:       "1 second",
		59:      "59 seconds",
		60:      "1 minute",
		90:      "1 minute 30 seconds",
		3600:    "1 hour",
		86400:   "1 day",
		90061:   "1 day 1 hour 1 minute 1 second",
		7776000: "90 days",
		172920:  "2 days 2 minutes",
	} {
		var words []string
		for _, part := range spelledDuration(seconds) {
			words = append(words, part.Figure, catalogueText(t, part.UnitKey))
		}
		if got := strings.Join(words, " "); got != want {
			t.Errorf("%d seconds is spelled %q, want %q", seconds, got, want)
		}
	}
}

// TestADurationFieldHoldsTheValueInItsLongestExactUnit: the field never rounds.
func TestADurationFieldHoldsTheValueInItsLongestExactUnit(t *testing.T) {
	t.Parallel()
	for seconds, want := range map[int64][2]string{
		0:       {"0", unitSeconds},
		5:       {"5", unitSeconds},
		90:      {"90", unitSeconds},
		300:     {"5", unitMinutes},
		3600:    {"1", unitHours},
		5400:    {"90", unitMinutes},
		86400:   {"1", unitDays},
		7776000: {"90", unitDays},
	} {
		figure, unit := fieldDuration(seconds)
		if figure != want[0] || unit != want[1] {
			t.Errorf("%d seconds fills the field with %s %s, want %s %s", seconds, figure, unit,
				want[0], want[1])
		}
	}
}

// TestATypedDurationBecomesSeconds: a figure in a unit is multiplied; a figure in
// seconds, or with no unit, reaches the validation exactly as typed; an unknown unit
// and an overflowing product are refused here.
func TestATypedDurationBecomesSeconds(t *testing.T) {
	t.Parallel()
	for _, typed := range []struct {
		figure, unit, seconds string
		refusal               messageKey
	}{
		{"2", unitHours, "7200", ""},
		{"90", unitDays, "7776000", ""},
		{"-1", unitMinutes, "-60", ""},
		{"1.5", unitSeconds, "1.5", ""},
		{"often", "", "often", ""},
		{"often", unitDays, "often", ""},
		{"1", "fortnights", "", msgDurationUnitUnknown},
		{strconv.FormatInt(math.MaxInt64/86400+1, 10), unitDays, "", msgCollectionIntervalTooLarge},
	} {
		seconds, refusal := typedDuration(typed.figure, typed.unit)
		if seconds != typed.seconds || refusal != typed.refusal {
			t.Errorf("%s %s became %q refused %q, want %q refused %q", typed.figure, typed.unit,
				seconds, refusal, typed.seconds, typed.refusal)
		}
	}
}
