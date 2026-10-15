package strip

import (
	"context"
	"embed"
	"sort"
	"strings"
	"time"

	"github.com/NaejEL/opnview/internal/live"
	"github.com/NaejEL/opnview/internal/store"
	"github.com/NaejEL/opnview/internal/store/reads"
	"github.com/NaejEL/opnview/internal/store/samples"
	"github.com/NaejEL/opnview/internal/store/sqldb"
	"github.com/NaejEL/opnview/internal/store/vocab"
	"github.com/NaejEL/opnview/internal/widget/envelope"
	"github.com/NaejEL/opnview/internal/widget/request"
	"github.com/NaejEL/opnview/internal/widget/traffic"
)

//go:embed *.statements.sql
var statements embed.FS

func init() { _ = sqldb.RegisterStatements("live_strip", statements) }

// The window a strip covers, in seconds: the mockup's two minutes by default, an hour at most --
// fine samples are kept an hour by default -- and never more than MaxBuckets buckets.
const (
	DefaultWindowSeconds int64 = 120
	MaxWindowSeconds     int64 = 3600
	MaxBuckets           int64 = 1000
)

// The throughput series, by traffic direction (store.CurrentRates.ByDirection), and the local
// addresses whose sending no sample splits between outbound and inter_interface.
const (
	SeriesOutbound        = "outbound"
	SeriesInbound         = "inbound"
	SeriesInterInterface  = "inter_interface"
	SeriesOutboundUnsplit = "outbound_unsplit"
)

// The decision series: the filter log's allowed connections, its refusals by engine kind
// (docs/data-model.md, the blocked_decision entity), and the resolvers' refusals of every list.
const (
	SeriesAllowed                    = "allowed"
	SeriesFirewallRule               = "firewall_rule"
	SeriesFirewallNoRule             = "firewall_no_rule"
	SeriesFirewallReasonNotRecorded  = "firewall_reason_not_recorded"
	SeriesDNSRefusal                 = "dns_refusal"
	kindFirewallLog                  = "firewall_log"
	kindDNSLookup                    = "dns_lookup"
	measureLogged                    = "logged"
	measureDNSLookups                = "dns_lookups"
	measureSampled                   = "sampled"
	unitBitsPerSecond                = "bit_per_second"
	gapKindOpnview, gapNotRunning    = "opnview", "opnview_not_running"
	outcomeHealthy, outcomeHealthyNo = "healthy", "healthy_empty"
)

// Strip is the rolling strip: a trailing series at the live cadence (coverage row 8 of
// docs/widget-api.md, "Live figures").
type Strip struct {
	// CadenceSeconds is the width of a bucket: the live cadence in force.
	CadenceSeconds int64 `json:"cadence_seconds"`
	// Window is [from, to) of the buckets, aligned on UTC multiples of the cadence from the epoch.
	Window envelope.Interval `json:"window"`
	// LiveInterestUntil is when the live-interest lease this request renewed lapses.
	LiveInterestUntil *int64 `json:"live_interest_until"`
	// Throughput is sampled, never summed with Decisions, which are counted.
	Throughput []ThroughputSeries `json:"throughput"`
	Decisions  []DecisionSeries   `json:"decisions"`
	// Gaps are the intervals a bound provider did not cover, and those while opnview was not
	// running.
	Gaps []envelope.Gap `json:"gaps"`
}

// ThroughputSeries is the sampled rate of one direction, per bucket.
type ThroughputSeries struct {
	Key            string             `json:"key"`
	Measure        string             `json:"measure"`
	Unit           string             `json:"unit"`
	SpanSeconds    int64              `json:"span_seconds"`
	CadenceSeconds int64              `json:"cadence_seconds"`
	LowerBound     bool               `json:"lower_bound"`
	Buckets        []ThroughputBucket `json:"buckets"`
}

// ThroughputBucket is one bucket of a throughput series: the mean of the samples taken in it, or
// a gap -- no sample, never a rate of 0.
type ThroughputBucket struct {
	From      int64    `json:"from"`
	To        int64    `json:"to"`
	Gap       bool     `json:"gap"`
	Value     *float64 `json:"value"`
	Samples   int      `json:"samples"`
	SampledAt *int64   `json:"sampled_at"`
}

