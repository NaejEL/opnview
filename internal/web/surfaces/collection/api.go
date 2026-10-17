package collection

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/NaejEL/opnview/internal/web/messages"

	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/sizing"
	"github.com/NaejEL/opnview/internal/store/attribution"
	"github.com/NaejEL/opnview/internal/web"
)

// The collection surface's JSON equivalents (docs/widget-api.md, "Settings API"): every figure the
// page draws -- per sized kind the pair in force, the measurement and the suggestion; per polled
// kind its interval; every collection setting, aggregate_mode included -- and every save of its
// cards, checked and stored by the functions the page uses (save.go). The page's fill and cancel
// controls store nothing and have no JSON route: the suggestion is in the answer.

// The JSON paths.
const (
	// PathAPICollection is every figure of the page.
	PathAPICollection = "/api/collection"
	// PathAPICollectionKind saves one kind's interval, page size and selections.
	PathAPICollectionKind = "/api/collection/kinds/{kind}"
	// PathAPICollectionSettings saves one group of collection settings.
	PathAPICollectionSettings = "/api/collection/settings/{card}"
)

// ConditionRetentionConfirmationRequired answers a save that would shorten the retention horizon
// without confirming the horizon it saves. Nothing is stored.
const ConditionRetentionConfirmationRequired = "retention_confirmation_required"

// apiRoutes are the surface's JSON rows of the route table.
func (s *surface) apiRoutes() []web.Route {
	return []web.Route{
		web.JSONRoute(http.MethodGet, PathAPICollection, s.handleAPICollection),
		web.JSONRoute(http.MethodPost, PathAPICollectionKind, s.handleAPIKind),
		web.JSONRoute(http.MethodPost, PathAPICollectionSettings, s.handleAPISettingCard),
	}
}

// CollectionAnswer is every figure of the collection page.
type CollectionAnswer struct {
	// Kinds are the sized kinds the provider table holds, in the registry's order.
	Kinds []SizedKindState `json:"kinds"`
	// PolledKinds are the kinds read on an interval and not sized.
	PolledKinds []PolledKindState `json:"polled_kinds"`
	// Settings are the groups of collection settings, each with its values in force.
	Settings []SettingCardState `json:"settings"`
}

// SizedKindState is one sized kind's card.
type SizedKindState struct {
	Kind               string                `json:"kind"`
	PageSizeAdjustable bool                  `json:"page_size_adjustable"`
	SourceState        SourceState           `json:"source_state"`
	InForce            InForceState          `json:"in_force"`
	Measurement        MeasurementState      `json:"measurement"`
	Suggestion         SuggestionState       `json:"suggestion"`
	Implementations    []ImplementationState `json:"implementations"`
}

// SourceState is what the firewall reports of a kind, beside the operator's decision.
type SourceState struct {
	// Availability is the kind's condition as the Sources page names it: healthy,
	// not_collecting, unavailable, not_yet_probed or unknown.
	Availability string `json:"availability"`
	// Selection is auto, on or off.
	Selection string `json:"selection"`
}

// InForceState is the pair in force, and the page's ceiling.
type InForceState struct {
	// PageSize is null for a read whose page is not a setting.
	PageSize        *int  `json:"page_size"`
	IntervalSeconds int64 `json:"interval_seconds"`
	// PageCeiling is the largest page the firewall serves, null when it caps none; PageState
	// says whether the page in force is within it, null without a ceiling.
	PageCeiling *int    `json:"page_ceiling"`
	PageState   *string `json:"page_state"`
}

