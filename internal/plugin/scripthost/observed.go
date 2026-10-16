package scripthost

import (
	"errors"
	"strconv"

	"github.com/dop251/goja"

	"github.com/NaejEL/opnview/internal/broker/answer"
	"github.com/NaejEL/opnview/internal/decode"
)

// The observed answer, as a script reads it (A10 of the broker amendment).
//
// A firewall answer reaches a script as an object whose reads are recorded field by field: each
// member is read through Get, each item through the array's Get, and what was read decides what
// the mirror stores. It is a host-side dynamic object rather than a JavaScript Proxy: a property
// read is one Go call and no trap, which is what keeps a pass reading a thousand rows within twice
// the time of a plain object (BR-AC17). Enumerating or serialising an ordinary object reads it
// whole; a script may write into an answer, and what it writes stays in the script.

// rerunSignal is what interrupts a call whose script read a field the mirror did not hold: the call
// is run again, its requests to that endpoint answered from the firewall.
type rerunSignal struct {
	endpoint string
	path     string
}

func (r *rerunSignal) Error() string {
	return "the mirror did not hold " + r.path + " of " + r.endpoint
}

// observedObject is an object of an answer.
type observedObject struct {
	calls    *hostCalls
	vm       *goja.Runtime
	o        *answer.Observed
	endpoint string
	written  map[string]goja.Value
	deleted  map[string]bool
}

// observedArray is an array of an answer.
type observedArray struct {
	calls    *hostCalls
	vm       *goja.Runtime
	o        *answer.Observed
	endpoint string
	written  map[int]goja.Value
	length   int
}

// observe turns an observed value into what a script reads: a primitive for a scalar, a dynamic
// object or array for a container.
func (h *hostCalls) observe(vm *goja.Runtime, o *answer.Observed, endpoint string, direct bool) goja.Value {
	if o == nil {
		return goja.Undefined()
	}
	switch o.Kind() {
	case answer.Object:
		return vm.NewDynamicObject(&observedObject{calls: h, vm: vm, o: o, endpoint: endpoint})
	case answer.Array:
		length, err := o.Len()
		if err != nil {
			return h.notHeld(vm, endpoint, err)
		}
		return vm.NewDynamicArray(&observedArray{calls: h, vm: vm, o: o, endpoint: endpoint, length: length})
	}
	value := o.Peek()
	if direct {
		read, err := o.Scalar()
		if err != nil {
			return h.notHeld(vm, endpoint, err)
		}
		value = read
	}
	return scalarValue(vm, value)
}

// scalarValue is a scalar as JSON.parse would give it: a number as a float, a string, a boolean,
// null.
func scalarValue(vm *goja.Runtime, value *answer.Value) goja.Value {
	if value == nil {
		return goja.Undefined()
	}
	switch value.Kind {
	case answer.Null:
		return goja.Null()
	case answer.Bool:
		return vm.ToValue(value.Text == "true")
	case answer.Number:
		number, _ := strconv.ParseFloat(value.Text, 64)
		return vm.ToValue(number)
	case answer.String:
		return vm.ToValue(value.Text)
	}
	return vm.ToValue(value.Plain())
}

// notHeld ends the call so it runs again with the endpoint answered from the firewall. The
// interruption is not an exception: a script's try cannot catch it.
func (h *hostCalls) notHeld(vm *goja.Runtime, endpoint string, err error) goja.Value {
	var missing *answer.NotHeld
	if errors.As(err, &missing) {
		vm.Interrupt(&rerunSignal{endpoint: endpoint, path: missing.Path})
	}
	return goja.Undefined()
}

func (d *observedObject) Get(key string) goja.Value {
	if d.deleted[key] {
		return nil
	}
	if written, present := d.written[key]; present {
		return written
	}
	member, present, err := d.o.Get(d.calls.renamed(d.endpoint, key))
	if err != nil {
		return d.calls.notHeld(d.vm, d.endpoint, err)
	}
	if !present {
		return nil
	}
	return d.calls.observe(d.vm, member, d.endpoint, false)
}

func (d *observedObject) Set(key string, value goja.Value) bool {
	if d.written == nil {
		d.written = map[string]goja.Value{}
	}
	d.written[key] = value
	delete(d.deleted, key)
	return true
}