// DecisionSeries is one kind of decision, counted per bucket from the records' own timestamps.
type DecisionSeries struct {
	Key          string `json:"key"`
	Measure      string `json:"measure"`
	ProviderKind string `json:"provider_kind"`
	// CompleteUntil is the instant the kind's records are complete up to: the end of its active
	// provider's last healthy pass. A bucket ending after it is a gap, never a zero; null when no
	// healthy pass is recorded, and every bucket is then a gap.
	CompleteUntil *int64           `json:"complete_until"`
	LowerBound    bool             `json:"lower_bound"`
	Buckets       []DecisionBucket `json:"buckets"`
}

// DecisionBucket is one bucket of a decision series.
type DecisionBucket struct {
	From  int64  `json:"from"`
	To    int64  `json:"to"`
	Gap   bool   `json:"gap"`
	Value *int64 `json:"value"`
}

// Request is what a strip is drawn for.
type Request struct {
	// Now is the instant the strip ends at.
	Now time.Time
	// CadenceSeconds is the bucket width: the live cadence in force, or the measurement interval
	// where no provider declares a live pass.
	CadenceSeconds int64
	// WindowSeconds is how far back it reaches.
	WindowSeconds int64
	// Interest is the lease the request renews; nil renews none.
	Interest *live.Interest
}

// ErrWindowOutOfRange is a window that is not a whole number of seconds from two cadences to
// MaxWindowSeconds, or that would hold more than MaxBuckets buckets.
type ErrWindowOutOfRange struct{ Minimum, Maximum int64 }

func (e *ErrWindowOutOfRange) Error() string { return "strip: window_seconds is out of range" }

// WindowBounds are the window a request at a cadence may ask for.
func WindowBounds(cadence int64) (minimum, maximum int64) {
	minimum, maximum = 2*cadence, MaxWindowSeconds
	if limit := cadence * MaxBuckets; limit < maximum {
		maximum = limit
	}
	if maximum < minimum {
		maximum = minimum
	}
	return minimum, maximum
}

// Read draws the strip from the store and renews the live-interest lease. It reads stored samples
// and records only: it never calls the firewall.
func Read(ctx context.Context, database *store.Store, request Request) (*Strip, error) {
	width := request.CadenceSeconds
	if width <= 0 {
		width = samples.DefaultMeasurementIntervalSeconds
	}
	minimum, maximum := WindowBounds(width)
	if request.WindowSeconds < minimum || request.WindowSeconds > maximum {
		return nil, &ErrWindowOutOfRange{Minimum: minimum, Maximum: maximum}
	}
	request.Interest.Renew(request.Now)
	now := request.Now.Unix()
	to := now - traffic.FloorMod(now, width) + width
	from := now - request.WindowSeconds
	from -= traffic.FloorMod(from, width)
	result := &Strip{CadenceSeconds: width, Window: envelope.Interval{From: from, To: to},
		Throughput: []ThroughputSeries{}, Decisions: []DecisionSeries{}, Gaps: []envelope.Gap{}}
	if until, held := request.Interest.Until(); held {
		at := until.Unix()
		result.LiveInterestUntil = &at
	}
	buckets := traffic.BucketIntervals(result.Window, width)

	throughput, err := readThroughput(ctx, database, result.Window, buckets, width)
	if err != nil {
		return nil, err
	}
	result.Throughput = throughput

	gaps, err := readGaps(ctx, database, result.Window)
	if err != nil {
		return nil, err
	}
	result.Gaps = gaps
	decisions, err := readDecisions(ctx, database, result.Window, buckets, width, now, gaps)
	if err != nil {
		return nil, err
	}
	result.Decisions = decisions
	return result, nil
}

