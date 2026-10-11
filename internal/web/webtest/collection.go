package webtest

import (
	"context"
	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/sizing"
	"github.com/NaejEL/opnview/internal/store"
	"github.com/NaejEL/opnview/internal/web"
	"html"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

// signedInHarness is a harness with an account, signed in.
func SignedInHarness(t *testing.T) *Harness {
	t.Helper()
	h := New(t)
	h.CompleteSetup()
	return h
}

// storeFlows stores one synthesised filter-log record per source instant, each
// ingested five seconds after it. Nothing here is an address or an identifier read
// off a network.
func (h *Harness) StoreFlows(observedAt ...int64) {
	h.T.Helper()
	batch := RandomHex(h.T, 4)
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
		if err := h.Store.InsertFlow(context.Background(), flow); err != nil {
			h.T.Fatalf("storing a flow observed at %d: %v", instant, err)
		}
	}
}

// storeBurst stores count filter-log records spread over the minute starting at
// from: one through the product's own insert, and the rest copied from it by one
// statement, because sixteen hundred separate inserts cost seconds and prove
// nothing more.
func (h *Harness) StoreBurst(from int64, count int) {
	h.T.Helper()
	h.StoreFlows(from)
	db := h.Store.DB()
	var seed string
	if err := db.QueryRow(`SELECT log_digest FROM flow WHERE observed_at = ? ORDER BY id DESC LIMIT 1`,
		from).Scan(&seed); err != nil {
		h.T.Fatalf("reading the seed record back: %v", err)
	}
	var copied, selected []string
	for _, column := range TableColumns(h.T, db, "flow") {
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
		h.T.Fatalf("copying the seed record: %v", err)
	}
}

// providerID looks up one implementation's registry row.
func (h *Harness) ProviderID(kind, providerKey string) int64 {
	h.T.Helper()
	id, err := h.Store.ProviderID(context.Background(), kind, providerKey)
	if err != nil {
		h.T.Fatalf("looking up the %s provider %s: %v", kind, providerKey, err)
	}
	return id
}

// setAvailability records what a probe of one implementation found.
func (h *Harness) SetAvailability(kind, providerKey string, state store.AvailabilityState) {
	h.T.Helper()
	if err := h.Store.SetAvailability(context.Background(), h.ProviderID(kind, providerKey),
		state, store.OutcomeHealthy, "test-probe", nil, h.Clock.Now().Unix()); err != nil {
		h.T.Fatalf("recording %s for %s/%s: %v", state, kind, providerKey, err)
	}
}

// setSelection stores the operator's selection for one implementation.
func (h *Harness) SetSelection(kind, providerKey string, selection config.Selection) {
	h.T.Helper()
	if err := h.Store.SetSetting(context.Background(), config.KeySourceSelection(kind, providerKey),
		string(selection), h.Clock.Now().Unix()); err != nil {
		h.T.Fatalf("selecting %s for %s/%s: %v", selection, kind, providerKey, err)
	}
}

// recordGap stores one collection gap of an implementation, detected at an instant.
func (h *Harness) RecordGap(kind, providerKey string, reason store.GapReason, start, end int64) {
	h.T.Helper()
	if err := h.Store.RecordCollectionGap(context.Background(), store.CollectionGap{
		ProviderID: h.ProviderID(kind, providerKey), IntervalStartAt: start,
		IntervalEndAt: end, Reason: reason, DetectedAt: end,
	}); err != nil {
		h.T.Fatalf("recording a %s gap: %v", reason, err)
	}
}

// setting reads one setting row, failing when it is absent.
func (h *Harness) Setting(key string) string {
	h.T.Helper()
	value, present, err := h.Store.Setting(context.Background(), key)
	if err != nil {
		h.T.Fatalf("reading the setting %s: %v", key, err)
	}
	if !present {
		h.T.Fatalf("the setting %s is absent", key)
	}
	return value
}

// pairForm is what one card's save submits.
func PairForm(kind, pageSize, intervalSeconds string) url.Values {
	return url.Values{
		FieldKind: {kind}, CardFieldName(kind, FieldPageSize): {pageSize},
		CardFieldName(kind, FieldInterval): {intervalSeconds},
	}
}

// fillForm is what one card's fill control submits.
func FillForm(kind string) url.Values {
	return url.Values{FieldFill: {kind}}
}

