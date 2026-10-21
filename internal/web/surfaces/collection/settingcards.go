package collection

import (
	"context"
	"strconv"
	"time"

	"github.com/NaejEL/opnview/internal/web/settingfields"

	"github.com/NaejEL/opnview/internal/collect/provider"
	"github.com/NaejEL/opnview/internal/config"
)

// The collection surface's settings cards: the two resolver-record reads, which are polled and
// not sized, and the settings that govern what collection does with what it reads.
//
// EVERY SAVE IS VALIDATED BY config.Load ITSELF. The value, in seconds, is put over the stored
// rows and the whole configuration is loaded, exactly as start-up loads it; what Load refuses is
// refused here, with nothing stored, and what Load accepts is written and handed to the running
// service. So a save cannot accept a value Load would refuse, nor refuse one it would accept.
//
// A SHORTER RETENTION IS CONFIRMED FOR THE VALUE IT WARNED ABOUT. A save shortening the horizon
// without `confirm` equal to the new horizon stores nothing and is answered with what the
// confirmation is about (api.go).
//
// A CHANGED SETTING IS NOT RETROACTIVE: what it governs is what is collected, derived or purged
// from then on.

// fieldRule is how a setting's value is read, before config.Load decides.
type fieldRule int

const (
	// rulePositive is a whole number above zero.
	rulePositive fieldRule = iota
	// ruleNonNegative is a whole number, zero or above.
	ruleNonNegative
	// ruleAggregateMode is one of the two site-name modes.
	ruleAggregateMode
)

// The two site-name modes, as the setting row holds them.
const (
	aggregateModeFull      = "full"
	aggregateModeNoDomains = "no_domains"
)

// settingSpec is one setting on a card.
type settingSpec struct {
	// Key is the setting row and the body member.
	Key string
	// Rule reads the value.
	Rule fieldRule
	// RefusalKey is what a refused value is answered with.
	RefusalKey messageKey
	// inForce reads the value in force out of a configuration.
	inForce func(config.Config) int64
}

// settingCardSpec is one settings card.
type settingCardSpec struct {
	// ID names the card in its route.
	ID string
	// Settings are its settings, in order.
	Settings []settingSpec
}

// settingCardSpecs are the settings cards, in the order the answer lists them.
func settingCardSpecs() []settingCardSpec {
	return []settingCardSpec{
		{ID: "pairing", Settings: []settingSpec{{
			Key: config.KeyLegPairingWindow, Rule: ruleNonNegative, RefusalKey: msgValueOutOfRange,
			inForce: func(c config.Config) int64 { return c.LegPairingWindowSeconds },
		}}},
		{ID: "attribution", Settings: []settingSpec{
			{Key: config.KeyAttributionMaxDelay, Rule: rulePositive, RefusalKey: msgCollectionValueInvalid,
				inForce: func(c config.Config) int64 { return c.AttributionMaxDelaySeconds }},
			{Key: config.KeyCacheAnswerDelay, Rule: rulePositive, RefusalKey: msgCollectionValueInvalid,
				inForce: func(c config.Config) int64 { return c.CacheAnswerDelaySeconds }},
		}},
		{ID: "storage", Settings: []settingSpec{
			{Key: config.KeyRetentionSeconds, Rule: ruleNonNegative, RefusalKey: msgValueNotNonNegative,
				inForce: func(c config.Config) int64 { return c.RetentionSeconds }},
			{Key: config.KeyAggregateMode, Rule: ruleAggregateMode, RefusalKey: msgAggregateModeUnknown},
		}},
	}
}

// polledKind is a kind read on an interval but not sized: there is no page to size, so its
// edit carries the interval and the selections and no measurement.
type polledKind struct {
	// Kind is the registry kind.
	Kind string
	// IntervalKey is the setting row holding its interval.
	IntervalKey string
}

// interval reads the kind's interval in force.
func (k polledKind) interval(settings config.Config) time.Duration {
	return settings.Interval(k.Kind, provider.CadencePoll)
}

// polledKinds are the kinds polled and not sized: every core kind whose file gives it a collection
// card (internal/collect/provider/kinds). A kind is answered only when the provider table holds it,
// and its implementations are read from that table.
func polledKinds() []polledKind {
	var polled []polledKind
	for _, contract := range provider.PolledKinds() {
		cadence, _ := contract.CadenceOf(provider.CadencePoll)
		polled = append(polled, polledKind{Kind: contract.Kind, IntervalKey: cadence.Setting})
	}
	return polled
}

// settingInForce is one setting's value in force, as its row would hold it.
func settingInForce(setting settingSpec, settings config.Config) string {
	if setting.Rule == ruleAggregateMode {
		return settings.AggregateMode
	}
	return strconv.FormatInt(setting.inForce(settings), 10)
}

// typedSetting reads one value by its rule, and then by config.Load. A duration's value arrives
// here in seconds.
func typedSetting(ctx context.Context, base config.SettingReader, spec settingSpec, typed string) messageKey {
	switch spec.Rule {
	case rulePositive:
		if value, err := strconv.ParseInt(typed, 10, 64); err != nil || value <= 0 {
			return spec.RefusalKey
		}
	case ruleNonNegative:
		if value, err := strconv.ParseInt(typed, 10, 64); err != nil || value < 0 {
			return spec.RefusalKey
		}
	case ruleAggregateMode:
		if typed != aggregateModeFull && typed != aggregateModeNoDomains {
			return spec.RefusalKey
		}
	}
	if !settingfields.AcceptedByLoad(ctx, base, map[string]string{spec.Key: typed}) {
		return spec.RefusalKey
	}
	return ""
}

// shortens says whether a retention horizon purges more than the one in force: a horizon, where
// there was none, or a shorter one. 0 is unlimited.
func shortens(inForce int64, typed string) bool {
	horizon, err := strconv.ParseInt(typed, 10, 64)
	if err != nil || horizon == 0 {
		return false
	}
	return inForce == 0 || horizon < inForce
}
