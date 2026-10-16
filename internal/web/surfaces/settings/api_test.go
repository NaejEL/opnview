package settings_test

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/collect/ipgeolocation"
	"github.com/NaejEL/opnview/internal/collect/registrabledomain"
	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/store/vocab"
	"github.com/NaejEL/opnview/internal/web"
	"github.com/NaejEL/opnview/internal/web/settingfields"
	"github.com/NaejEL/opnview/internal/web/surfaces/settings"
	"github.com/NaejEL/opnview/internal/web/webtest"
	"github.com/NaejEL/opnview/internal/web/webtest/collectiontest"
	"github.com/NaejEL/opnview/internal/web/webtest/settingstest"
)

// The settings surface's JSON equivalents (C-AC9): every figure of the page in the JSON answer,
// and every save of a card made, and refused, as the page makes and refuses it.

// storedRows renders the setting rows and the names of the stored credentials, with the harness's
// own firewall address replaced by a placeholder so two harnesses compare.
func storedRows(t *testing.T, h *webtest.Harness) string {
	t.Helper()
	var builder strings.Builder
	rows, err := h.Store.DB().Query("SELECT key, value FROM setting ORDER BY key")
	if err != nil {
		t.Fatalf("reading the settings: %v", err)
	}
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			t.Fatalf("reading the settings: %v", err)
		}
		builder.WriteString(key + "=" + strings.ReplaceAll(value, h.Fake.BaseURL(), "<firewall>") + "\n")
	}
	_ = rows.Close()
	names, err := h.Store.DB().Query("SELECT name FROM encrypted_credential ORDER BY name")
	if err != nil {
		t.Fatalf("reading the credentials: %v", err)
	}
	for names.Next() {
		var name string
		if err := names.Scan(&name); err != nil {
			t.Fatalf("reading the credentials: %v", err)
		}
		builder.WriteString("credential " + name + "\n")
	}
	_ = names.Close()
	return builder.String()
}

// pageAndJSON is one input given to the page and to its JSON route, on two installations.
type pageAndJSON struct {
	what string
	// form is the page's submission, built against the harness it is sent to; path and body the
	// JSON route's.
	form func(h *webtest.Harness) url.Values
	path string
	body func(h *webtest.Harness) map[string]any
	// refused is true for an input both must refuse, changing nothing.
	refused bool
}

// assertSameChange gives one input to the page on one installation and to the JSON route on
// another, and asserts both stored the same rows, or both refused it and stored nothing.
func assertSameChange(t *testing.T, cases []pageAndJSON) {
	t.Helper()
	for _, input := range cases {
		t.Run(input.what, func(t *testing.T) {
			t.Parallel()
			pageSide, jsonSide := collectiontest.SignedInHarness(t), collectiontest.SignedInHarness(t)
			pageBefore, jsonBefore := storedRows(t, pageSide), storedRows(t, jsonSide)
			answer := pageSide.Post(web.PathSettings, input.form(pageSide))
			webtest.Discard(t, answer)
			status, decoded := settingstest.SendJSON(jsonSide, input.path, input.body(jsonSide))
			if input.refused {
				if answer.StatusCode != http.StatusBadRequest {
					t.Errorf("the page answered %d rather than refusing", answer.StatusCode)
				}
				if status != http.StatusBadRequest || decoded["condition"] == nil {
					t.Errorf("the JSON route answered %d %v rather than refusing", status, decoded)
				}
				if storedRows(t, pageSide) != pageBefore || storedRows(t, jsonSide) != jsonBefore {
					t.Error("a refused input changed a row")
				}
				return
			}
			if answer.StatusCode != http.StatusSeeOther {
				t.Errorf("the page answered %d rather than saving", answer.StatusCode)
			}
			if status != http.StatusOK {
				t.Errorf("the JSON route answered %d %v rather than saving", status, decoded)
			}
			if page, json := storedRows(t, pageSide), storedRows(t, jsonSide); page != json {
				t.Errorf("the page and the JSON route stored different rows:\npage:\n%s\njson:\n%s", page, json)
			}
		})
	}
}

