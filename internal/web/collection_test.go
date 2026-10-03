package web

import (
	"context"
	"html"
	"html/template"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/sizing"
	"github.com/NaejEL/opnview/internal/store"
)

// The collection surface.
//
// EVERY FIGURE HERE IS READ FROM ROWS A TEST WROTE, on a clock the test stopped. The
// measurement window ends at the harness clock's instant, so the records each test
// stores are placed in it, or outside it, by arithmetic on that instant rather than
// by the time the test happens to run.

// signedInHarness is a harness with an account, signed in.
func signedInHarness(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t)
	h.completeSetup()
	return h
}

// storeFlows stores one synthesised filter-log record per source instant, each
// ingested five seconds after it. Nothing here is an address or an identifier read
// off a network.
func (h *harness) storeFlows(observedAt ...int64) {
	h.t.Helper()
	batch := randomHex(h.t, 4)
	for index, instant := range observedAt {
		flow := store.Flow{
			LogDigest:            "collection-test-" + batch + "-" + strconv.Itoa(index),
			ObservedAt:           instant,
			IngestedAt:           instant + 5,
			InterfaceDevice:      "example-device",
			InterfaceLookupState: store.LookupResolved,
			SrcAddress:           "example-source",
			DstAddress:           "example-destination",
			Protocol:             "tcp",
			IPVersion:            4,
			Action:               "pass",
			Direction:            "in",
			PacketBytes:          100,
			RuleLookupState:      store.LookupPending,
		}
		if err := h.store.InsertFlow(context.Background(), flow); err != nil {
			h.t.Fatalf("storing a flow observed at %d: %v", instant, err)
		}
	}
}

// storeBurst stores count filter-log records spread over the minute starting at
// from: one through the product's own insert, and the rest copied from it by one
// statement, because sixteen hundred separate inserts cost seconds and prove
// nothing more.
func (h *harness) storeBurst(from int64, count int) {
	h.t.Helper()
	h.storeFlows(from)
	db := h.store.DB()
	var seed string
	if err := db.QueryRow(`SELECT log_digest FROM flow WHERE observed_at = ? ORDER BY id DESC LIMIT 1`,
		from).Scan(&seed); err != nil {
		h.t.Fatalf("reading the seed record back: %v", err)
	}
	var copied, selected []string
	for _, column := range tableColumns(h.t, db, "flow") {
		switch column {
		case "id":
			continue
		case "log_digest":
			selected = append(selected, "log_digest || '-' || n.i")
		case "observed_at":
			selected = append(selected, "observed_at + n.i % 60")
		case "ingested_at":
			selected = append(selected, "ingested_at + n.i % 60")
		default:
			selected = append(selected, `"`+column+`"`)
		}
		copied = append(copied, `"`+column+`"`)
	}
	statement := `WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < ?)
		INSERT INTO flow (` + strings.Join(copied, ", ") + `)
		SELECT ` + strings.Join(selected, ", ") + ` FROM flow, n WHERE log_digest = ?`
	if _, err := db.Exec(statement, count-1, seed); err != nil {
		h.t.Fatalf("copying the seed record: %v", err)
	}
}

// providerID looks up one implementation's registry row.
func (h *harness) providerID(kind, providerKey string) int64 {
	h.t.Helper()
	id, err := h.store.ProviderID(context.Background(), kind, providerKey)
	if err != nil {
		h.t.Fatalf("looking up the %s provider %s: %v", kind, providerKey, err)
	}
	return id
}

// setAvailability records what a probe of one implementation found.
func (h *harness) setAvailability(kind, providerKey string, state store.AvailabilityState) {
	h.t.Helper()
	if err := h.store.SetAvailability(context.Background(), h.providerID(kind, providerKey),
		state, "test-probe", nil, h.clock.Now().Unix()); err != nil {
		h.t.Fatalf("recording %s for %s/%s: %v", state, kind, providerKey, err)
	}
}

// setSelection stores the operator's selection for one implementation.
func (h *harness) setSelection(kind, providerKey string, selection config.Selection) {
	h.t.Helper()
	if err := h.store.SetSetting(context.Background(), config.KeySourceSelection(kind, providerKey),
		string(selection), h.clock.Now().Unix()); err != nil {
		h.t.Fatalf("selecting %s for %s/%s: %v", selection, kind, providerKey, err)
	}
}

