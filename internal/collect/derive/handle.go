package derive

import "github.com/NaejEL/opnview/internal/collect/core"

// Deriver is the part of the collector this package holds: the derivation every pass ends with -- classification, pairing, the aggregate refresh -- and the NetFlow join and measured volumes it computes.
// internal/collect composes it into its Collector, so its methods are the Collector's.
type Deriver struct{ *core.Collector }

// On is this part of the collector on the shared state, for another part that calls it.
func On(state *core.Collector) *Deriver { return &Deriver{Collector: state} }
