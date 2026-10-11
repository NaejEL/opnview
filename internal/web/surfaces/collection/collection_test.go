package collection_test

import (
	"context"
	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/sizing"
	"github.com/NaejEL/opnview/internal/store"
	"github.com/NaejEL/opnview/internal/web"
	"github.com/NaejEL/opnview/internal/web/surfaces/collection"
	"github.com/NaejEL/opnview/internal/web/webtest"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// suggestedHarness is a signed-in harness whose filter log is reachable and holds
// enough records, spread over an hour, for a suggestion.
func suggestedHarness(t *testing.T) *webtest.Harness {
	t.Helper()
	h := webtest.SignedInHarness(t)
	h.SetAvailability("firewall_log", "pf", store.StateReachable)
	now := h.Clock.Now().Unix()
	var instants []int64
	for minute := int64(60); minute > 0; minute-- {
		instants = append(instants, now-minute*60, now-minute*60+7)
	}
	h.StoreFlows(instants...)
	return h
}

// TestTheCollectionRouteNamesItselfAndLabelsEachCard: one heading names the route,
// and every card is labelled by a heading naming its kind.
func TestTheCollectionRouteNamesItselfAndLabelsEachCard(t *testing.T) {
	t.Parallel()
	h := webtest.SignedInHarness(t)
	page := webtest.PageSource(t, h, webtest.PathCollection)

	if count := strings.Count(page, "<h1>"); count != 1 {
		t.Fatalf("the collection surface carries %d top-level headings rather than one", count)
	}
	if !strings.Contains(page, "<h1>"+webtest.CatalogueText(t, "collection.title")+"</h1>") {
		t.Error("the top-level heading does not name the collection surface")
	}
	for _, kind := range sizing.Kinds() {
		card := webtest.CardOf(t, page, kind.Kind)
		if !strings.Contains(card, `aria-labelledby="card_name_`+kind.Kind+`"`) {
			t.Errorf("the %s card is not labelled by its heading", kind.Kind)
		}
		heading := `<h2 id="card_name_` + kind.Kind + `">` +
			webtest.CatalogueText(t, messageKey(kind.LabelKey)) + "</h2>"
		if !strings.Contains(card, heading) {
			t.Errorf("the %s card has no heading naming its kind", kind.Kind)
		}
	}
}

// TestAKindWithNoProviderRowProducesNoCard: the cards are the kinds the provider
// table holds, read at run time.
func TestAKindWithNoProviderRowProducesNoCard(t *testing.T) {
	t.Parallel()
	h := webtest.SignedInHarness(t)
	db := h.Store.DB()
	for _, statement := range []string{
		`DELETE FROM source_availability WHERE provider_id IN
		    (SELECT id FROM provider WHERE kind = 'security_event')`,
		`DELETE FROM provider WHERE kind = 'security_event'`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("removing the security-event registry rows: %v", err)
		}
	}

	page := webtest.PageSource(t, h, webtest.PathCollection)
	if strings.Contains(page, `id="card_security_event"`) {
		t.Error("a kind with no provider row still produced a card")
	}
	for _, kind := range []string{"firewall_log", "dhcp_lease", "dns_lookup"} {
		webtest.CardOf(t, page, kind)
	}
}

