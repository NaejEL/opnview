package store

import (
	"context"
	"fmt"
	"testing"

	"github.com/NaejEL/opnview/internal/store/flows"
)

// TestChunkedAndUnchunkedPairingAreIdentical is AC29 of
// specs/SPEC-step-5b-widget-api.md (B8b): over a fixture spanning several chunks --
// pairs straddling a chunk boundary, an ambiguous pair at a boundary, and stale
// partners handed from one record to another across chunks -- the pairing cut into
// chunks writes the same pair_outcome and paired_flow_id as one transaction over the
// whole span, and a second run changes nothing.
func TestChunkedAndUnchunkedPairingAreIdentical(t *testing.T) {
	t.Parallel()
	for _, counts := range [][2]int{{2, 4}, {3, 7}} {
		t.Run(fmt.Sprintf("%d interfaces, %d clients", counts[0], counts[1]), func(t *testing.T) {
			t.Parallel()
			const chunk = int64(50)
			network := newTestNetwork(t, counts[0], counts[1])
			ctx := context.Background()
			start := network.now - 1000
			var specs []networkFlow
			for index := 0; index < 60; index++ {
				client := network.clients[index%len(network.clients)]
				at := start + int64(index)*13
				// Every fifth connection is moved to the last second of a chunk, so its
				// second leg falls in the next one.
				if index%5 == 0 {
					at = start - flows.DefaultPairingWindowSeconds + ((at-start)/chunk+1)*chunk - 1
				}
				first, second := network.legs(connection{client: client, peer: outside(index, client.v6),
					at: at, ipID: int64(5000 + index), seq: int64(90000 + index), lag: 1}, "")
				specs = append(specs, first, second)
			}
			// Two connections that look alike at a chunk boundary: neither out record has
			// one candidate, so neither pairs, whichever chunk reads it.
			v4 := clientsOfFamily(network, false)
			boundary := start - flows.DefaultPairingWindowSeconds + 6*chunk - 1
			a, b := network.legs(connection{client: v4[0], peer: outside(200, false), at: boundary,
				ipID: 7777, seq: 31000, lag: 1}, "")
			c, d := network.legs(connection{client: v4[len(v4)-1], peer: outside(200, false), at: boundary,
				ipID: 7777, seq: 31000, lag: 1}, "")
			specs = append(specs, a, b, c, d)
			addresses := network.insert(t, specs)
			if _, err := network.db.Reclassify(ctx, addresses, network.now); err != nil {
				t.Fatalf("classifying: %v", err)
			}

			from, to := start, network.now
			whole := int64(1 << 40)
			if _, err := network.db.PairInChunks(ctx, from, to, flows.DefaultPairingWindowSeconds, whole); err != nil {
				t.Fatalf("pairing in one transaction: %v", err)
			}
			correct := pairState(t, network.db)
			if pairs := scalarInt(t, network.db, "SELECT count(*) FROM flow WHERE pair_outcome = 'second_leg'"); pairs < 50 {
				t.Fatalf("only %d pairs were made, so the comparison is vacuous", pairs)
			}

			// Stale partners: every pair's out record is handed the next pair's in record,
			// so the pass has to clear and re-pair across chunks.
			perturb(t, network.db)
			backup(t, network.db)
			if pairState(t, network.db) == correct {
				t.Fatal("the perturbation changed nothing, so the comparison is vacuous")
			}

			if _, err := network.db.PairInChunks(ctx, from, to, flows.DefaultPairingWindowSeconds, whole); err != nil {
				t.Fatalf("pairing in one transaction: %v", err)
			}
			unchunked := pairState(t, network.db)
			restore(t, network.db)
			chunks := len(flows.PairChunks(from-flows.DefaultPairingWindowSeconds, to+flows.DefaultPairingWindowSeconds, chunk))
			if chunks < 10 {
				t.Fatalf("the span is cut into %d chunks, too few to test the cut", chunks)
			}
			if _, err := network.db.PairInChunks(ctx, from, to, flows.DefaultPairingWindowSeconds, chunk); err != nil {
				t.Fatalf("pairing in chunks: %v", err)
			}
			chunked := pairState(t, network.db)
			if chunked != unchunked {
				t.Errorf("the chunked pairing differs from the pairing in one transaction:\n%s\nwant\n%s",
					chunked, unchunked)
			}
			if unchunked != correct {
				t.Error("the pairing of the perturbed records is not the pairing of the clean ones")
			}
			again, err := network.db.PairInChunks(ctx, from, to, flows.DefaultPairingWindowSeconds, chunk)
			if err != nil {
				t.Fatalf("pairing in chunks again: %v", err)
			}
			if len(again.FlowInstants) != 0 {
				t.Errorf("a second chunked run changed %d records", len(again.FlowInstants))
			}
			if outcome, _ := outcomeOf(t, network.db, b.device, b.src, b.dst); outcome == flows.PairSecondLeg {
				t.Error("an out record with two candidates at a chunk boundary was paired")
			}
		})
	}
}

func scalarInt(t *testing.T, database *Store, query string) int64 {
	t.Helper()
	var value int64
	if err := database.DB().QueryRow(query).Scan(&value); err != nil {
		t.Fatalf("running %s: %v", query, err)
	}
	return value
}

// perturb hands every second leg's partner to the next pair, in id order, and every
// first leg to the previous out record, keeping each row's own CHECK.
func perturb(t *testing.T, database *Store) {
	t.Helper()
	rows, err := database.DB().Query(
		"SELECT id, paired_flow_id FROM flow WHERE pair_outcome = 'second_leg' ORDER BY id")
	if err != nil {
		t.Fatalf("reading the pairs: %v", err)
	}
	var outs, ins []int64
	for rows.Next() {
		var out, in int64
		if err := rows.Scan(&out, &in); err != nil {
			t.Fatalf("reading the pairs: %v", err)
		}
		outs, ins = append(outs, out), append(ins, in)
	}
	_ = rows.Close()
	for index, out := range outs {
		in := ins[(index+1)%len(ins)]
		if _, err := database.DB().Exec("UPDATE flow SET paired_flow_id = ? WHERE id = ?", in, out); err != nil {
			t.Fatalf("perturbing: %v", err)
		}
		if _, err := database.DB().Exec("UPDATE flow SET paired_flow_id = ? WHERE id = ?", out, in); err != nil {
			t.Fatalf("perturbing: %v", err)
		}
	}
}

func backup(t *testing.T, database *Store) {
	t.Helper()
	if _, err := database.DB().Exec(
		"CREATE TABLE pairing_backup AS SELECT id, pair_outcome, paired_flow_id FROM flow"); err != nil {
		t.Fatalf("keeping the perturbed state: %v", err)
	}
}

func restore(t *testing.T, database *Store) {
	t.Helper()
	if _, err := database.DB().Exec(`UPDATE flow SET
		pair_outcome = (SELECT b.pair_outcome FROM pairing_backup AS b WHERE b.id = flow.id),
		paired_flow_id = (SELECT b.paired_flow_id FROM pairing_backup AS b WHERE b.id = flow.id)`); err != nil {
		t.Fatalf("restoring the perturbed state: %v", err)
	}
}