// TestTheSettingsJSONSavesMakeThePagesChangeAndRefuseItsInputs is C-AC9 for POST /settings, card
// by card.
func TestTheSettingsJSONSavesMakeThePagesChangeAndRefuseItsInputs(t *testing.T) {
	t.Parallel()
	apiKey, apiSecret := webtest.RandomHex(t, 8), webtest.RandomHex(t, 16)
	firewall := func(firewallURL func(h *webtest.Harness) string, key, secretValue, fingerprint string,
		refused bool, what string) pageAndJSON {
		return pageAndJSON{what: what, refused: refused, path: settings.PathAPIFirewall,
			form: func(h *webtest.Harness) url.Values {
				return url.Values{webtest.FieldSettingsCard: {webtest.SettingsCardFirewall},
					"firewall_url": {firewallURL(h)}, "api_key": {key}, "api_secret": {secretValue},
					"certificate_fingerprint": {fingerprint}}
			},
			body: func(h *webtest.Harness) map[string]any {
				return map[string]any{"url": firewallURL(h), "api_key": key, "api_secret": secretValue,
					"certificate_fingerprint": fingerprint}
			}}
	}
	fake := func(h *webtest.Harness) string { return h.Fake.BaseURL() }
	invalid := func(*webtest.Harness) string { return "ftp://firewall.invalid" }
	theme := func(value string, refused bool) pageAndJSON {
		return pageAndJSON{what: "theme " + value, refused: refused, path: settings.PathAPITheme,
			form: func(*webtest.Harness) url.Values {
				return url.Values{webtest.FieldSettingsCard: {webtest.SettingsCardAppearance}, "theme": {value}}
			},
			body: func(*webtest.Harness) map[string]any { return map[string]any{"theme": value} }}
	}
	interval := func(seconds string, refused bool) pageAndJSON {
		field := config.KeyPublicSuffixRefreshInterval
		return pageAndJSON{what: "public suffix interval " + seconds, refused: refused,
			path: settings.PathAPIPublicSuffix,
			form: func(*webtest.Harness) url.Values {
				return url.Values{webtest.FieldSettingsCard: {webtest.SettingsCardPublicSuffix}, field: {seconds},
					settingfields.UnitFieldName(field): {settingfields.UnitSeconds}}
			},
			body: func(*webtest.Harness) map[string]any {
				if number, err := strconv.ParseInt(seconds, 10, 64); err == nil {
					return map[string]any{"refresh_interval_seconds": number}
				}
				return map[string]any{"refresh_interval_seconds": seconds}
			}}
	}
	assertSameChange(t, []pageAndJSON{
		firewall(fake, apiKey, apiSecret, "", false, "the firewall's URL, key and secret"),
		firewall(fake, "", "", "", false, "the firewall's URL alone"),
		firewall(invalid, apiKey, apiSecret, "", true, "a URL that is no firewall API's"),
		firewall(fake, apiKey, apiSecret, "not a fingerprint", true, "a malformed fingerprint"),
		firewall(fake, "", apiSecret, "", true, "a secret with no key"),
		theme(string(web.ThemeDark), false),
		theme("sepia", true),
		interval("7200", false),
		interval("0", true),
		interval("seven", true),
	})
}

