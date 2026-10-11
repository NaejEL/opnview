package widget

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/NaejEL/opnview/internal/dataset"
)

// The fourth value shape of docs/dashboard-format.md, "Parameters": `series`, an ordered
// array of objects, each a flat map of scalars and reference objects. Its order is the
// draw order and the legend order, so it is kept; each entry is parsed on its own, so an
// unknown key, an invalid value or an unresolved reference in one entry concerns that
// entry and no other.

// The sources a series can be drawn from, and the measures and metrics each offers. The
// vocabulary is the API's own, documented in docs/widget-api.md: the catalogue's Custom
// chart names the keys (`source`, `metric` or `measure`, `axis`, `label`, `render`,
// `aggregation`) and leaves their values to the build.
const (
	SourceTraffic             = "traffic"
	SourceFirewallMetric      = "firewall_metric"
	SourceInterfaceThroughput = "interface_throughput"
	SourceDNS                 = "dns"
	SourceSecurityEvent       = "security_event"
)

var (
	SeriesSources = []string{SourceTraffic, SourceFirewallMetric, SourceInterfaceThroughput,
		SourceDNS, SourceSecurityEvent}
	SeriesMeasures = map[string][]string{
		SourceTraffic:             {"bytes", "connections", "allowed", "blocked", "netflow_bytes", "netflow_packets"},
		SourceInterfaceThroughput: {"bits_per_second", "bytes", "packets", "errors"},
		SourceDNS:                 {"lookups", "blocked"},
		SourceSecurityEvent:       {"events", "blocked"},
	}
	HealthMetrics      = []string{"uptime", "cpu", "memory", "swap", "disk", "temperature", "gateway_latency", "gateway_loss"}
	SeriesAxes         = []string{"left", "right"}
	SeriesRenders      = []string{"line", "area", "bars", "points"}
	SeriesAggregations = []string{"mean", "min", "max", "sum", "last"}
	// seriesReferenceKeys are the keys a series entry may scope itself with, each a
	// reference of the kind it is named after.
	seriesReferenceKeys = []string{"interface", "client", "rule", "provider", "country", "operator", "site"}
	// defaultAggregation is each source's aggregation when an entry names none: a count
	// is summed over a bucket, a gauge and a rate are averaged.
	defaultAggregation = map[string]string{
		SourceTraffic: "sum", SourceFirewallMetric: "mean", SourceInterfaceThroughput: "mean",
		SourceDNS: "sum", SourceSecurityEvent: "sum",
	}
)

// SeriesEntry is one parsed entry.
type SeriesEntry struct {
	Source      string
	Metric      string
	Measure     string
	Axis        string
	Label       string
	Render      string
	Aggregation string
	// references are the entry's reference objects by key.
	references map[string]Reference
	Echo       map[string]any
}

// SeriesEntryKeys is every key an entry accepts.
func SeriesEntryKeys() []string {
	keys := []string{"source", "metric", "measure", "axis", "label", "render", "aggregation"}
	return append(keys, seriesReferenceKeys...)
}

