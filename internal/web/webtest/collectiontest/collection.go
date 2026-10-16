package collectiontest

import (
	"context"
	"html"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/sizing"
	"github.com/NaejEL/opnview/internal/store/vocab"
	"github.com/NaejEL/opnview/internal/web"
	"github.com/NaejEL/opnview/internal/web/webtest"
)

// SignedInHarness is a harness with an account, signed in.
func SignedInHarness(t *testing.T) *webtest.Harness {
	t.Helper()
	h := webtest.New(t)
	h.CompleteSetup()
	return h
}

// StoreFlows stores one synthesised filter-log record per source instant, each
// ingested five seconds after it. Nothing here is an address or an identifier read
// off a network.
func StoreFlows(h *webtest.Harness, observedAt ...int64) {
	h.T.Helper()
	batch := webtest.RandomHex(h.T, 4)
	for index, instant := range observedAt {
		flow := vocab.Flow{
			LogDigest:            "collection-test-" + batch + "-" + strconv.Itoa(index),
			ObservedAt:           instant,
			IngestedAt:           instant + 5,
			InterfaceDevice:      "example-device",
			InterfaceLookupState: vocab.LookupResolved,
			SrcAddress:           "example-source",
			DstAddress:           "example-destination",
			Protocol:             "tcp",
			IPVersion:            4,
			Action:               "pass",
			Direction:            "in",
			PacketBytes:          100,
			RuleLookupState:      vocab.LookupPending,
		}
		if err := h.Store.InsertFlow(context.Background(), flow); err != nil {
			h.T.Fatalf("storing a flow observed at %d: %v", instant, err)
		}
	}
}