// recordGap stores one collection gap of an implementation, detected at an instant.
func (h *harness) recordGap(kind, providerKey string, reason store.GapReason, start, end int64) {
	h.t.Helper()
	if err := h.store.RecordCollectionGap(context.Background(), store.CollectionGap{
		ProviderID: h.providerID(kind, providerKey), IntervalStartAt: start,
		IntervalEndAt: end, Reason: reason, DetectedAt: end,
	}); err != nil {
		h.t.Fatalf("recording a %s gap: %v", reason, err)
	}
}

// setting reads one setting row, failing when it is absent.
func (h *harness) setting(key string) string {
	h.t.Helper()
	value, present, err := h.store.Setting(context.Background(), key)
	if err != nil {
		h.t.Fatalf("reading the setting %s: %v", key, err)
	}
	if !present {
		h.t.Fatalf("the setting %s is absent", key)
	}
	return value
}

// pairForm is what one card's save submits.
func pairForm(kind, pageSize, intervalSeconds string) url.Values {
	return url.Values{
		fieldKind: {kind}, fieldPageSize: {pageSize}, fieldInterval: {intervalSeconds},
	}
}

// fillForm is what one card's fill control submits.
func fillForm(kind string) url.Values {
	return url.Values{fieldKind: {kind}, fieldFill: {"1"}}
}

// cardOf returns one kind's card out of a rendered page, failing when there is
// none.
func cardOf(t *testing.T, page, kind string) string {
	t.Helper()
	start := strings.Index(page, `<section class="card" id="card_`+kind+`"`)
	if start < 0 {
		t.Fatalf("the page has no card for %s", kind)
	}
	end := strings.Index(page[start:], "</section>")
	if end < 0 {
		t.Fatalf("the card for %s is not closed", kind)
	}
	return page[start : start+end]
}

// catalogueText is the source-language string of one key.
func catalogueText(t *testing.T, key messageKey) string {
	t.Helper()
	catalogue, err := LoadCatalogue(SourceLanguage)
	if err != nil {
		t.Fatalf("loading the catalogue: %v", err)
	}
	value, present := catalogue.Strings[string(key)]
	if !present {
		t.Fatalf("the catalogue has no string for %q", key)
	}
	return template.HTMLEscapeString(value)
}

// figureShown says whether a card shows a row labelled by one key carrying one
// figure.
func figureShown(t *testing.T, card string, label messageKey, figure string) bool {
	t.Helper()
	return strings.Contains(card, "<dt>"+catalogueText(t, label)+"</dt>\n<dd><span class=\"figure\">"+
		figure+"</span></dd>")
}

// stateShown says whether a card shows a row labelled by one key carrying one state.
func stateShown(t *testing.T, card string, label, state messageKey) bool {
	t.Helper()
	return strings.Contains(card, "<dt>"+catalogueText(t, label)+"</dt>\n<dd>"+
		catalogueText(t, state)+"</dd>")
}

// fieldValue is what one input of a card holds.
func fieldValue(card, id string) string {
	return html.UnescapeString(attributeAfter(card[max(strings.Index(card, `id="`+id+`"`), 0):], `value="`))
}

// suggestedHarness is a signed-in harness whose filter log is reachable and holds
// enough records, spread over an hour, for a suggestion.
func suggestedHarness(t *testing.T) *harness {
	t.Helper()
	h := signedInHarness(t)
	h.setAvailability("firewall_log", "pf", store.StateReachable)
	now := h.clock.Now().Unix()
	var instants []int64
	for minute := int64(60); minute > 0; minute-- {
		instants = append(instants, now-minute*60, now-minute*60+7)
	}
	h.storeFlows(instants...)
	return h
}

