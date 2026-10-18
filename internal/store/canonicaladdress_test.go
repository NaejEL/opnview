package store_test

import (
	"context"
	"testing"
)

// TestAnAddressIsComparedAsAValue is PS19 rule 2's address comparison, as the dataset leg serves it: canonical_address reads an
// address the filter log wrote in any form as one canonical text, and a text that is not an address as
// NULL, never as an error that would refuse its flow.
func TestAnAddressIsComparedAsAValue(t *testing.T) {
	t.Parallel()
	database, _ := openTestStore(t)
	for text, want := range map[string]string{
		"2001:DB8:0:0:0:0:0:10": "2001:db8::10", "2001:db8::10": "2001:db8::10", "198.51.100.10": "198.51.100.10",
	} {
		var got string
		if err := database.DB().QueryRowContext(context.Background(), "SELECT canonical_address($1)", text).
			Scan(&got); err != nil || got != want {
			t.Errorf("canonical_address(%q) = %q (%v), want %q", text, got, err, want)
		}
	}
	var null *string
	if err := database.DB().QueryRowContext(context.Background(), "SELECT canonical_address('not an address')").
		Scan(&null); err != nil || null != nil {
		t.Errorf("a text that is not an address reads %v (%v), want NULL", null, err)
	}
}