// parseSeries parses the series list. A value that is not an array is invalid whole; an
// entry that is not an object is invalid and dropped from the list, with its path.
func parseSeries(key string, raw json.RawMessage, datasets *dataset.Catalogue) (any, []InvalidValue, []referenceAt,
	map[string]json.RawMessage) {
	unknown := map[string]json.RawMessage{}
	var entries []json.RawMessage
	if json.Unmarshal(raw, &entries) != nil {
		return nil, []InvalidValue{{Path: key, Value: raw, Reason: ReasonWrongType}}, nil, unknown
	}
	var (
		result     []any
		invalid    []InvalidValue
		references []referenceAt
	)
	for index, rawEntry := range entries {
		path := fmt.Sprintf("%s[%d]", key, index)
		var fields map[string]json.RawMessage
		if json.Unmarshal(rawEntry, &fields) != nil {
			invalid = append(invalid, InvalidValue{Path: path, Value: rawEntry, Reason: ReasonWrongType})
			continue
		}
		entry := map[string]any{}
		for field, value := range fields {
			if !Contains(SeriesEntryKeys(), field) {
				unknown[path+"."+field] = value
			}
		}
		textField := func(field string, vocabulary []string, fallback string) {
			value, present := fields[field]
			if !present || isJSONNull(value) {
				if fallback != "" {
					entry[field] = fallback
				}
				return
			}
			var text string
			if json.Unmarshal(value, &text) != nil {
				invalid = append(invalid, InvalidValue{Path: path + "." + field, Value: value, Reason: ReasonWrongType})
				if fallback != "" {
					entry[field] = fallback
				}
				return
			}
			if vocabulary != nil && !Contains(vocabulary, text) {
				invalid = append(invalid, InvalidValue{Path: path + "." + field, Value: value,
					Reason: ReasonNotInVocabulary})
				if fallback != "" {
					entry[field] = fallback
				}
				return
			}
			entry[field] = text
		}
		declaration, fromDataset := seriesDataset(fields["source"], datasets)
		if fromDataset {
			// A dataset of the catalogue (P8): its measure columns and their aggregations are
			// the series' vocabulary, and the references, which scope the five sources above,
			// do not apply.
			entry["source"] = declaration.Name
			parseDatasetSeries(path, fields, entry, declaration, &invalid)
			result = append(result, entry)
			continue
		}
		textField("source", SeriesSources, SourceTraffic)
		source, _ := entry["source"].(string)
		if source == SourceFirewallMetric {
			textField("metric", HealthMetrics, "cpu")
			if value, present := fields["measure"]; present && !isJSONNull(value) {
				invalid = append(invalid, InvalidValue{Path: path + ".measure", Value: value,
					Reason: ReasonNotInVocabulary})
			}
		} else {
			textField("measure", SeriesMeasures[source], SeriesMeasures[source][0])
			if value, present := fields["metric"]; present && !isJSONNull(value) {
				invalid = append(invalid, InvalidValue{Path: path + ".metric", Value: value,
					Reason: ReasonNotInVocabulary})
			}
		}
		textField("axis", SeriesAxes, "left")
		textField("label", nil, "")
		textField("render", SeriesRenders, "line")
		textField("aggregation", SeriesAggregations, defaultAggregation[source])
		for _, referenceKey := range seriesReferenceKeys {
			value, present := fields[referenceKey]
			if !present || isJSONNull(value) {
				continue
			}
			reference, ok := decodeReference(value)
			if !ok {
				invalid = append(invalid, InvalidValue{Path: path + "." + referenceKey, Value: value,
					Reason: reasonNotAReference})
				continue
			}
			echo := referenceValue(reference)
			entry[referenceKey] = echo
			references = append(references, referenceAt{path: path + "." + referenceKey, key: referenceKey,
				series: index, reference: reference, expected: referenceKey, echo: echo})
		}
		result = append(result, entry)
	}
	if result == nil {
		result = []any{}
	}
	sort.Slice(invalid, func(i, j int) bool { return invalid[i].Path < invalid[j].Path })
	return result, invalid, references, unknown
}

// Entries returns the parsed series entries of the effective parameters, in order.
func (p parsedParameters) Entries(key string) []SeriesEntry {
	list, _ := p.Effective[key].([]any)
	var result []SeriesEntry
	for _, item := range list {
		fields, _ := item.(map[string]any)
		entry := SeriesEntry{references: map[string]Reference{}, Echo: fields}
		entry.Source, _ = fields["source"].(string)
		entry.Metric, _ = fields["metric"].(string)
		entry.Measure, _ = fields["measure"].(string)
		entry.Axis, _ = fields["axis"].(string)
		entry.Label, _ = fields["label"].(string)
		entry.Render, _ = fields["render"].(string)
		entry.Aggregation, _ = fields["aggregation"].(string)
		result = append(result, entry)
	}
	return result
}