// TestTheCollectionSurfaceReportsEveryFigureItMeasures: every figure the
// measurement carries is on the card, in the form the measurement gives it.
func TestTheCollectionSurfaceReportsEveryFigureItMeasures(t *testing.T) {
	t.Parallel()
	h := suggestedHarness(t)
	now := h.Clock.Now().Unix()
	h.RecordGap("firewall_log", "pf", store.GapDigestOutsideWindow, now-900, now-840)
	h.RecordGap("firewall_log", "pf", store.GapDigestOutsideWindow, now-600, now-570)

	kind, _ := collection.KindNamed("firewall_log")
	measured, err := h.Store.MeasureRecordRate(context.Background(), kind.Spec,
		sizing.Window(h.Clock.Now(), config.DefaultRetentionSeconds), sizing.BucketSeconds)
	if err != nil {
		t.Fatalf("measuring the filter log: %v", err)
	}
	if !measured.HasRecords || !measured.MeanRate.Known || !measured.IngestedMeanRate.Known {
		t.Fatal("the stored records do not make a measurement with every figure, " +
			"so this assertion is vacuous")
	}

	card := webtest.CardOf(t, webtest.PageSource(t, h, webtest.PathCollection), "firewall_log")
	rate := func(r store.Rate) string { return strconv.FormatFloat(r.PerSecond, 'f', 3, 64) }
	for _, row := range []struct {
		label  messageKey
		figure string
	}{
		{msgLabelWindowStart, time.Unix(now-sizing.NominalWindowSeconds, 0).UTC().Format(time.RFC3339)},
		{msgLabelWindowEnd, time.Unix(now, 0).UTC().Format(time.RFC3339)},
		{msgLabelCoveredFrom, time.Unix(now-3600, 0).UTC().Format(time.RFC3339)},
		{msgLabelCoveredTo, time.Unix(now-60+7, 0).UTC().Format(time.RFC3339)},
		{msgLabelRecordCount, "120"},
		{msgLabelMeanRateSource, rate(measured.MeanRate)},
		{msgLabelMeanRateIngested, rate(measured.IngestedMeanRate)},
		{msgLabelPeakRate, rate(measured.PeakRate)},
		{msgLabelGapCount, "2"},
	} {
		if !webtest.FigureShown(t, card, row.label, row.figure) {
			t.Errorf("the card does not show %s as %s", row.label, row.figure)
		}
	}
	// The two lengths of time are spelled in days, hours, minutes and seconds.
	for _, row := range []struct {
		label   messageKey
		seconds int64
	}{
		{msgLabelRetentionHorizon, config.DefaultRetentionSeconds},
		{msgLabelMissedSeconds, 90},
	} {
		if !strings.Contains(card, "<dt>"+webtest.CatalogueText(t, row.label)+"</dt>\n<dd>"+
			webtest.DurationText(t, row.seconds)+"</dd>") {
			t.Errorf("the card does not show %s as %d seconds spelled out", row.label, row.seconds)
		}
	}
	gap := "<td>" + webtest.CatalogueText(t, msgGapDigestOutsideWindow) + `</td><td><span class="figure">2</span></td>` +
		"<td>" + webtest.DurationText(t, 90) + "</td>"
	if !strings.Contains(card, gap) {
		t.Error("the card does not show the gaps of the window by reason")
	}
	for _, row := range []messageKey{msgLabelSourceState, msgLabelSelection} {
		if !strings.Contains(card, "<dt>"+webtest.CatalogueText(t, row)+"</dt>") {
			t.Errorf("the card does not show %s", row)
		}
	}
	if !webtest.FigureShown(t, card, msgLabelPageSizeInForce, strconv.Itoa(config.DefaultFirewallLogPageSize)) ||
		!strings.Contains(card, "<dt>"+webtest.CatalogueText(t, msgLabelIntervalInForce)+"</dt>\n<dd>"+
			webtest.DurationText(t, int64(config.DefaultFirewallLogInterval/time.Second))+"</dd>") ||
		!webtest.FigureShown(t, card, msgLabelPageCeiling, strconv.Itoa(sizing.PageCeilingFirewallLog)) ||
		!webtest.StateShown(t, card, msgLabelPageState, msgPageWithinCeiling) {
		t.Error("the card does not show the pair in force, the ceiling and the page state")
	}
	if !webtest.StateShown(t, card, msgLabelSuggestionState, msgSuggestionAvailable) {
		t.Error("the card does not show its suggestion state")
	}

	// A table with no ingested clock, and a window with no record, are states.
	lease := webtest.CardOf(t, webtest.PageSource(t, h, webtest.PathCollection), "dhcp_lease")
	if !webtest.StateShown(t, lease, msgLabelMeanRateIngested, msgNoIngestedClock) ||
		!webtest.StateShown(t, lease, msgLabelCoveredFrom, msgNoRecordsInWindow) ||
		!webtest.StateShown(t, lease, msgLabelMeanRateSource, msgRateNotMeasured) ||
		!webtest.StateShown(t, lease, msgLabelPageCeiling, msgNoPageCeiling) {
		t.Error("the lease card does not name its missing figures as states")
	}
}

