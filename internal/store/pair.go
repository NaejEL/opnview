package store

import (
	"context"
	"database/sql"
	"fmt"
)

// Two legs of one connection.
//
// pf writes the packet that creates a state (pf.conf(5), FreeBSD 15.1-RELEASE, `log`:
// "Only the packet that establishes the state is logged"), and a connection that
// crosses the firewall creates a state on the interface it arrives on and on the one
// it leaves by. So a client's connection to the Internet is logged `in` on the
// client's interface and `out` on the upstream interface: two records of one
// connection, and 5A counted both. Under outbound NAT the second record's source is
// already the firewall's address on the upstream interface, because "translation
// occurs before filtering" (pf.conf(5), "NAT ruleset (pre-FreeBSD 15)"; "NAT rules are
// always processed before filter rules!", https://docs.opnsense.org/manual/firewall.html,
// processing order).
//
// THE RULE. An `out` record is the second leg of an `in` record on another interface
// when they carry the same protocol, IP version, destination address and destination
// port, every logged field verified to cross unchanged is equal (PairingFields), they
// were logged within the window of setting leg_pairing_window_seconds, the `in`
// record passed, and each is the other's only candidate. Under outbound NAT the
// second leg's source is this firewall; without it the two sources and their ports
// are equal. The decision is taken from the stored records alone, so it does not
// depend on the order they arrived in, it is taken again when either leg arrives
// later -- the collector passes the window of every record it stores or places --
// and it writes a row only when the row's outcome changes.
//
// THE OUTCOMES, recorded on each record with its partner (flow.pair_outcome,
// flow.paired_flow_id): 'first_leg' and 'second_leg' for a pair; and for an unpaired
// `out` record on an upstream interface whose source is this firewall, 'not_paired',
// always: it keeps its this-firewall end, but nothing opnview reads tells the firewall's
// own traffic from a client's NATed connection whose first record it cannot see -- a
// rule that does not log, an automatic port-forward rule, an `rdr pass` -- so opnview
// never claims the first (decision 3, as amended on 7 October 2026). No unpaired record
// is given a client: its source is this firewall, which never is one.

// PairingWindowKey is the setting row holding how far apart, in seconds, the two
// records of one connection may have been logged, and DefaultPairingWindowSeconds the
// window when the row is absent: the maintainer's decision 4. The schema writes the
// default row; internal/config refuses a malformed one.
const (
	PairingWindowKey            = "leg_pairing_window_seconds"
	DefaultPairingWindowSeconds = 1
)

// The pairing outcomes, stored in flow.pair_outcome. The terms are opnview's own: pf
// and OPNsense log records and have no word for one being the second of a pair.
const (
	PairFirstLeg  = "first_leg"
	PairSecondLeg = "second_leg"
	PairNotPaired = "not_paired"
)

// PairingField is one field the filter log reports that could tell two connections
// apart, and whether its invariance across the two legs of one connection is verified.
type PairingField struct {
	// LogField is the filter log's own field name.
	LogField string
	// Column is the flow column holding it, empty when it is not stored.
	Column string
	// Verified says the field is shown to cross the firewall unchanged under
	// OPNsense's defaults. Only a verified field is read by the pairing.
	Verified bool
	// Evidence is the source of the verdict, or UNVERIFIED: and why.
	Evidence string
}

