package frontjs_test

import (
	"strings"
	"testing"

	"github.com/NaejEL/opnview/internal/front/frontjs"
)

// The reconciler (A6): applying the same tree twice creates and removes nothing, a changed figure is
// updated in place with the node kept, and a refresh leaves alone a subtree holding the focus on a
// field, a form holding unapplied values and a region that manages itself -- and patches them once
// the reader leaves.

const reconcilerSetup = `
var h = opnview.h;
var host = document.createElement("div");
document.body.appendChild(host);
function tree(figure, label) {
  return [
    h("section", { key: "card", "class": "card" }, [
      h("h2", {}, label),
      h("span", { key: "figure", "class": "figure" }, figure),
      h("form", { key: "form", "data-dirty": "false" }, [
        h("input", { key: "name", id: "name", value: "kept" })
      ])
    ]),
    h("ul", { key: "list" }, [1, 2, 3].map(function (n) { return h("li", { key: "item-" + n }, String(n)); }))
  ];
}
`

func reconcilerPage(t *testing.T) *frontjs.Runtime {
	page := frontjs.New(t, frontjs.Options{})
	page.Run(reconcilerSetup)
	return page
}

func TestApplyingTheSameTreeTwiceCreatesAndRemovesNothing(t *testing.T) {
	t.Parallel()
	page := reconcilerPage(t)
	page.Run(`opnview.view.render(host, tree("1 024", "Title"));`)
	created := page.Run(`__dom.created`).ToInteger()
	removed := page.Run(`__dom.removed`).ToInteger()
	page.Run(`opnview.view.render(host, tree("1 024", "Title"));`)
	if page.Run(`__dom.created`).ToInteger() != created || page.Run(`__dom.removed`).ToInteger() != removed {
		t.Errorf("a second identical render created %d and removed %d nodes",
			page.Run(`__dom.created`).ToInteger()-created, page.Run(`__dom.removed`).ToInteger()-removed)
	}
}

func TestAChangedFigureIsUpdatedInPlace(t *testing.T) {
	t.Parallel()
	page := reconcilerPage(t)
	page.Run(`opnview.view.render(host, tree("1 024", "Title")); var before = document.querySelector("span.figure");
var beforeText = before.firstChild;`)
	page.Run(`opnview.view.render(host, tree("2 048", "Title"), { refresh: true });`)
	if !page.Run(`document.querySelector("span.figure") === before && before.firstChild === beforeText`).ToBoolean() {
		t.Error("the figure's node was replaced rather than updated")
	}
	if got := page.Text("span.figure"); got != "2 048" {
		t.Errorf("the figure reads %q", got)
	}
}

func TestARefreshLeavesAFocusedFieldAndADirtyFormAloneUntilTheyAreLeft(t *testing.T) {
	t.Parallel()
	page := reconcilerPage(t)
	page.Run(`opnview.view.render(host, tree("1", "Title"));`)
	// A reader types in the field: the refresh must not write over what they typed.
	page.Input("#name", "typed by the reader")
	page.Run(`
var next = tree("2", "Title");
next[0].children[2].children[0].props.value = "from the server";
opnview.view.render(host, next, { refresh: true });`)
	if got := page.Run(`document.getElementById("name").value`).String(); got != "typed by the reader" {
		t.Errorf("a refresh wrote %q over the field the reader is typing in", got)
	}
	if got := page.Text("span.figure"); got != "2" {
		t.Errorf("the figure beside the focused field was not refreshed: %q", got)
	}
	if !page.Run(`opnview.view.takeDeferred()`).ToBoolean() {
		t.Error("the refresh did not record that a subtree waits")
	}

	// A form holding unapplied values is left alone by a refresh, focused or not.
	page.Run(`document.getElementById("name").blur();
var dirty = tree("3", "Title");
dirty[0].children[2].attrs["data-dirty"] = "true";
opnview.view.render(host, dirty);
var refreshed = tree("4", "Title");
refreshed[0].children[2].attrs["data-dirty"] = "true";
refreshed[0].children[2].children[0].props.value = "from the server";
opnview.view.render(host, refreshed, { refresh: true });`)
	if got := page.Run(`document.getElementById("name").value`).String(); got == "from the server" {
		t.Error("a refresh patched a form holding unapplied values")
	}
	// The reader's own render applies.
	page.Run(`opnview.view.render(host, refreshed);`)
	if got := page.Run(`document.getElementById("name").value`).String(); got != "from the server" {
		t.Errorf("a render answering the reader's action did not apply: %q", got)
	}
}

