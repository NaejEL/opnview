package frontjs_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/NaejEL/opnview/internal/front/frontjs"
)

// Lot 3: editing and portability (F7-30 to F7-40), driven in the interpreter against a fake server
// that keeps every dashboard as the bytes it was sent.
//
// FIXTURE PROVENANCE. The dashboards below are invented: RFC 2606 names, interface identifiers as
// OPNsense numbers them (opt1, opt3, opt7), labels nobody's network carries. The imported documents
// are the format's own worked example (docs/dashboard-format.md) and the step-3 mockup's DOC_IMPORTED
// (docs/mockups/canvas-mockup.html), read from those files at test time. The widget answers are the
// recorded ones (answers_test.go) with their references and keys judged here.

// editFixture serves dashboards that are created, saved, deleted, restored and reordered, each stored
// as the exact bytes of the document the front sent, and answered with the document written last, as
// internal/web/surfaces/dashboards writes it.
type editFixture struct {
	*canvasFixture
	lock       sync.Mutex
	order      []int
	next       int
	origins    map[int]string
	sources    map[int]*string
	creates    int
	interfaces map[string]string
}

// dashboardAnswer is a dashboard as the server answers it, the document last.
type dashboardAnswer struct {
	ID         int             `json:"id"`
	Position   int             `json:"position"`
	Revision   int             `json:"revision"`
	Origin     string          `json:"origin"`
	SourceName *string         `json:"source_name"`
	CreatedAt  int             `json:"created_at"`
	UpdatedAt  int             `json:"updated_at"`
	Document   json.RawMessage `json:"document"`
}

func compactJSON(t *testing.T, text string) string {
	t.Helper()
	var out bytes.Buffer
	if err := json.Compact(&out, []byte(text)); err != nil {
		t.Fatalf("compacting %s: %v", text, err)
	}
	return out.String()
}

func newEditFixture(t *testing.T, documents ...string) *editFixture {
	t.Helper()
	compacted := make([]string, len(documents))
	for index, document := range documents {
		compacted[index] = compactJSON(t, document)
	}
	f := &editFixture{canvasFixture: newCanvasFixture(t, compacted...), next: len(documents) + 1,
		origins: map[int]string{}, sources: map[int]*string{},
		interfaces: map[string]string{"opt1": "Example LAN", "opt3": "Example lab"}}
	for index := range documents {
		f.order = append(f.order, index+1)
		f.origins[index+1] = "created"
	}
	f.on("GET /api/dashboards", func(frontjsRequest) frontjsAnswer { return ok(f.listing()) })
	for id := 1; id <= 12; id++ {
		f.routesFor(id)
	}
	f.on("POST /api/dashboards", func(request frontjsRequest) frontjsAnswer {
		var body struct {
			Document   json.RawMessage `json:"document"`
			Origin     string          `json:"origin"`
			SourceName *string         `json:"source_name"`
		}
		if err := json.Unmarshal([]byte(request.Body), &body); err != nil {
			return frontjsAnswer{Status: 400, Body: map[string]string{"condition": "malformed_body"}}
		}
		f.lock.Lock()
		id := f.next
		f.next++
		f.creates++
		f.order = append(f.order, id)
		f.origins[id] = body.Origin
		f.sources[id] = body.SourceName
		f.lock.Unlock()
		f.canvasFixture.mutex.Lock()
		f.documents[id] = string(body.Document)
		f.revisions[id] = 1
		f.canvasFixture.mutex.Unlock()
		return ok(f.answerOf(id))
	})
	f.on("POST /api/dashboards/restore", func(request frontjsRequest) frontjsAnswer {
		var body dashboardAnswer
		if err := json.Unmarshal([]byte(request.Body), &body); err != nil {
			return frontjsAnswer{Status: 400, Body: map[string]string{"condition": "malformed_body"}}
		}
		f.lock.Lock()
		f.order = append(f.order, body.ID)
		sort.Ints(f.order)
		f.origins[body.ID] = body.Origin
		f.sources[body.ID] = body.SourceName
		f.lock.Unlock()
		f.canvasFixture.mutex.Lock()
		f.documents[body.ID] = string(body.Document)
		f.revisions[body.ID] = body.Revision
		f.canvasFixture.mutex.Unlock()
		return ok(f.answerOf(body.ID))
	})
	f.on("POST /api/dashboards/order", func(request frontjsRequest) frontjsAnswer {
		var body struct {
			IDs []int `json:"ids"`
		}
		_ = json.Unmarshal([]byte(request.Body), &body)
		f.lock.Lock()
		f.order = body.IDs
		f.lock.Unlock()
		return ok(map[string]any{"ids": body.IDs})
	})
	candidates := []any{}
	for _, identifier := range []string{"opt1", "opt3"} {
		candidates = append(candidates, map[string]any{"label": f.interfaces[identifier], "referenceable": true,
			"keys": map[string]any{"identifier": identifier, "device": "igb" + identifier[3:], "user_label": f.interfaces[identifier]}})
	}
	f.references["interface"] = map[string]any{"kind": "interface", "by": []string{"identifier", "device", "user_label"},
		"candidates": candidates, "remainder": 0}
	f.on("GET /api/references/resolve", func(request frontjsRequest) frontjsAnswer {
		var list []map[string]any
		if cut := strings.Index(request.Path, "?references="); cut >= 0 {
			decoded, _ := urlDecode(request.Path[cut+len("?references="):])
			_ = json.Unmarshal([]byte(decoded), &list)
		}
		answered := []any{}
		for index, reference := range list {
			state := "no_match"
			if f.resolves(reference) {
				state = "resolved"
			}
			answered = append(answered, map[string]any{"index": index, "state": state})
		}
		return ok(map[string]any{"aggregate_mode": "full", "references": answered})
	})
	f.on("GET /api/datasets", func(frontjsRequest) frontjsAnswer { return ok(map[string]any{"datasets": []any{}}) })
	// The registry as GET /api/widgets answers it: each built-in's manifest parameters, the core's
	// volume_measure where the manifest declares volumes, and its title and question keys.
	f.on("GET /api/widgets", func(frontjsRequest) frontjsAnswer {
		rows := []any{}
		for _, widget := range builtinWidgets(t) {
			parameters := []any{}
			for _, parameter := range widget.Parameters {
				parameters = append(parameters, parameter)
			}
			if widget.Volumes {
				parameters = append(parameters, map[string]any{"key": "volume_measure", "kind": "enum",
					"values": []string{"logged", "netflow", "best_available"}, "default": "best_available", "class": "data"})
			}
			prefix := "plugin." + widget.Type + ".widget." + widget.Type + "."
			rows = append(rows, map[string]any{"type": widget.Type, "shape": widget.Shape, "windowed": widget.Windowed,
				"backend": true, "parameters": parameters, "title_key": prefix + "title", "question_key": prefix + "question",
				"renderer_url": "/front/widgets/" + widget.Type + "/renderer.js"})
		}
		return ok(map[string]any{"widgets": rows, "shapes": []any{}, "refused": []any{}})
	})
	for _, widget := range builtinWidgets(t) {
		f.judged(t, widget)
	}
	return f
}