// readThroughput reads every sample of the window, fine and baseline, and averages each
// direction's rate over the samples of each bucket.
func readThroughput(ctx context.Context, database *store.Store, window envelope.Interval, buckets []envelope.Interval,
	width int64) ([]ThroughputSeries, error) {
	instants, err := database.ReadRateSeries(ctx, window.From, window.To)
	if err != nil {
		return nil, err
	}
	byBucket := map[int64][]samples.RateInstant{}
	for _, instant := range instants {
		start := instant.At - traffic.FloorMod(instant.At, width)
		byBucket[start] = append(byBucket[start], instant)
	}
	keys := []string{SeriesOutbound, SeriesInbound, SeriesInterInterface, SeriesOutboundUnsplit}
	series := make([]ThroughputSeries, 0, len(keys))
	for _, key := range keys {
		entry := ThroughputSeries{Key: key, Measure: measureSampled, Unit: unitBitsPerSecond,
			SpanSeconds: vocab.SampleSeconds, CadenceSeconds: width, LowerBound: true,
			Buckets: make([]ThroughputBucket, 0, len(buckets))}
		for _, bucket := range buckets {
			inside := byBucket[bucket.From]
			cell := ThroughputBucket{From: bucket.From, To: bucket.To, Samples: len(inside)}
			if len(inside) == 0 {
				cell.Gap = true
				entry.Buckets = append(entry.Buckets, cell)
				continue
			}
			sum, latest := 0.0, int64(0)
			for _, instant := range inside {
				rate := instant.Rates.OutboundUnsplit
				if key != SeriesOutboundUnsplit {
					rate = instant.Rates.ByDirection[key]
				}
				// A sample taken with no reading in this direction measured none: the instant
				// was sampled, so its rate there is a measured 0, not a gap.
				sum += rate.Total()
				if instant.At > latest {
					latest = instant.At
				}
			}
			mean := sum / float64(len(inside))
			cell.Value, cell.SampledAt = &mean, &latest
			entry.Buckets = append(entry.Buckets, cell)
		}
		series = append(series, entry)
	}
	return series, nil
}

// readGaps reads the collection gaps of the two kinds the decisions count, and the intervals
// opnview was not running.
func readGaps(ctx context.Context, database *store.Store, window envelope.Interval) ([]envelope.Gap, error) {
	gaps := []envelope.Gap{}
	err := database.WidgetRows(ctx, "widget_collection_gaps", vocab.Period{}, map[string]any{
		"kinds": request.JsonTexts([]string{kindFirewallLog, kindDNSLookup}), "from": window.From, "to": window.To,
	}, func(row reads.RowScanner) error {
		var gap envelope.Gap
		if err := row.Scan(&gap.ProviderKind, &gap.ProviderKey, &gap.From, &gap.To, &gap.Reason); err != nil {
			return err
		}
		gaps = append(gaps, gap)
		return nil
	})
	if err != nil {
		return nil, err
	}
	notRunning, err := database.ReadNotRunning(ctx, window.From, window.To)
	if err != nil {
		return nil, err
	}
	for _, interval := range notRunning {
		gaps = append(gaps, envelope.Gap{ProviderKind: gapKindOpnview, From: interval.From, To: interval.To,
			Reason: gapNotRunning})
	}
	return gaps, nil
}

// completeUntil is the instant a kind's records are complete up to: the oldest last healthy pass of
// its active providers, never later than now. False when an active provider has none.
func completeUntil(providers []envelope.ProviderAvailability, kind string, now int64) (int64, bool) {
	until, found := int64(0), false
	for _, provider := range providers {
		if provider.Kind != kind || !provider.Active {
			continue
		}
		if provider.Outcome == nil || provider.CheckedAt == nil ||
			(*provider.Outcome != outcomeHealthy && *provider.Outcome != outcomeHealthyNo) {
			return 0, false
		}
		if !found || *provider.CheckedAt < until {
			until, found = *provider.CheckedAt, true
		}
	}
	if found && until > now {
		until = now
	}
	return until, found
}

