package scripthost

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/collect/provider"
	"github.com/NaejEL/opnview/internal/decode"
	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/store/vocab"
)

// stubSession is a session that answers every call with one body, for the tests of the runtime
// itself; the plugins' own tests run against the fake firewall.
type stubSession struct {
	body []byte
	now  int64
}

func (s stubSession) Call(_ context.Context, endpoint opnsense.Endpoint, _ opnsense.RequestOptions) (
	opnsense.Response, error) {
	return opnsense.Response{Endpoint: endpoint, Outcome: opnsense.OutcomeOK, StatusCode: 200, Body: s.body}, nil
}

func (s stubSession) ServiceState(context.Context, opnsense.Endpoint) (bool, vocab.AvailabilityState,
	vocab.AvailabilityOutcome, string, error) {
	return true, vocab.StateReachable, vocab.OutcomeHealthy, "", nil
}

func (s stubSession) ReadObject(context.Context, opnsense.Endpoint) (decode.Object, bool, error) {
	var object decode.Object
	err := json.Unmarshal(s.body, &object)
	return object, err == nil, nil
}

func (s stubSession) ReadCollection(context.Context, opnsense.Endpoint, ...string) ([]decode.Object, bool, error) {
	rows, err := decode.Rows(s.body)
	return rows, err == nil, nil
}

func (s stubSession) Stream(context.Context, opnsense.Endpoint, opnsense.StreamBound) (opnsense.StreamResponse,
	error) {
	return opnsense.StreamResponse{}, nil
}

func (s stubSession) Normaliser(context.Context) (decode.Normaliser, error) {
	return decode.Normaliser{Reference: time.Unix(s.now, 0)}, nil
}

func (s stubSession) Now() int64 { return s.now }

var _ provider.Session = stubSession{}

// runScript compiles a script and calls one of its functions against a stub session.
func runScript(t *testing.T, source, namespace, function string, body []byte, arguments ...any) (any, *Failure) {
	t.Helper()
	compiled, err := Compile("example", "example.js", source,
		map[string]opnsense.Endpoint{"rows": {Path: "/api/example/rows/search", Method: "GET"}}, time.Second)
	if err != nil {
		t.Fatalf("compiling: %v", err)
	}
	answer, _, failed := compiled.Call(context.Background(), stubSession{body: body, now: 1750000000}, namespace,
		function, arguments...)
	return answer, failed
}

// TestTheFirewallsDecodedValuesReadAsJavaScriptArraysAndObjects pins the interpreter behaviour the
// built-in plugins rely on: a body the host decoded is an array a script iterates and an object
// whose fields it reads, and handing one of its rows back to a field reader reads it as the core
// would.
func TestTheFirewallsDecodedValuesReadAsJavaScriptArraysAndObjects(t *testing.T) {
	t.Parallel()
	source := `exports.example = { read: function (host) {
		var response = host.call("rows");
		var body = response.json();
		var seen = [];
		body.rows.forEach(function (row) { seen.push(opnview.decode.string(row, "name") + ":" +
			opnview.decode.int(row, "count")); });
		return { result: { probe: "rows" }, isArray: Array.isArray(body.rows), keys: Object.keys(body).length,
			seen: seen, nested: opnview.decode.nestedFlag(body, "settings", "enabled") };
	} };`
	answer, failed := runScript(t, source, "example", "read",
		[]byte(`{"rows":[{"name":"a","count":"3"},{"name":"b","count":4}],"settings":{"enabled":"1"}}`))
	if failed != nil {
		t.Fatalf("the call failed: %v", failed)
	}
	object := answer.(map[string]any)
	if object["isArray"] != true {
		t.Errorf("a decoded array is not a JavaScript array: %v", object["isArray"])
	}
	if object["keys"] != int64(2) {
		t.Errorf("Object.keys of a decoded object gave %v", object["keys"])
	}
	seen, _ := object["seen"].([]any)
	if len(seen) != 2 || seen[0] != "a:3" || seen[1] != "b:4" {
		t.Errorf("the rows read as %v", seen)
	}
	if object["nested"] != true {
		t.Errorf("a nested flag read as %v", object["nested"])
	}
}

