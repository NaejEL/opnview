package widgets

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strings"
	"sync"
)

// The fields Phase C of the backend-completion cycle changes by name (AC B5: "byte-identical to the
// baseline snapshot's, except fields Phase C changes by name (each listed)").
//
// EVERY EXCEPTION IS A NAMED PATH, REGISTERED IN THE FILE OF THE CHANGE THAT MADE IT. A change
// registers, from an init() in its own golden_phasec_<change>_test.go, the type and the paths it
// alters; TestEveryBuiltinWidgetAnswersByteForByteAsBefore removes exactly those paths from the
// recorded answer and from the new one before comparing, so every other byte of the answer is
// still held to the record. A path is dot-separated from the answer's root; a part ending in
// "[]" names every element of that array, for example "data.chips[].current_rate". A path that
// names nothing in an answer removes nothing.
//
// THE REMOVAL KEEPS EVERYTHING ELSE AS WRITTEN. The answer is decoded into a tree that keeps each
// object's keys in the order they were written and each number as its literal text, and encoded
// back from that tree, so a change of key order or of number formatting outside the removed paths
// still fails the comparison.

var (
	phaseCMutex   sync.Mutex
	phaseCChanged = map[string][]string{}
)

// registerPhaseCChange records that a Phase C change alters the given paths of a type's answer.
func registerPhaseCChange(widgetType string, paths ...string) {
	phaseCMutex.Lock()
	defer phaseCMutex.Unlock()
	phaseCChanged[widgetType] = append(phaseCChanged[widgetType], paths...)
}

// phaseCPaths returns the paths registered for a type, sorted.
func phaseCPaths(widgetType string) []string {
	phaseCMutex.Lock()
	defer phaseCMutex.Unlock()
	paths := append([]string(nil), phaseCChanged[widgetType]...)
	sort.Strings(paths)
	return paths
}

// withoutPhaseCFields returns the answer with every path registered for its type removed. An answer
// of a type with no registered path is returned unchanged, byte for byte; an answer that is not one
// JSON value is returned unchanged too, so the comparison reports it.
func withoutPhaseCFields(widgetType string, answer []byte) []byte {
	paths := phaseCPaths(widgetType)
	if len(paths) == 0 {
		return answer
	}
	decoder := json.NewDecoder(bytes.NewReader(answer))
	decoder.UseNumber()
	tree, err := decodeOrdered(decoder)
	if err != nil {
		return answer
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return answer
	}
	for _, path := range paths {
		removePath(tree, strings.Split(path, "."))
	}
	var encoded bytes.Buffer
	if err := encodeOrdered(&encoded, tree); err != nil {
		return answer
	}
	return encoded.Bytes()
}

// orderedObject is a JSON object with its keys in the order they were written.
type orderedObject struct {
	keys   []string
	values map[string]any
}

// remove deletes one key, keeping the order of the others.
func (o *orderedObject) remove(key string) {
	if _, found := o.values[key]; !found {
		return
	}
	delete(o.values, key)
	for index, existing := range o.keys {
		if existing == key {
			o.keys = append(o.keys[:index], o.keys[index+1:]...)
			return
		}
	}
}

// decodeOrdered reads one JSON value: an *orderedObject, a []any, a json.Number, a string, a bool
// or nil.
func decodeOrdered(decoder *json.Decoder) (any, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	delimiter, isDelimiter := token.(json.Delim)
	if !isDelimiter {
		return token, nil
	}
	switch delimiter {
	case '{':
		object := &orderedObject{values: map[string]any{}}
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return nil, err
			}
			key, ok := keyToken.(string)
			if !ok {
				return nil, errors.New("an object key that is not a string")
			}
			value, err := decodeOrdered(decoder)
			if err != nil {
				return nil, err
			}
			if _, duplicate := object.values[key]; !duplicate {
				object.keys = append(object.keys, key)
			}
			object.values[key] = value
		}
		if _, err := decoder.Token(); err != nil {
			return nil, err
		}
		return object, nil
	case '[':
		elements := []any{}
		for decoder.More() {
			value, err := decodeOrdered(decoder)
			if err != nil {
				return nil, err
			}
			elements = append(elements, value)
		}
		if _, err := decoder.Token(); err != nil {
			return nil, err
		}
		return elements, nil
	}
	return nil, errors.New("an unexpected delimiter")
}

// encodeOrdered writes a tree decodeOrdered read, keys in their order and numbers as written.
func encodeOrdered(buffer *bytes.Buffer, node any) error {
	switch value := node.(type) {
	case *orderedObject:
		buffer.WriteByte('{')
		for index, key := range value.keys {
			if index > 0 {
				buffer.WriteByte(',')
			}
			encodedKey, err := json.Marshal(key)
			if err != nil {
				return err
			}
			buffer.Write(encodedKey)
			buffer.WriteByte(':')
			if err := encodeOrdered(buffer, value.values[key]); err != nil {
				return err
			}
		}
		buffer.WriteByte('}')
	case []any:
		buffer.WriteByte('[')
		for index, element := range value {
			if index > 0 {
				buffer.WriteByte(',')
			}
			if err := encodeOrdered(buffer, element); err != nil {
				return err
			}
		}
		buffer.WriteByte(']')
	case json.Number:
		buffer.WriteString(value.String())
	default:
		encoded, err := json.Marshal(value)
		if err != nil {
			return err
		}
		buffer.Write(encoded)
	}
	return nil
}

// removePath deletes one path from a decoded answer.
func removePath(node any, parts []string) {
	if len(parts) == 0 {
		return
	}
	object, ok := node.(*orderedObject)
	if !ok {
		return
	}
	part := parts[0]
	every := strings.HasSuffix(part, "[]")
	name := strings.TrimSuffix(part, "[]")
	if len(parts) == 1 {
		object.remove(name)
		return
	}
	child, found := object.values[name]
	if !found {
		return
	}
	if !every {
		removePath(child, parts[1:])
		return
	}
	elements, ok := child.([]any)
	if !ok {
		return
	}
	for _, element := range elements {
		removePath(element, parts[1:])
	}
}
