package collectiontest

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/NaejEL/opnview/internal/config"
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
	return value
}
