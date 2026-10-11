package answer

import (
	"sort"
	"strings"
	"sync"
)

// Field paths.
//
// A path names a field of an answer whatever row it is in: `total`, `rows[].hwaddr`,
// `interfaces.*.name`. An array's items are `[]`, a value-keyed object's members are `*`, and a
// field name holding a character of the syntax -- '.', '[', ']', '*' or '\' -- is escaped with
// a backslash. The root is the empty path.

// AnyItem and AnyMember are the two wildcard steps.
const (
	AnyItem   = "[]"
	AnyMember = "*"
)

// Join appends one step to a path: a field name, AnyItem or AnyMember.
func Join(path, step string) string {
	switch {
	case step == AnyItem:
		return path + AnyItem
	case path == "":
		if step == AnyMember {
			return AnyMember
		}
		return escape(step)
	case step == AnyMember:
		return path + "." + AnyMember
	}
	return path + "." + escape(step)
}

func escape(name string) string {
	if !strings.ContainsAny(name, `.[]*\`) {
		return name
	}
	var out strings.Builder
	for _, character := range name {
		if strings.ContainsRune(`.[]*\`, character) {
			out.WriteByte('\\')
		}
		out.WriteRune(character)
	}
	return out.String()
}

// Steps splits a path into its steps, the inverse of Join.
func Steps(path string) []string {
	var (
		steps   []string
		current strings.Builder
		escaped bool
		started bool
	)
	flush := func() {
		if started {
			steps = append(steps, current.String())
		}
		current.Reset()
		started = false
	}
	for index := 0; index < len(path); index++ {
		character := path[index]
		switch {
		case escaped:
			current.WriteByte(character)
			escaped, started = false, true
		case character == '\\':
			escaped, started = true, true
		case character == '.':
			flush()
		case character == '[' && index+1 < len(path) && path[index+1] == ']':
			flush()
			steps = append(steps, AnyItem)
			index++
		case character == '*' && !started:
			steps = append(steps, AnyMember)
		default:
			current.WriteByte(character)
			started = true
		}
	}
	flush()
	return steps
}

// Under reports whether path is prefix itself or lies below it.
func Under(path, prefix string) bool {
	if prefix == "" || path == prefix {
		return true
	}
	if !strings.HasPrefix(path, prefix) {
		return false
	}
	rest := path[len(prefix):]
	return strings.HasPrefix(rest, ".") || strings.HasPrefix(rest, AnyItem)
}

// Reads is what one reader read of one answer: the field paths, and the paths read whole -- the
// body's text, a json() call, an object enumerated or serialised -- below which every field
// counts as read.
type Reads struct {
	mutex  sync.Mutex
	fields map[string]bool
	whole  map[string]bool
}

// NewReads returns an empty record.
func NewReads() *Reads { return &Reads{fields: map[string]bool{}, whole: map[string]bool{}} }

// Field records one field read.
func (r *Reads) Field(path string) {
	r.mutex.Lock()
	r.fields[path] = true
	r.mutex.Unlock()
}

// Whole records a path read whole.
func (r *Reads) Whole(path string) {
	r.mutex.Lock()
	r.whole[path] = true
	r.mutex.Unlock()
}

// Fields returns the field paths read, sorted.
func (r *Reads) Fields() []string {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	return sortedSet(r.fields)
}

// WholePaths returns the paths read whole, sorted.
func (r *Reads) WholePaths() []string {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	return sortedSet(r.whole)
}

// Empty reports whether nothing was read.
func (r *Reads) Empty() bool {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	return len(r.fields) == 0 && len(r.whole) == 0
}

func sortedSet(set map[string]bool) []string {
	result := make([]string, 0, len(set))
	for path := range set {
		result = append(result, path)
	}
	sort.Strings(result)
	return result
}

// Covers reports whether a path is among the read fields, or below a path read whole.
func Covers(fields, whole []string, path string) bool {
	for _, prefix := range whole {
		if Under(path, prefix) {
			return true
		}
	}
	for _, field := range fields {
		if field == path {
			return true
		}
	}
	return false
}