// TestASourceThatIsNotCollectedIsItsOwnStateAndGetsNoSuggestion: records on the
// table do not make a suggestion for a kind nobody reads.
func TestASourceThatIsNotCollectedIsItsOwnStateAndGetsNoSuggestion(t *testing.T) {
	t.Parallel()
	h := suggestedHarness(t)
	h.SetAvailability("firewall_log", "pf", store.StateUnavailable)

	card := webtest.CardOf(t, webtest.PageSource(t, h, webtest.PathCollection), "firewall_log")
	if !webtest.StateShown(t, card, msgLabelSourceState, msgAvailabilityUnavailable) ||
		!webtest.StateShown(t, card, msgLabelSuggestionState, msgSuggestionSourceNotCollected) {
		t.Error("an unreachable source is not reported as not collected")
	}
	if strings.Contains(card, webtest.CatalogueText(t, msgLabelSuggestedInterval)) {
		t.Error("a source that is not collected was given a suggestion")
	}

	// Reachable but turned off by the operator: the same outcome, and the
	// selection is a row of its own.
	h.SetAvailability("firewall_log", "pf", store.StateReachable)
	h.SetSelection("firewall_log", "pf", config.SelectionOff)
	card = webtest.CardOf(t, webtest.PageSource(t, h, webtest.PathCollection), "firewall_log")
	if !webtest.StateShown(t, card, msgLabelSourceState, msgAvailabilityHealthy) ||
		!webtest.StateShown(t, card, msgLabelSelection, msgSelectionOff) ||
		!webtest.StateShown(t, card, msgLabelSuggestionState, msgSuggestionSourceNotCollected) {
		t.Error("a source turned off is not reported as reachable, off and not collected")
	}
}

// TestTheFillControlIsNotOfferedWithNothingToFillWith: the control is disabled in
// every card whose outcome carries no pair.
func TestTheFillControlIsNotOfferedWithNothingToFillWith(t *testing.T) {
	t.Parallel()
	fillControl := func(kind string) string {
		return `name="` + webtest.FieldFill + `" value="` + kind + `" formaction="` + webtest.PathCollection +
			`#card_` + kind + `" class="secondary"`
	}
	h := webtest.SignedInHarness(t)
	page := webtest.PageSource(t, h, webtest.PathCollection)
	for _, kind := range sizing.Kinds() {
		if !strings.Contains(webtest.CardOf(t, page, kind.Kind), fillControl(kind.Kind)+" disabled>") {
			t.Errorf("the %s card offers the fill control with no suggestion", kind.Kind)
		}
	}

	h = suggestedHarness(t)
	card := webtest.CardOf(t, webtest.PageSource(t, h, webtest.PathCollection), "firewall_log")
	if !strings.Contains(card, fillControl("firewall_log")+">") {
		t.Error("the fill control is not offered with a suggestion to fill the fields with")
	}
}

