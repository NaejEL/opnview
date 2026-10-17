package plugin

import (
	"strings"

	"github.com/NaejEL/opnview/internal/plugin/contract"
	"github.com/NaejEL/opnview/internal/store/vocab"
)

// The measures a plugin declares (PA5 of the plugin-API amendment to specs/SPEC-backend-completion-plugins.md;
// docs/plugins.md, "Measures"). A measure is checked whole at load: its own shape (vocab.DeclaredMeasure.Validate),
// the kind it needs an active provider of -- one the plugin itself provides -- its label in the plugin's own
// catalogue, and a name no other plugin's measure has. Once the plugin is accepted, its measures are
// registered, by a load given a store, for the process and in that store.

// checkMeasures checks the measures a manifest declares.
func checkMeasures(manifest contract.Manifest) *contract.Refusal {
	provided := map[string]bool{}
	for _, declaration := range manifest.Providers {
		provided[declaration.Kind] = true
	}
	namespace := "plugin." + manifest.Name + "."
	seen := map[string]bool{}
	for _, measure := range manifest.Measures {
		if reason := measure.Validate(); reason != "" {
			return contract.Refuse(manifest.Name, contract.RefusalMeasureInvalid, "%s", reason)
		}
		switch {
		case seen[measure.Name]:
			return contract.Refuse(manifest.Name, contract.RefusalMeasureInvalid, "the measure %s is declared twice",
				measure.Name)
		case !provided[measure.ProviderKind]:
			return contract.Refuse(manifest.Name, contract.RefusalMeasureInvalid, "the measure %s needs a provider of "+
				"the kind %s, which none of the plugin's providers is", measure.Name, measure.ProviderKind)
		case !strings.HasPrefix(measure.Label, namespace):
			return contract.Refuse(manifest.Name, contract.RefusalMeasureInvalid, "the measure %s names its label by a "+
				"key of the plugin's own catalogue, %s*", measure.Name, namespace)
		}
		if held, taken := vocab.MeasureNamed(measure.Name); taken && held.Plugin != "" && held.Plugin != manifest.Name {
			return contract.Refuse(manifest.Name, contract.RefusalMeasureInvalid, "the measure %s is declared by the "+
				"plugin %s", measure.Name, held.Plugin)
		}
		seen[measure.Name] = true
	}
	return nil
}

// registerMeasures records an accepted plugin's measures for the process.
func registerMeasures(manifest contract.Manifest) *contract.Refusal {
	for _, measure := range manifest.Measures {
		measure.Plugin = manifest.Name
		if err := vocab.RegisterMeasure(measure); err != nil {
			return contract.Refuse(manifest.Name, contract.RefusalMeasureInvalid, "%v", err)
		}
	}
	return nil
}

// Measures returns the measures every loaded plugin declares, each with its plugin.
func (l *Loaded) Measures() []vocab.DeclaredMeasure {
	var measures []vocab.DeclaredMeasure
	for _, member := range l.Members {
		for _, measure := range member.Manifest.Measures {
			measure.Plugin = member.Name
			measures = append(measures, measure)
		}
	}
	return measures
}
