package store_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NaejEL/opnview/internal/store/storetest"
	"github.com/NaejEL/opnview/internal/store/vocab"
)

// The read paths of the Interfaces and Sources surfaces.
//
// PROVENANCE: SYNTHESISED. The interfaces are written through UpsertInterface with
// example identifiers, the availability through SetAvailability and the gaps through
// RecordCollectionGap, at instants chosen here; the number of interfaces is a
// parameter.

// TestRecentGapsAreTheWindowNewestFirst: a gap detected before the instant given is
// left out, and the rest come newest first with their provider.
func TestRecentGapsAreTheWindowNewestFirst(t *testing.T) {
	t.Parallel()
	database, _ := openTestStore(t)
	ctx := context.Background()
	providerID, err := database.ProviderID(ctx, "firewall_log", "pf")
	if err != nil {
		t.Fatalf("looking up the provider: %v", err)
	}
	detail := "example detail"
	for _, detected := range []int64{storetest.ReferenceNow - 7200, storetest.ReferenceNow - 60, storetest.ReferenceNow - 90000} {
		if err := database.RecordCollectionGap(ctx, vocab.CollectionGap{ProviderID: providerID,
			IntervalStartAt: detected - 30, IntervalEndAt: detected, Reason: vocab.GapDigestOutsideWindow,
			Detail: &detail, DetectedAt: detected}); err != nil {
			t.Fatalf("recording a gap: %v", err)
		}
	}
	gaps, err := database.RecentCollectionGaps(ctx, storetest.ReferenceNow-86400)
	if err != nil {
		t.Fatalf("reading the gaps: %v", err)
	}
	if len(gaps) != 2 || gaps[0].DetectedAt != storetest.ReferenceNow-60 || gaps[1].DetectedAt != storetest.ReferenceNow-7200 {
		t.Fatalf("read %+v, want the two gaps of the window newest first", gaps)
	}
	if gaps[0].Kind != "firewall_log" || gaps[0].ProviderKey != "pf" || gaps[0].MissedSeconds() != 30 ||
		!gaps[0].HasDetail || gaps[0].Detail != detail {
		t.Errorf("a gap was read as %+v", gaps[0])
	}
}

// TestSourceAvailabilityIsTheDiagnosticQuery: the read returns exactly what the query
// `-- diagnostic: Source availability` returns, row for row, including a provider
// whose availability row is missing.
func TestSourceAvailabilityIsTheDiagnosticQuery(t *testing.T) {
	t.Parallel()
	database, _ := openTestStore(t)
	ctx := context.Background()
	providerID, err := database.ProviderID(ctx, "dhcp_lease", "kea")
	if err != nil {
		t.Fatalf("looking up the provider: %v", err)
	}
	detail := "HTTP 403 from the example endpoint"
	if err := database.SetAvailability(ctx, providerID, vocab.StateUnavailable, vocab.OutcomeDenied, "GET /example", &detail,
		storetest.ReferenceNow); err != nil {
		t.Fatalf("recording availability: %v", err)
	}
	missing, err := database.ProviderID(ctx, "dhcp_lease", "isc")
	if err != nil {
		t.Fatalf("looking up the provider: %v", err)
	}
	if _, err := database.DB().Exec("DELETE FROM source_availability WHERE provider_id = ?", missing); err != nil {
		t.Fatalf("deleting an availability row: %v", err)
	}

	query := diagnosticQuery(t, "Source availability")
	rows, err := database.DB().Query(query)
	if err != nil {
		t.Fatalf("running the diagnostic: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var expected []string
	for rows.Next() {
		var (
			kind, key, name string
			state, outcome  sql.NullString
			probe           sql.NullString
			detailValue     sql.NullString
			checked         sql.NullInt64
			active          int64
		)
		if err := rows.Scan(&kind, &key, &name, &state, &outcome, &probe, &detailValue, &checked, &active); err != nil {
			t.Fatalf("reading the diagnostic: %v", err)
		}
		expected = append(expected, fmt.Sprintf("%s|%s|%s|%t|%s|%s|%s|%t|%s|%d|%d", kind, key, name, state.Valid,
			state.String, outcome.String, probe.String, detailValue.Valid, detailValue.String, checked.Int64, active))
	}
	read, err := database.SourceAvailability(ctx)
	if err != nil {
		t.Fatalf("reading the availability: %v", err)
	}
	var got []string
	for _, row := range read {
		got = append(got, fmt.Sprintf("%s|%s|%s|%t|%s|%s|%s|%t|%s|%d|%d", row.Kind, row.ProviderKey,
			row.DisplayName, row.HasAvailability, row.State, row.Outcome, row.Probe, row.HasDetail, row.Detail,
			row.CheckedAt, zeroOrOne(row.IsActive)))
	}
	if strings.Join(got, "\n") != strings.Join(expected, "\n") || len(got) == 0 {
		t.Errorf("the read differs from the diagnostic:\nread:\n%s\ndiagnostic:\n%s",
			strings.Join(got, "\n"), strings.Join(expected, "\n"))
	}
}

// zeroOrOne writes a flag as the diagnostic query prints it.
func zeroOrOne(value bool) int64 {
	if value {
		return 1
	}
	return 0
}

// diagnosticQuery returns one query of sql/queries/diagnostics.sql by its name.
func diagnosticQuery(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "sql", "queries", "diagnostics.sql"))
	if err != nil {
		t.Fatalf("reading the diagnostics: %v", err)
	}
	text := string(raw)
	start := strings.Index(text, "-- diagnostic: "+name+"\n")
	if start < 0 {
		t.Fatalf("there is no diagnostic called %s", name)
	}
	// The comment above a query may carry a semicolon of its own prose, so the query
	// is read from its SELECT.
	text = text[start:]
	text = text[strings.Index(text, "\nSELECT")+1:]
	return text[:strings.Index(text, ";")]
}

// TestInterfacesAreReadInTheOrderTheyWereStored, at two and at five interfaces.
func TestInterfacesAreReadInTheOrderTheyWereStored(t *testing.T) {
	t.Parallel()
	for _, count := range []int{2, 5} {
		database, _ := openTestStore(t)
		ctx := context.Background()
		var ids []int64
		// Written in an order no name would sort them into.
		for index := count - 1; index >= 0; index-- {
			id, err := database.UpsertInterface(ctx, vocab.Interface{
				Identifier: fmt.Sprintf("example_if_%d", index), Device: fmt.Sprintf("exdev%d", index),
				Description: fmt.Sprintf("inside-%d.example", index), LinkType: "example-link",
				LinkKind: "other", IsUpstream: index == 0,
			}, storetest.ReferenceNow)
			if err != nil {
				t.Fatalf("writing an interface: %v", err)
			}
			ids = append(ids, id)
		}
		if err := database.SetLinkLocalEvidence(ctx, ids[1], true); err != nil {
			t.Fatalf("setting the link-local rule: %v", err)
		}
		rows, err := database.Interfaces(ctx)
		if err != nil {
			t.Fatalf("reading the interfaces: %v", err)
		}
		if len(rows) != count {
			t.Fatalf("read %d interfaces of %d", len(rows), count)
		}
		for index, row := range rows {
			if row.ID != ids[index] {
				t.Errorf("interface %d was read at position %d", row.ID, index)
			}
			if row.LinkLocalEvidence != (row.ID == ids[1]) || row.IsUpstream != (row.Identifier == "example_if_0") {
				t.Errorf("interface %d was read as %+v", row.ID, row)
			}
		}
	}
}
