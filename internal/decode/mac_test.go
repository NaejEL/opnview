package decode

import "testing"

// TestAMalformedHardwareAddressIsStoredAsAbsentRatherThanStoredWrong: a value that is not six
// colon-separated octets is refused, and an upper-case one of the right shape is lower-cased.
func TestAMalformedHardwareAddressIsStoredAsAbsentRatherThanStoredWrong(t *testing.T) {
	t.Parallel()
	for _, malformed := range []string{
		"", "not a mac", "0a:11:22:33:44", "0a-11-22-33-44-55", "0a:11:22:33:44:5g",
		"0A:11:22:33:44:55:66",
	} {
		if normalised, ok := NormaliseMAC(malformed); ok {
			t.Errorf("%q was accepted as the MAC %q", malformed, normalised)
		}
	}
	// And an upper-case one of the right shape is lower-cased rather than refused, because
	// the digit test is on a lower-case value.
	normalised, ok := NormaliseMAC("0A:11:22:33:44:55")
	if !ok || normalised != "0a:11:22:33:44:55" {
		t.Fatalf("an upper-case MAC normalised to %q (accepted: %v)", normalised, ok)
	}
}
