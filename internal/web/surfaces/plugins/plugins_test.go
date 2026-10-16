package plugins_test

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NaejEL/opnview/internal/plugin"
	"github.com/NaejEL/opnview/internal/plugin/contract"
	"github.com/NaejEL/opnview/internal/plugin/listing"
	"github.com/NaejEL/opnview/internal/web"
	"github.com/NaejEL/opnview/internal/web/surfaces/plugins"
	"github.com/NaejEL/opnview/internal/web/webtest"
)

// listing reads the plugin listing as the signed-in reader.
func readListing(t *testing.T, harness *webtest.Harness) (plugins.Listing, string) {
	t.Helper()
	answer := harness.Get(plugins.PathPlugins)
	body := webtest.ReadBody(t, answer)
	if answer.StatusCode != http.StatusOK {
		t.Fatalf("the listing answered %d: %.300s", answer.StatusCode, body)
	}
	var listed plugins.Listing
	if err := json.Unmarshal([]byte(body), &listed); err != nil {
		t.Fatalf("the listing is not JSON: %v", err)
	}
	return listed, body
}

func find(listed plugins.Listing, name string) (listing.ListedPlugin, bool) {
	for _, entry := range listed.Plugins {
		if entry.Name == name {
			return entry, true
		}
	}
	return listing.ListedPlugin{}, false
}

// condition reads a JSON answer's condition.
func condition(t *testing.T, answer *http.Response) string {
	t.Helper()
	body := webtest.ReadBody(t, answer)
	var decoded struct {
		Condition string `json:"condition"`
	}
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		t.Fatalf("the answer is not JSON: %.300s", body)
	}
	return decoded.Condition
}

// TestThePluginListingListsEveryBuiltinAndCarriesNoSecret is A11 and the source half of B2: every
// built-in plugin is listed as loaded with origin builtin, with its providers, endpoints and
// destinations; without a session the listing answers 401 with a JSON condition; and no answer
// carries the session token or the form token.
func TestThePluginListingListsEveryBuiltinAndCarriesNoSecret(t *testing.T) {
	t.Parallel()
	harness := webtest.New(t)
	harness.CompleteSetup()
	listed, body := readListing(t, harness)
	for _, name := range []string{"pf", "suricata", "kea", "isc", "dnsmasq", "unbound", "insight", "maxmind",
		"publicsuffix"} {
		entry, found := find(listed, name)
		if !found {
			t.Errorf("the built-in plugin %s is not listed", name)
			continue
		}
		if entry.State != listing.StateLoaded || entry.Origin != plugin.OriginBuiltin || entry.Version == "" ||
			entry.APIVersion != 1 || len(entry.Providers) == 0 {
			t.Errorf("%s is listed as %+v", name, entry)
		}
	}
	maxmind, _ := find(listed, "maxmind")
	if len(maxmind.Destinations) == 0 || !strings.HasPrefix(maxmind.Destinations[0], "https://download.maxmind.com") {
		t.Errorf("maxmind declares the destinations %v", maxmind.Destinations)
	}
	pf, _ := find(listed, "pf")
	if len(pf.Providers[0].Endpoints) == 0 || len(pf.Destinations) != 0 {
		t.Errorf("pf lists the endpoints %v and the destinations %v", pf.Providers[0].Endpoints, pf.Destinations)
	}
	for _, carried := range []string{harness.SessionCookie(), harness.SessionFormToken()} {
		if carried != "" && strings.Contains(body, carried) {
			t.Error("the listing carries the session token or the form token")
		}
	}

	anonymous := webtest.FreshClient(t, harness)
	answer := anonymous.Get(plugins.PathPlugins)
	if answer.StatusCode != http.StatusUnauthorized || condition(t, answer) != "no_session" {
		t.Errorf("the listing without a session answered %d", answer.StatusCode)
	}
}

