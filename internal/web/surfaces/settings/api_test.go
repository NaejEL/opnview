package settings_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/collect/ipgeolocation"
	"github.com/NaejEL/opnview/internal/collect/registrabledomain"
	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/store/accounts"
	"github.com/NaejEL/opnview/internal/store/vocab"
	"github.com/NaejEL/opnview/internal/web/surfaces/settings"
	"github.com/NaejEL/opnview/internal/web/webtest"
	"github.com/NaejEL/opnview/internal/web/webtest/collectiontest"
	"github.com/NaejEL/opnview/internal/web/webtest/settingstest"
)

// The settings surface's JSON routes (C-AC9): every figure of the settings in the JSON answer, and
// every save made, and refused, with nothing changed by a refusal.

// storedRows renders the setting rows and the names of the stored credentials, with the harness's
// own firewall address replaced by a placeholder.
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

// save is one input to a JSON save: the rows it must leave, or the code it must be refused with.
type save struct {
	what string
	path string
	body func(h *webtest.Harness) map[string]any
	// refusal is the code a refused input is answered with, changing nothing; empty for a save.
	refusal string
	// rows are the rows a save must leave, in storedRows' form, each line one of them.
	rows []string
}

// assertSaves gives each input to its JSON route on a fresh installation and asserts what it
// stored, or that it was refused with its code and stored nothing.
func assertSaves(t *testing.T, cases []save) {
	t.Helper()
	for _, input := range cases {
		t.Run(input.what, func(t *testing.T) {
			t.Parallel()
			h := collectiontest.SignedInHarness(t)
			before := storedRows(t, h)
			status, decoded := settingstest.SendJSON(h, input.path, input.body(h))
			after := storedRows(t, h)
			if input.refusal != "" {
				if status != http.StatusBadRequest || decoded["condition"] != input.refusal {
					t.Errorf("answered %d %v rather than refusing %s", status, decoded, input.refusal)
				}
				if after != before {
					t.Errorf("a refused input changed a row:\nbefore:\n%s\nafter:\n%s", before, after)
				}
				return
			}
			if status != http.StatusOK {
				t.Errorf("answered %d %v rather than saving", status, decoded)
			}
			for _, row := range input.rows {
				if !strings.Contains(after, row+"\n") {
					t.Errorf("the save did not leave %q; rows:\n%s", row, after)
				}
			}
		})
	}
}

// TestTheSettingsJSONSavesStoreAndRefuseTheirInputs is C-AC9 for the firewall card and the Public
// Suffix List's interval.
func TestTheSettingsJSONSavesStoreAndRefuseTheirInputs(t *testing.T) {
	t.Parallel()
	apiKey, apiSecret := webtest.RandomHex(t, 8), webtest.RandomHex(t, 16)
	firewall := func(what string, firewallURL func(h *webtest.Harness) string, key, secretValue, fingerprint,
		refusal string, rows ...string) save {
		return save{what: what, path: settings.PathAPIFirewall, refusal: refusal, rows: rows,
			body: func(h *webtest.Harness) map[string]any {
				return map[string]any{"url": firewallURL(h), "api_key": key, "api_secret": secretValue,
					"certificate_fingerprint": fingerprint}
			}}
	}
	fake := func(h *webtest.Harness) string { return h.Fake.BaseURL() }
	invalid := func(*webtest.Harness) string { return "ftp://firewall.invalid" }
	interval := func(seconds any, refusal string, rows ...string) save {
		return save{what: fmt.Sprintf("the Public Suffix List interval %v", seconds),
			path: settings.PathAPIPublicSuffix, refusal: refusal, rows: rows,
			body: func(*webtest.Harness) map[string]any { return map[string]any{"refresh_interval_seconds": seconds} }}
	}
	assertSaves(t, []save{
		firewall("the firewall's URL, key and secret", fake, apiKey, apiSecret, "", "",
			config.KeyOPNsenseBaseURL+"=<firewall>", config.KeyOPNsenseAPIKey+"="+apiKey,
			"credential "+accounts.CredentialOPNsenseAPISecret),
		firewall("the firewall's URL alone", fake, "", "", "", "", config.KeyOPNsenseBaseURL+"=<firewall>"),
		firewall("a URL that is no firewall API's", invalid, apiKey, apiSecret, "", "firewall_url_invalid"),
		firewall("a malformed fingerprint", fake, apiKey, apiSecret, "not a fingerprint", "fingerprint_invalid"),
		firewall("a secret with no key", fake, "", apiSecret, "", "api_key_required"),
		interval(7200, "", config.KeyPublicSuffixRefreshInterval+"=7200"),
		interval(0, "collection_value_invalid"),
		interval("seven", "collection_value_invalid"),
	})
}

