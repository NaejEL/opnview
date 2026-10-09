package web

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/maxmind"
	"github.com/NaejEL/opnview/internal/store"
)

// The MaxMind part of the settings surface. The surface downloads nothing: it stores
// the account ID beside the licence key, wakes the refresh, and reports the state the
// refresh recorded.

// setGeoIPProbe records an availability row for the MaxMind provider, as a refresh
// would.
func (h *harness) setGeoIPProbe(state store.AvailabilityState, probe string) {
	h.t.Helper()
	ctx := context.Background()
	id, err := h.store.ProviderID(ctx, maxmind.Kind, maxmind.ProviderKey)
	if err != nil {
		h.t.Fatalf("looking up the MaxMind provider: %v", err)
	}
	if err := h.store.SetAvailability(ctx, id, state, probe, nil, h.clock.Now().Unix()); err != nil {
		h.t.Fatalf("recording the MaxMind availability: %v", err)
	}
}

// TestTheMaxMindAccountIDIsASettingAndASaveWakesTheRefresh is AC10 and AC11: the
// account ID is a setting row, pre-filled, validated as a positive whole number, and
// a save of it or of a licence key wakes the refresh, while a save that changes
// neither does not.
func TestTheMaxMindAccountIDIsASettingAndASaveWakesTheRefresh(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.completeSetup()

	status, page := statusAndBody(t, h.post(PathSettings, url.Values{
		"maxmind_account_id": {"123456"}, "theme": {string(ThemeSystem)},
	}))
	if status != http.StatusOK {
		t.Fatalf("saving an account ID answered %d", status)
	}
	if got, present, err := h.store.Setting(context.Background(), config.KeyMaxMindAccountID); err != nil ||
		!present || got != "123456" {
		t.Errorf("the account ID row holds %q (present %v)", got, present)
	}
	if !strings.Contains(page, `name="maxmind_account_id" type="text" inputmode="numeric" autocomplete="off" spellcheck="false" value="123456"`) {
		t.Error("the account ID is not pre-filled")
	}
	if h.geoip.woken() != 1 {
		t.Errorf("saving a new account ID woke the refresh %d times, want once", h.geoip.woken())
	}

	discard(t, h.post(PathSettings, url.Values{
		"maxmind_account_id": {"123456"}, "theme": {string(ThemeSystem)},
	}))
	if h.geoip.woken() != 1 {
		t.Error("a save that changed no MaxMind credential woke the refresh")
	}
	discard(t, h.post(PathSettings, url.Values{
		"maxmind_account_id": {"123456"}, "maxmind_licence_key": {randomHex(t, 16)},
		"theme": {string(ThemeSystem)},
	}))
	if h.geoip.woken() != 2 {
		t.Error("saving a licence key did not wake the refresh")
	}

	for _, refused := range []string{"abc", "-4", "0", "12.5"} {
		status, page := statusAndBody(t, h.post(PathSettings, url.Values{
			"maxmind_account_id": {refused}, "theme": {string(ThemeSystem)},
		}))
		if status != http.StatusBadRequest || !strings.Contains(page,
			catalogueText(t, msgMaxMindAccountIDBad)) {
			t.Errorf("the account ID %q answered %d without its refusal", refused, status)
		}
	}
	if got, _, _ := h.store.Setting(context.Background(), config.KeyMaxMindAccountID); got != "123456" {
		t.Errorf("a refused account ID changed the stored one to %q", got)
	}
}

// TestTheDatasetStateIsTheOneTheRefreshRecorded: each probe the refresh records is
// its own sentence, and the build of the databases in use is shown as an instant.
func TestTheDatasetStateIsTheOneTheRefreshRecorded(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.completeSetup()
	text := pageText(t, h, PathSettings)
	if !strings.Contains(text, catalogueText(t, msgGeoIPNotDownloaded)) {
		t.Error("a fresh installation does not say the databases are not downloaded yet")
	}
	for probe, key := range map[string]messageKey{
		maxmind.ProbeNoLicenceKey:            msgGeoIPNoLicenceKey,
		maxmind.ProbeLicenceKeyUndecryptable: msgGeoIPKeyUnreadable,
		maxmind.ProbeNoAccountID:             msgGeoIPNoAccountID,
		maxmind.ProbeRefused:                 msgGeoIPRefused,
		maxmind.ProbeLimited:                 msgGeoIPLimited,
		maxmind.ProbeFailed:                  msgGeoIPFailed,
	} {
		h.setGeoIPProbe(store.StateUnavailable, probe)
		if !strings.Contains(pageText(t, h, PathSettings), catalogueText(t, key)) {
			t.Errorf("the probe %s does not read as %s", probe, key)
		}
	}

	build := time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)
	h.geoip.setBuild(build.Unix())
	h.setGeoIPProbe(store.StateReachable, maxmind.ProbeCurrent)
	page := pageSource(t, h, PathSettings)
	if !stateShown(t, page, msgLabelGeoIPState, msgGeoIPCurrent) ||
		!figureShown(t, page, msgLabelGeoIPBuild, build.Format(time.RFC3339)) {
		t.Error("current databases are not reported with their build")
	}
}

// geoIPPages renders the settings surface in every MaxMind state, for the catalogue's
// assertions.
func geoIPPages(t *testing.T) []renderedPage {
	t.Helper()
	h := newHarness(t)
	h.completeSetup()
	var pages []renderedPage
	for _, probe := range []string{
		maxmind.ProbeNoLicenceKey, maxmind.ProbeLicenceKeyUndecryptable, maxmind.ProbeNoAccountID,
		maxmind.ProbeRefused, maxmind.ProbeLimited, maxmind.ProbeFailed,
	} {
		h.setGeoIPProbe(store.StateUnavailable, probe)
		pages = append(pages, renderedPage{"the settings surface with the MaxMind state " + probe,
			pageSource(t, h, PathSettings)})
	}
	h.geoip.setBuild(time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC).Unix())
	h.setGeoIPProbe(store.StateReachable, maxmind.ProbeCurrent)
	pages = append(pages, renderedPage{"the settings surface with current MaxMind databases",
		pageSource(t, h, PathSettings)})
	_, refused := statusAndBody(t, h.post(PathSettings, url.Values{
		"maxmind_account_id": {"not-a-number"}, "theme": {string(ThemeSystem)},
	}))
	pages = append(pages, renderedPage{"a refused MaxMind account ID", refused})
	return pages
}