// TestTheCollectionRouteNamesItselfAndLabelsEachCard: one heading names the route,
// and every card is labelled by a heading naming its kind.
func TestTheCollectionRouteNamesItselfAndLabelsEachCard(t *testing.T) {
	h := signedInHarness(t)
	page := pageSource(t, h, PathCollection)

	if count := strings.Count(page, "<h1>"); count != 1 {
		t.Fatalf("the collection surface carries %d top-level headings rather than one", count)
	}
	if !strings.Contains(page, "<h1>"+catalogueText(t, "collection.title")+"</h1>") {
		t.Error("the top-level heading does not name the collection surface")
	}
	for _, kind := range sizing.Kinds() {
		card := cardOf(t, page, kind.Kind)
		if !strings.Contains(card, `aria-labelledby="card_name_`+kind.Kind+`"`) {
			t.Errorf("the %s card is not labelled by its heading", kind.Kind)
		}
		heading := `<h2 id="card_name_` + kind.Kind + `">` +
			catalogueText(t, messageKey(kind.LabelKey)) + "</h2>"
		if !strings.Contains(card, heading) {
			t.Errorf("the %s card has no heading naming its kind", kind.Kind)
		}
	}
}

// TestAKindWithNoProviderRowProducesNoCard: the cards are the kinds the provider
// table holds, read at run time.
func TestAKindWithNoProviderRowProducesNoCard(t *testing.T) {
	h := signedInHarness(t)
	db := h.store.DB()
	for _, statement := range []string{
		`DELETE FROM source_availability WHERE provider_id IN
		    (SELECT id FROM provider WHERE kind = 'security_event')`,
		`DELETE FROM provider WHERE kind = 'security_event'`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("removing the security-event registry rows: %v", err)
		}
	}

	page := pageSource(t, h, PathCollection)
	if strings.Contains(page, `id="card_security_event"`) {
		t.Error("a kind with no provider row still produced a card")
	}
	for _, kind := range []string{"firewall_log", "dhcp_lease", "dns_lookup"} {
		cardOf(t, page, kind)
	}
}

// TestTheCollectionSurfaceReportsEveryFigureItMeasures: every figure the
// measurement carries is on the card, in the form the measurement gives it.
func TestTheCollectionSurfaceReportsEveryFigureItMeasures(t *testing.T) {
	h := suggestedHarness(t)
	now := h.clock.Now().Unix()
	h.recordGap("firewall_log", "pf", store.GapDigestOutsideWindow, now-900, now-840)
	h.recordGap("firewall_log", "pf", store.GapDigestOutsideWindow, now-600, now-570)

	kind, _ := kindNamed("firewall_log")
	measured, err := h.store.MeasureRecordRate(context.Background(), kind.Spec,
		sizing.Window(h.clock.Now(), config.DefaultRetentionSeconds), sizing.BucketSeconds)
	if err != nil {
		t.Fatalf("measuring the filter log: %v", err)
	}
	if !measured.HasRecords || !measured.MeanRate.Known || !measured.IngestedMeanRate.Known {
		t.Fatal("the stored records do not make a measurement with every figure, " +
			"so this assertion is vacuous")
	}

	card := cardOf(t, pageSource(t, h, PathCollection), "firewall_log")
	rate := func(r store.Rate) string { return strconv.FormatFloat(r.PerSecond, 'f', 3, 64) }
	for _, row := range []struct {
		label  messageKey
		figure string
	}{
		{msgLabelWindowStart, time.Unix(now-sizing.NominalWindowSeconds, 0).UTC().Format(time.RFC3339)},
		{msgLabelWindowEnd, time.Unix(now, 0).UTC().Format(time.RFC3339)},
		{msgLabelRetentionHorizon, strconv.FormatInt(config.DefaultRetentionSeconds, 10)},
		{msgLabelCoveredFrom, time.Unix(now-3600, 0).UTC().Format(time.RFC3339)},
		{msgLabelCoveredTo, time.Unix(now-60+7, 0).UTC().Format(time.RFC3339)},
		{msgLabelRecordCount, "120"},
		{msgLabelMeanRateSource, rate(measured.MeanRate)},
		{msgLabelMeanRateIngested, rate(measured.IngestedMeanRate)},
		{msgLabelPeakRate, rate(measured.PeakRate)},
		{msgLabelGapCount, "2"},
		{msgLabelMissedSeconds, "90"},
	} {
		if !figureShown(t, card, row.label, row.figure) {
			t.Errorf("the card does not show %s as %s", row.label, row.figure)
		}
	}
	gap := "<td>" + catalogueText(t, msgGapDigestOutsideWindow) + `</td><td><span class="figure">2</span></td>` +
		`<td><span class="figure">90</span></td>`
	if !strings.Contains(card, gap) {
		t.Error("the card does not show the gaps of the window by reason")
	}
	for _, row := range []messageKey{msgLabelSourceState, msgLabelSelection} {
		if !strings.Contains(card, "<dt>"+catalogueText(t, row)+"</dt>") {
			t.Errorf("the card does not show %s", row)
		}
	}
	if !figureShown(t, card, msgLabelPageSizeInForce, strconv.Itoa(config.DefaultFirewallLogPageSize)) ||
		!figureShown(t, card, msgLabelIntervalInForce,
			strconv.FormatInt(int64(config.DefaultFirewallLogInterval/time.Second), 10)) ||
		!figureShown(t, card, msgLabelPageCeiling, strconv.Itoa(sizing.PageCeilingFirewallLog)) ||
		!stateShown(t, card, msgLabelPageState, msgPageWithinCeiling) {
		t.Error("the card does not show the pair in force, the ceiling and the page state")
	}
	if !stateShown(t, card, msgLabelSuggestionState, msgSuggestionAvailable) {
		t.Error("the card does not show its suggestion state")
	}

	// A table with no ingested clock, and a window with no record, are states.
	lease := cardOf(t, pageSource(t, h, PathCollection), "dhcp_lease")
	if !stateShown(t, lease, msgLabelMeanRateIngested, msgNoIngestedClock) ||
		!stateShown(t, lease, msgLabelCoveredFrom, msgNoRecordsInWindow) ||
		!stateShown(t, lease, msgLabelMeanRateSource, msgRateNotMeasured) ||
		!stateShown(t, lease, msgLabelPageCeiling, msgNoPageCeiling) {
		t.Error("the lease card does not name its missing figures as states")
	}
}

