package script

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"

	"github.com/dop251/goja"
)

// Writing a widget script's data as JSON, byte for byte as the Go core would have written it.
//
// JSON.stringify IS NOT THAT WRITER, for three reasons the golden responses of the built-in widgets
// would each catch, and this file settles each one once:
//
//   - KEY ORDER. Go writes a struct's fields in declaration order and a map's keys sorted
//     byte-wise. A script builds an object in the order it sets its members, which this writer keeps
//     -- that is how a script writes a struct -- except where the object is a map: one the script
//     marks with host.json.map, and one holding a key that looks like an array index ("12"), which
//     no Go struct field is called and whose place ECMAScript moves to the front in ascending
//     numeric order ("9" before "10"), where Go writes "10" before "9". Both are written with their
//     keys sorted byte-wise, as Go writes a map.
//   - NUMBERS. Every JavaScript number is a double, and an integer computed by a script is one
//     too. A number is written as Go's encoding/json writes a float64 -- which for a whole number
//     is its digits, exactly as Go writes an int64 -- so 3 is 3 and 2.5 is 2.5 whichever the Go
//     field was. NaN and the infinities are refused, as Go refuses them.
//   - STRINGS. Go escapes <, > and & (and U+2028, U+2029) and replaces invalid UTF-8; a string is
//     written by encoding/json itself, so it is escaped exactly as Go escapes it.
//
// What a script may not put in data is refused by name rather than dropped: a function, a symbol,
// a BigInt, a Date and every other object that is not plain data. A member whose value is
// undefined is left out, as an omitempty field is; an undefined array element is refused.

// maxDataDepth bounds the nesting of a script's data, so a cycle is refused rather than followed.
const maxDataDepth = 64

// Encode writes a value as JSON.
func (c *ScriptCall) Encode(value goja.Value) ([]byte, error) {
	var buffer bytes.Buffer
	if err := c.encodeValue(&buffer, value, 0); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func (c *ScriptCall) encodeValue(buffer *bytes.Buffer, value goja.Value, depth int) error {
	if depth > maxDataDepth {
		return fmt.Errorf("the data is nested deeper than %d levels, or holds a cycle", maxDataDepth)
	}
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		buffer.WriteString("null")
		return nil
	}
	if _, isSymbol := value.(*goja.Symbol); isSymbol {
		return errors.New("a symbol is not data")
	}
	if object, isObject := value.(*goja.Object); isObject {
		if _, callable := goja.AssertFunction(object); callable {
			return errors.New("a function is not data")
		}
		switch object.ClassName() {
		case "Array":
			return c.encodeArray(buffer, object, depth)
		case "Object":
			return c.encodeObject(buffer, object, depth)
		case "Number", "String", "Boolean":
			return encodeScalar(buffer, object.Export())
		}
		return fmt.Errorf("a %s object is not data", object.ClassName())
	}
	return encodeScalar(buffer, value.Export())
}

// encodeArray writes an array, every element in order.
func (c *ScriptCall) encodeArray(buffer *bytes.Buffer, array *goja.Object, depth int) error {
	length := array.Get("length").ToInteger()
	buffer.WriteByte('[')
	for index := int64(0); index < length; index++ {
		if index > 0 {
			buffer.WriteByte(',')
		}
		element := array.Get(strconv.FormatInt(index, 10))
		if element == nil || goja.IsUndefined(element) {
			return fmt.Errorf("the array element %d is undefined", index)
		}
		if err := c.encodeValue(buffer, element, depth+1); err != nil {
			return err
		}
	}
	buffer.WriteByte(']')
	return nil
}

// encodeObject writes an object: in the order its members were set, or sorted as a map.
func (c *ScriptCall) encodeObject(buffer *bytes.Buffer, object *goja.Object, depth int) error {
	if host := c.hostObjectOf(object); host != nil && host.own == nil {
		return c.encodeHostObject(buffer, host, depth)
	}
	keys := object.Keys()
	if c.isMap(object, keys) {
		sorted := append([]string(nil), keys...)
		sort.Strings(sorted)
		keys = sorted
	}
	buffer.WriteByte('{')
	written := 0
	for _, key := range keys {
		member := object.Get(key)
		if member == nil || goja.IsUndefined(member) {
			continue
		}
		if written > 0 {
			buffer.WriteByte(',')
		}
		if err := appendJSONString(buffer, key); err != nil {
			return err
		}
		buffer.WriteByte(':')
		if err := c.encodeValue(buffer, member, depth+1); err != nil {
			return memberError(key, err)
		}
		written++
	}
	buffer.WriteByte('}')
	return nil
}

// isMap reports whether an object is written as a Go map: marked by the script, or holding a key
// that looks like an array index.
func (c *ScriptCall) isMap(object *goja.Object, keys []string) bool {
	if host := c.hostObjectOf(object); host != nil && host.mapped {
		return true
	}
	if c.MapMarker != nil {
		if marker := object.GetSymbol(c.MapMarker); marker != nil && marker.ToBoolean() {
			return true
		}
	}
	for _, key := range keys {
		if isIndexKey(key) {
			return true
		}
	}
	return false
}

// isIndexKey reports whether a key is a canonical array index, which ECMAScript orders before
// every other key, in ascending numeric order.
func isIndexKey(key string) bool {
	if key == "" || len(key) > 10 || (len(key) > 1 && key[0] == '0') {
		return false
	}
	for _, character := range key {
		if character < '0' || character > '9' {
			return false
		}
	}
	number, err := strconv.ParseUint(key, 10, 64)
	return err == nil && number < math.MaxUint32
}

// encodeScalar writes a string, a number or a boolean exactly as encoding/json writes the Go value.
func encodeScalar(buffer *bytes.Buffer, value any) error {
	switch typed := value.(type) {
	case nil:
		buffer.WriteString("null")
		return nil
	case bool:
		buffer.WriteString(strconv.FormatBool(typed))
		return nil
	case string:
		return appendJSONString(buffer, typed)
	case int64:
		var digits [24]byte
		buffer.Write(strconv.AppendInt(digits[:0], typed, 10))
		return nil
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) {
			return fmt.Errorf("%v is not a JSON number", typed)
		}
		if typed == math.Trunc(typed) && math.Abs(typed) < 1e15 && !(typed == 0 && math.Signbit(typed)) {
			// A whole number, which encoding/json writes as its digits, as it writes the int64.
			var digits [24]byte
			buffer.Write(strconv.AppendInt(digits[:0], int64(typed), 10))
			return nil
		}
		encoded, err := json.Marshal(typed)
		if err != nil {
			return err
		}
		buffer.Write(encoded)
		return nil
	}
	return fmt.Errorf("a %T is not data", value)
}

// appendJSONString writes a string exactly as encoding/json writes it: a string of printable ASCII in
// which it escapes nothing is written between quotes as it is, and any other by encoding/json itself.
func appendJSONString(buffer *bytes.Buffer, text string) error {
	for index := 0; index < len(text); index++ {
		character := text[index]
		if character < 0x20 || character >= 0x7f || character == '"' || character == '\\' || character == '<' ||
			character == '>' || character == '&' {
			encoded, err := json.Marshal(text)
			if err != nil {
				return err
			}
			buffer.Write(encoded)
			return nil
		}
	}
	buffer.WriteByte('"')
	buffer.WriteString(text)
	buffer.WriteByte('"')
	return nil
}

// memberError names the member of an object under which a value that is not data was found.
func memberError(key string, err error) error {
	return fmt.Errorf("%s: %w", key, err)
}
