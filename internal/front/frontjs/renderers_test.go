package frontjs_test

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The renderers, drawn on the recorded answers (F7-AC17, F7-AC18, F7-AC21, F7-AC22): every served
// type draws every recorded answer that carries data; every rendering-class parameter changes the
// drawing for each value it offers (the drawn-parameter check) and is read by its renderer (the
// inert-parameter check); every chart series is straight between its samples and states how many it
// drew; every volume figure states its measure; the attribution method is shown per record.

// TestEveryServedTypeHasARendererAndARecordedAnswerAndDrawsEveryOne: a served type with no recorded
// answer fails here; so does a type whose renderer throws on one of its answers. A spread of five
// recorded variants of each type is drawn, within the front's test budget.
func TestEveryServedTypeHasARendererAndARecordedAnswerAndDrawsEveryOne(t *testing.T) {
	t.Parallel()
	page := drawingPage(t)
	answers := recordedAnswers(t)
	drawn := 0
	for _, widget := range builtinWidgets(t) {
		if widget.Renderer == nil {
			t.Errorf("%s has no renderer of its own: its payload is not its shape's contract", widget.Type)
			continue
		}
		found := spread(drawable(answers[widget.Type]), 5)
		if len(found) == 0 {
			t.Errorf("%s has no recorded answer carrying data", widget.Type)
			continue
		}
		setJSON(t, page, "__row", rowOf(t, widget))
		for index, answer := range found {
			setJSON(t, page, "__answer", string(answer.Raw))
			// Every answer is drawn; the first of each type is also applied to the document.
			call := "__draw"
			if index == 0 {
				call = "__render"
			}
			message := page.Run(`(function () { try { ` + call + `(__row, __answer); return ""; } catch (e) { return String(e && e.stack || e); } })()`).String()
			if message != "" {
				t.Errorf("%s %s: %s", widget.Type, answer.Parameters, message)
				continue
			}
			drawn++
		}
	}
	if drawn < 80 {
		t.Fatalf("only %d answers were drawn", drawn)
	}
}

// offeredValues are the values a rendering-class parameter is drawn with: each it offers.
func offeredValues(parameter manifestParameter) []any {
	switch parameter.Kind {
	case "boolean":
		return []any{true, false}
	case "enum":
		out := []any{}
		for _, value := range parameter.Values {
			out = append(out, value)
		}
		return out
	case "enum_set":
		out := []any{[]string{}}
		for _, value := range parameter.Values {
			out = append(out, []string{value})
		}
		return append(out, parameter.Values)
	case "object":
		return []any{nil, map[string]any{"left": map[string]any{"scale": "log", "unit": "bytes"},
			"right": map[string]any{"scale": "linear", "unit": "bits_per_second"}}}
	}
	return nil
}

// TestEveryRenderingParameterChangesTheDrawing: for every rendering-class parameter of every type,
// every value it offers draws a different tree, and an absent one draws as its default.
func TestEveryRenderingParameterChangesTheDrawing(t *testing.T) {
	t.Parallel()
	page := drawingPage(t)
	checked := 0
	for _, widget := range builtinWidgets(t) {
		answer := firstDrawable(t, widget.Type)
		setJSON(t, page, "__row", rowOf(t, widget))
		setJSON(t, page, "__answer", string(answer.Raw))
		for _, parameter := range widget.Parameters {
			if parameter.Class != "rendering" {
				continue
			}
			seen := map[string]any{}
			var defaultTree string
			for _, value := range offeredValues(parameter) {
				encoded, _ := json.Marshal(value)
				tree := page.Run(fmt.Sprintf(`(function () { var p = JSON.parse(JSON.stringify(__answer.parameters || {}));
p[%q] = %s; return __plain(__draw(__row, __answer, p).tree); })()`, parameter.Key, encoded)).String()
				for other, otherValue := range seen {
					if other == tree {
						t.Errorf("%s: %s = %s draws the same tree as %v", widget.Type, parameter.Key, encoded, otherValue)
					}
				}
				seen[tree] = string(encoded)
				defaultEncoded, _ := json.Marshal(parameter.Default)
				if string(encoded) == string(defaultEncoded) {
					defaultTree = tree
				}
				checked++
			}
			absent := page.Run(fmt.Sprintf(`(function () { var p = JSON.parse(JSON.stringify(__answer.parameters || {}));
delete p[%q]; return __plain(__draw(__row, __answer, p).tree); })()`, parameter.Key)).String()
			if defaultTree != "" && absent != defaultTree {
				t.Errorf("%s: an absent %s does not draw as its default", widget.Type, parameter.Key)
			}
		}
	}
	if checked < 20 {
		t.Fatalf("only %d values were drawn", checked)
	}
}

