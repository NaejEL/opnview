package derive

import "github.com/NaejEL/opnview/internal/collect/core"

// Deriver is the part of the collector this package holds: the ingest bookkeeping of the passes
// storing flows.
// internal/collect composes it into its Collector, so its methods are the Collector's.
type Deriver struct{ *core.Collector }

// On is this part of the collector on the shared state, for another part that calls it.
func On(state *core.Collector) *Deriver { return &Deriver{Collector: state} }
