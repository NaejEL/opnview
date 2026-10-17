package settingstest

import (
	"context"

	"github.com/NaejEL/opnview/internal/collect/ipgeolocation"
	"github.com/NaejEL/opnview/internal/store/vocab"
	"github.com/NaejEL/opnview/internal/web/webtest"
)

// SetGeoIPProbe records an availability row for the MaxMind provider, as a refresh
// would.
func SetGeoIPProbe(h *webtest.Harness, state vocab.AvailabilityState, probe string) {
	h.T.Helper()
	ctx := context.Background()
	id, err := h.Store.ProviderID(ctx, ipgeolocation.Kind, ipgeolocation.BuiltinKey)
	if err != nil {
		h.T.Fatalf("looking up the MaxMind provider: %v", err)
	}
	if err := h.Store.SetAvailability(ctx, id, state, vocab.OutcomeHealthy, probe, nil, h.Clock.Now().Unix()); err != nil {
		h.T.Fatalf("recording the MaxMind availability: %v", err)
	}
}
