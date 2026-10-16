package params

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/NaejEL/opnview/internal/dataset"
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
	ReasonNotInVocabulary = "not_in_vocabulary"
	ReasonWrongType       = "wrong_type"
	ReasonOutOfRange      = "out_of_range"
	reasonDuplicate       = "duplicate_member"
	reasonMissing         = "missing_required"
	reasonNotAReference   = "not_a_reference_object"
)

// ParsedParameters is the result of parsing one request's parameters against a type.
type ParsedParameters struct {
	Effective map[string]any
	Unknown   map[string]json.RawMessage
	Invalid   []InvalidValue
	// References are the reference objects to resolve, by path.
	References []referenceAt
	// Missing names a required key that was absent or invalid.
	Missing []string
}

// referenceAt is one reference object found at a path.
type referenceAt struct {
	Path      string
	Key       string
	Series    int // -1 outside a series entry
	Reference Reference
	expected  string
	// Echo is the reference as the effective parameters echo it, so a reference the
	// aggregate mode withholds can have its value removed from the echo too.
	Echo map[string]any
}

// ErrMalformed is a parameters payload that cannot be parsed at all: not JSON, or not a
// JSON object. It is the one parameter failure that refuses the request (A16).
type ErrMalformed struct{ Reason string }

func (e ErrMalformed) Error() string { return "malformed parameters: " + e.Reason }

// ParseParameters parses a raw parameters payload against a type's declared keys. An
// empty payload is an empty object.
func ParseParameters(raw string, params []Param) (ParsedParameters, error) {
	return ParseParametersWith(raw, params, nil)
}

// ParseParametersWith parses a payload with the data catalogue at hand: a series may then be read
// from any of its datasets (custom_chart, P8 of specs/SPEC-backend-completion-plugins.md).
func ParseParametersWith(raw string, params []Param, datasets *dataset.Catalogue) (ParsedParameters, error) {
	result := ParsedParameters{
		Effective: map[string]any{},
		Unknown:   map[string]json.RawMessage{},
	}
	given := map[string]json.RawMessage{}
	if strings.TrimSpace(raw) != "" {
		decoder := json.NewDecoder(strings.NewReader(raw))
		decoder.UseNumber()
		var value any
		if err := decoder.Decode(&value); err != nil {
			return result, ErrMalformed{Reason: "not_json"}
		}
		if decoder.More() {
			return result, ErrMalformed{Reason: "trailing_data"}
		}
		if _, isObject := value.(map[string]any); !isObject {
			return result, ErrMalformed{Reason: "not_an_object"}
		}
		if err := json.Unmarshal([]byte(raw), &given); err != nil {
			return result, ErrMalformed{Reason: "not_an_object"}
		}
	}
	declared := map[string]Param{}
	for _, param := range params {
		declared[param.Key] = param
	}
	for key, value := range given {
		if _, known := declared[key]; !known {
			result.Unknown[key] = value
		}
	}
	for _, param := range params {
		value, present := given[param.Key]
		if !present || isJSONNull(value) {
			if param.Required {
				result.Missing = append(result.Missing, param.Key)
			}
			result.Effective[param.Key] = param.Default
			continue
		}
		var (
			parsed     any
			problems   []InvalidValue
			references []referenceAt
		)
		if param.Kind == KindSeries {
			var unknown map[string]json.RawMessage
			parsed, problems, references, unknown = parseSeries(param.Key, value, datasets)
			for path, raw := range unknown {
				result.Unknown[path] = raw
			}
		} else {
			parsed, problems, references = parseValue(param, param.Key, value, -1)
		}
		if len(problems) > 0 && parsed == nil {
			result.Invalid = append(result.Invalid, problems...)
			if param.Required {
				result.Missing = append(result.Missing, param.Key)
			}
			result.Effective[param.Key] = param.Default
			continue
		}
		result.Invalid = append(result.Invalid, problems...)
		result.References = append(result.References, references...)
		result.Effective[param.Key] = parsed
	}
	sort.Slice(result.Invalid, func(i, j int) bool { return result.Invalid[i].Path < result.Invalid[j].Path })
	sort.Strings(result.Missing)
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
			return invalid(ReasonWrongType)
		}
		if !Contains(param.Values, text) {
			return invalid(ReasonNotInVocabulary)
		}
		return text, nil, nil
	case KindEnumSet:
		var members []string
		if json.Unmarshal(raw, &members) != nil {
			return invalid(ReasonWrongType)
		}
		seen := map[string]bool{}
		result := []any{}
		for _, member := range members {
			if !Contains(param.Values, member) {
				return invalid(ReasonNotInVocabulary)
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
			return invalid(ReasonWrongType)
		}
		return flag, nil, nil
	case KindInteger, KindNumber:
		var number float64
		if json.Unmarshal(raw, &number) != nil {
			return invalid(ReasonWrongType)
		}
		if param.Kind == KindInteger && number != math.Trunc(number) {
			return invalid(ReasonWrongType)
		}
		if number < param.Min || (param.HasMax && number > param.Max) {
			return invalid(ReasonOutOfRange)
		}
		return number, nil, nil
	case KindStringList:
		var members []string
		if json.Unmarshal(raw, &members) != nil {
			return invalid(ReasonWrongType)
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
		return echo, nil, []referenceAt{{Path: path, Key: param.Key, Series: series,
			Reference: reference, expected: param.Reference, Echo: echo}}
	case KindReferenceList:
		var members []json.RawMessage
		if json.Unmarshal(raw, &members) != nil {
			return invalid(ReasonWrongType)
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
			references = append(references, referenceAt{Path: fmt.Sprintf("%s[%d]", path, index),
				Key: param.Key, Series: series, Reference: reference, expected: param.Reference, Echo: echo})
		}
		return result, nil, references
	case KindObject:
		var object map[string]any
		if json.Unmarshal(raw, &object) != nil {
			return invalid(ReasonWrongType)
		}
		return object, nil, nil
	}
	return invalid(ReasonWrongType)
}

// isJSONNull reports whether a raw value is the JSON null.
func isJSONNull(raw json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

// Contains reports whether a vocabulary holds a value.
func Contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

// The typed reads of an effective parameter. A handler reads only keys its preset
// declares, so a missing key is a defect in the preset and reads as the zero value.

func (p ParsedParameters) Text(key string) string {
	value, _ := p.Effective[key].(string)
	return value
}

func (p ParsedParameters) Flag(key string) bool {
	value, _ := p.Effective[key].(bool)
	return value
}

func (p ParsedParameters) Integer(key string) int64 {
	switch value := p.Effective[key].(type) {
	case float64:
		return int64(value)
	case int:
		return int64(value)
	case int64:
		return value
	}
	return 0
}

func (p ParsedParameters) Number(key string) float64 {
	switch value := p.Effective[key].(type) {
	case float64:
		return value
	case int:
		return float64(value)
	case int64:
		return float64(value)
	}
	return 0
}

func (p ParsedParameters) Set(key string) []string {
	members, _ := p.Effective[key].([]any)
	result := make([]string, 0, len(members))
	for _, member := range members {
		if text, ok := member.(string); ok {
			result = append(result, text)
		}
	}
	return result
}

func (p ParsedParameters) Has(key, member string) bool {
	return Contains(p.Set(key), member)
}

// The volume_measure parameter, and its value beyond the measures.
const (
	VolumeMeasureKey           = "volume_measure"
	VolumeMeasureBestAvailable = "best_available"
)
