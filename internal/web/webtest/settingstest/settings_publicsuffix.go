package settingstest

import (
	"github.com/NaejEL/opnview/internal/collect/registrabledomain"
	"github.com/NaejEL/opnview/internal/store/vocab"
	"github.com/NaejEL/opnview/internal/web/webtest"
	"github.com/NaejEL/opnview/internal/web/webtest/sourcestest"
)

// SetPublicSuffixProbe records a refresh outcome, with a detail.
func SetPublicSuffixProbe(h *webtest.Harness, state vocab.AvailabilityState, probe string, detail *string) {
	h.T.Helper()
	sourcestest.SetAvailabilityDetail(h, registrabledomain.Kind, registrabledomain.BuiltinKey, state, probe, detail)
}
