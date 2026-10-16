package script_test

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/dop251/goja"

	"github.com/NaejEL/opnview/internal/widget/envelope"
	"github.com/NaejEL/opnview/internal/widget/params"
	"github.com/NaejEL/opnview/internal/widget/request"
	"github.com/NaejEL/opnview/internal/widget/script"

	// The host's areas register from their own packages; a script here reads host.json.
	_ "github.com/NaejEL/opnview/internal/widget/hostareas/values"
)

// The widget script host's own tests: what the data writer writes, what the loader refuses, and
// that every area of the host is documented. The script widgets' answers themselves are asserted
// by the widget tests (internal/widget/widgets) and by each plugin's own tests.

// scriptCallFor runs a script's top level in a fresh runtime with a host over an empty request,
// and returns the call and the value of the global `value`.
func scriptCallFor(t *testing.T, source string) (*script.ScriptCall, goja.Value) {
	t.Helper()
	vm, _ := script.NewWidgetRuntime("example")
	request := &request.Request{Preset: request.Preset{Type: "example_widget"}, Response: &envelope.Response{}, Refs: map[string][]params.Resolved{}}
	call := script.NewScriptCall(request, vm, &script.ScriptWidget{Plugin: "example", Statements: map[string]script.WidgetStatement{}})
	if err := vm.Set("host", call.Host()); err != nil {
		t.Fatal(err)
	}
	if _, err := vm.RunString(source); err != nil {
		t.Fatalf("the script does not run: %v", err)
	}
	return call, vm.Get("value")
}

// TestTheDataWriterWritesWhatGoWrites is the byte identity at the seam: an object the script
// builds is written as encoding/json writes the Go value it stands for -- a struct's members in
// order, a map's keys sorted (marked, or holding an index-like key that JavaScript would have moved
// to the front in numeric order), numbers as Go writes them, strings escaped as Go escapes them.
func TestTheDataWriterWritesWhatGoWrites(t *testing.T) {
	t.Parallel()
	type member struct {
		Zeta  int64    `json:"zeta"`
		Alpha *float64 `json:"alpha"`
	}
	half := 2.5
	// Computed at run time, as the script computes it: a constant expression is exact in Go.
	tenth, fifth := 0.1, 0.2
	goValue := struct {
		Struct  member             `json:"struct"`
		Indexed map[string]int64   `json:"indexed"`
		Marked  map[string]string  `json:"marked"`
		Numbers []float64          `json:"numbers"`
		Whole   []int64            `json:"whole"`
		Text    string             `json:"text"`
		Nested  map[string][]int64 `json:"nested"`
		Absent  *string            `json:"absent,omitempty"`
		Null    *string            `json:"null"`
	}{
		Struct:  member{Zeta: 3, Alpha: &half},
		Indexed: map[string]int64{"9": 1, "10": 2, "x": 3},
		Marked:  map[string]string{"beta": "b", "alpha": "a"},
		Numbers: []float64{tenth + fifth, 1e21, 1e-7, -0.5},
		Whole:   []int64{0, 7, 9007199254740991},
		Text:    "<a & b>  ",
		Nested:  map[string][]int64{"12": {1}, "3": {2}},
	}
	want, err := json.Marshal(goValue)
	if err != nil {
		t.Fatal(err)
	}
	call, value := scriptCallFor(t, `
		var value = {
			struct: { zeta: 3, alpha: 2.5 },
			indexed: { "x": 3, "10": 2, "9": 1 },
			marked: host.json.map({ beta: "b", alpha: "a" }),
			numbers: [0.1 + 0.2, 1e21, 1e-7, -0.5],
			whole: [0, 14 / 2, 9007199254740991],
			text: "<a & b>  ",
			nested: { "3": [2], "12": [1] },
			absent: undefined,
			null: null
		};`)
	got, err := call.Encode(value)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Errorf("the script's data is written\n%s\nand Go writes\n%s", got, want)
	}
	text, err := call.VM.RunString(`host.json.encode(value)`)
	if err != nil || text.String() != string(want) {
		t.Errorf("host.json.encode answers %v (%v)", text, err)
	}
}

