package infer

import (
	"net"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/NaejEL/opnview/internal/broker/answer"
	"github.com/NaejEL/opnview/internal/decode"
)

// The inferred types of a field (A10).
const (
	TypeIP        = "ip_address"
	TypeMAC       = "mac_address"
	TypeTimestamp = "timestamp"
	TypeNumber    = "number"
	TypeText      = "text"
	TypeBoolean   = "boolean"
	// TypeJSON is a value whose type is ambiguous or changes: stored as JSONB.
	TypeJSON = "json"
)

// Types is every type a field can be inferred or corrected to.
func Types() []string {
	return []string{TypeIP, TypeMAC, TypeTimestamp, TypeNumber, TypeText, TypeBoolean, TypeJSON}
}

// The natures of a datum.
const (
	// NatureSnapshot is current state: leases, interfaces. A change of a stored field of a row
	// writes a history row.
	NatureSnapshot = "snapshot"
	// NatureRecordStream is records that accumulate: logs, lookups. Reads are incremental on a
	// cursor.
	NatureRecordStream = "record_stream"
)

// The roles of a request parameter.
const (
	RoleWindowStart = "window_start"
	RoleWindowEnd   = "window_end"
	RolePage        = "page"
	RolePageSize    = "page_size"
	// RoleIdentifying is every other parameter: each combination of them is one datum.
	RoleIdentifying = "identifying"
	// RoleIgnored is a parameter a correction declares the firewall ignores: it is sent as the
	// requester gave it, identifies no datum and is never relied on (the filter log's digest).
	RoleIgnored = "ignored"
)

// Roles is every role a parameter can be inferred or corrected to.
func Roles() []string {
	return []string{RoleWindowStart, RoleWindowEnd, RolePage, RolePageSize, RoleIdentifying, RoleIgnored}
}

// Corrections are what a plugin's manifest corrects for one endpoint. Every member is optional.
type Corrections struct {
	Nature string `json:"nature,omitempty"`
	// RowSet is the path of the answer's rows, such as rows[] or [].
	RowSet string `json:"rows,omitempty"`
	// Key and Cursor are field names of a row, each possibly composite.
	Key    []string `json:"key,omitempty"`
	Cursor []string `json:"cursor,omitempty"`
	// Parameters gives a role to request parameters, by name.
	Parameters map[string]string `json:"parameters,omitempty"`
	// Types gives a type to fields, by path.
	Types map[string]string `json:"types,omitempty"`
	// Domains says whether fields carry a domain name, by path.
	Domains map[string]bool `json:"domains,omitempty"`
	// ValueKeyed names objects whose keys are values, by path.
	ValueKeyed []string `json:"value_keyed,omitempty"`
	// DedupeOnContent declares a record stream whose rows have no key and are told apart by
	// their stored content (Unbound's lookups).
	DedupeOnContent bool `json:"dedupe_on_content,omitempty"`
}

// Empty reports whether the corrections correct nothing.
func (c Corrections) Empty() bool {
	return c.Nature == "" && c.RowSet == "" && len(c.Key) == 0 && len(c.Cursor) == 0 && len(c.Parameters) == 0 &&
		len(c.Types) == 0 && len(c.Domains) == 0 && len(c.ValueKeyed) == 0 && !c.DedupeOnContent
}

// Shape is what was learned of one answer.
type Shape struct {
	// RowSet is the path of the rows: rows[], [], or "" when the answer is one object.
	RowSet string
	Nature string
	// Key is the identity key of a row, empty when none was found.
	Key []string
	// Cursor is the cursor of a record stream, and CursorType timestamp or number;
	// CursorDescending says the answer lists newest first.
	Cursor           []string
	CursorType       string
	CursorDescending bool
	// Types are the leaf fields' types, by path from the answer's root.
	Types map[string]string
	// Domains are the text fields carrying a domain name, by path.
	Domains map[string]bool
	// ValueKeyed are the paths of objects stored as rows keyed by their key.
	ValueKeyed []string
	// DedupeOnContent says the rows are told apart by their content.
	DedupeOnContent bool
}

// Vocabulary is the installation's own names the inference may recognise as keys of a
// value-keyed object: interface identifiers and network devices, read by discovery. It is never
// written into a schema; it only tells a key that is a value from one that is a field name.
type Vocabulary func() []string

