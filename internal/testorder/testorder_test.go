package testorder

import (
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// started records the tests of this package in the order they start.
var (
	startedMu sync.Mutex
	started   []string
)

func record(t *testing.T) {
	startedMu.Lock()
	defer startedMu.Unlock()
	started = append(started, t.Name())
}

// TestMain applies the mechanism to this package itself: the test declared last is named first.
func TestMain(m *testing.M) {
	if err := StartFirst(m, "TestTheNamedTestsStartFirstThroughTestMain"); err != nil {
		os.Stderr.WriteString(err.Error() + "\n")
		os.Exit(1)
	}
	os.Exit(m.Run())
}

func TestTheOtherTestsKeepTheirSourceOrder(t *testing.T) {
	record(t)
	startedMu.Lock()
	defer startedMu.Unlock()
	// Under a -run selecting this test alone, it is the only one started.
	want := []string{"TestTheNamedTestsStartFirstThroughTestMain", "TestTheOtherTestsKeepTheirSourceOrder"}
	if len(started) == 1 {
		want = want[1:]
	}
	if !reflect.DeepEqual(started, want) {
		t.Errorf("the tests started in the order %v, not %v", started, want)
	}
}

func TestTheNamedTestsStartFirstThroughTestMain(t *testing.T) {
	record(t)
	startedMu.Lock()
	defer startedMu.Unlock()
	if len(started) != 1 || started[0] != t.Name() {
		t.Errorf("the test named in TestMain started after %v", started)
	}
}

func names(tests []testing.InternalTest) []string {
	out := make([]string, len(tests))
	for index, test := range tests {
		out[index] = test.Name
	}
	return out
}

func TestTheNamedTestsComeFirstInTheOrderGivenAndTheOthersKeepTheirs(t *testing.T) {
	tests := []testing.InternalTest{{Name: "A"}, {Name: "B"}, {Name: "C"}, {Name: "D"}, {Name: "E"}}
	ordered, err := order(tests, []string{"D", "B"})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := names(ordered), []string{"D", "B", "A", "C", "E"}; !reflect.DeepEqual(got, want) {
		t.Errorf("ordered %v, want %v", got, want)
	}
}

func TestANameThatIsNotATestIsRefused(t *testing.T) {
	tests := []testing.InternalTest{{Name: "A"}, {Name: "B"}}
	_, err := order(tests, []string{"B", "Missing"})
	if err == nil || !strings.Contains(err.Error(), "Missing") {
		t.Errorf("an unknown name gave %v, not an error naming it", err)
	}
}

func TestANameGivenTwiceIsRefused(t *testing.T) {
	tests := []testing.InternalTest{{Name: "A"}, {Name: "B"}}
	if _, err := order(tests, []string{"A", "A"}); err == nil {
		t.Error("a name given twice was accepted")
	}
}

func TestNoTestingMIsRefused(t *testing.T) {
	if err := StartFirst(nil, "A"); err == nil {
		t.Error("a nil testing.M was accepted")
	}
}