// resolves says whether a reference names something this fake installation has.
func (f *editFixture) resolves(reference map[string]any) bool {
	if reference["kind"] != "interface" {
		return true
	}
	value, _ := reference["value"].(string)
	_, found := f.interfaces[value]
	return found
}

// judged answers a type as the widget API would judge the request's references and keys: a top-level
// interface reference that names nothing empties the widget, a key the manifest does not declare is
// kept and reported. What these tests check is the editing, not the drawings, so a widget that
// resolves answers the measured nothing of reachable_no_rows -- a panel, cheap to draw -- rather
// than a recorded answer every renderer would draw.
func (f *editFixture) judged(t *testing.T, widget builtinWidget) {
	declared := map[string]manifestParameter{}
	for _, parameter := range widget.Parameters {
		declared[parameter.Key] = parameter
	}
	widgetType := widget.Type
	f.answer(widget.Type, func(parameters map[string]any) frontjsAnswer {
		answer := map[string]any{"type": widgetType, "condition": "reachable_no_rows", "data": nil,
			"parameters": parameters}
		unknown := map[string]any{}
		unresolved := []any{}
		for key, value := range parameters {
			parameter, known := declared[key]
			if !known && key != "volume_measure" {
				unknown[key] = value
				continue
			}
			if parameter.Kind != "reference_list" && parameter.Kind != "reference" {
				continue
			}
			list, isList := value.([]any)
			if !isList {
				list = []any{value}
			}
			for index, item := range list {
				reference, _ := item.(map[string]any)
				if reference != nil && !f.resolves(reference) {
					path := key
					if isList {
						path = fmt.Sprintf("%s[%d]", key, index)
					}
					unresolved = append(unresolved, map[string]any{"path": path, "kind": reference["kind"],
						"by": reference["by"], "value": reference["value"], "label": reference["label"], "reason": "no_match"})
				}
			}
		}
		answer["unknown_parameters"] = unknown
		answer["unresolved_references"] = unresolved
		if len(unresolved) > 0 {
			answer["condition"] = "unresolved_reference"
			answer["data"] = nil
		}
		return ok(answer)
	})
}

func (f *editFixture) answerOf(id int) dashboardAnswer {
	f.lock.Lock()
	origin, source := f.origins[id], f.sources[id]
	f.lock.Unlock()
	f.canvasFixture.mutex.Lock()
	defer f.canvasFixture.mutex.Unlock()
	return dashboardAnswer{ID: id, Position: id, Revision: f.revisions[id], Origin: origin, SourceName: source,
		CreatedAt: 1700000000, UpdatedAt: 1700000000, Document: json.RawMessage(f.documents[id])}
}

func (f *editFixture) listing() map[string]any {
	f.lock.Lock()
	order := append([]int(nil), f.order...)
	f.lock.Unlock()
	listed := []any{}
	for position, id := range order {
		var document struct {
			Title    string `json:"title"`
			Canvases []struct {
				ID    string `json:"id"`
				Title string `json:"title"`
			} `json:"canvases"`
		}
		_ = json.Unmarshal([]byte(f.document(id)), &document)
		canvases := []any{}
		for _, canvas := range document.Canvases {
			canvases = append(canvases, map[string]any{"id": canvas.ID, "title": canvas.Title})
		}
		answer := f.answerOf(id)
		listed = append(listed, map[string]any{"id": id, "position": position + 1, "title": document.Title,
			"revision": answer.Revision, "origin": answer.Origin, "source_name": answer.SourceName, "canvases": canvases})
	}
	return map[string]any{"dashboards": listed, "format": map[string]int{"current": 1, "minimum": 1}}
}

func (f *editFixture) exists(id int) bool {
	f.lock.Lock()
	defer f.lock.Unlock()
	for _, other := range f.order {
		if other == id {
			return true
		}
	}
	return false
}

func (f *editFixture) routesFor(id int) {
	path := fmt.Sprintf("/api/dashboards/%d", id)
	f.on("GET "+path, func(frontjsRequest) frontjsAnswer {
		if !f.exists(id) {
			return frontjsAnswer{Status: 404, Body: map[string]string{"condition": "unknown_dashboard"}}
		}
		answer := f.answerOf(id)
		return ok(map[string]any{"id": answer.ID, "revision": answer.Revision, "origin": answer.Origin,
			"source_name": answer.SourceName, "document": answer.Document})
	})
	f.on("POST "+path, func(request frontjsRequest) frontjsAnswer {
		if !f.exists(id) {
			return frontjsAnswer{Status: 404, Body: map[string]string{"condition": "unknown_dashboard"}}
		}
		var body struct {
			Revision int             `json:"revision"`
			Document json.RawMessage `json:"document"`
		}
		_ = json.Unmarshal([]byte(request.Body), &body)
		f.canvasFixture.mutex.Lock()
		if body.Revision != f.revisions[id] {
			current := f.revisions[id]
			f.canvasFixture.mutex.Unlock()
			return frontjsAnswer{Status: 409, Body: map[string]any{"condition": "revision_conflict", "revision": current}}
		}
		f.revisions[id]++
		// The member's bytes, as the server keeps them: the request's own text between the document's
		// key and the envelope's end.
		at := strings.Index(request.Body, `"document":`)
		f.documents[id] = strings.TrimSuffix(request.Body[at+len(`"document":`):], "}")
		f.canvasFixture.mutex.Unlock()
		return ok(f.answerOf(id))
	})
	f.on("POST "+path+"/delete", func(frontjsRequest) frontjsAnswer {
		if !f.exists(id) {
			return frontjsAnswer{Status: 404, Body: map[string]string{"condition": "unknown_dashboard"}}
		}
		answer := f.answerOf(id)
		f.lock.Lock()
		kept := []int{}
		for _, other := range f.order {
			if other != id {
				kept = append(kept, other)
			}
		}
		f.order = kept
		f.lock.Unlock()
		return ok(answer)
	})
}

