package main

import (
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func main() {
	write := os.Args[1] == "-write"
	roots := os.Args[2:]
	var missing []string
	for _, root := range roots {
		check(filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() || d.Name() == "testdata" {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			entries, err := os.ReadDir(path)
			check(err)
			var files []string
			hasDoc := false
			for _, e := range entries {
				n := e.Name()
				if e.IsDir() || !strings.HasSuffix(n, ".go") || strings.HasSuffix(n, "_test.go") {
					continue
				}
				if n == "doc.go" {
					hasDoc = true
				}
				files = append(files, n)
			}
			if len(files) == 0 || hasDoc {
				return nil
			}
			sort.Strings(files)
			var withDoc []string
			pkg := ""
			for _, n := range files {
				fset := token.NewFileSet()
				f, err := parser.ParseFile(fset, filepath.Join(path, n), nil, parser.ParseComments|parser.PackageClauseOnly)
				check(err)
				pkg = f.Name.Name
				if f.Doc != nil {
					withDoc = append(withDoc, n)
				}
			}
			if len(withDoc) == 0 {
				missing = append(missing, path+" ("+pkg+")")
				return nil
			}
			fmt.Printf("%s: comment in %s\n", path, strings.Join(withDoc, ", "))
			if !write {
				return nil
			}
			// The comment of the file whose comment begins "Package <name>", else the first.
			source := withDoc[0]
			for _, n := range withDoc {
				raw, _ := os.ReadFile(filepath.Join(path, n))
				fset := token.NewFileSet()
				f, _ := parser.ParseFile(fset, "", raw, parser.ParseComments|parser.PackageClauseOnly)
				if strings.HasPrefix(f.Doc.Text(), "Package "+pkg+" ") {
					source = n
					break
				}
			}
			raw, err := os.ReadFile(filepath.Join(path, source))
			check(err)
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, "", raw, parser.ParseComments|parser.PackageClauseOnly)
			check(err)
			from := fset.Position(f.Doc.Pos()).Offset
			to := fset.Position(f.Doc.End()).Offset
			comment := string(raw[from:to])
			rest := string(raw[:from]) + strings.TrimLeft(string(raw[to:]), "\n")
			check(os.WriteFile(filepath.Join(path, source), []byte(rest), 0o644))
			check(os.WriteFile(filepath.Join(path, "doc.go"), []byte(comment+"\npackage "+pkg+"\n"), 0o644))
			return nil
		}))
	}
	for _, m := range missing {
		fmt.Println("MISSING", m)
	}
}

func check(err error) {
	if err != nil {
		fatalf("%v", err)
	}
}

// fatalf reports a failure and ends the command.
func fatalf(format string, arguments ...any) {
	fmt.Fprintf(os.Stderr, "mkdoc: "+format+"\n", arguments...)
	os.Exit(1)
}
