package frontjs_test

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/NaejEL/opnview/internal/front/frontjs"
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
  var out = { lines: [], dots: [], areas: 0, stacked: 0 };
  __host.querySelectorAll("path").forEach(function (path) {
    var cls = path.getAttribute("class") || "";
    if (cls.indexOf("series-line") >= 0) { out.lines.push([path.getAttribute("d"), path.getAttribute("data-sample-count")]); }
    if (cls.split(" ").indexOf("area") >= 0) {
      var svg = path;
      while (svg && svg.tagName !== "SVG") { svg = svg.parentNode; }
      if (!svg || svg.getAttribute("data-stacked") !== "true") { out.areas++; }
    }
  });
  out.stacked = __host.querySelectorAll('svg[data-stacked="true"]').length;
  __host.querySelectorAll("g").forEach(function (g) {
    if ((g.getAttribute("class") || "").indexOf("sample-dots") >= 0) {
      out.dots.push([g.childNodes.length, g.getAttribute("data-sample-count")]);
    }
  });
  return JSON.stringify(out);
})()`).String()
			var found struct {
				Lines   [][2]*string `json:"lines"`
				Dots    [][2]any     `json:"dots"`
				Areas   int          `json:"areas"`
				Stacked int          `json:"stacked"`
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
			// An area separates stacked series and nothing else: none is drawn in a chart that stacks
			// nothing, and a widget whose stacking is a parameter stacks only when it is asked to. (The
			// recent strip of traffic_composition stacks its slices by design, as the mockup draws it.)
			if found.Areas > 0 {
				t.Errorf("%s %s: %d areas are drawn in a chart that stacks nothing", widget.Type, answer.Parameters, found.Areas)
			}
			declaresStacked := false
			for _, parameter := range widget.Parameters {
				declaresStacked = declaresStacked || parameter.Key == "stacked"
			}
			if declaresStacked && found.Stacked > 0 && (parameters == nil || parameters["stacked"] != true) {
				t.Errorf("%s %s: a chart stacks while stacked is not asked for", widget.Type, answer.Parameters)
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
  __host.querySelectorAll("[data-remainder]").concat(__host.querySelectorAll("[data-excluded]"), __host.querySelectorAll("[data-total]")).forEach(function (el) {
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
    return { side: el.getAttribute("data-side"), level: Number(el.getAttribute("data-level")), key: el.getAttribute("data-path"),
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

// TestALogAxisChangesTheDrawingAndCountsEveryPoint: custom_chart's axes.<side>.scale is drawn, not
// stated -- the same recorded answer drawn on a log axis gives other series geometry, still of M and L
// only with every drawn point counted -- and a value a log axis cannot place, at or below zero, is not
// drawn and is counted under the chart, never moved to a made-up floor.
func TestALogAxisChangesTheDrawingAndCountsEveryPoint(t *testing.T) {
	t.Parallel()
	page := drawingPage(t)
	var chart builtinWidget
	for _, widget := range builtinWidgets(t) {
		if widget.Type == "custom_chart" {
			chart = widget
		}
	}
	setJSON(t, page, "__row", rowOf(t, chart))
	setJSON(t, page, "__answer", string(firstDrawable(t, "custom_chart").Raw))
	report := page.Run(`(function () {
  function draw(scale) {
    var parameters = JSON.parse(JSON.stringify(__answer.parameters || {}));
    parameters.axes = { left: { scale: scale }, right: { scale: scale } };
    __render(__row, __answer, parameters);
    var lines = [];
    __host.querySelectorAll("path").forEach(function (path) {
      if ((path.getAttribute("class") || "").indexOf("series-line") >= 0) {
        lines.push([path.getAttribute("d"), path.getAttribute("data-sample-count")]);
      }
    });
    var svg = __host.querySelector("svg.chart-svg");
    return { lines: lines, scale: svg ? svg.getAttribute("data-scale-left") : null };
  }
  return JSON.stringify({ linear: draw("linear"), log: draw("log") });
})()`).String()
	var drawn struct {
		Linear, Log struct {
			Lines [][2]string `json:"lines"`
			Scale string      `json:"scale"`
		}
	}
	if err := json.Unmarshal([]byte(report), &drawn); err != nil {
		t.Fatal(err)
	}
	if drawn.Linear.Scale != "linear" || drawn.Log.Scale != "log" {
		t.Errorf("the chart is drawn on %q and %q, not linear then log", drawn.Linear.Scale, drawn.Log.Scale)
	}
	if fmt.Sprint(drawn.Linear.Lines) == fmt.Sprint(drawn.Log.Lines) {
		t.Errorf("a log axis draws the same series as a linear one: %v", drawn.Log.Lines)
	}
	for _, line := range drawn.Log.Lines {
		commands := pathCommands.FindAllString(line[0], -1)
		for _, command := range commands {
			if command != "M" && command != "L" {
				t.Errorf("a series line on a log axis holds %s", command)
			}
		}
		if count, _ := strconv.Atoi(line[1]); count != len(commands) {
			t.Errorf("a series line on a log axis draws %d points and says %d", len(commands), count)
		}
	}

	// A series crossing zero: 1, 10, 100, 0, 1000, -5. The log axis draws 1, 10 and 100 as one line and
	// 1000 alone, and counts the two it cannot place.
	result := page.Run(`(function () {
  var kit = opnview.kit.create({ t: opnview.strings.t, tp: opnview.strings.tp, tk: opnview.strings.tk,
    td: opnview.strings.td, format: opnview.format, size: { width: 600, height: 200 }, prefix: "x" });
  var values = [1, 10, 100, 0, 1000, -5];
  var tree = kit.chart({ from: 0, to: 600, scale: { left: "log" }, width: 600, height: 200, series: [{ key: "s", label: "s",
    points: values.map(function (value, index) { return { from: index * 100, to: index * 100 + 100, value: value }; }) }] });
  opnview.view.render(__host, [tree]);
  var counted = 0, marks = 0, ys = [];
  __host.querySelectorAll("g").forEach(function (g) {
    if ((g.getAttribute("class") || "").indexOf("sample-dots") >= 0) {
      counted += Number(g.getAttribute("data-sample-count"));
      marks += g.childNodes.length;
      g.childNodes.forEach(function (dot) { ys.push(Number(dot.getAttribute("cy"))); });
    }
  });
  return JSON.stringify({ counted: counted, marks: marks, ys: ys, note: __host.textContent.indexOf("2 values at or below zero") >= 0 });
})()`).String()
	var crossing struct {
		Counted, Marks int
		Ys             []float64
		Note           bool
	}
	if err := json.Unmarshal([]byte(result), &crossing); err != nil {
		t.Fatal(err)
	}
	if crossing.Counted != 4 || crossing.Marks != 4 {
		t.Errorf("a log axis over 1, 10, 100, 0, 1000, -5 draws %d marks and counts %d, not the 4 it can place",
			crossing.Marks, crossing.Counted)
	}
	if len(crossing.Ys) == 4 {
		// A decade is the same height wherever it is: 1 to 10, 10 to 100 and 100 to 1000 are equal steps.
		first, second := crossing.Ys[0]-crossing.Ys[1], crossing.Ys[1]-crossing.Ys[2]
		if first <= 0 || first-second > 0.5 || second-first > 0.5 {
			t.Errorf("the decades are not equal steps up a log axis: %v", crossing.Ys)
		}
	}
	if !crossing.Note {
		t.Errorf("the two values a log axis cannot place are not counted under the chart")
	}
}

// TestARendererRegistersOnlyWhatItsURLOwns (A1's precedence, in the page): a script registers the
// type or the shape the URL it was loaded from names, and nothing else. A renderer of the data
// directory naming a built-in type or a shape, a second registration of a type already drawn, and a
// registration made from no loaded script are each refused, and the built-in draws as before.
func TestARendererRegistersOnlyWhatItsURLOwns(t *testing.T) {
	t.Parallel()
	page := drawingPage(t)
	before := page.Run(`opnview.widgets.renderer("top_sites", "ranked_bars") === opnview.widgets.renderer("top_sites", null) &&
  String(opnview.widgets.renderer("top_sites", null).render).length`).String()
	hijack := `opnview.widgets.register({ %s, render: function () { return opnview.h("p", {}, "HIJACKED"); } });`
	for _, attempt := range []struct{ name, url, definition, reason string }{
		{"another widget's directory naming a built-in type", "/front/widgets/example_operator/renderer.js",
			`type: "top_sites"`, "not_its_own_type"},
		{"a widget's directory naming a shape", "/front/widgets/example_operator/renderer.js",
			`shape: "ranked_bars"`, "not_its_own_shape"},
		{"the built-in's own directory, a second time", "/front/widgets/top_sites/other.js",
			`type: "top_sites"`, "already_registered"},
		{"a shape's file, a second time", "/front/shapes/ranked_bars.js", `shape: "ranked_bars"`, "already_registered"},
		{"a shape's file naming a type", "/front/shapes/ranked_bars.js", `type: "top_sites"`, "not_its_own_shape"},
		{"a file outside the published renderers", "/front/core/extra.js", `type: "top_sites"`, "not_loaded_by_the_core"},
	} {
		refused := int(page.Run(`opnview.widgets.refusedRegistrations().length`).ToInteger())
		page.RunAt(attempt.url, fmt.Sprintf(hijack, attempt.definition))
		reasons := page.Run(`JSON.stringify(opnview.widgets.refusedRegistrations().map(function (r) { return r.reason; }))`).String()
		var listed []string
		if err := json.Unmarshal([]byte(reasons), &listed); err != nil {
			t.Fatal(err)
		}
		if len(listed) != refused+1 || listed[refused] != attempt.reason {
			t.Errorf("%s: the refusals are %v, not one more of %s", attempt.name, listed, attempt.reason)
		}
	}
	page.Run(fmt.Sprintf(hijack, `type: "example_late"`))
	if page.Run(`opnview.widgets.hasOwn("example_late")`).ToBoolean() {
		t.Error("a registration made while no script was running was accepted")
	}
	page.RunAt("/front/widgets/example_operator/renderer.js", fmt.Sprintf(hijack, `type: "example_operator"`))
	if !page.Run(`opnview.widgets.hasOwn("example_operator")`).ToBoolean() {
		t.Error("a widget's renderer could not register its own type")
	}
	after := page.Run(`opnview.widgets.renderer("top_sites", "ranked_bars") === opnview.widgets.renderer("top_sites", null) &&
  String(opnview.widgets.renderer("top_sites", null).render).length`).String()
	if after != before {
		t.Error("a refused registration replaced the built-in renderer")
	}
	widget := builtinWidget{}
	for _, candidate := range builtinWidgets(t) {
		if candidate.Type == "top_sites" {
			widget = candidate
		}
	}
	setJSON(t, page, "__row", rowOf(t, widget))
	setJSON(t, page, "__answer", string(firstDrawable(t, "top_sites").Raw))
	if strings.Contains(page.Run(`__plain(__draw(__row, __answer).tree)`).String(), "HIJACKED") {
		t.Error("top_sites draws the hijacking renderer")
	}
}

// TestAFrontOnlyWidgetReadsOnlyTheThreeReadRoutes (A2): the reads a front-only widget may have the
// core perform are the three read routes as written -- the rolling strip is the live_strip widget, read
// through the first, and its old route is no read route -- and a dot component or an escape that a
// browser would resolve to another route is refused.
func TestAFrontOnlyWidgetReadsOnlyTheThreeReadRoutes(t *testing.T) {
	t.Parallel()
	page := frontjs.New(t, frontjs.Options{})
	for path, want := range map[string]bool{
		"/api/widget/top_sites?parameters=%7B%7D": true,
		"/api/datasets": true,
		"/api/datasets/dns/query?parameters=%7B%7D": true,
		"/api/datasets/example_peers.peer/query":    true,
		"/api/widget/live_strip":                    true,
		"/api/live/strip":                           false,
		"/api/datasets/../query":                    false,
		"/api/datasets/./query":                     false,
		"/api/datasets/dns/../../session/query":     false,
		"/api/datasets/%2e%2e/query":                false,
		"/api/datasets/dns%2fquery/query":           false,
		"/api/datasets/dns..x/query":                false,
		"/api/widget/../dashboards":                 false,
		"/api/dashboards":                           false,
		"/api/session/sign-out":                     false,
		"https://example.com/api/datasets":          false,
	} {
		if got := page.Run(fmt.Sprintf(`opnview.widgets.allowedRead(%q)`, path)).ToBoolean(); got != want {
			t.Errorf("allowedRead(%s) = %v, want %v", path, got, want)
		}
	}
}

// TestTheMatrixDrawsEachColumnOfItsAnswerOnce: the outside and the firewall itself are columns the
// answer lists; each recorded answer of the matrix draws exactly one column header per column.
func TestTheMatrixDrawsEachColumnOfItsAnswerOnce(t *testing.T) {
	t.Parallel()
	page := drawingPage(t)
	for _, widget := range builtinWidgets(t) {
		if widget.Type == "interface_traffic_matrix" {
			setJSON(t, page, "__row", rowOf(t, widget))
		}
	}
	for _, answer := range drawable(recordedAnswers(t)["interface_traffic_matrix"]) {
		setJSON(t, page, "__answer", string(answer.Raw))
		counts := page.Run(`(function () { __render(__row, __answer);
  return __host.querySelectorAll("th.colhead").length + "|" + (__answer.data.columns || []).length; })()`).String()
		drawn, listed, _ := strings.Cut(counts, "|")
		if drawn != listed {
			t.Errorf("%s: %s column headers for %s columns", answer.Parameters, drawn, listed)
		}
	}
}

// TestACustomChartGivesEachUnitItsAxisAndNamesWhatItDraws (the ergonomist's review of 17 October
// 2026): a series of another unit than its axis's moves to the free axis, and one no axis can carry
// is named under the chart; series and annotations are named by the catalogue, never by their codes;
// the annotations are a titled strip with a legend whose bars are as wide as their buckets; no axis
// repeats a tick label.
func TestACustomChartGivesEachUnitItsAxisAndNamesWhatItDraws(t *testing.T) {
	t.Parallel()
	page := drawingPage(t)
	for _, widget := range builtinWidgets(t) {
		if widget.Type == "custom_chart" {
			setJSON(t, page, "__row", rowOf(t, widget))
		}
	}
	const hour = 3600
	from := 1790000000 - 24*hour
	buckets := func(values ...float64) []any {
		out := []any{}
		for index, value := range values {
			out = append(out, map[string]any{"from": from + index*hour, "to": from + (index+1)*hour, "gap": false, "value": value})
		}
		return out
	}
	series := func(key, source, figure, unit, measure string, values ...float64) map[string]any {
		figureKey := "measure"
		if source == "firewall_metric" {
			figureKey = "metric"
		}
		return map[string]any{"key": key, "condition": "reachable", "unit": unit, "measure": measure, "aggregation": "mean",
			"parameters": map[string]any{"source": source, figureKey: figure}, "buckets": buckets(values...), "total": nil}
	}
	answer := map[string]any{"type": "custom_chart", "condition": "reachable",
		"window":     map[string]any{"requested": map[string]any{"from": from, "to": from + 24*hour}},
		"parameters": map[string]any{"legend": "table", "tooltip": "single"},
		"data": map[string]any{
			"series": []any{
				series("s0", "traffic", "bytes", "bytes", "logged", 1000, 2000, 3000),
				series("s1", "firewall_metric", "cpu", "ratio", "sampled_gauge", 0.2, 0.4, 0.3),
				series("s2", "dns", "lookups", "lookups", "dns", 4, 5, 6),
			},
			"annotations": []any{map[string]any{"kind": "security_events", "buckets": []any{
				map[string]any{"from": from, "to": from + hour, "gap": false, "value": 1},
				map[string]any{"from": from + hour, "to": from + 2*hour, "gap": false, "value": 2}}}},
		}}
	encoded, _ := json.Marshal(answer)
	setJSON(t, page, "__answer", string(encoded))
	result := page.Run(`(function () {
  __render(__row, __answer, __answer.parameters, { width: 800, height: 420 });
  var out = {};
  out.axes = __host.querySelectorAll("[data-axis]").map(function (n) { return n.getAttribute("data-axis") + "=" + n.textContent; });
  out.unplaced = __host.querySelectorAll('[data-condition="no_axis"]').map(function (n) { return n.getAttribute("data-series"); });
  out.text = __host.textContent;
  out.title = __host.querySelectorAll("[data-annotations] h3").length;
  out.legend = __host.querySelectorAll("[data-annotations] .legend").length;
  var bars = __host.querySelectorAll("[data-annotations] .series-bars rect");
  out.widest = Math.max.apply(null, bars.map(function (r) { return Number(r.getAttribute("width")); }));
  var repeated = false;
  __host.querySelectorAll("svg").forEach(function (svg) {
    var seen = {};
    svg.querySelectorAll("text.tick").forEach(function (tick) {
      var side = tick.getAttribute("text-anchor") + "|" + tick.getAttribute("x");
      var key = side + "|" + tick.textContent;
      if (tick.getAttribute("text-anchor") !== "middle" && seen[key]) { repeated = true; }
      seen[key] = true;
    });
  });
  out.repeated = repeated;
  return JSON.stringify(out);
})()`).String()
	var drawn struct {
		Axes     []string
		Unplaced []string
		Text     string
		Title    int
		Legend   int
		Widest   float64
		Repeated bool
	}
	if err := json.Unmarshal([]byte(result), &drawn); err != nil {
		t.Fatalf("%v: %s", err, result)
	}
	english := englishCatalogue(t)
	prefix := "plugin.custom_chart.widget.custom_chart."
	if len(drawn.Axes) != 2 || !strings.Contains(strings.Join(drawn.Axes, " "), english[prefix+"unit.ratio"]) {
		t.Errorf("the axes are named %v: the CPU share does not have the right axis", drawn.Axes)
	}
	if strings.Join(drawn.Unplaced, " ") != "s2" {
		t.Errorf("the series no axis carries are %v, not the third unit's", drawn.Unplaced)
	}
	cpu := strings.NewReplacer("{source}", english["series.source.firewall_metric"], "{measure}", english[prefix+"figure.cpu"]).
		Replace(english[prefix+"series_label"])
	if !strings.Contains(drawn.Text, cpu) || strings.Contains(drawn.Text, "firewall_metric") {
		t.Errorf("the CPU series is not named %q by the catalogue: %s", cpu, drawn.Text)
	}
	if drawn.Title != 1 || drawn.Legend != 1 || !strings.Contains(drawn.Text, english["collection.kind.security_event"]) {
		t.Errorf("the annotations have %d titles and %d legends", drawn.Title, drawn.Legend)
	}
	if drawn.Widest <= 0 || drawn.Widest > 800/12 {
		t.Errorf("an annotation bar of one hour in a day is %v wide on an 800-pixel chart", drawn.Widest)
	}
	if drawn.Repeated {
		t.Error("an axis repeats a tick label")
	}
}

// TestASankeyRibbonTakesItsSourcesColourAndOpensItsPair (the ergonomist's review of 17 October 2026):
// the ribbons leaving one node share its series colour, ribbons of different sources do not all share
// one, and every ribbon opens a detail of its pair.
func TestASankeyRibbonTakesItsSourcesColourAndOpensItsPair(t *testing.T) {
	t.Parallel()
	page := drawingPage(t)
	for _, widget := range builtinWidgets(t) {
		if widget.Type == "traffic_sankey" {
			setJSON(t, page, "__row", rowOf(t, widget))
		}
	}
	setJSON(t, page, "__answer", string(firstDrawable(t, "traffic_sankey").Raw))
	result := page.Run(`(function () {
  var drawn = __render(__row, __answer);
  var byLeft = {}, classes = {}, picked = 0, ribbons = __host.querySelectorAll("path.sankey-ribbon");
  ribbons.forEach(function (ribbon) {
    var fill = (ribbon.getAttribute("class").match(/ f[1-6]/) || [""])[0];
    var left = ribbon.getAttribute("data-left");
    (byLeft[left] = byLeft[left] || {})[fill] = true;
    classes[fill] = true;
    var subject = ribbon.getAttribute("data-subject");
    if (subject && drawn.subjects[subject] && drawn.subjects[subject].kind === "flow") { picked++; }
  });
  return JSON.stringify({ ribbons: ribbons.length, picked: picked, lefts: Object.keys(byLeft).length,
    oneEach: Object.keys(byLeft).every(function (left) { return Object.keys(byLeft[left]).length === 1; }),
    colours: Object.keys(classes).filter(Boolean).length });
})()`).String()
	var drawn struct {
		Ribbons, Picked, Lefts, Colours int
		OneEach                         bool
	}
	if err := json.Unmarshal([]byte(result), &drawn); err != nil {
		t.Fatal(err)
	}
	if drawn.Ribbons == 0 || drawn.Picked != drawn.Ribbons {
		t.Errorf("%d of %d ribbons open a detail of their pair", drawn.Picked, drawn.Ribbons)
	}
	if !drawn.OneEach || (drawn.Lefts > 1 && drawn.Colours < 2) {
		t.Errorf("the ribbons of %d sources are drawn in %d colours (one per source: %v)", drawn.Lefts, drawn.Colours, drawn.OneEach)
	}
}
