package store_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NaejEL/opnview/internal/store/storetest"
	"github.com/NaejEL/opnview/internal/store/vocab"
)

// The second iteration of the step-5A corrections: the purged part and the purge under a
// short retention, and the roadmap's live-validation checklist (AC1, AC2, AC12).

// TestThePurgeRecordsHowFarBackItHasPurged is the store's half of the fix for a flow stored
// again after its purge: the purge records the furthest point it has purged to, and that
// watermark only moves forward, even when the retention is lengthened later.
func TestThePurgeRecordsHowFarBackItHasPurged(t *testing.T) {
	t.Parallel()
	network := storetest.NewNetwork(t, 2, 2)
	ctx := context.Background()
	if _, found, err := network.DB.PurgedBefore(ctx); err != nil || found {
		t.Fatalf("before any purge the watermark reads %v, %v", found, err)
	}
	if err := network.DB.SetSetting(ctx, "retention_seconds", "600", network.Now); err != nil {
		t.Fatalf("setting the retention: %v", err)
	}
	if err := network.DB.Purge(ctx, network.Now); err != nil {
		t.Fatalf("purging: %v", err)
	}
	if before, found, err := network.DB.PurgedBefore(ctx); err != nil || !found || before != network.Now-600 {
		t.Errorf("after a purge the watermark reads %d, %v, %v, not %d", before, found, err, network.Now-600)
	}
	// A longer retention purges less far back; the watermark keeps the furthest point.
	if err := network.DB.SetSetting(ctx, "retention_seconds", "86400", network.Now); err != nil {
		t.Fatalf("setting the retention: %v", err)
	}
	if err := network.DB.Purge(ctx, network.Now+60); err != nil {
		t.Fatalf("purging: %v", err)
	}
	if before, _, _ := network.DB.PurgedBefore(ctx); before != network.Now-600 {
		t.Errorf("a longer retention moved the watermark back to %d", before)
	}
}

// TestALeaseNamingAPurgedAddressMovesItsPurgedPart: when a lease names an address all of
// whose flows in an hour were purged, the purged part moves to the better identity as the
// flows would have, and the client, peer and owner families keep every byte and every peer.
func TestALeaseNamingAPurgedAddressMovesItsPurgedPart(t *testing.T) {
	t.Parallel()
	network := storetest.NewNetwork(t, 2, 4)
	ctx := context.Background()
	var client storetest.Client
	for _, candidate := range network.Clients {
		if !candidate.Leased {
			client = candidate
			break
		}
	}
	hour := vocab.PeriodHour.SlotStart(network.Now)
	if err := network.DB.SetSetting(ctx, "retention_seconds", fmt.Sprint(network.Now-(hour+400)),
		network.Now); err != nil {
		t.Fatalf("setting the retention: %v", err)
	}
	addresses := network.Insert(t, []storetest.Flow{
		outboundFlow(client, hour+100, 100, 1, 0),
		outboundFlow(client, hour+200, 200, 2, 0),
	})
	network.ClassifyAndRefresh(t, addresses)
	if err := network.DB.Purge(ctx, network.Now); err != nil {
		t.Fatalf("purging: %v", err)
	}

	mac := "00:00:00:00:fe:03"
	leased, err := network.DB.UpsertClient(ctx, vocab.Client{
		Identity: vocab.ClientIdentity{Kind: vocab.IdentityMAC, Key: mac}, InterfaceID: &client.InterfaceID,
		MAC: &mac, LastAddress: &client.Address,
	}, network.Now)
	if err != nil {
		t.Fatalf("writing the leased client: %v", err)
	}
	result, err := network.DB.Reclassify(ctx, []string{client.Address}, network.Now)
	if err != nil {
		t.Fatalf("classifying: %v", err)
	}
	if _, err := storetest.RefreshAggregates(ctx, network.DB, 0, network.Now+5, result.FlowInstants); err != nil {
		t.Fatalf("refreshing: %v", err)
	}
	for _, period := range []vocab.Period{vocab.PeriodHour, vocab.PeriodDay} {
		for _, family := range []string{"volume", "client", "peer", "owner"} {
			if got := slotBytes(t, network.DB, family, period, hour); got != 300 {
				t.Errorf("the %s %s slot holds %d bytes after the lease, not 300", period.Name, family, got)
			}
		}
		if got := storetest.QueryInt(t, network.DB, "SELECT coalesce(sum(bytes), 0) FROM client_volume_aggregate_"+
			period.Name+" WHERE period_start_at = $1 AND client_id = $2", period.SlotStart(hour), leased); got != 300 {
			t.Errorf("the %s client slot of the better identity holds %d bytes, not 300", period.Name, got)
		}
		if peers := storetest.QueryInt(t, network.DB, "SELECT coalesce(max(distinct_peers), 0) FROM client_volume_aggregate_"+
			period.Name+" WHERE period_start_at = $1", period.SlotStart(hour)); peers != 2 {
			t.Errorf("the %s client slot counts %d distinct peers, not 2", period.Name, peers)
		}
	}
}

