package purgeconcurrency_test

import (
	"os"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/store"
)

// TestMain runs these tests in a zone that is not UTC, as internal/collect's TestMain does: an instant
// read from the local clock instead of the firewall's lands hours out.
func TestMain(m *testing.M) {
	if err := os.Setenv("TZ", "Australia/Sydney"); err != nil {
		panic("purgeconcurrency_test: setting TZ: " + err.Error())
	}
	location, err := time.LoadLocation("Australia/Sydney")
	if err != nil {
		panic("purgeconcurrency_test: the zone database does not carry Australia/Sydney: " + err.Error())
	}
	time.Local = location
	os.Exit(m.Run())
}

// interfaceIDOf reads one interface's id by its identifier.
func interfaceIDOf(t *testing.T, database *store.Store, identifier string) int64 {
	t.Helper()
	var id int64
	if err := database.DB().QueryRow("SELECT id FROM interface WHERE identifier = $1", identifier).Scan(&id); err != nil {
		t.Fatalf("reading interface %s: %v", identifier, err)
	}
	return id
}
