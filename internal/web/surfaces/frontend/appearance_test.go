package frontend_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/NaejEL/opnview/internal/web/webtest"
)

// F7-AC6's server half: with nothing stored the appearance answers no value set; each refusal of a
// write answers its code and writes nothing; a saved palette and mode persist across a restart and
// are what a second browser reads.

type appearanceAnswer struct {
	Palette        *string `json:"palette"`
	Mode           *string `json:"mode"`
	Language       *string `json:"language"`
	DefaultPalette string  `json:"default_palette"`
	Palettes       []struct {
		ID string `json:"id"`
	} `json:"palettes"`
	Modes []string `json:"modes"`
}

func readAppearance(t *testing.T, harness *webtest.Harness) appearanceAnswer {
	t.Helper()
	var answer appearanceAnswer
	if status := getJSON(t, harness, "/api/appearance", &answer); status != http.StatusOK {
		t.Fatalf("GET /api/appearance answered %d", status)
	}
	return answer
}

func TestTheAppearanceRefusesWhatIsNotOfferedAndPersistsWhatIs(t *testing.T) {
	t.Parallel()
	harness := webtest.New(t)
	fresh := readAppearance(t, harness)
	if fresh.Palette != nil || fresh.Mode != nil || fresh.Language != nil || fresh.DefaultPalette != "industrial" ||
		len(fresh.Palettes) != 6 || len(fresh.Modes) != 3 {
		t.Fatalf("a fresh installation answers %+v", fresh)
	}
	harness.CompleteSetup()
	token := harness.SessionFormToken()
	for body, code := range map[string]string{
		`{"palette": "example-none"}`:              "palette_unknown",
		`{"mode": "sepia"}`:                        "mode_unknown",
		`{"language": "xx"}`:                       "language_unknown",
		`{"palette": "nord", "mode": "sepia"}`:     "mode_unknown",
		`{"palette": 7}`:                           "palette_unknown",
		`{"palette": "nord", "colour": "example"}`: "malformed_body",
	} {
		status, answer := webtest.StatusAndBody(t, harness.PostJSON("/api/appearance", body, token, ""))
		var refused map[string]string
		_ = json.Unmarshal([]byte(answer), &refused)
		if status != http.StatusBadRequest || refused["condition"] != code {
			t.Errorf("%s answered %d %v, want %s", body, status, refused, code)
		}
		if after := readAppearance(t, harness); after.Palette != nil || after.Mode != nil {
			t.Errorf("the refused write %s stored %+v", body, after)
		}
	}
	if status, _ := webtest.StatusAndBody(t, harness.PostJSON("/api/appearance", `{"palette": "nord"}`, "", "")); status != http.StatusForbidden {
		t.Errorf("a write without the form token answered %d", status)
	}
	status, _ := webtest.StatusAndBody(t, harness.PostJSON("/api/appearance", `{"palette": "nord", "mode": "dark"}`, token, ""))
	if status != http.StatusOK {
		t.Fatalf("a valid write answered %d", status)
	}
	second := webtest.FreshClient(t, harness)
	if seen := readAppearance(t, second); seen.Palette == nil || *seen.Palette != "nord" || seen.Mode == nil ||
		*seen.Mode != "dark" {
		t.Errorf("a second browser reads %+v", seen)
	}
	restarted := harness.Reopen()
	if seen := readAppearance(t, restarted); seen.Palette == nil || *seen.Palette != "nord" {
		t.Errorf("after a restart the appearance is %+v", seen)
	}
	anonymous := webtest.FreshClient(t, restarted)
	anonymousToken, _, _ := anonymous.SessionToken()
	if status, _ := webtest.StatusAndBody(t, anonymous.PostJSON("/api/appearance", `{"mode": "light"}`, anonymousToken, "")); status != http.StatusUnauthorized {
		t.Errorf("a write without a session answered %d", status)
	}
}
