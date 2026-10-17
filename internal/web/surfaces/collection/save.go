package collection

import (
	"context"
	"strconv"

	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/sizing"
	"github.com/NaejEL/opnview/internal/store/attribution"
	"github.com/NaejEL/opnview/internal/web/settingfields"
)

// What a save of a card checks and stores, written once for the JSON routes (api.go). A figure reaches these functions in
// seconds.

// The fields of an edit a refusal names, independent of how the JSON body spells them.
const (
	editFieldInterval = "interval"
	editFieldPageSize = "page_size"
	// editFieldSelection, followed by a provider key, is one implementation's selection.
	editFieldSelection = "selection:"
)

// kindEdit is a checked edit of a polled kind: its interval, its page size where the page is a
// setting, and the selections the edit carried.
type kindEdit struct {
	seconds    int64
	pageSize   int
	selections map[string]config.Selection
}

// checkSelections reads the selections an edit carried, in the providers' order, and refuses the
// first that is not one of the three words.
func checkSelections(providers []attribution.ProviderOfKind, sent map[string]string) (map[string]config.Selection,
	messageKey, string) {
	selections := make(map[string]config.Selection, len(providers))
	for _, provider := range providers {
		value, present := sent[provider.ProviderKey]
		if !present {
			continue
		}
		selection, err := config.ParseSelection(value)
		if err != nil {
			return nil, msgCollectionValueInvalid, editFieldSelection + provider.ProviderKey
		}
		selections[provider.ProviderKey] = selection
	}
	return selections, "", ""
}

// checkKindEdit checks an edit of a sized kind: the interval, then the page size where the page is
// a setting, then the selections. A page size above the firewall's ceiling is not refused: it is
// stored and reported as above it.
func checkKindEdit(kind sizing.Kind, providers []attribution.ProviderOfKind, secondsText, pageText string,
	sent map[string]string) (kindEdit, messageKey, string) {
	seconds, refusal := settingfields.TypedIntervalSeconds(secondsText)
	if refusal != "" {
		return kindEdit{}, refusal, editFieldInterval
	}
	edit := kindEdit{seconds: seconds}
	if kind.PageIsAdjustable() {
		if edit.pageSize, refusal = typedPageSize(pageText); refusal != "" {
			return kindEdit{}, refusal, editFieldPageSize
		}
	}
	selections, refusal, field := checkSelections(providers, sent)
	if refusal != "" {
		return kindEdit{}, refusal, field
	}
	edit.selections = selections
	return edit, "", ""
}

// checkPolledEdit checks an edit of a polled kind, which has no page: the interval, then the
// selections.
func checkPolledEdit(providers []attribution.ProviderOfKind, secondsText string, sent map[string]string) (kindEdit,
	messageKey, string) {
	seconds, refusal := settingfields.TypedIntervalSeconds(secondsText)
	if refusal != "" {
		return kindEdit{}, refusal, editFieldInterval
	}
	selections, refusal, field := checkSelections(providers, sent)
	if refusal != "" {
		return kindEdit{}, refusal, field
	}
	return kindEdit{seconds: seconds, selections: selections}, "", ""
}

// saveKindEdit stores a sized kind's checked edit and makes it live.
func (s *surface) saveKindEdit(ctx context.Context, kind sizing.Kind, providers []attribution.ProviderOfKind,
	edit kindEdit) error {
	now := s.now().UTC().Unix()
	if kind.PageIsAdjustable() {
		if err := s.store.SetSetting(ctx, kind.PageSizeKey, strconv.Itoa(edit.pageSize), now); err != nil {
			return err
		}
	}
	return s.saveIntervalAndSelections(ctx, kind.Kind, kind.IntervalKey, providers, edit, now)
}

// savePolledEdit stores a polled kind's checked edit and makes it live.
func (s *surface) savePolledEdit(ctx context.Context, kind polledKind, providers []attribution.ProviderOfKind,
	edit kindEdit) error {
	return s.saveIntervalAndSelections(ctx, kind.Kind, kind.IntervalKey, providers, edit, s.now().UTC().Unix())
}

// saveIntervalAndSelections stores a kind's interval and selections, then hands the configuration
// to the running service and applies the selections at once.
func (s *surface) saveIntervalAndSelections(ctx context.Context, kind, intervalKey string,
	providers []attribution.ProviderOfKind, edit kindEdit, now int64) error {
	if err := s.store.SetSetting(ctx, intervalKey, strconv.FormatInt(edit.seconds, 10), now); err != nil {
		return err
	}
	for _, provider := range providers {
		selection, chosen := edit.selections[provider.ProviderKey]
		if !chosen {
			continue
		}
		if err := s.store.SetSetting(ctx, config.KeySourceSelection(kind, provider.ProviderKey),
			string(selection), now); err != nil {
			return err
		}
	}
	// THE RUNNING SERVICE, through the same path start-up uses. This is what makes a saved pair
	// reach the scheduler and the collectors without a restart.
	if err := s.ApplyConfiguration(ctx); err != nil {
		return err
	}
	// The selections are applied at once, from what the last probe round found, without
	// contacting the firewall.
	if s.collector != nil {
		return s.collector.Reselect(ctx, kind)
	}
	return nil
}

// checkSettingCard checks a group of settings, each in the card's order -- its figure in seconds,
// as secondsOf gives it, by its rule and then by config.Load -- and then all of them together,
// which is what will be in force. It returns the values to store, or the refusal and the setting
// it is about.
func checkSettingCard(ctx context.Context, base config.SettingReader, spec settingCardSpec,
	secondsOf func(settingSpec) (string, messageKey)) (map[string]string, messageKey, string) {
	values := map[string]string{}
	for _, setting := range spec.Settings {
		seconds, refusal := secondsOf(setting)
		if refusal != "" {
			return nil, refusal, setting.Key
		}
		if refusal := typedSetting(ctx, base, setting, seconds); refusal != "" {
			return nil, refusal, setting.Key
		}
		values[setting.Key] = seconds
	}
	if !settingfields.AcceptedByLoad(ctx, base, values) {
		return nil, spec.Settings[0].RefusalKey, spec.Settings[0].Key
	}
	return values, "", ""
}

// needsRetentionConfirmation says whether a save would shorten the retention horizon without
// having confirmed the horizon it saves.
func (s *surface) needsRetentionConfirmation(ctx context.Context, values map[string]string,
	confirmed string) (bool, error) {
	horizon, present := values[config.KeyRetentionSeconds]
	if !present || confirmed == horizon {
		return false, nil
	}
	settings, err := s.Configuration(ctx)
	if err != nil {
		return false, err
	}
	return shortens(settings.RetentionSeconds, horizon), nil
}

// saveSettingValues stores a checked group of settings and hands the configuration to the running
// service.
func (s *surface) saveSettingValues(ctx context.Context, spec settingCardSpec, values map[string]string) error {
	now := s.now().UTC().Unix()
	for _, setting := range spec.Settings {
		if err := s.store.SetSetting(ctx, setting.Key, values[setting.Key], now); err != nil {
			return err
		}
	}
	return s.ApplyConfiguration(ctx)
}
