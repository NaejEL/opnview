package script

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"sort"
	"sync"

	"github.com/dop251/goja"

	"github.com/NaejEL/opnview/internal/widget/envelope"
	"github.com/NaejEL/opnview/internal/widget/request"
)

// The host a widget script is handed: one object per AREA of the core, host.<area>.<function>.
//
// AN AREA IS A FILE. Each host_<area>.go file registers its area from its own init() with
// registerHostArea, and installs its functions on the object it is handed; this file lists none of
// them. Exposing a further shared figure of the core to widget scripts is adding a host_<area>.go
// file, and editing no other -- the rule a widget plugin's own directory follows, applied to the
// core's side of the seam. docs/plugins.md, "Widgets", documents every area for authors.
//
// WHAT A HOST FUNCTION MAY DO, AND HOW IT FAILS. It reads through the request (the store's read
// connections, the request's caches) and records on it (a state, a withheld field, the coverage).
// A script that calls one wrongly -- a missing argument, a statement it does not declare -- gets a
// TypeError it may catch; a read that fails is the store's failure, which no script can catch: the
// call is interrupted and the request ends with the store's error.

// hostArea is one area of the host.
type hostArea struct {
	name    string
	install func(call *ScriptCall, area *goja.Object)
}

var hostAreas = struct {
	sync.Mutex
	areas    map[string]hostArea
	failures []error
}{areas: map[string]hostArea{}}

// RegisterHostArea registers one area of the host, from the init() of its own file. An area
// registered twice is recorded, and the service is not built.
func RegisterHostArea(name string, install func(call *ScriptCall, area *goja.Object)) {
	hostAreas.Lock()
	defer hostAreas.Unlock()
	if _, duplicate := hostAreas.areas[name]; duplicate {
		hostAreas.failures = append(hostAreas.failures, fmt.Errorf("widget: the host area %q is registered twice", name))
		return
	}
	hostAreas.areas[name] = hostArea{name: name, install: install}
}

// HostAreaFailures joins the registration failures of the host's areas.
func HostAreaFailures() error {
	hostAreas.Lock()
	defer hostAreas.Unlock()
	return errors.Join(hostAreas.failures...)
}

// ScriptCall is one request's host: the request, the runtime, and the first failure of a read.
type ScriptCall struct {
	Request *request.Request
	VM      *goja.Runtime
	Widget  *ScriptWidget
	// err is the first read that failed: the store's, which ends the request as the store's.
	err error
	// MapMarker marks an object the script declares to be a map, written with its keys sorted
	// as Go writes a map's (internal/widget/hostareas/values/host_json.go, script_json.go).
	MapMarker *goja.Symbol
	parse     goja.Callable
}

// newScriptCall prepares one request's host.
func newScriptCall(r *request.Request, vm *goja.Runtime, widget *ScriptWidget) *ScriptCall {
	call := &ScriptCall{Request: r, VM: vm, Widget: widget, MapMarker: goja.NewSymbol("opnview.map")}
	if object, isObject := vm.Get("JSON").(*goja.Object); isObject {
		call.parse, _ = goja.AssertFunction(object.Get("parse"))
	}
	return call
}

// host builds the host object: every registered area, each on an object of its own.
func (c *ScriptCall) host() *goja.Object {
	hostAreas.Lock()
	areas := make([]hostArea, 0, len(hostAreas.areas))
	for _, area := range hostAreas.areas {
		areas = append(areas, area)
	}
	hostAreas.Unlock()
	sort.Slice(areas, func(i, j int) bool { return areas[i].name < areas[j].name })
	host := c.VM.NewObject()
	for _, area := range areas {
		object := c.VM.NewObject()
		area.install(c, object)
		_ = host.Set(area.name, object)
	}
	return host
}

// Fail records a failed read and stops the script: an interrupt, which no try/catch of the script
// can swallow.
func (c *ScriptCall) Fail(err error) goja.Value {
	if c.err == nil {
		c.err = err
	}
	c.VM.Interrupt(err)
	return goja.Undefined()
}

// Throw raises a TypeError for a script that called a host function wrongly.
func (c *ScriptCall) Throw(format string, arguments ...any) {
	panic(c.VM.NewTypeError(fmt.Sprintf(format, arguments...)))
}

// Function sets one host function on an area.
func (c *ScriptCall) Function(area *goja.Object, name string, body func(goja.FunctionCall) goja.Value) {
	_ = area.Set(name, body)
}

// Value hands a Go value to the script as the JSON it would be written as, so a struct's fields
// keep their order and their JSON names, and a map's keys are sorted as Go writes them. A struct
// whose members allow it -- the figures, the rows of the window -- is built member by member to the
// same result rather than written and parsed (script_flat.go).
func (c *ScriptCall) Value(v any) goja.Value {
	if v == nil {
		return goja.Null()
	}
	if c.parse != nil {
		if crossed, flat := c.flatValue(reflect.ValueOf(v)); flat {
			return crossed
		}
	}
	return c.jsonValue(v)
}

