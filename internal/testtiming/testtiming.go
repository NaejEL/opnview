package testtiming

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"time"
)

// Variable names the directory the steps are written under. Unset, which is the default
// everywhere, nothing is recorded and every call returns at once.
const Variable = "OPNVIEW_TEST_TIMING_DIR"

// The steps a test's database goes through.
const (
	// StepClone is the CREATE DATABASE ... TEMPLATE of a test's database.
	StepClone = "clone"
	// StepSchema is Open's apply of schema.sql, or its check that the database already holds it.
	StepSchema = "schema"
	// StepRole is Open's creation and grants of the database's read role.
	StepRole = "role"
	// StepWait is Open's wait for the database to answer.
	StepWait = "wait"
	// StepRetry is one failed attempt of that wait, each followed by a pause.
	StepRetry = "retry"
	// StepDrop is the drop of a test's database and of its read role.
	StepDrop = "drop"
	// StepLifetime is a test database's life from its clone to its drop: the test's body, its opens
	// included.
	StepLifetime = "lifetime"
)

var (
	mutex sync.Mutex
	// files are this process's open files, by the directory they were opened in.
	files = map[string]*os.File{}
)

// Enabled reports whether the steps are recorded.
func Enabled() bool { return os.Getenv(Variable) != "" }

// Record records one step for the package of the test running it.
func Record(step string, took time.Duration) {
	if !Enabled() {
		return
	}
	RecordFor(Package(), step, took)
}

// RecordFor records one step for a package named beforehand: a cleanup runs where no test function
// is on the stack.
func RecordFor(pkg, step string, took time.Duration) {
	directory := os.Getenv(Variable)
	if directory == "" {
		return
	}
	mutex.Lock()
	defer mutex.Unlock()
	file, opened := files[directory]
	if !opened {
		// A failure leaves nothing recorded, which the breakdown shows as a package with no step.
		file, _ = os.OpenFile(filepath.Join(directory, fmt.Sprintf("%d.tsv", os.Getpid())),
			os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		files[directory] = file
	}
	if file == nil {
		return
	}
	_, _ = fmt.Fprintf(file, "%s\t%s\t%d\n", pkg, step, took.Nanoseconds())
}

// Package is the import path of the package whose test binary is running: a test binary is built
// as <import path>.test, its external test package with it. Outside a test binary it is the
// program's own path.
func Package() string {
	packageOnce.Do(func() {
		packagePath = "unknown"
		if info, ok := debug.ReadBuildInfo(); ok && info.Path != "" {
			packagePath = strings.TrimSuffix(info.Path, ".test")
		}
	})
	return packagePath
}

var (
	packageOnce sync.Once
	packagePath string
)

// Recorder hands Record, RecordFor and Package to a package that must not link this one: the store
// records the steps of its opens and of its test databases through it (internal/store, timing.go),
// set by the test binaries (internal/store/storetest).
type Recorder struct{}

// Record is Record.
func (Recorder) Record(step string, took time.Duration) { Record(step, took) }

// RecordFor is RecordFor.
func (Recorder) RecordFor(pkg, step string, took time.Duration) { RecordFor(pkg, step, took) }

// Package is Package.
func (Recorder) Package() string { return Package() }
