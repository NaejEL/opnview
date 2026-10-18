package frontjs_test

import (
	"encoding/json"
	"strings"
	"testing"
)

// The Settings view performs every action its JSON routes offer (F7-AC14, Settings), and the
// generic settings form renders a declaration of each setting type from a synthetic plugin's
// manifest, refusing in the form what the declaration refuses and showing the server's refusal
// against its field (F7-AC15). No front file names the synthetic plugin: it exists only here.

const secretTyped = "secret-typed-in-the-test"

// settingsAnswer is GET /api/settings, as a save leaves it.
func settingsAnswer(verification string) map[string]any {
	return map[string]any{
		"firewall": map[string]any{"url": "https://firewall.example", "api_key": "key-of-the-test",
			"certificate_fingerprint": nil, "credential_state": "ready", "verification": verification},
	}
}

// syntheticPlugin is a plugin declaring one setting of each type.
func syntheticPlugin(secretSet bool) map[string]any {
	declarations := []any{
		map[string]any{"key": "count", "type": "integer", "min": 1, "max": 10, "default": 5, "value": 5, "set": false,
			"label": "plugin.example_settings.setting.count"},
		map[string]any{"key": "ratio", "type": "number", "min": 0, "max": 1, "default": 0.5, "value": 0.5, "set": false,
			"label": "plugin.example_settings.setting.ratio"},
		map[string]any{"key": "enabled", "type": "boolean", "default": false, "value": false, "set": false,
			"label": "plugin.example_settings.setting.enabled"},
		map[string]any{"key": "greeting", "type": "text", "default": "", "value": "", "set": false,
			"label": "plugin.example_settings.setting.greeting"},
		map[string]any{"key": "flavour", "type": "enum", "values": []string{"plain", "salted"}, "default": "plain",
			"value": "plain", "set": false, "label": "plugin.example_settings.setting.flavour"},
		map[string]any{"key": "token", "type": "secret", "value": nil, "set": secretSet,
			"label": "plugin.example_settings.setting.token"},
	}
	return map[string]any{"name": "example_settings", "display_name": "Example settings", "origin": "data_directory",
		"state": "loaded", "settings": declarations}
}

func settingsServer(t *testing.T) *fakeServer {
	server := newFakeServer(t, true, true)
	server.extraStrings = map[string]string{
		"plugin.example_settings.setting.count": "Count", "plugin.example_settings.setting.ratio": "Ratio",
		"plugin.example_settings.setting.enabled": "Enabled", "plugin.example_settings.setting.greeting": "Greeting",
		"plugin.example_settings.setting.flavour": "Flavour", "plugin.example_settings.setting.token": "Token",
	}
	server.on("GET /api/settings", func(frontjsRequest) frontjsAnswer { return ok(settingsAnswer("not_attempted")) })
	server.on("POST /api/settings/firewall", func(request frontjsRequest) frontjsAnswer {
		return ok(settingsAnswer("verified"))
	})
	server.on("POST /api/settings/firewall/certificate", func(frontjsRequest) frontjsAnswer {
		return ok(map[string]any{"presented_fingerprint": strings.Repeat("ab", 32)})
	})
	secretSet := false
	server.on("GET /api/plugins", func(frontjsRequest) frontjsAnswer {
		return ok(map[string]any{"plugins": []any{syntheticPlugin(secretSet),
			map[string]any{"name": "broken_example", "origin": "data_directory", "state": "refused",
				"reason":   map[string]string{"code": "manifest_invalid", "detail": "plugin.json: unknown field"},
				"settings": []any{}}}})
	})
	server.on("POST /api/plugins/example_settings/settings", func(request frontjsRequest) frontjsAnswer {
		var body map[string]any
		_ = json.Unmarshal([]byte(request.Body), &body)
		if body["greeting"] == "refused by the server" {
			return frontjsAnswer{Status: 400, Body: map[string]string{"condition": "invalid_setting_value", "key": "greeting"}}
		}
		if _, sent := body["token"]; sent {
			secretSet = true
		}
		plugin := syntheticPlugin(secretSet)
		return ok(map[string]any{"plugin": "example_settings", "settings": plugin["settings"]})
	})
	liveSettings := map[string]any{"settings": []any{map[string]any{"key": "live_interest_seconds", "value": 60,
		"default": 60, "minimum": 10, "maximum": 3600, "set": false}}, "cadences": []any{}}
	server.on("GET /api/live/settings", func(frontjsRequest) frontjsAnswer { return ok(liveSettings) })
	server.on("GET /api/widgets", func(frontjsRequest) frontjsAnswer {
		return ok(map[string]any{"widgets": []any{}, "shapes": []any{},
			"themes":    []any{map[string]any{"id": "industrial", "origin": "builtin", "file": "front/themes/industrial.json"}},
			"languages": []any{map[string]any{"code": "en", "origin": "builtin", "file": "front/locale/en.json", "missing_strings": []any{}}},
			"refused": []any{map[string]any{"part": "theme", "id": "broken", "code": "theme_token_missing",
				"path": "/data/themes/broken.json", "detail": "modes.light leaves out the token \"accent\""}}})
	})
	return server
}

