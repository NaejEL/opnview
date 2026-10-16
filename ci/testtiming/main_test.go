package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestTheBreakdownSumsEachPackagesSteps: the lines of every process's file are summed per package
// and step, the body is each database's life less its opens, and the slowest package comes first.
func TestTheBreakdownSumsEachPackagesSteps(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(directory, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("1.tsv", "example/slow\tclone\t1000000000\nexample/slow\tschema\t2000000000\n"+
		"example/slow\tlifetime\t10000000000\nexample/slow\tretry\t1000000000\n")
	write("2.tsv", "example/slow\tclone\t1000000000\nexample/slow\trole\t1000000000\n"+
		"example/fast\tclone\t500000000\nexample/fast\tlifetime\t1000000000\nnot a line\n")
	byPackage, err := read(directory)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(render(byPackage)), "\n")
	if len(lines) != 4 {
		t.Fatalf("the table is\n%s", strings.Join(lines, "\n"))
	}
	slow := strings.Fields(lines[1])
	if slow[0] != "example/slow" || slow[1] != "2" || slow[2] != "2.0" || slow[3] != "2.0" || slow[4] != "1.0" ||
		slow[6] != "1" || slow[8] != "7.0" {
		t.Errorf("the slow package reads %v", slow)
	}
	if fast := strings.Fields(lines[2]); fast[0] != "example/fast" || fast[8] != "1.0" {
		t.Errorf("the fast package reads %v", fast)
	}
	if total := strings.Fields(lines[3]); total[0] != "total" || total[1] != "3" || total[2] != "2.5" {
		t.Errorf("the total reads %v", total)
	}
}
