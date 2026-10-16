package store_test

import (
	"fmt"
	"os"
	"testing"

	"github.com/NaejEL/opnview/internal/testorder"
)

// TestMain starts this package's longest tests before every other (internal/testorder says why).
func TestMain(m *testing.M) {
	if err := testorder.StartFirst(m, longestTests...); err != nil {
		fmt.Fprintln(os.Stderr, "store_test:", err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

// longestTests are this package's longest tests, longest first. Each is one database's fixture
// followed by an equivalence check that cannot be split without building the fixture again.
// Measured on 16 October 2026 on 16 CPUs with go test -json, the package alone: 17.6, 10.9, 7.2,
// 7.0 and 5.2 s, the first three starting 2.9, 2.6 and 6.8 s into a 20.5 s run in source order.
var longestTests = []string{
	"TestEverySlotEqualsTheFlowsAcrossTheCalendarAndItsCorrections",
	"TestTheCurrentSlotsEqualTheSumsOverFlowWhateverTheRetention",
	"TestTheRollingWindowEqualsADirectComputationInsideTheHorizon",
	"TestEveryFlowLandsInExactlyOneNodePerLevelOfTheTree",
	"TestASeriesSumsToItsWindowAndMarksItsGaps",
}