// PairingFields is every candidate the survey's response shape offers, with its
// verdict (docs/opnsense-api-survey.md, "Two legs of one connection, read for the
// step-5A live corrections"). A test fails if a pairing statement reads a field that
// is not verified here.
var PairingFields = []PairingField{
	{LogField: "id", Column: "ip_id", Verified: true,
		Evidence: "pf rewrites the IPv4 identification field only under scrub random-id (pf.conf(5): " +
			"\"Replaces the IP identification field with random values\"), which OPNsense adds only when " +
			"its IP Random id setting is on (opnsense/core 26.7.3, src/etc/inc/filter.inc line 577, scrubrnid), " +
			"off by default, or a Normalization rule sets Random ID for the traffic it covers (line 563); " +
			"translation rewrites addresses and ports (pf.conf(5), TRANSLATION)"},
	{LogField: "seq", Column: "tcp_seq", Verified: true,
		Evidence: "pf rewrites the TCP sequence number only under modulate state or synproxy state " +
			"(pf.conf(5), STATE MODULATION), and an OPNsense rule keeps state by default " +
			"(Firewall/Filter.xml, statetype, <Default>keep</Default>; " +
			"docs.opnsense.org, manual/firewall.html, State type)"},
	{LogField: "flow", Verified: false,
		Evidence: "UNVERIFIED: neither pf.conf(5) nor docs.opnsense.org says whether pf rewrites the IPv6 " +
			"flow label"},
	{LogField: "datalen", Verified: false,
		Evidence: "UNVERIFIED: no source states that the logged payload length is equal on both legs; " +
			"fragment reassembly by scrub, on by default, can change what a leg logs"},
	{LogField: "length", Column: "packet_bytes", Verified: false,
		Evidence: "UNVERIFIED: as datalen, the packet length a leg logs after scrub's reassembly is not " +
			"shown equal on both legs"},
	{LogField: "ttl", Verified: false,
		Evidence: "UNVERIFIED: no cited source states what a forwarded packet's logged ttl is on each " +
			"leg, and it is expected to differ"},
	{LogField: "hoplimit", Verified: false,
		Evidence: "UNVERIFIED: for the reason ttl is"},
}

// Pairing is what one pairing pass changed.
type Pairing struct {
	// FlowInstants are the observed_at instants of every record whose outcome changed:
	// the slots a refresh must recompute and the flows whose attribution is decided
	// again.
	FlowInstants []int64
	// Pairs is how many pairs the window holds after the pass.
	Pairs int
}

// pairRecord is one record's pairing fields.
type pairRecord struct {
	id          int64
	at          int64
	device      string
	srcAddress  string
	srcPort     sql.NullInt64
	dstAddress  string
	dstPort     sql.NullInt64
	protocol    string
	ipVersion   int64
	ipID        sql.NullInt64
	tcpSeq      sql.NullInt64
	srcFirewall int64
	outcome     sql.NullString
	partner     sql.NullInt64
}

func scanPairRecord(scanner interface{ Scan(...any) error }) (pairRecord, error) {
	var record pairRecord
	err := scanner.Scan(&record.id, &record.at, &record.device, &record.srcAddress, &record.srcPort,
		&record.dstAddress, &record.dstPort, &record.protocol, &record.ipVersion, &record.ipID,
		&record.tcpSeq, &record.srcFirewall, &record.outcome, &record.partner)
	return record, err
}

// parameters renders a record's pairing fields for the candidate statements.
func (r pairRecord) parameters(window int64) map[string]any {
	return map[string]any{
		"dst_address": r.dstAddress, "at": r.at, "window": window, "device": r.device,
		"protocol": r.protocol, "ip_version": r.ipVersion, "dst_port": nullableValue(r.dstPort),
		"ip_id": nullableValue(r.ipID), "tcp_seq": nullableValue(r.tcpSeq),
		"src_is_this_firewall": r.srcFirewall, "src_address": r.srcAddress,
		"src_port": nullableValue(r.srcPort),
	}
}

func nullableValue(value sql.NullInt64) any {
	if !value.Valid {
		return nil
	}
	return value.Int64
}

// target is the outcome a record should carry.
type target struct {
	outcome *string
	partner *int64
}