// Learn infers an answer's shape and marks its value-keyed objects in place. A shape -- which
// field is a timestamp, which orders the rows -- does not depend on the firewall's offset from
// UTC, so it is learned whether or not the offset has been read; no instant is kept from it.
func Learn(value *answer.Value, corrections Corrections, vocabulary Vocabulary, normaliser decode.Normaliser) Shape {
	normaliser = normaliser.Relative()
	shape := Shape{Types: map[string]string{}, Domains: map[string]bool{}}
	names := map[string]bool{}
	if vocabulary != nil {
		for _, name := range vocabulary() {
			names[name] = true
		}
	}
	corrected := map[string]bool{}
	for _, path := range corrections.ValueKeyed {
		corrected[path] = true
	}
	markValueKeyed(value, "", names, corrected, &shape.ValueKeyed)
	sort.Strings(shape.ValueKeyed)

	kinds := map[string]map[answer.Kind]bool{}
	samples := map[string][]string{}
	collect(value, "", kinds, samples)
	for path, set := range kinds {
		shape.Types[path] = typeOf(path, set, samples[path], normaliser)
		if shape.Types[path] == TypeText && hostNames(samples[path]) {
			shape.Domains[path] = true
		}
	}
	for path, kind := range corrections.Types {
		shape.Types[path] = kind
	}
	for path, carries := range corrections.Domains {
		if carries {
			shape.Domains[path] = true
		} else {
			delete(shape.Domains, path)
		}
	}

	shape.RowSet = corrections.RowSet
	if shape.RowSet == "" {
		shape.RowSet = mainRowSet(value)
	}
	rows := RowsAt(value, shape.RowSet)
	shape.Cursor, shape.CursorType, shape.CursorDescending = cursorOf(rows, shape, normaliser)
	shape.Nature = NatureSnapshot
	if len(shape.Cursor) > 0 {
		shape.Nature = NatureRecordStream
	}
	if corrections.Nature != "" {
		shape.Nature = corrections.Nature
	}
	if len(corrections.Cursor) > 0 {
		shape.Cursor = corrections.Cursor
		shape.CursorType = TypeTimestamp
		if len(corrections.Cursor) == 1 {
			if kind := shape.Types[answer.Join(shape.RowSet, corrections.Cursor[0])]; kind == TypeNumber {
				shape.CursorType = TypeNumber
			}
		} else {
			shape.CursorType = TypeNumber
		}
		_, _, shape.CursorDescending = directionOf(rows, shape.Cursor, shape.CursorType, normaliser)
	}
	shape.Key = keyOf(rows, shape)
	if len(corrections.Key) > 0 {
		shape.Key = corrections.Key
	}
	shape.DedupeOnContent = corrections.DedupeOnContent
	if shape.DedupeOnContent && len(corrections.Key) == 0 {
		// Rows told apart by their content have no key.
		shape.Key = nil
	}
	return shape
}

// fieldName is what a stable field name looks like; a key that is not one, or that is one of the
// installation's own names, makes its object value-keyed.
var fieldName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// markValueKeyed marks the objects whose keys are values: a corrected path; an object whose keys
// include one of the installation's names or a key that is no field name (an address, a number);
// or an object of two or more objects sharing most of their keys.
func markValueKeyed(value *answer.Value, path string, names, corrected map[string]bool, marked *[]string) {
	if value == nil {
		return
	}
	switch value.Kind {
	case answer.Array:
		for _, item := range value.Items {
			markValueKeyed(item, answer.Join(path, answer.AnyItem), names, corrected, marked)
		}
	case answer.Object:
		if corrected[path] || valueKeyed(value, names) {
			if !value.ValueKeyed {
				value.ValueKeyed = true
			}
			if !contains(*marked, path) {
				*marked = append(*marked, path)
			}
			for _, key := range value.Keys {
				markValueKeyed(value.Fields[key], answer.Join(path, answer.AnyMember), names, corrected, marked)
			}
			return
		}
		for _, key := range value.Keys {
			markValueKeyed(value.Fields[key], answer.Join(path, key), names, corrected, marked)
		}
	}
}

func valueKeyed(object *answer.Value, names map[string]bool) bool {
	if len(object.Keys) == 0 {
		return false
	}
	for _, key := range object.Keys {
		if names[key] || !fieldName.MatchString(key) {
			return true
		}
	}
	if len(object.Keys) < 2 {
		return false
	}
	// An object of objects sharing their keys is rows keyed by their key.
	shared := map[string]int{}
	for _, key := range object.Keys {
		member := object.Fields[key]
		if member == nil || member.Kind != answer.Object || len(member.Keys) == 0 {
			return false
		}
		for _, inner := range member.Keys {
			shared[inner]++
		}
	}
	common := 0
	for _, count := range shared {
		if count == len(object.Keys) {
			common++
		}
	}
	return common*2 >= len(shared) && common > 0
}

