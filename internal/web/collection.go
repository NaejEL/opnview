package web

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/sizing"
	"github.com/NaejEL/opnview/internal/store"
)

// The collection surface: per source kind, the pair that governs its polling, the
// record rate opnview has measured, and the pair it would suggest.
//
// IT READS THE LOCAL DATABASE AND NOTHING ELSE. Every figure on it comes from the
// rows the collectors already stored, through store.MeasureRecordRate; drawing it,
// measuring and filling the fields contact nothing, and saving writes two setting
// rows. Nothing here can reach the firewall.
//
// A SAVE TAKES EFFECT WITHOUT A RESTART. After the rows are written the whole
// configuration is loaded again, through the path start-up uses, and handed to the
// live holder the scheduler reads and to the running collector.
//
// Rebuilt after the loss of 2 October 2026 from the compiled package of that
// evening: the behaviour is the compiled one; the comments are rewritten.

// The form's field names. They are carried into each card rather than spelled in
// the template, so the page that writes them and the handler that reads them
// cannot disagree.
const (
	// fieldKind names the kind a card's form is about.
	fieldKind = "kind"
	// fieldPageSize and fieldInterval carry the pair typed.
	fieldPageSize = "page_size"
	fieldInterval = "interval_seconds"
	// fieldFill is the fill control: present, it asks for the suggestion to be put
	// into the fields, and nothing is stored. Only its presence is read, never its
	// value.
	fieldFill = "fill_suggestion"
	// fieldSelectionPrefix, followed by a provider key, carries the operator's
	// selection of one implementation of the card's kind.
	fieldSelectionPrefix = "selection_"
)

// handleCollectionForm draws the collection surface. Drawing it measures each
// kind from the database and contacts nothing.
func (s *Server) handleCollectionForm(writer http.ResponseWriter, request *http.Request) {
	built, err := s.newView(writer, request, sessionFrom(request), titleKeyFor(pageCollection))
	if err != nil {
		s.failInternal(writer, request, err)
		return
	}
	if err := s.fillCollection(request.Context(), &built, nil, nil); err != nil {
		s.failInternal(writer, request, err)
		return
	}
	if err := s.renderer.render(writer, http.StatusOK, pageCollection, built); err != nil {
		s.failInternal(writer, request, err)
	}
}

// typedPair is what was typed into one card, put back into that card's fields
// when a submission is refused, or the suggestion when the reader asked for it, so
// that nobody has to type a figure twice.
type typedPair struct {
	// kind is the card the pair belongs to.
	kind string
	// pageSize is what the page field shows, as text: what was typed is shown as
	// typed, even when it is not a number, because that is the thing to correct.
	pageSize string
	// intervalSecs is what the interval field shows, as text, in seconds.
	intervalSecs string
	// selections is what each implementation's selection field shows, by provider
	// key; an implementation absent from it shows its stored selection.
	selections map[string]string
}

// cardFeedback is what one card has to say after a submission: a save or a fill
// confirmed, or a figure refused against the field that caused it. It is drawn in
// the card it belongs to and nowhere else: a page with several cards says
// nothing at the top that a reader would have to match to a card, and the other
// cards show what is in force as if nothing had been submitted.
type cardFeedback struct {
	// kind is the card it belongs to.
	kind string
	// notice is a save or a fill confirmed.
	notice messageKey
	// refusal is a figure refused, and field the field that caused it, which the
	// template marks invalid and gives the focus.
	refusal messageKey
	field   string
}

