package webtest

import (
	"context"
	"github.com/NaejEL/opnview/internal/collect/ipgeolocation"
	"github.com/NaejEL/opnview/internal/store"
	"github.com/NaejEL/opnview/internal/web"
	"net/url"
	"testing"
	"time"
)

// setGeoIPProbe records an availability row for the MaxMind provider, as a refresh
// would.
func (h *Harness) SetGeoIPProbe(state store.AvailabilityState, probe string) {
	h.T.Helper()
	ctx := context.Background()
	id, err := h.Store.ProviderID(ctx, ipgeolocation.Kind, ipgeolocation.BuiltinKey)
	if err != nil {
		h.T.Fatalf("looking up the MaxMind provider: %v", err)
	}
	if err := h.Store.SetAvailability(ctx, id, state, store.OutcomeHealthy, probe, nil, h.Clock.Now().Unix()); err != nil {
		h.T.Fatalf("recording the MaxMind availability: %v", err)
	}
}

// geoIPPages renders the settings surface in every MaxMind state, for the catalogue's
// assertions.
func GeoIPPages(t *testing.T) []RenderedPage {
	t.Helper()
	h := New(t)
	h.CompleteSetup()
	var pages []RenderedPage
	for _, probe := range []string{
		ipgeolocation.ProbeNoLicenceKey, ipgeolocation.ProbeLicenceKeyUndecryptable, ipgeolocation.ProbeNoAccountID,
		ipgeolocation.ProbeRefused, ipgeolocation.ProbeLimited, ipgeolocation.ProbeFailed,
	} {
		h.SetGeoIPProbe(store.StateUnavailable, probe)
		pages = append(pages, RenderedPage{"the settings surface with the MaxMind state " + probe,
			PageSource(t, h, web.PathSettings)})
	}
	h.GeoIP.SetBuild(time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC).Unix())
	h.SetGeoIPProbe(store.StateReachable, ipgeolocation.ProbeCurrent)
	pages = append(pages, RenderedPage{"the settings surface with current MaxMind databases",
		PageSource(t, h, web.PathSettings)})
	_, refused := StatusAndBody(t, h.Post(web.PathSettings, url.Values{
		FieldSettingsCard: {SettingsCardGeolocation}, "maxmind_account_id": {"not-a-number"}, "theme": {string(web.ThemeSystem)},
	}))
	pages = append(pages, RenderedPage{"a refused MaxMind account ID", refused})
	return pages
}
