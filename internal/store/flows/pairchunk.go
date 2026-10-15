package flows

import (
	"context"
	"fmt"

	"github.com/NaejEL/opnview/internal/store/sqldb"
)

// The pairing, cut into chunks (B8b of specs/SPEC-step-5b-widget-api.md).
//
// WHY THE RESULT DOES NOT DEPEND ON THE CHUNKS. The decision for one `out` record --
// its only `in` candidate, when that candidate's only `out` candidate is it -- reads
// the records' logged fields and never their pairing columns, so it is the same
// whichever transaction takes it. What does depend on more than the record is the
// clearing of a partner a record no longer has: it is cleared unless ANOTHER record now
// pairs with it, and that other record may lie in another chunk. So a span of several
// chunks is done in two passes: every chunk's `out` records are decided first, into one
// map for the whole span, and only then is each chunk written, against that whole map.
// The writes are then exactly those one transaction over the whole span would have
// made, and a test asserts it on a fixture spanning several chunks.
//
// Each `out` record belongs to exactly one chunk: the chunks tile [from - window,
// to + window] without overlap. The `in` side needs no chunk of its own: an `in` record
// is written in the chunk of the `out` record it pairs with, or cleared in the chunk of
// the one it no longer pairs with.

// pairChunk is one stretch of the span, both ends inclusive, as pairing_out_records
// reads them.
type pairChunk struct {
	from, to int64
}

// pairChunks tiles [low, high] with chunks of at most size seconds.
func PairChunks(low, high, size int64) []pairChunk {
	if size <= 0 || high-low < size {
		return []pairChunk{{from: low, to: high}}
	}
	var chunks []pairChunk
	for start := low; start <= high; start += size {
		chunks = append(chunks, pairChunk{from: start, to: min(start+size-1, high)})
	}
	return chunks
}

// pairDecision is what the decision pass found: each paired `out` record's `in`
// record, and the reverse.
type pairDecision struct {
	desired map[int64]int64
	inverse map[int64]int64
	decided map[int64]struct{}
}

// pairInChunks is Pair with the chunk size a parameter, so a test can compare a span
// cut into several chunks with the same span in one.
func (s *FlowStore) PairInChunks(ctx context.Context, from, to, window, size int64) (Pairing, error) {
	var result Pairing
	if window < 0 {
		return result, fmt.Errorf("store: the pairing window must be 0 or more seconds, got %d", window)
	}
	if to < from {
		return result, nil
	}
	chunks := PairChunks(from-window, to+window, size)
	if len(chunks) == 1 {
		// One chunk: decided and written in one transaction, as the pairing always was.
		return s.pairOneTransaction(ctx, chunks[0], window)
	}

	upstream, err := upstreamDevices(ctx, s.SQL)
	if err != nil {
		return result, err
	}
	decision := pairDecision{desired: map[int64]int64{}, inverse: map[int64]int64{},
		decided: map[int64]struct{}{}}
	for _, chunk := range chunks {
		if err := s.decideChunk(ctx, chunk, window, decision); err != nil {
			return result, err
		}
	}
	for _, chunk := range chunks {
		instants, err := s.writeChunk(ctx, chunk, upstream, decision)
		if err != nil {
			return result, err
		}
		result.FlowInstants = append(result.FlowInstants, instants...)
	}
	result.Pairs = len(decision.desired)
	return result, nil
}

