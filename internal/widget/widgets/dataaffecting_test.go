package widgets

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/NaejEL/opnview/internal/store/vocab"
	"github.com/NaejEL/opnview/internal/widget/params"
)

// AC6: every data-affecting parameter of every served type changes the response when its
// value changes, on a fixture built to make it; every rendering-only one is echoed.

// alternatives are the values a key is tried with beside its default. A key whose
// alternatives come from its own vocabulary or kind needs no entry here.
func alternatives(f *fixture, widgetType string, param params.Param) []any {
	if param.Kind == params.KindSeries {
		return []any{[]any{map[string]any{"source": "traffic"}}, []any{map[string]any{"source": "dns"}}}
	}
	switch param.Key {
	case "interfaces":
		return []any{[]any{map[string]any{"kind": "interface", "by": "device", "value": f.interfaces[0].device}}}
	case "clients":
		return []any{[]any{map[string]any{"kind": "client", "by": "mac", "value": "00:00:5e:00:53:00"}}}
	case "client":
		return []any{map[string]any{"kind": "client", "by": "mac", "value": "00:00:5e:00:53:04"}}
	case "owners":
		return []any{[]any{map[string]any{"kind": "owner", "by": "display_name", "value": "Example Person A"}}}
	case "blocklists":
		return []any{[]any{"example-list-a"}}
	case "gateways":
		return []any{[]any{"no-such-gateway"}}
	case "thresholds":
		return []any{map[string]any{"cpu": map[string]any{"warning": 0}}}
	case "min_share":
		return []any{0.5}
	case "min_bytes", "min_count", "min_flows":
		return []any{1e9, 1}
	case "history_limit":
		return []any{1}
	case "depth":
		return []any{1}
	case "period":
		return []any{"1h", "7d", "30d"}
	}
	var result []any
	switch param.Kind {
	case params.KindEnum:
		for _, value := range param.Values {
			if value != param.Default {
				result = append(result, value)
			}
		}
	case params.KindEnumSet:
		for _, value := range param.Values {
			result = append(result, []any{value})
		}
	case params.KindBoolean:
		flag, _ := param.Default.(bool)
		result = append(result, !flag)
	case params.KindInteger, params.KindNumber:
		result = append(result, float64(1), float64(2))
	}
	return result
}

// TestEveryDataParameterChangesTheDataAndEveryRenderingOneIsEchoed is AC6.
func TestEveryDataParameterChangesTheDataAndEveryRenderingOneIsEchoed(t *testing.T) {
	t.Parallel()
	f := newFixture(t, defaultSpec)
	// Two earlier addresses on the upstream interface, so the history has something to cut.
	bits := int64(24)
	for index, address := range []string{"198.51.100.201", "198.51.100.202"} {
		check(t, f.db.UpsertInterfaceAddress(context.Background(), vocab.InterfaceAddress{InterfaceID: f.upstream.id,
			SourceField: vocab.SourceFieldAddr4, Address: address, PrefixLength: &bits, AddressFamily: 4}, nil,
			f.now-500000+int64(index)*1000))
	}
	for _, widgetType := range f.service.Types() {
		preset, _ := f.service.Preset(widgetType)
		base := withPeriod(f, widgetType, "24h")
		if widgetType == "custom_chart" {
			base["series"] = []any{map[string]any{"source": "traffic"}, map[string]any{"source": "firewall_metric"}}
		}
		reference := f.respond(t, widgetType, base)
		referenceData, _ := json.Marshal(reference.Data)
		for _, param := range preset.Params {
			if !param.Data {
				value := renderingValue(param)
				parameters := copyParameters(base)
				parameters[param.Key] = value
				response := f.respond(t, widgetType, parameters)
				if !reflect.DeepEqual(normalise(response.Parameters[param.Key]), normalise(value)) {
					t.Errorf("%s: the rendering-only %s was not echoed: %v", widgetType, param.Key,
						response.Parameters[param.Key])
				}
				continue
			}
			changed := false
			for _, value := range alternatives(f, widgetType, param) {
				parameters := copyParameters(base)
				parameters[param.Key] = value
				response := f.respond(t, widgetType, parameters)
				encoded, _ := json.Marshal(response.Data)
				if string(encoded) != string(referenceData) {
					changed = true
					break
				}
			}
			if !changed {
				t.Errorf("%s: no value of the data-affecting %s changed the data", widgetType, param.Key)
			}
		}
	}
}

// renderingValue is a value of a rendering-only key other than its default.
func renderingValue(param params.Param) any {
	switch param.Kind {
	case params.KindBoolean:
		flag, _ := param.Default.(bool)
		return !flag
	case params.KindEnum:
		for _, value := range param.Values {
			if value != param.Default {
				return value
			}
		}
	case params.KindEnumSet:
		return []any{param.Values[0]}
	case params.KindObject:
		return map[string]any{"left": map[string]any{"unit": "example"}}
	}
	return param.Default
}

func copyParameters(parameters map[string]any) map[string]any {
	copied := map[string]any{}
	for key, value := range parameters {
		copied[key] = value
	}
	return copied
}

// normalise passes a value through JSON, as the service sees it.
func normalise(value any) any {
	encoded, _ := json.Marshal(value)
	var decoded any
	_ = json.Unmarshal(encoded, &decoded)
	return decoded
}
