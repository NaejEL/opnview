package store

import (
	"context"
	"fmt"
	"testing"

	"github.com/NaejEL/opnview/internal/store/samples"
	"github.com/NaejEL/opnview/internal/store/vocab"
)

// TestThePerPeerCurrentRateIsEachInsidePairReadingAndTheUpstreamReadingIsNotSummed is row 24 of the
// mockup's coverage (docs/widget-api.md): the per-pair readings of traffic/top, stored per local
// address and peer, are the current rate per peer -- inbound what the peer sent, outbound what was
// sent to it, measured only for a local address read with that one peer -- per client and over
// every inside local address; the same conversation read on the upstream interface, whose local
// address is the firewall's own, is not summed again.
func TestThePerPeerCurrentRateIsEachInsidePairReadingAndTheUpstreamReadingIsNotSummed(t *testing.T) {
	t.Parallel()
	network := newTestNetwork(t, 2, 4)
	ctx := context.Background()
	if err := network.db.SetSetting(ctx, samples.MeasurementIntervalKey, fmt.Sprint(300), network.now); err != nil {
		t.Fatalf("writing the interval: %v", err)
	}
	at := network.now - 100
	pairIn := func(device, local, peer string, in float64) {
		insertReading(t, network.db, vocab.SubjectInterfaceEndpointPair, vocab.InterfaceEndpointPairKey(device, local, peer),
			vocab.MeasureRateBitsIn, vocab.UnitBitPerSecond, in, at)
	}
	pairOut := func(device, local, peer string, out float64) {
		insertReading(t, network.db, vocab.SubjectInterfaceEndpointPair, vocab.InterfaceEndpointPairKey(device, local, peer),
			vocab.MeasureRateBitsOut, vocab.UnitBitPerSecond, out, at)
	}
	endpoint := func(device, local string, in, out float64) {
		key := vocab.InterfaceEndpointKey(device, local)
		insertReading(t, network.db, vocab.SubjectInterfaceEndpoint, key, vocab.MeasureRateBitsIn, vocab.UnitBitPerSecond, in, at)
		insertReading(t, network.db, vocab.SubjectInterfaceEndpoint, key, vocab.MeasureRateBitsOut, vocab.UnitBitPerSecond, out, at)
	}
	shared := outside(1, network.clients[0].v6)
	only := outside(2, network.clients[0].v6)

	// The first client talks to two peers: no per-pair outbound figure exists.
	first := network.clients[0]
	endpoint(first.device, first.address, 130, 240)
	pairIn(first.device, first.address, shared, 100)
	pairIn(first.device, first.address, only, 30)
	// The second leased client talks to the shared peer alone: its total is the pair's outbound figure.
	second := network.clients[3]
	endpoint(second.device, second.address, 50, 70)
	pairIn(second.device, second.address, shared, 50)
	pairOut(second.device, second.address, shared, 70)
	// The upstream reading of the same conversations, after NAT.
	endpoint(network.upstream.device, network.upstream.address, 5000, 5000)
	pairIn(network.upstream.device, network.upstream.address, shared, 5000)

	rates, err := network.db.ReadCurrentRates(ctx, network.now)
	if err != nil {
		t.Fatalf("reading the rates: %v", err)
	}
	if rate := rates.ByPeer[shared]; rate.InBitsPerSecond != 150 || rate.OutMeasured || rate.SampledAt != at {
		t.Errorf("the shared peer reads %+v, not 150 in from the two inside clients and an unmeasured out", rate)
	}
	if rate := rates.ByPeer[only]; rate.InBitsPerSecond != 30 || rate.OutMeasured {
		t.Errorf("the other peer reads %+v", rate)
	}
	if _, present := rates.ByPeer[network.upstream.address]; present || len(rates.ByPeer) != 2 {
		t.Errorf("the peers are %v; the upstream reading was summed again", rates.ByPeer)
	}
	firstID := queryInt(t, network.db, "SELECT id FROM client WHERE last_address = ?", first.address)
	secondID := queryInt(t, network.db, "SELECT id FROM client WHERE last_address = ?", second.address)
	if peers := rates.ByClientPeer[firstID]; len(peers) != 2 || peers[shared].InBitsPerSecond != 100 ||
		peers[only].InBitsPerSecond != 30 || peers[shared].OutMeasured {
		t.Errorf("the first client's peers are %+v", peers)
	}
	if rate := rates.ByClientPeer[secondID][shared]; rate.InBitsPerSecond != 50 || rate.OutBitsPerSecond != 70 ||
		!rate.OutMeasured {
		t.Errorf("the one-peer client reads %+v, not 50 in and a measured 70 out", rate)
	}
	// The rates the existing figures carry are unchanged by the per-peer ones.
	if rate := rates.ByClient[firstID]; rate.InBitsPerSecond != 130 || rate.OutBitsPerSecond != 240 {
		t.Errorf("the first client's own rate is %+v", rate)
	}
	// Past the bound, no peer holds a current sample: a state, not a zero.
	later, err := network.db.ReadCurrentRates(ctx, network.now+601)
	if err != nil {
		t.Fatalf("reading the rates: %v", err)
	}
	if len(later.ByPeer) != 0 || len(later.ByClientPeer) != 0 {
		t.Errorf("past the bound the peers read %v", later.ByPeer)
	}
}