// cardOf returns one kind's card out of a rendered page, failing when there is
// none.
func CardOf(t *testing.T, page, kind string) string {
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
func CatalogueText(t *testing.T, key messageKey) string {
	t.Helper()
	catalogue, err := web.ProductCatalogue(web.SourceLanguage)
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
func FigureShown(t *testing.T, card string, label messageKey, figure string) bool {
	t.Helper()
	return strings.Contains(card, "<dt>"+CatalogueText(t, label)+"</dt>\n<dd><span class=\"figure\">"+
		figure+"</span></dd>")
}

// stateShown says whether a card shows a row labelled by one key carrying one state.
func StateShown(t *testing.T, card string, label, state messageKey) bool {
	t.Helper()
	return strings.Contains(card, "<dt>"+CatalogueText(t, label)+"</dt>\n<dd>"+
		CatalogueText(t, state)+"</dd>")
}

// fieldValue is what one input of a card holds.
func FieldValue(card, id string) string {
	return html.UnescapeString(AttributeAfter(card[max(strings.Index(card, `id="`+id+`"`), 0):], `value="`))
}

// collectionPages renders the collection surface in every state these tests can
// reach, so the catalogue's assertions run against each.
//
// THE ORDER IS THE STATE. One harness is moved from too little measured, to a
// suggestion, to a peak no page can keep up with, and each card is put in a
// different condition so that every state the surface names is drawn at least
// once.
func CollectionPages(t *testing.T) []RenderedPage {
	t.Helper()
	var pages []RenderedPage
	h := SignedInHarness(t)
	// A save answers with a redirect back to the surface, which is followed: the page
	// checked is the one the reader lands on.
	add := func(name string, answer *http.Response) {
		t.Helper()
		_, body := Followed(t, h, answer)
		pages = append(pages, RenderedPage{name, body})
	}

	now := h.Clock.Now().Unix()
	// The filter log reachable and on auto; Suricata installed and switched off on
	// the firewall; Kea turned on by the operator and never probed; both resolvers
	// turned off.
	h.SetAvailability("firewall_log", "pf", store.StateReachable)
	h.SetAvailability("security_event", "suricata", store.StatePresentButDisabled)
	h.SetSelection("dhcp_lease", "kea", config.SelectionOn)
	h.SetSelection("dns_lookup", "unbound", config.SelectionOff)
	h.SetSelection("dns_lookup", "dnsmasq", config.SelectionOff)

	h.StoreFlows(now - 600)
	pages = append(pages, RenderedPage{"the collection surface with too little measured",
		PageSource(t, h, PathCollection)})

	var instants []int64
	for minute := int64(60); minute > 0; minute-- {
		instants = append(instants, now-minute*60)
	}
	h.StoreFlows(instants...)
	h.RecordGap("firewall_log", "pf", store.GapDigestOutsideWindow, now-900, now-840)
	h.RecordGap("security_event", "suricata", store.GapEveRotationLost, now-800, now-700)
	h.RecordGap("dns_lookup", "unbound", store.GapResolverWindowNotHonoured, now-500, now-450)
	pages = append(pages, RenderedPage{"the collection surface with a suggestion",
		PageSource(t, h, PathCollection)})

	add("the collection surface with a suggestion filled", h.Post(PathCollection, FillForm("firewall_log")))
	add("the collection surface with nothing to fill", h.Post(PathCollection, FillForm("security_event")))
	add("the collection surface with a figure refused",
		h.Post(PathCollection, PairForm("firewall_log", "many", "10")))
	add("the collection surface with an interval too large", h.Post(PathCollection,
		PairForm("firewall_log", "500", strconv.FormatInt(config.MaxIntervalSeconds+1, 10))))
	add("the collection surface with an unknown kind",
		h.Post(PathCollection, PairForm("no_such_kind", "1", "1")))
	add("the collection surface with a page above the ceiling", h.Post(PathCollection,
		PairForm("security_event", strconv.Itoa(sizing.PageCeilingSecurityEvent+1), "60")))

	// An unlimited retention reaches the surface through the next save, which loads
	// the configuration again.
	if err := h.Store.SetSetting(context.Background(), config.KeyRetentionSeconds, "0", now); err != nil {
		t.Fatalf("storing an unlimited retention: %v", err)
	}
	add("the collection surface with an unlimited retention",
		h.Post(PathCollection, PairForm("dhcp_lease", "500", "300")))

	// A peak no page within the ceiling keeps up with, even at the shortest interval.
	h.StoreBurst(now-180, 1600)
	pages = append(pages, RenderedPage{"the collection surface with a peak no page covers",
		PageSource(t, h, PathCollection)})
	return pages
}

// selectedIn returns the value of the option selected in one select of a card, or
// the empty string when none is.
func SelectedIn(t *testing.T, card, id string) string {
	t.Helper()
	start := strings.Index(card, `<select id="`+id+`"`)
	if start < 0 {
		t.Fatalf("the card has no select %s", id)
	}
	body := card[start:]
	body = body[:strings.Index(body, "</select>")]
	marker := strings.Index(body, " selected>")
	if marker < 0 {
		return ""
	}
	return AttributeAfter(body[strings.LastIndex(body[:marker], "<option"):], `value="`)
}
