package script

import (
	"encoding"
	"encoding/json"
	"math"
	"reflect"
	"strconv"
	"strings"
	"sync"

	"github.com/dop251/goja"
)

// FLAT VALUES, MEMBER BY MEMBER. Value and DecodeArgument cross between Go and the script through
// JSON, which keeps every rule in one place and costs a marshal, a parse and a reflective decode
// per crossing. A widget sums figures once per row of a 30-day window -- host.figures.add, thousands
// of times a request -- and those crossings were most of its answer's time (E.3 of the Phase E
// amendment to specs/SPEC-backend-completion-plugins.md). A struct whose members are whole numbers,
// strings, pointers to either, slices of such structs, and structs embedded without a tag, which
// JSON flattens, crosses here member by member instead, to the same result: the same members in the
// same order, the same members left out, the same numbers; any other member crosses through JSON on
// its own. Anything this path does not read exactly as JSON would -- a member it does not expect, a
// key differing from a member's name only in case, which JSON matches, a value of another type, a
// type with a JSON method of its own -- is handed to the JSON path, which keeps its errors.

// flatField is one member of a flat struct, as JSON names it.
type flatField struct {
	name      string
	index     []int
	kind      flatKind
	omitEmpty bool
}

// flatKind is what a flat member holds.
type flatKind int

const (
	flatInteger flatKind = iota
	flatText
	flatIntegerPointer
	flatTextPointer
	flatFloat
	flatFloatPointer
	// flatRows is a slice of structs, crossed as an array of flat values.
	flatRows
	// flatIntegers is a slice of whole numbers, crossed as an array of numbers.
	flatIntegers
	// flatOther is any other member: crossed through JSON on its own, and read only through JSON.
	flatOther
)

// flatLayout is a flat struct's members, by name and in order.
type flatLayout struct {
	fields []flatField
	byName map[string]int
	folded map[string]bool
}

// flatLayouts caches each type's layout; nil marks a type that is not flat.
var flatLayouts sync.Map

// layoutOf is a struct type's flat layout, or nil when JSON would not write it member by member as
// its visible fields: a type with a JSON method of its own, an embedded pointer or tagged embedded
// struct, a member without a JSON name, or two members of one name.
func layoutOf(structType reflect.Type) *flatLayout {
	if cached, found := flatLayouts.Load(structType); found {
		return cached.(*flatLayout)
	}
	layout := &flatLayout{byName: map[string]int{}, folded: map[string]bool{}}
	if ownJSON(structType) {
		layout = nil
	}
	for _, field := range reflect.VisibleFields(structType) {
		if layout == nil {
			break
		}
		if field.Anonymous {
			if field.Type.Kind() == reflect.Pointer || field.Tag.Get("json") != "" || ownJSON(field.Type) {
				layout = nil
			}
			continue
		}
		if !field.IsExported() {
			continue
		}
		name, options, _ := strings.Cut(field.Tag.Get("json"), ",")
		if _, duplicate := layout.byName[name]; name == "-" || name == "" || duplicate {
			layout = nil
			break
		}
		member := flatField{name: name, index: field.Index, omitEmpty: strings.Contains(","+options+",", ",omitempty,")}
		switch {
		case ownJSON(field.Type) || strings.Contains(","+options+",", ",string,"):
			member.kind = flatOther
		case field.Type == integerType:
			member.kind = flatInteger
		case field.Type == textType:
			member.kind = flatText
		case field.Type == reflect.PointerTo(integerType):
			member.kind = flatIntegerPointer
		case field.Type == reflect.PointerTo(textType):
			member.kind = flatTextPointer
		case field.Type == floatType:
			member.kind = flatFloat
		case field.Type == reflect.PointerTo(floatType):
			member.kind = flatFloatPointer
		case field.Type.Kind() == reflect.Slice && field.Type.Elem().Kind() == reflect.Struct:
			member.kind = flatRows
		case field.Type == integersType:
			member.kind = flatIntegers
		default:
			member.kind = flatOther
		}
		layout.byName[name] = len(layout.fields)
		layout.folded[strings.ToLower(name)] = true
		layout.fields = append(layout.fields, member)
	}
	flatLayouts.Store(structType, layout)
	return layout
}

// ownJSON reports whether a type, or a pointer to it, writes or reads itself: JSON calls its method
// rather than walking its members.
func ownJSON(t reflect.Type) bool {
	for _, candidate := range []reflect.Type{t, reflect.PointerTo(t)} {
		if candidate.Implements(jsonMarshaler) || candidate.Implements(jsonUnmarshaler) ||
			candidate.Implements(textMarshaler) || candidate.Implements(textUnmarshaler) {
			return true
		}
	}
	return false
}

// The scalar types crossed member by member: exactly int64, float64 and string, never a type named
// after them, which JSON may write otherwise (json.Number is a string written as a number).
var (
	integerType = reflect.TypeFor[int64]()
	textType    = reflect.TypeFor[string]()
	floatType   = reflect.TypeFor[float64]()
	// integersType is exactly []int64: a measure's parts (traffic.MeasureMembers.MeasureParts).
	integersType = reflect.TypeFor[[]int64]()
)

