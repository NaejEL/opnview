package harness

import (
	"net/http/httptest"
	"testing"
)

// TestEveryHostAnswerIsHeardAndSearchedForCredentials is BR-AC16's harness: under `opnview plugin
// test` every host function's answer reaches the search, and a credential or the firewall's address
// in any of them is a failure of the test. That every answer is heard, and that an accessor is not
// invoked on the script's behalf, is the runtime's half (internal/plugin/scripthost,
// TestEveryHostAnswerIsHeard).
func TestEveryHostAnswerIsHeardAndSearchedForCredentials(t *testing.T) {
	t.Parallel()
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
