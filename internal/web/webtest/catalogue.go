package webtest

import (
	"net/http"
	"strings"
	"testing"
)

// StripCSSComments removes /* … */ from a stylesheet, so what is scanned is the rules
// rather than the prose explaining them.
func StripCSSComments(source string) string { return StripDelimited(source, "/*", "*/") }

// StripDelimited removes everything from each opening to the next closing, both included; an
// opening with no closing removes the rest.
func StripDelimited(source, opening, closing string) string {
	var builder strings.Builder
	for {
		start := strings.Index(source, opening)
		if start < 0 {
			builder.WriteString(source)
			return builder.String()
		}
		builder.WriteString(source[:start])
		end := strings.Index(source[start:], closing)
		if end < 0 {
			return builder.String()
		}
		source = source[start+end+len(closing):]
	}
}

// RenderedPage is one surface in one state, named so a failure says which.
type RenderedPage struct {
	Name     string
	Document string
}

// Discard reads and closes a response nothing asserts on. It is the redirect that
// carries a message to the page the reader lands on: the body is empty and the page
// that matters is the next one.
func Discard(t *testing.T, answer *http.Response) {
	t.Helper()
	_, _ = StatusAndBody(t, answer)
}
