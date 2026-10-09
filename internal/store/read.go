package store

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
)

// The read functions step 5B's widget API consumes. Each one's contract is in
// docs/data-model.md, "The store functions step 5B consumes"; every statement they
// run is in read.sql, where sql/schema-checks.sh plans it at both seed sizes.
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
type VolumeFigures struct {
	Bytes              int64
	AllowedBytes       int64
	BlockedBytes       int64
	UnknownBytes       int64
	AllowedConnections int64
	BlockedConnections int64
	UnknownConnections int64
}

func (f *VolumeFigures) add(other VolumeFigures) {
	f.Bytes += other.Bytes
	f.AllowedBytes += other.AllowedBytes
	f.BlockedBytes += other.BlockedBytes
	f.UnknownBytes += other.UnknownBytes
	f.AllowedConnections += other.AllowedConnections
	f.BlockedConnections += other.BlockedConnections
	f.UnknownConnections += other.UnknownConnections
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

// ExitLegs sums the second legs per device they were logged on: what an interface's
// own figures add to the rows that name it, for the connections whose first leg was
// logged on another interface.
func (w VolumeWindow) ExitLegs() map[string]VolumeFigures {
	sums := map[string]VolumeFigures{}
	for _, row := range w.Rows {
		if row.ExitLegDevice == nil {
			continue
		}
		figures := sums[*row.ExitLegDevice]
		figures.add(row.VolumeFigures)
		sums[*row.ExitLegDevice] = figures
	}
	return sums
}

// ReadVolumeWindow assembles the volume of [from, to) at query time: the whole
// hours inside it from the 1 h slots, and the partial hours at either edge from
// `flow`. Inside the flow horizon this equals a computation over `flow` alone;
// beyond it the slots answer, and Covered says which interval they cover.
func (s *Store) ReadVolumeWindow(ctx context.Context, from, to, now int64) (VolumeWindow, error) {
	result := VolumeWindow{Requested: Interval{From: from, To: to}}
	if to <= from {
		return result, nil
	}
	firstHour := PeriodHour.SlotStart(from)
	if firstHour < from {
		firstHour = PeriodHour.SlotEnd(firstHour)
	}
	lastHour := PeriodHour.SlotStart(to)

	sums := map[string]*VolumeRow{}
	if firstHour < lastHour {
		if err := s.readVolume(ctx, "read_volume_flows", from, firstHour, sums); err != nil {
			return result, err
		}
		if err := s.readVolume(ctx, "read_volume_slots", firstHour, lastHour, sums); err != nil {
			return result, err
		}
		if err := s.readVolume(ctx, "read_volume_flows", lastHour, to, sums); err != nil {
			return result, err
		}
	} else if err := s.readVolume(ctx, "read_volume_flows", from, to, sums); err != nil {
		return result, err
	}
	result.Rows = sortedVolumeRows(sums)

	earliestFlow, hasFlow, err := queryOptionalInt(ctx, s.db, "read_earliest_flow", nil)
	if err != nil {
		return result, err
	}
	earliestSlot, hasSlot, err := queryOptionalInt(ctx, s.db, "read_earliest_slot", nil)
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
	if (!hasFlow || from < earliestFlow) && hasSlot && earliestSlot <= PeriodHour.SlotStart(from) &&
		firstHour < lastHour {
		covered.From = max(covered.From, firstHour)
	}
	if covered.To > covered.From {
		result.Covered = &covered
	}
	return result, nil
}

// readVolume adds one statement's rows over [from, to) into sums.
func (s *Store) readVolume(ctx context.Context, name string, from, to int64,
	sums map[string]*VolumeRow) error {
	if to <= from {
		return nil
	}
	text, err := Statement(name)
	if err != nil {
		return err
	}
	rows, err := s.db.QueryContext(ctx, text, sql.Named("from", from), sql.Named("to", to))
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
		if err := rows.Scan(&src, &dst, &peer, &row.TrafficScope, &row.TrafficDirection, &exit,
			&row.Bytes, &row.AllowedBytes, &row.BlockedBytes, &row.UnknownBytes,
			&row.AllowedConnections, &row.BlockedConnections, &row.UnknownConnections); err != nil {
			return fmt.Errorf("store: %s: %w", name, err)
		}
		row.SrcInterfaceID = nullableInt(src)
		row.DstInterfaceID = nullableInt(dst)
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

// Bucket is one bucket of a series, aligned to a slot boundary.
type Bucket struct {
	// Interval is the part of the slot inside the requested window.
	Interval
	// Total is the bucket's volume, and nil when the bucket is a gap holding no
	// row: a bucket outside the covered interval, or overlapping an interval the
	// filter log is known not to cover, is a gap and never a zero.
	Total *VolumeFigures
	// ByDirection splits Total by traffic direction; nil when Total is.
	ByDirection map[string]VolumeFigures
	// Gap says the bucket is not fully covered. A gap that still holds rows keeps
	// them in Total, so the buckets always sum to the window's total.
	Gap bool
}

// ReadVolumeSeries returns the series of [from, to) in buckets aligned to the
// slots of one period. A bucket inside a covered, gap-free interval with no rows
// is 0.
func (s *Store) ReadVolumeSeries(ctx context.Context, from, to int64, bucket Period,
	now int64) ([]Bucket, error) {
	gaps, err := s.firewallLogGaps(ctx, from, to)
	if err != nil {
		return nil, err
	}
	var buckets []Bucket
	for _, start := range bucket.Slots(from, to) {
		interval := Interval{From: max(from, start), To: min(to, bucket.SlotEnd(start))}
		window, err := s.ReadVolumeWindow(ctx, interval.From, interval.To, now)
		if err != nil {
			return nil, err
		}
		gap := window.Covered == nil || window.Covered.From > interval.From ||
			window.Covered.To < interval.To
		for _, missing := range gaps {
			if missing.From < interval.To && missing.To >= interval.From {
				gap = true
			}
		}
		result := Bucket{Interval: interval, Gap: gap}
		if !gap || len(window.Rows) > 0 {
			total := window.Total()
			result.Total = &total
			result.ByDirection = window.ByDirection()
		}
		buckets = append(buckets, result)
	}
	return buckets, nil
}

// firewallLogGaps returns the collection gaps of the filter log overlapping a
// window.
func (s *Store) firewallLogGaps(ctx context.Context, from, to int64) ([]Interval, error) {
	text, err := Statement("read_firewall_log_gaps")
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, text, sql.Named("from", from), sql.Named("to", to))
	if err != nil {
		return nil, fmt.Errorf("store: read_firewall_log_gaps: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var gaps []Interval
	for rows.Next() {
		var gap Interval
		if err := rows.Scan(&gap.From, &gap.To); err != nil {
			return nil, fmt.Errorf("store: read_firewall_log_gaps: %w", err)
		}
		gaps = append(gaps, gap)
	}
	return gaps, rows.Err()
}

// TreeRoot is what the inside side of a connection tree starts from.
type TreeRoot string

// The three roots the catalogue's connection tree offers.
const (
	TreeRootInterface TreeRoot = "interface"
	TreeRootClient    TreeRoot = "client"
	TreeRootOwner     TreeRoot = "owner"
)

// TreeNodeThisFirewall is the node an end that is this firewall lands under, on
// whichever side of the tree that end is.
const TreeNodeThisFirewall = "this_firewall"

// TreeNode is one node of a connection tree. Its figures are the sums of the flows
// that landed in it, and its children's figures sum to its own.
type TreeNode struct {
	// Key names the node: "interface:<id>", "client:<id>", "owner:<id>",
	// "operator:<asn>", "unplaced:<state>", "site:<name>", or one of the
	// condition keys documented on ReadConnectionTree.
	Key         string
	Bytes       int64
	Connections int64
	Blocked     int64
	Children    []*TreeNode
}

// Tree is the two sides of a connection tree over one window.
type Tree struct {
	// Inside starts at the chosen root and has the client below it (a client root
	// has one level).
	Inside []*TreeNode
	// Outside starts at the operator, or the condition the destination is in, and
	// has the site name below it.
	Outside []*TreeNode
}

// ReadConnectionTree builds both sides of the connection tree for [from, to). Every
// flow lands in exactly one node per level on each side, so a level always sums to
// the level above it:
//
//   - inside, a flow lands under its inside end: "interface:<id>", or
//     "interface:none" when neither end is inside; "client:<id>", or
//     "client:none"; for an owner root "owner:<id>", "owner:unassigned" for a
//     client nobody attributed, or "owner:none" when there is no client;
//   - outside, under its outside end: "operator:<asn>" for a placed address with
//     an AS number, "operator:none" for a placed one without, "unplaced:miss" and
//     "unplaced:pending" for the geolocation's own states and "unplaced:no_row"
//     for an address never looked up, or "inter_interface" when neither end is
//     outside; and below that "site:<name>" or "site:none" -- no site name
//     inferred.
//
// THIS FIREWALL HAS A NODE ON EACH SIDE, "this_firewall", and an end that is this
// firewall lands there and nowhere else: never under an operator, an unplaced
// condition, "interface:none" or "client:none". A flow between an inside client and
// this firewall lands under the client inside and under "this_firewall" outside; a
// flow between this firewall and an outside address lands under "this_firewall"
// inside -- with "this_firewall" again below it, for the roots that have a second
// level -- and under that address's operator outside. A second leg is left out: its
// connection is the first leg's, which the tree already holds.
func (s *Store) ReadConnectionTree(ctx context.Context, from, to int64, root TreeRoot) (Tree, error) {
	var tree Tree
	switch root {
	case TreeRootInterface, TreeRootClient, TreeRootOwner:
	default:
		return tree, fmt.Errorf("store: %q is not a connection-tree root", root)
	}
	text, err := Statement("read_connection_tree")
	if err != nil {
		return tree, err
	}
	rows, err := s.db.QueryContext(ctx, text, sql.Named("from", from), sql.Named("to", to))
	if err != nil {
		return tree, fmt.Errorf("store: read_connection_tree: %w", err)
	}
	defer func() { _ = rows.Close() }()

	inside := newTreeLevel()
	outside := newTreeLevel()
	for rows.Next() {
		var (
			interfaceID, clientID, ownerID, asn sql.NullInt64
			direction                           string
			lookupState, site                   sql.NullString
			connections, bytes, blocked         int64
		)
		if err := rows.Scan(&interfaceID, &clientID, &ownerID, &direction, &lookupState, &asn,
			&site, &connections, &bytes, &blocked); err != nil {
			return tree, fmt.Errorf("store: read_connection_tree: %w", err)
		}
		// An end that is this firewall: inside when the other end is outside (or is this
		// firewall too), outside when the other end is an inside client.
		firewall := direction == DirectionToThisFirewall || direction == DirectionFromThisFirewall
		firewallInside := firewall && (lookupState.Valid || !interfaceID.Valid)
		firewallOutside := firewall && !lookupState.Valid

		clientKey := keyOr("client", clientID, "none")
		var first, second string
		switch {
		case firewallInside:
			first = TreeNodeThisFirewall
			if root != TreeRootClient {
				second = TreeNodeThisFirewall
			}
		case root == TreeRootInterface:
			first, second = keyOr("interface", interfaceID, "none"), clientKey
		case root == TreeRootClient:
			first = clientKey
		case root == TreeRootOwner:
			owner := keyOr("owner", ownerID, "unassigned")
			if !clientID.Valid {
				owner = "owner:none"
			}
			first, second = owner, clientKey
		}
		inside.add(first, second, bytes, connections, blocked)

		var destination string
		switch {
		case firewallOutside:
			destination = TreeNodeThisFirewall
		case direction == "inter_interface":
			destination = "inter_interface"
		case lookupState.Valid && lookupState.String == "resolved":
			destination = keyOr("operator", asn, "none")
		case lookupState.Valid:
			destination = "unplaced:" + lookupState.String
		default:
			destination = "unplaced:no_row"
		}
		siteKey := "site:none"
		if site.Valid {
			siteKey = "site:" + site.String
		}
		outside.add(destination, siteKey, bytes, connections, blocked)
	}
	if err := rows.Err(); err != nil {
		return tree, fmt.Errorf("store: read_connection_tree: %w", err)
	}
	tree.Inside = inside.nodes()
	tree.Outside = outside.nodes()
	return tree, nil
}

// keyOr renders a nullable id as a node key, or the fallback when it is NULL.
func keyOr(prefix string, value sql.NullInt64, fallback string) string {
	if !value.Valid {
		return prefix + ":" + fallback
	}
	return fmt.Sprintf("%s:%d", prefix, value.Int64)
}

// treeLevel accumulates a two-level tree.
type treeLevel struct {
	byKey map[string]*TreeNode
	child map[string]map[string]*TreeNode
}

func newTreeLevel() *treeLevel {
	return &treeLevel{byKey: map[string]*TreeNode{}, child: map[string]map[string]*TreeNode{}}
}

func (l *treeLevel) add(first, second string, bytes, connections, blocked int64) {
	node, present := l.byKey[first]
	if !present {
		node = &TreeNode{Key: first}
		l.byKey[first] = node
		l.child[first] = map[string]*TreeNode{}
	}
	node.Bytes += bytes
	node.Connections += connections
	node.Blocked += blocked
	if second == "" {
		return
	}
	leaf, present := l.child[first][second]
	if !present {
		leaf = &TreeNode{Key: second}
		l.child[first][second] = leaf
	}
	leaf.Bytes += bytes
	leaf.Connections += connections
	leaf.Blocked += blocked
}

// nodes returns the level sorted by bytes, then by key, with its children sorted
// the same way.
func (l *treeLevel) nodes() []*TreeNode {
	order := func(nodes []*TreeNode) {
		sort.Slice(nodes, func(i, j int) bool {
			if nodes[i].Bytes != nodes[j].Bytes {
				return nodes[i].Bytes > nodes[j].Bytes
			}
			return nodes[i].Key < nodes[j].Key
		})
	}
	nodes := make([]*TreeNode, 0, len(l.byKey))
	for key, node := range l.byKey {
		for _, leaf := range l.child[key] {
			node.Children = append(node.Children, leaf)
		}
		order(node.Children)
		nodes = append(nodes, node)
	}
	order(nodes)
	return nodes
}

// AttributionRate is the share of a client's outbound flows that carry a site name.
type AttributionRate struct {
	// EligibleFlows are the flows with an outside destination in the window.
	EligibleFlows int64
	// NamedFlows are those that carry an attribution.
	NamedFlows int64
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
func (s *Store) ReadAttributionRate(ctx context.Context, from, to int64, clientID *int64) (
	AttributionRate, error) {
	var result AttributionRate
	covered, _, err := queryOptionalInt(ctx, s.db, "read_lookup_coverage",
		map[string]any{"from": from, "to": to})
	if err != nil {
		return result, err
	}
	result.ResolverCovered = covered > 0

	text, err := Statement("read_attribution_rate")
	if err != nil {
		return result, err
	}
	var (
		mean    sql.NullFloat64
		maximum sql.NullInt64
	)
	if err := s.db.QueryRowContext(ctx, text, sql.Named("from", from), sql.Named("to", to),
		sql.Named("client_id", optional(clientID))).Scan(&result.EligibleFlows, &result.NamedFlows,
		&mean, &maximum); err != nil {
		return result, fmt.Errorf("store: read_attribution_rate: %w", err)
	}
	if mean.Valid {
		value := mean.Float64
		result.MeanDelaySeconds = &value
	}
	result.MaxDelaySeconds = nullableInt(maximum)
	if result.ResolverCovered && result.EligibleFlows > 0 {
		rate := float64(result.NamedFlows) / float64(result.EligibleFlows)
		result.Rate = &rate
	}
	return result, nil
}

// SiteTotal is one site name's figures over several slots of the domain family.
type SiteTotal struct {
	SiteName    string
	Bytes       int64
	Connections int64
	// Clients is the distinct clients over every slot read, counted rather than
	// summed.
	Clients int64
}

// ReadSiteTotals sums the domain family of one period over the slots starting in
// [from, to).
func (s *Store) ReadSiteTotals(ctx context.Context, period Period, from, to int64) ([]SiteTotal, error) {
	text, err := PeriodStatement("read_site_totals", period)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, text, sql.Named("from", from), sql.Named("to", to))
	if err != nil {
		return nil, fmt.Errorf("store: read_site_totals: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var totals []SiteTotal
	for rows.Next() {
		var total SiteTotal
		if err := rows.Scan(&total.SiteName, &total.Bytes, &total.Connections, &total.Clients); err != nil {
			return nil, fmt.Errorf("store: read_site_totals: %w", err)
		}
		totals = append(totals, total)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: read_site_totals: %w", err)
	}
	sort.SliceStable(totals, func(i, j int) bool {
		if totals[i].Bytes != totals[j].Bytes {
			return totals[i].Bytes > totals[j].Bytes
		}
		return totals[i].SiteName < totals[j].SiteName
	})
	return totals, nil
}

// OwnerTotal is one owner's figures over several slots. OwnerID nil is the
// unassigned bucket.
type OwnerTotal struct {
	OwnerID *int64
	VolumeFigures
	// Clients is the distinct clients over every slot read, counted from the client
	// family and never summed from client_count.
	Clients int64
}

// ReadOwnerTotals sums the owner family of one period over the slots starting in
// [from, to), and counts each owner's distinct clients across them.
func (s *Store) ReadOwnerTotals(ctx context.Context, period Period, from, to int64) ([]OwnerTotal, error) {
	totals := map[string]*OwnerTotal{}
	ownerKey := func(id sql.NullInt64) string {
		if !id.Valid {
			return "unassigned"
		}
		return fmt.Sprint(id.Int64)
	}
	text, err := PeriodStatement("read_owner_totals", period)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, text, sql.Named("from", from), sql.Named("to", to))
	if err != nil {
		return nil, fmt.Errorf("store: read_owner_totals: %w", err)
	}
	for rows.Next() {
		var (
			id    sql.NullInt64
			total OwnerTotal
		)
		if err := rows.Scan(&id, &total.Bytes, &total.AllowedBytes, &total.BlockedBytes,
			&total.UnknownBytes, &total.AllowedConnections, &total.BlockedConnections,
			&total.UnknownConnections); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("store: read_owner_totals: %w", err)
		}
		total.OwnerID = nullableInt(id)
		totals[ownerKey(id)] = &total
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return nil, fmt.Errorf("store: read_owner_totals: %w", err)
	}

	text, err = PeriodStatement("read_owner_client_counts", period)
	if err != nil {
		return nil, err
	}
	counts, err := s.db.QueryContext(ctx, text, sql.Named("from", from), sql.Named("to", to))
	if err != nil {
		return nil, fmt.Errorf("store: read_owner_client_counts: %w", err)
	}
	defer func() { _ = counts.Close() }()
	for counts.Next() {
		var (
			id      sql.NullInt64
			clients int64
		)
		if err := counts.Scan(&id, &clients); err != nil {
			return nil, fmt.Errorf("store: read_owner_client_counts: %w", err)
		}
		if total, present := totals[ownerKey(id)]; present {
			total.Clients = clients
		}
	}
	if err := counts.Err(); err != nil {
		return nil, fmt.Errorf("store: read_owner_client_counts: %w", err)
	}

	keys := make([]string, 0, len(totals))
	for key := range totals {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]OwnerTotal, 0, len(keys))
	for _, key := range keys {
		result = append(result, *totals[key])
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
func (s *Store) ReadInterfaceClients(ctx context.Context, from, to int64) ([]InterfaceClients, error) {
	interfaces, err := s.readInterfaces(ctx)
	if err != nil {
		return nil, err
	}
	seen := map[int64]int64{}
	text, err := Statement("read_interface_seen_clients")
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, text, sql.Named("from", from), sql.Named("to", to))
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
		known, _, err := queryOptionalInt(ctx, s.db, "read_interface_known_clients",
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

func (s *Store) readInterfaces(ctx context.Context) ([]interfaceRow, error) {
	text, err := Statement("read_interfaces")
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, text)
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

// ReadBlockedDecisionCounts counts the refusals of [from, to) per engine kind.
func (s *Store) ReadBlockedDecisionCounts(ctx context.Context, from, to int64) (map[string]int64, error) {
	text, err := Statement("read_blocked_decision_counts")
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, text, sql.Named("from", from), sql.Named("to", to))
	if err != nil {
		return nil, fmt.Errorf("store: read_blocked_decision_counts: %w", err)
	}
	defer func() { _ = rows.Close() }()
	counts := map[string]int64{}
	for rows.Next() {
		var (
			kind  string
			count int64
		)
		if err := rows.Scan(&kind, &count); err != nil {
			return nil, fmt.Errorf("store: read_blocked_decision_counts: %w", err)
		}
		counts[kind] = count
	}
	return counts, rows.Err()
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
func (s *Store) ReadRuleLogging(ctx context.Context) (RuleLogging, error) {
	result := RuleLogging{NotLoggingByInterface: map[int64]int64{}}
	interfaces, err := s.readInterfaces(ctx)
	if err != nil {
		return result, err
	}
	byKey := map[string]int64{}
	for _, iface := range interfaces {
		byKey[iface.identifier] = iface.id
	}
	text, err := Statement("read_rules")
	if err != nil {
		return result, err
	}
	rows, err := s.db.QueryContext(ctx, text)
	if err != nil {
		return result, fmt.Errorf("store: read_rules: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var (
			id                   int64
			interfaceList        sql.NullString
			legacy, logsMatching sql.NullInt64
		)
		if err := rows.Scan(&id, &interfaceList, &legacy, &logsMatching); err != nil {
			return result, fmt.Errorf("store: read_rules: %w", err)
		}
		if !logsMatching.Valid {
			result.NotReported++
			continue
		}
		if logsMatching.Int64 != 0 {
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
		case legacy.Valid && legacy.Int64 == 1:
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
func (s *Store) ReadPublicAddresses(ctx context.Context) ([]PublicAddress, error) {
	interfaces, err := s.readInterfaces(ctx)
	if err != nil {
		return nil, err
	}
	text, err := Statement("read_interface_addresses")
	if err != nil {
		return nil, err
	}
	var result []PublicAddress
	for _, iface := range interfaces {
		if !iface.upstream {
			continue
		}
		rows, err := s.db.QueryContext(ctx, text, sql.Named("interface_id", iface.id))
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
			row.PrefixLength = nullableInt(prefix)
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

// nullableInt turns a nullable integer into a pointer.
func nullableInt(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	copied := value.Int64
	return &copied
}
