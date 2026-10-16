package collection

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/NaejEL/opnview/internal/web/messages"

	"github.com/NaejEL/opnview/internal/store/attribution"
	"github.com/NaejEL/opnview/internal/store/reads"
	"github.com/NaejEL/opnview/internal/store/vocab"
	"github.com/NaejEL/opnview/internal/web"
	"github.com/NaejEL/opnview/internal/web/settingfields"

	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/sizing"
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
//
// THE WHOLE PAGE IS ONE FORM, so every field is named after its card: a card's id, a
// dot, the field (cardFieldName). A submission carries what is typed in every card,
// each Save names the card it belongs to, and only that card is stored: the others are
// drawn again with what was posted in them, marked edited and not saved, so saving one
// card never silently discards what was typed in another.
const (
	// fieldKind is a sized kind's Save button: its value names the kind.
	fieldKind = "kind"
	// fieldPageSize and fieldInterval, after the card's id, carry the pair typed; the
	// interval is a figure beside its unit, unitFieldName of it.
	fieldPageSize = "page_size"
	fieldInterval = "interval_seconds"
	// fieldFill is the fill control: its value names the kind whose suggestion is to
	// be put into its fields, and nothing is stored.
	fieldFill = "fill_suggestion"
	// fieldSelectionPrefix, followed by a provider key and after the card's id,
	// carries the operator's selection of one implementation of the card's kind.
	fieldSelectionPrefix = "selection_"
)

// cardFieldName is one card's field: the card's id, a dot, the field.
func cardFieldName(card, field string) string { return card + "." + field }

// collectionPage is what one draw of the collection surface has to say: what a
// submission carried, the card drawn as stored rather than as typed, and the feedback
// of the card the submission was about.
type collectionPage struct {
	// draft is the first value of every field the submission carried.
	draft map[string]string
	// except is the card whose fields were just stored, or whose edit was cancelled:
	// it is drawn as stored.
	except string
	// sized and settings are the feedback of a sized kind's card or of a settings card.
	sized    *cardFeedback
	settings *settingsFeedback
}

// postedDraft is the first value of every field a submission carried.
func postedDraft(request *http.Request) map[string]string {
	draft := map[string]string{}
	for name, values := range request.PostForm {
		if len(values) > 0 {
			draft[name] = values[0]
		}
	}
	return draft
}

// typed is what a submission carried in one card's fields, by field name, for every
// card but the one drawn as stored. It is nil when there is nothing.
func (page *collectionPage) typed(card string, names ...string) map[string]string {
	if page == nil || page.draft == nil || card == page.except {
		return nil
	}
	found := map[string]string{}
	for _, name := range names {
		if value, present := page.draft[name]; present {
			found[name] = value
		}
	}
	return found
}

// handleCollectionForm draws the collection surface. Drawing it measures each kind
// from the database and contacts nothing. A save lands here, with the card it was
// made in named by the query, and that card says it was saved.
func (s *surface) handleCollectionForm(writer http.ResponseWriter, request *http.Request) {
	page := &collectionPage{}
	if saved := request.URL.Query().Get(querySaved); saved != "" {
		switch {
		case isSizedKind(saved):
			page.sized = &cardFeedback{kind: saved, notice: msgCollectionSaved}
		case isPolledKind(saved):
			page.settings = &settingsFeedback{card: saved, notice: msgCollectionSaved}
		case isSettingCard(saved):
			page.settings = &settingsFeedback{card: saved, notice: msgSettingsSaved}
		}
	}
	s.renderCollectionPage(writer, request, http.StatusOK, page, "")
}

// cardFeedback is what one sized kind's card has to say after a submission: a save or
// a fill confirmed, or a figure refused against the field that caused it. It is drawn
// in the card it belongs to and nowhere else: a page with several cards says nothing at
// the top that a reader would have to match to a card.
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