// TestTheMaxMindSettingsAreTheMaxMindPluginsSettings is C-AC9 for the geolocation settings: the
// maxmind plugin's declared settings store the rows the settings answer reports, refuse what is
// not an account ID, and null removes the account ID.
func TestTheMaxMindSettingsAreTheMaxMindPluginsSettings(t *testing.T) {
	t.Parallel()
	licence := webtest.RandomHex(t, 16)
	path := "/api/plugins/" + settings.GeolocationPlugin + "/settings"
	geolocation := func(what string, body map[string]any, refused bool, rows ...string) save {
		refusal := ""
		if refused {
			refusal = "invalid_setting_value"
		}
		return save{what: what, path: path, refusal: refusal, rows: rows,
			body: func(*webtest.Harness) map[string]any { return body }}
	}
	assertSaves(t, []save{
		geolocation("an account ID and a licence key", map[string]any{"account_id": 100001, "licence_key": licence},
			false, config.KeyMaxMindAccountID+"=100001", "credential "+accounts.CredentialMaxMindLicenceKey),
		geolocation("an account ID that is no number", map[string]any{"account_id": "first"}, true),
	})

	t.Run("an account ID of nought is refused", func(t *testing.T) {
		t.Parallel()
		h := collectiontest.SignedInHarness(t)
		before := storedRows(t, h)
		status, answer := settingstest.SendJSON(h, path, map[string]any{"account_id": 0})
		if status != http.StatusBadRequest || answer["condition"] == nil {
			t.Errorf("an account ID of nought answered %d %v rather than a refusal", status, answer)
		}
		if storedRows(t, h) != before {
			t.Error("a refused account ID changed a row")
		}
	})

	t.Run("a null account ID removes it", func(t *testing.T) {
		t.Parallel()
		h := collectiontest.SignedInHarness(t)
		if status, answer := settingstest.SendJSON(h, path, map[string]any{"account_id": 100001}); status != http.StatusOK {
			t.Fatalf("storing an account ID answered %d %v", status, answer)
		}
		if status, answer := settingstest.SendJSON(h, path, map[string]any{"account_id": nil}); status != http.StatusOK {
			t.Fatalf("clearing the account ID answered %d %v", status, answer)
		}
		if _, present, err := h.Store.Setting(t.Context(), config.KeyMaxMindAccountID); err != nil || present {
			t.Errorf("the account ID is still stored (%v)", err)
		}
		if got := settingstest.GetJSON(h, settings.PathAPISettings); settingstest.Path(got, "geolocation", "account_id") != nil {
			t.Errorf("the settings answer still reports an account ID: %v", got["geolocation"])
		}
	})
}

// TestTheFirewallCertificateIsFetchedAndStoredNowhere is C-AC9 for the certificate fetch.
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

