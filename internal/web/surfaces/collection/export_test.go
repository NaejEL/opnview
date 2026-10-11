package collection

import (
	"context"

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
