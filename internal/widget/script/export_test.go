package script

import (
	"reflect"
	"sort"

	"github.com/dop251/goja"

	"github.com/NaejEL/opnview/internal/widget/request"
)

// What this package's external tests call that the package keeps unexported. They are external
// because the host's areas register from packages that import this one (internal/widget/hostareas).

// NewWidgetRuntime is newWidgetRuntime.
func NewWidgetRuntime(plugin string) (*goja.Runtime, *goja.Object) { return newWidgetRuntime(plugin) }

// NewScriptCall is newScriptCall.
func NewScriptCall(r *request.Request, vm *goja.Runtime, widget *ScriptWidget) *ScriptCall {
	return newScriptCall(r, vm, widget)
}

// Host is host.
func (c *ScriptCall) Host() *goja.Object { return c.host() }

// ValueThroughJSON is jsonValue: a value handed to the script through its JSON alone.
func (c *ScriptCall) ValueThroughJSON(v any) goja.Value { return c.jsonValue(v) }

// CrossesFlat reports whether Value hands a value member by member, without the JSON path.
func (c *ScriptCall) CrossesFlat(v any) bool {
	_, flat := c.flatValue(reflect.ValueOf(v))
	return flat
}

// ReadsFlat reports whether decodeFlat reads an argument member by member, without the JSON path.
func (c *ScriptCall) ReadsFlat(value goja.Value, into any) bool {
	return c.decodeFlat(value, reflect.ValueOf(into).Elem())
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
