// Package datadirguard holds the tests of ci/data-dir-guard.sh, the guard
// ci/pre-deployment.sh and sql/schema-checks.sh run before any step (AC23 of
// specs/SPEC-step-5b-widget-api.md).
//
// They are Go tests rather than a step of the pre-deployment command so the everyday
// suite runs them: each refusal costs a few milliseconds, because a refused value
// stops both scripts before their first step, and the accepted value is checked
// against the guard function alone rather than by running the whole command.
package datadirguard

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// repositoryRoot is two directories above this package.
func repositoryRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolving the repository root: %v", err)
	}
	return root
}

// badDataDirs are AC23's four values: two relative ones, and two that show Git-Bash
// path conversion -- what `/data` becomes when Git Bash rewrites it, and a Windows
// path with a drive letter and backslashes.
var badDataDirs = []string{"data", "./data", "C:/Program Files/Git/data", `C:\data`}

// run runs a script with OPNVIEW_DATA_DIR set and returns its exit status and output.
func run(t *testing.T, script, dataDir string) (int, string) {
	t.Helper()
	command := exec.Command("bash", script)
	command.Dir = repositoryRoot(t)
	command.Env = append(command.Environ(), "OPNVIEW_DATA_DIR="+dataDir)
	output, err := command.CombinedOutput()
	if err == nil {
		return 0, string(output)
	}
	exitError, isExit := err.(*exec.ExitError)
	if !isExit {
		t.Fatalf("running %s: %v", script, err)
	}
	return exitError.ExitCode(), string(output)
}

// TestPreDeploymentRefusesABadDataDirBeforeGoVet is AC23 for ci/pre-deployment.sh:
// each bad value exits non-zero, names the variable, and runs no step -- go vet
// above all.
func TestPreDeploymentRefusesABadDataDirBeforeGoVet(t *testing.T) {
	t.Parallel()
	for _, value := range badDataDirs {
		status, output := run(t, "ci/pre-deployment.sh", value)
		if status == 0 {
			t.Errorf("OPNVIEW_DATA_DIR=%q: pre-deployment exited 0", value)
		}
		if !strings.Contains(output, "OPNVIEW_DATA_DIR") {
			t.Errorf("OPNVIEW_DATA_DIR=%q: the refusal does not name the variable:\n%s", value, output)
		}
		if strings.Contains(output, "go vet") {
			t.Errorf("OPNVIEW_DATA_DIR=%q: go vet ran before the refusal:\n%s", value, output)
		}
	}
}

// TestSchemaChecksRefusesABadDataDirBeforeAnyCheck is the same guard on
// sql/schema-checks.sh (decision 16): refused before any database is created.
func TestSchemaChecksRefusesABadDataDirBeforeAnyCheck(t *testing.T) {
	t.Parallel()
	for _, value := range badDataDirs {
		status, output := run(t, "sql/schema-checks.sh", value)
		if status == 0 {
			t.Errorf("OPNVIEW_DATA_DIR=%q: schema-checks exited 0", value)
		}
		if !strings.Contains(output, "OPNVIEW_DATA_DIR") || !strings.Contains(output, "before any check") {
			t.Errorf("OPNVIEW_DATA_DIR=%q: the refusal does not name the variable, or came late:\n%s",
				value, output)
		}
	}
}

// TestTheGuardAcceptsAContainerPath is the other half of AC23: `/data`, the default,
// and another absolute container path pass the guard.
func TestTheGuardAcceptsAContainerPath(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"/data", "/tmp/opnview-data"} {
		command := exec.Command("bash", "-c", `. ci/data-dir-guard.sh && check_data_dir "$1"`,
			"guard", value)
		command.Dir = repositoryRoot(t)
		if output, err := command.CombinedOutput(); err != nil {
			t.Errorf("the guard refused %q: %v\n%s", value, err, output)
		}
	}
}
