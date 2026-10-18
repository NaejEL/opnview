package script

import (
	"bytes"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/dop251/goja"
)

// HOST OBJECTS: A FLAT STRUCT READ WHERE IT LIES. Building an object member by member (script_flat.go)
// costs a property map, a key and a value per member, for every row the core hands a script -- tens
// of thousands of rows in a 30-day window, and most of an answer's time once the crossings through
// JSON were gone (PA-AC29 of specs/SPEC-backend-completion-plugins.md). A flat struct is therefore
// handed to the script as an object backed by the Go value itself (goja's dynamic object): a member
// is read from the struct when the script reads it, and a host function handed the object back
// reads the struct directly rather than the script's view of it.
//
// IT IS THE SAME DATA. Its members are the members JSON writes, under the same names and in the same
// order, the members omitempty leaves out absent; a whole number, a string, null, a list of whole
// numbers, nested rows -- each read exactly as the object built member by member held it, and an
// object member is the same object each time it is read. A script may set, add and delete members:
// the first change copies every member into the object's own keys, in order, after which it is a
// map of its members as any object is, its keys ordered as ECMAScript orders an ordinary object's
// (index-like keys first, ascending, then the others in the order they were added). A host function
// handed a changed object reads what the script left there, as it reads any object.
//
// WHAT IT DOES NOT DO, which docs/plugins.md says to authors: it carries no symbol, it cannot be
// frozen, sealed or made non-extensible, and a member cannot be redefined as an accessor or made
// read-only, non-enumerable or non-configurable -- goja's dynamic objects refuse all of these with a
// TypeError. A script that needs any of them copies the row first (Object.assign({}, row)), which
// is an ordinary object.

// hostObjectType is a host object's type, as goja exports it.
var hostObjectType = reflect.TypeFor[*hostObject]()

// hostObject is one flat struct handed to the script.
type hostObject struct {
	call   *ScriptCall
	layout *flatLayout
	// value is the struct, a copy the core no longer holds.
	value reflect.Value
	// members are the object members already built, by field index: rows, nested objects, lists
	// and members crossed through JSON, each built once so the script reads the same object each time.
	members []goja.Value
	// own is every member once the script changed one, by key; nil until then. order is their keys in
	// the order they were added.
	own   map[string]goja.Value
	order []string
	// mapped says host.json.map marked the object (MarkMap): it cannot carry the symbol a map
	// marker is.
	mapped bool
}

// newHostObject hands one flat struct to the script. A struct holding a number JSON refuses -- NaN or
// an infinity -- is refused here, as building it member by member refused it; every member crossed
// through JSON and every nested row is built here too, so a member JSON cannot write is refused now
// rather than when the script reads it.
func (c *ScriptCall) newHostObject(layout *flatLayout, value reflect.Value) goja.Value {
	host := &hostObject{call: c, layout: layout, value: value}
	for index, field := range layout.fields {
		member := value.FieldByIndex(field.index)
		switch field.kind {
		case flatFloat, flatFloatPointer:
			number := member
			if field.kind == flatFloatPointer {
				if member.IsNil() {
					continue
				}
				number = member.Elem()
			}
			if math.IsNaN(number.Float()) || math.IsInf(number.Float(), 0) {
				// JSON refuses it, and the JSON path says so.
				return c.jsonValue(member.Interface())
			}
		case flatRows, flatOther:
			if field.omitEmpty && emptyForJSON(member) {
				continue
			}
			if host.members == nil {
				host.members = make([]goja.Value, len(layout.fields))
			}
			host.members[index] = c.memberValue(member)
		}
	}
	return c.VM.NewDynamicObject(host)
}

// omitted reports whether JSON leaves a member out of the struct: omitempty and empty.
func omitted(field flatField, member reflect.Value) bool {
	if !field.omitEmpty {
		return false
	}
	switch field.kind {
	case flatInteger:
		return member.Int() == 0
	case flatText:
		return member.String() == ""
	case flatFloat:
		return member.Float() == 0
	case flatIntegerPointer, flatTextPointer, flatFloatPointer:
		return member.IsNil()
	case flatIntegers:
		return member.Len() == 0
	}
	return emptyForJSON(member)
}

