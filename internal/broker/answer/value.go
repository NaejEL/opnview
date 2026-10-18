package answer

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Kind is a JSON value's type.
type Kind uint8

// The six JSON types.
const (
	Null Kind = iota
	Bool
	Number
	String
	Object
	Array
)

// String names a kind as the catalogue reports it.
func (k Kind) String() string {
	switch k {
	case Bool:
		return "boolean"
	case Number:
		return "number"
	case String:
		return "string"
	case Object:
		return "object"
	case Array:
		return "array"
	}
	return "null"
}

// Value is one JSON value.
type Value struct {
	Kind Kind
	// Text is a scalar's lexical value: the digits of a number as printed, a string's value,
	// "true" or "false".
	Text string
	// Keys are an object's keys in the order the answer wrote them, and Fields its members.
	Keys   []string
	Fields map[string]*Value
	// Items are an array's elements, in order.
	Items []*Value
	// Holding, when set, is what the source of this value and everything below it holds, in place
	// of the holding the observation began with: a row the mirror wrote before a field was first
	// read does not hold that field, though a newer row beside it does.
	Holding Holding
	// ValueKeyed marks an object whose keys are values -- interface names, addresses -- rather
	// than field names: the mirror stores it as rows keyed by the key, and a read of one of its
	// members is recorded under the wildcard *, never under the key (A10).
	ValueKeyed bool
}

// Parse reads one JSON document, keeping every number's text and every object's key order. A
// body holding anything after the document is refused, as encoding/json refuses it.
func Parse(body []byte) (*Value, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	value, err := parseValue(decoder)
	if err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, errors.New("answer: data after the JSON document")
	}
	return value, nil
}

func parseValue(decoder *json.Decoder) (*Value, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	return fromToken(decoder, token)
}

func fromToken(decoder *json.Decoder, token json.Token) (*Value, error) {
	switch typed := token.(type) {
	case nil:
		return &Value{Kind: Null}, nil
	case bool:
		return &Value{Kind: Bool, Text: strconv.FormatBool(typed)}, nil
	case json.Number:
		return &Value{Kind: Number, Text: string(typed)}, nil
	case string:
		return &Value{Kind: String, Text: typed}, nil
	case json.Delim:
		switch typed {
		case '{':
			object := &Value{Kind: Object, Fields: map[string]*Value{}}
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return nil, err
				}
				key, isKey := keyToken.(string)
				if !isKey {
					return nil, errors.New("answer: an object key is not a string")
				}
				member, err := parseValue(decoder)
				if err != nil {
					return nil, err
				}
				if _, duplicate := object.Fields[key]; !duplicate {
					object.Keys = append(object.Keys, key)
				}
				// A key written twice keeps its last value, as encoding/json and PHP keep it.
				object.Fields[key] = member
			}
			if _, err := decoder.Token(); err != nil {
				return nil, err
			}
			return object, nil
		case '[':
			array := &Value{Kind: Array}
			for decoder.More() {
				item, err := parseValue(decoder)
				if err != nil {
					return nil, err
				}
				array.Items = append(array.Items, item)
			}
			if _, err := decoder.Token(); err != nil {
				return nil, err
			}
			return array, nil
		}
	}
	return nil, fmt.Errorf("answer: unexpected token %v", token)
}

// Marshal writes the value as JSON: numbers as printed, keys in their order.
func (v *Value) Marshal() []byte {
	var buffer bytes.Buffer
	v.write(&buffer)
	return buffer.Bytes()
}

func (v *Value) write(buffer *bytes.Buffer) {
	if v == nil {
		buffer.WriteString("null")
		return
	}
	switch v.Kind {
	case Null:
		buffer.WriteString("null")
	case Bool, Number:
		buffer.WriteString(v.Text)
	case String:
		writeString(buffer, v.Text)
	case Object:
		buffer.WriteByte('{')
		for index, key := range v.Keys {
			if index > 0 {
				buffer.WriteByte(',')
			}
			writeString(buffer, key)
			buffer.WriteByte(':')
			v.Fields[key].write(buffer)
		}
		buffer.WriteByte('}')
	case Array:
		buffer.WriteByte('[')
		for index, item := range v.Items {
			if index > 0 {
				buffer.WriteByte(',')
			}
			item.write(buffer)
		}
		buffer.WriteByte(']')
	}
}

