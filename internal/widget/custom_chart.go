package widget

import (
	"strconv"

	"github.com/NaejEL/opnview/internal/store"
)

// custom_chart: the time_series shape with no binding -- the reader names the series
// (docs/widget-catalogue.md, "Custom chart"). One query per series, every series on the
// same UTC-aligned buckets so a telemetry series and a traffic series share a time axis.
// Each series carries its own availability, coverage, gaps and unresolved references, so
// one missing source or one unresolved reference degrades its own line and no other.

var customChart = Preset{
	Type: "custom_chart", Shape: "time_series", Windowed: true,
	Params: []Param{
		periodParam(),
		{Key: "series", Kind: KindSeries, Default: []any{}, Data: true},
		{Key: "axes", Kind: KindObject},
		{Key: "legend", Kind: KindEnum, Values: []string{"table", "inline", "hidden"}, Default: "table"},
		{Key: "tooltip", Kind: KindEnum, Values: []string{"single", "all_series"}, Default: "single"},
		{Key: "annotations", Kind: KindEnumSet, Values: []string{"security_events", "blocked_spikes"},
			Default: []any{}, Data: true},
	},
	Serve: serveCustomChart,
}

// seriesProviders is the provider kinds a series of each source reads.
var seriesProviders = map[string][]string{
	sourceTraffic:             {"firewall_log"},
	sourceFirewallMetric:      {"measurement_sample"},
	sourceInterfaceThroughput: {"measurement_sample"},
	sourceDNS:                 {"dns_lookup"},
	sourceSecurityEvent:       {"security_event"},
}

// Annotation is one overlay of markers.
type Annotation struct {
	Kind    string   `json:"kind"`
	Buckets []Bucket `json:"buckets"`
}

// CustomChartData is the chart.
type CustomChartData struct {
	BucketSeconds int64        `json:"bucket_seconds"`
	Series        []Series     `json:"series"`
	Annotations   []Annotation `json:"annotations"`
}

func serveCustomChart(r *Request) (any, bool, error) {
	providers, err := readProviders(r.ctx, r.db)
	if err != nil {
		return nil, false, err
	}
	width := r.bucketWidth()
	data := CustomChartData{BucketSeconds: width, Series: []Series{}, Annotations: []Annotation{}}
	hasRows := false
	kinds := map[string]bool{}
	for index, entry := range r.params.entries("series") {
		kindsOf := seriesProviders[entry.Source]
		if entry.Source == sourceTraffic {
			if _, scoped := r.entries[index]["country"]; scoped {
				kindsOf = append(kindsOf, "geo_asn")
			}
			if _, scoped := r.entries[index]["operator"]; scoped {
				kindsOf = append(kindsOf, "geo_asn")
			}
			if _, scoped := r.entries[index]["site"]; scoped {
				kindsOf = append(kindsOf, "dns_lookup")
			}
		}
		series := Series{Key: "series:" + strconv.Itoa(index), Label: entry.Label, Echo: entry.echo,
			Aggregation: entry.Aggregation, Availability: []ProviderAvailability{}, Gaps: []Gap{},
			Unresolved: []UnresolvedReference{}, Buckets: []Bucket{}}
		for _, provider := range providers {
			if contains(kindsOf, provider.Kind) {
				series.Availability = append(series.Availability, provider)
			}
		}
		for _, kind := range kindsOf {
			kinds[kind] = true
		}
		prefix := "series[" + strconv.Itoa(index) + "]."
		for _, unresolvedReference := range r.response.UnresolvedReferences {
			if len(unresolvedReference.Path) > len(prefix) && unresolvedReference.Path[:len(prefix)] == prefix {
				series.Unresolved = append(series.Unresolved, unresolvedReference)
			}
		}
		requested := &WindowReport{Requested: r.window, Coverage: coverageNone,
			FlowHorizonAt: r.response.Window.FlowHorizonAt}
		series.Window = requested
		if r.failed[index] {
			series.Condition = ConditionUnresolvedReference
			if r.withheldSeries[index] {
				// Scoped by a site under no_domains: no data, whatever the name, so the
				// series cannot tell which names were visited (A10, decision 6).
				series.Condition = ConditionWithheldByAggregateMode
			}
			series.Buckets = nil
			data.Series = append(data.Series, series)
			continue
		}
		series.Gaps, err = r.readGaps(kindsOf, r.window)
		if err != nil {
			return nil, false, err
		}
		rows, err := r.fillSeries(&series, entry, r.entries[index], width)
		if err != nil {
			return nil, false, err
		}
		series.Condition = kindCondition(series.Availability, kindsOf[0])
		if series.Condition == ConditionReachable && !rows {
			series.Condition = ConditionReachableNoRows
		}
		if series.Condition != ConditionReachable && series.Condition != ConditionReachableNoRows && !rows {
			series.Buckets = nil
			series.Total = nil
		}
		hasRows = hasRows || rows
		data.Series = append(data.Series, series)
	}
	for _, kind := range providerKinds {
		if kinds[kind] {
			r.preset.Providers = append(r.preset.Providers, kind)
		}
	}
	for _, provider := range providers {
		if kinds[provider.Kind] {
			r.response.Availability = append(r.response.Availability, provider)
		}
	}
	if len(data.Series) > 0 {
		r.cover(data.Series[0].Window.Covered)
	}
	for _, kind := range r.params.set("annotations") {
		annotation, err := r.annotation(kind, width)
		if err != nil {
			return nil, false, err
		}
		data.Annotations = append(data.Annotations, annotation)
	}
	return data, hasRows, nil
}