// The interfaces through which a type writes or reads its own JSON.
var (
	jsonMarshaler   = reflect.TypeFor[json.Marshaler]()
	jsonUnmarshaler = reflect.TypeFor[json.Unmarshaler]()
	textMarshaler   = reflect.TypeFor[encoding.TextMarshaler]()
	textUnmarshaler = reflect.TypeFor[encoding.TextUnmarshaler]()
)

// flatValue hands a value to the script member by member when it is a struct with a flat layout, a
// pointer to one, or a slice of them, and reports false for anything else, which Value hands
// through JSON.
func (c *ScriptCall) flatValue(value reflect.Value) (goja.Value, bool) {
	switch value.Kind() {
	case reflect.Pointer:
		if value.Type().Elem().Kind() != reflect.Struct {
			return nil, false
		}
		if value.IsNil() {
			return goja.Null(), true
		}
		return c.flatValue(value.Elem())
	case reflect.Slice:
		if value.Type().Elem().Kind() != reflect.Struct || layoutOf(value.Type().Elem()) == nil {
			return nil, false
		}
		if value.IsNil() {
			return goja.Null(), true
		}
		rows := make([]any, value.Len())
		for index := range rows {
			rows[index] = c.memberValue(value.Index(index))
		}
		return c.VM.NewArray(rows...), true
	case reflect.Struct:
	default:
		return nil, false
	}
	layout := layoutOf(value.Type())
	if layout == nil {
		return nil, false
	}
	object := c.VM.NewObject()
	for _, field := range layout.fields {
		member := value.FieldByIndex(field.index)
		switch field.kind {
		case flatInteger:
			if field.omitEmpty && member.Int() == 0 {
				continue
			}
			_ = object.Set(field.name, c.VM.ToValue(member.Int()))
		case flatText:
			if field.omitEmpty && member.String() == "" {
				continue
			}
			_ = object.Set(field.name, c.VM.ToValue(member.String()))
		case flatIntegerPointer, flatTextPointer:
			switch {
			case member.IsNil() && field.omitEmpty:
				continue
			case member.IsNil():
				_ = object.Set(field.name, goja.Null())
			case field.kind == flatTextPointer:
				_ = object.Set(field.name, c.VM.ToValue(member.Elem().String()))
			default:
				_ = object.Set(field.name, c.VM.ToValue(member.Elem().Int()))
			}
		case flatFloat, flatFloatPointer:
			number := member
			if field.kind == flatFloatPointer {
				if member.IsNil() {
					if !field.omitEmpty {
						_ = object.Set(field.name, goja.Null())
					}
					continue
				}
				number = member.Elem()
			}
			if field.kind == flatFloat && field.omitEmpty && number.Float() == 0 {
				continue
			}
			if math.IsNaN(number.Float()) || math.IsInf(number.Float(), 0) {
				// JSON refuses it, and the JSON path says so.
				_ = object.Set(field.name, c.jsonValue(member.Interface()))
				continue
			}
			_ = object.Set(field.name, c.VM.ToValue(number.Float()))
		case flatIntegers:
			switch {
			case field.omitEmpty && member.Len() == 0:
				continue
			case member.IsNil():
				_ = object.Set(field.name, goja.Null())
			default:
				numbers := make([]any, member.Len())
				for index := range numbers {
					numbers[index] = member.Index(index).Int()
				}
				_ = object.Set(field.name, c.VM.NewArray(numbers...))
			}
		case flatRows, flatOther:
			if field.omitEmpty && emptyForJSON(member) {
				continue
			}
			_ = object.Set(field.name, c.memberValue(member))
		}
	}
	return object, true
}

// decodeIntegers reads an array of whole numbers into a []int64 member, and reports whether it read it
// as JSON would. A hole, a member that is not a whole number, or anything that is not a plain array is
// the JSON path's to judge.
func decodeIntegers(member goja.Value, destination reflect.Value) bool {
	array, isObject := member.(*goja.Object)
	if !isObject || array.ClassName() != "Array" {
		return false
	}
	length := array.Get("length").ToInteger()
	numbers := make([]int64, length)
	for index := range numbers {
		element := array.Get(strconv.Itoa(index))
		if element == nil || goja.IsUndefined(element) || goja.IsNull(element) {
			return false
		}
		if _, isObject := element.(*goja.Object); isObject {
			return false
		}
		number, whole := WholeNumber(element.Export())
		if !whole {
			return false
		}
		numbers[index] = number
	}
	destination.Set(reflect.ValueOf(numbers))
	return true
}

// memberValue is one member handed to the script: member by member when it can be, through JSON
// otherwise.
func (c *ScriptCall) memberValue(member reflect.Value) goja.Value {
	if crossed, flat := c.flatValue(member); flat {
		return crossed
	}
	return c.jsonValue(member.Interface())
}

