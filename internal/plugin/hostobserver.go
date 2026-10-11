package plugin

import (
	"sort"

	"github.com/dop251/goja"
)

// observed wraps every host function so its answer is handed to an observer before the script
// reads it: `opnview plugin test` searches everything the host answers for a credential or the
// firewall's address (BR-AC16). The observer sees what the script could reach without changing
// what it reads: an answer of the firewall is seen as the broker served it, an accessor is never
// invoked, and an observed object is seen through its value, so no read is recorded on its behalf.
func (h *hostCalls) observed(vm *goja.Runtime, host *goja.Object, observe func(function string, answer any)) {
	descriptor, _ := goja.AssertFunction(vm.Get("Object").ToObject(vm).Get("getOwnPropertyDescriptor"))
	h.observeObject(vm, host, "", descriptor, observe)
}

// observeObject wraps the host functions of one object, and of the objects an extension adds to
// it (host.fetch, a table's functions).
func (h *hostCalls) observeObject(vm *goja.Runtime, host *goja.Object, prefix string, descriptor goja.Callable,
	observe func(function string, answer any)) {
	keys := host.Keys()
	sort.Strings(keys)
	for _, key := range keys {
		// A host function is a Go function: it is called as it is, so what it throws, and an
		// interruption, reach the script unchanged.
		member := host.Get(key)
		original, isNative := member.Export().(func(goja.FunctionCall) goja.Value)
		if !isNative {
			if nested, isObject := member.(*goja.Object); isObject && prefix == "" {
				h.observeObject(vm, nested, key+".", descriptor, observe)
			}
			continue
		}
		name := prefix + key
		_ = host.Set(key, func(call goja.FunctionCall) goja.Value {
			h.mutex.Lock()
			before := len(h.answers)
			h.mutex.Unlock()
			result := original(call)
			h.mutex.Lock()
			served := h.answers[before:]
			h.mutex.Unlock()
			for _, read := range served {
				if read.served == nil {
					continue
				}
				response := read.served.Response
				observe(name, map[string]any{"outcome": string(response.Outcome), "status_code": response.StatusCode,
					"detail": response.Detail, "body": string(response.Body), "served_from": read.served.ServedFrom,
					"replaced_by": read.served.ReplacedBy, "gaps": gapsOf(read.served.Gaps)})
			}
			observe(name, plainOf(vm, descriptor, result, 0))
			return result
		})
	}
}

// plainOf is a value as plain Go data, without invoking an accessor or recording a read.
func plainOf(vm *goja.Runtime, descriptor goja.Callable, value goja.Value, depth int) any {
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) || depth > 64 {
		return nil
	}
	if observed, _, _, isObserved := observedOf(value); isObserved {
		if peeked := observed.Peek(); peeked != nil {
			return string(peeked.Marshal())
		}
		return nil
	}
	object, isObject := value.(*goja.Object)
	if !isObject {
		return value.Export()
	}
	if _, isFunction := goja.AssertFunction(object); isFunction {
		return nil
	}
	plain := map[string]any{}
	for _, key := range object.Keys() {
		if descriptor != nil {
			found, err := descriptor(goja.Undefined(), object, vm.ToValue(key))
			if err == nil {
				if described, isDescribed := found.(*goja.Object); isDescribed {
					if getter := described.Get("get"); getter != nil && !goja.IsUndefined(getter) {
						continue
					}
					plain[key] = plainOf(vm, descriptor, described.Get("value"), depth+1)
					continue
				}
			}
		}
		plain[key] = plainOf(vm, descriptor, object.Get(key), depth+1)
	}
	return plain
}
