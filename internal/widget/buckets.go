package widget

import (
	"github.com/NaejEL/opnview/internal/store"
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
	Volume *Volume `json:"volume,omitempty"`
}

// BucketWidth is the width of the request's period.
func (r *Request) BucketWidth() int64 {
	width := bucketSeconds[r.Params.Text("period")]
	if width == 0 {
		width = bucketSeconds["24h"]
	}
	return width
}

// BucketIntervals are the buckets of the window, each clipped to it.
func BucketIntervals(window Interval, width int64) []Interval {
	var result []Interval
	for start := window.From - FloorMod(window.From, width); start < window.To; start += width {
		result = append(result, Interval{From: max(window.From, start), To: min(window.To, start+width)})
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
func IsGapBucket(bucket Interval, covered *Interval, gaps []Gap) bool {
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

// TrafficFilter scopes a traffic series.
type TrafficFilter struct {
	Interfaces []int64
	ClientID   *int64
	RuleID     *int64
	Country    *string
	Asn        *int64
	Site       *string
}

// unscoped reports whether only interfaces restrict the series, which the interface
// family's slots can answer beyond the flow horizon.
func (f TrafficFilter) unscoped() bool {
	return f.ClientID == nil && f.RuleID == nil && f.Country == nil && f.Asn == nil && f.Site == nil
}

// TrafficBuckets reads a traffic series' figures per bucket start and traffic direction,
// from the flows, and from the interface family's 1 h slots for the whole hours before the
// earliest flow kept when the bucket width is whole hours and the filter allows it. It
// returns the covered interval of what it read.
func (r *Request) TrafficBuckets(filter TrafficFilter, width int64) (map[int64]map[string]store.VolumeFigures,
	*Interval, error) {
	result := map[int64]map[string]store.VolumeFigures{}
	add := func(start int64, direction string, figures store.VolumeFigures) {
		if result[start] == nil {
			result[start] = map[string]store.VolumeFigures{}
		}
		sum := result[start][direction]
		AddFigures(&sum, figures)
		result[start][direction] = sum
	}
	parameters := r.WindowParameters()
	parameters["width"] = width
	parameters["interface_ids"] = JsonIDs(filter.Interfaces)
	parameters["client_id"] = OptionalID(filter.ClientID)
	parameters["rule_id"] = OptionalID(filter.RuleID)
	parameters["asn"] = OptionalID(filter.Asn)
	parameters["country"] = optionalText(filter.Country)
	parameters["site"] = optionalText(filter.Site)
	err := r.Db.WidgetRows(r.Ctx, "widget_traffic_buckets", store.Period{}, parameters,
		func(row store.RowScanner) error {
			var (
				start     int64
				direction string
				figures   store.VolumeFigures
			)
			if err := row.Scan(&start, &direction, &figures.Bytes, &figures.AllowedBytes,
				&figures.BlockedBytes, &figures.UnknownBytes, &figures.AllowedConnections,
				&figures.BlockedConnections, &figures.UnknownConnections); err != nil {
				return err
			}
			add(start, direction, figures)
			return nil
		})
	if err != nil {
		return nil, nil, err
	}
	earliest, has, err := r.Earliest("widget_earliest_flow")
	if err != nil {
		return nil, nil, err
	}
	coveredFrom := r.Window.To
	if has {
		coveredFrom = max(r.Window.From, earliest)
	}
	// Beyond the flow horizon, whole-hour buckets of a series the interface family can
	// answer are read from its slots.
	if width%3600 == 0 && filter.unscoped() && (!has || earliest > r.Window.From) {
		boundary := r.Window.To
		if has {
			boundary = store.PeriodHour.SlotStart(earliest)
		}
		firstHour := store.PeriodHour.SlotStart(r.Window.From)
		if firstHour < r.Window.From {
			firstHour = store.PeriodHour.SlotEnd(firstHour)
		}
		if firstHour < boundary {
			selected := map[int64]bool{}
			for _, id := range filter.Interfaces {
				selected[id] = true
			}
			slotsFrom := int64(-1)
			err := r.Db.WidgetRows(r.Ctx, "widget_traffic_hour_slots", store.Period{},
				map[string]any{"from": firstHour, "to": boundary}, func(row store.RowScanner) error {
					var (
						hour      int64
						src, dst  store.NullInt64
						direction string
						figures   store.VolumeFigures
					)
					if err := row.Scan(&hour, &src, &dst, &direction, &figures.Bytes, &figures.AllowedBytes,
						&figures.BlockedBytes, &figures.UnknownBytes, &figures.AllowedConnections,
						&figures.BlockedConnections, &figures.UnknownConnections); err != nil {
						return err
					}
					local := LocalInterface(store.VolumeKey{SrcInterfaceID: NullableNumber(src),
						DstInterfaceID: NullableNumber(dst), TrafficDirection: direction})
					if len(selected) > 0 && (local == nil || !selected[*local]) {
						return nil
					}
					if slotsFrom < 0 || hour < slotsFrom {
						slotsFrom = hour
					}
					add(hour-FloorMod(hour, width), direction, figures)
					return nil
				})
			if err != nil {
				return nil, nil, err
			}
			if slotsFrom >= 0 {
				coveredFrom = min(coveredFrom, slotsFrom)
				r.State("series_read_from_hour_slots_beyond_flow_horizon")
			}
		}
	}
	if coveredFrom >= min(r.Window.To, r.Now) {
		return result, nil, nil
	}
	return result, &Interval{From: coveredFrom, To: min(r.Window.To, r.Now)}, nil
}

// OptionalID renders a nullable id as a bindable value.
func OptionalID(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}

// optionalText renders a nullable text as a bindable value.
func optionalText(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}