// collect gathers every leaf path's kinds and a sample of its string values.
func collect(value *answer.Value, path string, kinds map[string]map[answer.Kind]bool, samples map[string][]string) {
	if value == nil {
		return
	}
	switch value.Kind {
	case answer.Object:
		for _, key := range value.Keys {
			step := key
			if value.ValueKeyed {
				step = answer.AnyMember
			}
			collect(value.Fields[key], answer.Join(path, step), kinds, samples)
		}
	case answer.Array:
		for _, item := range value.Items {
			collect(item, answer.Join(path, answer.AnyItem), kinds, samples)
		}
	default:
		if kinds[path] == nil {
			kinds[path] = map[answer.Kind]bool{}
		}
		kinds[path][value.Kind] = true
		if (value.Kind == answer.String || value.Kind == answer.Number) && len(samples[path]) < 64 {
			samples[path] = append(samples[path], value.Text)
		}
	}
}

// typeOf infers a leaf's type from the kinds it was seen with and a sample of its values.
func typeOf(path string, kinds map[answer.Kind]bool, sample []string, normaliser decode.Normaliser) string {
	delete(kinds, answer.Null)
	switch {
	case len(kinds) == 0:
		return TypeText
	case len(kinds) > 1:
		return TypeJSON
	case kinds[answer.Bool]:
		return TypeBoolean
	case kinds[answer.Number]:
		if timeName(path) && epochs(sample) {
			return TypeTimestamp
		}
		return TypeNumber
	}
	nonEmpty := 0
	ips, macs, stamps := 0, 0, 0
	for _, text := range sample {
		if text == "" {
			continue
		}
		nonEmpty++
		if net.ParseIP(text) != nil {
			ips++
		}
		if macShape.MatchString(text) {
			macs++
		}
		if Timestamp(text, normaliser) {
			stamps++
		}
	}
	switch {
	case nonEmpty == 0:
		return TypeText
	case ips == nonEmpty:
		return TypeIP
	case macs == nonEmpty:
		return TypeMAC
	case stamps == nonEmpty:
		return TypeTimestamp
	}
	return TypeText
}

var macShape = regexp.MustCompile(`^[0-9A-Fa-f]{2}([:-][0-9A-Fa-f]{2}){5}$`)

// hostShape is a host name: two or more dot-separated labels, the last alphabetic.
var hostShape = regexp.MustCompile(`^([A-Za-z0-9_]([A-Za-z0-9_-]{0,61}[A-Za-z0-9_])?\.)+[A-Za-z]{2,63}\.?$`)

// hostNames reports whether a text field's values have host-name shape (A10, "Domain names").
func hostNames(sample []string) bool {
	seen := 0
	for _, text := range sample {
		if text == "" {
			continue
		}
		if net.ParseIP(text) != nil || !hostShape.MatchString(text) {
			return false
		}
		seen++
	}
	return seen > 0
}

// timeName reports whether a field's name says it holds an instant.
func timeName(path string) bool {
	steps := answer.Steps(path)
	if len(steps) == 0 {
		return false
	}
	name := strings.ToLower(steps[len(steps)-1])
	for _, hint := range []string{"time", "timestamp", "date", "expire", "starts", "ends", "cltt", "last_seen",
		"first_seen"} {
		if strings.Contains(name, hint) {
			return true
		}
	}
	return strings.HasSuffix(name, "_at")
}

// epochs reports whether numbers read as instants between 2000 and 2100.
func epochs(sample []string) bool {
	if len(sample) == 0 {
		return false
	}
	for _, text := range sample {
		value, err := strconv.ParseFloat(text, 64)
		if err != nil || value < 946684800 || value >= 4102444800 {
			return false
		}
	}
	return true
}

// DependsOnOffset reports whether an answer holds a timestamp read as the firewall's local time
// while the normaliser does not know the firewall's offset from UTC: an instant nothing may be
// derived from yet (N4, live validation of 17 October 2026).
func DependsOnOffset(value *answer.Value, normaliser decode.Normaliser) bool {
	if value == nil || !normaliser.OffsetUnknown {
		return false
	}
	switch value.Kind {
	case answer.String:
		_, strict := Instant(value, normaliser)
		_, relative := Instant(value, normaliser.Relative())
		return relative && !strict
	case answer.Object:
		for _, field := range value.Fields {
			if DependsOnOffset(field, normaliser) {
				return true
			}
		}
	case answer.Array:
		for _, item := range value.Items {
			if DependsOnOffset(item, normaliser) {
				return true
			}
		}
	}
	return false
}

