package collection

import (
	"context"
	"github.com/NaejEL/opnview/internal/web"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/NaejEL/opnview/internal/config"
)

// The collection surface's settings cards: the two resolver-record reads, which are
// polled and not sized, and the settings that govern what collection does with what
// it reads.
//
// WHERE EACH ONE SITS, AND WHY (specs/SPEC-deferred-interface-bundle.md, scope 2):
//
//   - retention and the site-name mode OPEN the page, on a card of their own: they
//     govern everything stored, whichever kind stored it, and on a page eight
//     thousand pixels tall a card at the foot is a card nobody reaches;
//   - the resolver's cache and its local data each get a card of their own, after
//     the resolver lookups' card: they are read from the same resolver, beside its
//     lookups, and each is a kind of its own in the provider table;
//   - the attribution card follows them: its two settings decide how far before a
//     flow a lookup, or a cache answer, may name it, which is what the three reads
//     above it exist for;
//   - the pairing window follows the filter log's card: it decides which two
//     filter-log records are one connection's two legs, and nothing else reads it;
//   - the lease backends' failure limit follows the DHCP leases' card: it counts
//     lease passes, and the spec asks for it on or near that card;
//   - the Public Suffix List's refresh interval is on the settings surface, on that
//     list's own card, beside the state of the copy it refreshes.
//
// A card whose anchor is absent -- a kind this installation's provider table does
// not hold -- moves to the end rather than disappearing: the setting governs
// something whether or not the card it sat beside is drawn. An index of the cards
// heads the page, each entry a link to its card's anchor.
//
// A DURATION IS TYPED AS A NUMBER AND A UNIT, and is stored in seconds (duration.go).
// The field shows the value in force in the longest unit that holds it exactly, and
// the value in force is drawn a second time, under the field, only when the field
// shows something else -- a refused or unconfirmed value -- or when it is a named
// state rather than a figure: an unlimited retention.
//
// EVERY SAVE IS VALIDATED BY config.Load ITSELF. The typed value, turned into seconds,
// is put over the stored rows and the whole configuration is loaded, exactly as
// start-up loads it; what Load refuses is refused here, with nothing stored, and what
// Load accepts is written and handed to the running service. So the form cannot
// accept a value Load would refuse, nor refuse one it would accept -- including a rule
// the parallel cycle adds to Load later. An interval is first read as
// typedIntervalSeconds reads one, so a figure too large to be a duration is named as
// such.
//
// A SHORTER RETENTION IS CONFIRMED FOR THE VALUE IT WARNED ABOUT. The confirmation
// states the horizon in force and the one asked for, holds the values it is about
// out of reach of editing, and carries the horizon it confirms; a save whose horizon
// is not the one confirmed asks again. Cancel returns to the card unchanged.
//
// A CHANGED SETTING IS NOT RETROACTIVE, and nothing here says it is: the save is
// confirmed as saved, and what it governs is what is collected, derived or purged
// from then on.

// fieldCard names the settings card a form is about.
const fieldCard = "card"

// fieldConfirm carries, with a save that would shorten the retention horizon, the
// horizon in seconds that the operator confirmed.
const fieldConfirm = "confirm"

// fieldRule is how a setting's typed value is read, before config.Load decides.
type fieldRule int

const (
	// rulePositive is a whole number above zero.
	rulePositive fieldRule = iota
	// ruleNonNegative is a whole number, zero or above.
	ruleNonNegative
	// ruleAggregateMode is one of the two site-name modes.
	ruleAggregateMode
)

// settingSpec is one setting on a card.
type settingSpec struct {
	// Key is the setting row and the form field.
	Key string
	// LabelKey names it.
	LabelKey messageKey
	// Rule reads the typed value.
	Rule fieldRule
	// Duration says the value is a number of seconds, typed as a number and a unit.
	// RangeRefusal says a refusal of it states the range config.Load accepts.
	Duration     bool
	RangeRefusal bool
	// RefusalKey is what a refused value is told.
	RefusalKey messageKey
	// inForce reads the value in force out of a configuration.
	inForce func(config.Config) int64
}