// lastWrite is the last POST to a path, failing when there is none.
func lastWrite(t *testing.T, writes []frontjsRequest, path string) map[string]any {
	t.Helper()
	for index := len(writes) - 1; index >= 0; index-- {
		if writes[index].Path == path {
			var body map[string]any
			if err := json.Unmarshal([]byte(writes[index].Body), &body); err != nil {
				t.Fatalf("the write to %s is not JSON: %v", path, err)
			}
			return body
		}
	}
	t.Fatalf("nothing was written to %s; the writes were %v", path, writes)
	return nil
}

func TestTheSettingsViewSavesTheFirewallCardAndFetchesTheCertificate(t *testing.T) {
	t.Parallel()
	page := settingsServer(t).page(t, "/settings")
	if page.Count("#card-firewall") != 1 {
		t.Fatalf("no firewall card:\n%s", page.Outline())
	}
	// The secret is never read back: the field starts empty, and the card states its state.
	if value := page.Run(`document.getElementById("firewall-api-secret").value`).String(); value != "" {
		t.Errorf("the API secret field holds %q", value)
	}
	page.Click(`#card-firewall button[type="button"]`)
	if body := lastWrite(t, page.Writes(), "/api/settings/firewall/certificate"); body["url"] != "https://firewall.example" {
		t.Errorf("the certificate fetch sent %v", body)
	}
	if page.Count("#card-firewall .presented") != 1 {
		t.Fatalf("the presented fingerprint is not shown:\n%s", page.Outline())
	}
	page.Click(`#card-firewall .presented button`)
	page.Input("#firewall-api-secret", secretTyped)
	page.Click(`#card-firewall button[type="submit"]`)
	body := lastWrite(t, page.Writes(), "/api/settings/firewall")
	if body["certificate_fingerprint"] != strings.Repeat("ab", 32) || body["api_secret"] != secretTyped ||
		body["url"] != "https://firewall.example" || body["api_key"] != "key-of-the-test" {
		t.Errorf("the firewall save sent %v", body)
	}
	if page.Count("#card-firewall .form-notice") != 1 {
		t.Errorf("the save is not acknowledged:\n%s", page.Outline())
	}
	if strings.Contains(page.Outline(), secretTyped) {
		t.Error("the secret typed is still in the document after the save")
	}
}

// TestTheSettingsViewDrawsNoCardForARouteThatIsGone (carried item 6 of Lot 4): the Public Suffix List's and
// NetFlow's settings are their plugins' (PA8 of the open-plugin-API amendment), drawn by the generic plugin
// form; the view draws no card of its own for either and reads neither /api/netflow/settings nor
// /api/settings/public-suffix, which the server no longer answers.
func TestTheSettingsViewDrawsNoCardForARouteThatIsGone(t *testing.T) {
	t.Parallel()
	page := settingsServer(t).page(t, "/settings")
	if page.Count("#card-firewall") != 1 || page.Count("#card-live") != 1 {
		t.Fatalf("the firewall and live cards are not drawn:\n%s", page.Outline())
	}
	for _, card := range []string{"#card-public-suffix", "#card-netflow"} {
		if page.Count(card) != 0 {
			t.Errorf("the view draws %s, whose route is gone:\n%s", card, page.Outline())
		}
	}
	for _, request := range page.Requests() {
		if request.Path == "/api/netflow/settings" || request.Path == "/api/settings/public-suffix" {
			t.Errorf("the view requested %s %s, which the server no longer answers", request.Method, request.Path)
		}
	}
}

