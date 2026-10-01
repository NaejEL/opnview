package web

import (
	"os"
	"strings"
	"testing"

	"github.com/NaejEL/opnview/internal/secret"
)

// Reading a rendered page the way a reader does.
//
// SEVERAL CRITERIA ARE ABOUT WHAT A READER SEES rather than about what the HTML
// contains: no user-visible literal, no explanatory copy, no off-host resource. So
// the tests need the text nodes, separated from the markup, and they need it computed
// from the rendered page rather than from the template source — because what ships is
// the rendered page.

// visibleText returns the text nodes of an HTML document, with the markup removed.
//
// It is deliberately crude and deliberately strict: everything between < and > goes,
// and what is left is what a reader sees. The templates contain no script and no
// inline style, so there is nothing whose contents would survive the strip and not be
// visible — and if one were ever added, this would start reporting its source as
// visible text, which is the right failure.
func visibleText(document string) string {
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

// visibleLines returns the non-empty text nodes of a document.
func visibleLines(document string) []string {
	var lines []string
	for _, line := range strings.Split(visibleText(document), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			lines = append(lines, trimmed)
		}
	}
	return lines
}

// reopenWithoutKeyFile removes the key file and builds a second server over the same
// database, which is the lost-key-file state exactly as an operator would meet it:
// the database intact, the key gone.
func reopenWithoutKeyFile(t *testing.T, from *harness) *harness {
	t.Helper()
	from.http.Close()
	if err := from.store.Close(); err != nil {
		t.Fatalf("closing the database: %v", err)
	}
	if err := os.Remove(secret.KeyPath(from.dataDir)); err != nil {
		t.Fatalf("removing the key file: %v", err)
	}
	return newHarnessIn(t, from.dataDir, false)
}