// memberAt is a member as the script reads it, or nil when the struct does not hold it.
func (h *hostObject) memberAt(index int) goja.Value {
	field := h.layout.fields[index]
	member := h.value.FieldByIndex(field.index)
	if omitted(field, member) {
		return nil
	}
	if h.members != nil && h.members[index] != nil {
		return h.members[index]
	}
	vm := h.call.VM
	switch field.kind {
	case flatInteger:
		return vm.ToValue(member.Int())
	case flatText:
		return vm.ToValue(member.String())
	case flatFloat:
		return vm.ToValue(member.Float())
	case flatIntegerPointer, flatTextPointer, flatFloatPointer:
		switch {
		case member.IsNil():
			return goja.Null()
		case field.kind == flatTextPointer:
			return vm.ToValue(member.Elem().String())
		case field.kind == flatFloatPointer:
			return vm.ToValue(member.Elem().Float())
		}
		return vm.ToValue(member.Elem().Int())
	case flatIntegers:
		var list goja.Value = goja.Null()
		if !member.IsNil() {
			numbers := make([]any, member.Len())
			for position := range numbers {
				numbers[position] = member.Index(position).Int()
			}
			list = vm.NewArray(numbers...)
		}
		if h.members == nil {
			h.members = make([]goja.Value, len(h.layout.fields))
		}
		h.members[index] = list
		return list
	}
	// Rows and members crossed through JSON are built with the object; one left out is absent.
	return nil
}

// Get reads a member.
func (h *hostObject) Get(key string) goja.Value {
	if h.own != nil {
		return h.own[key]
	}
	index, known := h.layout.byName[key]
	if !known {
		return nil
	}
	return h.memberAt(index)
}

// Has reports whether the object holds a member.
func (h *hostObject) Has(key string) bool {
	if h.own != nil {
		_, held := h.own[key]
		return held
	}
	return h.Get(key) != nil
}

// Set changes or adds a member: the object becomes a map of its members.
func (h *hostObject) Set(key string, value goja.Value) bool {
	h.ownMembers()
	if _, held := h.own[key]; !held {
		h.order = append(h.order, key)
	}
	h.own[key] = value
	return true
}

// Delete removes a member: the object becomes a map of its members.
func (h *hostObject) Delete(key string) bool {
	h.ownMembers()
	if _, held := h.own[key]; held {
		delete(h.own, key)
		for position, name := range h.order {
			if name == key {
				h.order = append(h.order[:position], h.order[position+1:]...)
				break
			}
		}
	}
	return true
}

// Keys lists the members, in the order an ordinary object lists them.
func (h *hostObject) Keys() []string {
	if h.own != nil {
		return ordinaryKeyOrder(append([]string(nil), h.order...))
	}
	keys := make([]string, 0, len(h.layout.fields))
	for _, field := range h.layout.fields {
		if !omitted(field, h.value.FieldByIndex(field.index)) {
			keys = append(keys, field.name)
		}
	}
	if h.layout.indexKeys {
		return ordinaryKeyOrder(keys)
	}
	return keys
}

// ownMembers copies every member into the object's own keys, once.
func (h *hostObject) ownMembers() {
	if h.own != nil {
		return
	}
	keys := h.Keys()
	own := make(map[string]goja.Value, len(keys))
	for _, key := range keys {
		own[key] = h.Get(key)
	}
	h.own, h.order = own, keys
}

// ordinaryKeyOrder orders keys as ECMAScript orders an ordinary object's own keys, and goja with it:
// the array indices first, ascending, then every other key in the order it was added.
func ordinaryKeyOrder(keys []string) []string {
	indices := 0
	for _, key := range keys {
		if isIndexKey(key) {
			indices++
		}
	}
	if indices == 0 {
		return keys
	}
	ordered := make([]string, 0, len(keys))
	for _, key := range keys {
		if isIndexKey(key) {
			ordered = append(ordered, key)
		}
	}
	sort.Slice(ordered, func(i, j int) bool {
		left, _ := strconv.ParseUint(ordered[i], 10, 64)
		right, _ := strconv.ParseUint(ordered[j], 10, 64)
		return left < right
	})
	for _, key := range keys {
		if !isIndexKey(key) {
			ordered = append(ordered, key)
		}
	}
	return ordered
}

