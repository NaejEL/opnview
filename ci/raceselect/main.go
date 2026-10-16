// Command raceselect decides which packages the pre-deployment command runs under the race
// detector, by a stated rule and never a hand list: a package is selected when any of its Go files,
// its test files included,
//
//	(i)   holds a go statement;
//	(ii)  imports sync or sync/atomic;
//	(iii) serves HTTP: imports net/http/httptest, declares a ServeHTTP method, or names
//	      http.Handler, http.HandlerFunc, http.ServeMux or http.Server;
//	(iv)  or lies under internal/plugin/.
//
// It reads the packages of the module rooted at the directory it runs in (go list ./...), parses
// every file go list names for each, and prints one line per package:
//
//	selected <clause> <import path>
//	excluded - <import path>
//
// where <clause> is the first clause that decided it, (i) to (iv), with the file that triggered it.
// ci/pre-deployment.sh runs the selected packages with -race and the others without it.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// listedPackage is what go list -json says of one package.
type listedPackage struct {
	ImportPath     string
	Dir            string
	GoFiles        []string
	CgoFiles       []string
	TestGoFiles    []string
	XTestGoFiles   []string
	IgnoredGoFiles []string
}

// decision is why a package was selected; an empty clause is an exclusion.
type decision struct {
	clause string
	file   string
}

// pluginHost is the directory clause (iv) selects, relative to the module root.
const pluginHost = "internal/plugin"

func main() {
	root, err := os.Getwd()
	if err != nil {
		fail(err)
	}
	packages, err := list(root)
	if err != nil {
		fail(err)
	}
	for _, listed := range packages {
		decided, err := decide(root, listed)
		if err != nil {
			fail(err)
		}
		if decided.clause == "" {
			fmt.Printf("excluded - %s\n", listed.ImportPath)
			continue
		}
		fmt.Printf("selected %s %s (%s)\n", decided.clause, listed.ImportPath, decided.file)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "raceselect:", err)
	os.Exit(2)
}

// list runs go list -json ./... in the module root, with the build tags the pre-deployment command
// tests with, so a file only that run compiles is read too.
func list(root string) ([]listedPackage, error) {
	command := exec.Command("go", "list", "-json", "-tags", "predeployment", "./...")
	command.Dir = root
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("go list: %w: %s", err, stderr.String())
	}
	decoder := json.NewDecoder(bytes.NewReader(output))
	var packages []listedPackage
	for {
		var listed listedPackage
		if err := decoder.Decode(&listed); err == io.EOF {
			break
		} else if err != nil {
			return nil, err
		}
		packages = append(packages, listed)
	}
	return packages, nil
}

// decide applies the four clauses to one package, in order, and returns the first that holds.
func decide(root string, listed listedPackage) (decision, error) {
	relative, err := filepath.Rel(root, listed.Dir)
	if err != nil {
		return decision{}, err
	}
	relative = filepath.ToSlash(relative)
	var files []string
	for _, group := range [][]string{listed.GoFiles, listed.CgoFiles, listed.TestGoFiles, listed.XTestGoFiles,
		listed.IgnoredGoFiles} {
		files = append(files, group...)
	}
	sources := map[string][]byte{}
	for _, name := range files {
		raw, err := os.ReadFile(filepath.Join(listed.Dir, name))
		if err != nil {
			return decision{}, err
		}
		sources[name] = raw
	}
	decided, err := decideFiles(relative, sources)
	if err != nil {
		return decision{}, fmt.Errorf("%s: %w", listed.ImportPath, err)
	}
	return decided, nil
}

// decideFiles applies the four clauses to the files of a package in a directory relative to the
// module root, by file name, and returns the first clause that holds with the file that triggered it.
func decideFiles(directory string, sources map[string][]byte) (decision, error) {
	names := make([]string, 0, len(sources))
	for name := range sources {
		names = append(names, name)
	}
	sort.Strings(names)
	parsed := map[string]*ast.File{}
	fset := token.NewFileSet()
	for _, name := range names {
		file, err := parser.ParseFile(fset, name, sources[name], parser.SkipObjectResolution)
		if err != nil {
			return decision{}, err
		}
		parsed[name] = file
	}
	clauses := []struct {
		name  string
		holds func(*ast.File) bool
	}{
		{"(i)", holdsGoStatement},
		{"(ii)", importsSync},
		{"(iii)", servesHTTP},
	}
	for _, clause := range clauses {
		for _, name := range names {
			if clause.holds(parsed[name]) {
				return decision{clause: clause.name, file: name}, nil
			}
		}
	}
	if directory == pluginHost || strings.HasPrefix(directory, pluginHost+"/") {
		return decision{clause: "(iv)", file: directory}, nil
	}
	return decision{}, nil
}

// holdsGoStatement is clause (i).
func holdsGoStatement(file *ast.File) bool {
	found := false
	ast.Inspect(file, func(node ast.Node) bool {
		if _, isGo := node.(*ast.GoStmt); isGo {
			found = true
		}
		return !found
	})
	return found
}

// importsSync is clause (ii).
func importsSync(file *ast.File) bool {
	for _, spec := range file.Imports {
		path, _ := strconv.Unquote(spec.Path.Value)
		if path == "sync" || path == "sync/atomic" {
			return true
		}
	}
	return false
}

// servesHTTP is clause (iii).
func servesHTTP(file *ast.File) bool {
	httpName := ""
	for _, spec := range file.Imports {
		path, _ := strconv.Unquote(spec.Path.Value)
		switch path {
		case "net/http/httptest":
			return true
		case "net/http":
			httpName = "http"
			if spec.Name != nil {
				httpName = spec.Name.Name
			}
		}
	}
	found := false
	ast.Inspect(file, func(node ast.Node) bool {
		switch typed := node.(type) {
		case *ast.FuncDecl:
			if typed.Recv != nil && typed.Name.Name == "ServeHTTP" {
				found = true
			}
		case *ast.SelectorExpr:
			if identifier, isIdent := typed.X.(*ast.Ident); isIdent && httpName != "" && identifier.Name == httpName {
				switch typed.Sel.Name {
				case "Handler", "HandlerFunc", "ServeMux", "Server":
					found = true
				}
			}
		}
		return !found
	})
	return found
}