func TestATreeRefusesHandlerStringsAndStyleAttributes(t *testing.T) {
	t.Parallel()
	page := reconcilerPage(t)
	for _, attribute := range []string{`onclick: "x()"`, `style: "color: red"`} {
		message := page.Run(`(function () { try { opnview.h("div", { ` + attribute + ` }); return ""; }
catch (e) { return String(e.message); } })()`).String()
		if !strings.Contains(message, "refused") {
			t.Errorf("the attribute %s was accepted", attribute)
		}
	}
}

// A duration is spelled exactly, each unit with its figure: 410 seconds is 6 minutes 50 seconds.
func TestADurationIsSpelledWithItsFigures(t *testing.T) {
	t.Parallel()
	page := frontjs.New(t, frontjs.Options{})
	page.Run(`opnview.strings.install("en", {}, {"unit.minute.one": "minute", "unit.minute.other": "minutes",
  "unit.second.one": "second", "unit.second.other": "seconds", "unit.day.one": "day", "unit.day.other": "days"});`)
	for seconds, want := range map[string]string{"410": "6 minutes 50 seconds", "86400": "1 day", "0": "0 seconds"} {
		if got := page.Run(`opnview.format.duration(` + seconds + `)`).String(); got != want {
			t.Errorf("%s seconds is spelled %q, want %q", seconds, got, want)
		}
	}
}

// TestARefreshLeavesASelectionAloneUntilItCollapses (F7-AC23): a MAC the reader selected in a table
// stays selected -- its text node kept -- across a refresh, and is patched once the selection
// collapses.
func TestARefreshLeavesASelectionAloneUntilItCollapses(t *testing.T) {
	t.Parallel()
	page := reconcilerPage(t)
	page.Run(`
function table(mac, figure) {
  return [h("table", { key: "t" }, h("tbody", {}, [
    h("tr", { key: "r1" }, [h("td", { key: "mac", "class": "mac" }, mac), h("td", { key: "n", "class": "n" }, figure)]),
    h("tr", { key: "r2" }, [h("td", { key: "other", "class": "other" }, figure)])
  ]))];
}
opnview.view.render(host, table("02:00:5e:10:00:01", "1"));
var macText = document.querySelector("td.mac").firstChild;
__selection = { rangeCount: 1, isCollapsed: false, anchorNode: macText, focusNode: macText };
opnview.view.render(host, table("02:00:5e:10:00:99", "2"), { refresh: true });`)
	if !page.Run(`document.querySelector("td.mac").firstChild === macText`).ToBoolean() ||
		page.Text("td.mac") != "02:00:5e:10:00:01" {
		t.Errorf("a refresh patched the selected cell: %q", page.Text("td.mac"))
	}
	if page.Text("td.other") != "2" {
		t.Errorf("the rest of the table was not refreshed: %q", page.Text("td.other"))
	}
	page.Run(`__selection = { rangeCount: 0, isCollapsed: true, anchorNode: null };
opnview.view.render(host, table("02:00:5e:10:00:99", "2"), { refresh: true });`)
	if page.Text("td.mac") != "02:00:5e:10:00:99" {
		t.Errorf("the cell was not patched once the selection collapsed: %q", page.Text("td.mac"))
	}
}
