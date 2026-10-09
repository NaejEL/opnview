// Package budgetprobe holds the one test the test budget's own check drives.
//
// ci/checks.sh fails when its go test wall time exceeds the test budget, and
// ci/budget-self-test.sh proves that it does: it runs checks.sh with
// OPNVIEW_BUDGET_PROBE_SECONDS set past the budget, and this test sleeps that
// long, so the measured time is over the limit by construction. The switch is
// read here and nowhere else, and the self-test is the only thing that sets it.
package budgetprobe

import (
	"os"
	"strconv"
	"testing"
	"time"
)

// probeVariable is the environment switch the budget's own check sets.
const probeVariable = "OPNVIEW_BUDGET_PROBE_SECONDS"

// TestTheBudgetProbeSleepsOnlyWhenTheBudgetCheckAsks is the probe. Without the
// switch it sleeps for no time at all, which is what keeps the everyday suite
// unaffected; with it, the value must be a positive whole number of seconds, so
// a mistyped switch fails instead of quietly proving nothing.
func TestTheBudgetProbeSleepsOnlyWhenTheBudgetCheckAsks(t *testing.T) {
	value, set := os.LookupEnv(probeVariable)
	if !set {
		return
	}
	seconds, err := strconv.Atoi(value)
	if err != nil || seconds <= 0 {
		t.Fatalf("%s is %q, not a positive whole number of seconds", probeVariable, value)
	}
	started := time.Now()
	time.Sleep(time.Duration(seconds) * time.Second)
	if slept := time.Since(started); slept < time.Duration(seconds)*time.Second {
		t.Fatalf("the probe slept %v, less than the %d s it was asked for", slept, seconds)
	}
	t.Logf("the budget probe slept %d s", seconds)
}