// editPage opens a path with the grid given a width. No renderer is loaded: every widget of these
// tests answers a panel (judged), which the canvas draws itself.
func (f *editFixture) editPage(t *testing.T, path string) *frontjs.Runtime {
	t.Helper()
	page := frontjs.New(t, frontjs.Options{Handler: f.handle})
	page.Start(path)
	page.Advance(0)
	setGridWidth(page, 1600)
	return page
}

func setGridWidth(page *frontjs.Runtime, width int) {
	page.Run(fmt.Sprintf(`(function () { var grid = document.getElementById("canvas-grid");
  if (grid) { grid.clientWidth = %d; } window.innerWidth = %d; __windowEvent("resize"); })()`, width, width+400))
	page.Advance(0)
}

// widgetIn reads one widget of a stored document.
func widgetIn(t *testing.T, text, canvas, id string) map[string]any {
	t.Helper()
	var document struct {
		Canvases []struct {
			ID      string           `json:"id"`
			Widgets []map[string]any `json:"widgets"`
		} `json:"canvases"`
	}
	if err := json.Unmarshal([]byte(text), &document); err != nil {
		t.Fatalf("the stored document does not decode: %v\n%s", err, text)
	}
	for _, c := range document.Canvases {
		if c.ID != canvas {
			continue
		}
		for _, widget := range c.Widgets {
			if widget["id"] == id {
				return widget
			}
		}
	}
	return nil
}

func placementOf(t *testing.T, text, canvas, id string) map[string]any {
	t.Helper()
	widget := widgetIn(t, text, canvas, id)
	if widget == nil {
		t.Fatalf("no widget %s in %s", id, text)
	}
	placement, _ := widget["placement"].(map[string]any)
	return placement
}

// pointer dispatches a pointer event with coordinates, at an element or, with an empty selector, at
// the body (where the document's listeners hear it).
func pointer(page *frontjs.Runtime, selector, kind string, x, y int) {
	page.Run(fmt.Sprintf(`(function () {
  var target = %q ? document.querySelector(%q) : document.body;
  var event = __makeEvent(%q);
  event.clientX = %d; event.clientY = %d;
  target.dispatchEvent(event);
})()`, selector, selector, kind, x, y))
	page.Advance(0)
}

// dockText is the code tab's text, as the reader sees it in the pane.
func dockText(page *frontjs.Runtime) string {
	return page.Run(`document.getElementById("dock-code").value`).String()
}

// TestEditModeWritesThePlacementAtOnceAndTheLayoutNever (F7-AC30, the write-through half).
func TestEditModeWritesThePlacementAtOnceAndTheLayoutNever(t *testing.T) {
	t.Parallel()
	f := newEditFixture(t, oneCanvas)
	page := f.editPage(t, "/canvas/1/main")
	page.Click("#edit-toggle")
	page.Click("#rail-editor")
	if page.Count("#doc-dock") != 1 {
		t.Fatalf("the editor opened no dock:\n%s", page.Outline())
	}
	// The keyboard buttons, named by the card's heading.
	label := page.Attribute(`div[data-edit-bar="sites"] button[data-edit="right"]`, "aria-label")
	if !strings.Contains(label, "Top sites") {
		t.Errorf("the move button is named %q, not by the card's heading", label)
	}
	page.Click(`div[data-edit-bar="sites"] button[data-edit="right"]`)
	page.Advance(0)
	if got := placementOf(t, f.document(1), "main", "sites")["x"]; got != float64(1) {
		t.Fatalf("moving right wrote x %v", got)
	}
	if !strings.Contains(dockText(page), "x: 1") {
		t.Errorf("the Document tab does not show the new placement:\n%s", dockText(page))
	}
	page.Click(`div[data-edit-bar="sites"] button[data-edit="taller"]`)
	page.Advance(0)
	if got := placementOf(t, f.document(1), "main", "sites")["h"]; got != float64(9) {
		t.Errorf("making it taller wrote h %v", got)
	}
	// The pointer: the right edge dragged two columns wider is in the Document tab while the pointer
	// is still down, and written once when it is released.
	writes := len(page.Writes())
	column := (1600 + 16) / 12
	pointer(page, `span[data-grip="e"][data-widget="address"]`, "pointerdown", 500, 300)
	pointer(page, "", "pointermove", 500-2*column, 300)
	if len(page.Writes()) != writes {
		t.Errorf("a drag in progress wrote the document")
	}
	if !strings.Contains(dockText(page), "w: 3") {
		t.Errorf("the Document tab does not follow the drag:\n%s", dockText(page))
	}
	pointer(page, "", "pointerup", 500-2*column, 300)
	if got := placementOf(t, f.document(1), "main", "address")["w"]; got != float64(3) {
		t.Errorf("dragging the right edge two columns in wrote w %v", got)
	}
	// Moving by the card itself, a row down.
	pointer(page, `section[data-widget-card="address"]`, "pointerdown", 600, 300)
	pointer(page, "", "pointermove", 600, 300+56)
	pointer(page, "", "pointerup", 600, 300+56)
	if got := placementOf(t, f.document(1), "main", "address")["y"]; got != float64(1) {
		t.Errorf("dragging the card a row down wrote y %v", got)
	}
	// The narrow layout and the derived columns write nothing: the export before and after a
	// window resize is the same.
	before := page.Run(`opnview.editing.exported(opnview.editing.document(1), "json").text`).String()
	writes = len(page.Writes())
	for _, width := range []int{1100, 600, 390, 1600} {
		setGridWidth(page, width)
	}
	after := page.Run(`opnview.editing.exported(opnview.editing.document(1), "json").text`).String()
	if before != after || len(page.Writes()) != writes {
		t.Errorf("resizing the window changed the stored placement")
	}
}