// handleCollectionSubmit stores one card's pair, makes it live, and redraws the
// surface with the save confirmed in that card. With the fill control, it stores
// nothing and puts the suggestion into the fields instead. A refused figure is
// reported in the card, against its field, with what was typed still in it.
func (s *Server) handleCollectionSubmit(writer http.ResponseWriter, request *http.Request) {
	ctx := request.Context()

	kind, known := kindNamed(request.PostFormValue(fieldKind))
	if !known {
		// The kind is the form's own hidden field; a value that names no kind came
		// from somewhere other than a card, and there is no card to say it in.
		s.renderCollectionRefusal(writer, request, msgCollectionKindUnknown, "", nil)
		return
	}

	// THE FILL CONTROL WRITES NOTHING. It is handled before either field is read,
	// so a half-typed card loses nothing by asking for the suggestion, and nothing
	// typed into it is stored by asking.
	if request.PostFormValue(fieldFill) != "" {
		s.fillSuggestedFields(writer, request, kind)
		return
	}

	typed := typedPair{
		kind:         kind.Kind,
		pageSize:     request.PostFormValue(fieldPageSize),
		intervalSecs: request.PostFormValue(fieldInterval),
		selections:   map[string]string{},
	}
	// The selection of each implementation of the kind. A field the form did not
	// carry leaves the stored selection as it is.
	providers, err := s.store.ProvidersOfKind(ctx, kind.Kind)
	if err != nil {
		s.failInternal(writer, request, err)
		return
	}
	for _, provider := range providers {
		if values, sent := request.PostForm[fieldSelectionPrefix+provider.ProviderKey]; sent && len(values) > 0 {
			typed.selections[provider.ProviderKey] = values[0]
		}
	}

	// Every figure and every selection is checked before anything is stored.
	seconds, refusal := typedIntervalSeconds(typed.intervalSecs)
	if refusal != "" {
		s.renderCollectionRefusal(writer, request, refusal, fieldInterval, &typed)
		return
	}
	var pageSize int
	if kind.PageIsAdjustable() {
		pageSize, refusal = typedPageSize(typed.pageSize)
		if refusal != "" {
			s.renderCollectionRefusal(writer, request, refusal, fieldPageSize, &typed)
			return
		}
	}
	selections := make(map[string]config.Selection, len(typed.selections))
	for _, provider := range providers {
		value, sent := typed.selections[provider.ProviderKey]
		if !sent {
			continue
		}
		selection, err := config.ParseSelection(value)
		if err != nil {
			s.renderCollectionRefusal(writer, request, msgCollectionValueInvalid,
				fieldSelectionPrefix+provider.ProviderKey, &typed)
			return
		}
		selections[provider.ProviderKey] = selection
	}

	// A page size above the firewall's ceiling is STORED and reported as above it:
	// refusing it would hide the measurement that shows why it was typed. The
	// interval was bounded above already, by what a duration can hold.
	now := s.now().UTC().Unix()
	if kind.PageIsAdjustable() {
		if err := s.store.SetSetting(ctx, kind.PageSizeKey, strconv.Itoa(pageSize),
			now); err != nil {
			s.failInternal(writer, request, err)
			return
		}
	}
	if err := s.store.SetSetting(ctx, kind.IntervalKey, strconv.FormatInt(seconds, 10),
		now); err != nil {
		s.failInternal(writer, request, err)
		return
	}
	for _, provider := range providers {
		selection, chosen := selections[provider.ProviderKey]
		if !chosen {
			continue
		}
		if err := s.store.SetSetting(ctx, config.KeySourceSelection(kind.Kind, provider.ProviderKey),
			string(selection), now); err != nil {
			s.failInternal(writer, request, err)
			return
		}
	}

	// THE RUNNING SERVICE, through the same path start-up uses. This is what makes
	// a saved pair reach the scheduler and the collectors without a restart. Were
	// it skipped, the rows would be stored and nothing would read them until the
	// service started again.
	if err := s.applyConfiguration(ctx); err != nil {
		s.failInternal(writer, request, err)
		return
	}
	// The selections are applied at once, from what the last probe round found,
	// without contacting the firewall: a source turned on or off is read, or stops
	// being read, from the next pass.
	if s.collector != nil {
		if err := s.collector.Reselect(ctx, kind.Kind); err != nil {
			s.failInternal(writer, request, err)
			return
		}
	}

	built, err := s.newView(writer, request, sessionFrom(request), titleKeyFor(pageCollection))
	if err != nil {
		s.failInternal(writer, request, err)
		return
	}
	// The save is confirmed in the card it was saved in, and the fields show what
	// is now in force, read back from the rows just written rather than echoed from
	// the form.
	feedback := cardFeedback{kind: kind.Kind, notice: msgCollectionSaved}
	if err := s.fillCollection(ctx, &built, nil, &feedback); err != nil {
		s.failInternal(writer, request, err)
		return
	}
	if err := s.renderer.render(writer, http.StatusOK, pageCollection, built); err != nil {
		s.failInternal(writer, request, err)
	}
}

