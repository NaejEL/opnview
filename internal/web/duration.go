package web

import (
	"math"
	"strconv"
)

// Durations in the units a person reads them in.
//
// A SETTING IS STORED IN SECONDS AND READ IN DAYS. config.Load and every setting row
// hold a whole number of seconds, which is the right unit for a machine and the
// wrong one for a reader: 7776000 is ninety days, and nobody sees that at a glance.
// So every duration the surfaces draw is spelled in days, hours, minutes and seconds,
// and every duration field is a number beside a unit; the handler multiplies the two
// back into seconds and validates THAT through config.Load, exactly as before. What is
// stored, and what Load accepts, do not change.
//
// THE WORDS ARE THE CATALOGUE'S. A unit is a pair of keys, one for a count of one and
// one for any other count -- English's rule, the source language's -- and the figure
// beside it is data. A language whose plural rule differs is step 7's language file,
// which then needs more than the two forms; that is recorded here rather than assumed
// away.

// durationPart is one figure and its unit: "90" and "days".
type durationPart struct {
	Figure  string
	UnitKey messageKey
}

// durationUnit is one unit a duration is spelled or typed in.
type durationUnit struct {
	// Value is the unit as a form submits it.
	Value string
	// Seconds is its length.
	Seconds int64
	// One and Other name a count of one and any other count; FieldKey names the unit
	// as a field's option.
	One      messageKey
	Other    messageKey
	FieldKey messageKey
}

// The units, longest first.
const (
	unitDays    = "days"
	unitHours   = "hours"
	unitMinutes = "minutes"
	unitSeconds = "seconds"
)

// durationUnits are the units, longest first: the order a duration is spelled in.
func durationUnits() []durationUnit {
	return []durationUnit{
		{Value: unitDays, Seconds: 86400, One: msgUnitDay, Other: msgUnitDays, FieldKey: msgUnitDays},
		{Value: unitHours, Seconds: 3600, One: msgUnitHour, Other: msgUnitHours, FieldKey: msgUnitHours},
		{Value: unitMinutes, Seconds: 60, One: msgUnitMinute, Other: msgUnitMinutes, FieldKey: msgUnitMinutes},
		{Value: unitSeconds, Seconds: 1, One: msgUnitSecond, Other: msgUnitSeconds, FieldKey: msgUnitSeconds},
	}
}

// durationPartOf is one count of one unit.
func durationPartOf(count int64, unit durationUnit) durationPart {
	key := unit.Other
	if count == 1 {
		key = unit.One
	}
	return durationPart{Figure: strconv.FormatInt(count, 10), UnitKey: key}
}

// spelledDuration spells a number of seconds in every unit it holds, longest first
// and exactly: 410 is 6 minutes 50 seconds, 86400 is 1 day. Nothing is rounded,
// because a duration on these surfaces is a figure somebody may compare against a
// log. Nought is "0 seconds".
func spelledDuration(seconds int64) []durationPart {
	if seconds <= 0 {
		return []durationPart{durationPartOf(0, durationUnits()[len(durationUnits())-1])}
	}
	var parts []durationPart
	rest := seconds
	for _, unit := range durationUnits() {
		if count := rest / unit.Seconds; count > 0 {
			parts = append(parts, durationPartOf(count, unit))
			rest -= count * unit.Seconds
		}
	}
	return parts
}

// fieldDuration is how a duration field shows a number of seconds: in the longest
// unit that holds it exactly, so 7776000 is 90 days and 90 is 90 seconds. The field
// therefore always holds exactly the value in force, never a rounding of it.
func fieldDuration(seconds int64) (figure, unit string) {
	if seconds > 0 {
		for _, candidate := range durationUnits() {
			if seconds%candidate.Seconds == 0 {
				return strconv.FormatInt(seconds/candidate.Seconds, 10), candidate.Value
			}
		}
	}
	return strconv.FormatInt(seconds, 10), unitSeconds
}

// unitOptions are a duration field's units, the one shown selected.
func unitOptions(selected string) []choiceOption {
	var options []choiceOption
	for _, unit := range durationUnits() {
		options = append(options, choiceOption{Value: unit.Value, LabelKey: unit.FieldKey,
			Selected: unit.Value == selected})
	}
	return options
}

// unitFieldName is the form field carrying a duration field's unit.
func unitFieldName(field string) string { return field + "_unit" }

// typedDuration turns a typed figure and unit into the seconds text the validation
// reads.
//
// A FIGURE IN SECONDS, OR WITH NO UNIT AT ALL, IS PASSED THROUGH UNTOUCHED, so the
// validation sees exactly what was typed and refuses exactly what config.Load
// refuses. A figure in a longer unit is multiplied; one that does not parse is passed
// through as typed, and the validation refuses it as it would any other text. A unit
// that is not one of the four is refused here, and so is a product too large to be a
// number of seconds at all.
func typedDuration(figure, unit string) (seconds string, refusal messageKey) {
	if unit == "" || unit == unitSeconds {
		return figure, ""
	}
	for _, candidate := range durationUnits() {
		if candidate.Value != unit {
			continue
		}
		count, err := strconv.ParseInt(figure, 10, 64)
		if err != nil {
			return figure, ""
		}
		if count > math.MaxInt64/candidate.Seconds || count < math.MinInt64/candidate.Seconds {
			return "", msgCollectionIntervalTooLarge
		}
		return strconv.FormatInt(count*candidate.Seconds, 10), ""
	}
	return "", msgDurationUnitUnknown
}