// Timestamp reports whether a text is in one of the shapes internal/decode reads. Whether it is
// does not depend on the firewall's offset from UTC.
func Timestamp(text string, normaliser decode.Normaliser) bool {
	normaliser = normaliser.Relative()
	if _, err := normaliser.NormaliseISO(text); err == nil {
		return true
	}
	if !normaliser.Reference.IsZero() {
		if _, err := normaliser.NormaliseSyslog(text); err == nil {
			return true
		}
	}
	return false
}

// Instant reads a cursor value as an epoch, in the shapes internal/decode reads.
func Instant(value *answer.Value, normaliser decode.Normaliser) (int64, bool) {
	if value == nil {
		return 0, false
	}
	switch value.Kind {
	case answer.Number:
		number, err := strconv.ParseFloat(value.Text, 64)
		return int64(number), err == nil
	case answer.String:
		if epoch, err := normaliser.NormaliseISO(value.Text); err == nil {
			return epoch, true
		}
		if !normaliser.Reference.IsZero() {
			if epoch, err := normaliser.NormaliseSyslog(value.Text); err == nil {
				return epoch, true
			}
		}
		if epoch, err := decode.NormaliseEpoch(value.Text); err == nil {
			return epoch, true
		}
	}
	return 0, false
}

// mainRowSet is the answer's rows: the largest array of objects, or the answer itself when it is
// one object.
func mainRowSet(value *answer.Value) string {
	best, size := "", 0
	var walk func(*answer.Value, string)
	walk = func(node *answer.Value, path string) {
		if node == nil {
			return
		}
		switch node.Kind {
		case answer.Array:
			rows := 0
			for _, item := range node.Items {
				if item != nil && item.Kind == answer.Object {
					rows++
				}
			}
			if rows > size || (rows == size && rows > 0 && len(path) < len(best)) {
				best, size = answer.Join(path, answer.AnyItem), rows
			}
			for _, item := range node.Items {
				walk(item, answer.Join(path, answer.AnyItem))
			}
		case answer.Object:
			if node.ValueKeyed && len(node.Keys) > size {
				best, size = answer.Join(path, answer.AnyMember), len(node.Keys)
			}
			for _, key := range node.Keys {
				step := key
				if node.ValueKeyed {
					step = answer.AnyMember
				}
				walk(node.Fields[key], answer.Join(path, step))
			}
		}
	}
	walk(value, "")
	if size == 0 && value != nil && value.Kind == answer.Array {
		return answer.AnyItem
	}
	return best
}

// RowsAt returns the rows at a row-set path, with the key of each when the row set is a
// value-keyed object.
func RowsAt(value *answer.Value, rowSet string) []*answer.Value {
	if rowSet == "" {
		if value == nil {
			return nil
		}
		return []*answer.Value{value}
	}
	nodes := []*answer.Value{value}
	for _, step := range answer.Steps(rowSet) {
		var next []*answer.Value
		for _, node := range nodes {
			if node == nil {
				continue
			}
			switch step {
			case answer.AnyItem:
				if node.Kind == answer.Array {
					next = append(next, node.Items...)
				}
			case answer.AnyMember:
				if node.Kind == answer.Object {
					for _, key := range node.Keys {
						next = append(next, node.Fields[key])
					}
				}
			default:
				if member := node.Field(step); member != nil {
					next = append(next, member)
				}
			}
		}
		nodes = next
	}
	return nodes
}

// keyNames are the field names an identity key is looked for under, in order of preference.
var keyNames = []string{"id", "uuid", "__digest__", "digest", "hwaddr", "mac", "macaddr", "address", "ip",
	"ip_address", "ipaddr", "identifier", "name"}

// keyOf finds a row's identity key: a field of a preferred name, or typed as a MAC or IP address,
// that is non-null on every row and unique within the answer. A field null on every row is never
// a key.
func keyOf(rows []*answer.Value, shape Shape) []string {
	if len(rows) < 1 {
		return nil
	}
	var candidates []string
	candidates = append(candidates, keyNames...)
	for path, kind := range shape.Types {
		if kind != TypeMAC && kind != TypeIP {
			continue
		}
		prefix := shape.RowSet
		if !answer.Under(path, prefix) || path == prefix {
			continue
		}
		steps := answer.Steps(strings.TrimPrefix(strings.TrimPrefix(path, prefix), "."))
		if len(steps) == 1 {
			candidates = append(candidates, steps[0])
		}
	}
	for _, name := range candidates {
		seen := map[string]bool{}
		valid := true
		for _, row := range rows {
			field := row.Field(name)
			if field == nil || !field.Scalar() || field.Kind == answer.Null || field.Text == "" || seen[field.Text] {
				valid = false
				break
			}
			seen[field.Text] = true
		}
		if valid {
			return []string{name}
		}
	}
	return nil
}