// settingCardSpec is one settings card.
type settingCardSpec struct {
	// ID names the card in its form and its anchors.
	ID string
	// LabelKey is its heading.
	LabelKey messageKey
	// Leading puts the card at the head of the page. Otherwise After are the cards it
	// follows, the first one drawn; none drawn puts it last.
	Leading bool
	After   []string
	// Settings are its fields, in order.
	Settings []settingSpec
}

// settingCardSpecs are the settings cards, in the order their anchors are tried.
func settingCardSpecs() []settingCardSpec {
	return []settingCardSpec{
		{ID: "pairing", LabelKey: msgCardPairing, After: []string{"firewall_log"},
			Settings: []settingSpec{{
				Key: config.KeyLegPairingWindow, LabelKey: msgLabelPairingWindow,
				Rule: ruleNonNegative, Duration: true, RangeRefusal: true,
				RefusalKey: msgValueOutOfRange,
				inForce:    func(c config.Config) int64 { return c.LegPairingWindowSeconds },
			}}},
		{ID: "lease_backends", LabelKey: msgCardLeaseBackends, After: []string{"dhcp_lease"},
			Settings: []settingSpec{{
				Key: config.KeyLeaseBackendFailurePassLimit, LabelKey: msgLabelLeaseFailureLimit,
				Rule: rulePositive, RefusalKey: msgCollectionValueInvalid,
				inForce: func(c config.Config) int64 { return c.LeaseBackendFailurePassLimit },
			}}},
		{ID: "attribution", LabelKey: msgCardAttribution,
			After: []string{"resolver_local_data", "resolver_cache", "dns_lookup"},
			Settings: []settingSpec{
				{Key: config.KeyAttributionMaxDelay, LabelKey: msgLabelAttributionDelay,
					Rule: rulePositive, Duration: true, RefusalKey: msgCollectionValueInvalid,
					inForce: func(c config.Config) int64 { return c.AttributionMaxDelaySeconds }},
				{Key: config.KeyCacheAnswerDelay, LabelKey: msgLabelCacheAnswerCap,
					Rule: rulePositive, Duration: true, RefusalKey: msgCollectionValueInvalid,
					inForce: func(c config.Config) int64 { return c.CacheAnswerDelaySeconds }},
			}},
		{ID: "storage", LabelKey: msgCardStorage, Leading: true,
			Settings: []settingSpec{
				{Key: config.KeyRetentionSeconds, LabelKey: msgLabelRetention,
					Rule: ruleNonNegative, Duration: true, RefusalKey: msgValueNotNonNegative,
					inForce: func(c config.Config) int64 { return c.RetentionSeconds }},
				{Key: config.KeyAggregateMode, LabelKey: msgLabelSiteNames,
					Rule: ruleAggregateMode, RefusalKey: msgAggregateModeUnknown},
			}},
	}
}

// polledKind is a kind read on an interval but not sized: there is no page to size,
// so its card carries the interval and the selections and no measurement.
type polledKind struct {
	// Kind is the registry kind.
	Kind string
	// IntervalKey is the setting row holding its interval.
	IntervalKey string
	// interval reads the interval in force.
	interval func(config.Config) time.Duration
	// After are the cards it follows.
	After []string
}

// polledKinds are the kinds polled and not sized. A kind is drawn only when the
// provider table holds it, and its implementations are read from that table.
func polledKinds() []polledKind {
	return []polledKind{
		{Kind: "resolver_cache", IntervalKey: config.KeyResolverCacheInterval,
			interval: func(c config.Config) time.Duration { return c.ResolverCacheInterval },
			After:    []string{"dns_lookup"}},
		{Kind: "resolver_local_data", IntervalKey: config.KeyResolverLocalDataInterval,
			interval: func(c config.Config) time.Duration { return c.ResolverLocalDataInterval },
			After:    []string{"resolver_cache", "dns_lookup"}},
	}
}