// jsonValue hands a Go value to the script through its JSON.
func (c *ScriptCall) jsonValue(v any) goja.Value {
	if v == nil {
		return goja.Null()
	}
	if listed, isListed := scriptMembersOf(reflect.ValueOf(v)); isListed {
		if read, isRead := c.membersValue(listed); isRead {
			return read
		}
	}
	encoded, err := json.Marshal(v)
	if err != nil {
		c.Throw("a host value cannot be handed to the script: %v", err)
	}
	return c.encodedValue(encoded)
}

// encodedValue is JSON encoding/json wrote, as the script's JSON.parse reads it.
func (c *ScriptCall) encodedValue(encoded []byte) goja.Value {
	if c.parse == nil {
		return c.VM.ToValue(string(encoded))
	}
	if read, isRead := c.parseJSON(encoded); isRead {
		return read
	}
	parsed, err := c.parse(goja.Undefined(), c.VM.ToValue(string(encoded)))
	if err != nil {
		c.Throw("a host value cannot be handed to the script: %v", err)
	}
	return parsed
}

// ExportOf is a script's value as Go reads it: nil for undefined and null.
func ExportOf(value goja.Value) any {
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return nil
	}
	return value.Export()
}

// Absent reports whether an argument is undefined or null.
func Absent(value goja.Value) bool {
	return value == nil || goja.IsUndefined(value) || goja.IsNull(value)
}

// IntegerOf reads an argument that must be a whole number.
func (c *ScriptCall) IntegerOf(value goja.Value, what string) int64 {
	number, ok := WholeNumber(ExportOf(value))
	if !ok {
		c.Throw("%s is %s, not a whole number", what, Describe(value))
	}
	return number
}

// OptionalIntegerOf reads an argument that is a whole number or null.
func (c *ScriptCall) OptionalIntegerOf(value goja.Value, what string) *int64 {
	if Absent(value) {
		return nil
	}
	number := c.IntegerOf(value, what)
	return &number
}

// TextOf reads an argument that must be a string.
func (c *ScriptCall) TextOf(value goja.Value, what string) string {
	text, ok := ExportOf(value).(string)
	if !ok {
		c.Throw("%s is %s, not a string", what, Describe(value))
	}
	return text
}

// TextsOf reads an argument that must be an array of strings.
func (c *ScriptCall) TextsOf(value goja.Value, what string) []string {
	list, ok := ExportOf(value).([]any)
	if !ok {
		c.Throw("%s is %s, not an array of strings", what, Describe(value))
	}
	texts := make([]string, 0, len(list))
	for _, element := range list {
		text, isText := element.(string)
		if !isText {
			c.Throw("%s holds a member that is not a string", what)
		}
		texts = append(texts, text)
	}
	return texts
}

// IntegersOf reads an argument that must be an array of whole numbers.
func (c *ScriptCall) IntegersOf(value goja.Value, what string) []int64 {
	list, ok := ExportOf(value).([]any)
	if !ok {
		c.Throw("%s is %s, not an array of whole numbers", what, Describe(value))
	}
	numbers := make([]int64, 0, len(list))
	for _, element := range list {
		number, isNumber := WholeNumber(element)
		if !isNumber {
			c.Throw("%s holds a member that is not a whole number", what)
		}
		numbers = append(numbers, number)
	}
	return numbers
}

// IntervalOf reads an argument that must be an interval, {from, to}.
func (c *ScriptCall) IntervalOf(value goja.Value, what string) envelope.Interval {
	object, isObject := value.(*goja.Object)
	if !isObject {
		c.Throw("%s is %s, not an interval {from, to}", what, Describe(value))
	}
	return envelope.Interval{From: c.IntegerOf(object.Get("from"), what+".from"), To: c.IntegerOf(object.Get("to"), what+".to")}
}

// OptionalIntervalOf reads an argument that is an interval or null.
func (c *ScriptCall) OptionalIntervalOf(value goja.Value, what string) *envelope.Interval {
	if Absent(value) {
		return nil
	}
	interval := c.IntervalOf(value, what)
	return &interval
}

// DecodeArgument reads an argument the script built into a Go value of a JSON shape, through JSON.
func (c *ScriptCall) DecodeArgument(value goja.Value, what string, into any) {
	encoded, err := c.Encode(value)
	if err != nil {
		c.Throw("%s cannot be read: %v", what, err)
	}
	if err := json.Unmarshal(encoded, into); err != nil {
		c.Throw("%s is not of the expected shape: %v", what, err)
	}
}

// WholeNumber reads an exported number that holds a whole value.
func WholeNumber(value any) (int64, bool) {
	switch typed := value.(type) {
	case int64:
		return typed, true
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) || typed != math.Trunc(typed) ||
			typed < math.MinInt64 || typed >= math.MaxInt64 {
			return 0, false
		}
		return int64(typed), true
	}
	return 0, false
}

// Describe names what a value is, for a message.
func Describe(value goja.Value) string {
	switch {
	case value == nil || goja.IsUndefined(value):
		return "undefined"
	case goja.IsNull(value):
		return "null"
	}
	exported := value.Export()
	if _, isHost := exported.(*hostObject); isHost {
		// A host object is described as the object it reads as.
		exported = map[string]any{}
	}
	return fmt.Sprintf("%T %s", exported, value.String())
}