// handleCollectionSubmit stores one sized kind's pair and selections, makes them live,
// and sends the browser back to the card. With the fill control, it stores nothing and
// puts the suggestion into the fields instead. A refused figure is reported in the
// card, against its field, with what was typed still in it.
func (s *surface) handleCollectionSubmit(writer http.ResponseWriter, request *http.Request) {
	ctx := request.Context()
	draft := postedDraft(request)

	named := request.PostFormValue(fieldKind)
	fill := request.PostFormValue(fieldFill)
	if fill != "" {
		named = fill
	}
	kind, known := kindNamed(named)
	if !known {
		// The kind is the name a card's own button sends. None is the Enter key's
		// default button; a name that is no kind came from somewhere other than a card.
		// Nothing is stored either way, and everything typed is kept.
		refusal := msgCollectionKindUnknown
		if named == "" {
			refusal = msgCardNotNamed
		}
		s.renderCollectionPage(writer, request, http.StatusBadRequest,
			&collectionPage{draft: draft}, refusal)
		return
	}

	// THE FILL CONTROL WRITES NOTHING. It is handled before any field is read, so a
	// half-typed card loses nothing by asking for the suggestion, and nothing typed
	// into it is stored by asking.
	if fill != "" {
		s.fillSuggestedFields(writer, request, kind, draft)
		return
	}

	pageName := cardFieldName(kind.Kind, fieldPageSize)
	intervalName := cardFieldName(kind.Kind, fieldInterval)
	refuse := func(refusal messageKey, field string) {
		s.renderCollectionPage(writer, request, http.StatusBadRequest, &collectionPage{draft: draft,
			sized: &cardFeedback{kind: kind.Kind, refusal: refusal, field: field}}, "")
	}

	// The selection of each implementation of the kind. A field the form did not
	// carry leaves the stored selection as it is.
	providers, err := s.store.ProvidersOfKind(ctx, kind.Kind)
	if err != nil {
		s.FailInternal(writer, request, err)
		return
	}

	// Every figure and every selection is checked before anything is stored.
	secondsText, refusal := settingfields.TypedDuration(draft[intervalName], draft[settingfields.UnitFieldName(intervalName)])
	if refusal != "" {
		refuse(refusal, intervalName)
		return
	}
	sent := map[string]string{}
	for _, provider := range providers {
		if value, present := draft[cardFieldName(kind.Kind, fieldSelectionPrefix+provider.ProviderKey)]; present {
			sent[provider.ProviderKey] = value
		}
	}
	edit, refusal, refused := checkKindEdit(kind, providers, secondsText, draft[pageName], sent)
	if refusal != "" {
		field := intervalName
		if refused == editFieldPageSize {
			field = pageName
		} else if provider, isSelection := strings.CutPrefix(refused, editFieldSelection); isSelection {
			field = cardFieldName(kind.Kind, fieldSelectionPrefix+provider)
		}
		refuse(refusal, field)
		return
	}
	if err := s.saveKindEdit(ctx, kind, providers, edit); err != nil {
		s.FailInternal(writer, request, err)
		return
	}
	// The saved card shows what is now in force, read back from the rows just written
	// rather than echoed from the form; every other card keeps what was typed in it.
	s.finishCollectionSave(writer, request, &collectionPage{draft: draft, except: kind.Kind,
		sized: &cardFeedback{kind: kind.Kind, notice: msgCollectionSaved}}, kind.Kind)
}

// finishCollectionSave answers a save. When no other card holds an unsaved edit, the
// browser is sent back to the surface at the saved card's anchor, so a reload does not
// post again; when one does, the page is drawn in place, because a redirect would
// discard that edit.
func (s *surface) finishCollectionSave(writer http.ResponseWriter, request *http.Request,
	page *collectionPage, card string) {
	built, err := s.collectionView(writer, request, page, "")
	if err != nil {
		s.FailInternal(writer, request, err)
		return
	}
	if !built.hasUnsavedEdits() {
		http.Redirect(writer, request, collectionSavedLocation(card), http.StatusSeeOther)
		return
	}
	if err := s.Render(writer, http.StatusOK, pageCollection, &built); err != nil {
		s.FailInternal(writer, request, err)
	}
}

// collectionSavedLocation is where a save sends the browser: the surface, naming the
// card saved, at that card's anchor.
func collectionSavedLocation(card string) string {
	return PathCollection + "?" + url.Values{querySaved: {card}}.Encode() + "#card_" + card
}

