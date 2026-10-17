package frontend_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/NaejEL/opnview/internal/web/webtest"
	"github.com/NaejEL/opnview/internal/widget/params"
	"github.com/NaejEL/opnview/internal/widget/request"
)

// F7-AC11: GET /api/widgets answers every type the widget service serves, each with exactly its
// manifest's parameters, and volume_measure exactly on the types declaring volumes.

func TestTheRegistryListsEveryServedTypeWithItsManifestsParameters(t *testing.T) {
	t.Parallel()
	harness := webtest.New(t)
	harness.CompleteSetup()
	var listed registry
	if status := getJSON(t, harness, "/api/widgets", &listed); status != http.StatusOK {
		t.Fatalf("GET /api/widgets answered %d", status)
	}
	service := harness.Server.Widgets()
	rows := map[string]int{}
	for index, row := range listed.Widgets {
		rows[row.Type] = index
	}
	served := service.Types()
	if len(served) < 20 {
		t.Fatalf("only %d types are served", len(served))
	}
	for _, widgetType := range append(served, service.FrontOnlyTypes()...) {
		index, found := rows[widgetType]
		if !found {
			t.Errorf("the registry does not list %s", widgetType)
			continue
		}
		preset, _ := service.Known(widgetType)
		want := append([]params.ManifestParameter{}, preset.Manifest.Parameters...)
		if preset.Volumes {
			core := request.VolumeMeasureParam()
			want = append(want, params.ManifestParameter{Key: core.Key, Kind: string(core.Kind), Values: core.Values,
				Default: core.Default, Class: params.ClassData})
		}
		got, _ := json.Marshal(listed.Widgets[index].Parameters)
		expected, _ := json.Marshal(want)
		if string(got) != string(expected) {
			t.Errorf("%s lists the parameters %s, its manifest declares %s", widgetType, got, expected)
		}
		hasVolumeMeasure := false
		for _, parameter := range listed.Widgets[index].Parameters {
			hasVolumeMeasure = hasVolumeMeasure || parameter.Key == params.VolumeMeasureKey
		}
		if hasVolumeMeasure != preset.Volumes {
			t.Errorf("%s: volume_measure listed %v, volumes declared %v", widgetType, hasVolumeMeasure, preset.Volumes)
		}
	}
	if len(listed.Widgets) != len(served)+len(service.FrontOnlyTypes()) {
		t.Errorf("the registry lists %d widgets, the service knows %d", len(listed.Widgets),
			len(served)+len(service.FrontOnlyTypes()))
	}
	anonymous := webtest.FreshClient(t, harness)
	if status, _ := webtest.StatusAndBody(t, anonymous.Get("/api/widgets")); status != http.StatusUnauthorized {
		t.Errorf("the registry answered %d with no session", status)
	}
}
