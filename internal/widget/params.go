package widget

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
)

// Parameter handling (A3, A4 of specs/SPEC-step-5b-widget-api.md).
//
// The accepted keys of a type are exactly the keys of its catalogue Parameters paragraph
// (docs/widget-catalogue.md, Part 2), declared once in the preset's file. A key the type
// does not declare is reported and ignored; a value outside a key's vocabulary or type
// takes the default and is reported; a value of the right type is never clamped; an absent
// key takes the default declared beside it, which docs/widget-api.md tabulates.

// ParamKind is the value shape a key accepts, in the terms of docs/dashboard-format.md,
// "Parameters".
type ParamKind string

// The value shapes.
const (
	// KindEnum is a scalar string from a closed vocabulary.
	KindEnum ParamKind = "enum"
	// KindEnumSet is an array of scalars, each from a closed vocabulary, unordered and
	// without duplicates.
	KindEnumSet ParamKind = "enum_set"
	// KindBoolean is a boolean.
	KindBoolean ParamKind = "boolean"
	// KindInteger is a whole number at or above Min (and at or below Max when set).
	KindInteger ParamKind = "integer"
	// KindNumber is a number at or above Min (and at or below Max when set).
	KindNumber ParamKind = "number"
	// KindReference is one reference object.
	KindReference ParamKind = "reference"
	// KindReferenceList is an array of reference objects.
	KindReferenceList ParamKind = "reference_list"
	// KindStringList is an array of free strings, matched exactly: names the
	// installation observed, such as a blocklist's name.
	KindStringList ParamKind = "string_list"
	// KindSeries is the ordered array of objects of `series`.
	KindSeries ParamKind = "series"
	// KindObject is an object the API passes through untouched, for rendering
	// parameters whose structure belongs to the renderer (the chart's axes).
	KindObject ParamKind = "object"
)

// Param declares one key of one type.
type Param struct {
	Key  string
	Kind ParamKind
	// Values is the vocabulary of an enum or enum set.
	Values []string
	// Default is the value an absent or invalid key takes, as JSON would decode it:
	// a string, a bool, a float64, a []any, or nil for "absent" (an optional list that
	// is empty means no restriction).
	Default any
	// Min and Max bound a number; Max is ignored when HasMax is false.
	Min    float64
	Max    float64
	HasMax bool
	// Reference is the reference kind of a reference or a reference list.
	Reference string
	// Data says the API applies the key: two values give two different responses. A key
	// that is not Data is rendering-only: accepted, echoed in the effective parameters,
	// and never applied (A4.5).
	Data bool
	// Required says the type has nothing to answer without it.
	Required bool
}

// InvalidValue is a value the API could not apply, and why. Its default was applied
// instead.
type InvalidValue struct {
	Path   string          `json:"path"`
	Value  json.RawMessage `json:"value"`
	Reason string          `json:"reason"`
}

// The reasons a value is invalid.
const (
	reasonNotInVocabulary = "not_in_vocabulary"
	reasonWrongType       = "wrong_type"
	reasonOutOfRange      = "out_of_range"
	reasonDuplicate       = "duplicate_member"
	reasonMissing         = "missing_required"
	reasonNotAReference   = "not_a_reference_object"
)

// parsedParameters is the result of parsing one request's parameters against a type.
type parsedParameters struct {
	effective map[string]any
	unknown   map[string]json.RawMessage
	invalid   []InvalidValue
	// references are the reference objects to resolve, by path.
	references []referenceAt
	// missing names a required key that was absent or invalid.
	missing []string
}

// referenceAt is one reference object found at a path.
type referenceAt struct {
	path      string
	key       string
	series    int // -1 outside a series entry
	reference Reference
	expected  string
	// echo is the reference as the effective parameters echo it, so a reference the
	// aggregate mode withholds can have its value removed from the echo too.
	echo map[string]any
}

// errMalformed is a parameters payload that cannot be parsed at all: not JSON, or not a
// JSON object. It is the one parameter failure that refuses the request (A16).
type errMalformed struct{ reason string }

func (e errMalformed) Error() string { return "malformed parameters: " + e.reason }

// parseParameters parses a raw parameters payload against a type's declared keys. An
// empty payload is an empty object.
func parseParameters(raw string, params []Param) (parsedParameters, error) {
	result := parsedParameters{
		effective: map[string]any{},
		unknown:   map[string]json.RawMessage{},
	}
	given := map[string]json.RawMessage{}
	if strings.TrimSpace(raw) != "" {
		decoder := json.NewDecoder(strings.NewReader(raw))
		decoder.UseNumber()
		var value any
		if err := decoder.Decode(&value); err != nil {
			return result, errMalformed{reason: "not_json"}
		}
		if decoder.More() {
			return result, errMalformed{reason: "trailing_data"}
		}
		if _, isObject := value.(map[string]any); !isObject {
			return result, errMalformed{reason: "not_an_object"}
		}
		if err := json.Unmarshal([]byte(raw), &given); err != nil {
			return result, errMalformed{reason: "not_an_object"}
		}
	}
	declared := map[string]Param{}
	for _, param := range params {
		declared[param.Key] = param
	}
	for key, value := range given {
		if _, known := declared[key]; !known {
			result.unknown[key] = value
		}
	}
	for _, param := range params {
		value, present := given[param.Key]
		if !present || isJSONNull(value) {
			if param.Required {
				result.missing = append(result.missing, param.Key)
			}
			result.effective[param.Key] = param.Default
			continue
		}
		var (
			parsed     any
			problems   []InvalidValue
			references []referenceAt
		)
		if param.Kind == KindSeries {
			var unknown map[string]json.RawMessage
			parsed, problems, references, unknown = parseSeries(param.Key, value)
			for path, raw := range unknown {
				result.unknown[path] = raw
			}
		} else {
			parsed, problems, references = parseValue(param, param.Key, value, -1)
		}
		if len(problems) > 0 && parsed == nil {
			result.invalid = append(result.invalid, problems...)
			if param.Required {
				result.missing = append(result.missing, param.Key)
			}
			result.effective[param.Key] = param.Default
			continue
		}
		result.invalid = append(result.invalid, problems...)
		result.references = append(result.references, references...)
		result.effective[param.Key] = parsed
	}
	sort.Slice(result.invalid, func(i, j int) bool { return result.invalid[i].Path < result.invalid[j].Path })
	sort.Strings(result.missing)
	return result, nil
}

