package frontjs_test

import (
	"os"
	"runtime/debug"
	"testing"
)

// TestMain runs the package with the collector started at five times the live heap rather than
// twice it. Every test here runs the front's own files in an interpreter, which allocates a Go
// object for each value a script makes, so the package's time was a third collection: measured on
// 17 October 2026, the default setting cost it 33 s of CPU and this one 22 s, for a heap that stays
// in the tens of megabytes. Nothing a test asserts depends on when memory is collected.
func TestMain(m *testing.M) {
	debug.SetGCPercent(400)
	os.Exit(m.Run())
}