// TestTheMaxMindCardsJSONEquivalentIsTheMaxMindPluginsSettings is C-AC9 for the geolocation card:
// the maxmind plugin's declared settings store the rows the page stores, refuse what it refuses,
// and null removes the account ID as an empty field does.
func TestTheMaxMindCardsJSONEquivalentIsTheMaxMindPluginsSettings(t *testing.T) {
	t.Parallel()
	licence := webtest.RandomHex(t, 16)
	path := "/api/plugins/" + settings.GeolocationPlugin + "/settings"
	geolocation := func(what, accountID string, body map[string]any, refused bool) pageAndJSON {
		return pageAndJSON{what: what, refused: refused, path: path,
			form: func(*webtest.Harness) url.Values {
				form := url.Values{webtest.FieldSettingsCard: {webtest.SettingsCardGeolocation},
					"maxmind_account_id": {accountID}}
				if _, set := body["licence_key"]; set {
					form.Set("maxmind_licence_key", licence)
				}
				return form
			},
			body: func(*webtest.Harness) map[string]any { return body }}
	}
	assertSameChange(t, []pageAndJSON{
		geolocation("an account ID and a licence key", "100001",
			map[string]any{"account_id": 100001, "licence_key": licence}, false),
		geolocation("an account ID that is no number", "first", map[string]any{"account_id": "first"}, true),
		geolocation("an account ID of nought", "0", map[string]any{"account_id": 0}, true),
	})

	t.Run("an emptied account ID is removed by both", func(t *testing.T) {
		t.Parallel()
		pageSide, jsonSide := collectiontest.SignedInHarness(t), collectiontest.SignedInHarness(t)
		for _, h := range []*webtest.Harness{pageSide, jsonSide} {
			if status, answer := settingstest.SendJSON(h, path, map[string]any{"account_id": 100001}); status != http.StatusOK {
				t.Fatalf("storing an account ID answered %d %v", status, answer)
			}
		}
		webtest.Discard(t, pageSide.Post(web.PathSettings, url.Values{
			webtest.FieldSettingsCard: {webtest.SettingsCardGeolocation}, "maxmind_account_id": {""}}))
		if status, answer := settingstest.SendJSON(jsonSide, path, map[string]any{"account_id": nil}); status != http.StatusOK {
			t.Fatalf("clearing the account ID answered %d %v", status, answer)
		}
		for _, h := range []*webtest.Harness{pageSide, jsonSide} {
			if _, present, err := h.Store.Setting(t.Context(), config.KeyMaxMindAccountID); err != nil || present {
				t.Errorf("the account ID is still stored (%v)", err)
			}
		}
	})
}

// TestTheFirewallCertificateIsFetchedAndStoredNowhere is C-AC9 for the page's fetch control.
func TestTheFirewallCertificateIsFetchedAndStoredNowhere(t *testing.T) {
	t.Parallel()
	h := webtest.NewTLS(t)
	h.CompleteSetup()
	before := storedRows(t, h)
	status, answer := settingstest.SendJSON(h, settings.PathAPIFirewallCertificate, map[string]any{"url": h.Fake.BaseURL()})
	if status != http.StatusOK || answer["presented_fingerprint"] != h.Fake.Fingerprint() {
		t.Errorf("fetching the certificate answered %d %v, the firewall presents %s", status, answer,
			h.Fake.Fingerprint())
	}
	for _, refused := range []struct{ url, condition string }{
		{"", "firewall_url_required"},
		{"ftp://firewall.invalid", "firewall_url_invalid"},
	} {
		status, answer := settingstest.SendJSON(h, settings.PathAPIFirewallCertificate, map[string]any{"url": refused.url})
		if status != http.StatusBadRequest || answer["condition"] != refused.condition {
			t.Errorf("fetching from %q answered %d %v rather than %s", refused.url, status, answer, refused.condition)
		}
	}
	if storedRows(t, h) != before {
		t.Error("fetching a certificate stored something")
	}
}