// TestTheDataWriterRefusesWhatIsNotData: what Go would refuse, or what JSON.stringify would drop
// silently, is refused by name.
func TestTheDataWriterRefusesWhatIsNotData(t *testing.T) {
	t.Parallel()
	for name, source := range map[string]string{
		"NaN":                 `var value = { x: NaN };`,
		"an infinity":         `var value = [1 / 0];`,
		"a function":          `var value = { f: function () {} };`,
		"a date":              `var value = { at: new Date(0) };`,
		"an undefined member": `var value = [1, undefined];`,
		"a cycle":             `var value = {}; value.self = value;`,
		"a symbol":            `var value = { s: Symbol("x") };`,
	} {
		call, value := scriptCallFor(t, source)
		if encoded, err := call.Encode(value); err == nil {
			t.Errorf("%s was written as %s", name, encoded)
		}
	}
}

// TestAScriptThatCannotServeIsRefusedAtLoad: a script that does not compile, throws at its top
// level or exports no widget.serve is refused when it loads, never at the first request.
func TestAScriptThatCannotServeIsRefusedAtLoad(t *testing.T) {
	t.Parallel()
	for name, source := range map[string]string{
		"a syntax error":   `exports.widget = { serve: function (host) { return {; } };`,
		"a thrown error":   `throw new Error("broken");`,
		"no serve":         `exports.widget = {};`,
		"serve not a fn":   `exports.widget = { serve: 1 };`,
		"no widget at all": `exports.helpers = {};`,
	} {
		if _, err := script.CompileWidgetScript("example.js", source); err == nil {
			t.Errorf("%s compiled", name)
		}
	}
	if _, err := script.CompileWidgetScript("example.js",
		`exports.widget = { serve: function (host) { return { data: null, has_rows: false }; } };`); err != nil {
		t.Errorf("a valid script was refused: %v", err)
	}
}

// flatExample is a flat struct as the figures are: whole numbers, a string, pointers to whole
// numbers, some left out when empty, and an embedded struct JSON flattens.
type flatExample struct {
	Bytes   int64 `json:"bytes"`
	Allowed int64 `json:"allowed"`
	flatEmbedded
}

// flatEmbedded is flatExample's embedded members.
type flatEmbedded struct {
	Measure string `json:"measure,omitempty"`
	Packets *int64 `json:"packets,omitempty"`
	Kept    *int64 `json:"kept"`
	// Coverage is nested, as the figures' coverage is: crossed member by member only while nil.
	Coverage *flatCoverage `json:"coverage,omitempty"`
}

// flatCoverage is a nested member.
type flatCoverage struct {
	Measured int64 `json:"measured"`
}

// flatWindow holds rows of flat structs and members of other kinds, as the volume window does.
type flatWindow struct {
	Requested flatCoverage  `json:"requested"`
	Covered   *flatCoverage `json:"covered"`
	Rows      []flatExample `json:"rows"`
	Names     []string      `json:"names,omitempty"`
	Share     float64       `json:"share"`
	Latitude  *float64      `json:"latitude"`
	Spread    float64       `json:"spread,omitempty"`
	Label     *string       `json:"label"`
	When      json.Number   `json:"when,omitempty"`
}

