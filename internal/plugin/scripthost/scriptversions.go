package scripthost

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/dop251/goja"

	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/plugin/contract"
	"github.com/NaejEL/opnview/internal/store/vocab"
)

// FailureVersionUnsupported is the outcome of a call on a firewall whose version no range covers.
const FailureVersionUnsupported = vocab.OutcomeFirewallVersionUnsupported

// Route is one way of calling an endpoint: for every version when versions is nil.
type Route struct {
	Versions *contract.VersionRange
	Endpoint opnsense.Endpoint
	// Fields maps a name the script reads to the firewall's; adapter is an exported adapter's name.
	Fields  map[string]string
	Adapter string
}

// versionUnsupported is the detail of a call on a version no range covers.
func versionUnsupported(version string, known bool, ranges []contract.VersionRange) string {
	described := make([]string, 0, len(ranges))
	for _, held := range ranges {
		described = append(described, held.String())
	}
	sort.Strings(described)
	if !known {
		return "the firewall's OPNsense version is not known yet; the plugin declares the ranges " +
			strings.Join(described, ", ")
	}
	return fmt.Sprintf("the firewall runs OPNsense %s, which none of the plugin's ranges %s covers", version,
		strings.Join(described, ", "))
}

// select picks the route of an endpoint for a version: the one whose range covers it, or the one
// with no range. It answers the detail of firewall_version_unsupported when none applies.
func selectRoute(routes []Route, version string, known bool) (Route, string) {
	var ranges []contract.VersionRange
	for _, candidate := range routes {
		if candidate.Versions == nil {
			return candidate, ""
		}
		ranges = append(ranges, *candidate.Versions)
		if known && candidate.Versions.Contains(version) {
			return candidate, ""
		}
	}
	return Route{}, versionUnsupported(version, known, ranges)
}

// versionSignal ends a call whose endpoint has no route for the firewall's version. It is not an
// exception: a script's try cannot catch it.
type versionSignal struct{ detail string }

func (v *versionSignal) Error() string { return v.detail }

// ScriptVariant is one compiled script file and the range it runs on.
type ScriptVariant struct {
	Versions contract.VersionRange
	Program  *goja.Program
}

// forVersion is the script that runs on a version: this one when it maps no range or its range
// covers the version, else the variant whose range does. It answers the detail of
// firewall_version_unsupported when none applies.
func (s *Script) forVersion(version string, known bool) (*goja.Program, string) {
	if s.Versions == nil {
		return s.Program, ""
	}
	ranges := []contract.VersionRange{*s.Versions}
	if known && s.Versions.Contains(version) {
		return s.Program, ""
	}
	for _, variant := range s.Variants {
		ranges = append(ranges, variant.Versions)
		if known && variant.Versions.Contains(version) {
			return variant.Program, ""
		}
	}
	return nil, versionUnsupported(version, known, ranges)
}

// routeOf is the route of an endpoint on the firewall's version, recording its field mapping and
// its adapter for the answers it gives. A version no range covers ends the call with
// firewall_version_unsupported, before anything is sent.
func (h *hostCalls) routeOf(s *Script, name string, declared opnsense.Endpoint) Route {
	routes := s.Routes[name]
	if len(routes) == 0 {
		return Route{Endpoint: declared}
	}
	version, known := h.gate.FirewallVersion()
	chosen, unsupported := selectRoute(routes, version, known)
	if unsupported != "" {
		panic(&versionSignal{detail: fmt.Sprintf("the endpoint %s: %s", name, unsupported)})
	}
	h.mutex.Lock()
	if len(chosen.Fields) > 0 {
		if h.renames == nil {
			h.renames = map[string]map[string]string{}
		}
		h.renames[chosen.Endpoint.Path] = chosen.Fields
	}
	if chosen.Adapter != "" {
		if h.adapters == nil {
			h.adapters = map[string]string{}
		}
		h.adapters[name] = chosen.Adapter
	} else {
		delete(h.adapters, name)
	}
	h.mutex.Unlock()
	return chosen
}

// renamed is the firewall's name of a field the script reads under a stable name, on the route
// an observed answer came from.
func (h *hostCalls) renamed(endpoint, key string) string {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	if firewall, mapped := h.renames[endpoint][key]; mapped {
		return firewall
	}
	return key
}

// stableName is the name the script reads a firewall field under.
func (h *hostCalls) stableName(endpoint, key string) string {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	for stable, firewall := range h.renames[endpoint] {
		if firewall == key {
			return stable
		}
	}
	return key
}

// adapt hands an answer's value to the route's adapter, which returns the value the script reads.
func (h *hostCalls) adapt(vm *goja.Runtime, name string, answer goja.Value) goja.Value {
	h.mutex.Lock()
	adapter := h.adapters[name]
	h.mutex.Unlock()
	object, isObject := answer.(*goja.Object)
	if adapter == "" || !isObject || h.exports == nil {
		return answer
	}
	adapters, _ := h.exports.Get("adapters").(*goja.Object)
	if adapters == nil {
		Throw(vm, "the route of %s names the adapter %s, and the script exports no adapters", name, adapter)
	}
	function, isFunction := goja.AssertFunction(adapters.Get(adapter))
	if !isFunction {
		Throw(vm, "the route of %s names the adapter %s, which the script does not export", name, adapter)
	}
	adapted, err := function(goja.Undefined(), object.Get("value"))
	if err != nil {
		var exception *goja.Exception
		if errors.As(err, &exception) {
			panic(exception.Value())
		}
		Throw(vm, "the adapter %s of %s failed: %v", adapter, name, err)
	}
	_ = object.Set("value", adapted)
	return object
}

// firewall sets host.firewall: the firewall's OPNsense version, read-only, null until read.
func (h *hostCalls) firewall(vm *goja.Runtime, host *goja.Object) {
	described := vm.NewObject()
	if version, known := h.gate.FirewallVersion(); known {
		_ = described.DefineDataProperty("version", vm.ToValue(version), goja.FLAG_FALSE, goja.FLAG_FALSE,
			goja.FLAG_TRUE)
	} else {
		_ = described.DefineDataProperty("version", goja.Null(), goja.FLAG_FALSE, goja.FLAG_FALSE, goja.FLAG_TRUE)
	}
	_ = host.DefineDataProperty("firewall", described, goja.FLAG_FALSE, goja.FLAG_FALSE, goja.FLAG_TRUE)
}

// renamePlain gives a value read whole the names the script reads: every object's firewall field
// names mapped back to the stable names of the route it came from.
func (h *hostCalls) renamePlain(endpoint string, value any) any {
	h.mutex.Lock()
	fields := h.renames[endpoint]
	h.mutex.Unlock()
	if len(fields) == 0 {
		return value
	}
	stable := make(map[string]string, len(fields))
	for name, firewall := range fields {
		stable[firewall] = name
	}
	var rename func(any) any
	rename = func(value any) any {
		switch typed := value.(type) {
		case map[string]any:
			renamed := make(map[string]any, len(typed))
			for key, member := range typed {
				if name, mapped := stable[key]; mapped {
					key = name
				}
				renamed[key] = rename(member)
			}
			return renamed
		case []any:
			for index, item := range typed {
				typed[index] = rename(item)
			}
			return typed
		}
		return value
	}
	return rename(value)
}
