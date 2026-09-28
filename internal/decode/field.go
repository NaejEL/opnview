// Package decode reads what an OPNsense endpoint actually said, and turns it into the shapes
// the schema stores. Two things, and nothing else: the tolerant field readers over a decoded
// JSON response, and the normalisation of the three timestamp shapes into the UTC epoch every
// column holds.
//
// WHY IT IS ITS OWN PACKAGE, and it is not for a directory layout. These functions encode
// MEASURED facts about the firewall's API, and they are the layer that enforces the rule the
// whole product rests on — a field that is absent, or present in a shape that cannot be read,
// is reported as ABSENT, so that nothing downstream ever writes a zero that means "we could not
// look". Inside the collector they were exercised only incidentally, through whichever
// implementation happened to meet a shape; here they are tested directly against the shapes
// docs/opnsense-api-survey.md records, and the tests can name the measurement each case comes
// from.
//
// WHY THE NAME IS `decode`. OPNsense has no word for this — it has no such layer, which is the
// reason this one exists — so the term is opnview's own and is recorded here as CLAUDE.md
// requires. `decode` was chosen over a collector-flavoured name because both sides of the
// seam this layer serves need it: when connectors become separate processes, a connector
// decodes its own product's answers and normalises its own timestamps, and a package named for
// the host would then be named for the wrong half. It reads a source's answer; it knows nothing
// about kinds, providers, the store, or what any of it is for.
//
// WHAT IS NOT HERE, because it was here and did not belong: folding several failures into one
// belongs to a pass that reads five kinds, and truncating an epoch to its UTC day is arithmetic
// on a value opnview already owns rather than on anything a source said. Both stayed in
// internal/collect, each with a line saying why.
package decode

import (
	"encoding/json"
	"strconv"
	"strings"
)

// The field readers exist for a documented reason rather than out of laziness. OPNsense's
// Mvc\Request reads $_REQUEST uncast, so the same logical field comes back as a
// JSON number from one endpoint and as a string from another depending on how the
// backend assembled the response; the survey establishes the FIELD NAMES of the
// five data sources and, for the telemetry endpoints the verified section added,
// it establishes the endpoints and NOT their field names at all. Decoding into a
// rigid struct would therefore either fail on a type nobody wrote down or, worse,
// silently read a zero.
//
// The rule these functions enforce is the one that matters: a field that is
// absent, or present in a shape that cannot be read, is reported as ABSENT — and
// every caller treats absent as a state, never as a zero.

// Object is a decoded JSON object.
type Object = map[string]any

// String returns a string field, and whether it was present and non-empty.
// A number is rendered, because a port or a length arrives either way.
func String(object Object, key string) (string, bool) {
	raw, present := object[key]
	if !present || raw == nil {
		return "", false
	}
	switch typed := raw.(type) {
	case string:
		trimmed := strings.TrimSpace(typed)
		return trimmed, trimmed != ""
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64), true
	case bool:
		return strconv.FormatBool(typed), true
	default:
		return "", false
	}
}

// StringPointer returns a string field as a nullable column value. Absent and
// empty both become nil, which is the "not reported" the nullable columns carry.
func StringPointer(object Object, key string) *string {
	value, present := String(object, key)
	if !present {
		return nil
	}
	return &value
}

// Int returns an integer field, and whether it was present and readable.
func Int(object Object, key string) (int64, bool) {
	raw, present := object[key]
	if !present || raw == nil {
		return 0, false
	}
	switch typed := raw.(type) {
	case float64:
		return int64(typed), true
	case string:
		trimmed := strings.TrimSpace(typed)
		if trimmed == "" {
			return 0, false
		}
		if value, err := strconv.ParseInt(trimmed, 10, 64); err == nil {
			return value, true
		}
		if value, err := strconv.ParseFloat(trimmed, 64); err == nil {
			return int64(value), true
		}
		return 0, false
	case bool:
		if typed {
			return 1, true
		}
		return 0, true
	default:
		return 0, false
	}
}