// TestThePickerListsTheServedTypesAndANewWidgetStartsAtItsDefaults (F7-AC31).
func TestThePickerListsTheServedTypesAndANewWidgetStartsAtItsDefaults(t *testing.T) {
	t.Parallel()
	f := newEditFixture(t, oneCanvas)
	page := f.editPage(t, "/canvas/1/main")
	page.Click("#edit-toggle")
	page.Click("#add-widget-slot")
	widgets := builtinWidgets(t)
	if got := page.Count("button.picker-item"); got != len(widgets) {
		t.Fatalf("the picker lists %d types, /api/widgets serves %d", got, len(widgets))
	}
	for _, step6 := range []string{"security_alerts_over_time", "alerts_by_signature", "alerts_by_client_and_interface"} {
		if page.Count(fmt.Sprintf(`button[data-pick="%s"]`, step6)) != 0 {
			t.Errorf("the picker offers %s, which this build does not serve", step6)
		}
	}
	for _, widget := range widgets {
		item := fmt.Sprintf(`button[data-pick="%s"]`, widget.Type)
		for _, part := range []string{".pi-name", ".pi-question", ".pi-shape"} {
			if strings.TrimSpace(page.Text(item+" "+part)) == "" {
				t.Errorf("%s: the picker shows no %s", widget.Type, part)
			}
		}
		if strings.Contains(page.Text(item+" .pi-name"), "⁇") || strings.Contains(page.Text(item+" .pi-question"), "⁇") {
			t.Errorf("%s: the picker's title or question is not in the catalogue", widget.Type)
		}
	}
	page.Click(`button[data-pick="connection_tree"]`)
	page.Advance(0)
	added := widgetIn(t, f.document(1), "main", "connection-tree")
	if added == nil {
		t.Fatalf("picking a type added no widget: %s", f.document(1))
	}
	want := map[string]any{}
	for _, widget := range widgets {
		if widget.Type != "connection_tree" {
			continue
		}
		// The registry's declaration: the manifest's, and the core's volume_measure where it adds it.
		if widget.Volumes {
			want["volume_measure"] = "best_available"
		}
		// Its period excepted: a new widget follows the canvas's range (the orchestrator's amendment of
		// F7-AC31, 17 October 2026), so it carries none of its own.
		for _, parameter := range widget.Parameters {
			if parameter.Default != nil && parameter.Key != "period" {
				want[parameter.Key] = parameter.Default
			}
		}
	}
	if fmt.Sprint(added["parameters"]) != fmt.Sprint(want) {
		t.Errorf("a new widget starts at %v, not at its manifest's defaults %v", added["parameters"], want)
	}
	if page.Count(`section[data-widget-card="connection-tree"] span[data-chip="own_range"]`) != 0 {
		t.Error("a new widget says it has a range of its own")
	}
	if revealed := page.Run(`__dom.revealed ? __dom.revealed.getAttribute("data-widget-card") : ""`).String(); revealed != "connection-tree" {
		t.Errorf("the new widget was not scrolled into view (%q was)", revealed)
	}
}

// undoCheckers are an undo by one of the ways a reader has, and a check that an edit writes the
// first dashboard and that Undo then puts its bytes back.
func undoCheckers(t *testing.T, f *editFixture, page *frontjs.Runtime) (func(string, string), func(string, func(), string)) {
	undo := func(name string, via string) {
		t.Helper()
		switch via {
		case "keyboard":
			page.Run(`(function () { var event = __makeEvent("keydown", { key: "z" }); event.ctrlKey = true;
  document.body.dispatchEvent(event); })()`)
		case "notice":
			if page.Count("#notice-undo") != 1 {
				t.Errorf("%s: the removal offers no undo", name)
				return
			}
			page.Click("#notice-undo")
		default:
			page.Click(via)
		}
		page.Advance(0)
	}
	check := func(name string, act func(), via string) {
		t.Helper()
		before := f.document(1)
		act()
		page.Advance(0)
		if f.document(1) == before {
			t.Errorf("%s wrote nothing", name)
			return
		}
		undo(name, via)
		if f.document(1) != before {
			t.Errorf("%s then Undo does not restore the document byte for byte:\n%s\n%s", name, f.document(1), before)
		}
	}
	return undo, check
}

// twoDashboards is a second dashboard, so an order and a deletion have something to act on.
const twoDashboards = `{"format_version": 1, "title": "Other", "canvases": [{"id": "x", "title": "X", "widgets": []}]}`

// TestEveryCanvasEditIsUndoneByteForByte (F7-AC32): the layout, the widgets, the dock and the
// canvases.
func TestEveryCanvasEditIsUndoneByteForByte(t *testing.T) {
	t.Parallel()
	f := newEditFixture(t, oneCanvas, twoDashboards)
	page := f.editPage(t, "/canvas/1/main")
	page.Click("#edit-toggle")
	_, check := undoCheckers(t, f, page)
	check("a move", func() { page.Click(`div[data-edit-bar="sites"] button[data-edit="down"]`) }, "#canvas-undo")
	check("a resize", func() {
		pointer(page, `span[data-grip="s"][data-widget="sites"]`, "pointerdown", 100, 100)
		pointer(page, "", "pointermove", 100, 100+2*56)
		pointer(page, "", "pointerup", 100, 100+2*56)
	}, "keyboard")
	check("an added widget", func() {
		page.Click("#add-widget-slot")
		page.Click(`button[data-pick="top_sites"]`)
	}, "#canvas-undo")
	check("a removed widget", func() { page.Click(`div[data-edit-bar="address"] button[data-edit="remove"]`) }, "notice")
	check("a period", func() { page.Choose("#period-select", "30d") }, "#canvas-undo")
	check("an Apply", func() {
		page.Click("#rail-editor")
		page.Click("#dock-tab-document")
		page.Input("#dock-code", strings.Replace(dockText(page), "title: Main\n", "title: Main hall\n", 1))
		page.Click("#dock-apply")
	}, "#canvas-undo")
	check("a form's Apply", func() {
		page.Click(`button[data-configure="sites"]`)
		page.Input(`#form-sites-limit`, "5")
		page.Click("#dock-apply")
	}, "#canvas-undo")
	check("a renamed canvas", func() { page.Choose("#canvas-title-input", "Front room") }, "#canvas-undo")
	check("a canvas moved", func() { page.Click("#canvas-move-right") }, "#canvas-undo")
	check("a canvas added", func() { page.Click("#canvas-add") }, "#canvas-undo")
	page.Go("/canvas/1/main")
	page.Advance(0)
	check("a canvas removed", func() { page.Click("#canvas-remove") }, "notice")
}

