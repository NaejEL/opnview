package collect

import (
	"testing"
	"time"
)

// TestDayStartTruncatesToTheUTCDay pins the boundary a level-3 client identity is keyed on.
//
// It moved here with DayStart when the decoding layer left this package, and it is worth saying
// what it guards: the level-3 key is composed from the interface, the address and this
// truncated instant, so a day boundary that followed the host's zone would mint a different
// client identity depending on which machine opnview happened to run on.
func TestDayStartTruncatesToTheUTCDay(t *testing.T) {
	t.Parallel()
	instant := time.Date(2026, time.September, 26, 23, 51, 6, 0, time.UTC).Unix()
	want := time.Date(2026, time.September, 26, 0, 0, 0, 0, time.UTC).Unix()
	if got := DayStart(instant); got != want {
		t.Fatalf("the day start is %d, want %d", got, want)
	}
}
