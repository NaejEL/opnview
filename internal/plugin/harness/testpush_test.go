package harness

import (
	"net/netip"
	"testing"
)

// TestTheTestWorldSenderComesFromItsFixtureAndIsADocumentationAddress: the test world's firewall
// address is read from testdata/test_world.json, never written in the code, and it is an address
// reserved for documentation (RFC 5737's three blocks), so a plugin test can never name anybody's
// firewall by default.
func TestTheTestWorldSenderComesFromItsFixtureAndIsADocumentationAddress(t *testing.T) {
	sender, err := defaultTestSender()
	if err != nil {
		t.Fatal(err)
	}
	documentation := false
	for _, block := range []string{"192.0.2.0/24", "198.51.100.0/24", "203.0.113.0/24"} {
		if netip.MustParsePrefix(block).Contains(sender.Unmap()) {
			documentation = true
		}
	}
	if !documentation {
		t.Errorf("the test world's sender %s is not an RFC 5737 documentation address", sender)
	}
}
