//go:build njrecord

package collect_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/NaejEL/opnview/internal/collect/collecttest"
)

// TestRecordTheGoJoinOverTheFixtures is PA10's recording over NJ12's fixtures F1 to F17 (and FA), run by
// hand before the join leaves Go:
//
//	go test -tags njrecord -run TestRecordTheGoJoinOverTheFixtures -count=1 ./internal/collect/
//
// It runs the fixtures as TestTheJoinWithEveryInterfaceCaptured does -- through the real v9 decoder and
// the real filter-log parser, forward records first -- in the world they share, F17 in its own, and F16's
// interleaved counters as TestLossIsDetectedUnderSeveralCounters does, and writes each world's recording
// under plugins/netflow/testdata/go-join/.
func TestRecordTheGoJoinOverTheFixtures(t *testing.T) {
	const provenance = "SYNTHESISED, NOT A CAPTURE: the Go join's output (internal/collect/derive and " +
		"internal/store/netflow, branch wip/backend-plugins, 17 October 2026) over NJ12's fixtures in " +
		"internal/collect/testdata/netflowjoin, which are synthesised from the capture analysis's shapes with " +
		"documentation addresses. Recorded by TestRecordTheGoJoinOverTheFixtures (build tag njrecord) for PA10 of " +
		"specs/SPEC-backend-completion-plugins.md; column-map.json maps each recorded column to its name after the " +
		"join leaves Go."
	out := filepath.Join("..", "..", "plugins", "netflow", "testdata", "go-join")
	world := collecttest.LoadJoinWorld(t, collecttest.JoinFixtureDirectory)
	fixtures, upstreamIngress := splitByCapture(collecttest.LoadJoinFixtures(t, collecttest.JoinFixtureDirectory))

	h := runJoinWorld(t, world, fixtures, orderForwardFirst, nil)
	dumpJoin(t, h, filepath.Join(out, "fixtures.json"), provenance, nil)

	h = runJoinWorld(t, world, upstreamIngress, orderForwardFirst, nil)
	dumpJoin(t, h, filepath.Join(out, "f17_upstream_ingress_captured.json"), provenance, nil)

	var counters struct {
		Provenance          map[string]any `json:"__provenance__"`
		Name                string         `json:"name"`
		Counters            []uint32       `json:"counters"`
		DatagramsPerCounter int            `json:"datagrams_per_counter"`
		Dropped             struct {
			Counter  int `json:"counter"`
			Datagram int `json:"datagram"`
		} `json:"dropped"`
		Record collecttest.FixtureRecord `json:"record"`
	}
	raw, err := os.ReadFile(filepath.Join(collecttest.JoinFixtureDirectory, "f16_interleaved_counters.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &counters); err != nil {
		t.Fatal(err)
	}
	h = collecttest.NewNetflowWorldHarness(t, world.Body, world.Offered())
	h.Capture(t, collecttest.CaptureStartAt, world.Captured(nil)...)
	h.At(collecttest.NetflowReadSecond)
	export := collecttest.LegInstant + 10
	for round := 0; round < counters.DatagramsPerCounter; round++ {
		for counter, first := range counters.Counters {
			export++
			if counter == counters.Dropped.Counter && round == counters.Dropped.Datagram {
				continue
			}
			record := counters.Record.FlowRecord()
			record.SrcPort = uint16(40000 + counter*100 + round)
			h.DeliverRaw(t, collecttest.Datagram(export, first+uint32(round), record))
		}
	}
	h.RecordGaps()
	rows, err := h.Store.DB().QueryContext(context.Background(),
		`SELECT (to_jsonb(g) - 'id' - 'provider_id')::text FROM collection_gap g`)
	if err != nil {
		t.Fatal(err)
	}
	var gaps []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		gaps = append(gaps, line)
	}
	_ = rows.Close()
	sort.Strings(gaps)
	dumpJoin(t, h, filepath.Join(out, "f16_interleaved_counters.json"), provenance, map[string][]string{"collection_gap": gaps})
}
