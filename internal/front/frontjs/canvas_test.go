package frontjs_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/NaejEL/opnview/internal/front/frontjs"
)

// The canvas (F7-17 to F7-27), driven in the interpreter against a fake server: its tabs, its grid,
// its period and scope and what they make the requests carry, its cards' degraded panels, its
// details and their pivots, and its refresh.
//
// FIXTURE PROVENANCE. The answers are the widget API's recorded answers (answers_test.go); the
// dashboards and references below are invented: RFC 2606 names, documentation-range values.

// canvasFixture is a fake server holding dashboards and answering the widget API.
type canvasFixture struct {
	*fakeServer
	mutex      sync.Mutex
	documents  map[int]string
	revisions  map[int]int
	answers    map[string]func(parameters map[string]any) frontjsAnswer
	extraRows  []map[string]any
	references map[string]any
}

// newCanvasFixture serves dashboards, keyed by id in order, each a document's JSON.
func newCanvasFixture(t *testing.T, documents ...string) *canvasFixture {
	t.Helper()
	fixture := &canvasFixture{fakeServer: newFakeServer(t, true, true), documents: map[int]string{},
		revisions: map[int]int{}, answers: map[string]func(map[string]any) frontjsAnswer{}, references: map[string]any{}}
	for index, document := range documents {
		fixture.documents[index+1] = document
		fixture.revisions[index+1] = 1
	}
	fixture.on("POST /api/dashboards/default", func(frontjsRequest) frontjsAnswer { return ok(map[string]any{"created": false}) })
	fixture.on("GET /api/dashboards", func(frontjsRequest) frontjsAnswer {
		fixture.mutex.Lock()
		defer fixture.mutex.Unlock()
		listed := []any{}
		for id := 1; id <= len(fixture.documents); id++ {
			var document struct {
				Title    string `json:"title"`
				Canvases []struct {
					ID    string `json:"id"`
					Title string `json:"title"`
				} `json:"canvases"`
			}
			_ = json.Unmarshal([]byte(fixture.documents[id]), &document)
			canvases := []any{}
			for _, canvas := range document.Canvases {
				canvases = append(canvases, map[string]any{"id": canvas.ID, "title": canvas.Title})
			}
			listed = append(listed, map[string]any{"id": id, "position": id, "title": document.Title,
				"revision": fixture.revisions[id], "origin": "created", "source_name": nil, "canvases": canvases})
		}
		return ok(map[string]any{"dashboards": listed, "format": map[string]int{"current": 1, "minimum": 1}})
	})
	for id := 1; id <= len(documents); id++ {
		dashboard := id
		fixture.on(fmt.Sprintf("GET /api/dashboards/%d", dashboard), func(frontjsRequest) frontjsAnswer {
			fixture.mutex.Lock()
			defer fixture.mutex.Unlock()
			// The document is answered as the text it was stored with, as the server splices it.
			return ok(map[string]any{"id": dashboard, "revision": fixture.revisions[dashboard], "origin": "created",
				"source_name": nil, "document": json.RawMessage(fixture.documents[dashboard])})
		})
		fixture.on(fmt.Sprintf("POST /api/dashboards/%d", dashboard), func(request frontjsRequest) frontjsAnswer {
			fixture.mutex.Lock()
			defer fixture.mutex.Unlock()
			var body struct {
				Revision int             `json:"revision"`
				Document json.RawMessage `json:"document"`
			}
			_ = json.Unmarshal([]byte(request.Body), &body)
			if body.Revision != fixture.revisions[dashboard] {
				return frontjsAnswer{Status: 409, Body: map[string]any{"condition": "revision_conflict", "revision": fixture.revisions[dashboard]}}
			}
			fixture.revisions[dashboard]++
			fixture.documents[dashboard] = string(body.Document)
			return ok(map[string]any{"id": dashboard, "revision": fixture.revisions[dashboard], "origin": "created"})
		})
	}
	fixture.on("GET /api/widgets", func(frontjsRequest) frontjsAnswer {
		rows := []any{}
		for _, widget := range builtinWidgets(t) {
			var row map[string]any
			_ = json.Unmarshal([]byte(rowOf(t, widget)), &row)
			row["title_key"] = "plugin." + widget.Type + ".widget." + widget.Type + ".title"
			row["renderer_url"] = "/front/widgets/" + widget.Type + "/renderer.js"
			rows = append(rows, row)
		}
		for _, row := range fixture.extraRows {
			rows = append(rows, row)
		}
		return ok(map[string]any{"widgets": rows, "shapes": []any{}, "refused": []any{}})
	})
	for _, kind := range []string{"interface", "client", "owner"} {
		referenceKind := kind
		fixture.on("GET /api/references/"+referenceKind, func(frontjsRequest) frontjsAnswer {
			fixture.mutex.Lock()
			defer fixture.mutex.Unlock()
			if listed, found := fixture.references[referenceKind]; found {
				return ok(listed)
			}
			return ok(map[string]any{"kind": referenceKind, "by": []string{}, "candidates": []any{}, "remainder": 0})
		})
	}
	return fixture
}