// StoreBurst stores count filter-log records spread over the minute starting at
// from: one through the product's own insert, and the rest copied from it by one
// statement, because sixteen hundred separate inserts cost seconds and prove
// nothing more.
func StoreBurst(h *webtest.Harness, from int64, count int) {
	h.T.Helper()
	StoreFlows(h, from)
	db := h.Store.DB()
	var seed string
	if err := db.QueryRow(`SELECT log_digest FROM flow WHERE observed_at = ? ORDER BY id DESC LIMIT 1`,
		from).Scan(&seed); err != nil {
		h.T.Fatalf("reading the seed record back: %v", err)
	}
	var copied, selected []string
	for _, column := range webtest.TableColumns(h.T, db, "flow") {
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

// ProviderID looks up one implementation's registry row.
func ProviderID(h *webtest.Harness, kind, providerKey string) int64 {
	h.T.Helper()
	id, err := h.Store.ProviderID(context.Background(), kind, providerKey)
	if err != nil {
		h.T.Fatalf("looking up the %s provider %s: %v", kind, providerKey, err)
	}
	return id
}

// SetAvailability records what a probe of one implementation found.
func SetAvailability(h *webtest.Harness, kind, providerKey string, state vocab.AvailabilityState) {
	h.T.Helper()
	if err := h.Store.SetAvailability(context.Background(), ProviderID(h, kind, providerKey),
		state, vocab.OutcomeHealthy, "test-probe", nil, h.Clock.Now().Unix()); err != nil {
		h.T.Fatalf("recording %s for %s/%s: %v", state, kind, providerKey, err)
	}
}

// SetSelection stores the operator's selection for one implementation.
func SetSelection(h *webtest.Harness, kind, providerKey string, selection config.Selection) {
	h.T.Helper()
	if err := h.Store.SetSetting(context.Background(), config.KeySourceSelection(kind, providerKey),
		string(selection), h.Clock.Now().Unix()); err != nil {
		h.T.Fatalf("selecting %s for %s/%s: %v", selection, kind, providerKey, err)
	}
}

// RecordGap stores one collection gap of an implementation, detected at an instant.
func RecordGap(h *webtest.Harness, kind, providerKey string, reason vocab.GapReason, start, end int64) {
	h.T.Helper()
	if err := h.Store.RecordCollectionGap(context.Background(), vocab.CollectionGap{
		ProviderID: ProviderID(h, kind, providerKey), IntervalStartAt: start,
		IntervalEndAt: end, Reason: reason, DetectedAt: end,
	}); err != nil {
		h.T.Fatalf("recording a %s gap: %v", reason, err)
	}
}

// Setting reads one setting row, failing when it is absent.
func Setting(h *webtest.Harness, key string) string {
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

// PairForm is what one card's save submits.
func PairForm(kind, pageSize, intervalSeconds string) url.Values {
	return url.Values{
		webtest.FieldKind: {kind}, webtest.CardFieldName(kind, webtest.FieldPageSize): {pageSize},
		webtest.CardFieldName(kind, webtest.FieldInterval): {intervalSeconds},
	}
}

// FillForm is what one card's fill control submits.
func FillForm(kind string) url.Values {
	return url.Values{webtest.FieldFill: {kind}}
}

// CardOf returns one kind's card out of a rendered page, failing when there is
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

// CatalogueText is the source-language string of one key.
func CatalogueText(t *testing.T, key webtest.MessageKey) string {
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

// FigureShown says whether a card shows a row labelled by one key carrying one
// figure.
func FigureShown(t *testing.T, card string, label webtest.MessageKey, figure string) bool {
	t.Helper()
	return strings.Contains(card, "<dt>"+CatalogueText(t, label)+"</dt>\n<dd><span class=\"figure\">"+
		figure+"</span></dd>")
}

// StateShown says whether a card shows a row labelled by one key carrying one state.
func StateShown(t *testing.T, card string, label, state webtest.MessageKey) bool {
	t.Helper()
	return strings.Contains(card, "<dt>"+CatalogueText(t, label)+"</dt>\n<dd>"+
		CatalogueText(t, state)+"</dd>")
}

// FieldValue is what one input of a card holds.
func FieldValue(card, id string) string {
	return html.UnescapeString(webtest.AttributeAfter(card[max(strings.Index(card, `id="`+id+`"`), 0):], `value="`))
}

// CollectionPages renders the collection surface in every state these tests can
// reach, so the catalogue's assertions run against each.
//
// THE ORDER IS THE STATE. One harness is moved from too little measured, to a
// suggestion, to a peak no page can keep up with, and each card is put in a
// different condition so that every state the surface names is drawn at least
// once.
func CollectionPages(t *testing.T) []webtest.RenderedPage {
	t.Helper()
	var pages []webtest.RenderedPage
	h := SignedInHarness(t)
	// A save answers with a redirect back to the surface, which is followed: the page
	// checked is the one the reader lands on.
	add := func(name string, answer *http.Response) {
		t.Helper()
		_, body := webtest.Followed(t, h, answer)
		pages = append(pages, webtest.RenderedPage{Name: name, Document: body})
	}

	now := h.Clock.Now().Unix()
	// The filter log reachable and on auto; Suricata installed and switched off on
	// the firewall; Kea turned on by the operator and never probed; both resolvers
	// turned off.
	SetAvailability(h, "firewall_log", "pf", vocab.StateReachable)
	SetAvailability(h, "security_event", "suricata", vocab.StatePresentButDisabled)
	SetSelection(h, "dhcp_lease", "kea", config.SelectionOn)
	SetSelection(h, "dns_lookup", "unbound", config.SelectionOff)
	SetSelection(h, "dns_lookup", "dnsmasq", config.SelectionOff)

	StoreFlows(h, now-600)
	pages = append(pages, webtest.RenderedPage{Name: "the collection surface with too little measured",
		Document: webtest.PageSource(t, h, webtest.PathCollection)})

	var instants []int64
	for minute := int64(60); minute > 0; minute-- {
		instants = append(instants, now-minute*60)
	}
	StoreFlows(h, instants...)
	RecordGap(h, "firewall_log", "pf", vocab.GapDigestOutsideWindow, now-900, now-840)
	RecordGap(h, "security_event", "suricata", vocab.GapEveRotationLost, now-800, now-700)
	RecordGap(h, "dns_lookup", "unbound", vocab.GapResolverWindowNotHonoured, now-500, now-450)
	pages = append(pages, webtest.RenderedPage{Name: "the collection surface with a suggestion",
		Document: webtest.PageSource(t, h, webtest.PathCollection)})

	add("the collection surface with a suggestion filled", h.Post(webtest.PathCollection, FillForm("firewall_log")))
	add("the collection surface with nothing to fill", h.Post(webtest.PathCollection, FillForm("security_event")))
	add("the collection surface with a figure refused",
		h.Post(webtest.PathCollection, PairForm("firewall_log", "many", "10")))
	add("the collection surface with an interval too large", h.Post(webtest.PathCollection,
		PairForm("firewall_log", "500", strconv.FormatInt(config.MaxIntervalSeconds+1, 10))))
	add("the collection surface with an unknown kind",
		h.Post(webtest.PathCollection, PairForm("no_such_kind", "1", "1")))
	add("the collection surface with a page above the ceiling", h.Post(webtest.PathCollection,
		PairForm("security_event", strconv.Itoa(sizing.PageCeilingSecurityEvent+1), "60")))

	// An unlimited retention reaches the surface through the next save, which loads
	// the configuration again.
	if err := h.Store.SetSetting(context.Background(), config.KeyRetentionSeconds, "0", now); err != nil {
		t.Fatalf("storing an unlimited retention: %v", err)
	}
	add("the collection surface with an unlimited retention",
		h.Post(webtest.PathCollection, PairForm("dhcp_lease", "500", "300")))

	// A peak no page within the ceiling keeps up with, even at the shortest interval.
	StoreBurst(h, now-180, 1600)
	pages = append(pages, webtest.RenderedPage{Name: "the collection surface with a peak no page covers",
		Document: webtest.PageSource(t, h, webtest.PathCollection)})
	return pages
}

// SelectedIn returns the value of the option selected in one select of a card, or
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
	return webtest.AttributeAfter(body[strings.LastIndex(body[:marker], "<option"):], `value="`)
}