// TestASourceThatIsNotCollectedIsItsOwnStateAndGetsNoSuggestion: records on the
// table do not make a suggestion for a kind nobody reads.
func TestASourceThatIsNotCollectedIsItsOwnStateAndGetsNoSuggestion(t *testing.T) {
	h := suggestedHarness(t)
	h.setAvailability("firewall_log", "pf", store.StateUnavailable)

	card := cardOf(t, pageSource(t, h, PathCollection), "firewall_log")
	if !stateShown(t, card, msgLabelSourceState, msgSourceUnavailable) ||
		!stateShown(t, card, msgLabelSuggestionState, msgSuggestionSourceNotCollected) {
		t.Error("an unreachable source is not reported as not collected")
	}
	if strings.Contains(card, catalogueText(t, msgLabelSuggestedInterval)) {
		t.Error("a source that is not collected was given a suggestion")
	}

	// Reachable but turned off by the operator: the same outcome, and the
	// selection is a row of its own.
	h.setAvailability("firewall_log", "pf", store.StateReachable)
	h.setSelection("firewall_log", "pf", config.SelectionOff)
	card = cardOf(t, pageSource(t, h, PathCollection), "firewall_log")
	if !stateShown(t, card, msgLabelSourceState, msgSourceReachable) ||
		!stateShown(t, card, msgLabelSelection, msgSelectionOff) ||
		!stateShown(t, card, msgLabelSuggestionState, msgSuggestionSourceNotCollected) {
		t.Error("a source turned off is not reported as reachable, off and not collected")
	}
}

// TestTheFillControlIsNotOfferedWithNothingToFillWith: the control is disabled in
// every card whose outcome carries no pair.
func TestTheFillControlIsNotOfferedWithNothingToFillWith(t *testing.T) {
	fillControl := `name="` + fieldFill + `" value="1" class="secondary"`
	h := signedInHarness(t)
	page := pageSource(t, h, PathCollection)
	for _, kind := range sizing.Kinds() {
		if !strings.Contains(cardOf(t, page, kind.Kind), fillControl+" disabled>") {
			t.Errorf("the %s card offers the fill control with no suggestion", kind.Kind)
		}
	}

	h = suggestedHarness(t)
	card := cardOf(t, pageSource(t, h, PathCollection), "firewall_log")
	if !strings.Contains(card, fillControl+">") {
		t.Error("the fill control is not offered with a suggestion to fill the fields with")
	}
}