func (d *observedObject) Has(key string) bool {
	if d.deleted[key] {
		return false
	}
	if _, present := d.written[key]; present {
		return true
	}
	present, err := d.o.Has(d.calls.renamed(d.endpoint, key))
	if err != nil {
		d.calls.notHeld(d.vm, d.endpoint, err)
		return false
	}
	return present
}

func (d *observedObject) Delete(key string) bool {
	if d.deleted == nil {
		d.deleted = map[string]bool{}
	}
	d.deleted[key] = true
	delete(d.written, key)
	return true
}

func (d *observedObject) Keys() []string {
	keys, err := d.o.Keys()
	if err != nil {
		d.calls.notHeld(d.vm, d.endpoint, err)
		return nil
	}
	var result []string
	for _, key := range keys {
		key = d.calls.stableName(d.endpoint, key)
		if !d.deleted[key] {
			result = append(result, key)
		}
	}
	for key := range d.written {
		found := false
		for _, held := range result {
			found = found || held == key
		}
		if !found {
			result = append(result, key)
		}
	}
	return result
}

func (a *observedArray) Len() int { return a.length }

func (a *observedArray) Get(index int) goja.Value {
	if written, present := a.written[index]; present {
		return written
	}
	if index < 0 || index >= a.length {
		return nil
	}
	return a.calls.observe(a.vm, a.o.Index(index), a.endpoint, true)
}

func (a *observedArray) Set(index int, value goja.Value) bool {
	if a.written == nil {
		a.written = map[int]goja.Value{}
	}
	a.written[index] = value
	if index >= a.length {
		a.length = index + 1
	}
	return true
}

func (a *observedArray) SetLen(length int) bool {
	a.length = length
	return true
}

// observedOf returns the observed value a script handed back to a host helper, if it is one.
func observedOf(value goja.Value) (*answer.Observed, string, *hostCalls, bool) {
	object, isObject := value.(*goja.Object)
	if !isObject {
		return nil, "", nil, false
	}
	switch typed := object.Export().(type) {
	case *observedObject:
		return typed.o, typed.endpoint, typed.calls, true
	case *observedArray:
		return typed.o, typed.endpoint, typed.calls, true
	}
	return nil, "", nil, false
}

// fieldsOf reads the named keys of an observed object, as internal/decode reads an object: the
// keys read and no other, or the whole object when no key is named.
func (h *hostCalls) fieldsOf(vm *goja.Runtime, o *answer.Observed, endpoint string, keys []string) map[string]any {
	if len(keys) == 0 {
		value, err := o.Whole()
		if err != nil {
			h.notHeld(vm, endpoint, err)
			return nil
		}
		object, _ := h.renamePlain(endpoint, value.Plain()).(map[string]any)
		return object
	}
	object := map[string]any{}
	for _, key := range keys {
		member, present, err := o.Get(h.renamed(endpoint, key))
		if err != nil {
			h.notHeld(vm, endpoint, err)
			return nil
		}
		if !present {
			continue
		}
		if member.Kind() == answer.Object || member.Kind() == answer.Array {
			value, err := member.Whole()
			if err != nil {
				h.notHeld(vm, endpoint, err)
				return nil
			}
			object[key] = h.renamePlain(endpoint, value.Plain())
			continue
		}
		object[key] = member.Peek().Plain()
	}
	return object
}

// walk descends a path of keys through an observed value, nil where a step is absent.
func (h *hostCalls) walk(vm *goja.Runtime, o *answer.Observed, endpoint string, steps []string) *answer.Observed {
	current := o
	for _, step := range steps {
		member, present, err := current.Get(h.renamed(endpoint, step))
		if err != nil {
			h.notHeld(vm, endpoint, err)
			return nil
		}
		if !present {
			return nil
		}
		current = member
	}
	return current
}

// nestedOf is decode.Nested over an observed object: the object at a path, observed.
func (h *hostCalls) nestedOf(vm *goja.Runtime, o *answer.Observed, endpoint string, steps []string) goja.Value {
	found := h.walk(vm, o, endpoint, steps)
	if found == nil || found.Kind() != answer.Object {
		return goja.Null()
	}
	return h.observe(vm, found, endpoint, false)
}

