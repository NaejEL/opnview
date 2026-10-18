package reads

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/NaejEL/opnview/internal/store/sqldb"
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

// ReadVolumeWindow assembles the volume of [from, to) at query time: the whole
// hours inside it from the 1 h slots, and the partial hours at either edge from
// `flow`. Inside the flow horizon this equals a computation over `flow` alone;
// beyond it the slots answer, and Covered says which interval they cover.
func (s *ReadStore) ReadVolumeWindow(ctx context.Context, from, to, now int64) (VolumeWindow, error) {
	return s.ReadVolumeWindowOf(ctx, from, to, now, "")
}

// ReadVolumeWindowOf is ReadVolumeWindow of one measure: the logged figures, or, naming a declared
// measure, its figures of the same rows -- the same keys, the same connections, the measure's bytes --
// read from the measure_parts of the 1 h slots and, at either edge, through flow_measures. The
// window's coverage is the same for both.
func (s *ReadStore) ReadVolumeWindowOf(ctx context.Context, from, to, now int64, measure string) (VolumeWindow, error) {
	result := VolumeWindow{Requested: Interval{From: from, To: to}}
	flows, slots := "read_volume_flows", "read_volume_slots"
	if measure != "" {
		flows, slots = "read_volume_flows_measured", "read_volume_slots_measured"
	}
	if to <= from {
		return result, nil
	}
	firstHour := vocab.PeriodHour.SlotStart(from)
	if firstHour < from {
		firstHour = vocab.PeriodHour.SlotEnd(firstHour)
	}
	lastHour := vocab.PeriodHour.SlotStart(to)

	sums := map[string]*VolumeRow{}
	if firstHour < lastHour {
		if err := s.readVolume(ctx, flows, from, firstHour, sums, measure); err != nil {
			return result, err
		}
		if err := s.readVolume(ctx, slots, firstHour, lastHour, sums, measure); err != nil {
			return result, err
		}
		if err := s.readVolume(ctx, flows, lastHour, to, sums, measure); err != nil {
			return result, err
		}
	} else if err := s.readVolume(ctx, flows, from, to, sums, measure); err != nil {
		return result, err
	}
	result.Rows = sortedVolumeRows(sums)

	earliestFlow, hasFlow, err := sqldb.QueryOptionalInt(ctx, s.Reader, "read_earliest_flow", nil)
	if err != nil {
		return result, err
	}
	earliestSlot, hasSlot, err := sqldb.QueryOptionalInt(ctx, s.Reader, "read_earliest_slot", nil)
	if err != nil {
		return result, err
	}
	if !hasFlow && !hasSlot {
		return result, nil
	}
	dataStart := earliestFlow
	if !hasFlow || (hasSlot && earliestSlot < dataStart) {
		dataStart = earliestSlot
	}
	covered := Interval{From: max(from, dataStart), To: min(to, now)}
	// The left edge is read from `flow`. When the flows of that edge are already
	// purged while the slot of its hour survives, the edge is not covered.
	if (!hasFlow || from < earliestFlow) && hasSlot && earliestSlot <= vocab.PeriodHour.SlotStart(from) &&
		firstHour < lastHour {
		covered.From = max(covered.From, firstHour)
	}
	if covered.To > covered.From {
		result.Covered = &covered
	}
	return result, nil
}