// TestEveryDashboardEditIsUndoneByteForByte (F7-AC32): the dashboards themselves.
func TestEveryDashboardEditIsUndoneByteForByte(t *testing.T) {
	t.Parallel()
	f := newEditFixture(t, oneCanvas, twoDashboards)
	page := f.editPage(t, "/dashboards")
	undo, check := undoCheckers(t, f, page)
	check("a renamed dashboard", func() { page.Choose("#dashboard-title-1", "Front office") }, "#dashboards-undo")
	order := fmt.Sprint(f.listing()["dashboards"])
	page.Click(`tr[data-dashboard="1"] button[data-move="down"]`)
	page.Advance(0)
	if fmt.Sprint(f.listing()["dashboards"]) == order {
		t.Error("moving a dashboard down reordered nothing")
	}
	undo("an order", "#dashboards-undo")
	if fmt.Sprint(f.listing()["dashboards"]) != order {
		t.Error("undoing an order does not restore it")
	}
	before := f.document(2)
	page.Click(`button[data-delete="2"]`)
	page.Advance(0)
	if f.exists(2) {
		t.Fatal("deleting a dashboard left it")
	}
	undo("a deleted dashboard", "notice")
	if !f.exists(2) || f.document(2) != before {
		t.Errorf("undoing a deletion does not restore the dashboard byte for byte")
	}
	page.Go("/dashboards")
	page.Advance(0)
	page.Click("#dashboard-new")
	page.Advance(0)
	if f.creates != 1 || !f.exists(3) {
		t.Fatal("creating a dashboard stored none")
	}
	page.Go("/dashboards")
	page.Advance(0)
	undo("a created dashboard", "#dashboards-undo")
	if f.exists(3) {
		t.Error("undoing a creation leaves the dashboard")
	}
}

// TestTheFormOffersExactlyTheManifestKeysAndOnlyDropdownsForReferences (F7-AC33).
func TestTheFormOffersExactlyTheManifestKeysAndOnlyDropdownsForReferences(t *testing.T) {
	t.Parallel()
	widgets := builtinWidgets(t)
	entries := []string{}
	for index, widget := range widgets {
		parameters := ""
		if widget.Type == "client_volume_ranking" {
			parameters = `, "parameters": {"interfaces": [{"kind": "interface", "by": "identifier", "value": "opt7", "label": "Workshop"}]}`
		}
		entries = append(entries, fmt.Sprintf(`{"id": "w%d", "type": %q, "placement": {"x": 0, "y": %d, "w": 6, "h": 4}%s}`,
			index, widget.Type, index*4, parameters))
	}
	f := newEditFixture(t, `{"format_version": 1, "title": "Every type", "canvases": [{"id": "all", "title": "All", "widgets": [`+
		strings.Join(entries, ",")+`]}]}`)
	// What is checked is the form, not the drawings: every card answers a source not yet probed, a
	// panel, so the canvas stays cheap to draw while the dock is opened twenty-six times.
	for _, widget := range widgets {
		widgetType := widget.Type
		f.answer(widgetType, func(map[string]any) frontjsAnswer {
			return ok(map[string]any{"type": widgetType, "condition": "not_yet_probed", "data": nil, "parameters": map[string]any{}})
		})
	}
	page := f.editPage(t, "/canvas/1/all")
	for index, widget := range widgets {
		id := fmt.Sprintf("w%d", index)
		page.Click(fmt.Sprintf(`button[data-configure="%s"]`, id))
		got := []string{}
		listed := page.Run(`JSON.stringify(document.querySelectorAll("#doc-dock [data-form-field]").map(function (el) {
  var field = el.getAttribute("data-form-field");
  var texts = el.querySelectorAll("input").filter(function (input) { return input.getAttribute("type") === "text"; });
  return { key: field, selects: el.querySelectorAll("select[data-form-reference]").length, texts: texts.length,
    unresolved: (el.getAttribute("class") || "").indexOf("is-unresolved") >= 0 };
}))`).String()
		var fields []struct {
			Key        string
			Selects    int
			Texts      int
			Unresolved bool
		}
		if err := json.Unmarshal([]byte(listed), &fields); err != nil {
			t.Fatal(err)
		}
		want := []string{}
		kinds := map[string]string{}
		for _, parameter := range widget.Parameters {
			want = append(want, parameter.Key)
			kinds[parameter.Key] = parameter.Kind
		}
		if widget.Volumes {
			want = append(want, "volume_measure")
		}
		for _, field := range fields {
			got = append(got, field.Key)
			if kind := kinds[field.Key]; kind == "reference" || kind == "reference_list" {
				if field.Selects == 0 || field.Texts != 0 {
					t.Errorf("%s.%s: a reference is edited by %d dropdowns and %d text fields", widget.Type, field.Key,
						field.Selects, field.Texts)
				}
			}
			if widget.Type == "client_volume_ranking" && field.Key == "interfaces" && !field.Unresolved {
				t.Errorf("an unresolved interface is not marked not found in the form")
			}
		}
		sort.Strings(got)
		sort.Strings(want)
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("%s: the form offers %v, the manifest declares %v", widget.Type, got, want)
		}
	}
	// The unresolved value is the current selection, marked, never typed.
	page.Click(`button[data-configure="w` + fmt.Sprint(indexOf(widgets, "client_volume_ranking")) + `"]`)
	selected := page.Run(`(function () {
  var select = document.querySelector("#doc-dock select[data-form-reference]");
  var option = select.querySelectorAll("option").filter(function (o) { return o.value === "current"; })[0];
  return select.value + "|" + (option ? option.textContent : "") + "|" + select.getAttribute("aria-invalid");
})()`).String()
	if !strings.HasPrefix(selected, "current|Workshop, not found|true") {
		t.Errorf("the unresolved reference shows as %q", selected)
	}
	if missing := page.Run(`JSON.stringify(opnview.strings.missing())`).String(); missing != "[]" {
		t.Errorf("the forms ask for keys the catalogue lacks: %s", missing)
	}
}

func indexOf(widgets []builtinWidget, widgetType string) int {
	for index, widget := range widgets {
		if widget.Type == widgetType {
			return index
		}
	}
	return -1
}

