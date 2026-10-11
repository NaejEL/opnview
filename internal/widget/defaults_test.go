package widget

import (
	"encoding/json"
	"testing"
)

// defaultParameters are the parameters a type needs beyond its defaults to answer at all:
// client_traffic_detail's required client.
func defaultParameters(f *fixture, widgetType string) map[string]any {
	if widgetType == "client_traffic_detail" {
		return map[string]any{"client": map[string]any{"kind": "client", "by": "mac",
			"value": "00:00:5e:00:53:00"}}
	}
	return nil
}

// TestEveryServedTypeAnswersAtItsDefaultsWithReachableData is the smoke test of the whole
// registry: every served type, at its defaults, for every period, answers the envelope with
// data on a populated installation, serialises, and reports the condition of a healthy
// source.
func TestEveryServedTypeAnswersAtItsDefaultsWithReachableData(t *testing.T) {
	t.Parallel()
	f := newFixture(t, defaultSpec)
	for _, widgetType := range f.service.Types() {
		for _, period := range []string{"1h", "24h", "7d", "30d"} {
			parameters := defaultParameters(f, widgetType)
			if parameters == nil {
				parameters = map[string]any{}
			}
			if contains(paramKeys(f.service, widgetType), "period") {
				parameters["period"] = period
			}
			response := f.respond(t, widgetType, parameters)
			if _, err := json.Marshal(response); err != nil {
				t.Errorf("%s: the response does not serialise: %v", widgetType, err)
			}
			if response.Condition != ConditionReachable && response.Condition != ConditionReachableNoRows {
				t.Errorf("%s %s: condition %q on a healthy installation (unresolved %+v, missing %v)", widgetType,
					period, response.Condition, response.UnresolvedReferences, response.MissingParameters)
			}
			if response.Data == nil {
				t.Errorf("%s %s: no data on a healthy installation", widgetType, period)
			}
			if len(response.InvalidParameters) != 0 || len(response.UnknownParameters) != 0 {
				t.Errorf("%s: defaults reported as invalid or unknown: %+v %+v", widgetType,
					response.InvalidParameters, response.UnknownParameters)
			}
		}
	}
}

// paramKeys lists a served type's declared keys.
func paramKeys(service *Service, widgetType string) []string {
	preset, _ := service.Preset(widgetType)
	var keys []string
	for _, param := range preset.Params {
		keys = append(keys, param.Key)
	}
	return keys
}
