package widget

import (
	"sort"

	"github.com/NaejEL/opnview/internal/store"
)

// Sampled readings: the firewall's own gauges and its interface counters
// (measurement_sample). A sample is a value at an instant, so a longer period wants an
// aggregation -- mean, minimum, maximum, sum or last -- never a count; and a bucket with no
// sample is a gap, because a missing sample means opnview was not collecting, never a 0.

// MetricMeasures maps a health metric of the catalogue to the subject kind and the
// measures the sampler stores it under (internal/store, Measure).
var MetricMeasures = map[string]struct {
	Kind     store.SubjectKind
	Measures []store.Measure
}{
	"uptime":          {store.SubjectFirewall, []store.Measure{store.MeasureUptimeSeconds}},
	"cpu":             {store.SubjectFirewall, []store.Measure{store.MeasureCPUUseRatio}},
	"memory":          {store.SubjectFirewall, []store.Measure{store.MeasureMemoryUseRatio}},
	"swap":            {store.SubjectFirewall, []store.Measure{store.MeasureSwapUsedBytes, store.MeasureSwapTotalBytes}},
	"disk":            {store.SubjectFirewall, []store.Measure{store.MeasureDiskUseRatio}},
	"temperature":     {store.SubjectFirewall, []store.Measure{store.MeasureTemperatureCelsius}},
	"gateway_latency": {store.SubjectGateway, []store.Measure{store.MeasureDelayMilliseconds}},
	"gateway_loss":    {store.SubjectGateway, []store.Measure{store.MeasureLossRatio}},
}

// subject is one measured subject.
type subject struct {
	Kind, Key, Measure, Unit string
}

// MeasuredSubjects returns the subjects that reported a metric's measures in the window.
func (r *Request) MeasuredSubjects(metric string) ([]subject, error) {
	mapping := MetricMeasures[metric]
	var result []subject
	err := r.Db.WidgetRows(r.Ctx, "widget_measurement_subjects", store.Period{}, map[string]any{
		"from": r.Window.From, "to": r.Window.To, "subject_kinds": JsonTexts([]string{string(mapping.Kind)}),
	}, func(row store.RowScanner) error {
		var item subject
		if err := row.Scan(&item.Kind, &item.Key, &item.Measure, &item.Unit); err != nil {
			return err
		}
		for _, measure := range mapping.Measures {
			if string(measure) == item.Measure {
				result = append(result, item)
			}
		}
		return nil
	})
	sort.Slice(result, func(i, j int) bool {
		if result[i].Measure != result[j].Measure {
			return result[i].Measure < result[j].Measure
		}
		return result[i].Key < result[j].Key
	})
	return result, err
}

// Point is one sampled value.
type Point struct {
	At    int64   `json:"at"`
	Value float64 `json:"value"`
}

// SamplesOf reads one subject's samples over the window.
func (r *Request) SamplesOf(item subject) ([]Point, error) {
	var points []Point
	err := r.Db.WidgetRows(r.Ctx, "widget_measurement_series", store.Period{}, map[string]any{
		"subject_kind": item.Kind, "subject_key": item.Key, "measure": item.Measure,
		"from": r.Window.From, "to": r.Window.To,
	}, func(row store.RowScanner) error {
		var point Point
		if err := row.Scan(&point.At, &point.Value); err != nil {
			return err
		}
		points = append(points, point)
		return nil
	})
	return points, err
}

// AggregateBuckets aggregates points into the window's buckets. A bucket no point falls
// in is a gap with no value.
func AggregateBuckets(window Interval, width int64, points []Point, aggregation string) []Bucket {
	grouped := map[int64][]Point{}
	for _, point := range points {
		start := point.At - FloorMod(point.At, width)
		grouped[start] = append(grouped[start], point)
	}
	var buckets []Bucket
	for _, interval := range BucketIntervals(window, width) {
		start := interval.From - FloorMod(interval.From, width)
		bucket := Bucket{From: interval.From, To: interval.To, Gap: true}
		if values := grouped[start]; len(values) > 0 {
			value := aggregate(values, aggregation)
			bucket.Gap, bucket.Value = false, &value
		}
		buckets = append(buckets, bucket)
	}
	return buckets
}

// aggregate combines one bucket's points.
func aggregate(points []Point, aggregation string) float64 {
	sort.Slice(points, func(i, j int) bool { return points[i].At < points[j].At })
	result := points[0].Value
	sum := 0.0
	for _, point := range points {
		sum += point.Value
		switch aggregation {
		case "min":
			result = min(result, point.Value)
		case "max":
			result = max(result, point.Value)
		}
	}
	switch aggregation {
	case "sum":
		return sum
	case "last":
		return points[len(points)-1].Value
	case "mean":
		return sum / float64(len(points))
	}
	return result
}

// ThroughputMeasures maps a throughput measure to the counters it reads, inbound then
// outbound, and the factor applied to their per-second increase.
var ThroughputMeasures = map[string]struct {
	In, Out   store.Measure
	PerSecond bool
	Factor    float64
}{
	"bits_per_second": {store.MeasureBytesIn, store.MeasureBytesOut, true, 8},
	"bytes":           {store.MeasureBytesIn, store.MeasureBytesOut, false, 1},
	"packets":         {store.MeasurePacketsIn, store.MeasurePacketsOut, false, 1},
	"errors":          {store.MeasureErrorsIn, store.MeasureErrorsOut, false, 1},
}

// ThroughputPoints reads one device's counter as points: the per-second rate times the
// factor, or the increase between two samples, stamped at the later sample. A counter
// that went down is a reset and gives no point -- never a negative rate, never a 0.
func (r *Request) ThroughputPoints(device string, counter store.Measure, perSecond bool,
	factor float64) ([]Point, int, error) {
	readings, err := r.Db.ReadInterfaceThroughput(r.Ctx, device, counter, r.Window.From, r.Window.To)
	if err != nil {
		return nil, 0, err
	}
	var (
		points []Point
		resets int
	)
	for _, reading := range readings {
		if reading.Reset || reading.PerSecond == nil {
			resets++
			continue
		}
		value := *reading.PerSecond * factor
		if !perSecond {
			value = *reading.PerSecond * float64(reading.To-reading.From) * factor
		}
		points = append(points, Point{At: reading.To, Value: value})
	}
	return points, resets, nil
}