// answer answers one widget type, from the request's parameters.
func (f *canvasFixture) answer(widgetType string, handler func(parameters map[string]any) frontjsAnswer) {
	f.answers[widgetType] = handler
	f.on("GET /api/widget/"+widgetType, func(request frontjsRequest) frontjsAnswer {
		parameters := map[string]any{}
		if cut := strings.Index(request.Path, "?parameters="); cut >= 0 {
			text := request.Path[cut+len("?parameters="):]
			decoded, err := urlDecode(text)
			if err == nil {
				_ = json.Unmarshal([]byte(decoded), &parameters)
			}
		}
		return handler(parameters)
	})
}

// recordedFor answers a type with its reference recorded answer.
func (f *canvasFixture) recordedFor(t *testing.T, widgetType string) {
	raw := firstDrawable(t, widgetType).Raw
	f.answer(widgetType, func(map[string]any) frontjsAnswer {
		var body any
		_ = json.Unmarshal(raw, &body)
		return ok(body)
	})
}

// document is a dashboard's stored document.
func (f *canvasFixture) document(id int) string {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	return f.documents[id]
}

// canvasPage opens the front on a canvas path, with every renderer loaded and every timer run.
func (f *canvasFixture) canvasPage(t *testing.T, path string) *frontjs.Runtime {
	t.Helper()
	page := frontjs.New(t, frontjs.Options{Handler: f.handle})
	page.LoadRenderers()
	page.Start(path)
	page.Advance(0)
	return page
}

// widgetRequests counts the requests of one widget type so far.
func widgetRequests(page *frontjs.Runtime, widgetType string) int {
	count := 0
	for _, request := range page.Requests() {
		if strings.HasPrefix(request.Path, "/api/widget/"+widgetType+"?") || request.Path == "/api/widget/"+widgetType {
			count++
		}
	}
	return count
}

// urlDecode decodes a percent-encoded query value.
func urlDecode(text string) (string, error) {
	var out strings.Builder
	for index := 0; index < len(text); index++ {
		switch text[index] {
		case '%':
			if index+2 >= len(text) {
				return "", fmt.Errorf("truncated escape")
			}
			var value byte
			if _, err := fmt.Sscanf(text[index+1:index+3], "%02x", &value); err != nil {
				return "", err
			}
			out.WriteByte(value)
			index += 2
		case '+':
			out.WriteByte(' ')
		default:
			out.WriteByte(text[index])
		}
	}
	return out.String(), nil
}

const oneCanvas = `{"format_version": 1, "title": "Example", "canvases": [
  {"id": "main", "title": "Main", "period": "24h", "widgets": [
    {"id": "sites", "type": "top_sites", "placement": {"x": 0, "y": 0, "w": 7, "h": 8}},
    {"id": "address", "type": "public_address", "placement": {"x": 7, "y": 0, "w": 5, "h": 8}}
  ]},
  {"id": "second", "title": "Second", "widgets": [
    {"id": "freshness", "type": "aggregate_freshness", "placement": {"x": 0, "y": 0, "w": 6, "h": 6}}
  ]}
]}`

// TestTheTabsAreOnePerCanvasAndGroupedPerDashboard (F7-17).
func TestTheTabsAreOnePerCanvasAndGroupedPerDashboard(t *testing.T) {
	t.Parallel()
	fixture := newCanvasFixture(t, oneCanvas, `{"format_version": 1, "title": "Other", "canvases": [{"id": "x", "title": "X", "widgets": []}]}`)
	fixture.recordedFor(t, "top_sites")
	fixture.recordedFor(t, "public_address")
	page := fixture.canvasPage(t, "/")
	if got := page.Count("a.tab"); got != 3 {
		t.Fatalf("%d tabs for three canvases:\n%s", got, page.Outline())
	}
	if got := page.Count("div.tab-group"); got != 2 {
		t.Errorf("%d tab groups for two dashboards", got)
	}
	if got := page.Attribute(`a.tab[aria-selected="true"]`, "href"); got != "/canvas/1/main" {
		t.Errorf("the first canvas's tab is not selected at the root: %s", got)
	}
	if page.Count("section.card") != 2 {
		t.Errorf("the first canvas draws %d cards", page.Count("section.card"))
	}
}

// TestTheGridDerivesItsColumnsAndNeverWritesThePlacement (F7-18).
func TestTheGridDerivesItsColumnsAndNeverWritesThePlacement(t *testing.T) {
	t.Parallel()
	page := frontjs.New(t, frontjs.Options{})
	got := page.Run(`(function () {
  var widgets = [
    { id: "a", placement: { x: 0, y: 0, w: 7, h: 4 } },
    { id: "b", placement: { x: 7, y: 0, w: 5, h: 2 } },
    { id: "c", placement: { x: 7, y: 9, w: 5, h: 3 } },
    { id: "d", placement: { x: 0, y: 30, w: 12, h: 2 } }
  ];
  var before = JSON.stringify(widgets);
  var out = {
    twelve: opnview.grid.layout(widgets, 12),
    eight: opnview.grid.layout(widgets, 8),
    one: opnview.grid.layout(widgets, 1),
    columns: [opnview.grid.columnsFor(1600), opnview.grid.columnsFor(1200), opnview.grid.columnsFor(600)],
    unchanged: JSON.stringify(widgets) === before
  };
  return JSON.stringify(out);
})()`).String()
	type cell struct{ Index, Column, Span, Row, Rows int }
	var layout struct {
		Twelve, Eight, One []cell
		Columns            []int
		Unchanged          bool
	}
	if err := json.Unmarshal([]byte(got), &layout); err != nil {
		t.Fatal(err)
	}
	if !layout.Unchanged {
		t.Error("laying the grid out changed the document's placements")
	}
	if !reflect.DeepEqual(layout.Columns, []int{12, 8, 1}) {
		t.Errorf("the column counts at 1600, 1200 and 600 px are %v", layout.Columns)
	}
	byIndex := map[int]cell{}
	for _, c := range layout.Twelve {
		byIndex[c.Index] = c
	}
	// c, authored at y 9 under b, floats up to the row b leaves free; d floats up under a and c.
	if byIndex[2].Row != 3 || byIndex[2].Column != 8 {
		t.Errorf("on twelve columns c sits at %+v, not floated up under b", byIndex[2])
	}
	if byIndex[3].Row != 6 {
		t.Errorf("on twelve columns d sits at row %d, not floated up to row 6", byIndex[3].Row)
	}
	for _, c := range layout.Eight {
		if c.Span < 4 && c.Index != 3 {
			t.Errorf("on eight columns a card spans %d, below a half", c.Span)
		}
	}
	for _, c := range layout.One {
		if c.Span != 1 || c.Column != 1 {
			t.Errorf("on one column a card sits at %+v", c)
		}
	}
}

