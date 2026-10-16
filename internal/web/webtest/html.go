package webtest

import (
	"github.com/NaejEL/opnview/internal/secret"
	"os"
	"strings"
	"testing"
)

// VisibleText returns the text nodes of an HTML document, with the markup removed.
//
// It is deliberately crude and deliberately strict: everything between < and > goes,
// and what is left is what a reader sees. The templates contain no script and no
// inline style, so there is nothing whose contents would survive the strip and not be
// visible — and if one were ever added, this would start reporting its source as
// visible text, which is the right failure.
func VisibleText(document string) string {
	var builder strings.Builder
	inTag := false
	for index := 0; index < len(document); index++ {
		switch character := document[index]; {
		case character == '<':
			inTag = true
		case character == '>':
			inTag = false
			builder.WriteByte('\n')
		case !inTag:
			builder.WriteByte(character)
		}
	}
	return builder.String()
}

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
