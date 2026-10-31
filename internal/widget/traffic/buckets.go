package traffic

import (
	"github.com/NaejEL/opnview/internal/widget/envelope"
	"github.com/NaejEL/opnview/internal/widget/request"
)

// Bucketed series (decision 8). Buckets are UTC-aligned multiples of their width from the
// epoch, sub-hour for the short periods, read from the records of the window. A bucket
// outside the covered interval, or overlapping a gap, is marked as a gap and never as a
// zero; a gap bucket that still holds rows keeps them, so the buckets always sum to the
// window's total. No bucket is smoothed or interpolated.

// bucketSeconds is the bucket width of each period: 1 m, 15 m, 1 h and 24 h, so a series
// has between 30 and 168 buckets and every boundary is a UTC minute, quarter-hour, hour or
// day.
var bucketSeconds = map[string]int64{"1h": 60, "24h": 900, "7d": 3600, "30d": 86400}

// Bucket is one bucket of a series.
type Bucket struct {
	From int64 `json:"from"`
	To   int64 `json:"to"`
	// Gap says the bucket is not fully covered: outside the covered interval, or
	// overlapping a gap. A gap bucket with no row has no value.
	Gap bool `json:"gap"`
	// Value is the bucket's figure for the series' measure, null in a gap with no row.
	Value *float64 `json:"value"`
	// Volume is the bucket's logged figures, for a traffic series.
	Volume *envelope.Volume `json:"volume,omitempty"`
}

// BucketWidth is the width of the request's period.
func BucketWidth(r *request.Request) int64 {
	width := bucketSeconds[r.Params.Text("period")]
	if width == 0 {
		width = bucketSeconds["24h"]
	}
	return width
}

// BucketIntervals are the buckets of the window, each clipped to it.
func BucketIntervals(window envelope.Interval, width int64) []envelope.Interval {
	var result []envelope.Interval
	for start := window.From - FloorMod(window.From, width); start < window.To; start += width {
		result = append(result, envelope.Interval{From: max(window.From, start), To: min(window.To, start+width)})
	}
	return result
}

// FloorMod is the non-negative remainder.
func FloorMod(value, width int64) int64 {
	remainder := value % width
	if remainder < 0 {
		remainder += width
	}
	return remainder
}

// IsGapBucket reports whether a bucket lies outside the covered interval or meets a gap.
func IsGapBucket(bucket envelope.Interval, covered *envelope.Interval, gaps []envelope.Gap) bool {
	if covered == nil || covered.From > bucket.From || covered.To < bucket.To {
		return true
	}
	for _, gap := range gaps {
		if gap.From < bucket.To && gap.To >= bucket.From {
			return true
		}
	}
	return false
}