// applyConfiguration loads the configuration again and hands it to whatever is
// running: the live holder the scheduler reads its intervals from, and the
// collector that reads its page sizes. Either may be absent, in a test or before
// anything runs; the rows are stored either way, and the next start reads them.
//
// IT LOADS RATHER THAN PATCHES, the way start-up loads, so a running service and
// a restarted one cannot disagree about what is in force.
func (s *Server) applyConfiguration(ctx context.Context) error {
	settings, err := config.Load(ctx, s.store)
	if err != nil {
		return err
	}
	if s.settings != nil {
		s.settings.Set(settings)
	}
	if s.collector != nil {
		s.collector.Configure(settings)
	}
	return nil
}

// fillSuggestedFields redraws the surface with one kind's suggestion in its fields,
// and STORES NOTHING: the save beside the fields is the second deliberate act.
func (s *Server) fillSuggestedFields(writer http.ResponseWriter, request *http.Request,
	kind sizing.Kind) {
	ctx := request.Context()
	built, err := s.newView(writer, request, sessionFrom(request), titleKeyFor(pageCollection))
	if err != nil {
		s.failInternal(writer, request, err)
		return
	}
	// Measured again: the suggestion shown is the one the database supports now.
	_, suggestion, _, err := s.measureKind(ctx, kind)
	if err != nil {
		s.failInternal(writer, request, err)
		return
	}

	// A suggestion is put into the fields only when there is a pair: the outcomes
	// that carry none — not yet measured, not coverable, not collected — leave the
	// fields as they are and say so in the card. The fill control is disabled in
	// those states, so this is a request that did not come from the page as drawn,
	// or a state that changed between drawing and pressing. For a read whose page
	// is not a setting, only the interval is filled.
	var typed *typedPair
	feedback := &cardFeedback{kind: kind.Kind, notice: msgSuggestionNotFilled}
	if suggestion.Outcome == sizing.OutcomeSuggested {
		typed = &typedPair{
			kind:         kind.Kind,
			intervalSecs: strconv.FormatInt(int64(suggestion.Pair.Interval/time.Second), 10),
		}
		if kind.PageIsAdjustable() {
			typed.pageSize = strconv.Itoa(suggestion.Pair.PageSize)
		}
		// A reader may still change either figure before saving.
		feedback.notice = msgSuggestionFilled
	}

	if err := s.fillCollection(ctx, &built, typed, feedback); err != nil {
		s.failInternal(writer, request, err)
		return
	}
	if err := s.renderer.render(writer, http.StatusOK, pageCollection, built); err != nil {
		s.failInternal(writer, request, err)
	}
}

// renderCollectionRefusal redraws the surface with a refusal and nothing stored.
//
// A REFUSAL IS DRAWN AGAINST THE FIELD THAT CAUSED IT, in the card it belongs to,
// and what was typed is put back so the figure can be corrected rather than typed
// again. A refusal that belongs to no field of any card — a kind the form does not
// know — is the page's own.
//
// The status is 400: the request was refused, and a refusal is not an HTTP 200
// with a sad sentence on it.
func (s *Server) renderCollectionRefusal(writer http.ResponseWriter, request *http.Request,
	key messageKey, field string, typed *typedPair) {
	built, err := s.newView(writer, request, sessionFrom(request), titleKeyFor(pageCollection))
	if err != nil {
		s.failInternal(writer, request, err)
		return
	}

	var feedback *cardFeedback
	if field != "" && typed != nil {
		feedback = &cardFeedback{kind: typed.kind, refusal: key, field: field}
	} else {
		built.Error = key
	}
	if err := s.fillCollection(request.Context(), &built, typed, feedback); err != nil {
		s.failInternal(writer, request, err)
		return
	}
	if err := s.renderer.render(writer, http.StatusBadRequest, pageCollection, built); err != nil {
		s.failInternal(writer, request, err)
	}
}

// kindNamed returns the registry entry of the kind a form names.
func kindNamed(name string) (sizing.Kind, bool) {
	for _, kind := range sizing.Kinds() {
		if kind.Kind == name {
			return kind, true
		}
	}
	return sizing.Kind{}, false
}

// typedPageSize reads a typed page size. A page is a positive count of records;
// anything else is refused, against the page field.
//
// A PAGE ABOVE THE CEILING IS NOT REFUSED HERE. The ceiling is the firewall's, the
// setting is the operator's, and a page above it is stored and reported as above
// the ceiling on the card, beside the ceiling it exceeds, rather than refused
// where nobody would see why.
//
// The returned key is empty when the figure is accepted, and names the refusal
// otherwise.
func typedPageSize(typed string) (int, messageKey) {
	pageSize, err := strconv.Atoi(typed)
	if err != nil || pageSize <= 0 {
		return 0, msgCollectionValueInvalid
	}
	return pageSize, ""
}

