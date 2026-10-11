package webtest

import (
	"github.com/NaejEL/opnview/internal/collect/registrabledomain"
	"github.com/NaejEL/opnview/internal/store"
)

// setPublicSuffixProbe records a refresh outcome, with a detail.
func (h *Harness) SetPublicSuffixProbe(state store.AvailabilityState, probe string, detail *string) {
	h.T.Helper()
	h.SetAvailabilityDetail(registrabledomain.Kind, registrabledomain.BuiltinKey, state, probe, detail)
}