// settingsCard is a card on the collection surface that is not a sized kind's: a
// polled kind's, or a group of settings.
type settingsCard struct {
	// ID names the card's anchors and its button.
	ID string
	// LabelKey is its heading.
	LabelKey messageKey
	// Availability is a polled kind's implementations, each with its availability.
	Availability []figureRow
	// Implementations are a polled kind's selections; Fields the card's own fields.
	Implementations []implementationRow
	Fields          []web.SettingField
	// NoticeKey confirms a save in this card; Edited says it is drawn with values a
	// submission carried that are not stored.
	NoticeKey messageKey
	Edited    bool
	// Confirmation is a shorter retention waiting for its confirmation; while it is
	// set the card draws it in place of its fields.
	Confirmation *retentionConfirmation
	// FieldCard, FieldConfirm and FieldCancel are the names of the card's buttons and
	// of the horizon its confirmation carries.
	FieldCard    string
	FieldConfirm string
	FieldCancel  string
}

// retentionConfirmation is a shorter retention horizon waiting to be confirmed.
type retentionConfirmation struct {
	// Now is the horizon in force and Requested the one asked for.
	Now       web.InForceValue
	Requested web.InForceValue
	// Changes are the card's other settings whose value the save changes, each shown
	// with its value in force and the one asked for, so the confirmation shows
	// everything it applies.
	Changes []pendingChange
	// Pending are the card's values as they will be saved, carried in the form and
	// not editable; Value is the horizon, in seconds, the confirmation is for.
	Pending []hiddenValue
	Value   string
}

// pendingChange is one setting a confirmation applies beside the retention horizon.
type pendingChange struct {
	LabelKey  messageKey
	Now       web.InForceValue
	Requested web.InForceValue
}

// hiddenValue is one value a form carries without showing a field for it.
type hiddenValue struct {
	Name  string
	Value string
}

// collectionEntry is one card of the collection surface, in page order.
type collectionEntry struct {
	Sizing   *collectionCard
	Settings *settingsCard
}

// indexLink is one entry of the collection surface's index: a card's anchor and its
// heading.
type indexLink struct {
	Anchor   string
	LabelKey messageKey
}

// settingsFeedback is what one settings card has to say after a submission.
type settingsFeedback struct {
	card   string
	notice messageKey
	// confirm is a shorter retention waiting for its confirmation, and pending the
	// card's values, in seconds, as they would be saved.
	confirm bool
	pending map[string]string
	// refused names the field refused and refusal why.
	refused string
	refusal messageKey
}

// isPolledKind says whether a card id is a polled kind's.
func isPolledKind(card string) bool {
	for _, kind := range polledKinds() {
		if kind.Kind == card {
			return true
		}
	}
	return false
}

// isSettingCard says whether a card id is a group of settings.
func isSettingCard(card string) bool {
	for _, spec := range settingCardSpecs() {
		if spec.ID == card {
			return true
		}
	}
	return false
}

