package surfaces_test

import (
	"github.com/NaejEL/opnview/internal/web/webtest"
	"testing"
)

// databaseBytes returns every text and blob value in the database, concatenated.
//
// It is the coarser instrument: where databaseContains answers "is this string
// anywhere", this answers "what is in there at all", which is what a test asserting
// the ABSENCE of a previous ciphertext needs — the previous value is bytes rather
// than a string somebody chose.
func databaseBytes(t *testing.T, harness *webtest.Harness) string {
	t.Helper()
	return webtest.DatabaseBytesExcept(t, harness)
}