// fillSuggestedFields redraws the surface with one kind's suggestion in its fields,
// and STORES NOTHING: the save beside the fields is the second deliberate act. Every
// other card keeps what was typed in it.
func (s *surface) fillSuggestedFields(writer http.ResponseWriter, request *http.Request,
	kind sizing.Kind, draft map[string]string) {
	ctx := request.Context()
	// Measured again: the suggestion shown is the one the database supports now.
	_, suggestion, _, err := s.measureKind(ctx, kind)
	if err != nil {
		s.FailInternal(writer, request, err)
		return
	}

	// A suggestion is put into the fields only when there is a pair: the outcomes
	// that carry none — not yet measured, not coverable, not collected — leave the
	// fields as they are and say so in the card. The fill control is disabled in
	// those states, so this is a request that did not come from the page as drawn,
	// or a state that changed between drawing and pressing. For a read whose page
	// is not a setting, only the interval is filled, in the longest unit that holds
	// it exactly, as the field shows every interval.
	feedback := &cardFeedback{kind: kind.Kind, notice: msgSuggestionNotFilled}
	if suggestion.Outcome == sizing.OutcomeSuggested {
		intervalName := cardFieldName(kind.Kind, fieldInterval)
		figure, unit := settingfields.FieldDuration(int64(suggestion.Pair.Interval / time.Second))
		draft[intervalName] = figure
		draft[settingfields.UnitFieldName(intervalName)] = unit
		if kind.PageIsAdjustable() {
			draft[cardFieldName(kind.Kind, fieldPageSize)] = strconv.Itoa(suggestion.Pair.PageSize)
		}
		// A reader may still change either figure before saving.
		feedback.notice = msgSuggestionFilled
	}
	s.renderCollectionPage(writer, request, http.StatusOK,
		&collectionPage{draft: draft, sized: feedback}, "")
}

// renderCollectionPage draws the collection surface at a status, with one card's
// feedback, or a refusal that belongs to no card at the head of the page. A refusal
// answers 400: the request was refused, and a refusal is not an HTTP 200 with a sad
// sentence on it.
func (s *surface) renderCollectionPage(writer http.ResponseWriter, request *http.Request, status int,
	page *collectionPage, pageRefusal messageKey) {
	built, err := s.collectionView(writer, request, page, pageRefusal)
	if err != nil {
		s.FailInternal(writer, request, err)
		return
	}
	if err := s.Render(writer, status, pageCollection, &built); err != nil {
		s.FailInternal(writer, request, err)
	}
}