// parseValue parses one value. It returns the parsed value, or nil with problems when the
// whole value is invalid; a series may return a value and the problems of its entries.
func parseValue(param Param, path string, raw json.RawMessage, series int) (any, []InvalidValue,
	[]referenceAt) {
	invalid := func(reason string) (any, []InvalidValue, []referenceAt) {
		return nil, []InvalidValue{{Path: path, Value: raw, Reason: reason}}, nil
	}
	switch param.Kind {
	case KindEnum:
		var text string
		if json.Unmarshal(raw, &text) != nil {
			return invalid(reasonWrongType)
		}
		if !contains(param.Values, text) {
			return invalid(reasonNotInVocabulary)
		}
		return text, nil, nil
	case KindEnumSet:
		var members []string
		if json.Unmarshal(raw, &members) != nil {
			return invalid(reasonWrongType)
		}
		seen := map[string]bool{}
		result := []any{}
		for _, member := range members {
			if !contains(param.Values, member) {
				return invalid(reasonNotInVocabulary)
			}
			if seen[member] {
				return invalid(reasonDuplicate)
			}
			seen[member] = true
			result = append(result, member)
		}
		return result, nil, nil
	case KindBoolean:
		var flag bool
		if json.Unmarshal(raw, &flag) != nil {
			return invalid(reasonWrongType)
		}
		return flag, nil, nil
	case KindInteger, KindNumber:
		var number float64
		if json.Unmarshal(raw, &number) != nil {
			return invalid(reasonWrongType)
		}
		if param.Kind == KindInteger && number != math.Trunc(number) {
			return invalid(reasonWrongType)
		}
		if number < param.Min || (param.HasMax && number > param.Max) {
			return invalid(reasonOutOfRange)
		}
		return number, nil, nil
	case KindStringList:
		var members []string
		if json.Unmarshal(raw, &members) != nil {
			return invalid(reasonWrongType)
		}
		result := []any{}
		for _, member := range members {
			result = append(result, member)
		}
		return result, nil, nil
	case KindReference:
		reference, ok := decodeReference(raw)
		if !ok {
			return invalid(reasonNotAReference)
		}
		echo := referenceValue(reference)
		return echo, nil, []referenceAt{{path: path, key: param.Key, series: series,
			reference: reference, expected: param.Reference, echo: echo}}
	case KindReferenceList:
		var members []json.RawMessage
		if json.Unmarshal(raw, &members) != nil {
			return invalid(reasonWrongType)
		}
		result := []any{}
		var references []referenceAt
		for index, member := range members {
			reference, ok := decodeReference(member)
			if !ok {
				return nil, []InvalidValue{{Path: fmt.Sprintf("%s[%d]", path, index), Value: member,
					Reason: reasonNotAReference}}, nil
			}
			echo := referenceValue(reference)
			result = append(result, echo)
			references = append(references, referenceAt{path: fmt.Sprintf("%s[%d]", path, index),
				key: param.Key, series: series, reference: reference, expected: param.Reference, echo: echo})
		}
		return result, nil, references
	case KindObject:
		var object map[string]any
		if json.Unmarshal(raw, &object) != nil {
			return invalid(reasonWrongType)
		}
		return object, nil, nil
	}
	return invalid(reasonWrongType)
}

// isJSONNull reports whether a raw value is the JSON null.
func isJSONNull(raw json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

// contains reports whether a vocabulary holds a value.
func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

// The typed reads of an effective parameter. A handler reads only keys its preset
// declares, so a missing key is a defect in the preset and reads as the zero value.

func (p parsedParameters) text(key string) string {
	value, _ := p.effective[key].(string)
	return value
}

func (p parsedParameters) flag(key string) bool {
	value, _ := p.effective[key].(bool)
	return value
}

func (p parsedParameters) integer(key string) int64 {
	switch value := p.effective[key].(type) {
	case float64:
		return int64(value)
	case int:
		return int64(value)
	case int64:
		return value
	}
	return 0
}

func (p parsedParameters) number(key string) float64 {
	switch value := p.effective[key].(type) {
	case float64:
		return value
	case int:
		return float64(value)
	case int64:
		return float64(value)
	}
	return 0
}

func (p parsedParameters) set(key string) []string {
	members, _ := p.effective[key].([]any)
	result := make([]string, 0, len(members))
	for _, member := range members {
		if text, ok := member.(string); ok {
			result = append(result, text)
		}
	}
	return result
}

func (p parsedParameters) has(key, member string) bool {
	return contains(p.set(key), member)
}
