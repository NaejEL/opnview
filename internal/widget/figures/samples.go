package figures

import (
	"sort"

	"github.com/NaejEL/opnview/internal/store/reads"
	"github.com/NaejEL/opnview/internal/store/vocab"
	"github.com/NaejEL/opnview/internal/widget/envelope"
	"github.com/NaejEL/opnview/internal/widget/request"
	"github.com/NaejEL/opnview/internal/widget/traffic"
)

// Sampled readings: the firewall's own gauges and its interface counters
// (measurement_sample). A sample is a value at an instant, so a longer period wants an
// aggregation -- mean, minimum, maximum, sum or last -- never a count; and a bucket with no
// sample is a gap, because a missing sample means opnview was not collecting, never a 0.

// Which subject kind and which measures serve a health metric is the provider's fact, declared by its
// plugin under `sample_metrics` and read here from the declarations (vocab.SampleMetrics): no Go file
// maps a metric to a measure.

// Subject is one measured subject.
type Subject struct {
	Kind, Key, Measure, Unit string
}

// MeasuredSubjects returns the subjects that reported a metric's measures in the window.
func MeasuredSubjects(r *request.Request, metric string) ([]Subject, error) {
	mapping, _ := vocab.SampleMetricNamed(metric)
	var result []Subject
	err := r.Db.WidgetRows(r.Ctx, "widget_measurement_subjects", vocab.Period{}, map[string]any{
		"from": r.Window.From, "to": r.Window.To, "subject_kinds": request.JsonTexts([]string{mapping.SubjectKind}),
	}, func(row reads.RowScanner) error {
		var item Subject
		if err := row.Scan(&item.Kind, &item.Key, &item.Measure, &item.Unit); err != nil {
			return err
		}
		for _, measure := range mapping.Measures {
			if measure == item.Measure {
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
func SamplesOf(r *request.Request, item Subject) ([]Point, error) {
	var points []Point
	err := r.Db.WidgetRows(r.Ctx, "widget_measurement_series", vocab.Period{}, map[string]any{
		"subject_kind": item.Kind, "subject_key": item.Key, "measure": item.Measure,
		"from": r.Window.From, "to": r.Window.To,
	}, func(row reads.RowScanner) error {
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
func AggregateBuckets(window envelope.Interval, width int64, points []Point, aggregation string) []traffic.Bucket {
	grouped := map[int64][]Point{}
	for _, point := range points {
		start := point.At - traffic.FloorMod(point.At, width)
		grouped[start] = append(grouped[start], point)
	}
	var buckets []traffic.Bucket
	for _, interval := range traffic.BucketIntervals(window, width) {
		start := interval.From - traffic.FloorMod(interval.From, width)
		bucket := traffic.Bucket{From: interval.From, To: interval.To, Gap: true}
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
	In, Out   vocab.Measure
	PerSecond bool
	Factor    float64
}{
	"bits_per_second": {vocab.MeasureBytesIn, vocab.MeasureBytesOut, true, 8},
	"bytes":           {vocab.MeasureBytesIn, vocab.MeasureBytesOut, false, 1},
	"packets":         {vocab.MeasurePacketsIn, vocab.MeasurePacketsOut, false, 1},
	"errors":          {vocab.MeasureErrorsIn, vocab.MeasureErrorsOut, false, 1},
}

// ThroughputPoints reads one device's counter as points: the per-second rate times the
// factor, or the increase between two samples, stamped at the later sample. A counter
// that went down is a reset and gives no point -- never a negative rate, never a 0.
func ThroughputPoints(r *request.Request, device string, counter vocab.Measure, perSecond bool,
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