// readDecisions counts the decisions of each kind per bucket.
func readDecisions(ctx context.Context, database *store.Store, window envelope.Interval, buckets []envelope.Interval,
	width, now int64, gaps []envelope.Gap) ([]DecisionSeries, error) {
	counts := map[string]map[int64]int64{}
	add := func(series string, start, count int64) {
		if counts[series] == nil {
			counts[series] = map[int64]int64{}
		}
		counts[series][start] += count
	}
	parameters := map[string]any{"width": width, "from": window.From, "to": window.To}
	if err := database.WidgetRows(ctx, "widget_live_strip_allowed", vocab.Period{}, parameters,
		func(row reads.RowScanner) error {
			var start, count int64
			if err := row.Scan(&start, &count); err != nil {
				return err
			}
			add(SeriesAllowed, start, count)
			return nil
		}); err != nil {
		return nil, err
	}
	if err := database.WidgetRows(ctx, "widget_live_strip_refusals", vocab.Period{}, parameters,
		func(row reads.RowScanner) error {
			var (
				start, count int64
				engine       string
			)
			if err := row.Scan(&start, &engine, &count); err != nil {
				return err
			}
			switch {
			case strings.HasPrefix(engine, "dns_"):
				add(SeriesDNSRefusal, start, count)
			case engine == SeriesFirewallRule || engine == SeriesFirewallNoRule ||
				engine == SeriesFirewallReasonNotRecorded:
				add(engine, start, count)
			}
			return nil
		}); err != nil {
		return nil, err
	}
	var earliestFlow, earliestLookup reads.NullInt64
	if err := database.WidgetRows(ctx, "widget_live_strip_earliest", vocab.Period{}, nil,
		func(row reads.RowScanner) error { return row.Scan(&earliestFlow, &earliestLookup) }); err != nil {
		return nil, err
	}
	providers, err := request.ReadProviders(ctx, database)
	if err != nil {
		return nil, err
	}
	definitions := []struct {
		key, measure, kind string
		earliest           reads.NullInt64
	}{
		{SeriesAllowed, measureLogged, kindFirewallLog, earliestFlow},
		{SeriesFirewallRule, measureLogged, kindFirewallLog, earliestFlow},
		{SeriesFirewallNoRule, measureLogged, kindFirewallLog, earliestFlow},
		{SeriesFirewallReasonNotRecorded, measureLogged, kindFirewallLog, earliestFlow},
		{SeriesDNSRefusal, measureDNSLookups, kindDNSLookup, earliestLookup},
	}
	result := make([]DecisionSeries, 0, len(definitions))
	for _, definition := range definitions {
		series := DecisionSeries{Key: definition.key, Measure: definition.measure, ProviderKind: definition.kind,
			LowerBound: true, Buckets: make([]DecisionBucket, 0, len(buckets))}
		until, complete := completeUntil(providers, definition.kind, now)
		var covered *envelope.Interval
		if complete {
			series.CompleteUntil = &until
			if definition.earliest.Valid && definition.earliest.Int64 < until {
				covered = &envelope.Interval{From: definition.earliest.Int64, To: until}
			}
		}
		kindGaps := gapsOf(gaps, definition.kind)
		for _, bucket := range buckets {
			cell := DecisionBucket{From: bucket.From, To: bucket.To}
			count, counted := counts[definition.key][bucket.From]
			if traffic.IsGapBucket(bucket, covered, kindGaps) && !counted {
				cell.Gap = true
			} else if traffic.IsGapBucket(bucket, covered, kindGaps) {
				// Records exist in a bucket the collection did not cover whole: they are shown,
				// and the bucket still says it is incomplete.
				cell.Gap = true
				value := count
				cell.Value = &value
			} else {
				value := count
				cell.Value = &value
			}
			series.Buckets = append(series.Buckets, cell)
		}
		result = append(result, series)
	}
	return result, nil
}

// gapsOf keeps the gaps of one kind and opnview's own absences.
func gapsOf(gaps []envelope.Gap, kind string) []envelope.Gap {
	var kept []envelope.Gap
	for _, gap := range gaps {
		if gap.ProviderKind == kind || gap.ProviderKind == gapKindOpnview {
			kept = append(kept, gap)
		}
	}
	sort.Slice(kept, func(i, j int) bool { return kept[i].From < kept[j].From })
	return kept
}
