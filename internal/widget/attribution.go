package widget

import (
	"github.com/NaejEL/opnview/internal/store"
)

// The states of a rate.
const (
	rateDefined             = "defined"
	RateUndefinedNoResolver = "undefined_no_resolver_evidence"
	rateUndefinedNoFlow     = "undefined_no_eligible_flow"
)

// attributionRow is one client's attribution.
type attributionRow struct {
	Client        ClientInfo   `json:"client"`
	EligibleFlows Count        `json:"eligible_flows"`
	EligibleBytes *Count       `json:"eligible_bytes,omitempty"`
	NamedFlows    Count        `json:"named_flows"`
	Methods       MethodCounts `json:"methods"`
	// Rate is NamedFlows / EligibleFlows, null when RateState says it is undefined.
	Rate             *float64 `json:"rate"`
	RateState        string   `json:"rate_state"`
	MeanDelaySeconds *float64 `json:"mean_delay_seconds"`
	MaxDelaySeconds  *int64   `json:"max_delay_seconds"`
}

// clientAttribution is one client's counts from widget_attribution_rate_by_client.
type clientAttribution struct {
	Eligible, Named, Cache, Timing, Bytes int64
	Mean                                  *float64
	Maximum                               *int64
}

// AttributionByClient reads the per-client counts of the window.
func (r *Request) AttributionByClient() (map[int64]clientAttribution, store.AttributionRate, error) {
	whole, err := r.Db.ReadAttributionRate(r.Ctx, r.Window.From, r.Window.To, nil)
	if err != nil {
		return nil, whole, err
	}
	result := map[int64]clientAttribution{}
	err = r.Db.WidgetRows(r.Ctx, "widget_attribution_rate_by_client", store.Period{}, r.WindowParameters(),
		func(row store.RowScanner) error {
			var (
				id      int64
				counts  clientAttribution
				mean    store.NullFloat64
				maximum store.NullInt64
			)
			if err := row.Scan(&id, &counts.Eligible, &counts.Named, &mean, &maximum, &counts.Cache,
				&counts.Timing, &counts.Bytes); err != nil {
				return err
			}
			counts.Mean, counts.Maximum = nullableFloat(mean), NullableNumber(maximum)
			result[id] = counts
			return nil
		})
	return result, whole, err
}

// RateOf is a client's rate and its state.
func RateOf(counts clientAttribution, resolverCovered bool) (*float64, string) {
	switch {
	case !resolverCovered:
		return nil, RateUndefinedNoResolver
	case counts.Eligible == 0:
		return nil, rateUndefinedNoFlow
	}
	rate := float64(counts.Named) / float64(counts.Eligible)
	return &rate, rateDefined
}