// cursorNames are numeric fields a record stream may be ordered by.
var cursorNames = []string{"id", "seq", "sequence", "rowid", "line"}

// cursorOf finds a record stream's cursor: a timestamp field, or a numeric id, whose values are
// ordered across the rows -- ascending or descending -- and not all equal.
func cursorOf(rows []*answer.Value, shape Shape, normaliser decode.Normaliser) ([]string, string, bool) {
	if len(rows) < 3 || shape.RowSet == "" {
		return nil, "", false
	}
	var names []string
	for path, kind := range shape.Types {
		if kind != TypeTimestamp || !answer.Under(path, shape.RowSet) || path == shape.RowSet {
			continue
		}
		steps := answer.Steps(strings.TrimPrefix(strings.TrimPrefix(path, shape.RowSet), "."))
		if len(steps) == 1 {
			names = append(names, steps[0])
		}
	}
	sort.Strings(names)
	for _, name := range names {
		if ordered, _, descending := directionOf(rows, []string{name}, TypeTimestamp, normaliser); ordered {
			return []string{name}, TypeTimestamp, descending
		}
	}
	for _, name := range cursorNames {
		if kind := shape.Types[answer.Join(shape.RowSet, name)]; kind != TypeNumber {
			continue
		}
		if ordered, _, descending := directionOf(rows, []string{name}, TypeNumber, normaliser); ordered {
			return []string{name}, TypeNumber, descending
		}
	}
	return nil, "", false
}

// directionOf reports whether the rows are ordered on a cursor, and which way.
func directionOf(rows []*answer.Value, cursor []string, kind string, normaliser decode.Normaliser) (bool, bool,
	bool) {
	var values []float64
	for _, row := range rows {
		value, ok := CursorValue(row, cursor, normaliser)
		if !ok {
			return false, false, false
		}
		values = append(values, value)
	}
	ascending, descending, distinct := true, true, false
	for index := 1; index < len(values); index++ {
		if values[index] < values[index-1] {
			ascending = false
		}
		if values[index] > values[index-1] {
			descending = false
		}
		if values[index] != values[index-1] {
			distinct = true
		}
	}
	return (ascending || descending) && distinct, ascending, descending && !ascending
}

// CursorValue reads a row's cursor as one number: an instant for a timestamp, the number itself,
// or for a composite cursor its parts weighted in order (the first the most significant).
func CursorValue(row *answer.Value, cursor []string, normaliser decode.Normaliser) (float64, bool) {
	if row == nil || len(cursor) == 0 {
		return 0, false
	}
	total := 0.0
	for index, name := range cursor {
		field := row.Field(name)
		if field == nil {
			return 0, false
		}
		var part float64
		if instant, ok := Instant(field, normaliser); ok && field.Kind == answer.String {
			part = float64(instant)
		} else {
			number, err := strconv.ParseFloat(field.Text, 64)
			if err != nil {
				return 0, false
			}
			part = number
		}
		if index == 0 {
			total = part
			continue
		}
		// A composite cursor (fileid, filepos): the first part dominates.
		total = total*1e12 + part
	}
	return total, true
}

// ParameterRole infers one request parameter's role from its name.
func ParameterRole(name string) string {
	switch strings.ToLower(strings.ReplaceAll(name, "_", "")) {
	case "timestart", "starttime", "start", "from", "since", "after":
		return RoleWindowStart
	case "timeend", "endtime", "end", "to", "until", "before":
		return RoleWindowEnd
	case "current", "page", "offset":
		return RolePage
	case "rowcount", "limit", "pagesize", "perpage":
		return RolePageSize
	}
	return RoleIdentifying
}

// DefaultNormaliser is the normaliser inference uses when none is given: UTC, now.
func DefaultNormaliser() decode.Normaliser { return decode.Normaliser{Reference: time.Now().UTC()} }

func contains(values []string, value string) bool {
	for _, held := range values {
		if held == value {
			return true
		}
	}
	return false
}
