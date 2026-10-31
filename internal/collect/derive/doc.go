// Package derive is the ingest bookkeeping every pass storing flows goes through: the instant a
// pass stamps its rows with stays in flight until the rows are written, so the rollups plugin's
// refresh -- the derivation that reads them by that stamp (plugins/rollups, ADR 0021) -- never
// moves its watermark past it (:ingest_watermark, internal/collect/derivations.go). The derivations
// a pass once ended with here -- classification, pairing, attribution, the refresh -- are plugins'
// derivations now. internal/collect composes it into the Collector.
package derive
