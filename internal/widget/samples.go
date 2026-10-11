package widget

import (
	"sort"

	"github.com/NaejEL/opnview/internal/store"
)

// Sampled readings: the firewall's own gauges and its interface counters
// (measurement_sample). A sample is a value at an instant, so a longer period wants an
// aggregation -- mean, minimum, maximum, sum or last -- never a count; and a bucket with no
// sample is a gap, because a missing sample means opnview was not collecting, never a 0.

// metricMeasures maps a health metric of the catalogue to the subject kind and the
// measures the sampler stores it under (internal/store, Measure).
var metricMeasures = map[string]struct {
	kind     store.SubjectKind
	measures []store.Measure
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
	kind, key, measure, unit string
}

// measuredSubjects returns the subjects that reported a metric's measures in the window.
func (r *Request) measuredSubjects(metric string) ([]subject, error) {
	mapping := metricMeasures[metric]
	var result []subject
	err := r.db.WidgetRows(r.ctx, "widget_measurement_subjects", store.Period{}, map[string]any{
		"from": r.window.From, "to": r.window.To, "subject_kinds": jsonTexts([]string{string(mapping.kind)}),
	}, func(row store.RowScanner) error {
		var item subject
		if err := row.Scan(&item.kind, &item.key, &item.measure, &item.unit); err != nil {
			return err
		}
		for _, measure := range mapping.measures {
			if string(measure) == item.measure {
				result = append(result, item)
			}
		}
		return nil
	})
	sort.Slice(result, func(i, j int) bool {
		if result[i].measure != result[j].measure {
			return result[i].measure < result[j].measure
		}
		return result[i].key < result[j].key
	})
	return result, err
}

// Point is one sampled value.
type Point struct {
	At    int64   `json:"at"`
	Value float64 `json:"value"`
}

// samplesOf reads one subject's samples over the window.
func (r *Request) samplesOf(item subject) ([]Point, error) {
	var points []Point
	err := r.db.WidgetRows(r.ctx, "widget_measurement_series", store.Period{}, map[string]any{
		"subject_kind": item.kind, "subject_key": item.key, "measure": item.measure,
		"from": r.window.From, "to": r.window.To,
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

// aggregateBuckets aggregates points into the window's buckets. A bucket no point falls
// in is a gap with no value.
func aggregateBuckets(window Interval, width int64, points []Point, aggregation string) []Bucket {
	grouped := map[int64][]Point{}
	for _, point := range points {
		start := point.At - floorMod(point.At, width)
		grouped[start] = append(grouped[start], point)
	}
	var buckets []Bucket
	for _, interval := range bucketIntervals(window, width) {
		start := interval.From - floorMod(interval.From, width)
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

// throughputMeasures maps a throughput measure to the counters it reads, inbound then
// outbound, and the factor applied to their per-second increase.
var throughputMeasures = map[string]struct {
	in, out   store.Measure
	perSecond bool
	factor    float64
}{
	"bits_per_second": {store.MeasureBytesIn, store.MeasureBytesOut, true, 8},
	"bytes":           {store.MeasureBytesIn, store.MeasureBytesOut, false, 1},
	"packets":         {store.MeasurePacketsIn, store.MeasurePacketsOut, false, 1},
	"errors":          {store.MeasureErrorsIn, store.MeasureErrorsOut, false, 1},
}

// throughputPoints reads one device's counter as points: the per-second rate times the
// factor, or the increase between two samples, stamped at the later sample. A counter
// that went down is a reset and gives no point -- never a negative rate, never a 0.
func (r *Request) throughputPoints(device string, counter store.Measure, perSecond bool,
	factor float64) ([]Point, int, error) {
	readings, err := r.db.ReadInterfaceThroughput(r.ctx, device, counter, r.window.From, r.window.To)
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

// coverSamples records the coverage of a sampled reading.
func (r *Request) coverSamples() error {
	earliest, has, err := r.earliest("widget_earliest_sample")
	if err != nil {
		return err
	}
	r.coverFrom(earliest, has)
	return nil
}