// readVolume adds one statement's rows over [from, to) into sums.
func (s *ReadStore) readVolume(ctx context.Context, name string, from, to int64,
	sums map[string]*VolumeRow, measure string) error {
	if to <= from {
		return nil
	}
	text, err := sqldb.Statement(name)
	if err != nil {
		return err
	}
	parameters := map[string]any{"from": from, "to": to}
	if measure != "" {
		parameters["measure"] = measure
	}
	rows, err := sqldb.Query(ctx, s.Reader, text, parameters)
	if err != nil {
		return fmt.Errorf("store: %s: %w", name, err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var (
			row        VolumeRow
			src, dst   sql.NullInt64
			peer, exit sql.NullString
		)
		targets := []any{&src, &dst, &peer, &row.TrafficScope, &row.TrafficDirection, &exit,
			&row.Bytes, &row.AllowedBytes, &row.BlockedBytes, &row.UnknownBytes,
			&row.AllowedConnections, &row.BlockedConnections, &row.UnknownConnections}
		if measure != "" {
			targets = append(targets, row.MeasureTarget(measure))
		}
		if err := rows.Scan(targets...); err != nil {
			return fmt.Errorf("store: %s: %w", name, err)
		}
		row.SrcInterfaceID = sqldb.NullableInt(src)
		row.DstInterfaceID = sqldb.NullableInt(dst)
		if peer.Valid {
			value := peer.String
			row.PeerAddress = &value
		}
		if exit.Valid {
			value := exit.String
			row.ExitLegDevice = &value
		}
		key := volumeKeyString(row.VolumeKey)
		if existing, present := sums[key]; present {
			existing.add(row.VolumeFigures)
			continue
		}
		copied := row
		sums[key] = &copied
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("store: %s: %w", name, err)
	}
	return nil
}

// volumeKeyString renders a key so two rows of one key meet in a map.
func volumeKeyString(key VolumeKey) string {
	part := func(value *int64) string {
		if value == nil {
			return "-"
		}
		return fmt.Sprint(*value)
	}
	peer := "-"
	if key.PeerAddress != nil {
		peer = "=" + *key.PeerAddress
	}
	exit := "-"
	if key.ExitLegDevice != nil {
		exit = "=" + *key.ExitLegDevice
	}
	return strings.Join([]string{part(key.SrcInterfaceID), part(key.DstInterfaceID), peer,
		key.TrafficScope, key.TrafficDirection, exit}, "\x1f")
}

// sortedVolumeRows returns the rows in a stable order: by bytes, then by key.
func sortedVolumeRows(sums map[string]*VolumeRow) []VolumeRow {
	keys := make([]string, 0, len(sums))
	for key := range sums {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	rows := make([]VolumeRow, 0, len(keys))
	for _, key := range keys {
		rows = append(rows, *sums[key])
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Bytes > rows[j].Bytes })
	return rows
}

// keyOr renders a nullable id as a node key, or the fallback when it is NULL.
func keyOr(prefix string, value sql.NullInt64, fallback string) string {
	if !value.Valid {
		return prefix + ":" + fallback
	}
	return fmt.Sprintf("%s:%d", prefix, value.Int64)
}

// AttributionRate is the share of a client's outbound flows that carry a site name.
type AttributionRate struct {
	// EligibleFlows are the flows with an outside destination in the window.
	EligibleFlows int64
	// NamedFlows are those that carry an attribution, by either method.
	NamedFlows int64
	// NamedByCacheAnswer and NamedByLookupTiming are NamedFlows per method; they sum to
	// it.
	NamedByCacheAnswer  int64
	NamedByLookupTiming int64
	// CacheCovered says whether the resolver's cache was read for the window: a record
	// of the cache whose coverage interval meets it. Without it no flow of the window
	// could have been named from exact evidence, and a low count of resolver_cache_answer
	// means that rather than poor evidence.
	CacheCovered bool
	// Rate is NamedFlows / EligibleFlows. It is nil -- UNDEFINED, not 0 -- when no
	// resolver source answered for the window, because then nothing could have named
	// a flow, and when there is no eligible flow to divide by.
	Rate *float64
	// ResolverCovered says whether opnview holds evidence that a resolver source
	// answered for the window: a lookup stored for an instant inside it, or a
	// dns_lookup availability found reachable by a probe inside it.
	ResolverCovered bool
	// MeanDelaySeconds and MaxDelaySeconds describe the attributed flows' delay
	// between lookup and flow; nil when there is none.
	MeanDelaySeconds *float64
	MaxDelaySeconds  *int64
}

// ReadAttributionRate computes the attribution rate of [from, to), for one client
// or, with a nil client, for every client.
//
// The rate is defined only when a resolver source answered FOR THE WINDOW: with no
// resolver source there is nothing that could have named a flow, and a 0 would say
// the resolver was asked and named nothing. The availability row holds only the
// latest probe, so it cannot say whether the resolver answered last week; what
// opnview holds for a past window is the lookups it stored for it. A window with
// neither a stored lookup nor a reachable probe inside it is undefined, whatever the
// resolver's state is now; one with a lookup is defined even if the resolver is
// unreachable now. With coverage and no attribution, the rate is 0.
//
// The named flows are also counted per method, resolver_cache_answer and lookup_timing,
// and the two sum to NamedFlows. Whether the resolver's cache was read for the window is
// reported beside them, as CacheCovered: it does not change whether the rate is defined,
// which stays the 5A meaning, but it says whether the exact method could have named
// anything.
func (s *ReadStore) ReadAttributionRate(ctx context.Context, from, to int64, clientID *int64) (
	AttributionRate, error) {
	var result AttributionRate
	covered, _, err := sqldb.QueryOptionalInt(ctx, s.Reader, "read_lookup_coverage",
		map[string]any{"from": from, "to": to})
	if err != nil {
		return result, err
	}
	result.ResolverCovered = covered > 0
	cacheCovered, _, err := sqldb.QueryOptionalInt(ctx, s.Reader, "read_cache_coverage",
		map[string]any{"from": from, "to": to})
	if err != nil {
		return result, err
	}
	result.CacheCovered = cacheCovered > 0

	text, err := sqldb.Statement("read_attribution_rate")
	if err != nil {
		return result, err
	}
	var (
		mean    sql.NullFloat64
		maximum sql.NullInt64
	)
	if err := sqldb.QueryRow(ctx, s.Reader, text, map[string]any{"from": from, "to": to, "client_id": sqldb.Optional(clientID)}).Scan(&result.EligibleFlows, &result.NamedFlows,
		&mean, &maximum, &result.NamedByCacheAnswer, &result.NamedByLookupTiming); err != nil {
		return result, fmt.Errorf("store: read_attribution_rate: %w", err)
	}
	if mean.Valid {
		value := mean.Float64
		result.MeanDelaySeconds = &value
	}
	result.MaxDelaySeconds = sqldb.NullableInt(maximum)
	if result.ResolverCovered && result.EligibleFlows > 0 {
		rate := float64(result.NamedFlows) / float64(result.EligibleFlows)
		result.Rate = &rate
	}
	return result, nil
}

// InterfaceClients is how many clients one interface showed in a window, out of
// how many it is known to have.
type InterfaceClients struct {
	InterfaceID int64
	// Seen is the distinct inside clients of the window's flows on the interface.
	Seen int64
	// Known is the client rows of the interface seen at or after the window's start.
	Known int64
}

// ReadInterfaceClients counts, per interface, the clients seen in [from, to) out of
// those known.
func (s *ReadStore) ReadInterfaceClients(ctx context.Context, from, to int64) ([]InterfaceClients, error) {
	interfaces, err := s.readInterfaces(ctx)
	if err != nil {
		return nil, err
	}
	seen := map[int64]int64{}
	text, err := sqldb.Statement("read_interface_seen_clients")
	if err != nil {
		return nil, err
	}
	rows, err := sqldb.Query(ctx, s.Reader, text, map[string]any{"from": from, "to": to})
	if err != nil {
		return nil, fmt.Errorf("store: read_interface_seen_clients: %w", err)
	}
	for rows.Next() {
		var (
			id    sql.NullInt64
			count int64
		)
		if err := rows.Scan(&id, &count); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("store: read_interface_seen_clients: %w", err)
		}
		if id.Valid {
			seen[id.Int64] = count
		}
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return nil, fmt.Errorf("store: read_interface_seen_clients: %w", err)
	}
	var result []InterfaceClients
	for _, iface := range interfaces {
		known, _, err := sqldb.QueryOptionalInt(ctx, s.Reader, "read_interface_known_clients",
			map[string]any{"interface_id": iface.id, "from": from})
		if err != nil {
			return nil, err
		}
		result = append(result, InterfaceClients{InterfaceID: iface.id, Seen: seen[iface.id], Known: known})
	}
	return result, nil
}

