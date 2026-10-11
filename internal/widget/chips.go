package widget

// ChipRate is a current rate, of the sampled measure.
type ChipRate struct {
	State            string   `json:"state"`
	Measure          string   `json:"measure"`
	InBitsPerSecond  *float64 `json:"in_bits_per_second"`
	OutBitsPerSecond *float64 `json:"out_bits_per_second"`
	OutMeasured      bool     `json:"out_measured"`
	SampledAt        *int64   `json:"sampled_at"`
}