// TestEveryRenderingParameterIsReadByItsRenderer: the inert-parameter check -- a rendering-class key
// its renderer never names is a control that changes nothing.
func TestEveryRenderingParameterIsReadByItsRenderer(t *testing.T) {
	t.Parallel()
	for _, widget := range builtinWidgets(t) {
		for _, parameter := range widget.Parameters {
			if parameter.Class != "rendering" {
				continue
			}
			if !regexp.MustCompile(`(["'.])` + regexp.QuoteMeta(parameter.Key) + `\b`).MatchString(widget.RendererText) {
				t.Errorf("%s's renderer never reads its rendering parameter %s", widget.Type, parameter.Key)
			}
		}
	}
}

// pathCommands are the commands of an SVG path.
var pathCommands = regexp.MustCompile(`[A-Za-z]`)

// TestChartSeriesAreStraightAndStateTheirSamples: in every chart drawn, a series line is a path of M
// and L only (unless smooth is asked), each series element carries data-sample-count equal to the
// points it draws, and an area appears only under stacked.
func TestChartSeriesAreStraightAndStateTheirSamples(t *testing.T) {
	t.Parallel()
	page := drawingPage(t)
	lines := 0
	for _, widget := range builtinWidgets(t) {
		if widget.Shape != "time_series" && !strings.Contains(widget.RendererText, "kit.chart(") &&
			!strings.Contains(widget.RendererText, "kit.spark(") {
			continue
		}
		setJSON(t, page, "__row", rowOf(t, widget))
		for _, answer := range spread(drawable(recordedAnswers(t)[widget.Type]), 3) {
			setJSON(t, page, "__answer", string(answer.Raw))
			report := page.Run(`(function () {
  __render(__row, __answer);
  var out = { lines: [], dots: [], areas: 0 };
  __host.querySelectorAll("path").forEach(function (path) {
    var cls = path.getAttribute("class") || "";
    if (cls.indexOf("series-line") >= 0) { out.lines.push([path.getAttribute("d"), path.getAttribute("data-sample-count")]); }
    if (cls.split(" ").indexOf("area") >= 0) { out.areas++; }
  });
  __host.querySelectorAll("g").forEach(function (g) {
    if ((g.getAttribute("class") || "").indexOf("sample-dots") >= 0) {
      out.dots.push([g.childNodes.length, g.getAttribute("data-sample-count")]);
    }
  });
  return JSON.stringify(out);
})()`).String()
			var found struct {
				Lines [][2]*string `json:"lines"`
				Dots  [][2]any     `json:"dots"`
				Areas int          `json:"areas"`
			}
			if err := json.Unmarshal([]byte(report), &found); err != nil {
				t.Fatal(err)
			}
			parameters, _ := answer.Answer["parameters"].(map[string]any)
			smooth := parameters != nil && parameters["line_interpolation"] == "smooth"
			for _, line := range found.Lines {
				lines++
				if line[0] == nil || line[1] == nil {
					t.Errorf("%s %s: a series line without its path or its sample count", widget.Type, answer.Parameters)
					continue
				}
				commands := pathCommands.FindAllString(*line[0], -1)
				count, _ := strconv.Atoi(*line[1])
				if !smooth {
					for _, command := range commands {
						if command != "M" && command != "L" {
							t.Errorf("%s %s: a series line holds %s", widget.Type, answer.Parameters, command)
							break
						}
					}
					if count != len(commands) {
						t.Errorf("%s %s: a series line draws %d points and says %d", widget.Type, answer.Parameters, len(commands), count)
					}
				}
			}
			for _, dots := range found.Dots {
				if fmt.Sprint(dots[0]) != fmt.Sprint(dots[1]) {
					t.Errorf("%s %s: a sample group draws %v marks and says %v", widget.Type, answer.Parameters, dots[0], dots[1])
				}
			}
			if found.Areas > 0 && (parameters == nil || parameters["stacked"] != true) && widget.Type != "custom_chart" {
				t.Errorf("%s %s: an area is drawn while nothing is stacked", widget.Type, answer.Parameters)
			}
		}
	}
	if lines < 10 {
		t.Fatalf("only %d series lines were drawn", lines)
	}
}

