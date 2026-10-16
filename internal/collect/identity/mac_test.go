package identity

import (
	"testing"
)

// TestAMalformedHardwareAddressIsStoredAsAbsentRatherThanStoredWrong is the reason the MAC
// column is constrained to the seventeen-character form.
//
// client.mac_is_randomised tests the SECOND HEX DIGIT for the IEEE locally-administered
// bit, and that test is only total on a value of known shape. A MAC opnview cannot
// normalise has to be absent rather than wrong, because a wrong second digit would mark a
// real burned-in address as an unstable identity.
func TestAMalformedHardwareAddressIsStoredAsAbsentRatherThanStoredWrong(t *testing.T) {
	t.Parallel()
	for _, malformed := range []string{
		"", "not a mac", "0a:11:22:33:44", "0a-11-22-33-44-55", "0a:11:22:33:44:5g",
		"0A:11:22:33:44:55:66",
	} {
		if normalised, ok := normaliseMAC(malformed); ok {
			t.Errorf("%q was accepted as the MAC %q", malformed, normalised)
		}
	}
	// And an upper-case one of the right shape is lower-cased rather than refused, because
	// the digit test is on a lower-case value.
	normalised, ok := normaliseMAC("0A:11:22:33:44:55")
	if !ok || normalised != "0a:11:22:33:44:55" {
		t.Fatalf("an upper-case MAC normalised to %q (accepted: %v)", normalised, ok)
	}
}