// Field returns an object's member, or nil when the value is no object or has no such member.
func (v *Value) Field(key string) *Value {
	if v == nil || v.Kind != Object {
		return nil
	}
	return v.Fields[key]
}

// Set adds or replaces an object's member, keeping the key's place when it is already there.
func (v *Value) Set(key string, member *Value) {
	if v.Fields == nil {
		v.Fields = map[string]*Value{}
	}
	if _, present := v.Fields[key]; !present {
		v.Keys = append(v.Keys, key)
	}
	v.Fields[key] = member
}

// Plain returns the value as encoding/json decodes it into an interface: maps, slices, float64
// numbers, strings, booleans and nil. It is the shape internal/decode reads, and the one a
// script's json() returned.
func (v *Value) Plain() any {
	if v == nil {
		return nil
	}
	switch v.Kind {
	case Bool:
		return v.Text == "true"
	case Number:
		number, _ := strconv.ParseFloat(v.Text, 64)
		return number
	case String:
		return v.Text
	case Object:
		object := make(map[string]any, len(v.Keys))
		for _, key := range v.Keys {
			object[key] = v.Fields[key].Plain()
		}
		return object
	case Array:
		items := make([]any, len(v.Items))
		for index, item := range v.Items {
			items[index] = item.Plain()
		}
		return items
	}
	return nil
}

// Scalar reports whether the value is no object and no array.
func (v *Value) Scalar() bool { return v != nil && v.Kind != Object && v.Kind != Array }

// text builders for tests and the mirror.

// NewString is a string value.
func NewString(text string) *Value { return &Value{Kind: String, Text: text} }

// NewNumber is a number value with the given lexical form.
func NewNumber(text string) *Value { return &Value{Kind: Number, Text: text} }

// NewNull is the null value.
func NewNull() *Value { return &Value{Kind: Null} }

// NewObject is an empty object.
func NewObject() *Value { return &Value{Kind: Object, Fields: map[string]*Value{}} }

// NewArray is an array of the given items.
func NewArray(items ...*Value) *Value { return &Value{Kind: Array, Items: items} }

// CanonicalNumber reports whether a number's text is what PostgreSQL's numeric type prints back
// for it: plain decimal notation, no exponent, no negative zero.
func CanonicalNumber(text string) bool {
	if text == "" || strings.ContainsAny(text, "eE+") {
		return false
	}
	unsigned := strings.TrimPrefix(text, "-")
	if unsigned == "" {
		return false
	}
	digits := strings.Replace(unsigned, ".", "", 1)
	for _, character := range digits {
		if character < '0' || character > '9' {
			return false
		}
	}
	if strings.HasPrefix(text, "-") && strings.Trim(digits, "0") == "" {
		return false // negative zero prints back as 0
	}
	return !strings.HasSuffix(unsigned, ".") && !strings.HasPrefix(unsigned, ".")
}

// writeString writes a string exactly as encoding/json writes it: one of printable ASCII in which it
// escapes nothing between quotes as it is -- most of an answer's keys and values, which encoding/json
// was most of the time writing a served answer took -- and any other by encoding/json itself.
func writeString(buffer *bytes.Buffer, text string) {
	for index := 0; index < len(text); index++ {
		character := text[index]
		if character < 0x20 || character >= 0x7f || character == '"' || character == '\\' || character == '<' ||
			character == '>' || character == '&' {
			encoded, _ := json.Marshal(text)
			buffer.Write(encoded)
			return
		}
	}
	buffer.WriteByte('"')
	buffer.WriteString(text)
	buffer.WriteByte('"')
}