// IntPointer returns an integer field as a nullable column value.
func IntPointer(object Object, key string) *int64 {
	value, present := Int(object, key)
	if !present {
		return nil
	}
	return &value
}

// Float returns a floating-point field, and whether it was present and
// readable. A percentage written with a trailing sign is accepted, because the
// telemetry endpoints' shapes are not established and one of them is known to
// render figures for display.
func Float(object Object, key string) (float64, bool) {
	raw, present := object[key]
	if !present || raw == nil {
		return 0, false
	}
	switch typed := raw.(type) {
	case float64:
		return typed, true
	case string:
		trimmed := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(typed), "%"))
		if trimmed == "" {
			return 0, false
		}
		if value, err := strconv.ParseFloat(trimmed, 64); err == nil {
			return value, true
		}
		return 0, false
	default:
		return 0, false
	}
}

// FirstFloat returns the first of several candidate keys that holds a readable
// number, the key it came from, and whether any did.
//
// It is used only against the telemetry endpoints, whose field names the survey
// does not establish. The candidate list is an UNVERIFIED assumption, marked as
// such at each call site, and returning the key that answered is what lets the
// end of 4B record which one a real firewall uses instead of guessing again.
func FirstFloat(object Object, keys ...string) (float64, string, bool) {
	for _, key := range keys {
		if value, present := Float(object, key); present {
			return value, key, true
		}
	}
	return 0, "", false
}

// Flag returns a three-state flag: true, false, or not reported. OPNsense
// writes these as 0/1, as "0"/"1", and as booleans, depending on the endpoint.
func Flag(object Object, key string) *bool {
	value, present := Int(object, key)
	if !present {
		return nil
	}
	flag := value != 0
	return &flag
}

// Objects returns the objects a collection holds, from either shape OPNsense uses: a JSON array,
// or a map keyed by something — an interface identifier, a device name.
//
// WHAT IT DOES NOT DO, stated because the comment here used to claim the opposite and the claim
// was never true: a lone object is NOT returned as a list of one. It cannot be. A map whose
// values are the rows and a single row that happens to be a map are the same JSON, so a decoder
// that accepted both would read `{"address": "..."}` as one row on some endpoints and as a
// keyed collection of none on others, and the choice would fall to whichever field types
// happened to be present. The keyed reading is the one the API requires — measured:
// /api/diagnostics/traffic/top/ answers per interface under the interface's own name — so it is
// the only one, and a caller that needs the key reads the map itself rather than this list.
// A lone object therefore yields the objects nested inside it, which for a row of scalars is
// none: the caller sees zero rows and records that, rather than a row assembled by guesswork.
func Objects(container any) []Object {
	switch typed := container.(type) {
	case []any:
		objects := make([]Object, 0, len(typed))
		for _, element := range typed {
			if object, ok := element.(Object); ok {
				objects = append(objects, object)
			}
		}
		return objects
	case Object:
		// A map keyed by something — an interface identifier, a device name — is
		// the other shape OPNsense uses for a collection. The key is not lost: a
		// caller that needs it reads the map itself.
		objects := make([]Object, 0, len(typed))
		for _, element := range typed {
			if object, ok := element.(Object); ok {
				objects = append(objects, object)
			}
		}
		return objects
	default:
		return nil
	}
}

// Nested walks a dotted path into a decoded object, the way OPNsense's
// model responses nest — `ids.general.enabled`, `dhcpv4.general.enabled`,
// `unbound.general.enabled`.
func Nested(object Object, path ...string) (Object, bool) {
	current := object
	for _, step := range path {
		next, present := current[step]
		if !present {
			return nil, false
		}
		asObject, ok := next.(Object)
		if !ok {
			return nil, false
		}
		current = asObject
	}
	return current, true
}

