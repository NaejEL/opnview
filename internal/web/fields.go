package web

import (
	"context"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/NaejEL/opnview/internal/config"
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

// DurationPart is one figure and its unit: "90" and "days".
type DurationPart struct {
	Figure  string
	UnitKey messageKey
}

// DurationUnit is one unit a duration is spelled or typed in.
type DurationUnit struct {
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
	UnitDays    = "days"
	UnitHours   = "hours"
	UnitMinutes = "minutes"
	UnitSeconds = "seconds"
)

// DurationUnits are the units, longest first: the order a duration is spelled in.
func DurationUnits() []DurationUnit {
	return []DurationUnit{
		{Value: UnitDays, Seconds: 86400, One: msgUnitDay, Other: msgUnitDays, FieldKey: msgUnitDays},
		{Value: UnitHours, Seconds: 3600, One: msgUnitHour, Other: msgUnitHours, FieldKey: msgUnitHours},
		{Value: UnitMinutes, Seconds: 60, One: msgUnitMinute, Other: msgUnitMinutes, FieldKey: msgUnitMinutes},
		{Value: UnitSeconds, Seconds: 1, One: msgUnitSecond, Other: msgUnitSeconds, FieldKey: msgUnitSeconds},
	}
}

// durationPartOf is one count of one unit.
func durationPartOf(count int64, unit DurationUnit) DurationPart {
	key := unit.Other
	if count == 1 {
		key = unit.One
	}
	return DurationPart{Figure: strconv.FormatInt(count, 10), UnitKey: key}
}

// SpelledDuration spells a number of seconds in every unit it holds, longest first
// and exactly: 410 is 6 minutes 50 seconds, 86400 is 1 day. Nothing is rounded,
// because a duration on these surfaces is a figure somebody may compare against a
// log. Nought is "0 seconds".
func SpelledDuration(seconds int64) []DurationPart {
	if seconds <= 0 {
		return []DurationPart{durationPartOf(0, DurationUnits()[len(DurationUnits())-1])}
	}
	var parts []DurationPart
	rest := seconds
	for _, unit := range DurationUnits() {
		if count := rest / unit.Seconds; count > 0 {
			parts = append(parts, durationPartOf(count, unit))
			rest -= count * unit.Seconds
		}
	}
	return parts
}

// FieldDuration is how a duration field shows a number of seconds: in the longest
// unit that holds it exactly, so 7776000 is 90 days and 90 is 90 seconds. The field
// therefore always holds exactly the value in force, never a rounding of it.
func FieldDuration(seconds int64) (figure, unit string) {
	if seconds > 0 {
		for _, candidate := range DurationUnits() {
			if seconds%candidate.Seconds == 0 {
				return strconv.FormatInt(seconds/candidate.Seconds, 10), candidate.Value
			}
		}
	}
	return strconv.FormatInt(seconds, 10), UnitSeconds
}

// UnitOptions are a duration field's units, the one shown selected.
func UnitOptions(selected string) []ChoiceOption {
	var options []ChoiceOption
	for _, unit := range DurationUnits() {
		options = append(options, ChoiceOption{Value: unit.Value, LabelKey: unit.FieldKey,
			Selected: unit.Value == selected})
	}
	return options
}

// UnitFieldName is the form field carrying a duration field's unit.
func UnitFieldName(field string) string { return field + "_unit" }

// TypedDuration turns a typed figure and unit into the seconds text the validation
// reads.
//
// A FIGURE IN SECONDS, OR WITH NO UNIT AT ALL, IS PASSED THROUGH UNTOUCHED, so the
// validation sees exactly what was typed and refuses exactly what config.Load
// refuses. A figure in a longer unit is multiplied; one that does not parse is passed
// through as typed, and the validation refuses it as it would any other text. A unit
// that is not one of the four is refused here, and so is a product too large to be a
// number of seconds at all.
func TypedDuration(figure, unit string) (seconds string, refusal messageKey) {
	if unit == "" || unit == UnitSeconds {
		return figure, ""
	}
	for _, candidate := range DurationUnits() {
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

// ChoiceOption is one option of a select whose options are not the source selection's
// three.
type ChoiceOption struct {
	Value    string
	LabelKey MessageKey
	Selected bool
}

// InForceValue is a value in force: a duration, a figure, or a named state.
type InForceValue struct {
	Parts    []DurationPart
	Value    string
	StateKey MessageKey
}

// AcceptedRange is the range of values config.Load accepts for one setting: its lowest
// value, and its highest or the named state of having none.
type AcceptedRange struct {
	Low  InForceValue
	High InForceValue
}

// SettingField is one field of a settings card, drawn by the layout's "setting-field"
// template the same on every surface that has settings: a select, a figure beside its unit,
// or a plain figure; the refusal against it when there is one; and the value in force
// beneath it, drawn only when the field shows something else or when what is in force is a
// named state.
type SettingField struct {
	// Name is the form field, ID its element id.
	Name string
	ID   string
	// LabelKey names it.
	LabelKey MessageKey
	// Value is what the field shows: the value in force, or what was typed. For a
	// duration it is the figure, in the unit Units has selected.
	Value string
	// Options make the field a select.
	Options []ChoiceOption
	// UnitName and Units make the field a duration: a figure and a unit.
	UnitName string
	Units    []ChoiceOption
	// RefusalKey refuses what was typed in it, which marks it invalid; Range, beside
	// it, is the range config.Load accepts, for a setting whose refusal states one.
	Range      *AcceptedRange
	RefusalKey MessageKey
	// InForce is the value in force, drawn under the field when ShowInForce: when the
	// field shows something else, or when the value in force is a named state.
	InForce     InForceValue
	ShowInForce bool
}

// DurationField builds a duration field: the value in force in its longest exact
// unit, or the figure and unit typed and not stored.
func DurationField(name, id string, label MessageKey, inForceSeconds int64,
	typed map[string]string) SettingField {
	figure, unit := FieldDuration(inForceSeconds)
	field := SettingField{Name: name, ID: id, LabelKey: label, UnitName: UnitFieldName(name),
		Value: figure, InForce: InForceValue{Parts: SpelledDuration(inForceSeconds)}}
	shownUnit := unit
	if value, present := typed[name]; present {
		field.Value = value
		shownUnit = typed[UnitFieldName(name)]
		if shownUnit == "" {
			shownUnit = UnitSeconds
		}
	}
	field.Units = UnitOptions(shownUnit)
	field.ShowInForce = field.Value != figure || shownUnit != unit
	return field
}

// DurationDiffers says whether a typed figure and unit name a number of seconds other
// than the one in force. A figure that does not read as a duration differs.
func DurationDiffers(figure, unit string, inForce int64) bool {
	seconds, refusal := TypedDuration(strings.TrimSpace(figure), unit)
	if refusal != "" {
		return true
	}
	parsed, err := strconv.ParseInt(seconds, 10, 64)
	return err != nil || parsed != inForce
}

// Instant is an epoch second in RFC 3339, UTC.
func Instant(epoch int64) string {
	return time.Unix(epoch, 0).UTC().Format(time.RFC3339)
}

// WholeSeconds is a duration as a whole number of seconds.
func WholeSeconds(d time.Duration) string {
	return strconv.FormatInt(int64(d/time.Second), 10)
}

// overlayReader is the stored settings with some rows replaced by what was typed:
// what config.Load would read if the save went through.
type overlayReader struct {
	base   config.SettingReader
	values map[string]string
}

// Setting reads a typed value first, then the stored row.
func (o overlayReader) Setting(ctx context.Context, key string) (string, bool, error) {
	if value, typed := o.values[key]; typed {
		return value, true, nil
	}
	return o.base.Setting(ctx, key)
}

// AcceptedByLoad says whether config.Load accepts the stored configuration with
// the typed rows put over it. It is the validation every settings save runs, on every
// surface that saves a setting: what Load refuses is refused, nothing is stored, and what
// Load accepts is written.
func AcceptedByLoad(ctx context.Context, base config.SettingReader, values map[string]string) bool {
	_, err := config.Load(ctx, overlayReader{base: base, values: values})
	return err == nil
}

// TypedIntervalSeconds reads a typed interval in whole seconds. An interval is a
// positive whole number; the ceiling is config.MaxIntervalSeconds, the largest whole
// number of seconds a time.Duration can hold, which is also the bound Load applies to a
// stored row, so a figure accepted here is one Load will read back.
func TypedIntervalSeconds(typed string) (int64, MessageKey) {
	seconds, err := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
	if err != nil || seconds <= 0 {
		return 0, msgCollectionValueInvalid
	}
	if seconds > config.MaxIntervalSeconds {
		return 0, msgCollectionIntervalTooLarge
	}
	return seconds, ""
}
