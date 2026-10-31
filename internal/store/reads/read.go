package reads

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/NaejEL/opnview/internal/store/vocab"
)

// The read functions step 5B's widget API consumes. Each one's contract is in
// docs/data-model.md, "The store functions step 5B consumes"; every statement they
// run is in read.statements.sql, where sql/schema-checks.sh plans it at both seed sizes.
//
// Three rules hold for all of them, because each is a way a figure lies:
//   - A figure the store could not compute is ABSENT -- a nil, a missing map
//     entry, a state -- and never a zero that means "we could not look".
//   - A distinct count is never summed across slots.
//   - Every volume is logged volume: the sum of packet lengths the filter log
//     recorded, which counts the packet that establishes a state and not the
//     rest of the connection, and nothing between two clients behind one
//     interface.

// Interval is a half-open span of UTC epoch seconds, [From, To).
type Interval struct {
	From int64
	To   int64
}

// VolumeKey is the key of one volume row.
type VolumeKey struct {
	// SrcInterfaceID and DstInterfaceID are the two ends' interfaces, nil for an
	// end that is outside.
	SrcInterfaceID *int64
	DstInterfaceID *int64
	// PeerAddress is the outside end, nil between two interfaces, and nil for a flow
	// with an end that is this firewall when its other end is not outside.
	PeerAddress *string
	// TrafficScope is east_west, north_south or this_firewall.
	TrafficScope string
	// TrafficDirection is outbound, inbound, inter_interface, to_this_firewall or
	// from_this_firewall.
	TrafficDirection string
	// ExitLegDevice is nil for every row a total counts, and the device a second leg
	// was logged on otherwise: the second records of paired connections, which count
	// on that interface's own figures and nowhere else (decision 2 of the step-5A live
	// corrections). Total and ByDirection leave them out; ExitLegs sums them.
	ExitLegDevice *string
}

// VolumeFigures are the summable figures of a volume. Allowed is flow.action = 'pass',
// blocked is 'block' or 'reject', and unknown is a record whose action opnview could
// not name: it is counted as neither, because a decision nobody read is not an
// allowance. Bytes is the three together.
//
// THE MEASURE. Figures are logged unless Measure names a declared measure (PA5 of the plugin-API
// amendment; vocab.DeclaredMeasure). A measure's figures are the same connections -- the connection
// counts are the logged connections counted -- with the measure's bytes in place of the logged bytes,
// split by the counting leg's action, and, per action, the parts: the packets, the coverage classes,
// the direction states met and the markers (flow_measures and measure_parts in schema.sql;
// docs/data-model.md, "Declared measures"). Two measures are never summed: a read answers one, never two.
type VolumeFigures struct {
	Bytes              int64
	AllowedBytes       int64
	BlockedBytes       int64
	UnknownBytes       int64
	AllowedConnections int64
	BlockedConnections int64
	UnknownConnections int64
	// Measure is the declared measure the figures are of; empty for logged figures.
	Measure string
	// Parts are the measure's figures counted per action; zero for logged figures.
	Parts MeasureParts
}

// Measured reports whether the figures are of a declared measure rather than logged.
func (f VolumeFigures) Measured() bool { return f.Measure != "" }

// MeasureParts are a measure's figures of the allowed, the blocked and the unknown connections, in the
// order the measure's parts hold them (schema.sql, "THE LAYOUT OF A MEASURE'S PARTS").
type MeasureParts struct {
	Allowed MeasurePart
	Blocked MeasurePart
	Unknown MeasurePart
}

// MeasurePart is a measure's figures of one action's connections: their packets and their coverage.
type MeasurePart struct {
	Packets  int64
	Coverage MeasureCoverage
}

// MeasureCoverage counts connections by how the measure covered them. The four coverage classes sum to
// the connections counted: Complete, every direction in the measure's complete state; Partial, some
// measured bytes but not every direction complete; Unmeasured, rows but no measured byte; NotKnown, no
// row -- the measure knew nothing at the connection's start. States counts, per declared direction
// state in its declared order, the connections with at least one direction in it: a connection whose
// two directions are in two states counts under both. Markers counts, per declared marker, the
// connections with a direction carrying it. The arrays are fixed-size so that coverage compares as a
// plain value; past the measure's declared states and markers they hold zero.
type MeasureCoverage struct {
	Complete   int64
	Partial    int64
	Unmeasured int64
	NotKnown   int64
	States     [vocab.MaxMeasureStates]int64
	Markers    [vocab.MaxMeasureMarkers]int64
}

// List is the three parts, in the order the measure's parts hold them.
func (p *MeasureParts) List() []*MeasurePart {
	return []*MeasurePart{&p.Allowed, &p.Blocked, &p.Unknown}
}

// Add sums other into p, figure by figure.
func (p *MeasureParts) Add(other MeasureParts) {
	others := other.List()
	for index, part := range p.List() {
		part.add(*others[index])
	}
}

