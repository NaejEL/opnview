package collection

import (
	"context"
	"fmt"

	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/sizing"
	"github.com/NaejEL/opnview/internal/web"
)

// What the external tests of this surface need of its internals.

// SettingCardSpecs are the settings cards, in the order their anchors are tried.
var SettingCardSpecs = settingCardSpecs

// RuleAggregateMode is the rule that reads the site-name mode.
const RuleAggregateMode = ruleAggregateMode

// AcceptedRangeOf asks config.Load which whole numbers of seconds it accepts for a setting.
var AcceptedRangeOf = acceptedRangeOf

// KindNamed is the sized kind a card id names.
var KindNamed = kindNamed

// MeasureSuggestion measures one kind and returns the suggestion the surface would draw.
func MeasureSuggestion(ctx context.Context, host web.Host, kind sizing.Kind) (sizing.Suggestion, error) {
	_, suggestion, _, err := newSurface(host).measureKind(ctx, kind)
	return suggestion, err
}

// SettingInForce reads a setting's value in force out of a configuration, as the card
// shows it.
func SettingInForce(setting settingSpec, defaults config.Config) int64 {
	return setting.inForce(defaults)
}

// PageFigure is one figure the collection page draws: the card it is on, its label (a catalogue
// key, or for a field its name) and what is drawn, a state being "state:" and its key and a
// duration its spelled parts.
type PageFigure struct {
	Card, Label, Text string
}

// spelled renders a duration as the page draws its parts.
func spelled(parts []web.DurationPart) string { return fmt.Sprint(parts) }

// figureText renders one figure row.
func figureText(row figureRow) string {
	switch {
	case row.StateKey != "":
		return "state:" + string(row.StateKey)
	case row.Duration != nil:
		return spelled(row.Duration)
	}
	return row.Value
}

// selectedOf is the selected option's value.
func selectedOf(options []selectionOption) string {
	for _, option := range options {
		if option.Selected {
			return option.Value
		}
	}
	return ""
}

// fieldText is a field as the page shows it: its figure and selected unit, or its selected option.
func fieldText(field web.SettingField) string {
	for _, option := range field.Options {
		if option.Selected {
			return option.Value
		}
	}
	for _, unit := range field.Units {
		if unit.Selected {
			return field.Value + " " + unit.Value
		}
	}
	return field.Value
}

// PageFigures are every figure the collection page draws, read through the page's own fill: what
// the JSON answer must carry (C-AC9).
func PageFigures(ctx context.Context, host web.Host) ([]PageFigure, error) {
	var built view
	if err := newSurface(host).fillCollection(ctx, &built, nil); err != nil {
		return nil, err
	}
	var figures []PageFigure
	add := func(card, label, text string) { figures = append(figures, PageFigure{card, label, text}) }
	for _, entry := range built.CollectionEntries {
		if card := entry.Sizing; card != nil {
			for _, group := range [][]figureRow{card.InForce, card.Measurement, card.Suggestion} {
				for _, row := range group {
					add(card.Kind, string(row.LabelKey), figureText(row))
				}
			}
			for _, gap := range card.Gaps {
				add(card.Kind, string(gap.ReasonKey), gap.Count+" "+spelled(gap.Missed))
			}
			for _, implementation := range card.Implementations {
				add(card.Kind, string(implementation.LabelKey), selectedOf(implementation.Options))
			}
		}
		if card := entry.Settings; card != nil {
			for _, row := range card.Availability {
				add(card.ID, "availability "+string(row.LabelKey), figureText(row))
			}
			for _, implementation := range card.Implementations {
				add(card.ID, string(implementation.LabelKey), selectedOf(implementation.Options))
			}
			for _, field := range card.Fields {
				add(card.ID, field.Name, fieldText(field))
			}
		}
	}
	return figures, nil
}