// fillCollectionEntries orders the collection surface's cards: the leading settings
// cards, then the sized kinds in registry order with every other settings card placed
// after its anchor; and the index of them all.
func (s *surface) fillCollectionEntries(ctx context.Context, built *view, page *collectionPage) error {
	settings, err := s.Configuration(ctx)
	if err != nil {
		return err
	}
	registered, err := s.store.KindsInProviderTable(ctx)
	if err != nil {
		return err
	}
	var entries []collectionEntry
	for index := range built.CollectionCards {
		entries = append(entries, collectionEntry{Sizing: &built.CollectionCards[index]})
	}

	var extras []*settingsCard
	var anchors [][]string
	var leading []collectionEntry
	for _, kind := range polledKinds() {
		if _, present := registered[kind.Kind]; !present {
			continue
		}
		card, err := s.polledKindCard(ctx, kind, settings, page)
		if err != nil {
			return err
		}
		extras = append(extras, card)
		anchors = append(anchors, kind.After)
	}
	for _, spec := range settingCardSpecs() {
		card := settingCardOf(spec, settings, page)
		if spec.Leading {
			leading = append(leading, collectionEntry{Settings: card})
			continue
		}
		extras = append(extras, card)
		anchors = append(anchors, spec.After)
	}

	var trailing []collectionEntry
	for index, card := range extras {
		placed := false
		for _, anchor := range anchors[index] {
			if at := entryIndex(entries, anchor); at >= 0 {
				entries = append(entries[:at+1], append([]collectionEntry{{Settings: card}}, entries[at+1:]...)...)
				placed = true
				break
			}
		}
		if !placed {
			trailing = append(trailing, collectionEntry{Settings: card})
		}
	}
	built.CollectionEntries = append(append(leading, entries...), trailing...)
	built.CollectionIndex = nil
	for _, entry := range built.CollectionEntries {
		switch {
		case entry.Sizing != nil:
			built.CollectionIndex = append(built.CollectionIndex,
				indexLink{Anchor: "card_" + entry.Sizing.Kind, LabelKey: entry.Sizing.LabelKey})
		case entry.Settings != nil:
			built.CollectionIndex = append(built.CollectionIndex,
				indexLink{Anchor: "card_" + entry.Settings.ID, LabelKey: entry.Settings.LabelKey})
		}
	}
	return nil
}

// entryIndex is the position of the card named id, or -1.
func entryIndex(entries []collectionEntry, id string) int {
	for index, entry := range entries {
		if (entry.Sizing != nil && entry.Sizing.Kind == id) ||
			(entry.Settings != nil && entry.Settings.ID == id) {
			return index
		}
	}
	return -1
}

// polledKindCard builds a polled kind's card: each implementation's availability, the
// selections and the interval field. It has no measurement, no suggestion and no fill
// control: nothing sizes these reads.
func (s *surface) polledKindCard(ctx context.Context, kind polledKind, settings config.Config,
	page *collectionPage) (*settingsCard, error) {
	card := &settingsCard{
		ID: kind.Kind, LabelKey: web.KindLabelKey(kind.Kind),
		FieldCard: fieldCard, FieldConfirm: fieldConfirm, FieldCancel: fieldCancel,
	}
	rows, err := s.store.SourceAvailability(ctx)
	if err != nil {
		return nil, err
	}
	intervalName := cardFieldName(kind.Kind, fieldInterval)
	names := []string{intervalName, web.UnitFieldName(intervalName)}
	for _, row := range rows {
		if row.Kind == kind.Kind {
			names = append(names, cardFieldName(kind.Kind, fieldSelectionPrefix+row.ProviderKey))
		}
	}
	typed := page.typed(kind.Kind, names...)
	for _, row := range rows {
		if row.Kind != kind.Kind {
			continue
		}
		availability, _ := web.AvailabilityOf(row)
		card.Availability = append(card.Availability, figureRow{
			LabelKey: web.ProviderLabelKey(row.Kind, row.ProviderKey), StateKey: availability})

		selection, err := config.LoadSourceSelection(ctx, s.store, row.Kind, row.ProviderKey)
		if err != nil {
			return nil, err
		}
		field := cardFieldName(kind.Kind, fieldSelectionPrefix+row.ProviderKey)
		shown := string(selection)
		if value, present := typed[field]; present {
			shown = value
			card.Edited = card.Edited || value != string(selection)
		}
		card.Implementations = append(card.Implementations, implementationRow{
			LabelKey: web.ProviderLabelKey(row.Kind, row.ProviderKey),
			Field:    field,
			Options:  selectionOptions(shown),
		})
	}
	inForce := int64(kind.interval(settings) / time.Second)
	card.Fields = []web.SettingField{web.DurationField(intervalName, fieldInterval+"_"+kind.Kind,
		msgFieldPollInterval, inForce, typed)}
	if figure, present := typed[intervalName]; present &&
		web.DurationDiffers(figure, typed[web.UnitFieldName(intervalName)], inForce) {
		card.Edited = true
	}
	if page != nil && page.settings != nil && page.settings.card == kind.Kind {
		card.NoticeKey = page.settings.notice
		if page.settings.refused != "" {
			markRefused(card, page.settings.refused, page.settings.refusal)
		}
	}
	return card, nil
}