// TestASmoothCurvePassesThroughEverySample: the kit's smooth curve ends each curve piece on the next
// measured point, so no sample is moved and none is dropped.
func TestASmoothCurvePassesThroughEverySample(t *testing.T) {
	t.Parallel()
	page := drawingPage(t)
	result := page.Run(`(function () {
  var points = [[0, 10], [10, 30], [20, 5], [30, 25], [40, 40]];
  var d = opnview.kit.smoothPath(points);
  var ends = d.split(/[MC]/).filter(Boolean).map(function (part) {
    var numbers = part.trim().split(/\s+/).map(Number);
    return numbers.slice(-2).join(",");
  });
  return JSON.stringify({ ends: ends, want: points.map(function (p) { return p.join(","); }) });
})()`).String()
	var got struct{ Ends, Want []string }
	if err := json.Unmarshal([]byte(result), &got); err != nil {
		t.Fatal(err)
	}
	if strings.Join(got.Ends, " ") != strings.Join(got.Want, " ") {
		t.Errorf("the smooth curve ends its pieces on %v, not on the samples %v", got.Ends, got.Want)
	}
}

// TestRankedBarsAndTheirRemainderSumToTheTotal (F7-AC19).
func TestRankedBarsAndTheirRemainderSumToTheTotal(t *testing.T) {
	t.Parallel()
	page := drawingPage(t)
	checked := 0
	for _, widget := range builtinWidgets(t) {
		if widget.Shape != "ranked_bars" {
			continue
		}
		setJSON(t, page, "__row", rowOf(t, widget))
		for _, answer := range spread(drawable(recordedAnswers(t)[widget.Type]), 8) {
			setJSON(t, page, "__answer", string(answer.Raw))
			sums := page.Run(`(function () {
  __render(__row, __answer);
  var bars = 0, remainder = 0, total = null;
  __host.querySelectorAll("[data-bar]").forEach(function (el) { bars += Number(el.getAttribute("data-value") || 0); });
  __host.querySelectorAll(".bar-row").forEach(function (el) {
    if (el.getAttribute("data-remainder") !== null || el.getAttribute("data-excluded") !== null) {
      remainder += Number(el.getAttribute("data-value") || 0);
    }
    if (el.getAttribute("data-total") !== null) { total = Number(el.getAttribute("data-total")); }
  });
  return JSON.stringify([bars, remainder, total]);
})()`).String()
			var got [3]*float64
			if err := json.Unmarshal([]byte(sums), &got); err != nil {
				t.Fatal(err)
			}
			if got[2] == nil {
				t.Errorf("%s %s: no total row", widget.Type, answer.Parameters)
				continue
			}
			if *got[0]+*got[1] != *got[2] {
				t.Errorf("%s %s: bars %v plus remainder %v is not the total %v", widget.Type, answer.Parameters, *got[0], *got[1], *got[2])
			}
			checked++
		}
	}
	if checked < 8 {
		t.Fatalf("only %d rankings were checked", checked)
	}
}