// MeasurementState is the measured record rate and the gaps of its window.
type MeasurementState struct {
	WindowStartAt int64 `json:"window_start_at"`
	WindowEndAt   int64 `json:"window_end_at"`
	// RetentionSeconds is the purge horizon, null for unlimited.
	RetentionSeconds *int64 `json:"retention_seconds"`
	// CoveredFromAt and CoveredToAt bound the records' own span, null when the window holds none.
	CoveredFromAt    *int64     `json:"covered_from_at"`
	CoveredToAt      *int64     `json:"covered_to_at"`
	RecordCount      int64      `json:"record_count"`
	MeanRate         RateState  `json:"mean_rate"`
	IngestedMeanRate RateState  `json:"ingested_mean_rate"`
	PeakRate         RateState  `json:"peak_rate"`
	GapCount         int64      `json:"gap_count"`
	MissedSeconds    int64      `json:"missed_seconds"`
	Gaps             []GapState `json:"gaps"`
}

// RateState is a rate in records per second, or the named state standing in its place:
// no_ingested_clock or rate_not_measured.
type RateState struct {
	PerSecond *float64 `json:"per_second"`
	State     *string  `json:"state"`
}

// GapState is one gap reason of the measured window.
type GapState struct {
	Reason        string `json:"reason"`
	Count         int64  `json:"count"`
	MissedSeconds int64  `json:"missed_seconds"`
}

// SuggestionState is the derivation's outcome and, when it has one, the pair it suggests.
type SuggestionState struct {
	// State is suggestion_available, rate_not_coverable, source_not_collected or
	// not_yet_measured.
	State           string `json:"state"`
	PageSize        *int   `json:"page_size"`
	IntervalSeconds *int64 `json:"interval_seconds"`
}

// ImplementationState is one implementation of a kind with the operator's selection of it, and,
// on a polled kind, its availability.
type ImplementationState struct {
	ProviderKey  string  `json:"provider_key"`
	Selection    string  `json:"selection"`
	Availability *string `json:"availability,omitempty"`
}

// PolledKindState is one polled kind's card.
type PolledKindState struct {
	Kind            string                `json:"kind"`
	IntervalSeconds int64                 `json:"interval_seconds"`
	Implementations []ImplementationState `json:"implementations"`
}

// SettingCardState is one group of settings.
type SettingCardState struct {
	Card     string         `json:"card"`
	Settings []SettingState `json:"settings"`
}

// SettingState is one setting in force: a whole number (seconds for a duration, 0 retention
// being unlimited), or the site-name mode's word.
type SettingState struct {
	Key   string `json:"key"`
	Value any    `json:"value"`
}

// RetentionConfirmation answers a save that would shorten the retention horizon: what is in
// force, what was asked, and every other setting of the card the same save changes.
type RetentionConfirmation struct {
	Condition                 string          `json:"condition"`
	InForceRetentionSeconds   int64           `json:"in_force_retention_seconds"`
	RequestedRetentionSeconds int64           `json:"requested_retention_seconds"`
	Changes                   []PendingChange `json:"changes"`
}

// PendingChange is one other setting a confirmation applies.
type PendingChange struct {
	Key       string `json:"key"`
	InForce   any    `json:"in_force"`
	Requested any    `json:"requested"`
}

// selectionWord is a kind's selection summary as auto, on or off.
func selectionWord(key messageKey) string {
	switch key {
	case msgSelectionOn:
		return string(config.SelectionOn)
	case msgSelectionOff:
		return string(config.SelectionOff)
	}
	return string(config.SelectionAuto)
}

// rateState is a measured rate, or its state, as rateFigure draws it.
func rateState(rate attribution.Rate, clock bool) RateState {
	switch {
	case !clock:
		state := web.ConditionOf(msgNoIngestedClock)
		return RateState{State: &state}
	case !rate.Known:
		state := web.ConditionOf(msgRateNotMeasured)
		return RateState{State: &state}
	}
	value := rate.PerSecond
	return RateState{PerSecond: &value}
}