// fillSeries reads one series into its buckets and records its coverage.
func (r *Request) fillSeries(series *Series, entry seriesEntry, references map[string]resolved,
	width int64) (bool, error) {
	window := series.Window
	setCovered := func(covered *Interval) {
		switch {
		case covered == nil:
			window.Covered, window.Coverage = nil, coverageNone
		case covered.From <= window.Requested.From && covered.To >= window.Requested.To:
			window.Covered, window.Coverage = covered, coverageFull
		default:
			window.Covered, window.Coverage = covered, coveragePartial
		}
	}
	idOf := func(key string) *int64 {
		if value, present := references[key]; present {
			id := value.id
			return &id
		}
		return nil
	}
	switch entry.Source {
	case sourceTraffic:
		filter := trafficFilter{clientID: idOf("client"), ruleID: idOf("rule"), asn: idOf("operator")}
		if value, present := references["interface"]; present {
			filter.interfaces = []int64{value.id}
		}
		if value, present := references["country"]; present {
			text := value.text
			filter.country = &text
		}
		if value, present := references["site"]; present {
			text := value.text
			filter.site = &text
		}
		buckets, covered, err := r.trafficBuckets(filter, width)
		if err != nil {
			return false, err
		}
		setCovered(covered)
		series.Measure, series.Unit, series.LowerBound = MeasureLogged, entry.Measure, true
		figure := map[string]string{"bytes": "bytes", "connections": "connections", "allowed": "allowed",
			"blocked": "blocked"}[entry.Measure]
		var total store.VolumeFigures
		rows := false
		for _, interval := range bucketIntervals(r.window, width) {
			start := interval.From - floorMod(interval.From, width)
			var sum store.VolumeFigures
			present := false
			for _, figures := range buckets[start] {
				addFigures(&sum, figures)
				present = true
			}
			bucket := Bucket{From: interval.From, To: interval.To, Gap: isGapBucket(interval, covered, series.Gaps)}
			if present || !bucket.Gap {
				value := figuresValue(figure, sum)
				volume := logged(sum)
				bucket.Value, bucket.Volume = &value, &volume
				addFigures(&total, sum)
				rows = rows || present
			}
			series.Buckets = append(series.Buckets, bucket)
		}
		value := figuresValue(figure, total)
		series.Total = &value
		return rows, nil
	case sourceFirewallMetric:
		if err := r.coverSamplesInto(setCovered); err != nil {
			return false, err
		}
		subjects, err := r.measuredSubjects(entry.Metric)
		if err != nil {
			return false, err
		}
		var points []Point
		for _, item := range subjects {
			if item.measure != string(metricMeasures[entry.Metric].measures[0]) {
				continue
			}
			series.Unit = item.unit
			read, err := r.samplesOf(item)
			if err != nil {
				return false, err
			}
			points = append(points, read...)
		}
		series.Measure = "sampled_gauge"
		series.Buckets = aggregateBuckets(r.window, width, points, entry.Aggregation)
		return len(points) > 0, nil
	case sourceInterfaceThroughput:
		if err := r.coverSamplesInto(setCovered); err != nil {
			return false, err
		}
		cache, err := r.install()
		if err != nil {
			return false, err
		}
		mapping := throughputMeasures[entry.Measure]
		var points []Point
		for _, info := range cache.interfaces {
			if id := idOf("interface"); id != nil && *id != info.ID {
				continue
			}
			for _, counter := range []store.Measure{mapping.in, mapping.out} {
				read, _, err := r.throughputPoints(info.Device, counter, mapping.perSecond, mapping.factor)
				if err != nil {
					return false, err
				}
				points = append(points, read...)
			}
		}
		series.Measure, series.Unit, series.LowerBound = MeasureInterfaceCounter, entry.Measure, true
		series.Buckets = aggregateBuckets(r.window, width, points, entry.Aggregation)
		return len(points) > 0, nil
	case sourceDNS, sourceSecurityEvent:
		name, earliestName := "widget_dns_buckets", "widget_earliest_lookup"
		if entry.Source == sourceSecurityEvent {
			name, earliestName = "widget_security_buckets", "widget_earliest_security_event"
		}
		earliest, has, err := r.earliest(earliestName)
		if err != nil {
			return false, err
		}
		var covered *Interval
		if has {
			covered = &Interval{From: max(r.window.From, earliest), To: min(r.window.To, r.now)}
			if covered.To <= covered.From {
				covered = nil
			}
		}
		setCovered(covered)
		parameters := r.windowParameters()
		parameters["width"] = width
		parameters["client_id"] = optionalID(idOf("client"))
		parameters["interface_ids"] = nil
		if id := idOf("interface"); id != nil {
			parameters["interface_ids"] = jsonIDs([]int64{*id})
		}
		parameters["provider_id"] = optionalID(idOf("provider"))
		parameters["resolver"] = nil
		if value, present := references["provider"]; present && entry.Source == sourceDNS {
			// A DNS series scoped to a resolver reads that resolver's lookups.
			parameters["resolver"] = value.text
		}
		counts := map[int64][2]int64{}
		err = r.db.WidgetRows(r.ctx, name, store.Period{}, parameters, func(row store.RowScanner) error {
			var start, all, blocked int64
			if err := row.Scan(&start, &all, &blocked); err != nil {
				return err
			}
			counts[start] = [2]int64{all, blocked}
			return nil
		})
		if err != nil {
			return false, err
		}
		series.Measure, series.Unit, series.LowerBound = entry.Source, entry.Measure, true
		rows := false
		var total float64
		for _, interval := range bucketIntervals(r.window, width) {
			start := interval.From - floorMod(interval.From, width)
			pair, present := counts[start]
			bucket := Bucket{From: interval.From, To: interval.To, Gap: isGapBucket(interval, covered, series.Gaps)}
			if present || !bucket.Gap {
				value := float64(pair[0])
				if entry.Measure == "blocked" {
					value = float64(pair[1])
				}
				bucket.Value = &value
				total += value
				rows = rows || present
			}
			series.Buckets = append(series.Buckets, bucket)
		}
		series.Total = &total
		return rows, nil
	}
	return false, nil
}

