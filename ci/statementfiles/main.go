// Command statementfiles prints, one path a line and sorted, every statements file under the
// directories it is given: every .sql file holding a line that begins "-- statement: ", the marker
// every statements file of the tree is split on, whoever owns it -- the store, a widget, a provider,
// a plugin a user added. sql/schema-checks.sh plans every statement of every file it prints, so a
// statements file added later is planned without being listed anywhere.
//
//	statementfiles <directory>...
//
// A directory named testdata is not walked: what it holds is a test's input, not a statement the
// product runs.
package main

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Marker begins the line that names a statement in a statements file.
const Marker = "-- statement: "

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "statementfiles: name at least one directory")
		os.Exit(2)
	}
	files, err := Find(os.Args[1:]...)
	if err != nil {
		fmt.Fprintln(os.Stderr, "statementfiles:", err)
		os.Exit(1)
	}
	for _, file := range files {
		fmt.Println(file)
	}
}

// Find returns, sorted, every statements file under the roots.
func Find(roots ...string) ([]string, error) {
	var files []string
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				if entry.Name() == "testdata" {
					return filepath.SkipDir
				}
				return nil
			}
			if filepath.Ext(path) != ".sql" {
				return nil
			}
			marked, err := holdsMarker(path)
			if err != nil {
				return err
			}
			if marked {
				files = append(files, path)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Strings(files)
	return files, nil
}

// holdsMarker reports whether a file holds a line beginning with the marker.
func holdsMarker(path string) (bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer func() { _ = file.Close() }()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		if strings.HasPrefix(scanner.Text(), Marker) {
			return true, nil
		}
	}
	return false, scanner.Err()
}