// nestedArgument is the object decode.NestedString and decode.NestedFlag read: for an observed
// answer, the path down to the leaf, holding the leaf and nothing beside it.
func nestedArgument(vm *goja.Runtime, call goja.FunctionCall, steps []string,
	objectArgument func(goja.FunctionCall, int, ...string) map[string]any) map[string]any {
	observed, endpoint, calls, isObserved := observedOf(call.Argument(0))
	if !isObserved || len(steps) == 0 {
		return objectArgument(call, 0)
	}
	parent := calls.walk(vm, observed, endpoint, steps[:len(steps)-1])
	if parent == nil {
		return map[string]any{}
	}
	leaf := calls.fieldsOf(vm, parent, endpoint, steps[len(steps)-1:])
	object := leaf
	for index := len(steps) - 2; index >= 0; index-- {
		object = map[string]any{steps[index]: object}
	}
	return object
}

// objectsOf is decode.Objects over an observed collection: its object items or members, observed.
func (h *hostCalls) objectsOf(vm *goja.Runtime, o *answer.Observed, endpoint string) goja.Value {
	var values []any
	switch o.Kind() {
	case answer.Array:
		count, err := o.Len()
		if err != nil {
			return h.notHeld(vm, endpoint, err)
		}
		for index := 0; index < count; index++ {
			if item := o.Index(index); item.Kind() == answer.Object {
				values = append(values, h.observe(vm, item, endpoint, false))
			}
		}
	case answer.Object:
		keys, err := o.Keys()
		if err != nil {
			return h.notHeld(vm, endpoint, err)
		}
		for _, key := range keys {
			member, present, err := o.Get(key)
			if err != nil {
				return h.notHeld(vm, endpoint, err)
			}
			if present && member.Kind() == answer.Object {
				values = append(values, h.observe(vm, member, endpoint, false))
			}
		}
	}
	return vm.NewArray(values...)
}

// rowsOf is decode.Rows over an observed answer: the rows of a grid-search envelope, or the
// answer's own items or members.
func (h *hostCalls) rowsOf(vm *goja.Runtime, o *answer.Observed, endpoint string) goja.Value {
	if o.Kind() == answer.Object {
		present, err := o.Has(h.renamed(endpoint, "rows"))
		if err != nil {
			return h.notHeld(vm, endpoint, err)
		}
		if present {
			rows, _, err := o.Get(h.renamed(endpoint, "rows"))
			if err != nil {
				return h.notHeld(vm, endpoint, err)
			}
			return h.objectsOf(vm, rows, endpoint)
		}
	}
	return h.objectsOf(vm, o, endpoint)
}

// nonEmpty is decode.NonEmpty over an observed value.
func (h *hostCalls) nonEmpty(vm *goja.Runtime, o *answer.Observed, endpoint string) bool {
	switch o.Kind() {
	case answer.Array:
		count, err := o.Len()
		if err != nil {
			h.notHeld(vm, endpoint, err)
			return false
		}
		return count > 0
	case answer.Object:
		keys, err := o.Keys()
		if err != nil {
			h.notHeld(vm, endpoint, err)
			return false
		}
		return len(keys) > 0
	}
	value, err := o.Scalar()
	if err != nil {
		h.notHeld(vm, endpoint, err)
		return false
	}
	return decode.NonEmpty(value.Plain())
}

// plainExport turns what a script answered into plain Go values: an observed answer it handed
// back, whole or in part, is read whole and given as encoding/json would decode it.
func plainExport(value any) any {
	switch typed := value.(type) {
	case *observedObject:
		read, err := typed.o.Whole()
		if err != nil {
			return nil
		}
		plain, _ := typed.calls.renamePlain(typed.endpoint, read.Plain()).(map[string]any)
		for key, written := range typed.written {
			if plain == nil {
				plain = map[string]any{}
			}
			plain[key] = plainExport(written.Export())
		}
		for key := range typed.deleted {
			delete(plain, key)
		}
		return plain
	case *observedArray:
		read, err := typed.o.Whole()
		if err != nil {
			return nil
		}
		items, _ := typed.calls.renamePlain(typed.endpoint, read.Plain()).([]any)
		for index, written := range typed.written {
			for len(items) <= index {
				items = append(items, nil)
			}
			items[index] = plainExport(written.Export())
		}
		return items
	case map[string]any:
		for key, member := range typed {
			typed[key] = plainExport(member)
		}
		return typed
	case []any:
		for index, item := range typed {
			typed[index] = plainExport(item)
		}
		return typed
	}
	return value
}