// coverSamplesInto records a sampled series' coverage.
func (r *Request) coverSamplesInto(set func(*Interval)) error {
	earliest, has, err := r.earliest("widget_earliest_sample")
	if err != nil {
		return err
	}
	if !has {
		set(nil)
		return nil
	}
	covered := &Interval{From: max(r.window.From, earliest), To: min(r.window.To, r.now)}
	if covered.To <= covered.From {
		covered = nil
	}
	set(covered)
	return nil
}

// annotation reads one overlay: the buckets holding a security event, or a blocked
// connection, with their counts.
func (r *Request) annotation(kind string, width int64) (Annotation, error) {
	annotation := Annotation{Kind: kind, Buckets: []Bucket{}}
	if kind == "security_events" {
		parameters := r.windowParameters()
		parameters["width"] = width
		parameters["provider_id"], parameters["client_id"], parameters["interface_ids"] = nil, nil, nil
		err := r.db.WidgetRows(r.ctx, "widget_security_buckets", store.Period{}, parameters,
			func(row store.RowScanner) error {
				var start, all, blocked int64
				if err := row.Scan(&start, &all, &blocked); err != nil {
					return err
				}
				value := float64(all)
				annotation.Buckets = append(annotation.Buckets, Bucket{From: start, To: start + width, Value: &value})
				return nil
			})
		return annotation, err
	}
	buckets, _, err := r.trafficBuckets(trafficFilter{}, width)
	if err != nil {
		return annotation, err
	}
	for _, start := range sortedStarts(buckets) {
		var sum store.VolumeFigures
		for _, figures := range buckets[start] {
			addFigures(&sum, figures)
		}
		if sum.BlockedConnections == 0 {
			continue
		}
		value := float64(sum.BlockedConnections)
		annotation.Buckets = append(annotation.Buckets, Bucket{From: start, To: start + width, Value: &value})
	}
	return annotation, nil
}