// hostObjectOf is the host object a script value is, unchanged since the host built it for this call,
// or nil.
func (c *ScriptCall) hostObjectOf(object *goja.Object) *hostObject {
	// The type first: exporting an ordinary object copies every member it holds.
	if object.ExportType() != hostObjectType {
		return nil
	}
	host, isHost := object.Export().(*hostObject)
	if !isHost || host.call != c {
		return nil
	}
	return host
}

// MarkMap marks an object the script declares to be a map (host.json.map): with the map marker, or,
// on a host object, which carries no symbol, with its own flag.
func (c *ScriptCall) MarkMap(object *goja.Object) error {
	if host := c.hostObjectOf(object); host != nil {
		host.mapped = true
		return nil
	}
	return object.DefineDataPropertySymbol(c.MapMarker, c.VM.ToValue(true), goja.FLAG_FALSE, goja.FLAG_FALSE,
		goja.FLAG_FALSE)
}

// decodePlan is how one flat layout's members land in another's: per member of the source, the index
// of the target's member of that name, -1 for none; and whether a name the target lacks is one JSON
// would match to a member of the target despite its case.
type decodePlan struct {
	targets []int
	folded  []bool
}

// decodePlans caches a plan per source and target layout.
var decodePlans sync.Map

// planFor is the plan from one layout to another.
func planFor(source, target *flatLayout) *decodePlan {
	key := [2]*flatLayout{source, target}
	if cached, found := decodePlans.Load(key); found {
		return cached.(*decodePlan)
	}
	plan := &decodePlan{targets: make([]int, len(source.fields)), folded: make([]bool, len(source.fields))}
	for index, field := range source.fields {
		plan.targets[index] = -1
		if position, known := target.byName[field.name]; known {
			plan.targets[index] = position
		} else {
			plan.folded[index] = target.folded[strings.ToLower(field.name)]
		}
	}
	decodePlans.Store(key, plan)
	return plan
}

// decodeInto reads an unchanged host object into a flat struct straight from the struct it holds, to
// the result decodeFlat reads from the script's view of it, and reports false wherever decodeFlat
// would hand the argument to the JSON path.
func (h *hostObject) decodeInto(target reflect.Value, layout *flatLayout) bool {
	plan := planFor(h.layout, layout)
	for index, field := range h.layout.fields {
		member := h.value.FieldByIndex(field.index)
		if omitted(field, member) {
			continue
		}
		position := plan.targets[index]
		if position < 0 {
			if plan.folded[index] {
				return false
			}
			switch field.kind {
			case flatRows, flatOther:
				if value := h.memberAt(index); value != nil && !plainScalar(value) {
					return false
				}
			case flatIntegers:
				if !member.IsNil() {
					// An array, which is not a plain scalar.
					return false
				}
			}
			continue
		}
		destination := target.FieldByIndex(layout.fields[position].index)
		if !h.decodeMember(index, field, member, layout.fields[position], destination) {
			return false
		}
	}
	return true
}

// decodeMember reads one member of the host object into a member of the target, as decodeFlat reads
// the script's view of it.
func (h *hostObject) decodeMember(index int, field flatField, member reflect.Value, into flatField,
	destination reflect.Value) bool {
	kind := field.kind
	switch field.kind {
	case flatIntegerPointer, flatTextPointer, flatFloatPointer:
		if member.IsNil() {
			destination.SetZero()
			return true
		}
		member, kind = member.Elem(), pointedKind(field.kind)
	}
	switch kind {
	case flatInteger, flatText, flatFloat:
	case flatIntegers:
		if into.kind == flatIntegers && !member.IsNil() {
			// A list of whole numbers into a list of whole numbers: each read as the script's number
			// holds it, as decodeIntegers reads the array.
			numbers := make([]int64, member.Len())
			for position := range numbers {
				whole, isWhole := WholeNumber(integerNumber(member.Index(position).Int()))
				if !isWhole {
					return false
				}
				numbers[position] = whole
			}
			destination.Set(reflect.ValueOf(numbers))
			return true
		}
		fallthrough
	default:
		// A list, rows, or a member crossed through JSON: read as the script reads it.
		value := h.memberAt(index)
		if value == nil || goja.IsUndefined(value) {
			return true
		}
		if goja.IsNull(value) {
			destination.SetZero()
			return true
		}
		return decodeScriptMember(value, into, destination)
	}
	switch into.kind {
	case flatInteger, flatIntegerPointer:
		var whole int64
		switch kind {
		case flatInteger:
			converted, isWhole := WholeNumber(integerNumber(member.Int()))
			if !isWhole {
				return false
			}
			whole = converted
		case flatFloat:
			converted, isWhole := WholeNumber(floatNumber(member.Float()))
			if !isWhole {
				return false
			}
			whole = converted
		default:
			return false
		}
		if into.kind == flatInteger {
			destination.SetInt(whole)
		} else {
			destination.Set(reflect.ValueOf(&whole))
		}
	case flatFloat, flatFloatPointer:
		var read float64
		switch kind {
		case flatInteger:
			read = float64(member.Int())
		case flatFloat:
			read = member.Float()
		default:
			return false
		}
		if into.kind == flatFloat {
			destination.SetFloat(read)
		} else {
			destination.Set(reflect.ValueOf(&read))
		}
	case flatText, flatTextPointer:
		if kind != flatText {
			return false
		}
		text := member.String()
		if into.kind == flatText {
			destination.SetString(text)
		} else {
			destination.Set(reflect.ValueOf(&text))
		}
	default:
		// Rows, a member crossed through JSON, or a list, which a scalar is not.
		return false
	}
	return true
}

