package installation

import (
	"github.com/dop251/goja"

	"github.com/NaejEL/opnview/internal/store/vocab"
	"github.com/NaejEL/opnview/internal/widget/script"
)

// host.samples: what the providers' plugins declare of the firewall's sampled readings. The readings
// themselves -- the subjects, the points, the interface counters and their buckets -- are the figures
// library's samples area since ADR 0021 (plugins/figures), over each widget's own statements.
//
//	host.samples.metrics  per health metric, the subject kind and the measures the sampler stores it
//	                      under, as the providers' plugins declare them (sample_metrics): {kind, measures}

// sampledMetric is a health metric's mapping as a script reads it.
type sampledMetric struct {
	Kind     string   `json:"kind"`
	Measures []string `json:"measures"`
}

func init() {
	script.RegisterHostArea("samples", func(call *script.ScriptCall, area *goja.Object) {
		metrics := map[string]sampledMetric{}
		for _, metric := range vocab.SampleMetrics() {
			metrics[metric.Name] = sampledMetric{Kind: metric.SubjectKind, Measures: append([]string(nil), metric.Measures...)}
		}
		_ = area.Set("metrics", call.Value(metrics))
	})
}