// typedIntervalSeconds reads a typed interval in whole seconds. An interval is a
// positive number of seconds that a duration can hold; a number too large for
// one is refused as such rather than as "not a number", because it IS a number.
//
// No floor is applied here. The shortest interval a SUGGESTION may carry is
// internal/sizing's rule; an operator may type a shorter one, and Load accepts
// any positive interval. The ceiling is config.MaxIntervalSeconds, the largest
// whole number of seconds a time.Duration can hold, which is also the bound Load
// applies to a stored row, so a figure accepted here is one Load will read
// back.
func typedIntervalSeconds(typed string) (int64, messageKey) {
	seconds, err := strconv.ParseInt(typed, 10, 64)
	if err != nil || seconds <= 0 {
		return 0, msgCollectionValueInvalid
	}
	if seconds > config.MaxIntervalSeconds {
		return 0, msgCollectionIntervalTooLarge
	}
	return seconds, ""
}

// fillCollection puts one card per kind into a view: every kind internal/sizing
// sizes that the provider table holds a row for, in the registry's order. A kind
// with no provider row produces no card at all.
//
// typed and feedback are for the card they name and are ignored by every other.
func (s *Server) fillCollection(ctx context.Context, built *view, typed *typedPair,
	feedback *cardFeedback) error {
	registered, err := s.store.KindsInProviderTable(ctx)
	if err != nil {
		return err
	}
	built.CollectionPath = PathCollection

	for _, kind := range sizing.Kinds() {
		if _, present := registered[kind.Kind]; !present {
			continue
		}
		card, err := s.buildCollectionCard(ctx, kind, typed, feedback)
		if err != nil {
			return err
		}
		built.CollectionCards = append(built.CollectionCards, card)
	}
	return nil
}

// measureKind measures one kind over the window the retention horizon allows,
// reports whether opnview collects it, and derives the suggestion from both. It
// reads the database only. A measurement taken before a later step failed is
// returned with the error, for a caller that can still use it.
func (s *Server) measureKind(ctx context.Context, kind sizing.Kind) (store.RecordRateMeasurement,
	sizing.Suggestion, kindState, error) {
	settings, err := s.configuration(ctx)
	if err != nil {
		return store.RecordRateMeasurement{}, sizing.Suggestion{}, kindState{}, err
	}
	window := sizing.Window(s.now(), settings.RetentionSeconds)
	measured, err := s.store.MeasureRecordRate(ctx, kind.Spec, window, sizing.BucketSeconds)
	if err != nil {
		return measured, sizing.Suggestion{}, kindState{}, err
	}
	state, err := s.sourceState(ctx, kind.Kind)
	if err != nil {
		return measured, sizing.Suggestion{}, kindState{}, err
	}
	suggestion := sizing.Suggest(kind, measured, pairInForce(kind, settings), state.collected)
	return measured, suggestion, state, nil
}

// configuration is the configuration in force: the live holder's, when the
// service runs one, or the database's. The live holder is what the scheduler and
// the collectors read, so the surface shows the pair they are polling at; a
// server built without one, as in a test, reads the rows the same way start-up
// does.
func (s *Server) configuration(ctx context.Context) (config.Config, error) {
	if s.settings != nil {
		return s.settings.Get(), nil
	}
	return config.Load(ctx, s.store)
}

// pairInForce is the pair one kind is polled at under a configuration.
func pairInForce(kind sizing.Kind, settings config.Config) sizing.Pair {
	switch kind.Kind {
	case "firewall_log":
		return sizing.Pair{PageSize: settings.FirewallLogPageSize,
			Interval: settings.FirewallLogInterval}
	case "security_event":
		return sizing.Pair{PageSize: settings.SecurityEventPageSize,
			Interval: settings.SecurityEventInterval}
	case "dhcp_lease":
		return sizing.Pair{PageSize: settings.DHCPLeasePageSize,
			Interval: settings.DHCPLeaseInterval}
	case "dns_lookup":
		return sizing.Pair{Interval: settings.DNSLookupInterval}
	}
	return sizing.Pair{}
}

// kindState is what the collection surface says about one kind's source, beside
// its figures.
type kindState struct {
	// availability is the firewall's report, as a state.
	availability messageKey
	// selection is the operator's decision, as a state.
	selection messageKey
	// collected says whether opnview reads the kind: some implementation is not
	// turned off, and some implementation is reachable. A kind not collected gets
	// no suggestion.
	collected bool
}