// TestTheFillControlWritesNothingAndFillsTheFields: the suggestion goes into the
// fields, and nothing is stored or handed to the running service.
func TestTheFillControlWritesNothingAndFillsTheFields(t *testing.T) {
	h := suggestedHarness(t)
	kind, _ := kindNamed("firewall_log")
	_, suggestion, _, err := h.server.measureKind(context.Background(), kind)
	if err != nil {
		t.Fatalf("measuring the filter log: %v", err)
	}
	if suggestion.Outcome != sizing.OutcomeSuggested {
		t.Fatalf("the stored records make no suggestion (%s), so this test is vacuous",
			suggestion.Outcome)
	}
	before := databaseBytesExcept(t, h, "session")

	status, page := statusAndBody(t, h.post(PathCollection, fillForm("firewall_log")))
	if status != http.StatusOK {
		t.Fatalf("filling answered %d", status)
	}
	card := cardOf(t, page, "firewall_log")
	if !strings.Contains(card, catalogueText(t, msgSuggestionFilled)) {
		t.Error("the card does not confirm the fill")
	}
	if got, want := fieldValue(card, "page_size_firewall_log"), strconv.Itoa(suggestion.Pair.PageSize); got != want {
		t.Errorf("the page field holds %q after the fill, want %q", got, want)
	}
	if got, want := fieldValue(card, "interval_firewall_log"),
		strconv.FormatInt(int64(suggestion.Pair.Interval/time.Second), 10); got != want {
		t.Errorf("the interval field holds %q after the fill, want %q", got, want)
	}

	if databaseBytesExcept(t, h, "session") != before {
		t.Error("the fill control wrote to the database")
	}
	if len(h.collector.received()) != 0 {
		t.Error("the fill control handed a configuration to the running collector")
	}

	// With no suggestion, the fields keep the pair in force and the card says so.
	_, page = statusAndBody(t, h.post(PathCollection, fillForm("security_event")))
	if !strings.Contains(cardOf(t, page, "security_event"), catalogueText(t, msgSuggestionNotFilled)) {
		t.Error("a fill with nothing to fill with is not reported in its card")
	}
}

// TestASavedPairIsStoredAndANonNumberIsRefused: a save writes the two rows, and a
// figure that is not a positive whole number writes neither.
func TestASavedPairIsStoredAndANonNumberIsRefused(t *testing.T) {
	h := signedInHarness(t)
	status, _ := statusAndBody(t, h.post(PathCollection, pairForm("firewall_log", "700", "20")))
	if status != http.StatusOK {
		t.Fatalf("saving a pair answered %d", status)
	}
	if got := h.setting("page_size_firewall_log"); got != "700" {
		t.Errorf("the stored page size is %q, want 700", got)
	}
	if got := h.setting("poll_interval_firewall_log_seconds"); got != "20" {
		t.Errorf("the stored interval is %q, want 20", got)
	}

	for _, refused := range []url.Values{
		pairForm("firewall_log", "seven", "20"),
		pairForm("firewall_log", "-3", "20"),
		pairForm("firewall_log", "800", "0"),
		pairForm("firewall_log", "800", "1.5"),
	} {
		status, page := statusAndBody(t, h.post(PathCollection, refused))
		if status != http.StatusBadRequest {
			t.Errorf("%v answered %d rather than a refusal", refused, status)
		}
		if !strings.Contains(page, catalogueText(t, msgCollectionValueInvalid)) {
			t.Errorf("%v was refused without saying why", refused)
		}
	}
	if h.setting("page_size_firewall_log") != "700" ||
		h.setting("poll_interval_firewall_log_seconds") != "20" {
		t.Error("a refused submission changed a stored figure")
	}

	// A kind the form does not know is refused on the page, with no card to say it in.
	status, page := statusAndBody(t, h.post(PathCollection, pairForm("no_such_kind", "1", "1")))
	if status != http.StatusBadRequest || !strings.Contains(page, catalogueText(t, msgCollectionKindUnknown)) {
		t.Errorf("an unknown kind answered %d without its refusal", status)
	}
}

