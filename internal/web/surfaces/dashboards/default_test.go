package dashboards_test

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	opnview "github.com/NaejEL/opnview"
	"github.com/NaejEL/opnview/internal/web/surfaces/dashboards"
	"github.com/NaejEL/opnview/internal/web/webtest"
)

// The default dashboard (F7-28, F7-AC28): offered once, on the first ask after a reader signed in,
// with origin default; never offered again, even after every dashboard is deleted; structurally
// valid; carrying no reference that does not travel; and drawn by the widget API with no unknown type
// and no invalid parameter.

// TestTheDefaultDashboardIsOfferedOnceAndNeverAgain.
func TestTheDefaultDashboardIsOfferedOnceAndNeverAgain(t *testing.T) {
	t.Parallel()
	harness, token := signedIn(t)
	first := decoded(t, answered(t, harness.PostJSON(dashboards.PathDashboardDefault, `{}`, token, ""), http.StatusOK))
	if first["created"] != true {
		t.Fatalf("the first offer answered %v", first)
	}
	listing := decoded(t, answered(t, harness.Get(dashboards.PathDashboards), http.StatusOK))
	listed, _ := listing["dashboards"].([]any)
	if len(listed) != 1 {
		t.Fatalf("after the offer %d dashboards are listed", len(listed))
	}
	stored := listed[0].(map[string]any)
	if stored["origin"] != "default" {
		t.Errorf("the default dashboard's origin is %v", stored["origin"])
	}
	if again := decoded(t, answered(t, harness.PostJSON(dashboards.PathDashboardDefault, `{}`, token, ""), http.StatusOK)); again["created"] != false {
		t.Errorf("a second offer answered %v", again)
	}
	id := strconv.FormatInt(int64(stored["id"].(float64)), 10)
	answered(t, harness.PostJSON("/api/dashboards/"+id+"/delete", `{}`, token, ""), http.StatusOK)
	if again := decoded(t, answered(t, harness.PostJSON(dashboards.PathDashboardDefault, `{}`, token, ""), http.StatusOK)); again["created"] != false {
		t.Errorf("an offer after every dashboard was deleted answered %v", again)
	}
	if after := table(t, harness); after != "" {
		t.Errorf("the default dashboard came back: %.200s", after)
	}
	// A write: without the form token it is refused and stores nothing.
	other, _ := signedIn(t)
	answered(t, other.PostJSON(dashboards.PathDashboardDefault, `{}`, "", ""), http.StatusForbidden)
	if stored := table(t, other); stored != "" {
		t.Errorf("an offer without the form token stored %.200s", stored)
	}
}

// TestTheDefaultDashboardIsValidPortableAndAnsweredByEveryType.
func TestTheDefaultDashboardIsValidPortableAndAnsweredByEveryType(t *testing.T) {
	t.Parallel()
	document, err := dashboards.DefaultDashboard()
	if err != nil {
		t.Fatal(err)
	}
	var model struct {
		Canvases []struct {
			ID      string `json:"id"`
			Period  string `json:"period"`
			Widgets []struct {
				ID         string         `json:"id"`
				Type       string         `json:"type"`
				Parameters map[string]any `json:"parameters"`
			} `json:"widgets"`
		} `json:"canvases"`
	}
	if err := json.Unmarshal([]byte(document), &model); err != nil {
		t.Fatal(err)
	}
	// No reference that names this installation's own things.
	var walk func(value any)
	walk = func(value any) {
		switch typed := value.(type) {
		case map[string]any:
			if kind, ok := typed["kind"].(string); ok {
				switch kind {
				case "interface", "client", "rule", "owner":
					t.Errorf("the default dashboard carries a %s reference: %v", kind, typed)
				}
			}
			for _, item := range typed {
				walk(item)
			}
		case []any:
			for _, item := range typed {
				walk(item)
			}
		}
	}
	var whole any
	if err := json.Unmarshal([]byte(document), &whole); err != nil {
		t.Fatal(err)
	}
	walk(whole)
	// Which types declare a period: their manifests, read from the plugins' own directories.
	periodic := map[string]bool{}
	manifests, err := fs.Glob(opnview.BuiltinPlugins(), "*/widgets/*/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, manifest := range manifests {
		raw, err := fs.ReadFile(opnview.BuiltinPlugins(), manifest)
		if err != nil {
			t.Fatal(err)
		}
		var declared struct {
			Type       string `json:"type"`
			Parameters []struct {
				Key string `json:"key"`
			} `json:"parameters"`
		}
		if err := json.Unmarshal(raw, &declared); err != nil {
			t.Fatal(err)
		}
		for _, parameter := range declared.Parameters {
			periodic[declared.Type] = periodic[declared.Type] || parameter.Key == "period"
		}
	}
	harness, _ := signedIn(t)
	asked := 0
	for _, canvas := range model.Canvases {
		for _, widget := range canvas.Widgets {
			parameters := map[string]any{}
			for key, value := range widget.Parameters {
				parameters[key] = value
			}
			if _, own := parameters["period"]; !own && periodic[widget.Type] && canvas.Period != "" {
				parameters["period"] = canvas.Period
			}
			encoded, _ := json.Marshal(parameters)
			status, body := webtest.StatusAndBody(t, harness.Get("/api/widget/"+widget.Type+"?parameters="+url.QueryEscape(string(encoded))))
			if status != http.StatusOK {
				t.Errorf("%s/%s answered %d: %.300s", canvas.ID, widget.ID, status, body)
				continue
			}
			var answer struct {
				Condition         string         `json:"condition"`
				InvalidParameters []any          `json:"invalid_parameters"`
				UnknownParameters map[string]any `json:"unknown_parameters"`
				MissingParameters []string       `json:"missing_parameters"`
			}
			if err := json.Unmarshal([]byte(body), &answer); err != nil {
				t.Fatal(err)
			}
			if len(answer.InvalidParameters) > 0 || len(answer.UnknownParameters) > 0 || len(answer.MissingParameters) > 0 {
				t.Errorf("%s/%s: invalid %v, unknown %v, missing %v", canvas.ID, widget.ID, answer.InvalidParameters,
					answer.UnknownParameters, answer.MissingParameters)
			}
			if answer.Condition == "unknown_widget_type" || strings.TrimSpace(answer.Condition) == "" {
				t.Errorf("%s/%s answered the condition %q", canvas.ID, widget.ID, answer.Condition)
			}
			asked++
		}
	}
	if asked < 15 {
		t.Fatalf("only %d widgets of the default dashboard were asked", asked)
	}
}
