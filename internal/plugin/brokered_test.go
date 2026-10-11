package plugin

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/broker"
	"github.com/NaejEL/opnview/internal/broker/mirror"
	"github.com/NaejEL/opnview/internal/collect/provider"
	"github.com/NaejEL/opnview/internal/decode"
	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/store/storetest"
)

// brokeredWorld is a script run through a real broker and mirror, over a fake firewall that
// counts its requests.
type brokeredWorld struct {
	gate    *broker.Broker
	session *provider.Host
	calls   *atomic.Int64
}

func newBrokeredWorld(t *testing.T, body string) *brokeredWorld {
	t.Helper()
	calls := &atomic.Int64{}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	generated := func() string {
		raw := make([]byte, 12)
		_, _ = rand.Read(raw)
		return hex.EncodeToString(raw)
	}
	client := opnsense.NewClient(opnsense.Credentials{BaseURL: server.URL, APIKey: generated(),
		APISecret: generated()}, http.DefaultTransport, 5*time.Second)
	database := storetest.Open(t)
	gate := broker.New(client, mirror.New(database, func() int64 { return time.Now().Unix() }), broker.Options{})
	t.Cleanup(gate.Close)
	session := provider.NewHost(gate, func(context.Context) (decode.Normaliser, error) {
		return decode.Normaliser{Reference: time.Now()}, nil
	}, func() int64 { return time.Now().Unix() })
	return &brokeredWorld{gate: gate, session: session, calls: calls}
}

// brokeredScript reads the named fields of every row of the example endpoint, and says where the
// answer came from.
const brokeredScript = `exports.example = { pass: function (host, fields) {
	var response = host.call("rows", { max_age_seconds: 3600 });
	var read = [];
	var rows = response.value.rows;
	for (var i = 0; i < rows.length; i++) {
		var row = {};
		for (var j = 0; j < fields.length; j++) {
			row[fields[j]] = rows[i][fields[j]];
		}
		read.push(row);
	}
	return { served_from: response.served_from, rows: read };
} };
exports.helpers = { catalogue: function () { return null; } };`

// TestAFieldTheMirrorDoesNotHoldIsReadFromTheFirewall is A10's "No missing field because of the
// mirror", through the plugin host: a second plugin pass reading a field no earlier pass read is
// run again with the endpoint answered from the firewall, and gets the field; a later pass reading
// both is served from the mirror, with no call.
func TestAFieldTheMirrorDoesNotHoldIsReadFromTheFirewall(t *testing.T) {
	t.Parallel()
	world := newBrokeredWorld(t, `{"rows": [{"id": "1", "a": "x", "b": "y"}, {"id": "2", "a": "p", "b": "q"}]}`)
	endpoint := opnsense.Endpoint{Path: "/api/examplebrokered/rows/search", Method: http.MethodGet}
	if err := opnsense.AdmitDeclaredEndpoint(endpoint); err != nil {
		t.Fatal(err)
	}
	compiled, err := compile("example_brokered", "example.js", brokeredScript,
		map[string]opnsense.Endpoint{"rows": endpoint}, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	compiled.version = "1"
	pass := func(fields ...string) map[string]any {
		t.Helper()
		answer, _, failed := compiled.call(context.Background(), world.session, "example", "pass", fields)
		if failed != nil {
			t.Fatalf("the pass reading %v failed: %+v", fields, failed)
		}
		return answer.(map[string]any)
	}
	first := pass("a")
	second := pass("b")
	encoded, _ := json.Marshal(second["rows"])
	if second["served_from"] != broker.ServedFromFirewall || string(encoded) != `[{"b":"y"},{"b":"q"}]` ||
		world.calls.Load() != 2 {
		t.Errorf("a field the mirror did not hold was answered %s from %v after %d calls (first %v)", encoded,
			second["served_from"], world.calls.Load(), first)
	}
	third := pass("a", "b")
	encoded, _ = json.Marshal(third["rows"])
	if third["served_from"] != broker.ServedFromMirror || string(encoded) != `[{"a":"x","b":"y"},{"a":"p","b":"q"}]` ||
		world.calls.Load() != 2 {
		t.Errorf("fields every pass read were answered %s from %v after %d calls", encoded, third["served_from"],
			world.calls.Load())
	}
}

// TestHostCatalogueIsTheBrokersCatalogue is BR-AC23's host half: host.catalogue() answers what
// GET /api/firewall/catalogue serves.
func TestHostCatalogueIsTheBrokersCatalogue(t *testing.T) {
	t.Parallel()
	world := newBrokeredWorld(t, `{"rows": [{"id": "1"}]}`)
	endpoint := opnsense.Endpoint{Path: "/api/examplecatalogue/rows/search", Method: http.MethodGet}
	if err := opnsense.AdmitDeclaredEndpoint(endpoint); err != nil {
		t.Fatal(err)
	}
	compiled, err := compile("example_catalogue", "example.js", `exports.example = { pass: function (host) {
			host.call("rows", {}).value.rows.length;
			return { catalogue: host.catalogue() };
		} };`, map[string]opnsense.Endpoint{"rows": endpoint}, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	compiled.version = "1"
	// The first pass learns the endpoint and reports what it read once it returns; the second
	// reads the catalogue that holds it, which nothing changes after.
	if _, _, failed := compiled.call(context.Background(), world.session, "example", "pass"); failed != nil {
		t.Fatalf("the first pass failed: %+v", failed)
	}
	answer, _, failed := compiled.call(context.Background(), world.session, "example", "pass")
	if failed != nil {
		t.Fatalf("the pass failed: %+v", failed)
	}
	catalogue, err := world.gate.Catalogue(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	served, _ := json.Marshal(catalogue)
	var want any
	_ = json.Unmarshal(served, &want)
	got, _ := json.Marshal(answer.(map[string]any)["catalogue"])
	wanted, _ := json.Marshal(want)
	if string(got) != string(wanted) || len(catalogue.Endpoints) == 0 {
		t.Errorf("host.catalogue() answered %s, and the broker's catalogue is %s", got, wanted)
	}
}