// TestASavedPairReachesTheRunningServiceWithoutARestart: the live holder and the
// running collector have the new pair as soon as the save answers.
func TestASavedPairReachesTheRunningServiceWithoutARestart(t *testing.T) {
	h := signedInHarness(t)
	discard(t, h.post(PathCollection, pairForm("security_event", "1200", "45")))

	live := h.live.Get()
	if live.SecurityEventPageSize != 1200 || live.SecurityEventInterval != 45*time.Second {
		t.Errorf("the live configuration holds %d records every %s after the save",
			live.SecurityEventPageSize, live.SecurityEventInterval)
	}
	received := h.collector.received()
	if len(received) != 1 {
		t.Fatalf("the running collector was configured %d times, want once", len(received))
	}
	if received[0].SecurityEventPageSize != 1200 || received[0].SecurityEventInterval != 45*time.Second {
		t.Error("the running collector was handed a configuration without the saved pair")
	}

	// A refusal reaches nothing.
	discard(t, h.post(PathCollection, pairForm("security_event", "x", "45")))
	if len(h.collector.received()) != 1 {
		t.Error("a refused submission was handed to the running collector")
	}
}

// TestASaveIsConfirmedInTheCardItWasSavedIn: the confirmation is drawn in the card
// that was saved, and nowhere else.
func TestASaveIsConfirmedInTheCardItWasSavedIn(t *testing.T) {
	h := signedInHarness(t)
	_, page := statusAndBody(t, h.post(PathCollection, pairForm("dhcp_lease", "600", "120")))
	saved := catalogueText(t, msgCollectionSaved)
	if strings.Count(page, saved) != 1 {
		t.Fatalf("the confirmation appears %d times rather than once", strings.Count(page, saved))
	}
	card := cardOf(t, page, "dhcp_lease")
	if !strings.Contains(card, saved) {
		t.Error("the confirmation is not in the card that was saved")
	}
	if fieldValue(card, "page_size_dhcp_lease") != "600" || fieldValue(card, "interval_dhcp_lease") != "120" {
		t.Error("the saved card does not show the pair now in force")
	}
}

// TestARefusedFigureIsReportedAgainstTheFieldThatCausedIt: the field that caused a
// refusal is marked, carries what was typed, and is the only one marked.
func TestARefusedFigureIsReportedAgainstTheFieldThatCausedIt(t *testing.T) {
	h := signedInHarness(t)
	for _, refused := range []struct {
		form           url.Values
		field, unfield string
		typed          string
	}{
		{pairForm("firewall_log", "800", "soon"), "interval_firewall_log", "page_size_firewall_log", "soon"},
		{pairForm("firewall_log", "lots", "30"), "page_size_firewall_log", "interval_firewall_log", "lots"},
	} {
		status, page := statusAndBody(t, h.post(PathCollection, refused.form))
		if status != http.StatusBadRequest {
			t.Errorf("%v answered %d rather than a refusal", refused.form, status)
			continue
		}
		card := cardOf(t, page, "firewall_log")
		field := card[strings.Index(card, `id="`+refused.field+`"`):]
		field = field[:strings.Index(field, ">")]
		if !strings.Contains(field, `aria-invalid="true"`) ||
			!strings.Contains(field, `aria-describedby="`+strings.Replace(refused.field, "_firewall_log", "_refusal_firewall_log", 1)+`"`) {
			t.Errorf("the %s field is not marked as the cause of the refusal", refused.field)
		}
		if fieldValue(card, refused.field) != refused.typed {
			t.Errorf("the %s field does not hold what was typed", refused.field)
		}
		other := card[strings.Index(card, `id="`+refused.unfield+`"`):]
		if strings.Contains(other[:strings.Index(other, ">")], "aria-invalid") {
			t.Errorf("the %s field is marked although it caused nothing", refused.unfield)
		}
		for _, kind := range []string{"security_event", "dhcp_lease", "dns_lookup"} {
			if strings.Contains(cardOf(t, page, kind), "aria-invalid") {
				t.Errorf("the %s card is marked by a refusal in another card", kind)
			}
		}
	}
}

