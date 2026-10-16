// Command testtiming prints where the everyday suite's time went, per package: the sums of what
// internal/testtiming recorded in the directory named, one line per package that created a test
// database, the slowest first.
//
//	testtiming <directory>
//
// ci/checks.sh runs it after go test when its CI-only switch OPNVIEW_TEST_BREAKDOWN is on. The
// columns are seconds summed over the package's tests, which run in parallel, so a column can exceed
// the package's wall time:
//
//   - clone: CREATE DATABASE ... TEMPLATE of each test's database;
//   - schema: Open's apply of schema.sql, or its check that the database records it already;
//   - role: Open's creation and grants of the read role;
//   - wait: Open's wait for the database to answer; retries: how many attempts of it failed, each
//     followed by a pause of a second;
//   - drop: the drop of each database and its read role;
//   - body: each database's life from its clone to its drop, less its opens: the test's own work.
package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// totals are one package's sums, by step, and how many times each step was recorded.
type totals struct {
	took  map[string]time.Duration
	count map[string]int
}

func main() {
	if len(os.Args) != 2 {
		fail(fmt.Errorf("usage: testtiming <directory>"))
	}
	byPackage, err := read(os.Args[1])
	if err != nil {
		fail(err)
	}
	fmt.Print(render(byPackage))
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "testtiming:", err)
	os.Exit(2)
}

// read sums every file of the directory.
func read(directory string) (map[string]*totals, error) {
	files, err := filepath.Glob(filepath.Join(directory, "*.tsv"))
	if err != nil {
		return nil, err
	}
	byPackage := map[string]*totals{}
	for _, path := range files {
		file, err := os.Open(filepath.Clean(path))
		if err != nil {
			return nil, err
		}
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			fields := strings.Split(scanner.Text(), "\t")
			if len(fields) != 3 {
				continue
			}
			nanoseconds, err := strconv.ParseInt(fields[2], 10, 64)
			if err != nil {
				continue
			}
			entry := byPackage[fields[0]]
			if entry == nil {
				entry = &totals{took: map[string]time.Duration{}, count: map[string]int{}}
				byPackage[fields[0]] = entry
			}
			entry.took[fields[1]] += time.Duration(nanoseconds)
			entry.count[fields[1]]++
		}
		closeErr := file.Close()
		if err := scanner.Err(); err != nil {
			return nil, err
		}
		if closeErr != nil {
			return nil, closeErr
		}
	}
	return byPackage, nil
}

// render is the table, the slowest package first, and a line of totals.
func render(byPackage map[string]*totals) string {
	type row struct {
		name                                  string
		clone, schema, role, wait, drop, body time.Duration
		databases, retries                    int
	}
	var rows []row
	sum := row{name: "total"}
	for name, entry := range byPackage {
		r := row{name: strings.TrimPrefix(name, "github.com/NaejEL/opnview/"),
			clone: entry.took["clone"], schema: entry.took["schema"], role: entry.took["role"],
			wait: entry.took["wait"], drop: entry.took["drop"], databases: entry.count["clone"],
			retries: entry.count["retry"]}
		r.body = entry.took["lifetime"] - r.schema - r.role - r.wait
		if r.body < 0 {
			r.body = 0
		}
		rows = append(rows, r)
		sum.clone += r.clone
		sum.schema += r.schema
		sum.role += r.role
		sum.wait += r.wait
		sum.drop += r.drop
		sum.body += r.body
		sum.databases += r.databases
		sum.retries += r.retries
	}
	sort.Slice(rows, func(i, j int) bool {
		a := rows[i].clone + rows[i].schema + rows[i].role + rows[i].wait + rows[i].drop + rows[i].body
		b := rows[j].clone + rows[j].schema + rows[j].role + rows[j].wait + rows[j].drop + rows[j].body
		if a != b {
			return a > b
		}
		return rows[i].name < rows[j].name
	})
	var out strings.Builder
	seconds := func(d time.Duration) string { return fmt.Sprintf("%.1f", d.Seconds()) }
	line := func(r row) {
		fmt.Fprintf(&out, "%-44s %9d %8s %8s %8s %8s %7d %8s %8s\n", r.name, r.databases, seconds(r.clone),
			seconds(r.schema), seconds(r.role), seconds(r.wait), r.retries, seconds(r.drop), seconds(r.body))
	}
	fmt.Fprintf(&out, "%-44s %9s %8s %8s %8s %8s %7s %8s %8s\n", "package", "databases", "clone", "schema",
		"role", "wait", "retries", "drop", "body")
	for _, r := range rows {
		line(r)
	}
	line(sum)
	return out.String()
}