// TestTheMapsPlacedAndUnplacedMakeTheTotal (F7-AC19): placed plus unplaced is the total, and the
// unplaced counter is drawn at zero too.
func TestTheMapsPlacedAndUnplacedMakeTheTotal(t *testing.T) {
	t.Parallel()
	page := drawingPage(t)
	checked := 0
	for _, widget := range builtinWidgets(t) {
		if widget.Shape != "map" {
			continue
		}
		setJSON(t, page, "__row", rowOf(t, widget))
		for _, answer := range spread(drawable(recordedAnswers(t)[widget.Type]), 6) {
			setJSON(t, page, "__answer", string(answer.Raw))
			for _, emptied := range []bool{false, true} {
				// Emptied: every unplaced row at zero, the counter still drawn.
				page.Run(fmt.Sprintf(`var __map = JSON.parse(JSON.stringify(__answer));
if (%t) { (__map.data.unplaced || []).forEach(function (row) { row.addresses = 0;
  Object.keys(row.volume).forEach(function (key) { if (typeof row.volume[key] === "number") { row.volume[key] = 0; } }); }); }`, emptied))
				report := page.Run(`(function () {
  __render(__row, __map);
  var reconcile = __host.querySelector(".map-reconcile");
  var counter = __host.querySelector("[data-unplaced]");
  if (!reconcile) { return JSON.stringify({ missing: "map-reconcile" }); }
  return JSON.stringify({ placed: Number(reconcile.getAttribute("data-placed")), unplaced: Number(reconcile.getAttribute("data-unplaced")),
    total: Number(reconcile.getAttribute("data-total")), below: Number(reconcile.getAttribute("data-below") || 0), counter: !!counter });
})()`).String()
				var got struct {
					Missing                        string
					Placed, Unplaced, Total, Below float64
					Counter                        bool
				}
				if err := json.Unmarshal([]byte(report), &got); err != nil {
					t.Fatal(err)
				}
				if got.Missing != "" {
					t.Fatalf("%s: no element of class %s", widget.Type, got.Missing)
				}
				if !got.Counter {
					t.Errorf("%s %s (emptied %t): the unplaced counter is not drawn", widget.Type, answer.Parameters, emptied)
				}
				if !emptied && got.Placed+got.Unplaced+got.Below != got.Total {
					t.Errorf("%s %s: placed %v + unplaced %v + below %v is not the total %v", widget.Type, answer.Parameters,
						got.Placed, got.Unplaced, got.Below, got.Total)
				}
				checked++
			}
		}
	}
	if checked < 10 {
		t.Fatalf("only %d maps were checked", checked)
	}
}