// pairOneTransaction decides and writes one chunk in one transaction.
func (s *FlowStore) pairOneTransaction(ctx context.Context, chunk pairChunk, window int64) (Pairing, error) {
	var result Pairing
	transaction, err := s.SQL.BeginTx(ctx, nil)
	if err != nil {
		return result, fmt.Errorf("store: starting a pairing: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	// The same statements run once per record; each text is prepared once per connection by the
	// driver's statement cache (internal/store/sqldb/driver.go).
	prepared := transaction

	upstream, err := upstreamDevices(ctx, prepared)
	if err != nil {
		return result, err
	}
	outs, err := chunkOuts(ctx, prepared, chunk)
	if err != nil {
		return result, err
	}
	decision := pairDecision{desired: map[int64]int64{}, inverse: map[int64]int64{},
		decided: map[int64]struct{}{}}
	if err := decide(ctx, prepared, outs, window, decision); err != nil {
		return result, err
	}
	result.FlowInstants, err = writePairing(ctx, prepared, outs, upstream, decision)
	if err != nil {
		return result, err
	}
	result.Pairs = len(decision.desired)
	if err := transaction.Commit(); err != nil {
		return result, fmt.Errorf("store: committing a pairing: %w", err)
	}
	return result, nil
}

// decideChunk decides one chunk's `out` records in a transaction that writes nothing.
func (s *FlowStore) decideChunk(ctx context.Context, chunk pairChunk, window int64, decision pairDecision) error {
	transaction, err := s.SQL.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: starting a pairing decision: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	prepared := transaction
	outs, err := chunkOuts(ctx, prepared, chunk)
	if err != nil {
		return err
	}
	return decide(ctx, prepared, outs, window, decision)
}

// writeChunk writes one chunk's outcomes against the whole span's decision. An `out`
// record stored after the decision pass read its chunk is left alone: it was not
// decided, and the pass that stored it asks for its own pairing.
func (s *FlowStore) writeChunk(ctx context.Context, chunk pairChunk, upstream map[string]bool,
	decision pairDecision) ([]int64, error) {
	transaction, err := s.SQL.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("store: starting a pairing: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	prepared := transaction
	read, err := chunkOuts(ctx, prepared, chunk)
	if err != nil {
		return nil, err
	}
	outs := read[:0]
	for _, out := range read {
		if _, decided := decision.decided[out.id]; decided {
			outs = append(outs, out)
		}
	}
	instants, err := writePairing(ctx, prepared, outs, upstream, decision)
	if err != nil {
		return nil, err
	}
	if err := transaction.Commit(); err != nil {
		return nil, fmt.Errorf("store: committing a pairing: %w", err)
	}
	return instants, nil
}

// chunkOuts reads the `out` records observed in one chunk.
func chunkOuts(ctx context.Context, q sqldb.Querier, chunk pairChunk) ([]pairRecord, error) {
	return pairRecords(ctx, q, "pairing_out_records", map[string]any{"from": chunk.from, "to": chunk.to})
}

// decide records what each `out` record should be paired with: its only candidate,
// when that candidate's only candidate is it.
func decide(ctx context.Context, q sqldb.Querier, outs []pairRecord, window int64, decision pairDecision) error {
	for _, out := range outs {
		decision.decided[out.id] = struct{}{}
		ins, err := sqldb.QueryInts(ctx, q, "pairing_in_candidates", out.parameters(window))
		if err != nil {
			return err
		}
		if len(ins) != 1 {
			continue
		}
		in, err := pairRecordByID(ctx, q, ins[0])
		if err != nil {
			return err
		}
		back, err := sqldb.QueryInts(ctx, q, "pairing_out_candidates", in.parameters(window))
		if err != nil {
			return err
		}
		if len(back) == 1 && back[0] == out.id {
			decision.desired[out.id] = in.id
			decision.inverse[in.id] = out.id
		}
	}
	return nil
}

// writePairing writes the outcome of some `out` records and of the `in` records they
// pair with or no longer pair with, against a decision that may cover more records
// than these, and returns the instants of every record whose outcome changed.
func writePairing(ctx context.Context, q sqldb.Querier, outs []pairRecord, upstream map[string]bool,
	decision pairDecision) ([]int64, error) {
	targets := map[int64]target{}
	order := []int64{}
	set := func(id int64, outcome *string, partner *int64) {
		if _, present := targets[id]; !present {
			order = append(order, id)
		}
		targets[id] = target{outcome: outcome, partner: partner}
	}
	first, second := PairFirstLeg, PairSecondLeg
	var pairedIns []int64
	for _, out := range outs {
		// A partner this record no longer has is cleared, unless another record of
		// the span now pairs with it.
		if out.outcome.Valid && out.outcome.String == PairSecondLeg && out.partner.Valid {
			previous := out.partner.Int64
			if decision.desired[out.id] != previous {
				if _, taken := decision.inverse[previous]; !taken {
					set(previous, nil, nil)
				}
			}
		}
		if in, paired := decision.desired[out.id]; paired {
			partner := in
			set(out.id, &second, &partner)
			pairedIns = append(pairedIns, in)
			continue
		}
		if out.srcFirewall == 1 && upstream[out.device] {
			outcome := PairNotPaired
			set(out.id, &outcome, nil)
			continue
		}
		set(out.id, nil, nil)
	}
	for _, in := range pairedIns {
		partner := decision.inverse[in]
		set(in, &first, &partner)
	}

	// Clearing first, then writing, so a record handed from one partner to another
	// in one transaction never transiently names two.
	var instants []int64
	for _, phase := range []bool{true, false} {
		for _, id := range order {
			wanted := targets[id]
			if (wanted.outcome == nil) != phase {
				continue
			}
			changed, err := sqldb.QueryInts(ctx, q, "pairing_record", map[string]any{
				"flow_id": id, "outcome": sqldb.OptionalText(wanted.outcome), "partner": sqldb.Optional(wanted.partner),
			})
			if err != nil {
				return nil, err
			}
			instants = append(instants, changed...)
		}
	}
	return instants, nil
}