// TestTheFillControlWritesNothingAndFillsTheFields: the suggestion goes into the
// fields, and nothing is stored or handed to the running service.
func TestTheFillControlWritesNothingAndFillsTheFields(t *testing.T) {
	t.Parallel()
	h := suggestedHarness(t)
	kind, _ := collection.KindNamed("firewall_log")
	suggestion, err := collection.MeasureSuggestion(context.Background(), h.Server, kind)
	if err != nil {
		t.Fatalf("measuring the filter log: %v", err)
	}
	if suggestion.Outcome != sizing.OutcomeSuggested {
		t.Fatalf("the stored records make no suggestion (%s), so this test is vacuous",
			suggestion.Outcome)
	}
	before := webtest.DatabaseBytesExcept(t, h, "session")

	status, page := webtest.StatusAndBody(t, h.Post(webtest.PathCollection, webtest.FillForm("firewall_log")))
	if status != http.StatusOK {
		t.Fatalf("filling answered %d", status)
	}
	card := webtest.CardOf(t, page, "firewall_log")
	if !strings.Contains(card, webtest.CatalogueText(t, msgSuggestionFilled)) {
		t.Error("the card does not confirm the fill")
	}
	if got, want := webtest.FieldValue(card, "page_size_firewall_log"), strconv.Itoa(suggestion.Pair.PageSize); got != want {
		t.Errorf("the page field holds %q after the fill, want %q", got, want)
	}
	// The interval goes in as a figure beside its unit, the longest that holds it
	// exactly, as every interval field shows one.
	figure, unit := web.FieldDuration(int64(suggestion.Pair.Interval / time.Second))
	if !webtest.DurationFieldShows(t, card, "interval_firewall_log", figure, unit) {
		t.Errorf("the interval field holds %q %q after the fill, want %s %s",
			webtest.FieldValue(card, "interval_firewall_log"), webtest.SelectedIn(t, card, "interval_firewall_log_unit"),
			figure, unit)
	}

	if webtest.DatabaseBytesExcept(t, h, "session") != before {
		t.Error("the fill control wrote to the database")
	}
	if len(h.Collector.Received()) != 0 {
		t.Error("the fill control handed a configuration to the running collector")
	}

	// With no suggestion, the fields keep the pair in force and the card says so.
	_, page = webtest.StatusAndBody(t, h.Post(webtest.PathCollection, webtest.FillForm("security_event")))
	if !strings.Contains(webtest.CardOf(t, page, "security_event"), webtest.CatalogueText(t, msgSuggestionNotFilled)) {
		t.Error("a fill with nothing to fill with is not reported in its card")
	}
}

// TestASavedPairIsStoredAndANonNumberIsRefused: a save writes the two rows, and a
// figure that is not a positive whole number writes neither.
func TestASavedPairIsStoredAndANonNumberIsRefused(t *testing.T) {
	t.Parallel()
	h := webtest.SignedInHarness(t)
	status, _ := webtest.StatusAndBody(t, h.Post(webtest.PathCollection, webtest.PairForm("firewall_log", "700", "20")))
	if status != http.StatusSeeOther {
		t.Fatalf("saving a pair answered %d", status)
	}
	if got := h.Setting("page_size_firewall_log"); got != "700" {
		t.Errorf("the stored page size is %q, want 700", got)
	}
	if got := h.Setting("poll_interval_firewall_log_seconds"); got != "20" {
		t.Errorf("the stored interval is %q, want 20", got)
	}

	for _, refused := range []url.Values{
		webtest.PairForm("firewall_log", "seven", "20"),
		webtest.PairForm("firewall_log", "-3", "20"),
		webtest.PairForm("firewall_log", "800", "0"),
		webtest.PairForm("firewall_log", "800", "1.5"),
	} {
		status, page := webtest.StatusAndBody(t, h.Post(webtest.PathCollection, refused))
		if status != http.StatusBadRequest {
			t.Errorf("%v answered %d rather than a refusal", refused, status)
		}
		if !strings.Contains(page, webtest.CatalogueText(t, msgCollectionValueInvalid)) {
			t.Errorf("%v was refused without saying why", refused)
		}
	}
	if h.Setting("page_size_firewall_log") != "700" ||
		h.Setting("poll_interval_firewall_log_seconds") != "20" {
		t.Error("a refused submission changed a stored figure")
	}

	// A kind the form does not know is refused on the page, with no card to say it in.
	status, page := webtest.StatusAndBody(t, h.Post(webtest.PathCollection, webtest.PairForm("no_such_kind", "1", "1")))
	if status != http.StatusBadRequest || !strings.Contains(page, webtest.CatalogueText(t, msgCollectionKindUnknown)) {
		t.Errorf("an unknown kind answered %d without its refusal", status)
	}
}

