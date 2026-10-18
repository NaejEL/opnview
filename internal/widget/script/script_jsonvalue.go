package script

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strconv"

	"github.com/dop251/goja"
)

// READING THE JSON A HOST VALUE IS WRITTEN AS, IN GO. A member that crosses through JSON -- a type
// that writes its own JSON, a map -- is written by encoding/json and then read by the script's
// JSON.parse, whose tokenizer is encoding/json's Decoder: a token at a time, an interface per
// token. The JSON encoding/json writes is compact and valid, so it is read here instead, byte by
// byte, into the same values JSON.parse builds: an object of its members, defined in order as
// JSON.parse defines them; an array; a number read as encoding/json reads one and handed as goja
// hands a float64 (goja's floatToValue: a whole number up to 2^53 is an integer); a string; true,
// false and null. Anything this reader does not expect is handed to JSON.parse, which reads it as
// it always did.

// parseJSON reads JSON as the script's JSON.parse reads it, and reports false for anything it leaves
// to JSON.parse.
func (c *ScriptCall) parseJSON(encoded []byte) (goja.Value, bool) {
	reader := jsonReader{call: c, data: encoded}
	value, read := reader.value(0)
	if !read || reader.position != len(encoded) {
		return nil, false
	}
	return value, true
}

// jsonReader reads compact JSON from a position.
type jsonReader struct {
	call     *ScriptCall
	data     []byte
	position int
}

// value reads one value.
func (r *jsonReader) value(depth int) (goja.Value, bool) {
	if r.position >= len(r.data) || depth > maxDataDepth {
		return nil, false
	}
	switch character := r.data[r.position]; {
	case character == '{':
		return r.object(depth)
	case character == '[':
		return r.array(depth)
	case character == '"':
		text, read := r.text()
		if !read {
			return nil, false
		}
		return r.call.VM.ToValue(text), true
	case character == 't':
		return r.literal("true", r.call.VM.ToValue(true))
	case character == 'f':
		return r.literal("false", r.call.VM.ToValue(false))
	case character == 'n':
		return r.literal("null", goja.Null())
	case character == '-' || (character >= '0' && character <= '9'):
		return r.number()
	}
	return nil, false
}

// literal reads one of true, false and null.
func (r *jsonReader) literal(word string, value goja.Value) (goja.Value, bool) {
	if !bytes.HasPrefix(r.data[r.position:], []byte(word)) {
		return nil, false
	}
	r.position += len(word)
	return value, true
}

// number reads a number as encoding/json reads it into a float64.
func (r *jsonReader) number() (goja.Value, bool) {
	start := r.position
	for r.position < len(r.data) {
		character := r.data[r.position]
		if (character >= '0' && character <= '9') || character == '-' || character == '+' || character == '.' ||
			character == 'e' || character == 'E' {
			r.position++
			continue
		}
		break
	}
	number, err := strconv.ParseFloat(string(r.data[start:r.position]), 64)
	if err != nil {
		return nil, false
	}
	return r.call.VM.ToValue(number), true
}

// text reads a string: one with no escape as its bytes, any other as encoding/json reads it.
func (r *jsonReader) text() (string, bool) {
	start := r.position
	r.position++
	escaped := false
	for r.position < len(r.data) {
		switch r.data[r.position] {
		case '\\':
			escaped = true
			r.position += 2
			continue
		case '"':
			r.position++
			if !escaped {
				return string(r.data[start+1 : r.position-1]), true
			}
			var text string
			if json.Unmarshal(r.data[start:r.position], &text) != nil {
				return "", false
			}
			return text, true
		}
		r.position++
	}
	return "", false
}

// object reads an object, defining its members in order as JSON.parse does.
func (r *jsonReader) object(depth int) (goja.Value, bool) {
	r.position++
	object := r.call.VM.NewObject()
	if r.position < len(r.data) && r.data[r.position] == '}' {
		r.position++
		return object, true
	}
	for r.position < len(r.data) {
		if r.data[r.position] != '"' {
			return nil, false
		}
		key, read := r.text()
		if !read || r.position >= len(r.data) || r.data[r.position] != ':' {
			return nil, false
		}
		r.position++
		member, read := r.value(depth + 1)
		if !read {
			return nil, false
		}
		if object.DefineDataProperty(key, member, goja.FLAG_TRUE, goja.FLAG_TRUE, goja.FLAG_TRUE) != nil {
			return nil, false
		}
		if r.position >= len(r.data) {
			return nil, false
		}
		switch r.data[r.position] {
		case ',':
			r.position++
		case '}':
			r.position++
			return object, true
		default:
			return nil, false
		}
	}
	return nil, false
}