// TestTheSettingsJSONCarriesEveryFigure is C-AC9's figures: each figure of the settings answer is
// the one stored or held, on an empty installation and on a configured one, and a figure with no
// answer is null beside its named state, never a nought.
func TestTheSettingsJSONCarriesEveryFigure(t *testing.T) {
	t.Parallel()
	expect := func(t *testing.T, answer map[string]any, path []any, want any) {
		t.Helper()
		if got := settingstest.Path(answer, path...); got != want {
			t.Errorf("%v is %#v, want %#v", path, got, want)
		}
	}

	t.Run("an installation with nothing configured", func(t *testing.T) {
		t.Parallel()
		h := collectiontest.SignedInHarness(t)
		answer := settingstest.GetJSON(h, settings.PathAPISettings)
		for _, absent := range [][]any{{"firewall", "url"}, {"firewall", "api_key"},
			{"firewall", "certificate_fingerprint"}, {"geolocation", "account_id"}, {"geolocation", "build_at"},
			{"public_suffix", "version"}, {"public_suffix", "rules"}, {"public_suffix", "confirmed_at"}} {
			expect(t, answer, absent, nil)
		}
		expect(t, answer, []any{"firewall", "credential_state"}, "absent")
		expect(t, answer, []any{"firewall", "verification"}, "not_attempted")
		expect(t, answer, []any{"geolocation", "licence_key_state"}, "absent")
		expect(t, answer, []any{"geolocation", "download_state"}, "not_downloaded")
		expect(t, answer, []any{"geolocation", "plugin"}, settings.GeolocationPlugin)
		expect(t, answer, []any{"public_suffix", "state"}, "not_yet_refreshed")
		expect(t, answer, []any{"public_suffix", "in_use"}, false)
		defaults, err := config.Load(t.Context(), h.Store)
		if err != nil {
			t.Fatalf("loading the defaults: %v", err)
		}
		expect(t, answer, []any{"public_suffix", "refresh_interval_seconds"},
			float64(defaults.PublicSuffixRefreshInterval/time.Second))
		if _, present := answer["theme"]; present {
			t.Error("the settings answer still carries a theme: the appearance is the front's")
		}
	})

	t.Run("an installation with everything configured", func(t *testing.T) {
		t.Parallel()
		h := collectiontest.SignedInHarness(t)
		apiKey := webtest.RandomHex(t, 8)
		webtest.Discard(t, settingstest.StoreCredentials(h, apiKey, webtest.RandomHex(t, 16)))
		if status, answer := settingstest.SendJSON(h, "/api/plugins/"+settings.GeolocationPlugin+"/settings",
			map[string]any{"account_id": 100001, "licence_key": webtest.RandomHex(t, 16)}); status != http.StatusOK {
			t.Fatalf("storing the MaxMind settings answered %d %v", status, answer)
		}
		build := h.Clock.Now().Add(-time.Hour).Unix()
		h.GeoIP.SetBuild(build)
		settingstest.SetGeoIPProbe(h, vocab.StateReachable, ipgeolocation.ProbeCurrent)
		confirmed := h.Clock.Now().Add(-time.Hour).Unix()
		h.Suffix.Hold("2026-09-20_00-00-00_UTC", 9876, confirmed)
		settingstest.SetPublicSuffixProbe(h, vocab.StateReachable, registrabledomain.ProbeDownloaded, nil)

		answer := settingstest.GetJSON(h, settings.PathAPISettings)
		expect(t, answer, []any{"firewall", "url"}, h.Fake.BaseURL())
		expect(t, answer, []any{"firewall", "api_key"}, apiKey)
		expect(t, answer, []any{"firewall", "credential_state"}, "ready")
		expect(t, answer, []any{"geolocation", "account_id"}, "100001")
		expect(t, answer, []any{"geolocation", "licence_key_state"}, "stored")
		expect(t, answer, []any{"geolocation", "download_state"}, "current")
		expect(t, answer, []any{"geolocation", "build_at"}, float64(build))
		expect(t, answer, []any{"public_suffix", "state"}, "downloaded")
		expect(t, answer, []any{"public_suffix", "in_use"}, true)
		expect(t, answer, []any{"public_suffix", "version"}, "2026-09-20_00-00-00_UTC")
		expect(t, answer, []any{"public_suffix", "version_stated"}, true)
		expect(t, answer, []any{"public_suffix", "rules"}, float64(9876))
		expect(t, answer, []any{"public_suffix", "confirmed_at"}, float64(confirmed))
	})

	t.Run("a copy in use that states no version", func(t *testing.T) {
		t.Parallel()
		h := collectiontest.SignedInHarness(t)
		h.Suffix.Hold("", 12, 0)
		answer := settingstest.GetJSON(h, settings.PathAPISettings)
		expect(t, answer, []any{"public_suffix", "in_use"}, true)
		expect(t, answer, []any{"public_suffix", "version"}, nil)
		expect(t, answer, []any{"public_suffix", "version_stated"}, false)
		expect(t, answer, []any{"public_suffix", "rules"}, float64(12))
		expect(t, answer, []any{"public_suffix", "confirmed_at"}, nil)
	})
}
