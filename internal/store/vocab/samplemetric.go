package vocab

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"sync"

	opnview "github.com/NaejEL/opnview"
)

// The sample metrics plugins declare (PA11 of the plugin-API amendment to
// specs/SPEC-backend-completion-plugins.md; docs/plugins.md, "Sample metrics").
//
// A SAMPLE METRIC is a health metric of the catalogue -- cpu, memory, gateway_latency -- read from
// measurement_sample: which subject kind it is stored under, and which measures. Which subject and
// which measures is a fact about the provider that writes the readings, so the provider's plugin
// declares it in its manifest, under `sample_metrics`, and no Go file maps a metric to a measure.
// The widget host reads the declarations (host.samples.metrics).

// SampleMetric is one declared sample metric.
type SampleMetric struct {
	// Name is the health metric it serves.
	Name string `json:"name"`
	// ProviderKind is the kind whose readings it reads, a kind the declaring plugin provides.
	ProviderKind string `json:"provider_kind"`
	// SubjectKind is the subject kind its readings are stored under.
	SubjectKind string `json:"subject_kind"`
	// Measures are the measures it reads, in the order a widget reads them; the first is its own figure.
	Measures []string `json:"measures"`
	// Plugin is the plugin that declares it; set by the loader, never by the manifest.
	Plugin string `json:"-"`
}

// sampleTerm is the shape of a metric's name, its subject kind and its measures: the terms
// measurement_sample itself constrains -- lower case, no space -- in snake_case.
var sampleTerm = regexp.MustCompile(`^[a-z][a-z0-9_]{0,47}$`)

// Validate names what is wrong with a declaration, or answers "".
func (m SampleMetric) Validate() string {
	switch {
	case !sampleTerm.MatchString(m.Name):
		return fmt.Sprintf("the sample metric name %q is not a snake_case word of at most 48 characters", m.Name)
	case !sampleTerm.MatchString(m.ProviderKind):
		return fmt.Sprintf("the sample metric %s names no provider_kind", m.Name)
	case !sampleTerm.MatchString(m.SubjectKind):
		return fmt.Sprintf("the sample metric %s names no subject_kind", m.Name)
	case len(m.Measures) == 0:
		return fmt.Sprintf("the sample metric %s names no measure", m.Name)
	}
	seen := map[string]bool{}
	for _, measure := range m.Measures {
		if !sampleTerm.MatchString(measure) || seen[measure] {
			return fmt.Sprintf("the sample metric %s's measure %q is not a snake_case word named once", m.Name, measure)
		}
		seen[measure] = true
	}
	return ""
}

var sampleMetrics = struct {
	mutex  sync.RWMutex
	byName map[string]SampleMetric
}{byName: map[string]SampleMetric{}}

// RegisterSampleMetric records a declared sample metric for the process. A metric another plugin
// declares is refused; the same plugin declaring it again replaces it.
func RegisterSampleMetric(metric SampleMetric) error {
	if reason := metric.Validate(); reason != "" {
		return fmt.Errorf("vocab: %s", reason)
	}
	sampleMetrics.mutex.Lock()
	defer sampleMetrics.mutex.Unlock()
	if held, taken := sampleMetrics.byName[metric.Name]; taken && held.Plugin != metric.Plugin {
		return fmt.Errorf("vocab: the sample metric %s is declared by the plugin %s", metric.Name, held.Plugin)
	}
	sampleMetrics.byName[metric.Name] = metric
	return nil
}

// SampleMetricNamed is a registered sample metric.
func SampleMetricNamed(name string) (SampleMetric, bool) {
	sampleMetrics.mutex.RLock()
	defer sampleMetrics.mutex.RUnlock()
	metric, found := sampleMetrics.byName[name]
	return metric, found
}

// SampleMetrics are the registered sample metrics, in name order.
func SampleMetrics() []SampleMetric {
	sampleMetrics.mutex.RLock()
	defer sampleMetrics.mutex.RUnlock()
	all := make([]SampleMetric, 0, len(sampleMetrics.byName))
	for _, metric := range sampleMetrics.byName {
		all = append(all, metric)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Name < all[j].Name })
	return all
}

// SampleMetricsDeclaredIn reads the sample metrics the plugins of a tree declare, each with its plugin,
// in the plugins' order. It validates nothing: RegisterSampleMetric does.
func SampleMetricsDeclaredIn(plugins fs.FS) ([]SampleMetric, error) {
	entries, err := fs.ReadDir(plugins, ".")
	if err != nil {
		return nil, fmt.Errorf("vocab: reading the plugins: %w", err)
	}
	var declared []SampleMetric
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		raw, err := fs.ReadFile(plugins, path.Join(entry.Name(), "plugin.json"))
		if err != nil {
			return nil, fmt.Errorf("vocab: reading %s/plugin.json: %w", entry.Name(), err)
		}
		var manifest struct {
			Name          string         `json:"name"`
			SampleMetrics []SampleMetric `json:"sample_metrics"`
		}
		if err := json.Unmarshal(raw, &manifest); err != nil {
			return nil, fmt.Errorf("vocab: %s/plugin.json: %w", entry.Name(), err)
		}
		for _, metric := range manifest.SampleMetrics {
			metric.Plugin = manifest.Name
			declared = append(declared, metric)
		}
	}
	return declared, nil
}

// builtinSampleMetricsErr is what registering the built-ins' sample metrics met at start.
var builtinSampleMetricsErr error

// BuiltinSampleMetricsError is what registering the built-ins' sample metrics met at start: a built-in
// declaring an invalid one is a defect of the build.
func BuiltinSampleMetricsError() error { return builtinSampleMetricsErr }

// The built-ins' sample metrics are known to the process from its start, as their measures are
// (measure.go): a widget answered in a process that loads no plugin reads them too. The loader
// registers the same declarations again, which is accepted.
func init() {
	declared, err := SampleMetricsDeclaredIn(opnview.BuiltinPlugins())
	if err != nil {
		builtinSampleMetricsErr = err
		return
	}
	for _, metric := range declared {
		if err := RegisterSampleMetric(metric); err != nil {
			builtinSampleMetricsErr = err
			return
		}
	}
}