// TestTheDockIsOnePanelAndItsPendingFormSurvivesARefresh (F7-AC37's structure, and F7-35's
// comment-loss warning).
func TestTheDockIsOnePanelAndItsPendingFormSurvivesARefresh(t *testing.T) {
	t.Parallel()
	f := newEditFixture(t, oneCanvas)
	page := f.editPage(t, "/canvas/1/main")
	page.Click(`button[data-configure="sites"]`)
	for selector, want := range map[string]int{`#doc-dock button[role="tab"]`: 3, `#doc-dock button[role="radio"]`: 0,
		"#dock-apply": 1, "#dock-revert": 1} {
		if got := page.Count(selector); got != want {
			t.Errorf("the dock on the form holds %d of %s, not %d", got, selector, want)
		}
	}
	page.Input("#form-sites-limit", "7")
	if page.Count(`section[data-widget-card="sites"] span[data-chip="unapplied"]`) != 1 {
		t.Errorf("the card does not say it is unapplied while the form holds a value:\n%s", page.Outline())
	}
	if !strings.Contains(lastWidgetRequest(page, "top_sites"), `"limit":7`) {
		t.Errorf("the card does not preview the form's value")
	}
	stored := f.document(1)
	page.Advance(61000)
	if f.document(1) != stored || page.Count(`section[data-widget-card="sites"] span[data-chip="unapplied"]`) != 1 ||
		page.Run(`document.getElementById("form-sites-limit").value`).String() != "7" {
		t.Errorf("a refresh lost or wrote the pending form")
	}
	page.Click("#dock-revert")
	page.Click("#dock-tab-document")
	for selector, want := range map[string]int{`#doc-dock button[role="radio"]`: 2, "#doc-dock textarea": 1} {
		if got := page.Count(selector); got != want {
			t.Errorf("the dock's code tab holds %d of %s, not %d", got, selector, want)
		}
	}
	// YAML holding a comment warns before saving, and saves on the second Apply.
	writes := len(page.Writes())
	page.Input("#dock-code", "# kept by hand\n"+strings.Replace(dockText(page), "title: Example\n", "title: Example office\n", 1))
	page.Click("#dock-apply")
	if page.Count("p.code-warning") != 1 || len(page.Writes()) != writes {
		t.Fatalf("a YAML text with a comment was saved without the warning")
	}
	page.Click("#dock-apply")
	page.Advance(0)
	if len(page.Writes()) != writes+1 {
		t.Errorf("Apply after the warning did not save: %s", page.Run(`(document.getElementById("dock-error") || {}).textContent + " | " + document.getElementById("dock-code").value.slice(0, 300)`).String())
	}
	// A syntax error is named with its line and stays marked.
	page.Input("#dock-code", dockText(page)+"\n  - : : :\n")
	page.Click("#dock-apply")
	if page.Count("#dock-error") != 1 || page.Count(".code-line.is-error") != 1 {
		t.Errorf("a refused Apply is not named and marked on its line: %s", page.Run(`(document.getElementById("dock-error") || {}).textContent`).String())
	}
}

// importedMockupDocument is the step-3 mockup's DOC_IMPORTED, as JSON.
func importedMockupDocument(t *testing.T, page *frontjs.Runtime) string {
	t.Helper()
	text := string(repositoryFile(t, "docs", "mockups", "canvas-mockup.html"))
	start := strings.Index(text, "var DOC_IMPORTED = {")
	end := strings.Index(text[start:], "\n};\n")
	if start < 0 || end < 0 {
		t.Fatal("the mockup holds no DOC_IMPORTED")
	}
	literal := text[start+len("var DOC_IMPORTED = ") : start+end+2]
	return page.Run("JSON.stringify(" + literal + ")").String()
}

// importText imports a text through the Import view and answers the dashboard id it was stored as, or 0.
func importText(t *testing.T, f *editFixture, page *frontjs.Runtime, text string) int {
	t.Helper()
	before := f.creates
	page.Go("/dashboards/import")
	page.Advance(0)
	page.Input("#import-text", text)
	page.Click("#import-submit")
	page.Advance(0)
	if f.creates == before {
		return 0
	}
	f.lock.Lock()
	defer f.lock.Unlock()
	return f.next - 1
}

// TestImportStoresReportsMarksAndExportsUnchanged (F7-AC35).
func TestImportStoresReportsMarksAndExportsUnchanged(t *testing.T) {
	t.Parallel()
	f := newEditFixture(t, oneCanvas)
	page := f.editPage(t, "/canvas/1/main")
	worked := fencedBlock(t, string(repositoryFile(t, "docs", "dashboard-format.md")), "json")
	if id := importText(t, f, page, worked); id == 0 {
		t.Fatalf("the worked example was not stored:\n%s", page.Outline())
	} else if compactJSON(t, f.document(id)) != compactJSON(t, worked) {
		t.Errorf("the worked example was stored changed")
	}
	imported := importedMockupDocument(t, page)
	id := importText(t, f, page, imported)
	if id == 0 {
		t.Fatalf("DOC_IMPORTED was not stored:\n%s", page.Outline())
	}
	if f.origins[id] != "imported" {
		t.Errorf("the import is stored with origin %q", f.origins[id])
	}
	if page.Path() != fmt.Sprintf("/canvas/%d/interface-review", id) {
		t.Errorf("the import lands on %s", page.Path())
	}
	setGridWidth(page, 1600)
	rows := page.Run(`JSON.stringify(document.querySelectorAll("tr[data-report-path]").map(function (row) {
  return row.getAttribute("data-report-condition") + " " + row.getAttribute("data-report-path"); }))`).String()
	want := `["unresolved_reference canvases[0].widgets[1].parameters.interfaces[0]","unknown_widget_type canvases[0].widgets[2].type","unknown_parameter_key canvases[0].widgets[4].parameters.colour_scheme"]`
	if rows != want {
		t.Errorf("the import report lists\n%s\nnot\n%s", rows, want)
	}
	if page.Count("tr.group-row") != 1 {
		t.Errorf("the report is not grouped by canvas")
	}
	for widget, condition := range map[string]string{"workshop-clients": "unresolved_reference",
		"client-activity": "unknown_widget_type"} {
		if page.Count(fmt.Sprintf(`section[data-widget-card="%s"] div[data-condition="%s"]`, widget, condition)) != 1 {
			t.Errorf("%s does not keep its card with the %s panel", widget, condition)
		}
	}
	// The code view marks exactly those lines, each notice naming kind, by, value and label.
	page.Click("#rail-editor")
	page.Click("#dock-tab-document")
	page.Click("#syntax-yaml")
	marked := page.Run(`JSON.stringify(document.querySelectorAll("#dock-ghost .code-line").filter(function (line) {
  var cls = line.getAttribute("class"); return cls.indexOf("is-unresolved") >= 0 || cls.indexOf("is-kept") >= 0;
}).map(function (line) { return Number(line.getAttribute("data-line")) - 1; }))`).String()
	expected := page.Run(`(function () {
  var lines = opnview.document.serialise(opnview.editing.document(` + fmt.Sprint(id) + `), "yaml").lines;
  return JSON.stringify(["canvases[0].widgets[1].parameters.interfaces[0]", "canvases[0].widgets[2].type",
    "canvases[0].widgets[4].parameters.colour_scheme"].map(function (path) { return opnview.document.lineOf(lines, path); })
    .sort(function (a, b) { return a - b; }));
})()`).String()
	if marked != expected {
		t.Errorf("the code view marks lines %s, the issues are on %s", marked, expected)
	}
	notices := page.Run(`JSON.stringify(document.querySelectorAll("#dock-ghost .code-notice").map(function (n) { return n.textContent; }))`).String()
	for _, part := range []string{"interface", "identifier", "opt7", "Workshop", "client_activity_timeline", "colour_scheme", "sunset"} {
		if !strings.Contains(notices, part) {
			t.Errorf("the line notices do not name %s: %s", part, notices)
		}
	}
	// Exporting without repair writes the imported members and references back unchanged.
	for _, syntax := range []string{"json", "yaml"} {
		requests := len(page.Requests())
		back := page.Run(fmt.Sprintf(`(function () {
  var file = opnview.editing.exported(opnview.editing.document(%d), %q);
  return JSON.stringify(opnview.document.parse(file.text, %q).document);
})()`, id, syntax, syntax)).String()
		if back != imported {
			t.Errorf("exporting in %s changed the document:\n%s\n%s", syntax, back, imported)
		}
		if len(page.Requests()) != requests {
			t.Errorf("exporting made a request")
		}
	}
	page.Go("/dashboards")
	page.Advance(0)
	requests := len(page.Requests())
	page.Click(fmt.Sprintf(`button[data-export="%d"]`, id))
	if len(page.Requests()) != requests {
		t.Errorf("the export control made a request")
	}
}

