package settings_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/store/accounts"
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

// TestTheSettingsJSONSavesStoreAndRefuseTheirInputs is C-AC9 for the firewall card.
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
	assertSaves(t, []save{
		firewall("the firewall's URL, key and secret", fake, apiKey, apiSecret, "", "",
			config.KeyOPNsenseBaseURL+"=<firewall>", config.KeyOPNsenseAPIKey+"="+apiKey,
			"credential "+accounts.CredentialOPNsenseAPISecret),
		firewall("the firewall's URL alone", fake, "", "", "", "", config.KeyOPNsenseBaseURL+"=<firewall>"),
		firewall("a URL that is no firewall API's", invalid, apiKey, apiSecret, "", "firewall_url_invalid"),
		firewall("a malformed fingerprint", fake, apiKey, apiSecret, "not a fingerprint", "fingerprint_invalid"),
		firewall("a secret with no key", fake, "", apiSecret, "", "api_key_required"),
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
			{"firewall", "certificate_fingerprint"}} {
			expect(t, answer, absent, nil)
		}
		expect(t, answer, []any{"firewall", "credential_state"}, "absent")
		expect(t, answer, []any{"firewall", "verification"}, "not_attempted")
		// A plugin's settings are the plugin's own, and the appearance is the front's.
		for _, gone := range []string{"theme", "geolocation", "public_suffix"} {
			if _, present := answer[gone]; present {
				t.Errorf("the settings answer still carries %s", gone)
			}
		}
	})

	t.Run("an installation with everything configured", func(t *testing.T) {
		t.Parallel()
		h := collectiontest.SignedInHarness(t)
		apiKey := webtest.RandomHex(t, 8)
		webtest.Discard(t, settingstest.StoreCredentials(h, apiKey, webtest.RandomHex(t, 16)))
		answer := settingstest.GetJSON(h, settings.PathAPISettings)
		expect(t, answer, []any{"firewall", "url"}, h.Fake.BaseURL())
		expect(t, answer, []any{"firewall", "api_key"}, apiKey)
		expect(t, answer, []any{"firewall", "credential_state"}, "ready")
	})
}