// TestASavedPairReachesTheRunningServiceWithoutARestart: the live holder and the
// running collector have the new pair as soon as the save answers.
func TestASavedPairReachesTheRunningServiceWithoutARestart(t *testing.T) {
	t.Parallel()
	h := webtest.SignedInHarness(t)
	webtest.Discard(t, h.Post(webtest.PathCollection, webtest.PairForm("security_event", "1200", "45")))

	live := h.Live.Get()
	if live.SecurityEventPageSize != 1200 || live.SecurityEventInterval != 45*time.Second {
		t.Errorf("the live configuration holds %d records every %s after the save",
			live.SecurityEventPageSize, live.SecurityEventInterval)
	}
	received := h.Collector.Received()
	if len(received) != 1 {
		t.Fatalf("the running collector was configured %d times, want once", len(received))
	}
	if received[0].SecurityEventPageSize != 1200 || received[0].SecurityEventInterval != 45*time.Second {
		t.Error("the running collector was handed a configuration without the saved pair")
	}

	// A refusal reaches nothing.
	webtest.Discard(t, h.Post(webtest.PathCollection, webtest.PairForm("security_event", "x", "45")))
	if len(h.Collector.Received()) != 1 {
		t.Error("a refused submission was handed to the running collector")
	}
}

// TestASaveIsConfirmedInTheCardItWasSavedIn: the confirmation is drawn in the card
// that was saved, and nowhere else.
func TestASaveIsConfirmedInTheCardItWasSavedIn(t *testing.T) {
	t.Parallel()
	h := webtest.SignedInHarness(t)
	answer := h.Post(webtest.PathCollection, webtest.PairForm("dhcp_lease", "600", "120"))
	if location := answer.Header.Get("Location"); location != webtest.PathCollection+"?saved=dhcp_lease#card_dhcp_lease" {
		t.Errorf("the save sent the browser to %q, not back to its card", location)
	}
	_, page := webtest.Followed(t, h, answer)
	saved := webtest.CatalogueText(t, msgCollectionSaved)
	if strings.Count(page, saved) != 1 {
		t.Fatalf("the confirmation appears %d times rather than once", strings.Count(page, saved))
	}
	card := webtest.CardOf(t, page, "dhcp_lease")
	if !strings.Contains(card, saved) {
		t.Error("the confirmation is not in the card that was saved")
	}
	if webtest.FieldValue(card, "page_size_dhcp_lease") != "600" ||
		!webtest.DurationFieldShows(t, card, "interval_dhcp_lease", "2", web.UnitMinutes) {
		t.Error("the saved card does not show the pair now in force")
	}
}