// TestAPluginSettingIsWrittenWithinItsBoundsAndItsSecretIsNeverServed is A8 over the endpoint: a
// declared setting is written within its bounds, refused outside them and refused when undeclared,
// each with a closed condition naming the key and with nothing written; a secret is stored sealed,
// listed only as set, and neither its plaintext nor its ciphertext is in any answer. A save of the
// ip_geolocation plugin's settings wakes its refresh.
func TestAPluginSettingIsWrittenWithinItsBoundsAndItsSecretIsNeverServed(t *testing.T) {
	t.Parallel()
	harness := webtest.New(t)
	harness.CompleteSetup()
	token := harness.SessionFormToken()
	path := "/api/plugins/maxmind/settings"

	for _, refused := range []struct {
		body, condition, key string
	}{
		{`{"account_id": 0}`, plugin.SettingRefusalOutOfRange, "account_id"},
		{`{"account_id": "not a number"}`, plugin.SettingRefusalInvalidValue, "account_id"},
		{`{"no_such_setting": 1}`, plugin.SettingRefusalUnknownKey, "no_such_setting"},
		{`{"account_id": 100001, "no_such_setting": 1}`, plugin.SettingRefusalUnknownKey, "no_such_setting"},
	} {
		answer := harness.PostJSON(path, refused.body, token, "")
		body := webtest.ReadBody(t, answer)
		var decoded plugins.SettingsRefusal
		_ = json.Unmarshal([]byte(body), &decoded)
		if answer.StatusCode != http.StatusBadRequest || decoded.Condition != refused.condition ||
			decoded.Key != refused.key {
			t.Errorf("%s answered %d %s", refused.body, answer.StatusCode, body)
		}
		if value := storedAccountID(t, harness); value != "" {
			t.Errorf("%s was refused and wrote %q", refused.body, value)
		}
	}
	for _, malformed := range []string{"", "[1]", "{} {}", "not json"} {
		answer := harness.PostJSON(path, malformed, token, "")
		if answer.StatusCode != http.StatusBadRequest || condition(t, answer) != plugins.ConditionMalformedBody {
			t.Errorf("the body %q answered %d", malformed, answer.StatusCode)
		}
	}
	if answer := harness.PostJSON("/api/plugins/no_such_plugin/settings", `{}`, token, ""); answer.StatusCode !=
		http.StatusNotFound || condition(t, answer) != plugins.ConditionUnknownPlugin {
		t.Errorf("an unknown plugin answered %d", answer.StatusCode)
	}

	wakesBefore := harness.GeoIP.Woken()
	licence := "example-licence-" + webtest.RandomHex(t, 6)
	answer := harness.PostJSON(path, `{"account_id": 100001, "licence_key": "`+licence+`"}`, token, "")
	written := webtest.ReadBody(t, answer)
	if answer.StatusCode != http.StatusOK {
		t.Fatalf("a valid write answered %d: %s", answer.StatusCode, written)
	}
	if value := storedAccountID(t, harness); value != "100001" {
		t.Errorf("the account ID is stored as %q", value)
	}
	if harness.GeoIP.Woken() != wakesBefore+1 {
		t.Error("a save of the ip_geolocation plugin's settings did not wake its refresh")
	}
	sealed, present, err := harness.Store.Credential(context.Background(), plugin.StorageKey("maxmind", "licence_key"))
	if err != nil || !present {
		t.Fatalf("the licence key was not stored as a credential: %v", err)
	}
	if strings.Contains(string(sealed.Ciphertext), licence) {
		t.Error("the licence key is stored in the clear")
	}
	_, listed := readListing(t, harness)
	for name, served := range map[string]string{"the write's answer": written, "the listing": listed} {
		for _, forbidden := range []string{licence, base64.StdEncoding.EncodeToString(sealed.Ciphertext),
			hex.EncodeToString(sealed.Ciphertext), base64.StdEncoding.EncodeToString(sealed.Nonce)} {
			if strings.Contains(served, forbidden) {
				t.Errorf("%s carries the secret or its ciphertext", name)
			}
		}
	}
	decoded, _ := readListing(t, harness)
	maxmind, _ := find(decoded, "maxmind")
	for _, setting := range maxmind.Settings {
		if setting.Key == "licence_key" && (!setting.Set || setting.Value != nil) {
			t.Errorf("the secret is listed as %+v", setting)
		}
	}
}

