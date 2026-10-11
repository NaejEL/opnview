package widget

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"sync"

	"github.com/dop251/goja"
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
	install func(call *scriptCall, area *goja.Object)
}

var hostAreas = struct {
	sync.Mutex
	areas    map[string]hostArea
	failures []error
}{areas: map[string]hostArea{}}

// registerHostArea registers one area of the host, from the init() of its own file. An area
// registered twice is recorded, and the service is not built.
func registerHostArea(name string, install func(call *scriptCall, area *goja.Object)) {
	hostAreas.Lock()
	defer hostAreas.Unlock()
	if _, duplicate := hostAreas.areas[name]; duplicate {
		hostAreas.failures = append(hostAreas.failures, fmt.Errorf("widget: the host area %q is registered twice", name))
		return
	}
	hostAreas.areas[name] = hostArea{name: name, install: install}
}

// hostAreaFailures joins the registration failures of the host's areas.
func hostAreaFailures() error {
	hostAreas.Lock()
	defer hostAreas.Unlock()
	return errors.Join(hostAreas.failures...)
}

// HostAreas returns the name of every area of the host, sorted: what docs/plugins.md documents,
// which a test compares.
func HostAreas() []string {
	hostAreas.Lock()
	defer hostAreas.Unlock()
	names := make([]string, 0, len(hostAreas.areas))
	for name := range hostAreas.areas {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// scriptCall is one request's host: the request, the runtime, and the first failure of a read.
type scriptCall struct {
	request *Request
	vm      *goja.Runtime
	widget  *scriptWidget
	// err is the first read that failed: the store's, which ends the request as the store's.
	err error
	// mapMarker marks an object the script declares to be a map, written with its keys sorted
	// as Go writes a map's (host_json.go, script_json.go).
	mapMarker *goja.Symbol
	parse     goja.Callable
}

// newScriptCall prepares one request's host.
func newScriptCall(r *Request, vm *goja.Runtime, widget *scriptWidget) *scriptCall {
	call := &scriptCall{request: r, vm: vm, widget: widget, mapMarker: goja.NewSymbol("opnview.map")}
	if object, isObject := vm.Get("JSON").(*goja.Object); isObject {
		call.parse, _ = goja.AssertFunction(object.Get("parse"))
	}
	return call
}

// host builds the host object: every registered area, each on an object of its own.
func (c *scriptCall) host() *goja.Object {
	hostAreas.Lock()
	areas := make([]hostArea, 0, len(hostAreas.areas))
	for _, area := range hostAreas.areas {
		areas = append(areas, area)
	}
	hostAreas.Unlock()
	sort.Slice(areas, func(i, j int) bool { return areas[i].name < areas[j].name })
	host := c.vm.NewObject()
	for _, area := range areas {
		object := c.vm.NewObject()
		area.install(c, object)
		_ = host.Set(area.name, object)
	}
	return host
}

// fail records a failed read and stops the script: an interrupt, which no try/catch of the script
// can swallow.
func (c *scriptCall) fail(err error) goja.Value {
	if c.err == nil {
		c.err = err
	}
	c.vm.Interrupt(err)
	return goja.Undefined()
}

// throw raises a TypeError for a script that called a host function wrongly.
func (c *scriptCall) throw(format string, arguments ...any) {
	panic(c.vm.NewTypeError(fmt.Sprintf(format, arguments...)))
}

// function sets one host function on an area.
func (c *scriptCall) function(area *goja.Object, name string, body func(goja.FunctionCall) goja.Value) {
	_ = area.Set(name, body)
}

// value hands a Go value to the script as the JSON it would be written as, so a struct's fields
// keep their order and their JSON names, and a map's keys are sorted as Go writes them.
func (c *scriptCall) value(v any) goja.Value {
	if v == nil {
		return goja.Null()
	}
	encoded, err := json.Marshal(v)
	if err != nil {
		c.throw("a host value cannot be handed to the script: %v", err)
	}
	if c.parse == nil {
		return c.vm.ToValue(string(encoded))
	}
	parsed, err := c.parse(goja.Undefined(), c.vm.ToValue(string(encoded)))
	if err != nil {
		c.throw("a host value cannot be handed to the script: %v", err)
	}
	return parsed
}

// exportOf is a script's value as Go reads it: nil for undefined and null.
func exportOf(value goja.Value) any {
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return nil
	}
	return value.Export()
}

// absent reports whether an argument is undefined or null.
func absent(value goja.Value) bool {
	return value == nil || goja.IsUndefined(value) || goja.IsNull(value)
}

// integerOf reads an argument that must be a whole number.
func (c *scriptCall) integerOf(value goja.Value, what string) int64 {
	number, ok := wholeNumber(exportOf(value))
	if !ok {
		c.throw("%s is %s, not a whole number", what, describe(value))
	}
	return number
}

// optionalIntegerOf reads an argument that is a whole number or null.
func (c *scriptCall) optionalIntegerOf(value goja.Value, what string) *int64 {
	if absent(value) {
		return nil
	}
	number := c.integerOf(value, what)
	return &number
}

// textOf reads an argument that must be a string.
func (c *scriptCall) textOf(value goja.Value, what string) string {
	text, ok := exportOf(value).(string)
	if !ok {
		c.throw("%s is %s, not a string", what, describe(value))
	}
	return text
}

// textsOf reads an argument that must be an array of strings.
func (c *scriptCall) textsOf(value goja.Value, what string) []string {
	list, ok := exportOf(value).([]any)
	if !ok {
		c.throw("%s is %s, not an array of strings", what, describe(value))
	}
	texts := make([]string, 0, len(list))
	for _, element := range list {
		text, isText := element.(string)
		if !isText {
			c.throw("%s holds a member that is not a string", what)
		}
		texts = append(texts, text)
	}
	return texts
}

// integersOf reads an argument that must be an array of whole numbers.
func (c *scriptCall) integersOf(value goja.Value, what string) []int64 {
	list, ok := exportOf(value).([]any)
	if !ok {
		c.throw("%s is %s, not an array of whole numbers", what, describe(value))
	}
	numbers := make([]int64, 0, len(list))
	for _, element := range list {
		number, isNumber := wholeNumber(element)
		if !isNumber {
			c.throw("%s holds a member that is not a whole number", what)
		}
		numbers = append(numbers, number)
	}
	return numbers
}

// intervalOf reads an argument that must be an interval, {from, to}.
func (c *scriptCall) intervalOf(value goja.Value, what string) Interval {
	object, isObject := value.(*goja.Object)
	if !isObject {
		c.throw("%s is %s, not an interval {from, to}", what, describe(value))
	}
	return Interval{From: c.integerOf(object.Get("from"), what+".from"), To: c.integerOf(object.Get("to"), what+".to")}
}

// optionalIntervalOf reads an argument that is an interval or null.
func (c *scriptCall) optionalIntervalOf(value goja.Value, what string) *Interval {
	if absent(value) {
		return nil
	}
	interval := c.intervalOf(value, what)
	return &interval
}

// decodeArgument reads an argument the script built into a Go value of a JSON shape, through JSON.
func (c *scriptCall) decodeArgument(value goja.Value, what string, into any) {
	encoded, err := c.encode(value)
	if err != nil {
		c.throw("%s cannot be read: %v", what, err)
	}
	if err := json.Unmarshal(encoded, into); err != nil {
		c.throw("%s is not of the expected shape: %v", what, err)
	}
}

// wholeNumber reads an exported number that holds a whole value.
func wholeNumber(value any) (int64, bool) {
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

// describe names what a value is, for a message.
func describe(value goja.Value) string {
	switch {
	case value == nil || goja.IsUndefined(value):
		return "undefined"
	case goja.IsNull(value):
		return "null"
	}
	return fmt.Sprintf("%T %s", value.Export(), value.String())
}