// collectionAnswer reads every figure of the page from the same reads the page draws from.
func (s *surface) collectionAnswer(ctx context.Context) (CollectionAnswer, error) {
	answer := CollectionAnswer{Kinds: []SizedKindState{}, PolledKinds: []PolledKindState{},
		Settings: []SettingCardState{}}
	settings, err := s.Configuration(ctx)
	if err != nil {
		return answer, err
	}
	registered, err := s.store.KindsInProviderTable(ctx)
	if err != nil {
		return answer, err
	}
	for _, kind := range sizing.Kinds() {
		if _, present := registered[kind.Kind]; !present {
			continue
		}
		state, err := s.sizedKindState(ctx, kind, settings)
		if err != nil {
			return answer, err
		}
		answer.Kinds = append(answer.Kinds, state)
	}
	rows, err := s.store.SourceAvailability(ctx)
	if err != nil {
		return answer, err
	}
	for _, kind := range polledKinds() {
		if _, present := registered[kind.Kind]; !present {
			continue
		}
		polled := PolledKindState{Kind: kind.Kind, IntervalSeconds: int64(kind.interval(settings) / time.Second),
			Implementations: []ImplementationState{}}
		for _, row := range rows {
			if row.Kind != kind.Kind {
				continue
			}
			selection, err := config.LoadSourceSelection(ctx, s.store, row.Kind, row.ProviderKey)
			if err != nil {
				return answer, err
			}
			availability, _ := messages.AvailabilityOf(row)
			code := web.ConditionOf(availability)
			polled.Implementations = append(polled.Implementations, ImplementationState{
				ProviderKey: row.ProviderKey, Selection: string(selection), Availability: &code})
		}
		answer.PolledKinds = append(answer.PolledKinds, polled)
	}
	for _, spec := range settingCardSpecs() {
		card := SettingCardState{Card: spec.ID}
		for _, setting := range spec.Settings {
			card.Settings = append(card.Settings, SettingState{Key: setting.Key,
				Value: settingJSONValue(setting, settingInForce(setting, settings))})
		}
		answer.Settings = append(answer.Settings, card)
	}
	return answer, nil
}

// settingJSONValue is a setting's row value as the answer carries it: the mode's word, or a whole
// number.
func settingJSONValue(setting settingSpec, value string) any {
	if setting.Rule == ruleAggregateMode {
		return value
	}
	number, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return value
	}
	return number
}

// sizedKindState is one sized kind's card in figures.
func (s *surface) sizedKindState(ctx context.Context, kind sizing.Kind, settings config.Config) (SizedKindState, error) {
	measured, suggestion, state, err := s.measureKind(ctx, kind)
	if err != nil {
		return SizedKindState{}, err
	}
	inForce := pairInForce(kind, settings)
	answer := SizedKindState{
		Kind:               kind.Kind,
		PageSizeAdjustable: kind.PageIsAdjustable(),
		SourceState: SourceState{Availability: web.ConditionOf(state.availability),
			Selection: selectionWord(state.selection)},
		InForce: InForceState{IntervalSeconds: int64(inForce.Interval / time.Second)},
		Measurement: MeasurementState{
			WindowStartAt: measured.Window.StartAt, WindowEndAt: measured.Window.EndAt,
			RecordCount: measured.RecordCount,
			MeanRate:    rateState(measured.MeanRate, true),
			IngestedMeanRate: rateState(measured.IngestedMeanRate,
				measured.HasIngestedClock),
			PeakRate:      rateState(measured.PeakRate, true),
			GapCount:      measured.GapCount,
			MissedSeconds: measured.MissedSeconds,
			Gaps:          []GapState{},
		},
		Suggestion:      SuggestionState{State: web.ConditionOf(suggestionStateKey(suggestion.Outcome))},
		Implementations: []ImplementationState{},
	}
	if kind.PageIsAdjustable() {
		pageSize := inForce.PageSize
		answer.InForce.PageSize = &pageSize
		if kind.HasPageCeiling() {
			ceiling := kind.PageCeiling
			pageState := web.ConditionOf(pageStateKey(kind, inForce.PageSize))
			answer.InForce.PageCeiling, answer.InForce.PageState = &ceiling, &pageState
		}
	}
	if measured.RetentionSeconds > 0 {
		retention := measured.RetentionSeconds
		answer.Measurement.RetentionSeconds = &retention
	}
	if measured.HasRecords {
		from, to := measured.CoveredFromAt, measured.CoveredToAt
		answer.Measurement.CoveredFromAt, answer.Measurement.CoveredToAt = &from, &to
	}
	for _, gap := range measured.Gaps {
		answer.Measurement.Gaps = append(answer.Measurement.Gaps,
			GapState{Reason: gap.Reason, Count: gap.Count, MissedSeconds: gap.MissedSeconds})
	}
	if suggestion.Outcome == sizing.OutcomeSuggested {
		interval := int64(suggestion.Pair.Interval / time.Second)
		answer.Suggestion.IntervalSeconds = &interval
		if !suggestion.IntervalOnly {
			pageSize := suggestion.Pair.PageSize
			answer.Suggestion.PageSize = &pageSize
		}
	}
	providers, err := s.store.ProvidersOfKind(ctx, kind.Kind)
	if err != nil {
		return SizedKindState{}, err
	}
	for _, provider := range providers {
		selection, err := config.LoadSourceSelection(ctx, s.store, kind.Kind, provider.ProviderKey)
		if err != nil {
			return SizedKindState{}, err
		}
		answer.Implementations = append(answer.Implementations,
			ImplementationState{ProviderKey: provider.ProviderKey, Selection: string(selection)})
	}
	return answer, nil
}