// TestTheSettingsJSONCarriesEveryFigureOfThePage is C-AC9's figures: every figure the page draws is
// in the JSON answer, compared value by value on an empty installation and on a configured one.
func TestTheSettingsJSONCarriesEveryFigureOfThePage(t *testing.T) {
	t.Parallel()
	check := func(t *testing.T, h *webtest.Harness) {
		t.Helper()
		figures, err := settings.PageFigures(t.Context(), h.Server)
		if err != nil {
			t.Fatalf("reading the page's figures: %v", err)
		}
		answer := settingstest.GetJSON(h, settings.PathAPISettings)
		text := func(path ...any) string {
			value, _ := settingstest.Path(answer, path...).(string)
			return value
		}
		number := func(path ...any) (int64, bool) {
			value, ok := settingstest.Path(answer, path...).(float64)
			return int64(value), ok
		}
		instant := func(path ...any) string {
			if value, ok := number(path...); ok {
				return settingfields.Instant(value)
			}
			return ""
		}
		state := func(name, code string) {
			t.Helper()
			if figures[name] == "" || web.ConditionOf(web.MessageKey(figures[name])) != code {
				t.Errorf("the page draws %s as %q and the answer as %q", name, figures[name], code)
			}
		}
		same := func(name, value string) {
			t.Helper()
			if figures[name] != value {
				t.Errorf("the page draws %s as %q and the answer as %q", name, figures[name], value)
			}
		}
		same("firewall_url", text("firewall", "url"))
		same("api_key", text("firewall", "api_key"))
		same("certificate_fingerprint", text("firewall", "certificate_fingerprint"))
		state("credential_state", text("firewall", "credential_state"))
		state("verification", text("firewall", "verification"))
		state("licence_key_state", text("geolocation", "licence_key_state"))
		same("account_id", text("geolocation", "account_id"))
		state("geoip_state", text("geolocation", "download_state"))
		same("geoip_build", instant("geolocation", "build_at"))
		state("public_suffix.state", text("public_suffix", "state"))
		inUse, _ := settingstest.Path(answer, "public_suffix", "in_use").(bool)
		state("public_suffix.in_use", map[bool]string{true: "yes", false: "no"}[inUse])
		same("public_suffix.version", text("public_suffix", "version"))
		stated, _ := settingstest.Path(answer, "public_suffix", "version_stated").(bool)
		if (figures["public_suffix.version_state"] != "") != (inUse && !stated) {
			t.Errorf("the page's unstated version (%q) disagrees with the answer's (in use %v, stated %v)",
				figures["public_suffix.version_state"], inUse, stated)
		}
		rules := ""
		if value, ok := number("public_suffix", "rules"); ok && inUse {
			rules = strconv.FormatInt(value, 10)
		}
		same("public_suffix.rules", rules)
		same("public_suffix.confirmed", instant("public_suffix", "confirmed_at"))
		if (figures["public_suffix.confirmed_state"] != "") != (settingstest.Path(answer, "public_suffix", "confirmed_at") == nil) {
			t.Errorf("the page's never-confirmed state (%q) disagrees with the answer", figures["public_suffix.confirmed_state"])
		}
		seconds, _ := number("public_suffix", "refresh_interval_seconds")
		figure, unit := settingfields.FieldDuration(seconds)
		same("public_suffix.interval", figure+" "+unit)
		same("theme", text("theme", "value"))
	}

	t.Run("an installation with nothing configured", func(t *testing.T) {
		t.Parallel()
		check(t, collectiontest.SignedInHarness(t))
	})
	t.Run("an installation with everything configured", func(t *testing.T) {
		t.Parallel()
		h := collectiontest.SignedInHarness(t)
		webtest.Discard(t, settingstest.StoreCredentials(h, webtest.RandomHex(t, 8), webtest.RandomHex(t, 16)))
		if status, answer := settingstest.SendJSON(h, "/api/plugins/"+settings.GeolocationPlugin+"/settings",
			map[string]any{"account_id": 100001, "licence_key": webtest.RandomHex(t, 16)}); status != http.StatusOK {
			t.Fatalf("storing the MaxMind settings answered %d %v", status, answer)
		}
		h.GeoIP.SetBuild(h.Clock.Now().Add(-time.Hour).Unix())
		settingstest.SetGeoIPProbe(h, vocab.StateReachable, ipgeolocation.ProbeCurrent)
		h.Suffix.Hold("2026-09-20_00-00-00_UTC", 9876, h.Clock.Now().Add(-time.Hour).Unix())
		settingstest.SetPublicSuffixProbe(h, vocab.StateReachable, registrabledomain.ProbeDownloaded, nil)
		if status, answer := settingstest.SendJSON(h, settings.PathAPITheme, map[string]any{"theme": "dark"}); status != http.StatusOK {
			t.Fatalf("storing the theme answered %d %v", status, answer)
		}
		check(t, h)
	})
	t.Run("a copy in use that states no version", func(t *testing.T) {
		t.Parallel()
		h := collectiontest.SignedInHarness(t)
		h.Suffix.Hold("", 12, 0)
		check(t, h)
	})
}
