package plugin

import (
	"context"
	"sync"
)

// The parameters a SQL-only derivation is handed beside :since and :now (ADR 0018, ADR 0020): values
// the core holds and a plugin's SQL may not read for itself -- the discovery interval a held address
// is read with, the leg-pairing window -- supplied by the service once the collector exists, and by
// the test harness with its defaults. Each is bound as :<name>; a plugin's own settings are bound as
// :setting_<key>.

// DerivationParameters supplies the core's values to a run.
type DerivationParameters func(ctx context.Context) (map[string]any, error)

// parameterHolder is what a load's derivations read the supplier from: set after the load, read at
// every run.
type parameterHolder struct {
	mutex  sync.RWMutex
	supply DerivationParameters
}

func (h *parameterHolder) set(supply DerivationParameters) {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	h.supply = supply
}

func (h *parameterHolder) values(ctx context.Context) (map[string]any, error) {
	h.mutex.RLock()
	supply := h.supply
	h.mutex.RUnlock()
	if supply == nil {
		return map[string]any{}, nil
	}
	return supply(ctx)
}

// SetDerivationParameters names what supplies the core's values to the load's SQL-only derivations.
func (l *Loaded) SetDerivationParameters(supply DerivationParameters) {
	if l.parameters != nil {
		l.parameters.set(supply)
	}
}
