package store_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/NaejEL/opnview/internal/store"
	"github.com/NaejEL/opnview/internal/store/plugintables"
	"github.com/NaejEL/opnview/internal/store/storetest"
	"github.com/NaejEL/opnview/internal/store/vocab"
)

// PA-AC25 of specs/SPEC-backend-completion-plugins.md: a pass writes its rows in a number of statements
// independent of its row count, and the aggregate refresh after it runs a number of statements
// independent of the slots it touches. Each test runs the same write twice, once small and once large,
// on databases of their own, and compares what a statement counter saw.

// statementsFor runs write on a fresh store under a statement counter and returns how many write
// statements, and how many statements in all, it ran.
func statementsFor(t *testing.T, arrange func(*store.Store), write func(*store.Store)) (int, int) {
	t.Helper()
	database, _ := openTestStore(t)
	if arrange != nil {
		arrange(database)
	}
	counter := storetest.CountStatements(t, database)
	write(database)
	return counter.Writes(), counter.Total()
}

// manyFlows is n filter-log records, one second apart, ending at the given instant.
func manyFlows(n int, endAt int64) []vocab.Flow {
	flows := make([]vocab.Flow, n)
	for index := range flows {
		flow := exampleFlow(index)
		flow.LogDigest = fmt.Sprintf("example-digest-%06d", index)
		flow.SrcAddress = fmt.Sprintf("192.0.2.%d", index%250+1)
		flow.DstAddress = fmt.Sprintf("198.51.100.%d", index%250+1)
		flow.ObservedAt = endAt - int64(index)
		flows[index] = flow
	}
	return flows
}

func TestAFilterLogPassOfTenAndOfAThousandRecordsRunsTheSameWriteStatements(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pass := func(n int) func(*store.Store) {
		return func(database *store.Store) {
			flows := manyFlows(n, 1750000000)
			digests := make([]string, len(flows))
			for index, flow := range flows {
				digests[index] = flow.LogDigest
			}
			if _, err := database.StoredFlowDigests(ctx, digests); err != nil {
				t.Fatal(err)
			}
			if err := database.InsertFlows(ctx, flows); err != nil {
				t.Fatal(err)
			}
			if stored := count(t, database, "flow"); stored != n {
				t.Fatalf("%d flows stored of %d", stored, n)
			}
		}
	}
	tenWrites, tenTotal := statementsFor(t, nil, pass(10))
	thousandWrites, thousandTotal := statementsFor(t, nil, pass(1000))
	if tenWrites != thousandWrites || tenTotal != thousandTotal || thousandWrites != 1 {
		t.Errorf("ten records ran %d write statements (%d in all), a thousand %d (%d in all); want one write each",
			tenWrites, tenTotal, thousandWrites, thousandTotal)
	}
}

func TestALeasePassOfTenAndOfAThousandLeasesRunsTheSameWriteStatements(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pass := func(n int) func(*store.Store) {
		return func(database *store.Store) {
			providerID, err := database.ProviderID(ctx, "dhcp_lease", "kea")
			if err != nil {
				t.Fatal(err)
			}
			clients := make([]vocab.Client, n)
			leases := make([]vocab.DHCPLease, n)
			for index := range clients {
				mac := fmt.Sprintf("00:00:5e:00:%02x:%02x", index/256, index%256)
				address := fmt.Sprintf("10.%d.%d.%d", index/65536, index/256%256, index%256)
				clients[index] = vocab.Client{Identity: vocab.ClientIdentity{Kind: vocab.IdentityMAC, Key: mac},
					MAC: &mac, LastAddress: &address}
				expiry := int64(1750003600)
				leases[index] = vocab.DHCPLease{Backend: "kea", Address: address, MAC: &mac, LeaseState: "active",
					GenerationKey: vocab.GenerationKeyOf(nil, &expiry, 0), ExpiresAt: &expiry, ObservedAt: 1750000000}
			}
			ids, err := database.UpsertClients(ctx, clients, 1750000000)
			if err != nil {
				t.Fatal(err)
			}
			for index := range leases {
				leases[index].ClientID = &ids[index]
			}
			if err := database.InsertDHCPLeases(ctx, providerID, leases); err != nil {
				t.Fatal(err)
			}
			if stored := count(t, database, "dhcp_lease"); stored != n {
				t.Fatalf("%d leases stored of %d", stored, n)
			}
		}
	}
	tenWrites, _ := statementsFor(t, nil, pass(10))
	thousandWrites, _ := statementsFor(t, nil, pass(1000))
	if tenWrites != thousandWrites || thousandWrites != 2 {
		t.Errorf("ten leases ran %d write statements, a thousand %d; want two each (the clients, the leases)",
			tenWrites, thousandWrites)
	}
}

