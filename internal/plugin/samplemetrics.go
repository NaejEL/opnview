package plugin

import (
	"github.com/NaejEL/opnview/internal/plugin/contract"
	"github.com/NaejEL/opnview/internal/store/vocab"
)

// The sample metrics a plugin declares (PA11 of the plugin-API amendment to
// specs/SPEC-backend-completion-plugins.md; docs/plugins.md, "Sample metrics"): which subject kind and
// which measures of measurement_sample serve a health metric. A declaration is checked whole at load --
// its shape, a provider of measurement_sample in the same plugin, no metric declared twice, none another
// plugin declares -- and, once the plugin is accepted by a load given a store, registered for the process.

// sampleKind is the kind whose readings a sample metric reads.
const sampleKind = "measurement_sample"

// checkSampleMetrics checks the sample metrics a manifest declares.
func checkSampleMetrics(manifest contract.Manifest) *contract.Refusal {
	if len(manifest.SampleMetrics) == 0 {
		return nil
	}
	provides := false
	for _, declaration := range manifest.Providers {
		provides = provides || declaration.Kind == sampleKind
	}
	if !provides {
		return contract.Refuse(manifest.Name, contract.RefusalManifestInvalid, "sample_metrics are read from the "+
			"readings of a %s provider, and the plugin provides none", sampleKind)
	}
	seen := map[string]bool{}
	for _, metric := range manifest.SampleMetrics {
		if reason := metric.Validate(); reason != "" {
			return contract.Refuse(manifest.Name, contract.RefusalManifestInvalid, "%s", reason)
		}
		if seen[metric.Name] {
			return contract.Refuse(manifest.Name, contract.RefusalManifestInvalid, "the sample metric %s is declared "+
				"twice", metric.Name)
		}
		if held, taken := vocab.SampleMetricNamed(metric.Name); taken && held.Plugin != manifest.Name {
			return contract.Refuse(manifest.Name, contract.RefusalManifestInvalid, "the sample metric %s is declared "+
				"by the plugin %s", metric.Name, held.Plugin)
		}
		seen[metric.Name] = true
	}
	return nil
}

// registerSampleMetrics records an accepted plugin's sample metrics for the process.
func registerSampleMetrics(manifest contract.Manifest) *contract.Refusal {
	for _, metric := range manifest.SampleMetrics {
		metric.Plugin = manifest.Name
		if err := vocab.RegisterSampleMetric(metric); err != nil {
			return contract.Refuse(manifest.Name, contract.RefusalManifestInvalid, "%v", err)
		}
	}
	return nil
}
