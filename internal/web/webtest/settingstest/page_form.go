package settingstest

import (
	"strings"
	"testing"
)

// AssertDefaultButtonNamesNoCard holds a page's one form to its first button -- the
// one the Enter key presses -- being a hidden one that names no card.
func AssertDefaultButtonNamesNoCard(t *testing.T, page, action string) {
	t.Helper()
	opening := `<form method="post" action="` + action + `"`
	if strings.Count(page, opening) != 1 {
		t.Fatalf("the page does not carry exactly one form posting to %s", action)
	}
	form := page[strings.Index(page, opening):]
	first := form[strings.Index(form, "<button"):]
	first = first[:strings.Index(first, ">")]
	if strings.Contains(first, "name=") || !strings.Contains(first, `class="visually-hidden"`) {
		t.Errorf("the form's first button, the Enter key's, is not a hidden one naming no card: %s", first)
	}
}
