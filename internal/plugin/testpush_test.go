package plugin

import (
	"testing"

	"github.com/NaejEL/opnview/internal/specialpurpose"
)

// TestTheTestWorldSenderComesFromItsFixtureAndIsADocumentationAddress: the test world's firewall
// address is read from testdata/test_world.json, never written in the code, and it is an address
// reserved for documentation, so a plugin test can never name anybody's firewall by default.
func TestTheTestWorldSenderComesFromItsFixtureAndIsADocumentationAddress(t *testing.T) {
	sender, err := defaultTestSender()
	if err != nil {
		t.Fatal(err)
	}
	entry, registered := specialpurpose.Lookup(sender)
	if !registered || entry.Reference != "RFC 5737" {
		t.Errorf("the test world's sender %s is not an RFC 5737 documentation address: %+v", sender, entry)
	}
}
