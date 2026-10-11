package webtest

import (
	"net/http"
	"strings"
	"testing"
)

// followed reads the answer to a submission: a 303 back to a surface is followed, and
// the page it lands on returned with the 303; any other answer is read as it is.
func Followed(t *testing.T, h *Harness, answer *http.Response) (int, string) {
	t.Helper()
	status, body := StatusAndBody(t, answer)
	if status != http.StatusSeeOther {
		return status, body
	}
	location := answer.Header.Get("Location")
	if cut := strings.Index(location, "#"); cut >= 0 {
		location = location[:cut]
	}
	if !strings.HasPrefix(location, "/") {
		t.Fatalf("a save redirected off the surface, to %q", location)
	}
	return status, PageSource(t, h, location)
}

// assertDefaultButtonNamesNoCard holds a page's one form to its first button -- the
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
