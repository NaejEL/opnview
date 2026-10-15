package traffic

import (
	"github.com/NaejEL/opnview/internal/widget/envelope"
	"github.com/NaejEL/opnview/internal/widget/params"
)

// Series is one series of a time-series response, carrying its own availability, coverage
// and gaps so one missing source degrades one line rather than the chart.
type Series struct {
	Key   string `json:"key"`
	Label string `json:"label,omitempty"`
	// Condition is the series' own condition code.
	Condition    string                          `json:"condition"`
	Availability []envelope.ProviderAvailability `json:"availability"`
	Window       *envelope.WindowReport          `json:"window"`
	Gaps         []envelope.Gap                  `json:"gaps"`
	// Unresolved lists the series' own unresolved references.
	Unresolved []params.UnresolvedReference `json:"unresolved_references"`
	// Aggregation is how a bucket's samples were combined, for a sampled series.
	Aggregation string `json:"aggregation,omitempty"`
	// Measure names what the values are.
	Measure     string           `json:"measure"`
	LowerBound  bool             `json:"lower_bound"`
	Unit        string           `json:"unit,omitempty"`
	Total       *float64         `json:"total"`
	TotalVolume *envelope.Volume `json:"total_volume,omitempty"`
	Buckets     []Bucket         `json:"buckets"`
	// Echo is the series entry as the request gave it, after defaults.
	Echo map[string]any `json:"parameters,omitempty"`
}