// pointedKind is the scalar kind a pointer member points to.
func pointedKind(kind flatKind) flatKind {
	switch kind {
	case flatIntegerPointer:
		return flatInteger
	case flatTextPointer:
		return flatText
	case flatFloatPointer:
		return flatFloat
	}
	return kind
}

// largestExactInteger is the largest whole number goja holds as an integer: beyond it, a number is a
// float, as every JavaScript number is.
const largestExactInteger = 1 << 53

// integerNumber is a whole number as the script's number exports it (goja's intToValue): an int64 up to
// 2^53 either way, a float64 beyond.
func integerNumber(number int64) any {
	if number >= -largestExactInteger && number <= largestExactInteger {
		return number
	}
	return float64(number)
}

// floatNumber is a float as the script's number exports it (goja's floatToValue): an int64 when it is a
// whole number up to 2^53 either way and not negative zero, the float64 otherwise.
func floatNumber(number float64) any {
	if (number != 0 || !math.Signbit(number)) && !math.IsInf(number, 0) && number == math.Trunc(number) &&
		number >= -largestExactInteger && number <= largestExactInteger {
		return int64(number)
	}
	return number
}

// encodeHostObject writes an unchanged host object as encodeObject writes the script's view of it,
// reading its scalars from the struct.
func (c *ScriptCall) encodeHostObject(buffer *bytes.Buffer, host *hostObject, depth int) error {
	keys := host.Keys()
	if host.mapped || (host.layout.indexKeys && isMapKeys(keys)) {
		sorted := append([]string(nil), keys...)
		sort.Strings(sorted)
		keys = sorted
	}
	buffer.WriteByte('{')
	for position, key := range keys {
		if position > 0 {
			buffer.WriteByte(',')
		}
		if err := appendJSONString(buffer, key); err != nil {
			return err
		}
		buffer.WriteByte(':')
		index := host.layout.byName[key]
		field := host.layout.fields[index]
		member := host.value.FieldByIndex(field.index)
		kind := field.kind
		switch field.kind {
		case flatIntegerPointer, flatTextPointer, flatFloatPointer:
			if member.IsNil() {
				buffer.WriteString("null")
				continue
			}
			member, kind = member.Elem(), pointedKind(field.kind)
		}
		var err error
		switch kind {
		case flatInteger:
			err = encodeScalar(buffer, integerNumber(member.Int()))
		case flatText:
			err = appendJSONString(buffer, member.String())
		case flatFloat:
			err = encodeScalar(buffer, floatNumber(member.Float()))
		default:
			err = c.encodeValue(buffer, host.memberAt(index), depth+1)
		}
		if err != nil {
			return memberError(key, err)
		}
	}
	buffer.WriteByte('}')
	return nil
}

// isMapKeys reports whether keys hold one that looks like an array index.
func isMapKeys(keys []string) bool {
	for _, key := range keys {
		if isIndexKey(key) {
			return true
		}
	}
	return false
}
