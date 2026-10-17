package surfaces_test

import (
	"strings"

	"github.com/NaejEL/opnview/internal/web/webtest"
)

// visibleLines returns the non-empty text nodes of a document.
func visibleLines(document string) []string {
	var lines []string
	for _, line := range strings.Split(webtest.VisibleText(document), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			lines = append(lines, trimmed)
		}
	}
	return lines
}