// TestAPurgeUnderAShortRetentionKeepsEveryFamilyWhole is AC1 with purges that actually
// remove flows. Under each retention, every slot of every family still equals the sums over
// every flow ever ingested, the purged ones included: no family loses a client's bytes to
// the client being purged, and none loses a site name to its lookup being purged first.
func TestAPurgeUnderAShortRetentionKeepsEveryFamilyWhole(t *testing.T) {
	t.Parallel()
	for _, retention := range []int64{1800, 600, 1} {
		t.Run(fmt.Sprintf("retention %d", retention), func(t *testing.T) {
			t.Parallel()
			network := storetest.NewNetwork(t, 2, 6)
			ctx := context.Background()
			addresses := network.Insert(t, network.FlowsFor(200, 6*3600))
			network.ClassifyAndRefresh(t, addresses)
			attributeNetwork(t, network)
			flows := readFlows(t, network.DB)
			instants := allFlowInstants(t, network.DB)

			if err := network.DB.SetSetting(ctx, "retention_seconds", fmt.Sprint(retention), network.Now); err != nil {
				t.Fatalf("setting the retention: %v", err)
			}
			if err := network.DB.Purge(ctx, network.Now); err != nil {
				t.Fatalf("purging: %v", err)
			}
			if left := storetest.QueryInt(t, network.DB, "SELECT count(*) FROM flow"); left >= int64(len(flows)) {
				t.Fatalf("the purge removed no flow (%d left)", left)
			}
			if _, err := storetest.RefreshAggregates(ctx, network.DB, 0, network.Now+5, instants); err != nil {
				t.Fatalf("refreshing: %v", err)
			}
			for _, period := range vocab.Periods() {
				for _, family := range allFamilies {
					expected := expectedFamily(flows, period, family)
					stored := storedFamily(t, network.DB, period, family)
					for key, want := range expected {
						if got := stored[key]; got != want {
							t.Errorf("after the purge the %s slot %s of the %s family holds %+v, every flow "+
								"ingested sums to %+v", period.Name, key, family, got, want)
						}
					}
					for key := range stored {
						if _, present := expected[key]; !present {
							t.Errorf("after the purge the %s family holds %s %s, which no flow produces",
								family, period.Name, key)
						}
					}
				}
			}
		})
	}
}

// liveValidationChecklist returns the validation paragraph of step 5 of a roadmap.
func liveValidationChecklist(t *testing.T, text string) string {
	t.Helper()
	start := strings.Index(text, "### Step 5 ")
	end := strings.Index(text, "### Step 6 ")
	if start < 0 || end < start {
		t.Fatal("the roadmap has no step 5 section")
	}
	step := text[start:end]
	validation := strings.Index(step, "**Validation**")
	if validation < 0 {
		t.Fatal("step 5 has no Validation paragraph")
	}
	return step[validation:]
}

// checklistGaps names every item of the step-5 live-validation checklist a validation
// paragraph does not carry.
func checklistGaps(paragraph string) []string {
	joined := strings.Join(strings.Fields(paragraph), " ")
	var missing []string
	for _, item := range []struct{ what, phrase string }{
		{"the gateway-status field names", "/api/routes/gateway/status"},
		{"the gateway-status placeholder", "`~`"},
		{"the swap response", "systemSwap"},
		{"the span of a traffic/top sample", "`traffic/top` sample covers"},
		{"a tunnel carrying gateways[]", "tunnel carrying `gateways[]`"},
		{"iftop counting a client on two interfaces", "`iftop`"},
		{"the FreeBSD release wording", "`releng`"},
		{"how often Unbound logs a host name", "host name rather than an address"},
	} {
		if !strings.Contains(joined, item.phrase) {
			missing = append(missing, item.what)
		}
	}
	if items := strings.Count(paragraph, "\n- "); items < 7 {
		missing = append(missing, fmt.Sprintf("seven items (%d listed)", items))
	}
	return missing
}

// TestRoadmapStepFiveCarriesTheLiveValidationChecklist is AC12.
func TestRoadmapStepFiveCarriesTheLiveValidationChecklist(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.Join("..", "..", "ROADMAP.md"))
	if err != nil {
		t.Fatalf("reading the roadmap: %v", err)
	}
	for _, missing := range checklistGaps(liveValidationChecklist(t, string(raw))) {
		t.Errorf("the step-5 validation does not list %s", missing)
	}
	// And the check has teeth: the paragraph as it stood before the checklist fails it.
	before := "### Step 5 x\n\n**Validation**: the numbers are correct, and the heuristic's attribution rate\nis honest.\n\n### Step 6 y\n"
	if len(checklistGaps(liveValidationChecklist(t, before))) == 0 {
		t.Error("a validation paragraph with no checklist passes the check")
	}
}