// sourceState summarises a kind's implementations for its card: what the firewall
// reports, what the operator decided, and whether opnview reads the kind at all.
//
// THE SELECTION IS NOT THE AVAILABILITY. They are two rows because they are two
// facts, and a surface that collapsed them would hide an operator's own decision
// behind a probe result.
//
// The kind is reachable when any implementation is; failing that, disabled when
// any is installed and switched off; failing that, unavailable. Its selection is
// off when every implementation is turned off, on when any is turned on, and auto
// otherwise. A selection that cannot be read is an error, as everywhere.
func (s *Server) sourceState(ctx context.Context, kind string) (kindState, error) {
	providers, err := s.store.ProvidersOfKind(ctx, kind)
	if err != nil {
		return kindState{}, err
	}
	var reachable, disabled, anyOn, anyNotOff bool
	for _, provider := range providers {
		selection, err := config.LoadSourceSelection(ctx, s.store, kind, provider.ProviderKey)
		if err != nil {
			return kindState{}, err
		}
		switch provider.State {
		case store.StateReachable:
			reachable = true
		case store.StatePresentButDisabled:
			disabled = true
		}
		if selection == config.SelectionOff {
			continue
		}
		anyNotOff = true
		anyOn = selection == config.SelectionOn || anyOn
	}

	// A kind with no implementation has none that is not turned off, and reads as
	// off: there is nothing to collect it from.
	state := kindState{selection: msgSelectionAuto}
	switch {
	case !anyNotOff:
		state.selection = msgSelectionOff
	case anyOn:
		state.selection = msgSelectionOn
	}
	switch {
	case reachable:
		state.availability = msgSourceReachable
	case disabled:
		state.availability = msgSourceDisabled
	default:
		state.availability = msgSourceUnavailable
	}
	state.collected = anyNotOff && reachable
	return state, nil
}

