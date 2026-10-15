package figures

import (
	"github.com/NaejEL/opnview/internal/store/reads"
	"github.com/NaejEL/opnview/internal/store/vocab"
	"github.com/NaejEL/opnview/internal/widget/envelope"
	"github.com/NaejEL/opnview/internal/widget/request"
)

// The states of a rate.
const (
	rateDefined             = "defined"
	RateUndefinedNoResolver = "undefined_no_resolver_evidence"
	rateUndefinedNoFlow     = "undefined_no_eligible_flow"
)

// attributionRow is one client's attribution.
type AttributionRow struct {
	Client        request.ClientInfo `json:"client"`
	EligibleFlows envelope.Count     `json:"eligible_flows"`
	EligibleBytes *envelope.Count    `json:"eligible_bytes,omitempty"`
	NamedFlows    envelope.Count     `json:"named_flows"`
	Methods       MethodCounts       `json:"methods"`
	// Rate is NamedFlows / EligibleFlows, null when RateState says it is undefined.
	Rate             *float64 `json:"rate"`
	RateState        string   `json:"rate_state"`
	MeanDelaySeconds *float64 `json:"mean_delay_seconds"`
	MaxDelaySeconds  *int64   `json:"max_delay_seconds"`
}

// clientAttribution is one client's counts from widget_attribution_rate_by_client.
type ClientAttribution struct {
	Eligible, Named, Cache, Timing, Bytes int64
	Mean                                  *float64
	Maximum                               *int64
	// Measured holds what a row read under netflow carries beyond its bytes
	// (internal/widget/request/measure.go).
	Measured reads.VolumeFigures
}

// AttributionByClient reads the per-client counts of the window.
func AttributionByClient(r *request.Request) (map[int64]ClientAttribution, reads.AttributionRate, error) {
	whole, err := r.Db.ReadAttributionRate(r.Ctx, r.Window.From, r.Window.To, nil)
	if err != nil {
		return nil, whole, err
	}
	result := map[int64]ClientAttribution{}
	err = r.Db.WidgetRows(r.Ctx, r.Measured("widget_attribution_rate_by_client"), vocab.Period{}, r.WindowParameters(),
		func(row reads.RowScanner) error {
			var (
				id      int64
				counts  ClientAttribution
				mean    reads.NullFloat64
				maximum reads.NullInt64
			)
			targets := []any{&id, &counts.Eligible, &counts.Named, &mean, &maximum, &counts.Cache,
				&counts.Timing, &counts.Bytes}
			if r.Netflow() {
				targets = append(targets, counts.Measured.NetflowTargets()...)
			}
			if err := row.Scan(targets...); err != nil {
				return err
			}
			counts.Mean, counts.Maximum = request.NullableFloat(mean), request.NullableNumber(maximum)
			result[id] = counts
			return nil
		})
	return result, whole, err
}

// RateOf is a client's rate and its state.
func RateOf(counts ClientAttribution, resolverCovered bool) (*float64, string) {
	switch {
	case !resolverCovered:
		return nil, RateUndefinedNoResolver
	case counts.Eligible == 0:
		return nil, rateUndefinedNoFlow
	}
	rate := float64(counts.Named) / float64(counts.Eligible)
	return &rate, rateDefined
}
