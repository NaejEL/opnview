package webtest

import (
	"os"
	"testing"

	"github.com/NaejEL/opnview/internal/secret"
)

// ReopenWithoutKeyFile removes the key file and builds a second server over the same
// database, which is the lost-key-file state exactly as an operator would meet it:
// the database intact, the key gone.
func ReopenWithoutKeyFile(t *testing.T, from *Harness) *Harness {
	t.Helper()
	from.HTTP.Close()
	if err := from.Store.Close(); err != nil {
		t.Fatalf("closing the database: %v", err)
	}
	if err := os.Remove(secret.KeyPath(from.DataDir)); err != nil {
		t.Fatalf("removing the key file: %v", err)
	}
	return NewIn(t, from.DataDir, false)
}