// markRefused marks one field of a card refused, or, for a selection, the select.
func markRefused(card *settingsCard, field string, refusal messageKey) {
	for index := range card.Fields {
		if card.Fields[index].Name == field {
			card.Fields[index].RefusalKey = refusal
		}
	}
	for index := range card.Implementations {
		if card.Implementations[index].Field == field {
			card.Implementations[index].RefusalKey = refusal
		}
	}
}

// retentionValue is a retention horizon as a value: a duration, or unlimited.
func retentionValue(seconds int64) web.InForceValue {
	if seconds == 0 {
		return web.InForceValue{StateKey: msgRetentionUnlimited}
	}
	return web.InForceValue{Parts: web.SpelledDuration(seconds)}
}

// settingValue is one setting's value, in seconds or as the mode, as a value to show.
func settingValue(setting settingSpec, value string) web.InForceValue {
	switch {
	case setting.Rule == ruleAggregateMode:
		return web.InForceValue{StateKey: aggregateModeKey(value)}
	case setting.Key == config.KeyRetentionSeconds:
		seconds, _ := strconv.ParseInt(value, 10, 64)
		return retentionValue(seconds)
	case setting.Duration:
		seconds, _ := strconv.ParseInt(value, 10, 64)
		return web.InForceValue{Parts: web.SpelledDuration(seconds)}
	default:
		return web.InForceValue{Value: value}
	}
}

// settingInForce is one setting's value in force, as its row would hold it.
func settingInForce(setting settingSpec, settings config.Config) string {
	if setting.Rule == ruleAggregateMode {
		return settings.AggregateMode
	}
	return strconv.FormatInt(setting.inForce(settings), 10)
}