// TestAnIntervalNoDurationRepresentsIsRefused: the largest interval a duration
// holds is accepted, and one second more is refused as too large.
func TestAnIntervalNoDurationRepresentsIsRefused(t *testing.T) {
	h := signedInHarness(t)
	tooLarge := strconv.FormatInt(config.MaxIntervalSeconds+1, 10)
	status, page := statusAndBody(t, h.post(PathCollection, pairForm("dns_lookup", "", tooLarge)))
	if status != http.StatusBadRequest {
		t.Fatalf("an interval no duration holds answered %d", status)
	}
	if !strings.Contains(cardOf(t, page, "dns_lookup"), catalogueText(t, msgCollectionIntervalTooLarge)) {
		t.Error("the refusal does not say the interval is too large")
	}

	largest := strconv.FormatInt(config.MaxIntervalSeconds, 10)
	status, _ = statusAndBody(t, h.post(PathCollection, pairForm("dns_lookup", "", largest)))
	if status != http.StatusOK {
		t.Fatalf("the largest interval a duration holds answered %d", status)
	}
	if got := h.setting("poll_interval_dns_lookup_seconds"); got != largest {
		t.Errorf("the stored interval is %q, want %s", got, largest)
	}
}

// TestAPageSizeAboveTheCeilingIsStoredAndReported: a page above the firewall's
// ceiling is stored, and the card says it is above it.
func TestAPageSizeAboveTheCeilingIsStoredAndReported(t *testing.T) {
	h := signedInHarness(t)
	above := strconv.Itoa(sizing.PageCeilingSecurityEvent + 1)
	status, page := statusAndBody(t, h.post(PathCollection, pairForm("security_event", above, "60")))
	if status != http.StatusOK {
		t.Fatalf("a page above the ceiling answered %d", status)
	}
	if got := h.setting("page_size_security_event"); got != above {
		t.Errorf("the stored page size is %q, want %s", got, above)
	}
	card := cardOf(t, page, "security_event")
	if !figureShown(t, card, msgLabelPageSizeInForce, above) ||
		!stateShown(t, card, msgLabelPageState, msgPageAboveCeiling) {
		t.Error("the card does not report the page in force as above the ceiling")
	}
}

// TestDrawingTheSurfaceAndMeasuringWriteNothing: the surface is drawn and measured
// from the database without changing a byte of it. The session row is left out: it
// records the reader's activity, which every signed-in request moves.
func TestDrawingTheSurfaceAndMeasuringWriteNothing(t *testing.T) {
	h := suggestedHarness(t)
	before := databaseBytesExcept(t, h, "session")
	_ = pageSource(t, h, PathCollection)
	discard(t, h.post(PathCollection, fillForm("firewall_log")))
	discard(t, h.post(PathCollection, fillForm("dns_lookup")))
	if databaseBytesExcept(t, h, "session") != before {
		t.Error("drawing or measuring the collection surface wrote to the database")
	}
}

// TestTheMeasurementContactsNothing: drawing, measuring, filling and saving make no
// call to the firewall, and any other destination fails the harness's transport.
func TestTheMeasurementContactsNothing(t *testing.T) {
	h := suggestedHarness(t)
	before := len(h.fake.received())
	_ = pageSource(t, h, PathCollection)
	discard(t, h.post(PathCollection, fillForm("firewall_log")))
	discard(t, h.post(PathCollection, pairForm("firewall_log", "600", "15")))
	discard(t, h.post(PathCollection, pairForm("firewall_log", "x", "15")))
	if after := len(h.fake.received()); after != before {
		t.Errorf("the collection surface made %d calls to the firewall", after-before)
	}
}

