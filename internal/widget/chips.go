package widget

// ChipRate is a current rate, of the sampled measure.
//
// Every live figure carries what it is and how old it may be (C3 of the backend-completion cycle,
// docs/widget-api.md "Live figures"): its measure; the instant of its newest reading; the span one
// reading covers; the cadence the readings were taken at -- a live pass's while somebody looks, the
// measurement interval otherwise -- and that it is a lower bound, the firewall seeing nothing of the
// traffic that stays behind one interface. A rate older than twice its cadence is the named state
// stale_sample, its figures beside it; a subject with no reading within the bound is
// no_current_sample, with no figure. Neither is ever a rate of 0.
type ChipRate struct {
	State            string   `json:"state"`
	Measure          string   `json:"measure"`
	InBitsPerSecond  *float64 `json:"in_bits_per_second"`
	OutBitsPerSecond *float64 `json:"out_bits_per_second"`
	OutMeasured      bool     `json:"out_measured"`
	SampledAt        *int64   `json:"sampled_at"`
	// SpanSeconds is how long one reading covers, CadenceSeconds how often readings are taken;
	// both null with no reading.
	SpanSeconds    *int64 `json:"span_seconds"`
	CadenceSeconds *int64 `json:"cadence_seconds"`
	// LowerBound is always true: a sampled rate sees only what crosses the firewall.
	LowerBound bool `json:"lower_bound"`
}