// TestTheRequestCarriesInheritedValuesOnlyWhereTheManifestDeclaresThem (F7-AC26, the request
// builder).
func TestTheRequestCarriesInheritedValuesOnlyWhereTheManifestDeclaresThem(t *testing.T) {
	t.Parallel()
	page := frontjs.New(t, frontjs.Options{})
	rows := map[string]string{}
	for _, widget := range builtinWidgets(t) {
		rows[widget.Type] = rowOf(t, widget)
	}
	setJSON(t, page, "__rows", "{"+joinRows(rows)+"}")
	got := page.Run(`(function () {
  var iface = { kind: "interface", by: "identifier", value: "opt1", label: "Example LAN" };
  var one = { kind: "client", by: "mac", value: "02:00:5e:10:00:01", label: "laptop.example" };
  var two = { kind: "client", by: "hostname", value: "printer.example", label: "printer.example" };
  var owner = { kind: "owner", by: "display_name", value: "Noé Example", label: "Noé Example" };
  var canvas = { id: "c", title: "C", period: "7d", scope: { interfaces: [iface], clients: [one], owners: [owner] }, widgets: [] };
  function ask(type, parameters, on) {
    return opnview.canvasModel.request(__rows[type], { id: type, type: type, parameters: parameters || {} }, on || canvas);
  }
  return JSON.stringify({
    sites: ask("top_sites"),
    sitesOwn: ask("top_sites", { period: "1h", interfaces: [{ kind: "interface", by: "device", value: "igb1" }] }),
    address: ask("public_address"),
    detail: ask("client_traffic_detail"),
    detailTwo: ask("client_traffic_detail", {}, { id: "c", title: "C", scope: { clients: [one, two] }, widgets: [] }),
    tree: ask("connection_tree")
  });
})()`).String()
	type built struct {
		Parameters map[string]any
		Notes      struct {
			OwnRange  bool
			OwnScope  []string
			NotScoped []string
			Period    *string
		}
	}
	var requests map[string]built
	if err := json.Unmarshal([]byte(got), &requests); err != nil {
		t.Fatal(err)
	}
	sites := requests["sites"]
	if sites.Parameters["period"] != "7d" || sites.Notes.OwnRange {
		t.Errorf("top_sites does not inherit the canvas's period: %v %+v", sites.Parameters, sites.Notes)
	}
	if fmt.Sprint(sites.Parameters["interfaces"]) == "<nil>" || fmt.Sprint(sites.Parameters["clients"]) == "<nil>" {
		t.Errorf("top_sites does not inherit the interface and client scope: %v", sites.Parameters)
	}
	if _, found := sites.Parameters["owners"]; found || !contains(sites.Notes.NotScoped, "owner") {
		t.Errorf("top_sites, declaring no owner parameter, is scoped by owner or not said so: %v %+v", sites.Parameters, sites.Notes)
	}
	own := requests["sitesOwn"]
	if own.Parameters["period"] != "1h" || !own.Notes.OwnRange || !contains(own.Notes.OwnScope, "interface") {
		t.Errorf("a widget's own period and interfaces are not kept and said: %v %+v", own.Parameters, own.Notes)
	}
	address := requests["address"]
	for _, key := range []string{"period", "interfaces", "clients", "owners"} {
		if _, found := address.Parameters[key]; found {
			t.Errorf("public_address, declaring no %s, is asked with it", key)
		}
	}
	if len(address.Notes.NotScoped) != 3 {
		t.Errorf("public_address does not say it is not scoped by the three kinds: %+v", address.Notes)
	}
	if client, _ := requests["detail"].Parameters["client"].(map[string]any); client == nil || client["value"] != "02:00:5e:10:00:01" {
		t.Errorf("client_traffic_detail does not take the canvas's one client: %v", requests["detail"].Parameters)
	}
	if _, found := requests["detailTwo"].Parameters["client"]; found || !contains(requests["detailTwo"].Notes.NotScoped, "client") {
		t.Errorf("client_traffic_detail takes one of two scoped clients: %+v", requests["detailTwo"])
	}
	tree := requests["tree"]
	if fmt.Sprint(tree.Parameters["owners"]) == "<nil>" {
		t.Errorf("connection_tree does not inherit the owner scope: %v", tree.Parameters)
	}
}