func (s *surface) handleAPICollection(writer http.ResponseWriter, request *http.Request) {
	s.answerCollection(writer, request)
}

// answerCollection answers with every figure of the page as it now stands.
func (s *surface) answerCollection(writer http.ResponseWriter, request *http.Request) {
	answer, err := s.collectionAnswer(request.Context())
	if err != nil {
		web.FailJSON(writer, request, err)
		return
	}
	web.WriteJSON(writer, http.StatusOK, answer)
}

// readBody decodes a write's body, or refuses it.
func readBody(writer http.ResponseWriter, request *http.Request) (map[string]any, bool) {
	body, ok := web.DecodeJSONObject(request.Body)
	if !ok {
		web.RefuseJSON(writer, http.StatusBadRequest, web.ConditionMalformedBody, "")
	}
	return body, ok
}

// sentSelections are the selections a body carries, as text by provider key.
func sentSelections(body map[string]any) (map[string]string, bool) {
	sent := map[string]string{}
	raw, present := body["selections"]
	if !present || raw == nil {
		return sent, true
	}
	object, ok := raw.(map[string]any)
	if !ok {
		return nil, false
	}
	for provider := range object {
		value, _ := web.JSONText(object, provider)
		sent[provider] = value
	}
	return sent, true
}

// refusedField is the body member an edit's refusal is about.
func refusedField(refused string) string {
	switch refused {
	case editFieldInterval:
		return "interval_seconds"
	case editFieldPageSize:
		return "page_size"
	}
	if provider, isSelection := strings.CutPrefix(refused, editFieldSelection); isSelection {
		return "selections." + provider
	}
	return refused
}

