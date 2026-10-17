package frontjs_test

import (
	"encoding/json"
	"testing"

	"github.com/NaejEL/opnview/internal/front/frontjs"
)

// The first-launch half of F7-AC6 that runs without a browser: with no appearance stored, the
// document's data-theme follows the operating system's preference, the palette is the default, and
// the language is the browser's first installed one, else English. A stored palette and mode are
// drawn as stored. The language selector is offered only when two or more languages are installed.

func appearancePage(t *testing.T, prefersDark bool, browser []string, stored map[string]any,
	languages []map[string]string) *frontjs.Runtime {
	t.Helper()
	server := newFakeServer(t, true, true)
	for key, value := range stored {
		server.appearance[key] = value
	}
	server.on("GET /api/appearance", func(frontjsRequest) frontjsAnswer {
		answer := server.appearanceAnswer()
		installed := []any{}
		for _, language := range languages {
			installed = append(installed, language)
		}
		answer["languages"] = installed
		return ok(answer)
	})
	server.on("GET /api/strings/fr", func(frontjsRequest) frontjsAnswer {
		return ok(map[string]any{"language": "fr", "strings": map[string]string{"language.name": "Français"}})
	})
	page := frontjs.New(t, frontjs.Options{Handler: server.handle, PrefersDark: prefersDark, Languages: browser})
	page.Start("/")
	return page
}

var englishOnly = []map[string]string{{"code": "en", "name": "English"}}

func TestTheFirstLaunchFollowsTheOperatingSystemAndTheBrowser(t *testing.T) {
	t.Parallel()
	dark := appearancePage(t, true, []string{"fr-FR", "en"}, nil, englishOnly)
	if got := dark.Run(`document.documentElement.getAttribute("data-theme")`).String(); got != "industrial-dark" {
		t.Errorf("with no appearance stored and a dark system, data-theme is %q", got)
	}
	if got := dark.Run(`document.documentElement.getAttribute("lang")`).String(); got != "en" {
		t.Errorf("with French wanted and not installed, the language is %q", got)
	}
	light := appearancePage(t, false, []string{"en"}, nil, englishOnly)
	if got := light.Run(`document.documentElement.getAttribute("data-theme")`).String(); got != "industrial-light" {
		t.Errorf("with no appearance stored and a light system, data-theme is %q", got)
	}
	if light.Count("#language-select") != 0 {
		t.Error("a language selector is offered with one language installed")
	}

	both := []map[string]string{{"code": "en", "name": "English"}, {"code": "fr", "name": "Français"}}
	french := appearancePage(t, false, []string{"fr-CA", "en"}, nil, both)
	if got := french.Run(`document.documentElement.getAttribute("lang")`).String(); got != "fr" {
		t.Errorf("with French installed and wanted first, the language is %q", got)
	}
	if french.Count("#language-select") != 1 {
		t.Error("no language selector with two languages installed")
	}
}

func TestAStoredPaletteAndModeAreDrawnAndSaved(t *testing.T) {
	t.Parallel()
	page := appearancePage(t, true, nil, map[string]any{"palette": "nord", "mode": "light"}, englishOnly)
	if got := page.Run(`document.documentElement.getAttribute("data-theme")`).String(); got != "nord-light" {
		t.Errorf("a stored Nord light is drawn as %q", got)
	}
	page.Choose("#mode-select", "dark")
	body := lastWrite(t, page.Writes(), "/api/appearance")
	raw, _ := json.Marshal(body)
	if string(raw) != `{"mode":"dark"}` {
		t.Errorf("the mode selector saved %s", raw)
	}
	if got := page.Run(`document.documentElement.getAttribute("data-theme")`).String(); got != "nord-dark" {
		t.Errorf("after saving dark the document is %q", got)
	}
}
