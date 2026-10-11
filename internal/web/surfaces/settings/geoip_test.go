package settings_test

import (
	"context"
	"github.com/NaejEL/opnview/internal/collect/ipgeolocation"
	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/store"
	"github.com/NaejEL/opnview/internal/web"
	"github.com/NaejEL/opnview/internal/web/webtest"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// TestTheMaxMindAccountIDIsASettingAndASaveWakesTheRefresh is AC10 and AC11: the
// account ID is a setting row, pre-filled, validated as a positive whole number, and
// a save of it or of a licence key wakes the refresh, while a save that changes
// neither does not.
func TestTheMaxMindAccountIDIsASettingAndASaveWakesTheRefresh(t *testing.T) {
	t.Parallel()
	h := webtest.New(t)
	h.CompleteSetup()

	status, page := webtest.Followed(t, h, h.Post(web.PathSettings, url.Values{
		webtest.FieldSettingsCard: {webtest.SettingsCardGeolocation}, "maxmind_account_id": {"123456"}, "theme": {string(web.ThemeSystem)},
	}))
	if status != http.StatusSeeOther {
		t.Fatalf("saving an account ID answered %d", status)
	}
	if got, present, err := h.Store.Setting(context.Background(), config.KeyMaxMindAccountID); err != nil ||
		!present || got != "123456" {
		t.Errorf("the account ID row holds %q (present %v)", got, present)
	}
	if !strings.Contains(page, `name="maxmind_account_id" type="text" inputmode="numeric" autocomplete="off" spellcheck="false" value="123456"`) {
		t.Error("the account ID is not pre-filled")
	}
	if h.GeoIP.Woken() != 1 {
		t.Errorf("saving a new account ID woke the refresh %d times, want once", h.GeoIP.Woken())
	}

	webtest.Discard(t, h.Post(web.PathSettings, url.Values{
		webtest.FieldSettingsCard: {webtest.SettingsCardGeolocation}, "maxmind_account_id": {"123456"}, "theme": {string(web.ThemeSystem)},
	}))
	if h.GeoIP.Woken() != 1 {
		t.Error("a save that changed no MaxMind credential woke the refresh")
	}
	webtest.Discard(t, h.Post(web.PathSettings, url.Values{
		webtest.FieldSettingsCard: {webtest.SettingsCardGeolocation}, "maxmind_account_id": {"123456"}, "maxmind_licence_key": {webtest.RandomHex(t, 16)},
		"theme": {string(web.ThemeSystem)},
	}))
	if h.GeoIP.Woken() != 2 {
		t.Error("saving a licence key did not wake the refresh")
	}

	for _, refused := range []string{"abc", "-4", "0", "12.5"} {
		status, page := webtest.StatusAndBody(t, h.Post(web.PathSettings, url.Values{
			webtest.FieldSettingsCard: {webtest.SettingsCardGeolocation}, "maxmind_account_id": {refused}, "theme": {string(web.ThemeSystem)},
		}))
		if status != http.StatusBadRequest || !strings.Contains(page,
			webtest.CatalogueText(t, msgMaxMindAccountIDBad)) {
			t.Errorf("the account ID %q answered %d without its refusal", refused, status)
		}
	}
	if got, _, _ := h.Store.Setting(context.Background(), config.KeyMaxMindAccountID); got != "123456" {
		t.Errorf("a refused account ID changed the stored one to %q", got)
	}
}

// TestTheDatasetStateIsTheOneTheRefreshRecorded: each probe the refresh records is
// its own sentence, and the build of the databases in use is shown as an instant.
func TestTheDatasetStateIsTheOneTheRefreshRecorded(t *testing.T) {
	t.Parallel()
	h := webtest.New(t)
	h.CompleteSetup()
	text := pageText(t, h, web.PathSettings)
	if !strings.Contains(text, webtest.CatalogueText(t, msgGeoIPNotDownloaded)) {
		t.Error("a fresh installation does not say the databases are not downloaded yet")
	}
	for probe, key := range map[string]messageKey{
		ipgeolocation.ProbeNoLicenceKey:            msgGeoIPNoLicenceKey,
		ipgeolocation.ProbeLicenceKeyUndecryptable: msgGeoIPKeyUnreadable,
		ipgeolocation.ProbeNoAccountID:             msgGeoIPNoAccountID,
		ipgeolocation.ProbeRefused:                 msgGeoIPRefused,
		ipgeolocation.ProbeLimited:                 msgGeoIPLimited,
		ipgeolocation.ProbeFailed:                  msgGeoIPFailed,
	} {
		h.SetGeoIPProbe(store.StateUnavailable, probe)
		if !strings.Contains(pageText(t, h, web.PathSettings), webtest.CatalogueText(t, key)) {
			t.Errorf("the probe %s does not read as %s", probe, key)
		}
	}

	build := time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)
	h.GeoIP.SetBuild(build.Unix())
	h.SetGeoIPProbe(store.StateReachable, ipgeolocation.ProbeCurrent)
	page := webtest.PageSource(t, h, web.PathSettings)
	if !webtest.StateShown(t, page, msgLabelGeoIPState, msgGeoIPCurrent) ||
		!webtest.FigureShown(t, page, msgLabelGeoIPBuild, build.Format(time.RFC3339)) {
		t.Error("current databases are not reported with their build")
	}
}
