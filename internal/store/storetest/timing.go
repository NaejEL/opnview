package storetest

import (
	"github.com/NaejEL/opnview/internal/store"
	"github.com/NaejEL/opnview/internal/testtiming"
)

// Every test binary that opens a store through this package records the steps of its opens and test
// databases for the suite's breakdown, which does nothing unless the CI-only switch is on.
func init() { store.Timing = testtiming.Recorder{} }