func (p *MeasurePart) add(other MeasurePart) {
	p.Packets += other.Packets
	c, o := &p.Coverage, other.Coverage
	c.Complete += o.Complete
	c.Partial += o.Partial
	c.Unmeasured += o.Unmeasured
	c.NotKnown += o.NotKnown
	for index := range c.States {
		c.States[index] += o.States[index]
	}
	for index := range c.Markers {
		c.Markers[index] += o.Markers[index]
	}
}

// Whole is the three parts summed: the packets and the coverage of every connection.
func (p MeasureParts) Whole() MeasurePart {
	var whole MeasurePart
	for _, part := range p.List() {
		whole.add(*part)
	}
	return whole
}

// Covered reports whether every connection counted is covered in a measure: none not known, and no
// direction in a state the measure declares not covered.
func (c MeasureCoverage) Covered(measure vocab.DeclaredMeasure) bool {
	if c.NotKnown != 0 {
		return false
	}
	for index, state := range measure.States {
		if c.States[index] != 0 && !state.Covered {
			return false
		}
	}
	return true
}

// Packets are the measure's packets of every connection counted; 0 for logged figures.
func (f VolumeFigures) Packets() int64 { return f.Parts.Whole().Packets }

// Coverage is the coverage of every connection counted; zero for logged figures.
func (f VolumeFigures) Coverage() MeasureCoverage { return f.Parts.Whole().Coverage }

// Covered reports whether every connection the figures count is covered by their measure; logged
// figures, and figures of a measure the process does not know, are not.
func (f VolumeFigures) Covered() bool {
	measure, found := vocab.MeasureNamed(f.Measure)
	return found && f.Coverage().Covered(measure)
}

// AllowedPart keeps the allowed connections of figures, in their measure, with their own packets and
// coverage.
func (f VolumeFigures) AllowedPart() VolumeFigures {
	return VolumeFigures{Bytes: f.AllowedBytes, AllowedBytes: f.AllowedBytes,
		AllowedConnections: f.AllowedConnections, Measure: f.Measure, Parts: MeasureParts{Allowed: f.Parts.Allowed}}
}

// BlockedPart keeps the blocked connections of figures, in their measure, with their own packets and
// coverage.
func (f VolumeFigures) BlockedPart() VolumeFigures {
	return VolumeFigures{Bytes: f.BlockedBytes, BlockedBytes: f.BlockedBytes,
		BlockedConnections: f.BlockedConnections, Measure: f.Measure, Parts: MeasureParts{Blocked: f.Parts.Blocked}}
}

// PartsArray is the figures' parts in the layout of their measure: per part, its bytes, its packets,
// its four classes, one count per declared state and one per declared marker. It is what a script's
// figures carry as measure_parts. Nil for logged figures, and for a measure the process does not know.
func (f VolumeFigures) PartsArray() []int64 {
	measure, found := vocab.MeasureNamed(f.Measure)
	if !found {
		return nil
	}
	array := make([]int64, 0, 3*measure.PartWidth())
	bytes := []int64{f.AllowedBytes, f.BlockedBytes, f.UnknownBytes}
	for index, part := range f.Parts.List() {
		c := part.Coverage
		array = append(array, bytes[index], part.Packets, c.Complete, c.Partial, c.Unmeasured, c.NotKnown)
		array = append(array, c.States[:len(measure.States)]...)
		array = append(array, c.Markers[:len(measure.Markers)]...)
	}
	return array
}

// SetPartsArray reads parts in the layout of a measure (PartsArray) into the figures, which become of
// that measure; the bytes the array holds are not read, the figures carry their own. An array of
// another width is an error naming the measure.
func (f *VolumeFigures) SetPartsArray(measureName string, array []int64) error {
	measure, found := vocab.MeasureNamed(measureName)
	if !found {
		return fmt.Errorf("store: the measure %q is not declared", measureName)
	}
	f.Measure = measureName
	f.Parts = MeasureParts{}
	if len(array) == 0 {
		return nil
	}
	width := measure.PartWidth()
	if len(array) != 3*width {
		return fmt.Errorf("store: the parts of the measure %s hold %d figures, not %d", measureName, len(array), 3*width)
	}
	for index, part := range f.Parts.List() {
		figures := array[index*width : (index+1)*width]
		part.Packets = figures[1]
		c := &part.Coverage
		c.Complete, c.Partial, c.Unmeasured, c.NotKnown = figures[2], figures[3], figures[4], figures[5]
		copy(c.States[:], figures[6:6+len(measure.States)])
		copy(c.Markers[:], figures[6+len(measure.States):])
	}
	return nil
}

// MeasureTarget is the scan destination of the parts column a read in a measure appends to the seven
// summable ones: the measure's JSON array of figures, or NULL where no row counted anything of it. The
// figures become of the measure.
func (f *VolumeFigures) MeasureTarget(measure string) sql.Scanner {
	f.Measure = measure
	return measurePartsScanner{figures: f, measure: measure}
}

// measurePartsScanner reads a measure's parts column.
type measurePartsScanner struct {
	figures *VolumeFigures
	measure string
}