// collectionView builds the collection surface for one draw.
func (s *surface) collectionView(writer http.ResponseWriter, request *http.Request,
	page *collectionPage, pageRefusal messageKey) (view, error) {
	built, err := s.newView(writer, request, web.SessionFrom(request), msgTitleCollection)
	if err != nil {
		return view{}, err
	}
	if pageRefusal != "" {
		built.Error = pageRefusal
	}
	if err := s.fillCollection(request.Context(), &built, page); err != nil {
		return view{}, err
	}
	return built, nil
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

// isSizedKind says whether a card id is a sized kind's.
func isSizedKind(card string) bool {
	_, known := kindNamed(card)
	return known
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
	pageSize, err := strconv.Atoi(strings.TrimSpace(typed))
	if err != nil || pageSize <= 0 {
		return 0, msgCollectionValueInvalid
	}
	return pageSize, ""
}

// fillCollection puts one card per kind into a view: every kind internal/sizing
// sizes that the provider table holds a row for, in the registry's order, and the
// settings cards placed among them. A kind with no provider row produces no card at
// all. page is what the draw has to say, nil for none.
func (s *surface) fillCollection(ctx context.Context, built *view, page *collectionPage) error {
	registered, err := s.store.KindsInProviderTable(ctx)
	if err != nil {
		return err
	}
	built.CollectionPath = PathCollection
	built.CollectionCards = nil

	for _, kind := range sizing.Kinds() {
		if _, present := registered[kind.Kind]; !present {
			continue
		}
		card, err := s.buildCollectionCard(ctx, kind, page)
		if err != nil {
			return err
		}
		built.CollectionCards = append(built.CollectionCards, card)
	}
	// The settings cards, each after the card it belongs beside; see
	// collection_settings.go.
	return s.fillCollectionEntries(ctx, built, page)
}

// measureKind measures one kind over the window the retention horizon allows,
// reports whether opnview collects it, and derives the suggestion from both. It
// reads the database only. A measurement taken before a later step failed is
// returned with the error, for a caller that can still use it.
func (s *surface) measureKind(ctx context.Context, kind sizing.Kind) (attribution.RecordRateMeasurement,
	sizing.Suggestion, kindState, error) {
	settings, err := s.Configuration(ctx)
	if err != nil {
		return attribution.RecordRateMeasurement{}, sizing.Suggestion{}, kindState{}, err
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
// The kind's availability is kindAvailability's, in the Sources surface's values.
// Its selection is
// off when every implementation is turned off, on when any is turned on, and auto
// otherwise. A selection that cannot be read is an error, as everywhere.
func (s *surface) sourceState(ctx context.Context, kind string) (kindState, error) {
	providers, err := s.store.ProvidersOfKind(ctx, kind)
	if err != nil {
		return kindState{}, err
	}
	var reachable, anyOn, anyNotOff bool
	for _, provider := range providers {
		selection, err := config.LoadSourceSelection(ctx, s.store, kind, provider.ProviderKey)
		if err != nil {
			return kindState{}, err
		}
		if provider.State == vocab.StateReachable {
			reachable = true
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
	// THE AVAILABILITY IS NAMED WITH THE SOURCES SURFACE'S OWN VALUES, so a kind reads
	// the same here as its providers read there: see kindAvailability.
	rows, err := s.store.SourceAvailability(ctx)
	if err != nil {
		return kindState{}, err
	}
	state.availability = kindAvailability(kind, rows)
	state.collected = anyNotOff && reachable
	return state, nil
}

// kindAvailability names a kind's condition from its providers' rows, with the
// values of the Source availability preset: healthy when any provider is reachable;
// failing that, installed and not collecting when any is; failing that, unavailable
// when any was checked and found so; failing that, not yet probed; and state unknown
// when no provider of the kind carries an availability row at all. Nothing but a
// reachable provider reads as healthy.
func kindAvailability(kind string, rows []reads.SourceRow) messageKey {
	found := map[messageKey]bool{}
	for _, row := range rows {
		if row.Kind != kind {
			continue
		}
		availability, _ := messages.AvailabilityOf(row)
		found[availability] = true
	}
	for _, availability := range []messageKey{msgAvailabilityHealthy, msgAvailabilityNotCollecting,
		msgAvailabilityUnavailable, msgAvailabilityNotYetProbed} {
		if found[availability] {
			return availability
		}
	}
	return msgAvailabilityUnknown
}

// buildCollectionCard builds one kind's card: the pair in force, the measurement,
// the gaps of the measured window, and the suggestion, each as a value or a state;
// and its fields, showing what is in force or what a submission carried in them.
func (s *surface) buildCollectionCard(ctx context.Context, kind sizing.Kind,
	page *collectionPage) (collectionCard, error) {
	settings, err := s.Configuration(ctx)
	if err != nil {
		return collectionCard{}, err
	}
	measured, suggestion, state, err := s.measureKind(ctx, kind)
	if err != nil {
		return collectionCard{}, err
	}
	inForce := pairInForce(kind, settings)

	pageName := cardFieldName(kind.Kind, fieldPageSize)
	intervalName := cardFieldName(kind.Kind, fieldInterval)
	selectionNames := []string{}
	providers, err := s.store.ProvidersOfKind(ctx, kind.Kind)
	if err != nil {
		return collectionCard{}, err
	}
	for _, provider := range providers {
		selectionNames = append(selectionNames,
			cardFieldName(kind.Kind, fieldSelectionPrefix+provider.ProviderKey))
	}
	typed := page.typed(kind.Kind,
		append([]string{pageName, intervalName, settingfields.UnitFieldName(intervalName)}, selectionNames...)...)

	inForceSeconds := int64(inForce.Interval / time.Second)
	card := collectionCard{
		Kind:             kind.Kind,
		LabelKey:         messageKey(kind.LabelKey),
		PageIsAdjustable: kind.PageIsAdjustable(),
		Interval: settingfields.DurationField(intervalName, "interval_"+kind.Kind, msgFieldPollInterval,
			inForceSeconds, typed),

		// The outcome of the derivation, as a state, whatever it is; the fill
		// control beside the fields is offered only when the outcome carries a
		// pair to fill them with.
		SuggestionStateKey: suggestionStateKey(suggestion.Outcome),
		FieldKind:          fieldKind,
		FieldFill:          fieldFill,
		SuggestionExists:   suggestion.Outcome == sizing.OutcomeSuggested,
	}
	if _, present := typed[intervalName]; present &&
		settingfields.DurationDiffers(typed[intervalName], typed[settingfields.UnitFieldName(intervalName)], inForceSeconds) {
		card.Edited = true
	}
	if kind.PageIsAdjustable() {
		value := strconv.Itoa(inForce.PageSize)
		card.PageSize = settingfields.SettingField{Name: pageName, ID: "page_size_" + kind.Kind,
			LabelKey: msgFieldPageSize, Value: value, InForce: settingfields.InForceValue{Value: value}}
		if shown, present := typed[pageName]; present {
			card.PageSize.Value = shown
			card.PageSize.ShowInForce = shown != value
			card.Edited = card.Edited || strings.TrimSpace(shown) != value
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
		LabelKey: msgLabelIntervalInForce, Duration: settingfields.SpelledDuration(inForceSeconds)})
	if kind.PageIsAdjustable() {
		card.InForce = append(card.InForce, ceilingFigure(kind))
		if kind.HasPageCeiling() {
			card.InForce = append(card.InForce, figureRow{
				LabelKey: msgLabelPageState,
				StateKey: pageStateKey(kind, inForce.PageSize)})
		}
	}

	card.Measurement = []figureRow{
		{LabelKey: msgLabelWindowStart, Value: settingfields.Instant(measured.Window.StartAt)},
		{LabelKey: msgLabelWindowEnd, Value: settingfields.Instant(measured.Window.EndAt)},
		retentionFigure(measured.RetentionSeconds),
		coveredFigure(msgLabelCoveredFrom, measured, measured.CoveredFromAt),
		coveredFigure(msgLabelCoveredTo, measured, measured.CoveredToAt),
		{LabelKey: msgLabelRecordCount, Value: strconv.FormatInt(measured.RecordCount, 10)},
		rateFigure(msgLabelMeanRateSource, measured.MeanRate, true),
		rateFigure(msgLabelMeanRateIngested, measured.IngestedMeanRate, measured.HasIngestedClock),
		rateFigure(msgLabelPeakRate, measured.PeakRate, true),
		{LabelKey: msgLabelGapCount, Value: strconv.FormatInt(measured.GapCount, 10)},
		{LabelKey: msgLabelMissedSeconds, Duration: settingfields.SpelledDuration(measured.MissedSeconds)},
	}
	for _, gap := range measured.Gaps {
		card.Gaps = append(card.Gaps, gapRow{
			ReasonKey: messageKey("gap_reason." + gap.Reason),
			Count:     strconv.FormatInt(gap.Count, 10),
			Missed:    settingfields.SpelledDuration(gap.MissedSeconds),
		})
	}

	// THE OPERATOR'S SELECTION of each implementation, as stored, or as typed and
	// not stored.
	for index, provider := range providers {
		selection, err := config.LoadSourceSelection(ctx, s.store, kind.Kind, provider.ProviderKey)
		if err != nil {
			return collectionCard{}, err
		}
		shown := string(selection)
		if value, present := typed[selectionNames[index]]; present {
			shown = value
			card.Edited = card.Edited || value != string(selection)
		}
		card.Implementations = append(card.Implementations, implementationRow{
			LabelKey: messages.ProviderLabelKey(kind.Kind, provider.ProviderKey),
			Field:    selectionNames[index],
			Options:  selectionOptions(shown),
		})
	}

	// What this card has to say, if the submission was about it, and nothing
	// otherwise: feedback is drawn in the card it belongs to. A card that speaks for
	// itself is not also marked edited.
	if page != nil && page.sized != nil && page.sized.kind == kind.Kind {
		card.NoticeKey = page.sized.notice
		switch page.sized.field {
		case intervalName:
			card.Interval.RefusalKey = page.sized.refusal
		case pageName:
			card.PageSize.RefusalKey = page.sized.refusal
		default:
			for index := range card.Implementations {
				if card.Implementations[index].Field == page.sized.field {
					card.Implementations[index].RefusalKey = page.sized.refusal
				}
			}
		}
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
			Duration: settingfields.SpelledDuration(int64(suggestion.Pair.Interval / time.Second))})
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
		Duration: settingfields.SpelledDuration(retentionSeconds)}
}

// coveredFigure is one end of the span the records cover, as an instant, or the
// state saying the window holds no record. An empty window has no span, and an
// instant of zero would read as the first second of 1970.
//
// The span is the records' own, by the source's clock: it can start after the
// window does and end before it, which is what the two rows exist to show.
func coveredFigure(label messageKey, measured attribution.RecordRateMeasurement, at int64) figureRow {
	// No record, no span: both ends say so.
	if !measured.HasRecords {
		return figureRow{LabelKey: label, StateKey: msgNoRecordsInWindow}
	}
	return figureRow{LabelKey: label, Value: settingfields.Instant(at)}
}

// rateFigure is a rate in records per second to three decimals, or a state.
func rateFigure(label messageKey, rate attribution.Rate, clock bool) figureRow {
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
