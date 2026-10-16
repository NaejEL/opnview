package store_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/NaejEL/opnview/internal/store/samples"
	"github.com/NaejEL/opnview/internal/store/storetest"
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
	network := storetest.NewNetwork(t, 2, 4)
	ctx := context.Background()
	if err := network.DB.SetSetting(ctx, samples.MeasurementIntervalKey, fmt.Sprint(300), network.Now); err != nil {
		t.Fatalf("writing the interval: %v", err)
	}
	at := network.Now - 100
	pairIn := func(device, local, peer string, in float64) {
		insertReading(t, network.DB, vocab.SubjectInterfaceEndpointPair, vocab.InterfaceEndpointPairKey(device, local, peer),
			vocab.MeasureRateBitsIn, vocab.UnitBitPerSecond, in, at)
	}
	pairOut := func(device, local, peer string, out float64) {
		insertReading(t, network.DB, vocab.SubjectInterfaceEndpointPair, vocab.InterfaceEndpointPairKey(device, local, peer),
			vocab.MeasureRateBitsOut, vocab.UnitBitPerSecond, out, at)
	}
	endpoint := func(device, local string, in, out float64) {
		key := vocab.InterfaceEndpointKey(device, local)
		insertReading(t, network.DB, vocab.SubjectInterfaceEndpoint, key, vocab.MeasureRateBitsIn, vocab.UnitBitPerSecond, in, at)
		insertReading(t, network.DB, vocab.SubjectInterfaceEndpoint, key, vocab.MeasureRateBitsOut, vocab.UnitBitPerSecond, out, at)
	}
	shared := storetest.Outside(1, network.Clients[0].V6)
	only := storetest.Outside(2, network.Clients[0].V6)

	// The first client talks to two peers: no per-pair outbound figure exists.
	first := network.Clients[0]
	endpoint(first.Device, first.Address, 130, 240)
	pairIn(first.Device, first.Address, shared, 100)
	pairIn(first.Device, first.Address, only, 30)
	// The second leased client talks to the shared peer alone: its total is the pair's outbound figure.
	second := network.Clients[3]
	endpoint(second.Device, second.Address, 50, 70)
	pairIn(second.Device, second.Address, shared, 50)
	pairOut(second.Device, second.Address, shared, 70)
	// The upstream reading of the same conversations, after NAT.
	endpoint(network.Upstream.Device, network.Upstream.Address, 5000, 5000)
	pairIn(network.Upstream.Device, network.Upstream.Address, shared, 5000)

	rates, err := network.DB.ReadCurrentRates(ctx, network.Now)
	if err != nil {
		t.Fatalf("reading the rates: %v", err)
	}
	if rate := rates.ByPeer[shared]; rate.InBitsPerSecond != 150 || rate.OutMeasured || rate.SampledAt != at {
		t.Errorf("the shared peer reads %+v, not 150 in from the two inside clients and an unmeasured out", rate)
	}
	if rate := rates.ByPeer[only]; rate.InBitsPerSecond != 30 || rate.OutMeasured {
		t.Errorf("the other peer reads %+v", rate)
	}
	if _, present := rates.ByPeer[network.Upstream.Address]; present || len(rates.ByPeer) != 2 {
		t.Errorf("the peers are %v; the upstream reading was summed again", rates.ByPeer)
	}
	firstID := storetest.QueryInt(t, network.DB, "SELECT id FROM client WHERE last_address = ?", first.Address)
	secondID := storetest.QueryInt(t, network.DB, "SELECT id FROM client WHERE last_address = ?", second.Address)
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
	later, err := network.DB.ReadCurrentRates(ctx, network.Now+601)
	if err != nil {
		t.Fatalf("reading the rates: %v", err)
	}
	if len(later.ByPeer) != 0 || len(later.ByClientPeer) != 0 {
		t.Errorf("past the bound the peers read %v", later.ByPeer)
	}
}