// TestAPluginSettingsWriteRefusesAMissingOrForeignProof is C-AC4 for the plugin settings endpoint:
// no session is a 401, a missing, wrong or foreign proof a 403, each in JSON, and no row changes.
func TestAPluginSettingsWriteRefusesAMissingOrForeignProof(t *testing.T) {
	t.Parallel()
	harness := webtest.New(t)
	harness.CompleteSetup()
	path := "/api/plugins/maxmind/settings"
	body := `{"account_id": 100001}`
	token := harness.SessionFormToken()

	anonymous := webtest.FreshClient(t, harness)
	if answer := anonymous.PostJSON(path, body, token, ""); answer.StatusCode != http.StatusUnauthorized ||
		condition(t, answer) != "no_session" {
		t.Errorf("a write without a session answered %d", answer.StatusCode)
	}
	for name, attempt := range map[string]func() *http.Response{
		"no proof":    func() *http.Response { return harness.PostJSON(path, body, "", "") },
		"a wrong one": func() *http.Response { return harness.PostJSON(path, body, token+"0", "") },
		"another origin": func() *http.Response {
			return harness.PostJSON(path, body, token, "https://elsewhere.example")
		},
		"a form body": func() *http.Response {
			request, _ := http.NewRequest(http.MethodPost, harness.HTTP.URL+path, strings.NewReader("account_id=1"))
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			request.Header.Set(web.HeaderCSRFToken, token)
			answer, err := harness.Client.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			return answer
		},
	} {
		answer := attempt()
		if answer.StatusCode != http.StatusForbidden || condition(t, answer) != web.ConditionCSRFRefused {
			t.Errorf("%s answered %d", name, answer.StatusCode)
		}
	}
	if value := storedAccountID(t, harness); value != "" {
		t.Errorf("a refused write stored %q", value)
	}
	if answer := harness.PostJSON(path, body, token, harness.HTTP.URL); answer.StatusCode != http.StatusOK {
		t.Errorf("a same-origin write with its proof answered %d", answer.StatusCode)
	}
}

// TestAThirdPartyPluginIsListedAndARefusedOneSaysWhy is A5's listing half and A11 over a data
// directory: a plugin dropped as files is listed loaded with origin data_directory, and one with a
// broken manifest is listed refused with its closed code -- the service started either way.
func TestAThirdPartyPluginIsListedAndARefusedOneSaysWhy(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	write := func(name, content string) {
		path := filepath.Join(dataDir, plugin.PluginsDirectory, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	write("example_reader/plugin.json", `{"api_version": 1, "name": "example_reader", "version": "0.1.0",
		"display_name": "Example reader", "description": "A lease source probed for its presence.",
		"script": "example_reader.js",
		"endpoints": [{"name": "status", "path": "/api/examplereader/service/status", "method": "GET"}],
		"providers": [{"kind": "dhcp_lease", "display_name": "Example reader", "read_strategy": "full_reread"}]}`)
	write("example_reader/example_reader.js", `exports.dhcp_lease = {
		probe: function (host) { return { result: { probe: "status", state: "unavailable",
			outcome: "plugin_not_installed" } }; },
		leases: function () { return { result: { probe: "status", state: "unavailable",
			outcome: "read_not_supported" }, unsupported: "an example reads nothing" }; } };`)
	write("broken_example/plugin.json", `{"api_version": 1, "name": `)

	harness := webtest.NewAdjusted(t, dataDir, true, false, func(options *web.Options) {
		loaded, err := plugin.Load(context.Background(), plugin.Options{DataDir: dataDir, Store: options.Store,
			Sealer: options.Box})
		if err != nil {
			t.Fatalf("loading the plugins: %v", err)
		}
		options.Plugins = loaded
	})
	harness.CompleteSetup()
	listed, _ := readListing(t, harness)
	reader, found := find(listed, "example_reader")
	if !found || reader.State != listing.StateLoaded || reader.Origin != plugin.OriginDataDirectory ||
		len(reader.Providers) != 1 || reader.Providers[0].Kind != "dhcp_lease" {
		t.Errorf("the third-party plugin is listed as %+v (found %v)", reader, found)
	}
	broken, found := find(listed, "broken_example")
	if !found || broken.State != listing.StateRefused || broken.Reason == nil ||
		broken.Reason.Code != contract.RefusalManifestInvalid || broken.Origin != plugin.OriginDataDirectory {
		t.Errorf("the broken plugin is listed as %+v (found %v)", broken, found)
	}
}

// storedAccountID is the stored MaxMind account ID, empty when none is stored.
func storedAccountID(t *testing.T, harness *webtest.Harness) string {
	t.Helper()
	value, _, err := harness.Store.Setting(context.Background(), plugin.StorageKey("maxmind", "account_id"))
	if err != nil {
		t.Fatal(err)
	}
	return value
}