// TestARefusedFigureIsReportedAgainstTheFieldThatCausedIt: the field that caused a
// refusal is marked, carries what was typed, and is the only one marked.
func TestARefusedFigureIsReportedAgainstTheFieldThatCausedIt(t *testing.T) {
	t.Parallel()
	h := webtest.SignedInHarness(t)
	for _, refused := range []struct {
		form           url.Values
		field, unfield string
		typed          string
	}{
		{webtest.PairForm("firewall_log", "800", "soon"), "interval_firewall_log", "page_size_firewall_log", "soon"},
		{webtest.PairForm("firewall_log", "lots", "30"), "page_size_firewall_log", "interval_firewall_log", "lots"},
	} {
		status, page := webtest.StatusAndBody(t, h.Post(webtest.PathCollection, refused.form))
		if status != http.StatusBadRequest {
			t.Errorf("%v answered %d rather than a refusal", refused.form, status)
			continue
		}
		card := webtest.CardOf(t, page, "firewall_log")
		field := card[strings.Index(card, `id="`+refused.field+`"`):]
		field = field[:strings.Index(field, ">")]
		if !strings.Contains(field, `aria-invalid="true"`) ||
			!strings.Contains(field, `aria-describedby="`+refused.field+`_refusal"`) {
			t.Errorf("the %s field is not marked as the cause of the refusal", refused.field)
		}
		if webtest.FieldValue(card, refused.field) != refused.typed {
			t.Errorf("the %s field does not hold what was typed", refused.field)
		}
		other := card[strings.Index(card, `id="`+refused.unfield+`"`):]
		if strings.Contains(other[:strings.Index(other, ">")], "aria-invalid") {
			t.Errorf("the %s field is marked although it caused nothing", refused.unfield)
		}
		for _, kind := range []string{"security_event", "dhcp_lease", "dns_lookup"} {
			if strings.Contains(webtest.CardOf(t, page, kind), "aria-invalid") {
				t.Errorf("the %s card is marked by a refusal in another card", kind)
			}
		}
	}
}

// TestAnIntervalNoDurationRepresentsIsRefused: the largest interval a duration
// holds is accepted, and one second more is refused as too large.
func TestAnIntervalNoDurationRepresentsIsRefused(t *testing.T) {
	t.Parallel()
	h := webtest.SignedInHarness(t)
	tooLarge := strconv.FormatInt(config.MaxIntervalSeconds+1, 10)
	status, page := webtest.StatusAndBody(t, h.Post(webtest.PathCollection, webtest.PairForm("dns_lookup", "", tooLarge)))
	if status != http.StatusBadRequest {
		t.Fatalf("an interval no duration holds answered %d", status)
	}
	if !strings.Contains(webtest.CardOf(t, page, "dns_lookup"), webtest.CatalogueText(t, msgCollectionIntervalTooLarge)) {
		t.Error("the refusal does not say the interval is too large")
	}

	largest := strconv.FormatInt(config.MaxIntervalSeconds, 10)
	status, _ = webtest.StatusAndBody(t, h.Post(webtest.PathCollection, webtest.PairForm("dns_lookup", "", largest)))
	if status != http.StatusSeeOther {
		t.Fatalf("the largest interval a duration holds answered %d", status)
	}
	if got := h.Setting("poll_interval_dns_lookup_seconds"); got != largest {
		t.Errorf("the stored interval is %q, want %s", got, largest)
	}
}

// TestAPageSizeAboveTheCeilingIsStoredAndReported: a page above the firewall's
// ceiling is stored, and the card says it is above it.
func TestAPageSizeAboveTheCeilingIsStoredAndReported(t *testing.T) {
	t.Parallel()
	h := webtest.SignedInHarness(t)
	above := strconv.Itoa(sizing.PageCeilingSecurityEvent + 1)
	status, page := webtest.Followed(t, h, h.Post(webtest.PathCollection, webtest.PairForm("security_event", above, "60")))
	if status != http.StatusSeeOther {
		t.Fatalf("a page above the ceiling answered %d", status)
	}
	if got := h.Setting("page_size_security_event"); got != above {
		t.Errorf("the stored page size is %q, want %s", got, above)
	}
	card := webtest.CardOf(t, page, "security_event")
	if !webtest.FigureShown(t, card, msgLabelPageSizeInForce, above) ||
		!webtest.StateShown(t, card, msgLabelPageState, msgPageAboveCeiling) {
		t.Error("the card does not report the page in force as above the ceiling")
	}
}