func TestAPluginPassOfTenAndOfAThousandRowsRunsTheSameWriteStatements(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	arrange := func(database *store.Store) {
		if _, err := database.EnsurePluginTable(ctx, exampleReadings("example_one")); err != nil {
			t.Fatal(err)
		}
	}
	pass := func(n int) func(*store.Store) {
		return func(database *store.Store) {
			rows := make([]map[string]any, n)
			for index := range rows {
				rows[index] = map[string]any{"sampled_at": int64(1750000000 - index), "subject": "example-subject",
					"value": float64(index)}
			}
			if err := database.WritePluginPass(ctx, "example_one", "example_provider",
				plugintables.PluginRows{"readings": rows}, map[string]string{"cursor": "1"}, 1750000000); err != nil {
				t.Fatal(err)
			}
		}
	}
	tenWrites, _ := statementsFor(t, arrange, pass(10))
	thousandWrites, _ := statementsFor(t, arrange, pass(1000))
	if tenWrites != thousandWrites {
		t.Errorf("ten rows ran %d write statements, a thousand %d", tenWrites, thousandWrites)
	}
}

func TestTheRefreshOfOneAndOfAHundredTouchedHoursRunsTheSameStatements(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	// Long past every current slot, so neither refresh rewrites a slot only because it is current.
	const lastHour = int64(1749999600)
	refresh := func(hours int) (func(*store.Store), func(*store.Store)) {
		arrange := func(database *store.Store) {
			flows := make([]vocab.Flow, hours)
			for index := range flows {
				flow := exampleFlow(index)
				flow.LogDigest = fmt.Sprintf("example-digest-%06d", index)
				flow.ObservedAt = lastHour - int64(index)*3600
				flows[index] = flow
			}
			if err := database.InsertFlows(ctx, flows); err != nil {
				t.Fatal(err)
			}
		}
		write := func(database *store.Store) {
			result, err := storetest.RefreshAggregates(ctx, database, 0, lastHour+40*86400, nil)
			if err != nil {
				t.Fatal(err)
			}
			hoursRewritten := 0
			for _, slot := range result.Rewritten {
				if slot.Period == vocab.PeriodHour.Name {
					hoursRewritten++
				}
			}
			// The touched hours and the current one.
			if hoursRewritten != hours+1 {
				t.Fatalf("the refresh rewrote %d hours, want %d", hoursRewritten, hours+1)
			}
		}
		return arrange, write
	}
	arrangeOne, writeOne := refresh(1)
	arrangeHundred, writeHundred := refresh(100)
	_, oneTotal := statementsFor(t, arrangeOne, writeOne)
	_, hundredTotal := statementsFor(t, arrangeHundred, writeHundred)
	if oneTotal != hundredTotal {
		t.Errorf("one touched hour ran %d statements, a hundred %d", oneTotal, hundredTotal)
	}
}

