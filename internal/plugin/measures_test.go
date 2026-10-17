package plugin

import (
	"encoding/json"
	"testing"

	"github.com/NaejEL/opnview/internal/plugin/contract"
)

// TestAMeasureIsValidatedAtLoad is PA5's load check: the netflow plugin's measure, declared by another
// plugin under a name of its own and its own catalogue key, loads; an ill-formed declaration, one needing
// a kind none of the plugin's providers implements, one labelled outside its catalogue, and one taking
// the netflow plugin's measure's name, are each refused with measure_invalid.
func TestAMeasureIsValidatedAtLoad(t *testing.T) {
	t.Parallel()
	base, script := netflowManifest(t)
	raw, err := json.Marshal(base)
	if err != nil {
		t.Fatal(err)
	}
	original, _ := netflowManifestMeasure(t)
	for name, test := range map[string]struct {
		change  func(measure map[string]any)
		refused bool
	}{
		"its own name and key":           {func(map[string]any) {}, false},
		"a state undeclared as complete": {func(m map[string]any) { m["complete_state"] = "nowhere" }, true},
		"a kind it does not provide":     {func(m map[string]any) { m["provider_kind"] = "dhcp_lease" }, true},
		"another plugin's label":         {func(m map[string]any) { m["label"] = "plugin.netflow.measure.netflow" }, true},
		"the netflow plugin's name":      {func(m map[string]any) { m["name"] = "netflow" }, true},
		"a reserved name":                {func(m map[string]any) { m["name"] = "logged" }, true},
	} {
		var manifest map[string]any
		if err := json.Unmarshal(raw, &manifest); err != nil {
			t.Fatal(err)
		}
		measure := map[string]any{}
		for key, value := range original {
			measure[key] = value
		}
		measure["name"], measure["label"] = "example_push_rate", "plugin.example_push.measure.rate"
		test.change(measure)
		manifest["measures"] = []any{measure}
		refusals := loadManifest(t, manifest, script).Refusals()
		switch {
		case test.refused && (len(refusals) != 1 || refusals[0].Code != contract.RefusalMeasureInvalid):
			t.Errorf("%s: the refusals are %+v, want one measure_invalid", name, refusals)
		case !test.refused && len(refusals) != 0:
			t.Errorf("%s: a valid measure was refused: %+v", name, refusals)
		}
	}
}

// netflowManifestMeasure is the measure the netflow plugin's manifest declares, as JSON values.
func netflowManifestMeasure(t *testing.T) (map[string]any, bool) {
	t.Helper()
	for _, loaded := range BuiltinsLoaded().Plugins() {
		for _, measure := range loaded.Manifest.Measures {
			raw, err := json.Marshal(measure)
			if err != nil {
				t.Fatal(err)
			}
			var value map[string]any
			if err := json.Unmarshal(raw, &value); err != nil {
				t.Fatal(err)
			}
			return value, true
		}
	}
	t.Fatal("no built-in plugin declares a measure")
	return nil, false
}
