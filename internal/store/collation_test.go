package store_test

import (
	"slices"
	"sort"
	"testing"
)

// TestTextOrdersAsBytesAndFoldsAsciiAlone is P-AC7's first half: on the database opnview runs on,
// mixed-case and non-ASCII names order exactly as their bytes do, and lower() folds ASCII letters and
// leaves every other letter as it is -- the byte-order rule docs/architecture.md, "Storage", states and
// the store enforces by refusing a database not created with the C collation.
func TestTextOrdersAsBytesAndFoldsAsciiAlone(t *testing.T) {
	t.Parallel()
	database, _ := openTestStore(t)
	names := []string{"beta", "Beta", "BETA", "école", "École", "zeta", "Zeta", "_under", "alpha-2",
		"alpha10", "alpha2", "über", "Uber", "日本"}
	rows, err := database.DB().Query("SELECT name FROM (SELECT unnest(CAST($1 AS text[])) AS name) AS n ORDER BY name",
		"{"+joinQuoted(names)+"}")
	if err != nil {
		t.Fatal(err)
	}
	var ordered []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		ordered = append(ordered, name)
	}
	_ = rows.Close()
	expected := append([]string(nil), names...)
	sort.Strings(expected)
	if !slices.Equal(ordered, expected) {
		t.Errorf("the database orders %q, byte order is %q", ordered, expected)
	}
	var folded string
	if err := database.DB().QueryRow("SELECT lower($1)", "ÉCOLE.Example.ÜBER").Scan(&folded); err != nil {
		t.Fatal(err)
	}
	if folded != "École.example.Über" {
		t.Errorf("lower() folds to %q, not ASCII alone", folded)
	}
}

// joinQuoted renders names as the elements of a PostgreSQL array literal.
func joinQuoted(names []string) string {
	result := ""
	for index, name := range names {
		if index > 0 {
			result += ","
		}
		result += `"` + name + `"`
	}
	return result
}