// settingCardOf builds one settings card from the configuration in force, with what a
// submission carried in it when it was not stored.
func settingCardOf(spec settingCardSpec, settings config.Config, page *collectionPage) *settingsCard {
	card := &settingsCard{ID: spec.ID, LabelKey: spec.LabelKey,
		FieldCard: fieldCard, FieldConfirm: fieldConfirm, FieldCancel: fieldCancel}
	var names []string
	for _, setting := range spec.Settings {
		names = append(names, setting.Key)
		if setting.Duration {
			names = append(names, web.UnitFieldName(setting.Key))
		}
	}
	typed := page.typed(spec.ID, names...)
	var feedback *settingsFeedback
	if page != nil && page.settings != nil && page.settings.card == spec.ID {
		feedback = page.settings
		card.NoticeKey = feedback.notice
	}
	if feedback != nil && feedback.confirm {
		requested, _ := strconv.ParseInt(feedback.pending[config.KeyRetentionSeconds], 10, 64)
		confirmation := &retentionConfirmation{
			Now:       retentionValue(settings.RetentionSeconds),
			Requested: retentionValue(requested),
			Value:     feedback.pending[config.KeyRetentionSeconds],
		}
		for _, setting := range spec.Settings {
			confirmation.Pending = append(confirmation.Pending,
				hiddenValue{Name: setting.Key, Value: feedback.pending[setting.Key]})
			// EVERYTHING THE CONFIRMATION APPLIES IS SHOWN. The retention horizon is the
			// reason it asks; any other setting of the card the same save changes is
			// listed beside it, in force and asked for, rather than carried unseen.
			if setting.Key == config.KeyRetentionSeconds {
				continue
			}
			if inForce := settingInForce(setting, settings); feedback.pending[setting.Key] != inForce {
				confirmation.Changes = append(confirmation.Changes, pendingChange{
					LabelKey:  setting.LabelKey,
					Now:       settingValue(setting, inForce),
					Requested: settingValue(setting, feedback.pending[setting.Key]),
				})
			}
		}
		card.Confirmation = confirmation
	}
	for _, setting := range spec.Settings {
		var field web.SettingField
		switch {
		case setting.Rule == ruleAggregateMode:
			mode := settings.AggregateMode
			if value, present := typed[setting.Key]; present {
				mode = value
			}
			field = web.SettingField{Name: setting.Key, ID: setting.Key, LabelKey: setting.LabelKey,
				Options: aggregateModeOptions(mode),
				InForce: web.InForceValue{StateKey: aggregateModeKey(settings.AggregateMode)}}
			field.ShowInForce = mode != settings.AggregateMode
			card.Edited = card.Edited || field.ShowInForce
		case setting.Duration:
			value := setting.inForce(settings)
			field = web.DurationField(setting.Key, setting.Key, setting.LabelKey, value, typed)
			if figure, present := typed[setting.Key]; present &&
				web.DurationDiffers(figure, typed[web.UnitFieldName(setting.Key)], value) {
				card.Edited = true
			}
			if setting.Key == config.KeyRetentionSeconds && value == 0 {
				field.InForce = retentionValue(0)
				field.ShowInForce = true
			}
		default:
			value := strconv.FormatInt(setting.inForce(settings), 10)
			field = web.SettingField{Name: setting.Key, ID: setting.Key, LabelKey: setting.LabelKey,
				Value: value, InForce: web.InForceValue{Value: value}}
			if shown, present := typed[setting.Key]; present {
				field.Value = shown
				card.Edited = card.Edited || strings.TrimSpace(shown) != value
			}
			field.ShowInForce = field.Value != value
		}
		card.Fields = append(card.Fields, field)
	}
	if feedback != nil && feedback.refused != "" {
		markRefused(card, feedback.refused, feedback.refusal)
		for index, setting := range spec.Settings {
			if setting.RangeRefusal && card.Fields[index].RefusalKey != "" {
				card.Fields[index].Range = acceptedRangeOf(setting.Key)
			}
		}
	}
	return card
}

// The two site-name modes, as config.Load spells them.
const (
	aggregateModeFull      = "full"
	aggregateModeNoDomains = "no_domains"
)

// aggregateModeOptions are the two modes, named by their consequence.
//
// THE WORDS ARE WHAT THE MODE DOES, AND IT DOES NOT REWRITE HISTORY. no_domains
// withholds site names from what opnview shows; attributions are stored whatever
// the mode (docs/data-model.md, domain_attribution, decision 7), so switching back
// finds the history intact. "Shown" and "Withheld" say that and no more.
func aggregateModeOptions(selected string) []web.ChoiceOption {
	return []web.ChoiceOption{
		{Value: aggregateModeFull, LabelKey: msgSiteNamesShown, Selected: selected == aggregateModeFull},
		{Value: aggregateModeNoDomains, LabelKey: msgSiteNamesWithheld,
			Selected: selected == aggregateModeNoDomains},
	}
}

// aggregateModeKey names the mode in force.
func aggregateModeKey(mode string) messageKey {
	if mode == aggregateModeNoDomains {
		return msgSiteNamesWithheld
	}
	return msgSiteNamesShown
}

// typedSetting reads one typed value by its rule, and then by config.Load. A
// duration's value arrives here already in seconds.
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
	if !web.AcceptedByLoad(ctx, base, map[string]string{spec.Key: typed}) {
		return spec.RefusalKey
	}
	return ""
}

// fieldCancel is the cancel control of a retention confirmation: its value names the
// card, and nothing is stored.
const fieldCancel = "cancel"