// handleAPIKind saves one kind's edit: {interval_seconds, page_size, selections}. As on the page,
// the interval is required, the page size is required where it is a setting, and a selection the
// body does not carry is left as it is.
func (s *surface) handleAPIKind(writer http.ResponseWriter, request *http.Request) {
	ctx := request.Context()
	body, ok := readBody(writer, request)
	if !ok {
		return
	}
	sent, ok := sentSelections(body)
	if !ok {
		web.RefuseJSON(writer, http.StatusBadRequest, web.ConditionMalformedBody, "selections")
		return
	}
	secondsText, _ := web.JSONText(body, "interval_seconds")
	named := request.PathValue("kind")
	if kind, known := kindNamed(named); known {
		providers, err := s.store.ProvidersOfKind(ctx, kind.Kind)
		if err != nil {
			web.FailJSON(writer, request, err)
			return
		}
		pageText, _ := web.JSONText(body, "page_size")
		edit, refusal, refused := checkKindEdit(kind, providers, secondsText, pageText, sent)
		if refusal != "" {
			web.RefuseJSON(writer, http.StatusBadRequest, web.ConditionOf(refusal), refusedField(refused))
			return
		}
		if err := s.saveKindEdit(ctx, kind, providers, edit); err != nil {
			web.FailJSON(writer, request, err)
			return
		}
		s.answerCollection(writer, request)
		return
	}
	for _, kind := range polledKinds() {
		if kind.Kind != named {
			continue
		}
		registered, err := s.store.KindsInProviderTable(ctx)
		if err != nil {
			web.FailJSON(writer, request, err)
			return
		}
		if _, present := registered[kind.Kind]; !present {
			break
		}
		providers, err := s.store.ProvidersOfKind(ctx, kind.Kind)
		if err != nil {
			web.FailJSON(writer, request, err)
			return
		}
		edit, refusal, refused := checkPolledEdit(providers, secondsText, sent)
		if refusal != "" {
			web.RefuseJSON(writer, http.StatusBadRequest, web.ConditionOf(refusal), refusedField(refused))
			return
		}
		if err := s.savePolledEdit(ctx, kind, providers, edit); err != nil {
			web.FailJSON(writer, request, err)
			return
		}
		s.answerCollection(writer, request)
		return
	}
	web.RefuseJSON(writer, http.StatusBadRequest, web.ConditionOf(msgCollectionKindUnknown), "kind")
}

// handleAPISettingCard saves one group of settings: a member per setting of the card, each a
// whole number (seconds for a duration) or the site-name mode's word, and, for a save shortening
// the retention horizon, "confirm" holding the horizon confirmed. A shorter horizon not confirmed
// stores nothing and answers 409 with what the confirmation is about.
func (s *surface) handleAPISettingCard(writer http.ResponseWriter, request *http.Request) {
	ctx := request.Context()
	body, ok := readBody(writer, request)
	if !ok {
		return
	}
	var spec *settingCardSpec
	for _, candidate := range settingCardSpecs() {
		if candidate.ID == request.PathValue("card") {
			found := candidate
			spec = &found
		}
	}
	if spec == nil {
		web.RefuseJSON(writer, http.StatusBadRequest, web.ConditionOf(msgCollectionKindUnknown), "card")
		return
	}
	values, refusal, refused := checkSettingCard(ctx, s.store, *spec, func(setting settingSpec) (string, messageKey) {
		text, _ := web.JSONText(body, setting.Key)
		text = strings.TrimSpace(text)
		return text, ""
	})
	if refusal != "" {
		web.RefuseJSON(writer, http.StatusBadRequest, web.ConditionOf(refusal), refused)
		return
	}
	confirmed, _ := web.JSONText(body, "confirm")
	confirm, err := s.needsRetentionConfirmation(ctx, values, confirmed)
	if err != nil {
		web.FailJSON(writer, request, err)
		return
	}
	if confirm {
		settings, err := s.Configuration(ctx)
		if err != nil {
			web.FailJSON(writer, request, err)
			return
		}
		requested, _ := strconv.ParseInt(values[config.KeyRetentionSeconds], 10, 64)
		answer := RetentionConfirmation{Condition: ConditionRetentionConfirmationRequired,
			InForceRetentionSeconds: settings.RetentionSeconds, RequestedRetentionSeconds: requested,
			Changes: []PendingChange{}}
		for _, setting := range spec.Settings {
			if setting.Key == config.KeyRetentionSeconds {
				continue
			}
			if inForce := settingInForce(setting, settings); values[setting.Key] != inForce {
				answer.Changes = append(answer.Changes, PendingChange{Key: setting.Key,
					InForce:   settingJSONValue(setting, inForce),
					Requested: settingJSONValue(setting, values[setting.Key])})
			}
		}
		web.WriteJSON(writer, http.StatusConflict, answer)
		return
	}
	if err := s.saveSettingValues(ctx, *spec, values); err != nil {
		web.FailJSON(writer, request, err)
		return
	}
	s.answerCollection(writer, request)
}