func TestTheGenericFormSavesOneSettingOfEachTypeIncludingASecret(t *testing.T) {
	t.Parallel()
	page := settingsServer(t).page(t, "/settings")
	card := "#card-plugin-example_settings"
	for _, id := range []string{"count", "ratio", "enabled", "greeting", "flavour", "token"} {
		if page.Count("#plugin-example_settings-"+id) != 1 {
			t.Fatalf("the setting %s is not drawn from its declaration:\n%s", id, page.Outline())
		}
	}
	if page.Attribute("#plugin-example_settings-token", "type") != "password" {
		t.Error("the secret is not a write-only field")
	}
	page.Input("#plugin-example_settings-count", "7")
	page.Input("#plugin-example_settings-ratio", "0.25")
	page.Choose("#plugin-example_settings-enabled", "true")
	page.Input("#plugin-example_settings-greeting", "hello")
	page.Choose("#plugin-example_settings-flavour", "salted")
	page.Input("#plugin-example_settings-token", secretTyped)
	page.Click(card + ` button[type="submit"]`)
	body := lastWrite(t, page.Writes(), "/api/plugins/example_settings/settings")
	want := map[string]any{"count": float64(7), "ratio": 0.25, "enabled": true, "greeting": "hello", "flavour": "salted",
		"token": secretTyped}
	for key, value := range want {
		if body[key] != value {
			t.Errorf("the save sent %s=%v, want %v (body %v)", key, body[key], value, body)
		}
	}
	outline := page.Outline()
	if strings.Contains(outline, secretTyped) {
		t.Error("the secret's value is in the document after the save")
	}
	if !strings.Contains(page.Text(card), englishCatalogue(t)["state.secret.set"]) {
		t.Errorf("the secret's state is not shown:\n%s", outline)
	}
}

func TestTheGenericFormRefusesInTheFormAndShowsTheServersRefusalAgainstItsField(t *testing.T) {
	t.Parallel()
	page := settingsServer(t).page(t, "/settings")
	card := "#card-plugin-example_settings"
	before := len(page.Writes())
	page.Input("#plugin-example_settings-count", "11")
	page.Click(card + ` button[type="submit"]`)
	if len(page.Writes()) != before {
		t.Fatalf("a value out of bounds was sent: %v", page.Writes()[before:])
	}
	if page.Attribute("#plugin-example_settings-count", "aria-invalid") != "true" ||
		page.Count(card+" #plugin-example_settings-count-error") != 1 {
		t.Fatalf("the refusal is not against its field:\n%s", page.Outline())
	}
	if !strings.Contains(page.Text(card+" #plugin-example_settings-count-error"),
		englishCatalogue(t)["error.setting_out_of_range"]) {
		t.Errorf("the refusal does not name out of range: %q", page.Text(card+" #plugin-example_settings-count-error"))
	}
	page.Input("#plugin-example_settings-count", "5")
	page.Input("#plugin-example_settings-greeting", "refused by the server")
	page.Click(card + ` button[type="submit"]`)
	if page.Count(card+" #plugin-example_settings-greeting-error") != 1 {
		t.Fatalf("the server's refusal is not against its field:\n%s", page.Outline())
	}
	if !strings.Contains(page.Text(card+" #plugin-example_settings-greeting-error"),
		englishCatalogue(t)["error.invalid_setting_value"]) {
		t.Errorf("the server's refusal code is not shown")
	}
}

func TestTheSettingsViewListsPluginsAndFrontPartsWithTheirReasons(t *testing.T) {
	t.Parallel()
	page := settingsServer(t).page(t, "/settings")
	text := page.Text("#card-parts")
	for _, want := range []string{"broken_example", "manifest_invalid", "theme_token_missing", "/data/themes/broken.json",
		"industrial"} {
		if !strings.Contains(text, want) {
			t.Errorf("the listing does not show %q:\n%s", want, text)
		}
	}
}
