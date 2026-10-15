package store

import (
	"database/sql"
	"testing"

	"github.com/NaejEL/opnview/internal/store/sqldb"
)

// TestAttributionNameLookupsSearchByNameAndClientOnBothBranches is AC22's Go half
// (B2 of specs/SPEC-step-5b-widget-api.md): the exact attribution's search for one
// name's lookups is an index search bounded by the name AND the client on both
// branches of its client predicate. sql/schema-checks.sh asserts the same plans at
// both seed sizes.
func TestAttributionNameLookupsSearchByNameAndClientOnBothBranches(t *testing.T) {
	t.Parallel()
	database, _ := openTestStore(t)
	text, err := sqldb.Statement("attribution_name_lookups")
	if err != nil {
		t.Fatal(err)
	}
	var arguments []any
	for _, parameter := range []string{"client_id", "start", "end"} {
		arguments = append(arguments, sql.Named(parameter, referenceNow))
	}
	for _, parameter := range []string{"name", "address"} {
		arguments = append(arguments, sql.Named(parameter, "example"))
	}
	plan := planOf(t, database, text, arguments...)
	// Each branch reads dns_resolution through an index leading on the name. Which of the two name
	// indexes the planner picks for a branch depends on the table's statistics; the scale checks of
	// sql/schema-checks.sh assert the choice on the analysed seed.
	for _, alias := range []string{"r", "a"} {
		if !usesIndex(plan, "idx_dns_resolution_name_client_id", alias) &&
			!usesIndex(plan, "idx_dns_resolution_name_client_address", alias) {
			t.Errorf("the branch %s does not read a name index: %+v", alias, plan)
		}
	}
	if scans(plan, "dns_resolution") {
		t.Errorf("the plan scans dns_resolution: %+v", plan)
	}
}

// TestAttributionNameLookupsKeepBothClientBranches: the two branches return what the
// one OR returned -- a lookup by the flow's client_id, and a lookup by its address when
// either side has no client_id -- and never one lookup twice.
func TestAttributionNameLookupsKeepBothClientBranches(t *testing.T) {
	t.Parallel()
	database, _ := openTestStore(t)
	db := database.DB()
	exec := func(query string, arguments ...any) {
		t.Helper()
		if _, err := db.Exec(query, arguments...); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}
	exec(`INSERT INTO client (id, identity_kind, identity_key, first_seen_at, last_seen_at)
	      VALUES (1, 'mac', '02:00:00:00:00:01', 0, 0), (2, 'mac', '02:00:00:00:00:02', 0, 0)`)
	insert := func(id int64, clientID any, address string) {
		exec(`INSERT INTO dns_resolution (id, resolver, lookup_key, domain, action, answer_source,
		      client_address, client_id, client_resolution, looked_up_at, ingested_at)
		      VALUES (?, 'unbound', ?, 'Example.ORG.', 'pass', 'Recursion', ?, ?, 'logged_address', ?, ?)`,
			id, "example-lookup-"+string(rune('a'+id)), address, clientID, referenceNow-10, referenceNow)
	}
	insert(1, int64(1), "192.0.2.10") // the flow's client, by id
	insert(2, nil, "192.0.2.10")      // the flow's address, no client_id
	insert(3, int64(2), "192.0.2.10") // another client at the same address
	insert(4, nil, "2001:db8::10")    // another address
	text, err := sqldb.Statement("attribution_name_lookups")
	if err != nil {
		t.Fatal(err)
	}
	ids := func(clientID any) []int64 {
		t.Helper()
		rows, err := db.Query(text, sql.Named("name", "example.org"), sql.Named("client_id", clientID),
			sql.Named("address", "192.0.2.10"), sql.Named("start", referenceNow-60), sql.Named("end", referenceNow))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = rows.Close() }()
		var found []int64
		for rows.Next() {
			var id int64
			var domain, key string
			var at int64
			var source sql.NullString
			if err := rows.Scan(&id, &domain, &at, &key, &source); err != nil {
				t.Fatal(err)
			}
			found = append(found, id)
		}
		return found
	}
	if got := ids(int64(1)); !sameIDs(got, []int64{1, 2}) {
		t.Errorf("with a client_id the search found %v, want the client's lookup and the address's 1 and 2", got)
	}
	if got := ids(nil); !sameIDs(got, []int64{1, 2, 3}) {
		t.Errorf("with no client_id the search found %v, want every lookup at the address", got)
	}
}

func sameIDs(got, want []int64) bool {
	if len(got) != len(want) {
		return false
	}
	seen := map[int64]int{}
	for _, id := range got {
		seen[id]++
	}
	for _, id := range want {
		if seen[id] != 1 {
			return false
		}
	}
	return true
}