// NestedString reads a leaf at a dotted path.
func NestedString(object Object, path ...string) (string, bool) {
	if len(path) == 0 {
		return "", false
	}
	parent, present := Nested(object, path[:len(path)-1]...)
	if !present {
		return "", false
	}
	return String(parent, path[len(path)-1])
}

// NestedFlag reads a three-state flag at a dotted path.
func NestedFlag(object Object, path ...string) *bool {
	if len(path) == 0 {
		return nil
	}
	parent, present := Nested(object, path[:len(path)-1]...)
	if !present {
		return nil
	}
	return Flag(parent, path[len(path)-1])
}

// Epoch reads an instant a source already expresses as epoch seconds.
func Epoch(row Object, key string) (int64, bool) {
	raw, present := row[key]
	if !present || raw == nil {
		return 0, false
	}
	epoch, err := NormaliseEpoch(raw)
	if err != nil {
		return 0, false
	}
	return epoch, true
}

// Port reads a port, rejecting a value outside the range the schema constrains rather
// than storing one the database would refuse.
func Port(record Object, key string) *int64 {
	value, present := Int(record, key)
	if !present || value < 0 || value > 65535 {
		return nil
	}
	return &value
}

// RawString reads a field as a string, empty when absent.
func RawString(record Object, key string) string {
	value, _ := String(record, key)
	return value
}

// EpochPointer reads an instant a source already expresses as epoch seconds.
func EpochPointer(row Object, key string) *int64 {
	raw, present := row[key]
	if !present || raw == nil {
		return nil
	}
	epoch, err := NormaliseEpoch(raw)
	if err != nil || epoch <= 0 {
		return nil
	}
	return &epoch
}

// NonEmpty reports whether a decoded JSON value holds anything. It is used on
// dnsmasq.dhcp_ranges, where only emptiness is read: the ranges themselves are the firewall's
// addressing configuration, which opnview neither stores nor interprets.
func NonEmpty(value any) bool {
	switch typed := value.(type) {
	case nil:
		return false
	case string:
		return typed != ""
	case []any:
		return len(typed) > 0
	case map[string]any:
		return len(typed) > 0
	case float64:
		return typed != 0
	case bool:
		return typed
	default:
		return true
	}
}

// Rows decodes a grid-search envelope, or a bare array, or a map keyed by something. All
// three shapes occur across the endpoints this cycle calls, and which one a given endpoint uses
// is not always established by the survey.
func Rows(body []byte) ([]Object, error) {
	var envelope struct {
		Rows json.RawMessage `json:"rows"`
	}
	if err := json.Unmarshal(body, &envelope); err == nil && len(envelope.Rows) > 0 {
		var rows any
		if err := json.Unmarshal(envelope.Rows, &rows); err != nil {
			return nil, err
		}
		return Objects(rows), nil
	}

	var bare any
	if err := json.Unmarshal(body, &bare); err != nil {
		return nil, err
	}
	return Objects(bare), nil
}

// FlagOrFalse reads a three-state flag as a two-state one, for a column that has only two
// states.
func FlagOrFalse(flag *bool) bool { return flag != nil && *flag }

// LowerASCII lower-cases the ASCII letters of s and leaves everything else. It is used only on
// values whose vocabulary the survey establishes, never to compare a name, a description or a
// label.
func LowerASCII(s string) string {
	out := []byte(s)
	for i := range out {
		if out[i] >= 'A' && out[i] <= 'Z' {
			out[i] += 'a' - 'A'
		}
	}
	return string(out)
}

// UpperASCII upper-cases the ASCII letters of s. It exists for one purpose: to reproduce the
// endpoint's own documented fallback for a missing interface description, which is the
// upper-cased identifier.
func UpperASCII(s string) string {
	out := []byte(s)
	for i := range out {
		if out[i] >= 'a' && out[i] <= 'z' {
			out[i] -= 'a' - 'A'
		}
	}
	return string(out)
}