// buildCollectionCard builds one kind's card: the pair in force, the measurement,
// the gaps of the measured window, and the suggestion, each as a value or a state.
func (s *Server) buildCollectionCard(ctx context.Context, kind sizing.Kind, typed *typedPair,
	feedback *cardFeedback) (collectionCard, error) {
	settings, err := s.configuration(ctx)
	if err != nil {
		return collectionCard{}, err
	}
	measured, suggestion, state, err := s.measureKind(ctx, kind)
	if err != nil {
		return collectionCard{}, err
	}
	inForce := pairInForce(kind, settings)

	card := collectionCard{
		Kind:             kind.Kind,
		LabelKey:         messageKey(kind.LabelKey),
		PageIsAdjustable: kind.PageIsAdjustable(),
		PageSizeField:    strconv.Itoa(inForce.PageSize),
		IntervalField:    wholeSeconds(inForce.Interval),

		// The outcome of the derivation, as a state, whatever it is; the fill
		// control beside the fields is offered only when the outcome carries a
		// pair to fill them with.
		SuggestionStateKey: suggestionStateKey(suggestion.Outcome),
		FieldKind:          fieldKind,
		FieldPageSize:      fieldPageSize,
		FieldInterval:      fieldInterval,
		FieldFill:          fieldFill,
		SuggestionExists:   suggestion.Outcome == sizing.OutcomeSuggested,
	}
	// What this card has to say, if the submission was about it, and nothing
	// otherwise: feedback is drawn in the card it belongs to.
	if feedback != nil && feedback.kind == kind.Kind {
		card.NoticeKey = feedback.notice
		card.RefusalKey = feedback.refusal
		card.InvalidField = feedback.field
	}
	if !kind.PageIsAdjustable() {
		card.PageSizeField = ""
	}
	// What was typed, or filled, in this card replaces what is in force, so a
	// refused figure can be corrected rather than typed again.
	if typed != nil && typed.kind == kind.Kind {
		card.IntervalField = typed.intervalSecs
		if kind.PageIsAdjustable() {
			card.PageSizeField = typed.pageSize
		}
	}

	// THE PAIR IN FORCE, beside what the firewall reports and what the operator
	// decided. The page size, its ceiling and whether it is within it are rows only
	// for a read that takes a page size; the card says the page is not a setting
	// where its field would be. The ceiling is a property of the API, recorded in
	// the survey section the kind cites; a page above it is shown as such rather
	// than refused.
	//
	// THE MEASUREMENT follows, every figure it carries, each as a value or as the
	// state that stands in its place: the window, the retention horizon, the span
	// the records cover, the record count, the three rates, and the gaps with the
	// seconds they cost.
	card.InForce = []figureRow{
		{LabelKey: msgLabelSourceState, StateKey: state.availability},
		{LabelKey: msgLabelSelection, StateKey: state.selection},
	}
	if kind.PageIsAdjustable() {
		card.InForce = append(card.InForce, figureRow{
			LabelKey: msgLabelPageSizeInForce,
			Value:    strconv.Itoa(inForce.PageSize)})
	}
	card.InForce = append(card.InForce, figureRow{
		LabelKey: msgLabelIntervalInForce, Value: wholeSeconds(inForce.Interval)})
	if kind.PageIsAdjustable() {
		card.InForce = append(card.InForce, ceilingFigure(kind))
		if kind.HasPageCeiling() {
			card.InForce = append(card.InForce, figureRow{
				LabelKey: msgLabelPageState,
				StateKey: pageStateKey(kind, inForce.PageSize)})
		}
	}

	card.Measurement = []figureRow{
		{LabelKey: msgLabelWindowStart, Value: instant(measured.Window.StartAt)},
		{LabelKey: msgLabelWindowEnd, Value: instant(measured.Window.EndAt)},
		retentionFigure(measured.RetentionSeconds),
		coveredFigure(msgLabelCoveredFrom, measured, measured.CoveredFromAt),
		coveredFigure(msgLabelCoveredTo, measured, measured.CoveredToAt),
		{LabelKey: msgLabelRecordCount, Value: strconv.FormatInt(measured.RecordCount, 10)},
		rateFigure(msgLabelMeanRateSource, measured.MeanRate, true),
		rateFigure(msgLabelMeanRateIngested, measured.IngestedMeanRate, measured.HasIngestedClock),
		rateFigure(msgLabelPeakRate, measured.PeakRate, true),
		{LabelKey: msgLabelGapCount, Value: strconv.FormatInt(measured.GapCount, 10)},
		{LabelKey: msgLabelMissedSeconds, Value: strconv.FormatInt(measured.MissedSeconds, 10)},
	}
	for _, gap := range measured.Gaps {
		card.Gaps = append(card.Gaps, gapRow{
			ReasonKey:     messageKey("gap_reason." + gap.Reason),
			Count:         strconv.FormatInt(gap.Count, 10),
			MissedSeconds: strconv.FormatInt(gap.MissedSeconds, 10),
		})
	}

	// THE OPERATOR'S SELECTION of each implementation, as stored, or as typed into
	// a refused form for this card.
	providers, err := s.store.ProvidersOfKind(ctx, kind.Kind)
	if err != nil {
		return collectionCard{}, err
	}
	for _, provider := range providers {
		selection, err := config.LoadSourceSelection(ctx, s.store, kind.Kind, provider.ProviderKey)
		if err != nil {
			return collectionCard{}, err
		}
		shown := string(selection)
		if typed != nil && typed.kind == kind.Kind {
			if value, present := typed.selections[provider.ProviderKey]; present {
				shown = value
			}
		}
		card.Implementations = append(card.Implementations, implementationRow{
			LabelKey: messageKey("provider." + kind.Kind + "." + provider.ProviderKey),
			Field:    fieldSelectionPrefix + provider.ProviderKey,
			Options:  selectionOptions(shown),
		})
	}

	// THE SUGGESTION, under its own heading. The pair is shown only when there is
	// one; the state is shown always, so an outcome that carries no pair says why.
	//
	// The page suggested and the page in force are two separately labelled rows
	// under two headings, so a proposal can never be read as a setting. For a read
	// whose page is not a setting, only the interval is suggested.
	//
	// Nothing here is stored: a suggestion becomes the pair in force only when the
	// operator fills the fields with it and saves.
	if suggestion.Outcome == sizing.OutcomeSuggested {
		if !suggestion.IntervalOnly {
			card.Suggestion = append(card.Suggestion, figureRow{
				LabelKey: msgLabelSuggestedPageSize,
				Value:    strconv.Itoa(suggestion.Pair.PageSize)})
		}
		card.Suggestion = append(card.Suggestion, figureRow{
			LabelKey: msgLabelSuggestedInterval,
			Value:    wholeSeconds(suggestion.Pair.Interval)})
	}
	card.Suggestion = append(card.Suggestion, figureRow{
		LabelKey: msgLabelSuggestionState,
		StateKey: suggestionStateKey(suggestion.Outcome)})
	return card, nil
}

