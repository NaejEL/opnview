package testtiming

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestThePackageIsTheRunningTestsOwn: a step recorded from a test is counted to the package the test
// binary was built for.
func TestThePackageIsTheRunningTestsOwn(t *testing.T) {
	if got := Package(); got != "github.com/NaejEL/opnview/internal/testtiming" {
		t.Errorf("the running test's package is %q", got)
	}
}

// TestAStepIsRecordedOnlyWhileTheSwitchNamesADirectory: unset, nothing is written; set, one line per
// step lands in the directory named, under this process's file.
func TestAStepIsRecordedOnlyWhileTheSwitchNamesADirectory(t *testing.T) {
	t.Setenv(Variable, "")
	Record(StepClone, time.Second)
	if Enabled() {
		t.Fatal("an empty switch is on")
	}
	directory := t.TempDir()
	t.Setenv(Variable, directory)
	Record(StepRetry, time.Second)
	RecordFor("example/package", StepDrop, 2*time.Millisecond)
	raw, err := os.ReadFile(filepath.Join(directory, fmt.Sprintf("%d.tsv", os.Getpid())))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	want := []string{"github.com/NaejEL/opnview/internal/testtiming\tretry\t1000000000",
		"example/package\tdrop\t2000000"}
	if strings.Join(lines, "|") != strings.Join(want, "|") {
		t.Errorf("the directory holds %q, not %q", lines, want)
	}
}