func joinRows(rows map[string]string) string {
	parts := make([]string, 0, len(rows))
	for key, row := range rows {
		parts = append(parts, fmt.Sprintf("%q: %s", key, row))
	}
	return strings.Join(parts, ",")
}

func contains(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}

// TestTheCardsHeaderSaysOwnRangeAndNotScoped (F7-AC26, F7-20): the chips of the request notes.
func TestTheCardsHeaderSaysOwnRangeAndNotScoped(t *testing.T) {
	t.Parallel()
	fixture := newCanvasFixture(t, `{"format_version": 1, "canvases": [{"id": "main", "title": "Main", "period": "24h",
  "scope": {"owners": [{"kind": "owner", "by": "display_name", "value": "Noé Example"}]}, "widgets": [
    {"id": "sites", "type": "top_sites", "placement": {"x": 0, "y": 0, "w": 7, "h": 8}, "parameters": {"period": "7d"}}
  ]}]}`)
	fixture.recordedFor(t, "top_sites")
	page := fixture.canvasPage(t, "/canvas/1/main")
	if page.Count(`[data-chip="own_range"]`) != 1 {
		t.Errorf("a widget with its own period says nothing:\n%s", page.Outline())
	}
	if page.Count(`[data-chip="not_scoped"]`) != 1 {
		t.Errorf("a widget the owner scope cannot reach does not say not scoped")
	}
	if page.Text("h2.card-title") != englishCatalogue(t)["plugin.top_sites.widget.top_sites.title"] {
		t.Errorf("the card's title is %q, not the catalogue's", page.Text("h2.card-title"))
	}
}

