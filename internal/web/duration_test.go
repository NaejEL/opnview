package web_test

import (
	"math"
	"strconv"
	"strings"
	"testing"

	"github.com/NaejEL/opnview/internal/web"
	"github.com/NaejEL/opnview/internal/web/settingfields"
	"github.com/NaejEL/opnview/internal/web/webtest/collectiontest"
)

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
		for _, part := range settingfields.SpelledDuration(seconds) {
			words = append(words, part.Figure, collectiontest.CatalogueText(t, part.UnitKey))
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
		0:       {"0", settingfields.UnitSeconds},
		5:       {"5", settingfields.UnitSeconds},
		90:      {"90", settingfields.UnitSeconds},
		300:     {"5", settingfields.UnitMinutes},
		3600:    {"1", settingfields.UnitHours},
		5400:    {"90", settingfields.UnitMinutes},
		86400:   {"1", settingfields.UnitDays},
		7776000: {"90", settingfields.UnitDays},
	} {
		figure, unit := settingfields.FieldDuration(seconds)
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
		refusal               web.MessageKey
	}{
		{"2", settingfields.UnitHours, "7200", ""},
		{"90", settingfields.UnitDays, "7776000", ""},
		{"-1", settingfields.UnitMinutes, "-60", ""},
		{"1.5", settingfields.UnitSeconds, "1.5", ""},
		{"often", "", "often", ""},
		{"often", settingfields.UnitDays, "often", ""},
		{"1", "fortnights", "", web.MessageKey("error.duration_unit_unknown")},
		{strconv.FormatInt(math.MaxInt64/86400+1, 10), settingfields.UnitDays, "", web.MessageKey("error.collection_interval_too_large")},
	} {
		seconds, refusal := settingfields.TypedDuration(typed.figure, typed.unit)
		if seconds != typed.seconds || refusal != typed.refusal {
			t.Errorf("%s %s became %q refused %q, want %q refused %q", typed.figure, typed.unit,
				seconds, refusal, typed.seconds, typed.refusal)
		}
	}
}
