package widgets

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/NaejEL/opnview/internal/live"
	"github.com/NaejEL/opnview/internal/store/samples"
	"github.com/NaejEL/opnview/internal/store/vocab"
)

// The per-peer current rate (row 24 of the mockup's coverage, docs/widget-api.md) is exposed by the
// core once, as host.rates.byPeer() and host.rates.clientPeers(id), so that any widget plugin serves
// it. The proof: a third party's widget plugin, written as files into a data directory and nothing
// else, serves both, and the built-in client_traffic_detail serves the client's.

// examplePeerRatesPlugin is a third party's widget plugin reading the per-peer rates.
var examplePeerRatesPlugin = map[string]string{
	"plugin.json": `{"api_version": 2, "name": "example_peer_rates", "version": "0.1.0",
		"display_name": "Example peer rates", "widgets": ["example_peer_rates"]}`,
	"widgets/example_peer_rates/manifest.json": `{"manifest_version": 1, "type": "example_peer_rates",
		"shape": "table", "providers": ["measurement_sample"], "primary": "measurement_sample",
		"parameters": [{"key": "client", "kind": "integer", "default": 0, "min": 0}],
		"script": "peer_rates.js"}`,
	"widgets/example_peer_rates/peer_rates.js": `
exports.widget = {
  serve: function (host) {
    var all = host.rates.byPeer();
    var mine = host.rates.clientPeers(host.params.integer("client"));
    var rows = Object.keys(all).sort().map(function (peer) {
      return { peer: peer, rate: all[peer], client_rate: mine.hasOwnProperty(peer) ? mine[peer] : null };
    });
    return { data: { rows: rows }, has_rows: rows.length > 0 };
  }
};
`,
}

// peerRateData is the third party's answer.
type peerRateData struct {
	Rows []struct {
		Peer       string         `json:"peer"`
		Rate       map[string]any `json:"rate"`
		ClientRate map[string]any `json:"client_rate"`
	} `json:"rows"`
}

func TestAThirdPartyWidgetServesThePerPeerRateFromFilesAlone(t *testing.T) {
	t.Parallel()
	f := newFixture(t, defaultSpec)
	client := f.clients[0]
	clientID := f.scalar(t, "SELECT id FROM client WHERE last_address = $1 AND identity_kind = 'mac' ORDER BY id LIMIT 1", client.address)
	at := f.now - 20
	peers := []string{f.peers[0], f.peers[1]}
	f.sampleAt(t, at, vocab.SubjectInterfaceEndpoint, endpointKey(client.iface.device, client.address),
		vocab.MeasureRateBitsIn, vocab.UnitBitPerSecond, 900)
	f.sampleAt(t, at, vocab.SubjectInterfaceEndpointPair,
		pairKey(client.iface.device, client.address, peers[0]),
		vocab.MeasureRateBitsIn, vocab.UnitBitPerSecond, 600)
	f.sampleAt(t, at, vocab.SubjectInterfaceEndpointPair,
		pairKey(client.iface.device, client.address, peers[1]),
		vocab.MeasureRateBitsIn, vocab.UnitBitPerSecond, 300)

	dataDir := t.TempDir()
	writePlugin(t, dataDir, "example_peer_rates", examplePeerRatesPlugin)
	service := pluginService(t, f, dataDir)
	interest := live.New(nil)
	service.SetLiveInterest(interest)
	response, err := service.Respond(context.Background(), "example_peer_rates",
		fmt.Sprintf(`{"client":%d}`, clientID))
	check(t, err)
	if interest.Renewals() == 0 {
		t.Error("an answer carrying a per-peer rate did not renew the live-interest lease")
	}
	encoded, err := json.Marshal(response.Data)
	check(t, err)
	var data peerRateData
	check(t, json.Unmarshal(encoded, &data))
	want := map[string]float64{peers[0]: 600, peers[1]: 300}
	if len(data.Rows) != len(want) {
		t.Fatalf("the third party's widget served %+v", data.Rows)
	}
	for _, row := range data.Rows {
		if row.Rate["in_bits_per_second"] != want[row.Peer] || row.Rate["state"] != string(samples.RateCurrent) ||
			row.Rate["measure"] != "sampled" || row.Rate["lower_bound"] != true {
			t.Errorf("%s reads %v", row.Peer, row.Rate)
		}
		if row.ClientRate == nil || row.ClientRate["in_bits_per_second"] != want[row.Peer] {
			t.Errorf("%s: the client's rate with it reads %v", row.Peer, row.ClientRate)
		}
	}

	// The built-in destination widget the mockup draws with a rate per destination serves it too.
	builtin := f.respond(t, "client_traffic_detail", map[string]any{"client": map[string]any{"kind": "client",
		"by": "mac", "value": "00:00:5e:00:53:00"}})
	encoded, err = json.Marshal(builtin.Data)
	check(t, err)
	var detail struct {
		CurrentRateByPeer []struct {
			Peer string         `json:"peer"`
			Rate map[string]any `json:"rate"`
		} `json:"current_rate_by_peer"`
	}
	check(t, json.Unmarshal(encoded, &detail))
	if len(detail.CurrentRateByPeer) != 2 {
		t.Fatalf("client_traffic_detail serves %+v", detail.CurrentRateByPeer)
	}
	for _, entry := range detail.CurrentRateByPeer {
		if entry.Rate["in_bits_per_second"] != want[entry.Peer] {
			t.Errorf("client_traffic_detail: %s reads %v", entry.Peer, entry.Rate)
		}
	}
}