// TestRepairRewritesOnlyTheChosenReference (F7-AC36's repairs).
func TestRepairRewritesOnlyTheChosenReference(t *testing.T) {
	t.Parallel()
	f := newEditFixture(t, oneCanvas)
	page := f.editPage(t, "/canvas/1/main")
	imported := importedMockupDocument(t, page)
	id := importText(t, f, page, imported)
	if id == 0 {
		t.Fatal("DOC_IMPORTED was not stored")
	}
	setGridWidth(page, 1600)
	repaired := func() string {
		var document map[string]any
		_ = json.Unmarshal([]byte(imported), &document)
		widget := document["canvases"].([]any)[0].(map[string]any)["widgets"].([]any)[1].(map[string]any)
		widget["parameters"].(map[string]any)["interfaces"] = []any{map[string]any{"kind": "interface", "by": "identifier",
			"value": "opt1", "label": "Example LAN"}}
		raw, _ := json.Marshal(document)
		return string(raw)
	}
	same := func(text string) string {
		var decoded any
		_ = json.Unmarshal([]byte(text), &decoded)
		raw, _ := json.Marshal(decoded)
		return string(raw)
	}
	// Through the form, from the report's Repair.
	page.Click(`button[data-repair="canvases[0].widgets[1].parameters.interfaces[0]"]`)
	page.Choose("#form-workshop-clients-interfaces", "0")
	page.Click("#dock-apply")
	page.Advance(0)
	if same(f.document(id)) != same(repaired()) {
		t.Errorf("repairing through the form changed more than the reference:\n%s", f.document(id))
	}
	if page.Count(`tr[data-report-path="canvases[0].widgets[1].parameters.interfaces[0]"]`) != 0 {
		t.Errorf("the repaired reference is still reported")
	}
	page.Click("#dock-tab-document")
	if page.Count("#dock-ghost .is-unresolved") != 1 {
		t.Errorf("after Apply the code view marks %d unresolved lines, the unknown type's alone was expected",
			page.Count("#dock-ghost .is-unresolved"))
	}
	page.Click("#canvas-undo")
	page.Advance(0)
	if same(f.document(id)) != same(imported) {
		t.Fatalf("undoing the repair does not restore the import")
	}
	// Through the code tab.
	page.Click("#rail-editor")
	page.Click("#rail-editor")
	page.Click("#dock-tab-document")
	page.Click("#syntax-yaml")
	text := dockText(page)
	text = strings.Replace(text, "value: opt7\n", "value: opt1\n", 1)
	text = strings.Replace(text, "label: Workshop\n", "label: Example LAN\n", 1)
	page.Input("#dock-code", text)
	page.Click("#dock-apply")
	page.Advance(0)
	if same(f.document(id)) != same(repaired()) {
		t.Errorf("repairing through the code tab changed more than the reference:\n%s", f.document(id))
	}
}

// TestABrokenImportStoresNothingAndNamesWhereItBreaks (F7-AC36's refusals).
func TestABrokenImportStoresNothingAndNamesWhereItBreaks(t *testing.T) {
	t.Parallel()
	f := newEditFixture(t, oneCanvas)
	page := f.editPage(t, "/dashboards/import")
	var cases []struct {
		Name       string          `json:"name"`
		Text       string          `json:"text"`
		File       string          `json:"file"`
		PadToBytes int             `json:"pad_to_bytes"`
		Expect     *map[string]any `json:"expect"`
	}
	if err := json.Unmarshal(repositoryFile(t, "testdata", "dashboards", "conformance", "cases.json"), &cases); err != nil {
		t.Fatal(err)
	}
	for _, entry := range cases {
		if entry.Expect == nil {
			continue
		}
		text := entry.Text
		if entry.File != "" {
			text = string(repositoryFile(t, "testdata", "dashboards", "conformance", "documents", entry.File))
		}
		if entry.PadToBytes > len(text) {
			text += strings.Repeat(" ", entry.PadToBytes-len(text))
		}
		if stored := importText(t, f, page, text); stored != 0 {
			t.Errorf("%s: a structurally broken file was stored", entry.Name)
			continue
		}
		condition := page.Attribute("#import-refusal", "data-condition")
		expected := (*entry.Expect)["condition"].(string)
		if condition != expected && expected != "malformed_body" && expected != "document_too_large" {
			t.Errorf("%s: refused %s, not %s", entry.Name, condition, expected)
		}
		shown := page.Text("#import-refusal")
		for _, key := range []string{"canvas_index", "widget_index"} {
			if index, given := (*entry.Expect)[key].(float64); given && condition == expected &&
				!strings.Contains(shown, fmt.Sprint(index)) {
				t.Errorf("%s: the refusal %q does not name %s %v", entry.Name, shown, key, index)
			}
		}
		if field, _ := (*entry.Expect)["field"].(string); field != "" && condition == expected && !strings.Contains(shown, field) {
			t.Errorf("%s: the refusal %q does not name the field %s", entry.Name, shown, field)
		}
	}
	// A newer format is refused naming both versions.
	if stored := importText(t, f, page, `{"format_version": 2, "canvases": [{"id": "c", "title": "C", "widgets": []}]}`); stored != 0 {
		t.Fatal("a format_version 2 file was stored")
	}
	if shown := page.Text("#import-refusal"); !strings.Contains(shown, "2") || !strings.Contains(shown, "1") ||
		page.Attribute("#import-refusal", "data-condition") != "format_version_too_new" {
		t.Errorf("a format_version 2 file is refused as %q", shown)
	}
}