// handleCollectionSettingsSubmit saves one settings card -- a polled kind's interval
// and selections, or a group of settings -- or cancels a retention confirmation.
func (s *surface) handleCollectionSettingsSubmit(writer http.ResponseWriter, request *http.Request) {
	draft := postedDraft(request)
	// CANCEL STORES NOTHING, and the card it names is drawn as stored again; every
	// other card keeps what was typed in it.
	if cancelled := request.PostFormValue(fieldCancel); isSettingCard(cancelled) {
		page := &collectionPage{draft: draft, except: cancelled}
		built, err := s.collectionView(writer, request, page, "")
		if err != nil {
			s.FailInternal(writer, request, err)
			return
		}
		if !built.hasUnsavedEdits() {
			http.Redirect(writer, request, PathCollection+"#card_"+cancelled, http.StatusSeeOther)
			return
		}
		if err := s.Render(writer, http.StatusOK, pageCollection, &built); err != nil {
			s.FailInternal(writer, request, err)
		}
		return
	}
	card := request.PostFormValue(fieldCard)
	for _, kind := range polledKinds() {
		if kind.Kind == card {
			s.savePolledKind(writer, request, kind, draft)
			return
		}
	}
	for _, spec := range settingCardSpecs() {
		if spec.ID == card {
			s.saveSettingCard(writer, request, spec, draft)
			return
		}
	}
	// The card is the name a card's own button sends. None is the Enter key's default
	// button; a name that is no card came from somewhere other than the page. Nothing
	// is stored either way, and everything typed is kept.
	refusal := msgCollectionKindUnknown
	if card == "" {
		refusal = msgCardNotNamed
	}
	s.renderCollectionPage(writer, request, http.StatusBadRequest, &collectionPage{draft: draft}, refusal)
}

// savePolledKind stores a polled kind's interval and selections.
func (s *surface) savePolledKind(writer http.ResponseWriter, request *http.Request, kind polledKind,
	draft map[string]string) {
	ctx := request.Context()
	refuse := func(field string, refusal messageKey) {
		s.renderCollectionPage(writer, request, http.StatusBadRequest, &collectionPage{draft: draft,
			settings: &settingsFeedback{card: kind.Kind, refused: field, refusal: refusal}}, "")
	}
	registered, err := s.store.KindsInProviderTable(ctx)
	if err != nil {
		s.FailInternal(writer, request, err)
		return
	}
	if _, present := registered[kind.Kind]; !present {
		s.renderCollectionPage(writer, request, http.StatusBadRequest, &collectionPage{draft: draft},
			msgCollectionKindUnknown)
		return
	}
	providers, err := s.store.ProvidersOfKind(ctx, kind.Kind)
	if err != nil {
		s.FailInternal(writer, request, err)
		return
	}
	intervalName := cardFieldName(kind.Kind, fieldInterval)
	secondsText, refusal := web.TypedDuration(draft[intervalName], draft[web.UnitFieldName(intervalName)])
	if refusal != "" {
		refuse(intervalName, refusal)
		return
	}
	sent := map[string]string{}
	for _, provider := range providers {
		if value, present := draft[cardFieldName(kind.Kind, fieldSelectionPrefix+provider.ProviderKey)]; present {
			sent[provider.ProviderKey] = value
		}
	}
	edit, refusal, refused := checkPolledEdit(providers, secondsText, sent)
	if refusal != "" {
		field := intervalName
		if provider, isSelection := strings.CutPrefix(refused, editFieldSelection); isSelection {
			field = cardFieldName(kind.Kind, fieldSelectionPrefix+provider)
		}
		refuse(field, refusal)
		return
	}
	if err := s.savePolledEdit(ctx, kind, providers, edit); err != nil {
		s.FailInternal(writer, request, err)
		return
	}
	s.finishCollectionSave(writer, request, &collectionPage{draft: draft, except: kind.Kind,
		settings: &settingsFeedback{card: kind.Kind, notice: msgCollectionSaved}}, kind.Kind)
}

