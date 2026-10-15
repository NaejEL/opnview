package plugin

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/plugin/runtime"
)

// TestEveryHostAnswerIsHeardAndSearchedForCredentials is BR-AC16's harness: under `opnview plugin
// test` every host function's answer -- a firewall answer as the broker served it, and every other
// function's result -- reaches the search, an accessor is not invoked on the script's behalf, and
// a credential or the firewall's address in any of them is a failure of the test.
func TestEveryHostAnswerIsHeardAndSearchedForCredentials(t *testing.T) {
	t.Parallel()
	compiled, err := runtime.Compile("example", "example.js", `exports.example = { pass: function (host) {
			var answer = host.call("rows", {});
			var state = host.serviceState("rows");
			return { rows: answer.value.rows.length, running: state.running, now: host.now() };
		} };`,
		map[string]opnsense.Endpoint{"rows": {Path: "/api/example/rows/search", Method: "GET"}}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	var mutex sync.Mutex
	heard := map[string][]string{}
	compiled.Observe = func(function string, answer any) {
		encoded, _ := json.Marshal(answer)
		mutex.Lock()
		heard[function] = append(heard[function], string(encoded))
		mutex.Unlock()
	}
	body := []byte(`{"rows": [{"id": "carried-in-the-body"}]}`)
	answer, _, failed := compiled.Call(context.Background(), stubSession{body: body, now: 1750000000}, "example", "pass")
	if failed != nil {
		t.Fatalf("the pass failed: %+v", failed)
	}
	if encoded, _ := json.Marshal(answer); !strings.Contains(string(encoded), `"rows":1`) {
		t.Errorf("observing changed what the script read: %s", encoded)
	}
	for _, function := range []string{"call", "serviceState", "now"} {
		if len(heard[function]) == 0 {
			t.Errorf("host.%s's answer was not heard", function)
		}
	}
	if !strings.Contains(strings.Join(heard["call"], " "), "carried-in-the-body") {
		t.Errorf("the firewall answer was not heard as served: %v", heard["call"])
	}

	server := httptest.NewServer(nil)
	defer server.Close()
	world := &testWorld{server: server, key: randomCredential(), secret: randomCredential()}
	world.heard("call", map[string]any{"body": "nothing to see"})
	if leaks := world.credentialLeaks(); len(leaks) != 0 {
		t.Errorf("a clean answer is reported: %v", leaks)
	}
	for _, carried := range []string{world.key, world.secret, server.URL, "Authorization: x"} {
		world.hostAnswers = nil
		world.heard("call", map[string]any{"detail": "it says " + carried})
		if leaks := world.credentialLeaks(); len(leaks) == 0 {
			t.Errorf("an answer carrying %q is not reported", carried)
		}
	}
}