// Pair decides the pairing of every `out` record observed in [from - window,
// to + window] and of the `in` records they can pair with, and records what changed.
func (s *Store) Pair(ctx context.Context, from, to, window int64) (Pairing, error) {
	var result Pairing
	if window < 0 {
		return result, fmt.Errorf("store: the pairing window must be 0 or more seconds, got %d", window)
	}
	if to < from {
		return result, nil
	}
	transaction, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return result, fmt.Errorf("store: starting a pairing: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	// The same statements run once per record, so each is prepared once (exec.go).
	prepared := newPreparedTx(transaction)

	upstream, err := upstreamDevices(ctx, prepared)
	if err != nil {
		return result, err
	}
	outs, err := pairRecords(ctx, prepared, "pairing_out_records",
		map[string]any{"from": from - window, "to": to + window})
	if err != nil {
		return result, err
	}

	// What each `out` record should be paired with: its only candidate, when that
	// candidate's only candidate is it.
	desired := map[int64]int64{}
	inverse := map[int64]int64{}
	for _, out := range outs {
		ins, err := queryInts(ctx, prepared, "pairing_in_candidates", out.parameters(window))
		if err != nil {
			return result, err
		}
		if len(ins) != 1 {
			continue
		}
		in, err := pairRecordByID(ctx, prepared, ins[0])
		if err != nil {
			return result, err
		}
		back, err := queryInts(ctx, prepared, "pairing_out_candidates", in.parameters(window))
		if err != nil {
			return result, err
		}
		if len(back) == 1 && back[0] == out.id {
			desired[out.id] = in.id
			inverse[in.id] = out.id
		}
	}

	targets := map[int64]target{}
	order := []int64{}
	set := func(id int64, outcome *string, partner *int64) {
		if _, present := targets[id]; !present {
			order = append(order, id)
		}
		targets[id] = target{outcome: outcome, partner: partner}
	}
	first, second := PairFirstLeg, PairSecondLeg
	for _, out := range outs {
		// A partner this record no longer has is cleared, unless another record of
		// the window now pairs with it.
		if out.outcome.Valid && out.outcome.String == PairSecondLeg && out.partner.Valid {
			previous := out.partner.Int64
			if desired[out.id] != previous {
				if _, taken := inverse[previous]; !taken {
					set(previous, nil, nil)
				}
			}
		}
		if in, paired := desired[out.id]; paired {
			partner := in
			set(out.id, &second, &partner)
			continue
		}
		if out.srcFirewall == 1 && upstream[out.device] {
			outcome := PairNotPaired
			set(out.id, &outcome, nil)
			continue
		}
		set(out.id, nil, nil)
	}
	for in, out := range inverse {
		partner := out
		set(in, &first, &partner)
	}
	result.Pairs = len(desired)

	// Clearing first, then writing, so a record handed from one partner to another
	// in one pass never transiently names two.
	for _, phase := range []bool{true, false} {
		for _, id := range order {
			wanted := targets[id]
			if (wanted.outcome == nil) != phase {
				continue
			}
			instants, err := queryInts(ctx, prepared, "pairing_record", map[string]any{
				"flow_id": id, "outcome": optionalText(wanted.outcome), "partner": optional(wanted.partner),
			})
			if err != nil {
				return result, err
			}
			result.FlowInstants = append(result.FlowInstants, instants...)
		}
	}
	if err := transaction.Commit(); err != nil {
		return result, fmt.Errorf("store: committing a pairing: %w", err)
	}
	return result, nil
}

// optionalText renders a nullable text as a bindable value.
func optionalText(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

// pairRecords reads the records one statement returns.
func pairRecords(ctx context.Context, q querier, name string, parameters map[string]any) (
	[]pairRecord, error) {
	text, err := Statement(name)
	if err != nil {
		return nil, err
	}
	rows, err := q.QueryContext(ctx, text, named(text, parameters)...)
	if err != nil {
		return nil, fmt.Errorf("store: %s: %w", name, err)
	}
	defer func() { _ = rows.Close() }()
	var records []pairRecord
	for rows.Next() {
		record, err := scanPairRecord(rows)
		if err != nil {
			return nil, fmt.Errorf("store: %s: %w", name, err)
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: %s: %w", name, err)
	}
	return records, nil
}

// pairRecordByID reads one record's pairing fields.
func pairRecordByID(ctx context.Context, q querier, id int64) (pairRecord, error) {
	records, err := pairRecords(ctx, q, "pairing_record_fields", map[string]any{"flow_id": id})
	if err != nil {
		return pairRecord{}, err
	}
	if len(records) != 1 {
		return pairRecord{}, fmt.Errorf("store: the flow %d to pair is not stored", id)
	}
	return records[0], nil
}

// upstreamDevices returns the devices of the upstream interfaces.
func upstreamDevices(ctx context.Context, q querier) (map[string]bool, error) {
	rows, err := q.QueryContext(ctx, "SELECT device FROM interface WHERE is_upstream = 1")
	if err != nil {
		return nil, fmt.Errorf("store: reading the upstream interfaces: %w", err)
	}
	defer func() { _ = rows.Close() }()
	devices := map[string]bool{}
	for rows.Next() {
		var device string
		if err := rows.Scan(&device); err != nil {
			return nil, fmt.Errorf("store: reading the upstream interfaces: %w", err)
		}
		devices[device] = true
	}
	return devices, rows.Err()
}