// TestTheConnectionTreeReconcilesOnBothSides (F7-AC19, the mockup's tree-reconciliation check
// ported): over every recorded combination of root, depth, measure, limit_per_level, sort and
// include_blocked, the outside's first level sums to the answer's total and every node's children
// sum to the node.
func TestTheConnectionTreeReconcilesOnBothSides(t *testing.T) {
	t.Parallel()
	page := drawingPage(t)
	var widget builtinWidget
	for _, candidate := range builtinWidgets(t) {
		if candidate.Type == "connection_tree" {
			widget = candidate
		}
	}
	setJSON(t, page, "__row", rowOf(t, widget))
	combinations := 0
	for _, answer := range drawable(recordedAnswers(t)["connection_tree"]) {
		setJSON(t, page, "__answer", string(answer.Raw))
		report := page.Run(`(function () {
  __render(__row, __answer);
  var nodes = __host.querySelectorAll("[data-tree-node]").map(function (el) {
    return { side: el.getAttribute("data-side"), level: Number(el.getAttribute("data-level")), key: el.getAttribute("data-node"),
      parent: el.getAttribute("data-parent") || "", value: Number(el.getAttribute("data-value")) };
  });
  return JSON.stringify({ nodes: nodes, total: __answer.data.total.value });
})()`).String()
		var got struct {
			Nodes []struct {
				Side, Key, Parent string
				Level             int
				Value             float64
			}
			Total float64
		}
		if err := json.Unmarshal([]byte(report), &got); err != nil {
			t.Fatal(err)
		}
		if len(got.Nodes) == 0 {
			t.Fatalf("%s: no element carries data-tree-node", answer.Parameters)
		}
		combinations++
		for _, side := range []string{"inside", "outside"} {
			first := 0.0
			children := map[string]float64{}
			values := map[string]float64{}
			for _, node := range got.Nodes {
				if node.Side != side {
					continue
				}
				values[node.Key] = node.Value
				if node.Level == 1 {
					first += node.Value
				} else {
					children[node.Parent] += node.Value
				}
			}
			if side == "outside" && first != got.Total {
				t.Errorf("%s: the outside's first level draws %v of the total %v", answer.Parameters, first, got.Total)
			}
			parents := make([]string, 0, len(children))
			for parent := range children {
				parents = append(parents, parent)
			}
			sort.Strings(parents)
			for _, parent := range parents {
				if children[parent] != values[parent] {
					t.Errorf("%s: %s's children on the %s sum to %v, not %v", answer.Parameters, parent, side, children[parent], values[parent])
				}
			}
		}
	}
	if combinations < 10 {
		t.Fatalf("only %d combinations were checked", combinations)
	}
}

// TestEveryVolumeFigureStatesItsMeasureAndTwoAreNeverSummed (F7-AC21): every figure node a renderer
// draws carries its measure; a netflow answer and a logged one draw distinguishable measure chips;
// the kit refuses to add two measures.
func TestEveryVolumeFigureStatesItsMeasureAndTwoAreNeverSummed(t *testing.T) {
	t.Parallel()
	page := drawingPage(t)
	for _, widget := range builtinWidgets(t) {
		setJSON(t, page, "__row", rowOf(t, widget))
		answer := firstDrawable(t, widget.Type)
		setJSON(t, page, "__answer", string(answer.Raw))
		bare := page.Run(`(function () {
  __render(__row, __answer);
  return __host.querySelectorAll(".fig").filter(function (el) {
    return !el.getAttribute("data-measure") && (el.getAttribute("class") || "").indexOf("fig-derived") < 0; }).length;
})()`).ToInteger()
		if bare > 0 {
			t.Errorf("%s draws %d figures that do not state their measure", widget.Type, bare)
		}
	}
	got := page.Run(`(function () {
  var kit = opnview.kit.create({ t: opnview.strings.t, tp: opnview.strings.tp, tk: opnview.strings.tk, td: opnview.strings.td,
    format: opnview.format });
  var logged = { measure: "logged", lower_bound: true, value: 10 }, netflow = { measure: "netflow", lower_bound: true, value: 20 };
  var host = document.createElement("div");
  opnview.view.render(host, [kit.h("div", {}, kit.measures([logged, netflow]))]);
  var chips = host.querySelectorAll("[data-measure-chip]").map(function (el) { return el.getAttribute("class"); });
  return JSON.stringify({ sum: kit.add(logged, netflow), same: kit.add(logged, logged), chips: chips });
})()`).String()
	var result struct {
		Sum   *float64
		Same  float64
		Chips []string
	}
	if err := json.Unmarshal([]byte(got), &result); err != nil {
		t.Fatal(err)
	}
	if result.Sum != nil {
		t.Errorf("a logged and a netflow figure were added into %v", *result.Sum)
	}
	if result.Same != 20 {
		t.Errorf("two logged figures were added into %v", result.Same)
	}
	if len(result.Chips) != 2 || result.Chips[0] == result.Chips[1] {
		t.Errorf("a logged and a netflow figure draw chips that cannot be told apart: %v", result.Chips)
	}
	// A whole answer whose total is in another measure than its rows is drawn with both chips, and its
	// total is the answer's own, never a sum of the two.
	for _, widget := range builtinWidgets(t) {
		if widget.Type != "interface_volume_ranking" {
			continue
		}
		setJSON(t, page, "__row", rowOf(t, widget))
		setJSON(t, page, "__answer", string(firstDrawable(t, widget.Type).Raw))
		mixed := page.Run(`(function () {
  var answer = JSON.parse(JSON.stringify(__answer));
  answer.data.total.measure = "netflow";
  __render(__row, answer);
  var total = __host.querySelector("[data-total]");
  return JSON.stringify({ total: total ? Number(total.getAttribute("data-total")) : null, want: answer.data.total.bytes,
    measures: __host.querySelectorAll(".fig").map(function (el) { return el.getAttribute("data-measure"); }) });
})()`).String()
		var drawnMixed struct {
			Total, Want *float64
			Measures    []string
		}
		if err := json.Unmarshal([]byte(mixed), &drawnMixed); err != nil {
			t.Fatal(err)
		}
		if drawnMixed.Total == nil || drawnMixed.Want == nil || *drawnMixed.Total != *drawnMixed.Want {
			t.Errorf("a total in another measure is drawn as %v, not as the answer's %v", drawnMixed.Total, drawnMixed.Want)
		}
		if !strings.Contains(strings.Join(drawnMixed.Measures, " "), "netflow") {
			t.Errorf("a netflow total does not state its measure: %v", drawnMixed.Measures)
		}
	}
}