// TestDrawingTheSurfaceAndMeasuringWriteNothing: the surface is drawn and measured
// from the database without changing a byte of it. The session row is left out: it
// records the reader's activity, which every signed-in request moves.
func TestDrawingTheSurfaceAndMeasuringWriteNothing(t *testing.T) {
	t.Parallel()
	h := suggestedHarness(t)
	before := webtest.DatabaseBytesExcept(t, h, "session")
	_ = webtest.PageSource(t, h, webtest.PathCollection)
	webtest.Discard(t, h.Post(webtest.PathCollection, webtest.FillForm("firewall_log")))
	webtest.Discard(t, h.Post(webtest.PathCollection, webtest.FillForm("dns_lookup")))
	if webtest.DatabaseBytesExcept(t, h, "session") != before {
		t.Error("drawing or measuring the collection surface wrote to the database")
	}
}

// TestTheMeasurementContactsNothing: drawing, measuring, filling and saving make no
// call to the firewall, and any other destination fails the harness's transport.
func TestTheMeasurementContactsNothing(t *testing.T) {
	t.Parallel()
	h := suggestedHarness(t)
	before := len(h.Fake.Received())
	_ = webtest.PageSource(t, h, webtest.PathCollection)
	webtest.Discard(t, h.Post(webtest.PathCollection, webtest.FillForm("firewall_log")))
	webtest.Discard(t, h.Post(webtest.PathCollection, webtest.PairForm("firewall_log", "600", "15")))
	webtest.Discard(t, h.Post(webtest.PathCollection, webtest.PairForm("firewall_log", "x", "15")))
	if after := len(h.Fake.Received()); after != before {
		t.Errorf("the collection surface made %d calls to the firewall", after-before)
	}
}

// TestTheLowerBoundCaveatIsDocumentedAndNeverPrinted: the record rate counts what
// opnview stored, so it is a lower bound of what the source produced. That is
// written in docs/data-model.md and printed on no page of the surface.
func TestTheLowerBoundCaveatIsDocumentedAndNeverPrinted(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("../../../../docs/data-model.md")
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

	for _, page := range webtest.CollectionPages(t) {
		lowered := strings.ToLower(webtest.VisibleText(page.Document))
		for _, phrase := range []string{"lower bound", "at most", "not counted", "headroom"} {
			if strings.Contains(lowered, phrase) {
				t.Errorf("%s prints the caveat phrase %q", page.Name, phrase)
			}
		}
	}
}

// TestEachImplementationIsSelectableInItsCard: every implementation of a kind has its
// own selection in that kind's card, named from the catalogue, showing what is stored.
func TestEachImplementationIsSelectableInItsCard(t *testing.T) {
	t.Parallel()
	h := webtest.SignedInHarness(t)
	h.SetSelection("dhcp_lease", "kea", config.SelectionOn)
	card := webtest.CardOf(t, webtest.PageSource(t, h, webtest.PathCollection), "dhcp_lease")

	for _, implementation := range []struct {
		key   string
		label messageKey
		want  config.Selection
	}{
		{"dnsmasq", msgProviderDnsmasqDHCP, config.SelectionAuto},
		{"isc", msgProviderISC, config.SelectionAuto},
		{"kea", msgProviderKea, config.SelectionOn},
	} {
		id := webtest.CardFieldName("dhcp_lease", webtest.FieldSelectionPrefix+implementation.key)
		if !strings.Contains(card, `<label for="`+id+`">`+webtest.CatalogueText(t, implementation.label)+"</label>") {
			t.Errorf("the selection of %s is not labelled with its name", implementation.key)
		}
		if got := webtest.SelectedIn(t, card, id); got != string(implementation.want) {
			t.Errorf("the selection of %s shows %q, want %q", implementation.key, got, implementation.want)
		}
	}
	if strings.Contains(card, "provider.dns_lookup") || strings.Contains(card, `name="dns_lookup.`) {
		t.Error("the lease card offers an implementation of another kind")
	}
}