// TestSettingThePeriodAndTheScopeWritesTheCanvasAndUndoRestoresIt (F7-AC26, F7-AC25's undo).
func TestSettingThePeriodAndTheScopeWritesTheCanvasAndUndoRestoresIt(t *testing.T) {
	t.Parallel()
	fixture := newCanvasFixture(t, oneCanvas)
	fixture.recordedFor(t, "top_sites")
	fixture.recordedFor(t, "public_address")
	fixture.references["interface"] = map[string]any{"kind": "interface", "by": []string{"identifier", "device", "user_label"},
		"candidates": []any{map[string]any{"id": 3, "label": "Example LAN", "referenceable": true,
			"keys": map[string]any{"identifier": "opt1", "device": "igb1", "user_label": "Example LAN"}}}, "remainder": 0}
	page := fixture.canvasPage(t, "/canvas/1/main")
	original := fixture.document(1)
	page.Choose("#period-select", "7d")
	page.Advance(0)
	var saved struct {
		Canvases []map[string]any `json:"canvases"`
	}
	if err := json.Unmarshal([]byte(fixture.document(1)), &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Canvases[0]["period"] != "7d" {
		t.Fatalf("the range did not write the canvas's period: %v", saved.Canvases[0])
	}
	page.Choose("#scope-interface", "0")
	page.Advance(0)
	if err := json.Unmarshal([]byte(fixture.document(1)), &saved); err != nil {
		t.Fatal(err)
	}
	scope, _ := saved.Canvases[0]["scope"].(map[string]any)
	if scope == nil || !strings.Contains(fmt.Sprint(scope["interfaces"]), "opt1") {
		t.Fatalf("the scope did not write the canvas's scope: %v", saved.Canvases[0])
	}
	if last := lastWidgetRequest(page, "top_sites"); !strings.Contains(last, "opt1") || !strings.Contains(last, "7d") {
		t.Errorf("top_sites was not asked again with the canvas's period and scope: %s", last)
	}
	page.Click("#canvas-undo")
	page.Advance(0)
	page.Click("#canvas-undo")
	page.Advance(0)
	if compact(t, fixture.document(1)) != compact(t, original) {
		t.Errorf("two undos do not restore the document, member for member:\n%s\n%s", fixture.document(1), original)
	}
}

// lastWidgetRequest is the decoded path of the last request of a widget type.
func lastWidgetRequest(page *frontjs.Runtime, widgetType string) string {
	last := ""
	for _, request := range page.Requests() {
		if strings.HasPrefix(request.Path, "/api/widget/"+widgetType+"?") {
			last, _ = urlDecode(request.Path)
		}
	}
	return last
}

// TestOpeningADetailWritesNothingAndItsScopePivotIsUndoable (F7-AC25, the half without a browser).
func TestOpeningADetailWritesNothingAndItsScopePivotIsUndoable(t *testing.T) {
	t.Parallel()
	fixture := newCanvasFixture(t, `{"format_version": 1, "canvases": [{"id": "main", "title": "Main", "widgets": [
    {"id": "clients", "type": "client_volume_ranking", "placement": {"x": 0, "y": 0, "w": 7, "h": 8}}
  ]}]}`)
	fixture.recordedFor(t, "client_volume_ranking")
	page := fixture.canvasPage(t, "/canvas/1/main")
	writes := len(page.Writes())
	original := fixture.document(1)
	// The first client whose identity the format can name.
	page.Run(`(function () {
  var picked = document.querySelectorAll("[data-subject]").filter(function (el) {
    return el.getAttribute("data-subject").indexOf("client:") === 0; });
  window.__picked = picked;
})()`)
	if page.Run(`__picked.length`).ToInteger() == 0 {
		t.Fatalf("no client can be picked:\n%s", page.Outline())
	}
	opened := false
	for index := int64(0); index < page.Run(`__picked.length`).ToInteger(); index++ {
		page.Run(fmt.Sprintf(`__picked[%d].dispatchEvent(__makeEvent("click"));`, index))
		if page.Count("#detail-pop") != 1 {
			t.Fatalf("picking a client opened no detail:\n%s", page.Outline())
		}
		if page.Count(`button[data-pivot="scope"]`) == 1 {
			opened = true
			break
		}
		page.Click(`button[data-detail-close="true"]`)
	}
	if len(page.Writes()) != writes {
		t.Errorf("opening and closing details wrote %v", page.Writes()[writes:])
	}
	if !opened {
		t.Fatal("no picked client offers to scope the canvas")
	}
	if page.Count(`button[data-pivot="open"]`) == 0 {
		t.Error("a client's detail offers no pivot to a related subject")
	}
	page.Click(`button[data-pivot="scope"]`)
	page.Advance(0)
	if fixture.document(1) == original || !strings.Contains(fixture.document(1), `"clients"`) {
		t.Fatalf("scoping the canvas to the client wrote %s", fixture.document(1))
	}
	page.Click("#canvas-undo")
	page.Advance(0)
	if compact(t, fixture.document(1)) != compact(t, original) {
		t.Errorf("undo does not restore the document, member for member")
	}
	// "Add a widget about it" adds the widget the manifests choose for a client -- the one widget
	// whose one reference parameter is a single client -- with that client, and Undo removes it.
	page.Run(`__picked[0].dispatchEvent(__makeEvent("click"));`)
	for index := int64(0); index < page.Run(`__picked.length`).ToInteger() && page.Count(`button[data-pivot="add"]`) == 0; index++ {
		page.Run(fmt.Sprintf(`__picked = document.querySelectorAll("[data-subject]").filter(function (el) {
  return el.getAttribute("data-subject").indexOf("client:") === 0; }); __picked[%d].dispatchEvent(__makeEvent("click"));`, index))
	}
	if page.Count(`button[data-pivot="add"]`) != 1 {
		t.Fatalf("a client that can be named offers no widget about it:\n%s", page.Outline())
	}
	page.Click(`button[data-pivot="add"]`)
	page.Advance(0)
	if added := fixture.document(1); !strings.Contains(added, `"type":"client_traffic_detail"`) ||
		!strings.Contains(added, `"client":{"kind":"client"`) {
		t.Errorf("adding a widget about the client wrote %s", added)
	}
	page.Click("#canvas-undo")
	page.Advance(0)
	if compact(t, fixture.document(1)) != compact(t, original) {
		t.Errorf("undoing the added widget does not restore the document")
	}
	// A subject opened from the related list of a detail is not said to be missing from the answer.
	page.Run(`__picked = document.querySelectorAll("[data-subject]"); __picked[0].dispatchEvent(__makeEvent("click"));`)
	if page.Count(`button[data-pivot="open"]`) > 0 {
		page.Click(`button[data-pivot="open"]`)
		if page.Count("#detail-pop") != 1 || page.Count("#detail-pop .badge-gap") != 0 {
			t.Errorf("a related subject opens as missing from the latest answer:\n%s", page.Outline())
		}
	}
	// A client the format cannot name says why it offers no scope, where the pivot would be.
	reason := page.Run(`(function () {
  var host = document.createElement("div");
  opnview.view.render(host, [opnview.detail.render({ subject: { kind: "client", key: "9", title: "192.0.2.9",
    facts: [], related: [], reference: null }, canScope: false, canAdd: false, onClose: function () {} })]);
  var note = host.querySelector("[data-pivot]");
  return note ? note.getAttribute("data-pivot") + "|" + note.textContent : "";
})()`).String()
	if reason != "not_scopable|"+englishCatalogue(t)["detail.not_scopable.client"] {
		t.Errorf("a client known by its address only gives the reason %q", reason)
	}
	// The detail survives a refresh.
	page.Run(`__picked = document.querySelectorAll("[data-subject]"); __picked[0].dispatchEvent(__makeEvent("click"));`)
	page.Advance(61000)
	if page.Count("#detail-pop") != 1 {
		t.Error("a refresh closed the open detail")
	}
}

// TestEveryConditionIsADesignedPanel (F7-AC20): each condition of F7-22 keeps the card and its
// title, names the condition through its own key, links to the Sources view, and draws no zero and
// nothing in the error hue.
func TestEveryConditionIsADesignedPanel(t *testing.T) {
	t.Parallel()
	conditions := []string{"not_yet_probed", "unavailable", "present_but_disabled", "reachable_no_rows",
		"unresolved_reference", "missing_required_parameter", "withheld_by_aggregate_mode", "no_owner_exists",
		"unknown_widget_type", "script_failed", "read_failed"}
	widgets := []string{}
	for index := range conditions {
		widgets = append(widgets, fmt.Sprintf(`{"id": "w%d", "type": "top_sites", "placement": {"x": 0, "y": %d, "w": 6, "h": 4},
      "parameters": {"limit": %d}}`, index, index*4, index+1))
	}
	widgets = append(widgets, `{"id": "failing", "type": "failing_example", "placement": {"x": 0, "y": 99, "w": 6, "h": 4}}`)
	fixture := newCanvasFixture(t, `{"format_version": 1, "canvases": [{"id": "main", "title": "Main", "widgets": [`+
		strings.Join(widgets, ",")+`]}]}`)
	fixture.extraRows = append(fixture.extraRows, map[string]any{"type": "failing_example", "shape": "table", "backend": true,
		"parameters": []any{}, "title_key": "plugin.top_sites.widget.top_sites.title"})
	base := firstDrawable(t, "top_sites").Raw
	fixture.answer("top_sites", func(parameters map[string]any) frontjsAnswer {
		index := int(parameters["limit"].(float64)) - 1
		condition := conditions[index]
		switch condition {
		case "unknown_widget_type":
			return frontjsAnswer{Status: 404, Body: map[string]any{"condition": condition, "type": "top_sites"}}
		case "script_failed", "read_failed":
			return frontjsAnswer{Status: 500, Body: map[string]any{"condition": condition}}
		}
		var body map[string]any
		_ = json.Unmarshal(base, &body)
		body["condition"] = condition
		body["data"] = nil
		if condition == "unresolved_reference" {
			body["unresolved_references"] = []any{map[string]any{"path": "interfaces[0]", "kind": "interface", "by": "identifier",
				"value": "opt7", "label": "Workshop", "reason": "no_match"}}
		}
		if condition == "missing_required_parameter" {
			body["missing_parameters"] = []any{"client"}
		}
		return ok(body)
	})
	fixture.answer("failing_example", func(map[string]any) frontjsAnswer {
		var body any
		_ = json.Unmarshal(base, &body)
		return ok(body)
	})
	page := fixture.canvasPage(t, "/canvas/1/main")
	page.RunAt("/front/widgets/failing_example/renderer.js",
		`opnview.widgets.register({ type: "failing_example", render: function () { throw new Error("broken on purpose"); } });`)
	page.Go("/canvas/1/main")
	page.Advance(0)
	english := englishCatalogue(t)
	title := english["plugin.top_sites.widget.top_sites.title"]
	for index, condition := range conditions {
		card := fmt.Sprintf(`section[data-widget-card="w%d"]`, index)
		if page.Count(card) != 1 {
			t.Errorf("%s: the card is gone", condition)
			continue
		}
		if got := page.Text(card + " h2.card-title"); got != title {
			t.Errorf("%s: the card's title is %q", condition, got)
		}
		panelSelector := card + ` [data-condition="` + condition + `"]`
		if condition == "renderer_failed" {
			panelSelector = card + ` [data-condition]`
		}
		if page.Count(panelSelector) != 1 {
			t.Errorf("%s: no panel names the condition:\n%s", condition, page.Run(`document.querySelector('`+card+`').textContent`))
			continue
		}
		badge := page.Text(panelSelector + " .state-badge")
		want := english["condition."+condition]
		if condition == "renderer_failed" {
			want = english["condition.renderer_failed"]
		}
		if badge != want {
			t.Errorf("%s: the panel reads %q, not %q", condition, badge, want)
		}
		if page.Count(panelSelector+` a[href="/sources"]`) != 1 {
			t.Errorf("%s: the panel does not link to the Sources view", condition)
		}
		zero := page.Run(`(function () { var found = false;
  (function walk(node) { if (node.nodeType === 3) { if (node.nodeValue.trim() === "0") { found = true; } return; }
    if ((node.getAttribute("class") || "").indexOf("error") >= 0) { found = true; }
    node.childNodes.forEach(walk); })(document.querySelector('` + panelSelector + `'));
  return found; })()`).ToBoolean()
		if zero {
			t.Errorf("%s: the panel draws a zero or the error hue", condition)
		}
	}
	if page.Count(`section[data-widget-card="failing"] [data-condition="renderer_failed"]`) != 1 {
		t.Errorf("a renderer that throws does not draw the renderer-failure panel")
	}
}

// TestTheSchedulerRefreshesOnEventsOnlyWhileShown (F7-AC27, read with D21 as amended): while the
// stream is live, neither a windowed widget nor one carrying a live figure is asked by time -- the
// live widget at its reported cadence no more than the windowed one every minute -- but each is
// asked on the event that concerns it; nothing while the document is hidden, and nothing for a
// canvas not shown.
func TestTheSchedulerRefreshesOnEventsOnlyWhileShown(t *testing.T) {
	t.Parallel()
	page := liveCanvas(t)
	before := counts(page)
	page.Advance(60000)
	page.Advance(0)
	if asked := askedSince(page, before); len(asked) != 0 {
		t.Errorf("live, a minute alone asked %v", asked)
	}
	page.Emit("live_sample", `{"type": "live_sample", "at": 1790001810, "kind": "measurement_sample",
	  "provider_key": "top", "sampled_at": 1790001809}`)
	page.Advance(0)
	if asked := askedSince(page, before); fmt.Sprint(asked) != fmt.Sprint(map[string]int{"traffic_composition": 1}) {
		t.Errorf("a live sample asked %v", asked)
	}
	// Hidden: nothing is sent.
	page.Run(`document.visibilityState = "hidden";`)
	page.Advance(debounceMS(page))
	sent := len(page.Requests())
	page.Emit("resync", `{"type": "resync", "at": 1790001820}`)
	page.Advance(120000)
	for _, request := range page.Requests()[sent:] {
		if strings.HasPrefix(request.Path, "/api/widget/") {
			t.Errorf("a hidden page asked %s", request.Path)
		}
	}
	page.Run(`document.visibilityState = "visible";`)
	// Another canvas: the first one's widgets are asked no more.
	page.Go("/canvas/1/other")
	page.Advance(0)
	sites := widgetRequests(page, "top_sites")
	page.Emit("resync", `{"type": "resync", "at": 1790001830}`)
	page.Advance(180000)
	if got := widgetRequests(page, "top_sites"); got != sites {
		t.Errorf("a canvas not shown was refreshed: %d requests after %d", got, sites)
	}
	if widgetRequests(page, "public_address") < 2 {
		t.Errorf("the canvas shown was not refreshed")
	}
}

// TestAnAnswerThatChangedIsDrawnAtTheNextRefresh: the reconciler's short-cut -- a subtree whose view
// node is the very object it last matched is not walked -- never keeps a card on its previous answer.
// The second answer renames the first site; once the refresh has asked again, the card shows the
// new name and no longer the old one.
func TestAnAnswerThatChangedIsDrawnAtTheNextRefresh(t *testing.T) {
	t.Parallel()
	fixture := newCanvasFixture(t, `{"format_version": 1, "canvases": [
  {"id": "main", "title": "Main", "widgets": [
    {"id": "sites", "type": "top_sites", "placement": {"x": 0, "y": 0, "w": 12, "h": 8}}
  ]}]}`)
	raw := firstDrawable(t, "top_sites").Raw
	const before, after = "api.example.org", "renamed.example.org"
	if !bytes.Contains(raw, []byte(before)) {
		t.Fatalf("the recorded top_sites answer no longer names %s", before)
	}
	var mutex sync.Mutex
	asked := 0
	fixture.answer("top_sites", func(map[string]any) frontjsAnswer {
		mutex.Lock()
		asked++
		changed := asked > 1
		mutex.Unlock()
		text := raw
		if changed {
			text = bytes.ReplaceAll(raw, []byte(before), []byte(after))
		}
		var body any
		_ = json.Unmarshal(text, &body)
		return ok(body)
	})
	page := fixture.canvasPage(t, "/canvas/1/main")
	card := `section[data-widget-card="sites"] [data-widget-body="sites"]`
	if text := page.Text(card); !strings.Contains(text, before) {
		t.Fatalf("the first answer is not drawn: %.300s", text)
	}
	page.Emit("hello", hello)
	page.Advance(debounceMS(page))
	page.Emit("aggregate_refresh", `{"type": "aggregate_refresh", "at": 1790001810, "periods": ["hour"],
	  "families": ["volume"], "ranges": [{"period": "hour", "from": 1790000000, "to": 1790001800}]}`)
	page.Advance(0)
	if widgetRequests(page, "top_sites") < 2 {
		t.Fatalf("the refresh did not ask the widget again")
	}
	text := page.Text(card)
	if !strings.Contains(text, after) || strings.Contains(text, before) {
		t.Errorf("the changed answer is not drawn: %.300s", text)
	}
}

// TestEveryRendererDrawnInAPseudoLanguageShowsNoLiteral (F7-AC5 for the renderers): a canvas holding
// every built-in widget, each drawing its recorded answer, in a pseudo-language that wraps every
// catalogue string in markers, shows no text, aria-label, title or placeholder that is neither
// wrapped nor a value of an answer or of a formatter.
func TestEveryRendererDrawnInAPseudoLanguageShowsNoLiteral(t *testing.T) {
	t.Parallel()
	widgets := []string{}
	for index, widget := range builtinWidgets(t) {
		widgets = append(widgets, fmt.Sprintf(`{"id": "w%d", "type": %q, "placement": {"x": 0, "y": %d, "w": 12, "h": 8}}`,
			index, widget.Type, index*8))
	}
	fixture := newCanvasFixture(t, `{"format_version": 1, "title": "Example", "canvases": [{"id": "main", "title": "Main",
  "widgets": [`+strings.Join(widgets, ",")+`]}]}`)
	data := map[string]bool{"Example": true, "Main": true}
	for _, widget := range builtinWidgets(t) {
		answer := firstDrawable(t, widget.Type)
		var decoded any
		_ = json.Unmarshal(answer.Raw, &decoded)
		leaves(decoded, data)
		fixture.recordedFor(t, widget.Type)
	}
	var mutex sync.Mutex
	recording := func(request frontjsRequest) frontjsAnswer {
		answer := fixture.handle(request)
		raw, _ := json.Marshal(answer.Body)
		var decoded any
		_ = json.Unmarshal(raw, &decoded)
		mutex.Lock()
		leaves(decoded, data)
		mutex.Unlock()
		return answer
	}
	page := frontjs.New(t, frontjs.Options{Handler: recording})
	page.LoadRenderers()
	page.Run(`opnview.format.record(true); opnview.strings.setPseudo(true);`)
	page.Start("/canvas/1/main")
	page.Advance(0)
	if page.Count("section.card") != len(widgets) {
		t.Fatalf("%d cards drawn of %d", page.Count("section.card"), len(widgets))
	}
	if page.Count(`[data-condition="renderer_failed"]`) != 0 {
		t.Fatalf("a renderer failed in the pseudo-language:\n%s", page.Outline())
	}
	var formatted []string
	if err := json.Unmarshal([]byte(page.Run(`JSON.stringify(opnview.format.recorded())`).String()), &formatted); err != nil {
		t.Fatal(err)
	}
	for _, text := range formatted {
		data[text] = true
	}
	allowed, _ := json.Marshal(data)
	var found []string
	if err := json.Unmarshal([]byte(page.Run(pseudoWalk+"("+string(allowed)+")").String()), &found); err != nil {
		t.Fatal(err)
	}
	for _, literal := range found {
		t.Errorf("the canvas shows a string that is neither the catalogue's nor data: %s", literal)
	}
	if missing := page.Run(`JSON.stringify(opnview.strings.missing())`).String(); missing != "[]" {
		t.Errorf("the renderers asked for keys the catalogue does not hold: %s", missing)
	}
}

// compact is a document's text without its insignificant white space: the front holds a document as
// a model, so it sends back its members, in their order, and not the spacing it was first written
// with.
func compact(t *testing.T, text string) string {
	t.Helper()
	var out bytes.Buffer
	if err := json.Compact(&out, []byte(text)); err != nil {
		t.Fatalf("%v: %s", err, text)
	}
	return out.String()
}

// TestFocusingACardOnAnInterfaceAsksItForThatInterfaceAndWritesNothing: picking an interface in the
// connection tree and "only this, in this card" asks the tree again for that interface alone -- its
// outside side is then where that interface goes -- names the focus on the card, and writes nothing;
// "show all" asks for the whole answer again.
func TestFocusingACardOnAnInterfaceAsksItForThatInterfaceAndWritesNothing(t *testing.T) {
	t.Parallel()
	fixture := newCanvasFixture(t, `{"format_version": 1, "canvases": [{"id": "main", "title": "Main", "widgets": [
    {"id": "tree", "type": "connection_tree", "placement": {"x": 0, "y": 0, "w": 12, "h": 10}}
  ]}]}`)
	fixture.recordedFor(t, "connection_tree")
	page := fixture.canvasPage(t, "/canvas/1/main")
	original := fixture.document(1)
	writes := len(page.Writes())
	page.Run(`window.__picked = document.querySelectorAll("[data-subject]").filter(function (el) {
  return el.getAttribute("data-subject").indexOf("interface:") === 0; });`)
	if page.Run(`__picked.length`).ToInteger() == 0 {
		t.Fatalf("the tree offers no interface to pick:\n%s", page.Outline())
	}
	page.Run(`__picked[0].dispatchEvent(__makeEvent("click"));`)
	if page.Count(`button[data-pivot="focus"]`) != 1 {
		t.Fatalf("an interface of the tree offers no focus:\n%s", page.Outline())
	}
	page.Click(`button[data-pivot="focus"]`)
	page.Advance(0)
	if asked := lastWidgetRequest(page, "connection_tree"); !strings.Contains(asked, `"interfaces":[{"kind":"interface"`) {
		t.Errorf("the focused tree was asked %s", asked)
	}
	if page.Count(`section[data-widget-card="tree"] span[data-chip="focus"]`) != 1 {
		t.Errorf("the focused card does not say what it is focused on")
	}
	if fixture.document(1) != original || len(page.Writes()) != writes {
		t.Errorf("focusing a card wrote %v", page.Writes()[writes:])
	}
	page.Click(`button[data-focus-clear="tree"]`)
	page.Advance(0)
	if asked := lastWidgetRequest(page, "connection_tree"); strings.Contains(asked, `"interfaces":[{`) {
		t.Errorf("after show all the tree was asked %s", asked)
	}
	if page.Count(`span[data-chip="focus"]`) != 0 {
		t.Errorf("show all left the focus chip")
	}
}

// TestAMapZoomsAndResetsAndKeepsItsViewAcrossARefresh (the maintainer's review of 17 October 2026: the
// world map had no zoom): the zoom controls scale the basemap and move the marks without scaling them,
// the view survives a refresh, and the reset returns to the whole world. Nothing is written.
func TestAMapZoomsAndResetsAndKeepsItsViewAcrossARefresh(t *testing.T) {
	t.Parallel()
	fixture := newCanvasFixture(t, `{"format_version": 1, "canvases": [{"id": "main", "title": "Main", "widgets": [
    {"id": "map", "type": "passed_traffic_world_map", "placement": {"x": 0, "y": 0, "w": 8, "h": 8}}
  ]}]}`)
	fixture.recordedFor(t, "passed_traffic_world_map")
	page := fixture.canvasPage(t, "/canvas/1/main")
	writes := len(page.Writes())
	view := func() string { return page.Attribute(`[data-map-svg] .map-pan`, "transform") }
	if view() != "translate(0.00 0.00) scale(1.0000)" {
		t.Fatalf("the map starts at %q", view())
	}
	page.Click(`button[data-map-zoom="in"]`)
	zoomed := view()
	if !strings.Contains(zoomed, "scale(1.6000)") {
		t.Errorf("zooming in gives %q", zoomed)
	}
	if page.Count(`[data-map-svg] .map-mark-at`) > 0 && page.Attribute(`[data-map-svg] .map-mark-at circle`, "r") == "" {
		t.Error("a mark lost its radius")
	}
	page.Advance(61000)
	if view() != zoomed {
		t.Errorf("a refresh moved the view from %q to %q", zoomed, view())
	}
	page.Click(`button[data-map-zoom="reset"]`)
	if !strings.Contains(view(), "scale(1.0000)") {
		t.Errorf("the reset leaves %q", view())
	}
	if len(page.Writes()) != writes {
		t.Errorf("zooming the map wrote %v", page.Writes()[writes:])
	}
}