// Scan implements sql.Scanner.
func (s measurePartsScanner) Scan(source any) error {
	var text []byte
	switch value := source.(type) {
	case nil:
		return s.figures.SetPartsArray(s.measure, nil)
	case []byte:
		text = value
	case string:
		text = []byte(value)
	default:
		return fmt.Errorf("store: a measure's parts are %T, not JSON", source)
	}
	array, plain := plainIntegers(text)
	if !plain {
		array = nil
		if err := json.Unmarshal(text, &array); err != nil {
			return fmt.Errorf("store: reading the parts of the measure %s: %w", s.measure, err)
		}
	}
	return s.figures.SetPartsArray(s.measure, array)
}

// plainIntegers reads the text PostgreSQL writes for a JSON array of whole numbers -- "[1, 0, 42]",
// "[]" -- without the reflective JSON decoder: a measured read crosses one such array per row, and the
// decoder was most of what reading one cost (PA18.2 of the open-plugin-API amendment). It reports false
// for anything else -- a member that is not a plain whole number, a number out of range, any other
// shape -- and the JSON decoder then reads it, with its errors.
func plainIntegers(text []byte) ([]int64, bool) {
	index, length := 0, len(text)
	skip := func() {
		for index < length && (text[index] == ' ' || text[index] == '\n' || text[index] == '\t' || text[index] == '\r') {
			index++
		}
	}
	skip()
	if index >= length || text[index] != '[' {
		return nil, false
	}
	index++
	skip()
	numbers := make([]int64, 0, 64)
	if index < length && text[index] == ']' {
		index++
		skip()
		return numbers, index == length
	}
	for {
		negative := false
		// limit is the largest magnitude the number may reach: one more for a negative one.
		limit := uint64(1<<63 - 1)
		if index < length && text[index] == '-' {
			negative, limit = true, 1<<63
			index++
		}
		start := index
		var value uint64
		for index < length && text[index] >= '0' && text[index] <= '9' {
			digit := uint64(text[index] - '0')
			if value > limit/10 || value*10 > limit-digit {
				return nil, false
			}
			value = value*10 + digit
			index++
		}
		digits := index - start
		// JSON writes no leading zero, and a number with none of its digits is not one.
		if digits == 0 || (digits > 1 && text[start] == '0') {
			return nil, false
		}
		if negative {
			numbers = append(numbers, -int64(value-1)-1)
		} else {
			numbers = append(numbers, int64(value))
		}
		skip()
		if index >= length {
			return nil, false
		}
		switch text[index] {
		case ',':
			index++
			skip()
		case ']':
			index++
			skip()
			return numbers, index == length
		default:
			return nil, false
		}
	}
}

// Add sums other into f. Figures of a measure stay of it: the reads of one answer are all of one
// measure, so a logged figure added to a measured one is the zero a sum starts from.
func (f *VolumeFigures) Add(other VolumeFigures) { f.add(other) }

func (f *VolumeFigures) add(other VolumeFigures) {
	f.Bytes += other.Bytes
	f.AllowedBytes += other.AllowedBytes
	f.BlockedBytes += other.BlockedBytes
	f.UnknownBytes += other.UnknownBytes
	f.AllowedConnections += other.AllowedConnections
	f.BlockedConnections += other.BlockedConnections
	f.UnknownConnections += other.UnknownConnections
	if f.Measure == "" {
		f.Measure = other.Measure
	}
	f.Parts.Add(other.Parts)
}

// Connections is every connection: allowed, blocked and unknown.
func (f VolumeFigures) Connections() int64 {
	return f.AllowedConnections + f.BlockedConnections + f.UnknownConnections
}

// VolumeRow is one key and its figures.
type VolumeRow struct {
	VolumeKey
	VolumeFigures
}

// VolumeWindow is the volume of a rolling window.
type VolumeWindow struct {
	// Requested is the window that was asked for.
	Requested Interval
	// Covered is the part of it the store holds data for, or nil when it holds
	// none: before the earliest flow or slot opnview kept, and after now, nothing
	// is known, which is not the same as nothing having happened.
	Covered *Interval
	// Rows are the volumes, one per key.
	Rows []VolumeRow
}

// Total sums every row that counts a connection: every row but the second legs, so a
// connection logged on two interfaces is counted once.
func (w VolumeWindow) Total() VolumeFigures {
	var total VolumeFigures
	for _, row := range w.Rows {
		if row.ExitLegDevice != nil {
			continue
		}
		total.add(row.VolumeFigures)
	}
	return total
}

// ByDirection sums the rows Total counts, per traffic direction.
func (w VolumeWindow) ByDirection() map[string]VolumeFigures {
	sums := map[string]VolumeFigures{}
	for _, row := range w.Rows {
		if row.ExitLegDevice != nil {
			continue
		}
		figures := sums[row.TrafficDirection]
		figures.add(row.VolumeFigures)
		sums[row.TrafficDirection] = figures
	}
	return sums
}
