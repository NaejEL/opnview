package plugin

import (
	"testing"

	"github.com/NaejEL/opnview/internal/plugin/contract"
	"github.com/NaejEL/opnview/internal/store/vocab"
)

// TestTheHealthMetricsAreTheInsightPluginsDeclarations is PA11's sample-metric half: which subject kind
// and which measures serve each health metric is declared by the insight plugin's manifest, under
// sample_metrics, and the process reads it from there -- no Go file maps a metric to a measure.
func TestTheHealthMetricsAreTheInsightPluginsDeclarations(t *testing.T) {
	t.Parallel()
	loaded, found := BuiltinsLoaded().Plugin("insight")
	if !found {
		t.Fatal("no built-in insight plugin")
	}
	if len(loaded.Manifest.SampleMetrics) != 8 {
		t.Fatalf("the insight plugin declares %d sample metrics, not the catalogue's eight", len(loaded.Manifest.SampleMetrics))
	}
	for _, declared := range loaded.Manifest.SampleMetrics {
		registered, found := vocab.SampleMetricNamed(declared.Name)
		if !found || registered.Plugin != "insight" || registered.SubjectKind != declared.SubjectKind ||
			len(registered.Measures) != len(declared.Measures) {
			t.Errorf("%s is registered as %+v, declared as %+v", declared.Name, registered, declared)
		}
	}
	if swap, _ := vocab.SampleMetricNamed("swap"); len(swap.Measures) != 2 || swap.Measures[0] != "swap_used_bytes" {
		t.Errorf("swap reads %v, its used bytes first", swap.Measures)
	}
}

// TestASampleMetricIsValidatedAtLoad: a well-formed declaration of a plugin providing measurement_sample
// is accepted; one with no such provider, an ill-formed one, one declared twice and one another plugin
// declares are each refused with manifest_invalid.
func TestASampleMetricIsValidatedAtLoad(t *testing.T) {
	t.Parallel()
	sampler := []contract.ProviderDeclaration{{Kind: "measurement_sample"}}
	metric := vocab.SampleMetric{Name: "example_fan_speed", ProviderKind: "measurement_sample", SubjectKind: "firewall", Measures: []string{"fan_rpm"}}
	for name, test := range map[string]struct {
		providers []contract.ProviderDeclaration
		metrics   []vocab.SampleMetric
		refused   bool
	}{
		"a metric of its own":         {sampler, []vocab.SampleMetric{metric}, false},
		"no measurement_sample":       {[]contract.ProviderDeclaration{{Kind: "dhcp_lease"}}, []vocab.SampleMetric{metric}, true},
		"no measure":                  {sampler, []vocab.SampleMetric{{Name: "example_fan_speed", ProviderKind: "measurement_sample", SubjectKind: "firewall"}}, true},
		"a name that is not a word":   {sampler, []vocab.SampleMetric{{Name: "Fan speed", ProviderKind: "measurement_sample", SubjectKind: "firewall", Measures: []string{"fan_rpm"}}}, true},
		"a measure named twice":       {sampler, []vocab.SampleMetric{{Name: "example_fan_speed", ProviderKind: "measurement_sample", SubjectKind: "firewall", Measures: []string{"fan_rpm", "fan_rpm"}}}, true},
		"a metric declared twice":     {sampler, []vocab.SampleMetric{metric, metric}, true},
		"the insight plugin's metric": {sampler, []vocab.SampleMetric{{Name: "cpu", ProviderKind: "measurement_sample", SubjectKind: "firewall", Measures: []string{"cpu_use_ratio"}}}, true},
	} {
		refusal := checkSampleMetrics(contract.Manifest{Name: "example_sampler", Providers: test.providers,
			SampleMetrics: test.metrics})
		switch {
		case test.refused && (refusal == nil || refusal.Code != contract.RefusalManifestInvalid):
			t.Errorf("%s: refused with %+v, want manifest_invalid", name, refusal)
		case !test.refused && refusal != nil:
			t.Errorf("%s: a valid declaration was refused: %+v", name, refusal)
		}
	}
}
