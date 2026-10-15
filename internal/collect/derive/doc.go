// Package derive is the derivation every pass ends with: classification, pairing and the
// aggregate refresh, serialised and ordered against the purge (derive.go), and the NetFlow join
// and the measured volumes it computes (flowrecord.go, measuredvolume.go, pushindexes.go).
// internal/collect composes it into the Collector.
package derive
