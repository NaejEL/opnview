package flows_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/NaejEL/opnview/internal/store"

	"github.com/NaejEL/opnview/internal/store/flows"
	"github.com/NaejEL/opnview/internal/store/storetest"
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
			network := storetest.NewNetwork(t, counts[0], counts[1])
			ctx := context.Background()
			start := network.Now - 1000
			var specs []storetest.Flow
			for index := 0; index < 60; index++ {
				client := network.Clients[index%len(network.Clients)]
				at := start + int64(index)*13
				// Every fifth connection is moved to the last second of a chunk, so its
				// second leg falls in the next one.
				if index%5 == 0 {
					at = start - flows.DefaultPairingWindowSeconds + ((at-start)/chunk+1)*chunk - 1
				}
				first, second := network.Legs(storetest.Connection{Client: client, Peer: storetest.Outside(index, client.V6),
					At: at, IPID: int64(5000 + index), Seq: int64(90000 + index), Lag: 1}, "")
				specs = append(specs, first, second)
			}
			// Two connections that look alike at a chunk boundary: neither out record has
			// one candidate, so neither pairs, whichever chunk reads it.
			v4 := storetest.ClientsOfFamily(network, false)
			boundary := start - flows.DefaultPairingWindowSeconds + 6*chunk - 1
			a, b := network.Legs(storetest.Connection{Client: v4[0], Peer: storetest.Outside(200, false), At: boundary,
				IPID: 7777, Seq: 31000, Lag: 1}, "")
			c, d := network.Legs(storetest.Connection{Client: v4[len(v4)-1], Peer: storetest.Outside(200, false), At: boundary,
				IPID: 7777, Seq: 31000, Lag: 1}, "")
			specs = append(specs, a, b, c, d)
			addresses := network.Insert(t, specs)
			if _, err := network.DB.Reclassify(ctx, addresses, network.Now); err != nil {
				t.Fatalf("classifying: %v", err)
			}

			from, to := start, network.Now
			whole := int64(1 << 40)
			if _, err := network.DB.PairInChunks(ctx, from, to, flows.DefaultPairingWindowSeconds, whole); err != nil {
				t.Fatalf("pairing in one transaction: %v", err)
			}
			correct := storetest.PairState(t, network.DB)
			if pairs := scalarInt(t, network.DB, "SELECT count(*) FROM flow WHERE pair_outcome = 'second_leg'"); pairs < 50 {
				t.Fatalf("only %d pairs were made, so the comparison is vacuous", pairs)
			}

			// Stale partners: every pair's out record is handed the next pair's in record,
			// so the pass has to clear and re-pair across chunks.
			perturb(t, network.DB)
			backup(t, network.DB)
			if storetest.PairState(t, network.DB) == correct {
				t.Fatal("the perturbation changed nothing, so the comparison is vacuous")
			}

			if _, err := network.DB.PairInChunks(ctx, from, to, flows.DefaultPairingWindowSeconds, whole); err != nil {
				t.Fatalf("pairing in one transaction: %v", err)
			}
			unchunked := storetest.PairState(t, network.DB)
			restore(t, network.DB)
			chunks := len(flows.PairChunks(from-flows.DefaultPairingWindowSeconds, to+flows.DefaultPairingWindowSeconds, chunk))
			if chunks < 10 {
				t.Fatalf("the span is cut into %d chunks, too few to test the cut", chunks)
			}
			if _, err := network.DB.PairInChunks(ctx, from, to, flows.DefaultPairingWindowSeconds, chunk); err != nil {
				t.Fatalf("pairing in chunks: %v", err)
			}
			chunked := storetest.PairState(t, network.DB)
			if chunked != unchunked {
				t.Errorf("the chunked pairing differs from the pairing in one transaction:\n%s\nwant\n%s",
					chunked, unchunked)
			}
			if unchunked != correct {
				t.Error("the pairing of the perturbed records is not the pairing of the clean ones")
			}
			again, err := network.DB.PairInChunks(ctx, from, to, flows.DefaultPairingWindowSeconds, chunk)
			if err != nil {
				t.Fatalf("pairing in chunks again: %v", err)
			}
			if len(again.FlowInstants) != 0 {
				t.Errorf("a second chunked run changed %d records", len(again.FlowInstants))
			}
			if outcome, _ := storetest.OutcomeOf(t, network.DB, b.Device, b.Src, b.Dst); outcome == flows.PairSecondLeg {
				t.Error("an out record with two candidates at a chunk boundary was paired")
			}
		})
	}
}

func scalarInt(t *testing.T, database *store.Store, query string) int64 {
	t.Helper()
	var value int64
	if err := database.DB().QueryRow(query).Scan(&value); err != nil {
		t.Fatalf("running %s: %v", query, err)
	}
	return value
}

// perturb hands every second leg's partner to the next pair, in id order, and every
// first leg to the previous out record, keeping each row's own CHECK.
func perturb(t *testing.T, database *store.Store) {
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
		if _, err := database.DB().Exec("UPDATE flow SET paired_flow_id = $1 WHERE id = $2", in, out); err != nil {
			t.Fatalf("perturbing: %v", err)
		}
		if _, err := database.DB().Exec("UPDATE flow SET paired_flow_id = $1 WHERE id = $2", out, in); err != nil {
			t.Fatalf("perturbing: %v", err)
		}
	}
}

func backup(t *testing.T, database *store.Store) {
	t.Helper()
	if _, err := database.DB().Exec(
		"CREATE TABLE pairing_backup AS SELECT id, pair_outcome, paired_flow_id FROM flow"); err != nil {
		t.Fatalf("keeping the perturbed state: %v", err)
	}
}

func restore(t *testing.T, database *store.Store) {
	t.Helper()
	if _, err := database.DB().Exec(`UPDATE flow SET
		pair_outcome = (SELECT b.pair_outcome FROM pairing_backup AS b WHERE b.id = flow.id),
		paired_flow_id = (SELECT b.paired_flow_id FROM pairing_backup AS b WHERE b.id = flow.id)`); err != nil {
		t.Fatalf("restoring the perturbed state: %v", err)
	}
}