// TestImportingTheWorkedExampleReportsMarksAndExportsUnchanged (F7-AC35, for the corrected worked
// example of docs/dashboard-format.md): on an installation lacking the interface it names, it is
// stored, the report lists that reference under its canvas and widget, the card keeps its footprint
// with the unresolved panel, the code view marks that line with a notice naming kind, by, value and
// label, and an export without repair writes its members and references back unchanged.
func TestImportingTheWorkedExampleReportsMarksAndExportsUnchanged(t *testing.T) {
	t.Parallel()
	f := newEditFixture(t, oneCanvas)
	worked := fencedBlock(t, string(repositoryFile(t, "docs", "dashboard-format.md")), "json")
	var document struct {
		Canvases []struct {
			Widgets []struct {
				ID         string         `json:"id"`
				Parameters map[string]any `json:"parameters"`
			} `json:"widgets"`
		} `json:"canvases"`
	}
	if err := json.Unmarshal([]byte(worked), &document); err != nil {
		t.Fatal(err)
	}
	// The interface the example names is taken away from the installation, and every parameter
	// naming it is what the report must list.
	missing := map[string]bool{}
	var want []string
	var cards, labels []string
	for canvasIndex, canvas := range document.Canvases {
		for widgetIndex, widget := range canvas.Widgets {
			for key, value := range widget.Parameters {
				list, _ := value.([]any)
				for index, item := range list {
					reference, _ := item.(map[string]any)
					if reference == nil || reference["kind"] != "interface" {
						continue
					}
					missing[reference["value"].(string)] = true
					if label, _ := reference["label"].(string); label != "" {
						labels = append(labels, label)
					}
					want = append(want, fmt.Sprintf("unresolved_reference canvases[%d].widgets[%d].parameters.%s[%d]",
						canvasIndex, widgetIndex, key, index))
					cards = append(cards, widget.ID)
				}
			}
		}
	}
	if len(want) == 0 {
		t.Fatal("the worked example names no interface: this test checks nothing")
	}
	for identifier := range missing {
		delete(f.interfaces, identifier)
	}
	page := f.editPage(t, "/canvas/1/main")
	id := importText(t, f, page, worked)
	if id == 0 {
		t.Fatalf("the worked example was not stored:\n%s", page.Outline())
	}
	setGridWidth(page, 1600)
	var rows []string
	if err := json.Unmarshal([]byte(page.Run(`JSON.stringify(document.querySelectorAll("tr[data-report-path]").map(function (row) {
  return row.getAttribute("data-report-condition") + " " + row.getAttribute("data-report-path"); }))`).String()), &rows); err != nil {
		t.Fatal(err)
	}
	sort.Strings(rows)
	sort.Strings(want)
	if strings.Join(rows, "\n") != strings.Join(want, "\n") {
		t.Errorf("the import report lists\n%s\nnot\n%s", strings.Join(rows, "\n"), strings.Join(want, "\n"))
	}
	if page.Count("tr.group-row") < 1 {
		t.Errorf("the report is not grouped by canvas")
	}
	for _, card := range cards {
		if page.Count(fmt.Sprintf(`section[data-widget-card="%s"] div[data-condition="unresolved_reference"]`, card)) != 1 {
			t.Errorf("%s does not keep its card with the unresolved panel", card)
		}
	}
	page.Click("#rail-editor")
	page.Click("#dock-tab-document")
	page.Click("#syntax-yaml")
	marked := page.Run(`JSON.stringify(document.querySelectorAll("#dock-ghost .code-line").filter(function (line) {
  return line.getAttribute("class").indexOf("is-unresolved") >= 0;
}).map(function (line) { return Number(line.getAttribute("data-line")) - 1; }))`).String()
	paths := make([]string, len(want))
	for index, row := range want {
		paths[index] = strings.TrimPrefix(row, "unresolved_reference ")
	}
	encodedPaths, _ := json.Marshal(paths)
	expected := page.Run(`(function () {
  var lines = opnview.document.serialise(opnview.editing.document(` + fmt.Sprint(id) + `), "yaml").lines;
  return JSON.stringify(` + string(encodedPaths) + `.map(function (path) { return opnview.document.lineOf(lines, path); })
    .sort(function (a, b) { return a - b; }));
})()`).String()
	if marked != expected {
		t.Errorf("the code view marks lines %s, the unresolved references are on %s", marked, expected)
	}
	notices := page.Run(`JSON.stringify(document.querySelectorAll("#dock-ghost .code-notice").map(function (n) { return n.textContent; }))`).String()
	for identifier := range missing {
		for _, part := range []string{"interface", "identifier", identifier} {
			if !strings.Contains(notices, part) {
				t.Errorf("the line notices do not name %s: %s", part, notices)
			}
		}
	}
	for _, label := range labels {
		if !strings.Contains(notices, label) {
			t.Errorf("the line notices do not name the exported label %s: %s", label, notices)
		}
	}
	for _, syntax := range []string{"json", "yaml"} {
		back := page.Run(fmt.Sprintf(`(function () {
  var file = opnview.editing.exported(opnview.editing.document(%d), %q);
  return JSON.stringify(opnview.document.parse(file.text, %q).document);
})()`, id, syntax, syntax)).String()
		if compactJSON(t, back) != compactJSON(t, worked) {
			t.Errorf("exporting the worked example in %s changed it:\n%s", syntax, back)
		}
	}
}