// TestAScriptThatNeverReturnsIsStoppedAtItsBound is the timeout, at the runtime.
func TestAScriptThatNeverReturnsIsStoppedAtItsBound(t *testing.T) {
	t.Parallel()
	compiled, err := Compile("example", "example.js", `exports.example = { read: function () { for (;;) {} } };`,
		nil, 50*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	_, _, failed := compiled.Call(context.Background(), stubSession{}, "example", "read")
	if failed == nil || failed.Code != FailureTimeout {
		t.Fatalf("an endless script ended with %v", failed)
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Errorf("the bound took %v to act", elapsed)
	}
}

// TestAThrownErrorAndAMissingFunctionAreScriptFailures is the other half of P4 at the runtime.
func TestAThrownErrorAndAMissingFunctionAreScriptFailures(t *testing.T) {
	t.Parallel()
	_, failed := runScript(t, `exports.example = { read: function () { throw new Error("broken"); } };`,
		"example", "read", nil)
	if failed == nil || failed.Code != FailureScript {
		t.Errorf("a thrown error ended with %v", failed)
	}
	_, failed = runScript(t, `exports.example = {};`, "example", "read", nil)
	if failed == nil || failed.Code != FailureScript {
		t.Errorf("a missing function ended with %v", failed)
	}
	_, failed = runScript(t, `exports.example = { read: function (host) { host.call("undeclared"); } };`,
		"example", "read", nil)
	if failed == nil || failed.Code != FailureScript {
		t.Errorf("a call to an undeclared endpoint ended with %v", failed)
	}
}

// TestTheScriptHostGrantsOnlyThePort: a script is handed the firewall endpoints its manifest declares, the normaliser and the clock, and nothing
// that reaches the store, discovery, availability or another host. The members are pinned here,
// so a capability added to the host is a decision a test records.
func TestTheScriptHostGrantsOnlyThePort(t *testing.T) {
	t.Parallel()
	answer, failed := runScript(t, `exports.example = { read: function (host) {
		var globals = ["require", "process", "fs", "fetch", "XMLHttpRequest", "setTimeout"].filter(function (name) {
			return typeof this[name] !== "undefined"; }, this);
		return { result: { probe: "rows" }, members: Object.keys(host).sort(), globals: globals };
	} };`, "example", "read", nil)
	if failed != nil {
		t.Fatalf("the call failed: %v", failed)
	}
	object := answer.(map[string]any)
	members, _ := object["members"].([]any)
	// catalogue and events are the broker amendment's (A17): the learned API catalogue, and a
	// stream's events after a cursor; firewall is A19's read-only OPNsense version.
	want := []string{"call", "catalogue", "events", "firewall", "log", "normaliser", "now", "pagination",
		"readCollection", "readObject", "serviceState", "stream"}
	if len(members) != len(want) {
		t.Fatalf("the host grants %v, not %v", members, want)
	}
	for index, member := range want {
		if members[index] != member {
			t.Errorf("the host grants %v, not %v", members, want)
			break
		}
	}
	if globals, _ := object["globals"].([]any); len(globals) != 0 {
		t.Errorf("a script sees the globals %v", globals)
	}
}

// TestAPluginWithNoRangeIsUnaffectedByTheVersion is BR24's last part: a plugin declaring no range
// runs the same whatever the firewall's version, known or not -- every built-in plugin is one, and
// their own tests run with no version at all.
func TestAPluginWithNoRangeIsUnaffectedByTheVersion(t *testing.T) {
	t.Parallel()
	compiled, err := Compile("example", "example.js", `exports.example = { pass: function (host) {
			return { version: host.firewall.version, rows: host.call("rows", {}).value.rows.length };
		} };`, map[string]opnsense.Endpoint{"rows": {Path: "/api/example/rows/search", Method: "GET"}}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	answer, _, failed := compiled.Call(context.Background(), stubSession{body: []byte(`{"rows": [{"a": 1}]}`),
		now: 1750000000}, "example", "pass")
	if failed != nil {
		t.Fatalf("a plugin with no range failed on an unknown version: %+v", failed)
	}
	if got := answer.(map[string]any); got["rows"] != int64(1) && got["rows"] != float64(1) || got["version"] != nil {
		t.Errorf("the pass answered %v", got)
	}
}
