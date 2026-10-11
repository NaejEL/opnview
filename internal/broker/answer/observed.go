package answer

import "fmt"

// The observed answer: what a reader is handed, so that what it reads is recorded field by field
// (A10). Descending into an object or an array records nothing; reading a scalar records its path;
// asking how many items an array or a value-keyed object holds records its membership; enumerating
// or serialising an ordinary object records it read whole.

// Holding says what the source of an answer holds. An answer from the firewall holds everything;
// one served from the mirror holds the paths the mirror stores, and a read of any other is a read
// the mirror cannot answer.
type Holding interface {
	// Field reports whether a field path -- a scalar, or a membership `rows[]`, `x.*` -- is held.
	Field(path string) bool
	// Whole reports whether everything below a path is held.
	Whole(path string) bool
}

// everything is the holding of an answer the firewall gave.
type everything struct{}

func (everything) Field(string) bool { return true }
func (everything) Whole(string) bool { return true }

// Everything is the holding of a firewall answer.
var Everything Holding = everything{}

// NotHeld is a read of a path the answer's source does not hold. The reader's call cannot be
// answered from this answer, and is answered again from a firewall call (A10, "No missing field
// because of the mirror").
type NotHeld struct {
	Path string
}

func (e *NotHeld) Error() string { return fmt.Sprintf("answer: the path %q is not held", e.Path) }

// Observed is a value being read, with the path it sits at and the record of what was read.
type Observed struct {
	value   *Value
	path    string
	reads   *Reads
	holding Holding
}

// Observe begins observing an answer at its root.
func Observe(value *Value, reads *Reads, holding Holding) *Observed {
	if holding == nil {
		holding = Everything
	}
	return &Observed{value: value, reads: reads, holding: holdingOf(value, holding)}
}

// holdingOf is a value's own holding, or the one it inherits.
func holdingOf(value *Value, inherited Holding) Holding {
	if value != nil && value.Holding != nil {
		return value.Holding
	}
	return inherited
}

// Path is where the observed value sits.
func (o *Observed) Path() string { return o.path }

// Kind is the observed value's type; an absent value is Null.
func (o *Observed) Kind() Kind {
	if o.value == nil {
		return Null
	}
	return o.value.Kind
}

// Peek returns the value without recording a read: for the host's own bookkeeping only.
func (o *Observed) Peek() *Value { return o.value }

// child is the path of one member or item.
func (o *Observed) child(key string) string {
	if o.value != nil && o.value.ValueKeyed {
		return Join(o.path, AnyMember)
	}
	return Join(o.path, key)
}

// Get reads one member of an object. A scalar member, or an absent one, is a field read; an
// object or array member is descended into and records nothing yet.
func (o *Observed) Get(key string) (*Observed, bool, error) {
	path := o.child(key)
	var member *Value
	if o.value != nil && o.value.Kind == Object {
		member = o.value.Fields[key]
	}
	if member == nil || member.Scalar() {
		if !o.holding.Field(path) {
			return nil, false, &NotHeld{Path: path}
		}
		o.reads.Field(path)
	}
	if member == nil {
		return nil, false, nil
	}
	return &Observed{value: member, path: path, reads: o.reads, holding: holdingOf(member, o.holding)}, true, nil
}

// Has reports whether an object holds a member, which reads that member's presence.
func (o *Observed) Has(key string) (bool, error) {
	path := o.child(key)
	if o.value != nil && o.value.ValueKeyed {
		path = Join(o.path, AnyMember)
	}
	if !o.holding.Field(path) {
		return false, &NotHeld{Path: path}
	}
	o.reads.Field(path)
	if o.value == nil || o.value.Kind != Object {
		return false, nil
	}
	_, present := o.value.Fields[key]
	return present, nil
}

// Len is how many items an array holds, which reads its membership.
func (o *Observed) Len() (int, error) {
	path := Join(o.path, AnyItem)
	if !o.holding.Field(path) {
		return 0, &NotHeld{Path: path}
	}
	o.reads.Field(path)
	if o.value == nil || o.value.Kind != Array {
		return 0, nil
	}
	return len(o.value.Items), nil
}

// Index returns one item of an array, descending into it.
func (o *Observed) Index(index int) *Observed {
	path := Join(o.path, AnyItem)
	var item *Value
	if o.value != nil && o.value.Kind == Array && index >= 0 && index < len(o.value.Items) {
		item = o.value.Items[index]
	}
	return &Observed{value: item, path: path, reads: o.reads, holding: holdingOf(item, o.holding)}
}

// Keys enumerates an object's keys. For a value-keyed object that is its membership; for any
// other object it is the whole object read, since a reader that enumerated it can reach every
// field below.
func (o *Observed) Keys() ([]string, error) {
	if o.value == nil || o.value.Kind != Object {
		return nil, nil
	}
	if o.value.ValueKeyed {
		path := Join(o.path, AnyMember)
		if !o.holding.Field(path) {
			return nil, &NotHeld{Path: path}
		}
		o.reads.Field(path)
	} else {
		if !o.holding.Whole(o.path) {
			return nil, &NotHeld{Path: o.path}
		}
		o.reads.Whole(o.path)
	}
	return append([]string(nil), o.value.Keys...), nil
}

// Whole returns the value read whole: every field below it counts as read.
func (o *Observed) Whole() (*Value, error) {
	if !o.holding.Whole(o.path) {
		return nil, &NotHeld{Path: o.path}
	}
	o.reads.Whole(o.path)
	return o.value, nil
}

// Scalar reads the observed value as a scalar -- an array's item read directly -- recording its
// path.
func (o *Observed) Scalar() (*Value, error) {
	if !o.holding.Field(o.path) {
		return nil, &NotHeld{Path: o.path}
	}
	o.reads.Field(o.path)
	return o.value, nil
}

// ValueKeyed reports whether the observed value is a value-keyed object.
func (o *Observed) ValueKeyed() bool { return o.value != nil && o.value.ValueKeyed }