// TestTheRefreshNumbersItsRowsOncePerStatementWithoutGaps is the aggregate row numbering of the budget work
// in Lot 3 of the plugin-API amendment: the refresh's inserts number their rows themselves, under the
// row-id lock, so every aggregate table's ids are gap-free -- 1 to its row count on a store written by
// refreshes alone, the next rows numbered on from the largest -- and a row a writer inserts with no id
// still takes the next number from the row-id trigger.
func TestTheRefreshNumbersItsRowsOncePerStatementWithoutGaps(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, _ := openTestStore(t)
	const lastHour = int64(1749999600)
	insertHours := func(first, count int) {
		flows := make([]vocab.Flow, count)
		for index := range flows {
			flow := exampleFlow(first + index)
			flow.LogDigest = fmt.Sprintf("numbering-digest-%06d", first+index)
			flow.SrcAddress = fmt.Sprintf("192.0.2.%d", (first+index)%250+1)
			flow.ObservedAt = lastHour - int64(first+index)*3600
			flows[index] = flow
		}
		if err := database.InsertFlows(ctx, flows); err != nil {
			t.Fatal(err)
		}
	}
	tables := []string{}
	for _, family := range []string{"volume", "rule_volume", "domain_volume", "peer_volume", "client_volume",
		"owner_volume"} {
		for _, period := range vocab.Periods() {
			tables = append(tables, family+"_aggregate_"+period.Name)
		}
	}
	idsOf := func(table string) (count, low, high, distinct int64) {
		t.Helper()
		if err := database.DB().QueryRowContext(ctx, `SELECT count(*), coalesce(min(id), 0), coalesce(max(id), 0),
		    count(DISTINCT id) FROM `+table).Scan(&count, &low, &high, &distinct); err != nil {
			t.Fatal(err)
		}
		return count, low, high, distinct
	}
	insertHours(0, 50)
	if _, err := storetest.RefreshAggregates(ctx, database, 0, lastHour+40*86400, nil); err != nil {
		t.Fatal(err)
	}
	before := map[string]int64{}
	written := false
	for _, table := range tables {
		count, low, high, distinct := idsOf(table)
		if count > 0 && (low != 1 || high != count || distinct != count) {
			t.Errorf("%s holds %d rows numbered %d to %d (%d distinct), want 1 to %d", table, count, low, high,
				distinct, count)
		}
		written = written || count > 0
		before[table] = high
	}
	if !written {
		t.Fatal("the refresh wrote no aggregate row")
	}
	// Older hours, refreshed after: their rows are numbered on from the largest id, with no gap.
	insertHours(50, 30)
	if _, err := storetest.RefreshAggregates(ctx, database, 0, lastHour+40*86400, nil); err != nil {
		t.Fatal(err)
	}
	for _, table := range tables {
		var newRows, newLow, newHigh int64
		if err := database.DB().QueryRowContext(ctx, `SELECT count(*), coalesce(min(id), 0), coalesce(max(id), 0)
		    FROM `+table+` WHERE id > $1`, before[table]).Scan(&newRows, &newLow, &newHigh); err != nil {
			t.Fatal(err)
		}
		if newRows > 0 && (newLow != before[table]+1 || newHigh != before[table]+newRows) {
			t.Errorf("%s numbered its %d new rows %d to %d, want %d to %d", table, newRows, newLow, newHigh,
				before[table]+1, before[table]+newRows)
		}
		if _, _, high, distinct := idsOf(table); high > 0 {
			if count, _, _, _ := idsOf(table); distinct != count {
				t.Errorf("%s holds %d rows and %d distinct ids", table, count, distinct)
			}
		}
	}
	// A writer that names no id still takes the next number from the trigger.
	var assigned, largest int64
	if err := database.DB().QueryRowContext(ctx, "SELECT coalesce(max(id), 0) FROM volume_aggregate_1h").
		Scan(&largest); err != nil {
		t.Fatal(err)
	}
	if err := database.DB().QueryRowContext(ctx, `INSERT INTO volume_aggregate_1h (period_start_at, period_end_at,
	    peer_address, traffic_scope, traffic_direction, bytes, allowed_bytes, blocked_bytes, unknown_bytes, allowed_connections,
	    blocked_connections, unknown_connections, measure_parts, computed_at)
	    VALUES (3600, 7200, '203.0.113.9', 'north_south', 'outbound', 0, 0, 0, 0, 0, 0, 0, '{}', 7200) RETURNING id`).
		Scan(&assigned); err != nil {
		t.Fatal(err)
	}
	if assigned != largest+1 {
		t.Errorf("a row inserted with no id took %d, want %d", assigned, largest+1)
	}
}