// saveSettingCard stores one group of settings, each validated by config.Load.
//
// A SHORTER RETENTION ASKS FOR A CONFIRMATION, AND THE CONFIRMATION IS FOR ONE VALUE.
// The next purge deletes what is older than the new horizon, which cannot be undone
// (ROADMAP.md step 7: a destructive action confirms, or can be undone), so a save of a
// shorter horizon stores nothing and draws the card waiting for its confirmation,
// listing every other change the same save applies. The confirmation carries the
// horizon it was shown, in seconds, and a save is confirmed only when that is the
// horizon it saves: a different one -- typed, or sent by anything other than the
// confirmation as drawn -- is asked about again.
func (s *surface) saveSettingCard(writer http.ResponseWriter, request *http.Request, spec settingCardSpec,
	draft map[string]string) {
	ctx := request.Context()
	refuse := func(field string, refusal messageKey) {
		s.renderCollectionPage(writer, request, http.StatusBadRequest, &collectionPage{draft: draft,
			settings: &settingsFeedback{card: spec.ID, refused: field, refusal: refusal}}, "")
	}
	values, refusal, refused := checkSettingCard(ctx, s.store, spec, func(setting settingSpec) (string, messageKey) {
		seconds := strings.TrimSpace(draft[setting.Key])
		if setting.Duration {
			return web.TypedDuration(seconds, draft[web.UnitFieldName(setting.Key)])
		}
		return seconds, ""
	})
	if refusal != "" {
		refuse(refused, refusal)
		return
	}
	confirm, err := s.needsRetentionConfirmation(ctx, values, request.PostFormValue(fieldConfirm))
	if err != nil {
		s.FailInternal(writer, request, err)
		return
	}
	if confirm {
		s.renderCollectionPage(writer, request, http.StatusOK, &collectionPage{draft: draft,
			settings: &settingsFeedback{card: spec.ID, notice: msgRetentionConfirm, confirm: true,
				pending: values}}, "")
		return
	}
	if err := s.saveSettingValues(ctx, spec, values); err != nil {
		s.FailInternal(writer, request, err)
		return
	}
	s.finishCollectionSave(writer, request, &collectionPage{draft: draft, except: spec.ID,
		settings: &settingsFeedback{card: spec.ID, notice: msgSettingsSaved}}, spec.ID)
}

// shortens says whether a typed retention horizon purges more than the one in force:
// a horizon, where there was none, or a shorter one. 0 is unlimited.
func shortens(inForce int64, typed string) bool {
	horizon, err := strconv.ParseInt(typed, 10, 64)
	if err != nil || horizon == 0 {
		return false
	}
	return inForce == 0 || horizon < inForce
}

// noSettings is a configuration holding no row at all: the defaults.
type noSettings struct{}

// Setting finds nothing.
func (noSettings) Setting(context.Context, string) (string, bool, error) { return "", false, nil }

// acceptedRangeOf asks config.Load which whole numbers of seconds it accepts for one
// setting on its own, and returns the range.
//
// NO BOUND IS WRITTEN HERE. The range is found by asking Load itself -- the lowest
// accepted value from nought up, then the highest, each by bisection over what Load
// accepts -- so a bound config adds, or moves, is the bound this refusal states, with
// no second edit. It assumes, as every rule of Load's does, that what it accepts is
// one unbroken range; nil when it accepts no value at all.
func acceptedRangeOf(key string) *web.AcceptedRange {
	accepts := func(value int64) bool {
		return web.AcceptedByLoad(context.Background(), noSettings{},
			map[string]string{key: strconv.FormatInt(value, 10)})
	}
	low := int64(0)
	if !accepts(low) {
		top := int64(math.MaxInt64)
		if !accepts(top) {
			return nil
		}
		for low+1 < top {
			middle := low + (top-low)/2
			if accepts(middle) {
				top = middle
			} else {
				low = middle
			}
		}
		low = top
	}
	found := &web.AcceptedRange{Low: web.InForceValue{Parts: web.SpelledDuration(low)}}
	high, beyond := low, int64(math.MaxInt64)
	for high+1 < beyond {
		middle := high + (beyond-high)/2
		if accepts(middle) {
			high = middle
		} else {
			beyond = middle
		}
	}
	found.High = web.InForceValue{Parts: web.SpelledDuration(high)}
	return found
}