// TestASavedSelectionIsStoredAndAppliedAtOnce: a save stores each selection sent and
// hands the kind to the running collector; a save that sends none leaves them as they
// are.
func TestASavedSelectionIsStoredAndAppliedAtOnce(t *testing.T) {
	t.Parallel()
	h := webtest.SignedInHarness(t)
	form := webtest.PairForm("dhcp_lease", "500", "300")
	form.Set(webtest.CardFieldName("dhcp_lease", "selection_kea"), "on")
	form.Set(webtest.CardFieldName("dhcp_lease", "selection_isc"), "off")
	form.Set(webtest.CardFieldName("dhcp_lease", "selection_dnsmasq"), "auto")
	status, page := webtest.Followed(t, h, h.Post(webtest.PathCollection, form))
	if status != http.StatusSeeOther {
		t.Fatalf("saving a selection answered %d", status)
	}
	for key, want := range map[string]string{"kea": "on", "isc": "off", "dnsmasq": "auto"} {
		if got := h.Setting(config.KeySourceSelection("dhcp_lease", key)); got != want {
			t.Errorf("the stored selection of %s is %q, want %q", key, got, want)
		}
	}
	if got := h.Collector.ReselectedKinds(); len(got) != 1 || got[0] != "dhcp_lease" {
		t.Errorf("the running collector applied the selections of %v, want [dhcp_lease]", got)
	}
	card := webtest.CardOf(t, page, "dhcp_lease")
	if webtest.SelectedIn(t, card, webtest.CardFieldName("dhcp_lease", "selection_kea")) != "on" ||
		!webtest.StateShown(t, card, msgLabelSelection, msgSelectionOn) {
		t.Error("the saved card does not show the selection now in force")
	}

	webtest.Discard(t, h.Post(webtest.PathCollection, webtest.PairForm("dhcp_lease", "500", "300")))
	if got := h.Setting(config.KeySourceSelection("dhcp_lease", "kea")); got != "on" {
		t.Errorf("a save that sent no selection changed one to %q", got)
	}
}

// TestASelectionThatIsNotOneOfTheThreeIsRefusedAgainstItsField: nothing is stored and
// nothing applied, and the refusal is drawn against the selection that caused it.
func TestASelectionThatIsNotOneOfTheThreeIsRefusedAgainstItsField(t *testing.T) {
	t.Parallel()
	h := webtest.SignedInHarness(t)
	form := webtest.PairForm("dhcp_lease", "600", "120")
	form.Set(webtest.CardFieldName("dhcp_lease", "selection_kea"), "maybe")
	status, page := webtest.StatusAndBody(t, h.Post(webtest.PathCollection, form))
	if status != http.StatusBadRequest {
		t.Fatalf("a selection that is not one of the three answered %d", status)
	}
	card := webtest.CardOf(t, page, "dhcp_lease")
	field := card[strings.Index(card, `<select id="dhcp_lease.selection_kea"`):]
	field = field[:strings.Index(field, ">")]
	if !strings.Contains(field, `aria-invalid="true"`) ||
		!strings.Contains(field, `aria-describedby="dhcp_lease.selection_kea_refusal"`) {
		t.Error("the refused selection is not marked as the cause of the refusal")
	}
	if !strings.Contains(card, webtest.CatalogueText(t, msgCollectionValueInvalid)) {
		t.Error("the refusal does not say why")
	}
	if _, present, err := h.Store.Setting(context.Background(), "page_size_dhcp_lease"); err != nil || present {
		t.Error("a refused selection let the pair beside it be stored")
	}
	if _, present, err := h.Store.Setting(context.Background(),
		config.KeySourceSelection("dhcp_lease", "kea")); err != nil || present {
		t.Error("a refused selection was stored")
	}
	if len(h.Collector.ReselectedKinds()) != 0 || len(h.Collector.Received()) != 0 {
		t.Error("a refused submission reached the running collector")
	}
}
