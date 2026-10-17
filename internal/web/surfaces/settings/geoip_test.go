package settings_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/collect/ipgeolocation"
	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/store/vocab"
	"github.com/NaejEL/opnview/internal/web"
	"github.com/NaejEL/opnview/internal/web/surfaces/settings"
	"github.com/NaejEL/opnview/internal/web/webtest"
	"github.com/NaejEL/opnview/internal/web/webtest/settingstest"
)

// TestTheMaxMindAccountIDIsASettingAndASaveWakesTheRefresh is AC10 and AC11: the account ID is a
// setting row, answered back, validated as a positive whole number, and a save of the MaxMind
// settings wakes the refresh.
func TestTheMaxMindAccountIDIsASettingAndASaveWakesTheRefresh(t *testing.T) {
	t.Parallel()
	h := webtest.New(t)
	h.CompleteSetup()
	path := "/api/plugins/" + settings.GeolocationPlugin + "/settings"

	if status, answer := settingstest.SendJSON(h, path, map[string]any{"account_id": 123456}); status != http.StatusOK {
		t.Fatalf("saving an account ID answered %d %v", status, answer)
	}
	if got, present, err := h.Store.Setting(context.Background(), config.KeyMaxMindAccountID); err != nil ||
		!present || got != "123456" {
		t.Errorf("the account ID row holds %q (present %v)", got, present)
	}
	if got := settingstest.Path(settingstest.GetJSON(h, settings.PathAPISettings), "geolocation", "account_id"); got != "123456" {
		t.Errorf("the settings answer reports the account ID as %v", got)
	}
	if h.GeoIP.Woken() != 1 {
		t.Errorf("saving a new account ID woke the refresh %d times, want once", h.GeoIP.Woken())
	}
	if status, answer := settingstest.SendJSON(h, path, map[string]any{"account_id": 123456,
		"licence_key": webtest.RandomHex(t, 16)}); status != http.StatusOK {
		t.Fatalf("saving a licence key answered %d %v", status, answer)
	}
	if h.GeoIP.Woken() != 2 {
		t.Error("saving a licence key did not wake the refresh")
	}

	for _, refused := range []any{"abc", -4, 0, 12.5} {
		status, answer := settingstest.SendJSON(h, path, map[string]any{"account_id": refused})
		if status != http.StatusBadRequest || answer["condition"] == nil || answer["key"] != "account_id" {
			t.Errorf("the account ID %v answered %d %v without its refusal", refused, status, answer)
		}
	}
	if got, _, _ := h.Store.Setting(context.Background(), config.KeyMaxMindAccountID); got != "123456" {
		t.Errorf("a refused account ID changed the stored one to %q", got)
	}
}

// TestTheDatasetStateIsTheOneTheRefreshRecorded: each probe the refresh records is its own code,
// and the build of the databases in use is answered as an instant.
func TestTheDatasetStateIsTheOneTheRefreshRecorded(t *testing.T) {
	t.Parallel()
	h := webtest.New(t)
	h.CompleteSetup()
	state := func() any {
		return settingstest.Path(settingstest.GetJSON(h, settings.PathAPISettings), "geolocation", "download_state")
	}
	if got := state(); got != web.ConditionOf(msgGeoIPNotDownloaded) {
		t.Errorf("a fresh installation answers %v, not that the databases are not downloaded yet", got)
	}
	for probe, key := range map[string]messageKey{
		ipgeolocation.ProbeNoLicenceKey:            msgGeoIPNoLicenceKey,
		ipgeolocation.ProbeLicenceKeyUndecryptable: msgGeoIPKeyUnreadable,
		ipgeolocation.ProbeNoAccountID:             msgGeoIPNoAccountID,
		ipgeolocation.ProbeRefused:                 msgGeoIPRefused,
		ipgeolocation.ProbeLimited:                 msgGeoIPLimited,
		ipgeolocation.ProbeFailed:                  msgGeoIPFailed,
	} {
		settingstest.SetGeoIPProbe(h, vocab.StateUnavailable, probe)
		if got := state(); got != web.ConditionOf(key) {
			t.Errorf("the probe %s answers %v, not %s", probe, got, web.ConditionOf(key))
		}
	}

	build := time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)
	h.GeoIP.SetBuild(build.Unix())
	settingstest.SetGeoIPProbe(h, vocab.StateReachable, ipgeolocation.ProbeCurrent)
	answer := settingstest.GetJSON(h, settings.PathAPISettings)
	if settingstest.Path(answer, "geolocation", "download_state") != web.ConditionOf(msgGeoIPCurrent) ||
		settingstest.Path(answer, "geolocation", "build_at") != float64(build.Unix()) {
		t.Errorf("current databases are not answered with their build: %v", answer["geolocation"])
	}
}