// ceilingFigure is the row naming a kind's page ceiling, or the state saying it has
// none.
func ceilingFigure(kind sizing.Kind) figureRow {
	if !kind.HasPageCeiling() {
		return figureRow{LabelKey: msgLabelPageCeiling, StateKey: msgNoPageCeiling}
	}
	return figureRow{LabelKey: msgLabelPageCeiling, Value: strconv.Itoa(kind.PageCeiling)}
}

// retentionFigure is the row naming the retention horizon in seconds, or the state
// saying it is unlimited.
func retentionFigure(retentionSeconds int64) figureRow {
	if retentionSeconds <= 0 {
		return figureRow{LabelKey: msgLabelRetentionHorizon, StateKey: msgRetentionUnlimited}
	}
	return figureRow{LabelKey: msgLabelRetentionHorizon,
		Value: strconv.FormatInt(retentionSeconds, 10)}
}

// coveredFigure is one end of the span the records cover, as an instant, or the
// state saying the window holds no record. An empty window has no span, and an
// instant of zero would read as the first second of 1970.
//
// The span is the records' own, by the source's clock: it can start after the
// window does and end before it, which is what the two rows exist to show.
func coveredFigure(label messageKey, measured store.RecordRateMeasurement, at int64) figureRow {
	// No record, no span: both ends say so.
	if !measured.HasRecords {
		return figureRow{LabelKey: label, StateKey: msgNoRecordsInWindow}
	}
	return figureRow{LabelKey: label, Value: instant(at)}
}

// rateFigure is a rate in records per second to three decimals, or a state.
func rateFigure(label messageKey, rate store.Rate, clock bool) figureRow {
	switch {
	case !clock:
		return figureRow{LabelKey: label, StateKey: msgNoIngestedClock}
	case !rate.Known:
		return figureRow{LabelKey: label, StateKey: msgRateNotMeasured}
	}
	return figureRow{LabelKey: label,
		Value: strconv.FormatFloat(rate.PerSecond, 'f', 3, 64)}
}

// The three forms a figure on this surface takes, and nothing else does: a whole
// number, a rate to three decimals, and an instant in RFC 3339, UTC. A test holds
// the rendered pages to them.
//
// A figure that cannot be given in one of the three is not given at all: the row
// carries a state instead, from the catalogue, so a missing clock, an empty window
// or a measurement not yet possible reads as a sentence and never as a zero. The
// catalogue test's exception for figures in the page body covers these three
// forms and no other.

// pageStateKey says whether the page in force is within the ceiling.
func pageStateKey(kind sizing.Kind, pageSize int) messageKey {
	if pageSize > kind.PageCeiling {
		return msgPageAboveCeiling
	}
	return msgPageWithinCeiling
}

// suggestionStateKey is the state naming a derivation's outcome.
func suggestionStateKey(outcome sizing.Outcome) messageKey {
	switch outcome {
	case sizing.OutcomeSuggested:
		return msgSuggestionAvailable
	case sizing.OutcomeRateNotCoverable:
		return msgSuggestionNotCoverable
	case sizing.OutcomeSourceNotCollected:
		return msgSuggestionSourceNotCollected
	default:
		return msgSuggestionNotYetMeasured
	}
}

// instant is an epoch second in RFC 3339, UTC.
func instant(epoch int64) string {
	return time.Unix(epoch, 0).UTC().Format(time.RFC3339)
}

// wholeSeconds is a duration as a whole number of seconds.
func wholeSeconds(d time.Duration) string {
	return strconv.FormatInt(int64(d/time.Second), 10)
}

// selectionOptions are the three selections an implementation can have, with the one
// shown selected. The labels are the states each selection puts it in.
func selectionOptions(shown string) []selectionOption {
	return []selectionOption{
		{Value: string(config.SelectionAuto), LabelKey: msgSelectionAuto,
			Selected: shown == string(config.SelectionAuto)},
		{Value: string(config.SelectionOn), LabelKey: msgSelectionOn,
			Selected: shown == string(config.SelectionOn)},
		{Value: string(config.SelectionOff), LabelKey: msgSelectionOff,
			Selected: shown == string(config.SelectionOff)},
	}
}