// TestTheLowerBoundCaveatIsDocumentedAndNeverPrinted: the record rate counts what
// opnview stored, so it is a lower bound of what the source produced. That is
// written in docs/data-model.md and printed on no page of the surface.
func TestTheLowerBoundCaveatIsDocumentedAndNeverPrinted(t *testing.T) {
	raw, err := os.ReadFile("../../docs/data-model.md")
	if err != nil {
		t.Fatalf("reading docs/data-model.md: %v", err)
	}
	document := string(raw)
	start := strings.Index(document, "\n## Record rate\n")
	if start < 0 {
		t.Fatal("docs/data-model.md has no Record rate section")
	}
	section := document[start+1:]
	if end := strings.Index(section[1:], "\n## "); end >= 0 {
		section = section[:end+1]
	}
	if !strings.Contains(strings.ToLower(section), "lower bound") {
		t.Error("the Record rate section does not say the rate is a lower bound")
	}

	for _, page := range collectionPages(t) {
		lowered := strings.ToLower(visibleText(page.document))
		for _, phrase := range []string{"lower bound", "at most", "not counted", "headroom"} {
			if strings.Contains(lowered, phrase) {
				t.Errorf("%s prints the caveat phrase %q", page.name, phrase)
			}
		}
	}
}

// collectionPages renders the collection surface in every state these tests can
// reach, so the catalogue's assertions run against each.
//
// THE ORDER IS THE STATE. One harness is moved from too little measured, to a
// suggestion, to a peak no page can keep up with, and each card is put in a
// different condition so that every state the surface names is drawn at least
// once.
func collectionPages(t *testing.T) []renderedPage {
	t.Helper()
	var pages []renderedPage
	add := func(name string, answer *http.Response) {
		t.Helper()
		_, body := statusAndBody(t, answer)
		pages = append(pages, renderedPage{name, body})
	}

	h := signedInHarness(t)
	now := h.clock.Now().Unix()
	// The filter log reachable and on auto; Suricata installed and switched off on
	// the firewall; Kea turned on by the operator and never probed; both resolvers
	// turned off.
	h.setAvailability("firewall_log", "pf", store.StateReachable)
	h.setAvailability("security_event", "suricata", store.StatePresentButDisabled)
	h.setSelection("dhcp_lease", "kea", config.SelectionOn)
	h.setSelection("dns_lookup", "unbound", config.SelectionOff)
	h.setSelection("dns_lookup", "dnsmasq", config.SelectionOff)

	h.storeFlows(now - 600)
	pages = append(pages, renderedPage{"the collection surface with too little measured",
		pageSource(t, h, PathCollection)})

	var instants []int64
	for minute := int64(60); minute > 0; minute-- {
		instants = append(instants, now-minute*60)
	}
	h.storeFlows(instants...)
	h.recordGap("firewall_log", "pf", store.GapDigestOutsideWindow, now-900, now-840)
	h.recordGap("security_event", "suricata", store.GapEveRotationLost, now-800, now-700)
	h.recordGap("dns_lookup", "unbound", store.GapResolverWindowNotHonoured, now-500, now-450)
	pages = append(pages, renderedPage{"the collection surface with a suggestion",
		pageSource(t, h, PathCollection)})

	add("the collection surface with a suggestion filled", h.post(PathCollection, fillForm("firewall_log")))
	add("the collection surface with nothing to fill", h.post(PathCollection, fillForm("security_event")))
	add("the collection surface with a figure refused",
		h.post(PathCollection, pairForm("firewall_log", "many", "10")))
	add("the collection surface with an interval too large", h.post(PathCollection,
		pairForm("firewall_log", "500", strconv.FormatInt(config.MaxIntervalSeconds+1, 10))))
	add("the collection surface with an unknown kind",
		h.post(PathCollection, pairForm("no_such_kind", "1", "1")))
	add("the collection surface with a page above the ceiling", h.post(PathCollection,
		pairForm("security_event", strconv.Itoa(sizing.PageCeilingSecurityEvent+1), "60")))

	// An unlimited retention reaches the surface through the next save, which loads
	// the configuration again.
	if err := h.store.SetSetting(context.Background(), config.KeyRetentionSeconds, "0", now); err != nil {
		t.Fatalf("storing an unlimited retention: %v", err)
	}
	add("the collection surface with an unlimited retention",
		h.post(PathCollection, pairForm("dhcp_lease", "500", "300")))

	// A peak no page within the ceiling keeps up with, even at the shortest interval.
	h.storeBurst(now-180, 1600)
	pages = append(pages, renderedPage{"the collection surface with a peak no page covers",
		pageSource(t, h, PathCollection)})
	return pages
}
