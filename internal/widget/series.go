package widget

import (
	"encoding/json"
	"fmt"
	"sort"
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
	sourceTraffic             = "traffic"
	sourceFirewallMetric      = "firewall_metric"
	sourceInterfaceThroughput = "interface_throughput"
	sourceDNS                 = "dns"
	sourceSecurityEvent       = "security_event"
)

var (
	seriesSources = []string{sourceTraffic, sourceFirewallMetric, sourceInterfaceThroughput,
		sourceDNS, sourceSecurityEvent}
	seriesMeasures = map[string][]string{
		sourceTraffic:             {"bytes", "connections", "allowed", "blocked"},
		sourceInterfaceThroughput: {"bits_per_second", "bytes", "packets", "errors"},
		sourceDNS:                 {"lookups", "blocked"},
		sourceSecurityEvent:       {"events", "blocked"},
	}
	healthMetrics      = []string{"uptime", "cpu", "memory", "swap", "disk", "temperature", "gateway_latency", "gateway_loss"}
	seriesAxes         = []string{"left", "right"}
	seriesRenders      = []string{"line", "area", "bars", "points"}
	seriesAggregations = []string{"mean", "min", "max", "sum", "last"}
	// seriesReferenceKeys are the keys a series entry may scope itself with, each a
	// reference of the kind it is named after.
	seriesReferenceKeys = []string{"interface", "client", "rule", "provider", "country", "operator", "site"}
	// defaultAggregation is each source's aggregation when an entry names none: a count
	// is summed over a bucket, a gauge and a rate are averaged.
	defaultAggregation = map[string]string{
		sourceTraffic: "sum", sourceFirewallMetric: "mean", sourceInterfaceThroughput: "mean",
		sourceDNS: "sum", sourceSecurityEvent: "sum",
	}
)

// seriesEntry is one parsed entry.
type seriesEntry struct {
	Source      string
	Metric      string
	Measure     string
	Axis        string
	Label       string
	Render      string
	Aggregation string
	// references are the entry's reference objects by key.
	references map[string]Reference
	echo       map[string]any
}

// seriesEntryKeys is every key an entry accepts.
func seriesEntryKeys() []string {
	keys := []string{"source", "metric", "measure", "axis", "label", "render", "aggregation"}
	return append(keys, seriesReferenceKeys...)
}

// parseSeries parses the series list. A value that is not an array is invalid whole; an
// entry that is not an object is invalid and dropped from the list, with its path.
func parseSeries(key string, raw json.RawMessage) (any, []InvalidValue, []referenceAt,
	map[string]json.RawMessage) {
	unknown := map[string]json.RawMessage{}
	var entries []json.RawMessage
	if json.Unmarshal(raw, &entries) != nil {
		return nil, []InvalidValue{{Path: key, Value: raw, Reason: reasonWrongType}}, nil, unknown
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
			invalid = append(invalid, InvalidValue{Path: path, Value: rawEntry, Reason: reasonWrongType})
			continue
		}
		entry := map[string]any{}
		for field, value := range fields {
			if !contains(seriesEntryKeys(), field) {
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
				invalid = append(invalid, InvalidValue{Path: path + "." + field, Value: value, Reason: reasonWrongType})
				if fallback != "" {
					entry[field] = fallback
				}
				return
			}
			if vocabulary != nil && !contains(vocabulary, text) {
				invalid = append(invalid, InvalidValue{Path: path + "." + field, Value: value,
					Reason: reasonNotInVocabulary})
				if fallback != "" {
					entry[field] = fallback
				}
				return
			}
			entry[field] = text
		}
		textField("source", seriesSources, sourceTraffic)
		source, _ := entry["source"].(string)
		if source == sourceFirewallMetric {
			textField("metric", healthMetrics, "cpu")
			if value, present := fields["measure"]; present && !isJSONNull(value) {
				invalid = append(invalid, InvalidValue{Path: path + ".measure", Value: value,
					Reason: reasonNotInVocabulary})
			}
		} else {
			textField("measure", seriesMeasures[source], seriesMeasures[source][0])
			if value, present := fields["metric"]; present && !isJSONNull(value) {
				invalid = append(invalid, InvalidValue{Path: path + ".metric", Value: value,
					Reason: reasonNotInVocabulary})
			}
		}
		textField("axis", seriesAxes, "left")
		textField("label", nil, "")
		textField("render", seriesRenders, "line")
		textField("aggregation", seriesAggregations, defaultAggregation[source])
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

// entries returns the parsed series entries of the effective parameters, in order.
func (p parsedParameters) entries(key string) []seriesEntry {
	list, _ := p.effective[key].([]any)
	var result []seriesEntry
	for _, item := range list {
		fields, _ := item.(map[string]any)
		entry := seriesEntry{references: map[string]Reference{}, echo: fields}
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