// TestAFlatValueCrossesMemberByMemberAsItCrossesThroughJSON is the figures' fast crossing (E.3 of
// the Phase E amendment): a flat struct handed to the script member by member is written exactly as
// the same struct handed through JSON, and an object read back member by member is the struct JSON
// would read -- or, for anything the member path does not read exactly as JSON, the JSON path's own
// answer, its refusal included.
func TestAFlatValueCrossesMemberByMemberAsItCrossesThroughJSON(t *testing.T) {
	t.Parallel()
	call, _ := scriptCallFor(t, "var value = null;")
	seven := int64(7)
	for _, value := range []flatExample{
		{},
		{Bytes: 1500, Allowed: 3},
		{Bytes: 1 << 60, flatEmbedded: flatEmbedded{Measure: "netflow", Packets: &seven, Kept: &seven}},
		{Bytes: 3, flatEmbedded: flatEmbedded{Coverage: &flatCoverage{Measured: 2}}},
	} {
		flat, err := call.Encode(call.Value(value))
		if err != nil {
			t.Fatal(err)
		}
		viaJSON, err := call.Encode(call.ValueThroughJSON(value))
		if err != nil {
			t.Fatal(err)
		}
		if string(flat) != string(viaJSON) {
			t.Errorf("member by member %s, through JSON %s", flat, viaJSON)
		}
	}
	label := "a <label> & more"
	negativeZero, third := math.Copysign(0, -1), 1.0/3
	for _, window := range []flatWindow{
		{},
		{Requested: flatCoverage{Measured: 1}, Rows: []flatExample{{Bytes: 1}, {Allowed: 2}}, Share: 0.25,
			Names: []string{"one"}, Label: &label, When: "12"},
		{Covered: &flatCoverage{Measured: 3}, Rows: []flatExample{}, Share: 2},
		{Share: -0.5, Latitude: &negativeZero, Spread: 1e21},
		{Share: 1.0 / 3, Latitude: &third, Spread: 48},
	} {
		flat, err := call.Encode(call.Value(window))
		if err != nil {
			t.Fatal(err)
		}
		viaJSON, err := call.Encode(call.ValueThroughJSON(window))
		if err != nil {
			t.Fatal(err)
		}
		if string(flat) != string(viaJSON) {
			t.Errorf("member by member %s, through JSON %s", flat, viaJSON)
		}
	}
	vm := call.VM
	for _, source := range []string{
		`({bytes: 10, allowed: 2})`,
		`({bytes: 10, measure: "netflow", packets: 4, kept: null})`,
		`({bytes: null, extra: "ignored", other: 3})`,
		`({Bytes: 10})`,
		`({bytes: 10, extra: {nested: 1}})`,
		`({bytes: 2.5})`,
		`({bytes: "10"})`,
		`({bytes: 10, extra: NaN})`,
		`({measure: 4})`,
		`({bytes: 1, coverage: {measured: 2}})`,
		`({bytes: 1, coverage: null})`,
	} {
		argument, err := vm.RunString(source)
		if err != nil {
			t.Fatal(err)
		}
		var flat, viaJSON flatExample
		flatRefusal := refusalOf(func() { call.DecodeFlat(argument, "the figures", &flat) })
		jsonRefusal := refusalOf(func() { call.DecodeArgument(argument, "the figures", &viaJSON) })
		flatEncoded, _ := json.Marshal(flat)
		jsonEncoded, _ := json.Marshal(viaJSON)
		if flatRefusal != jsonRefusal || string(flatEncoded) != string(jsonEncoded) {
			t.Errorf("%s: member by member %s (%q), through JSON %s (%q)", source, flatEncoded, flatRefusal,
				jsonEncoded, jsonRefusal)
		}
	}
	// Arrays of rows are read as JSON reads them, member by member when they can be.
	for _, source := range []string{
		`[{bytes: 1}, null, {allowed: 2, measure: "netflow"}]`,
		`[]`,
		`[{bytes: 1}, 4]`,
		`[{bytes: 1.5}]`,
		`[{Bytes: 1}]`,
	} {
		argument, err := vm.RunString(source)
		if err != nil {
			t.Fatal(err)
		}
		var flat, viaJSON []flatExample
		flatRefusal := refusalOf(func() { call.DecodeFlat(argument, "the rows", &flat) })
		jsonRefusal := refusalOf(func() { call.DecodeArgument(argument, "the rows", &viaJSON) })
		flatEncoded, _ := json.Marshal(flat)
		jsonEncoded, _ := json.Marshal(viaJSON)
		if flatRefusal != jsonRefusal || string(flatEncoded) != string(jsonEncoded) {
			t.Errorf("%s: member by member %s (%q), through JSON %s (%q)", source, flatEncoded, flatRefusal,
				jsonEncoded, jsonRefusal)
		}
	}
	if !call.CrossesFlat(flatExample{}) || !call.CrossesFlat(flatWindow{}) || !call.CrossesFlat([]flatExample{}) {
		t.Error("a flat struct, or a slice of them, is handed through JSON, not member by member")
	}
	// And the member path is the one taken for what a widget hands the figures: a row of the
	// window, its keys and its nulls beside the figures, and figures the host built.
	for _, source := range []string{
		`({src_interface_id: 1, dst_interface_id: null, peer_address: null, traffic_scope: "north_south",
		   exit_leg_device: null, bytes: 10, allowed: 2})`,
		`({bytes: 10, measure: "netflow", packets: 4, kept: null})`,
	} {
		argument, err := vm.RunString(source)
		if err != nil {
			t.Fatal(err)
		}
		var read flatExample
		if !call.ReadsFlat(argument, &read) {
			t.Errorf("%s is read through JSON, not member by member", source)
		}
	}
}

// refusalOf runs a host read and returns the message of the TypeError it threw, or "".
func refusalOf(read func()) (message string) {
	defer func() {
		if recovered := recover(); recovered != nil {
			if exception, isValue := recovered.(goja.Value); isValue {
				message = exception.String()
				return
			}
			panic(recovered)
		}
	}()
	read()
	return ""
}
