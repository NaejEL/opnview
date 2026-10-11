package widget

import (
	"github.com/NaejEL/opnview/internal/store"
)

// traffic_over_time_by_scope: one series per selected scope -- outbound, inbound and
// inter-interface, from the traffic direction every flow carries -- in buckets, with the
// totals brought forward (docs/widget-catalogue.md, "Traffic over time by scope"). It is a
// multi-series response, so each series carries its own availability, coverage and gaps.

var trafficOverTimeByScope = Preset{
	Type: "traffic_over_time_by_scope", Shape: "time_series", Windowed: true,
	Providers: []string{"firewall_log"}, Primary: "firewall_log",
	Params: []Param{
		periodParam(),
		{Key: "scopes", Kind: KindEnumSet, Values: []string{"outbound", "inbound", "inter_interface"},
			Default: []any{"outbound", "inbound", "inter_interface"}, Data: true},
		{Key: "measure", Kind: KindEnum, Values: []string{"bytes", "connections"}, Default: "bytes", Data: true},
		interfacesParam(),
		{Key: "stacked", Kind: KindBoolean, Default: false},
	},
	Serve: serveTrafficOverTimeByScope,
}

// Series is one series of a time-series response, carrying its own availability, coverage
// and gaps so one missing source degrades one line rather than the chart.
type Series struct {
	Key   string `json:"key"`
	Label string `json:"label,omitempty"`
	// Condition is the series' own condition code.
	Condition    string                 `json:"condition"`
	Availability []ProviderAvailability `json:"availability"`
	Window       *WindowReport          `json:"window"`
	Gaps         []Gap                  `json:"gaps"`
	// Unresolved lists the series' own unresolved references.
	Unresolved []UnresolvedReference `json:"unresolved_references"`
	// Aggregation is how a bucket's samples were combined, for a sampled series.
	Aggregation string `json:"aggregation,omitempty"`
	// Measure names what the values are.
	Measure     string   `json:"measure"`
	LowerBound  bool     `json:"lower_bound"`
	Unit        string   `json:"unit,omitempty"`
	Total       *float64 `json:"total"`
	TotalVolume *Volume  `json:"total_volume,omitempty"`
	Buckets     []Bucket `json:"buckets"`
	// Echo is the series entry as the request gave it, after defaults.
	Echo map[string]any `json:"parameters,omitempty"`
}

// TimeSeriesData is a bucketed time series.
type TimeSeriesData struct {
	BucketSeconds int64    `json:"bucket_seconds"`
	Series        []Series `json:"series"`
}

func serveTrafficOverTimeByScope(r *Request) (any, bool, error) {
	width := r.bucketWidth()
	figures, covered, err := r.trafficBuckets(trafficFilter{interfaces: r.ids("interfaces")}, width)
	if err != nil {
		return nil, false, err
	}
	r.cover(covered)
	if err := r.hourFreshness(r.window.From, r.window.To); err != nil {
		return nil, false, err
	}
	measure := r.params.text("measure")
	data := TimeSeriesData{BucketSeconds: width, Series: []Series{}}
	hasRows := false
	for _, scope := range r.params.set("scopes") {
		series := Series{Key: scope, Measure: MeasureLogged, Unit: measure, LowerBound: true,
			Condition:    kindCondition(r.response.Availability, "firewall_log"),
			Availability: r.response.Availability, Window: r.response.Window, Gaps: r.response.Gaps,
			Unresolved: []UnresolvedReference{}}
		var total store.VolumeFigures
		for _, bucket := range bucketIntervals(r.window, width) {
			start := bucket.From - floorMod(bucket.From, width)
			sums, present := figures[start][scope]
			gap := isGapBucket(bucket, covered, r.response.Gaps)
			entry := Bucket{From: bucket.From, To: bucket.To, Gap: gap}
			if present || !gap {
				value := figuresValue(measure, sums)
				volume := logged(sums)
				entry.Value, entry.Volume = &value, &volume
				addFigures(&total, sums)
				if present {
					hasRows = true
				}
			}
			series.Buckets = append(series.Buckets, entry)
		}
		totalValue := figuresValue(measure, total)
		totalVolume := logged(total)
		series.Total, series.TotalVolume = &totalValue, &totalVolume
		if series.Condition == ConditionReachable && total.Bytes == 0 && total.Connections() == 0 {
			series.Condition = ConditionReachableNoRows
		}
		data.Series = append(data.Series, series)
	}
	return data, hasRows, nil
}