// array reads an array.
func (r *jsonReader) array(depth int) (goja.Value, bool) {
	r.position++
	var elements []any
	if r.position < len(r.data) && r.data[r.position] == ']' {
		r.position++
		return r.call.VM.NewArray(), true
	}
	for r.position < len(r.data) {
		element, read := r.value(depth + 1)
		if !read || r.position >= len(r.data) {
			return nil, false
		}
		elements = append(elements, element)
		switch r.data[r.position] {
		case ',':
			r.position++
		case ']':
			r.position++
			return r.call.VM.NewArray(elements...), true
		default:
			return nil, false
		}
	}
	return nil, false
}

// ScriptMembers is implemented by a type that writes its own JSON as an object of whole numbers and of
// such objects -- a Volume's coverage (envelope.VolumeCoverage): it lists the members its JSON writes,
// in order, each an int64 or another ScriptMembers, and the host builds from them the object the
// script's JSON.parse would read from that JSON, without writing and reading it. ok is false for a
// value the JSON path is to write, or refuse.
type ScriptMembers interface {
	ScriptMembers() (names []string, values []any, ok bool)
}

// membersValue is the object JSON.parse reads from a ScriptMembers value's JSON: its members defined in
// order, each whole number read as a JSON number is (a float64, which goja holds as an integer up to
// 2^53). It reports false for a value whose members, or a nested object's, are not listed.
func (c *ScriptCall) membersValue(value ScriptMembers) (goja.Value, bool) {
	names, values, ok := value.ScriptMembers()
	if !ok || len(names) != len(values) {
		return nil, false
	}
	object := c.VM.NewObject()
	for index, name := range names {
		var member goja.Value
		switch typed := values[index].(type) {
		case int64:
			member = c.VM.ToValue(float64(typed))
		case ScriptMembers:
			nested, isRead := c.membersValue(typed)
			if !isRead {
				return nil, false
			}
			member = nested
		default:
			return nil, false
		}
		if object.DefineDataProperty(name, member, goja.FLAG_TRUE, goja.FLAG_TRUE, goja.FLAG_TRUE) != nil {
			return nil, false
		}
	}
	return object, true
}

// scriptMembersOf is a member's ScriptMembers, when it has members to list: not a nil pointer, which
// JSON writes as null.
func scriptMembersOf(member reflect.Value) (ScriptMembers, bool) {
	if (member.Kind() == reflect.Pointer || member.Kind() == reflect.Interface) && member.IsNil() {
		return nil, false
	}
	listed, isListed := member.Interface().(ScriptMembers)
	return listed, isListed
}

// membersJSON writes a ScriptMembers value's JSON as encoding/json writes the value -- compact, every
// name a JSON string, every whole number its digits -- and reports false when it lists no members.
func membersJSON(value ScriptMembers) ([]byte, bool) {
	var buffer bytes.Buffer
	if !appendMembers(&buffer, value) {
		return nil, false
	}
	return buffer.Bytes(), true
}

// appendMembers writes one ScriptMembers object.
func appendMembers(buffer *bytes.Buffer, value ScriptMembers) bool {
	names, values, ok := value.ScriptMembers()
	if !ok || len(names) != len(values) {
		return false
	}
	var digits [24]byte
	buffer.WriteByte('{')
	for index, name := range names {
		if index > 0 {
			buffer.WriteByte(',')
		}
		if appendJSONString(buffer, name) != nil {
			return false
		}
		buffer.WriteByte(':')
		switch typed := values[index].(type) {
		case int64:
			buffer.Write(strconv.AppendInt(digits[:0], typed, 10))
		case ScriptMembers:
			if !appendMembers(buffer, typed) {
				return false
			}
		default:
			return false
		}
	}
	buffer.WriteByte('}')
	return true
}