// interfaceRow is the part of an interface the read functions need.
type interfaceRow struct {
	id         int64
	identifier string
	device     string
	upstream   bool
}

func (s *ReadStore) readInterfaces(ctx context.Context) ([]interfaceRow, error) {
	text, err := sqldb.Statement("read_interfaces")
	if err != nil {
		return nil, err
	}
	rows, err := s.Reader.QueryContext(ctx, text)
	if err != nil {
		return nil, fmt.Errorf("store: read_interfaces: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var result []interfaceRow
	for rows.Next() {
		var row interfaceRow
		if err := rows.Scan(&row.id, &row.identifier, &row.device, &row.upstream); err != nil {
			return nil, fmt.Errorf("store: read_interfaces: %w", err)
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

// RuleLogging is how many rules do not log, per interface.
type RuleLogging struct {
	// NotLoggingByInterface counts, per interface id, the model rules naming that
	// interface whose `log` flag is off.
	NotLoggingByInterface map[int64]int64
	// FloatingNotLogging counts the rules naming no interface -- floating rules,
	// which apply on every interface -- whose flag is off.
	FloatingNotLogging int64
	// UnresolvedNotLogging counts the rules whose interfaces could not be resolved:
	// legacy rules, whose `interface` the firewall reports as descriptions, which
	// nothing here matches on; model rules naming a key no interface carries; and
	// rules whose `interface` field was not reported at all.
	UnresolvedNotLogging int64
	// NotReported counts the rules whose `log` flag was not reported at all, which
	// is not the same as a rule that does not log.
	NotReported int64
}

// ReadRuleLogging counts the rules that do not log, per interface. A model rule's
// `interface` is a comma-separated list of interface configuration keys, each
// resolved by equality with interface.identifier, which is a key and not a name.
func (s *ReadStore) ReadRuleLogging(ctx context.Context) (RuleLogging, error) {
	result := RuleLogging{NotLoggingByInterface: map[int64]int64{}}
	interfaces, err := s.readInterfaces(ctx)
	if err != nil {
		return result, err
	}
	byKey := map[string]int64{}
	for _, iface := range interfaces {
		byKey[iface.identifier] = iface.id
	}
	text, err := sqldb.Statement("read_rules")
	if err != nil {
		return result, err
	}
	rows, err := s.Reader.QueryContext(ctx, text)
	if err != nil {
		return result, fmt.Errorf("store: read_rules: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var (
			id                   int64
			interfaceList        sql.NullString
			legacy, logsMatching sql.NullBool
		)
		if err := rows.Scan(&id, &interfaceList, &legacy, &logsMatching); err != nil {
			return result, fmt.Errorf("store: read_rules: %w", err)
		}
		if !logsMatching.Valid {
			result.NotReported++
			continue
		}
		if logsMatching.Bool {
			continue
		}
		keys := splitInterfaceList(interfaceList.String)
		switch {
		case !interfaceList.Valid:
			// The field was not reported at all, which says nothing about where the
			// rule applies.
			result.UnresolvedNotLogging++
		case len(keys) == 0:
			result.FloatingNotLogging++
		case legacy.Valid && legacy.Bool:
			result.UnresolvedNotLogging++
		default:
			resolved := map[int64]struct{}{}
			unresolved := false
			for _, key := range keys {
				interfaceID, present := byKey[key]
				if !present {
					unresolved = true
					continue
				}
				resolved[interfaceID] = struct{}{}
			}
			for interfaceID := range resolved {
				result.NotLoggingByInterface[interfaceID]++
			}
			if unresolved && len(resolved) == 0 {
				result.UnresolvedNotLogging++
			}
		}
	}
	return result, rows.Err()
}

// splitInterfaceList splits the endpoint's comma-separated interface field.
func splitInterfaceList(value string) []string {
	var keys []string
	for _, part := range strings.Split(value, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			keys = append(keys, trimmed)
		}
	}
	return keys
}

// AddressHistory is one address an interface held, or one gateway behind it.
type AddressHistory struct {
	SourceField   string
	Address       string
	PrefixLength  *int64
	AddressFamily int64
	// FirstSeenAt is when opnview first saw it: for the current address, when it
	// last changed, as far as opnview has been looking.
	FirstSeenAt int64
	LastSeenAt  int64
}

// PublicAddress is an upstream interface with its address history and its
// gateways.
type PublicAddress struct {
	InterfaceID int64
	// History is every address and gateway the interface held, newest first.
	History []AddressHistory
}

// ReadPublicAddresses returns every upstream interface's address history. The
// address the interface holds is what the firewall sees, and only that.
//
// THE DOUBLE-NAT LIMIT. Behind an upstream NAT -- an Internet box in router mode in
// front of the firewall, or a carrier-grade NAT at the provider -- the firewall does
// not see its public IPv4 address at all: the upstream interface holds a private or
// shared address, and the address the Internet sees belongs to a device opnview does
// not read. Nothing opnview reads can supply it: no OPNsense endpoint reports it, and
// asking a third party would be an outbound call the project does not allow. A screen
// built on this function says so rather than presenting the interface's address as
// public (ROADMAP.md, step-5 checklist; docs/widget-catalogue.md, "Public address").
func (s *ReadStore) ReadPublicAddresses(ctx context.Context) ([]PublicAddress, error) {
	interfaces, err := s.readInterfaces(ctx)
	if err != nil {
		return nil, err
	}
	text, err := sqldb.Statement("read_interface_addresses")
	if err != nil {
		return nil, err
	}
	var result []PublicAddress
	for _, iface := range interfaces {
		if !iface.upstream {
			continue
		}
		rows, err := sqldb.Query(ctx, s.Reader, text, map[string]any{"interface_id": iface.id})
		if err != nil {
			return nil, fmt.Errorf("store: read_interface_addresses: %w", err)
		}
		entry := PublicAddress{InterfaceID: iface.id}
		for rows.Next() {
			var (
				row    AddressHistory
				prefix sql.NullInt64
			)
			if err := rows.Scan(&row.SourceField, &row.Address, &prefix, &row.AddressFamily,
				&row.FirstSeenAt, &row.LastSeenAt); err != nil {
				_ = rows.Close()
				return nil, fmt.Errorf("store: read_interface_addresses: %w", err)
			}
			row.PrefixLength = sqldb.NullableInt(prefix)
			entry.History = append(entry.History, row)
		}
		err = rows.Err()
		_ = rows.Close()
		if err != nil {
			return nil, fmt.Errorf("store: read_interface_addresses: %w", err)
		}
		result = append(result, entry)
	}
	return result, nil
}