// TestTheAttributionMethodIsShownPerRecord (F7-AC22): client_traffic_detail draws method and
// correlation delay per attributed record; top_sites draws each row's methods.
func TestTheAttributionMethodIsShownPerRecord(t *testing.T) {
	t.Parallel()
	page := drawingPage(t)
	for _, widget := range builtinWidgets(t) {
		switch widget.Type {
		case "client_traffic_detail":
			setJSON(t, page, "__row", rowOf(t, widget))
			setJSON(t, page, "__answer", string(firstDrawable(t, widget.Type).Raw))
			text := page.Run(`(function () {
  var answer = JSON.parse(JSON.stringify(__answer));
  answer.data.records[0].site_name = "www.example.com";
  answer.data.records[0].method = "lookup_timing";
  answer.data.records[0].correlation_delay_seconds = 2;
  __render(__row, answer);
  var row = __host.querySelectorAll("[data-method]");
  return JSON.stringify({ methods: row.map(function (el) { return el.getAttribute("data-method"); }),
    text: __host.textContent, method: opnview.strings.t("method.lookup_timing"), delay: opnview.format.duration(2) });
})()`).String()
			var got struct {
				Methods             []string
				Text, Method, Delay string
			}
			if err := json.Unmarshal([]byte(text), &got); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(strings.Join(got.Methods, " "), "lookup_timing") || !strings.Contains(got.Text, got.Method) {
				t.Errorf("client_traffic_detail does not draw a record's method: %v", got.Methods)
			}
			if !strings.Contains(got.Text, got.Delay) {
				t.Errorf("client_traffic_detail does not draw a record's correlation delay %q", got.Delay)
			}
		case "top_sites":
			setJSON(t, page, "__row", rowOf(t, widget))
			setJSON(t, page, "__answer", string(firstDrawable(t, widget.Type).Raw))
			methods := page.Run(`(function () { __render(__row, __answer);
  return __host.querySelectorAll("[data-row] [data-method]").length; })()`).ToInteger()
			if methods == 0 {
				t.Error("top_sites does not draw its rows' methods")
			}
		}
	}
}

// spread is at most n answers of a list, taken evenly across it, the first always among them: the
// checks that walk a whole drawing read a spread of the recorded variants, the test that draws them
// all reads every one.
func spread(answers []recordedAnswer, n int) []recordedAnswer {
	if len(answers) <= n {
		return answers
	}
	out := make([]recordedAnswer, 0, n)
	for index := 0; index < n; index++ {
		out = append(out, answers[index*len(answers)/n])
	}
	return out
}