// emptyForJSON reports whether omitempty leaves a member out, as encoding/json decides it: false, 0,
// a nil pointer or interface, and an empty array, slice, map or string.
func emptyForJSON(member reflect.Value) bool {
	switch member.Kind() {
	case reflect.Array, reflect.Map, reflect.Slice, reflect.String:
		return member.Len() == 0
	case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64, reflect.Interface, reflect.Pointer:
		return member.IsZero()
	}
	return false
}

// DecodeFlat reads an argument into a flat struct as DecodeArgument reads it, member by member when
// it can and through DecodeArgument when it cannot.
func (c *ScriptCall) DecodeFlat(value goja.Value, what string, into any) {
	target := reflect.ValueOf(into)
	read := false
	if target.Kind() == reflect.Pointer {
		switch target.Elem().Kind() {
		case reflect.Struct:
			read = c.decodeFlat(value, target.Elem())
		case reflect.Slice:
			read = c.decodeFlatRows(value, target.Elem())
		}
	}
	if !read {
		target.Elem().SetZero()
		c.DecodeArgument(value, what, into)
	}
}

// decodeFlatRows reads an array of plain objects into a slice of flat structs, and reports whether it
// read it as JSON would: an element that is null is a struct holding nothing, and a hole, an element
// of another kind or one the member path cannot read is the JSON path's to judge.
func (c *ScriptCall) decodeFlatRows(value goja.Value, target reflect.Value) bool {
	array, isObject := value.(*goja.Object)
	if !isObject || array.ClassName() != "Array" || target.Type().Elem().Kind() != reflect.Struct {
		return false
	}
	length := array.Get("length").ToInteger()
	rows := reflect.MakeSlice(target.Type(), int(length), int(length))
	for index := 0; index < int(length); index++ {
		element := array.Get(strconv.Itoa(index))
		switch {
		case element == nil || goja.IsUndefined(element):
			return false
		case goja.IsNull(element):
			continue
		case !c.decodeFlat(element, rows.Index(index)):
			return false
		}
	}
	target.Set(rows)
	return true
}

// plainScalar reports whether a member is a scalar the JSON path writes without an error: null, a
// boolean, a string, or a finite number -- not an object, a symbol or anything else it refuses.
func plainScalar(member goja.Value) bool {
	if _, isObject := member.(*goja.Object); isObject {
		return false
	}
	if _, isSymbol := member.(*goja.Symbol); isSymbol {
		return false
	}
	switch typed := member.Export().(type) {
	case nil, bool, string, int64:
		return true
	case float64:
		return !math.IsNaN(typed) && !math.IsInf(typed, 0)
	}
	return false
}

// decodeFlat reads a plain object into a flat struct, and reports whether it read it as JSON would.
func (c *ScriptCall) decodeFlat(value goja.Value, target reflect.Value) bool {
	object, isObject := value.(*goja.Object)
	if !isObject || object.ClassName() != "Object" {
		return false
	}
	if _, callable := goja.AssertFunction(object); callable {
		return false
	}
	layout := layoutOf(target.Type())
	if layout == nil {
		return false
	}
	for _, key := range object.Keys() {
		member := object.Get(key)
		if member == nil || goja.IsUndefined(member) {
			continue
		}
		index, known := layout.byName[key]
		if !known {
			// A key JSON would match to a member despite its case, or a member that is not a
			// plain scalar, is the JSON path's to judge.
			if layout.folded[strings.ToLower(key)] {
				return false
			}
			if !plainScalar(member) {
				return false
			}
			continue
		}
		field := layout.fields[index]
		destination := target.FieldByIndex(field.index)
		if goja.IsNull(member) {
			destination.SetZero()
			continue
		}
		switch field.kind {
		case flatRows, flatOther:
			return false
		case flatIntegers:
			if !decodeIntegers(member, destination) {
				return false
			}
		case flatInteger, flatIntegerPointer:
			if _, isObject := member.(*goja.Object); isObject {
				return false
			}
			number, whole := WholeNumber(member.Export())
			if !whole {
				return false
			}
			if field.kind == flatInteger {
				destination.SetInt(number)
			} else {
				destination.Set(reflect.ValueOf(&number))
			}
		case flatFloat, flatFloatPointer:
			if _, isObject := member.(*goja.Object); isObject {
				return false
			}
			var number float64
			switch typed := member.Export().(type) {
			case int64:
				number = float64(typed)
			case float64:
				if math.IsNaN(typed) || math.IsInf(typed, 0) {
					return false
				}
				number = typed
			default:
				return false
			}
			if field.kind == flatFloat {
				destination.SetFloat(number)
			} else {
				destination.Set(reflect.ValueOf(&number))
			}
		case flatText, flatTextPointer:
			text, isText := member.Export().(string)
			if !isText {
				return false
			}
			if _, isObject := member.(*goja.Object); isObject {
				return false
			}
			if field.kind == flatText {
				destination.SetString(text)
			} else {
				destination.Set(reflect.ValueOf(&text))
			}
		}
	}
	return true
}
