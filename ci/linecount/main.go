// Command linecount measures the size of opnview's Go and plugin JavaScript, once and by one
// program (PA21 of specs/SPEC-backend-completion-plugins.md, amendment of 17 October 2026). It is
// run in the development container, from the repository root:
//
//	docker compose run --rm dev go run ./ci/linecount
//
// For every top-level package under cmd/ and internal/ (cmd/opnview, internal/store, ...), it
// prints, separately for non-_test.go and for _test.go files, the total, code, comment and blank
// line counts. A Go line is code when it holds a token other than a comment, comment when it holds
// a comment and nothing else, and blank otherwise; a raw string or a block comment counts every
// line it spans. It then prints the same four figures for the plugin JavaScript under plugins/.
// The figures a ceiling of PA21 applies to are the non-test code lines.
package main

import (
	"bufio"
	"errors"
	"fmt"
	"go/scanner"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// counts is one set of figures.
type counts struct {
	total, code, comment, blank int
}

func (c *counts) add(o counts) {
	c.total += o.total
	c.code += o.code
	c.comment += o.comment
	c.blank += o.blank
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "linecount:", err)
		os.Exit(1)
	}
}

func run() error {
	product := map[string]*counts{}
	tests := map[string]*counts{}
	for _, root := range []string{"cmd", "internal"} {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "testdata" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") {
				return nil
			}
			pkg := topLevel(path)
			c, err := countGo(path)
			if err != nil {
				return err
			}
			into := product
			if strings.HasSuffix(path, "_test.go") {
				into = tests
			}
			if into[pkg] == nil {
				into[pkg] = &counts{}
			}
			into[pkg].add(c)
			return nil
		})
		if err != nil {
			return err
		}
	}
	printTable("Go, non-test files (.go without _test.go)", product)
	fmt.Println()
	printTable("Go, test files (_test.go)", tests)
	fmt.Println()

	var js counts
	files := 0
	err := filepath.WalkDir("plugins", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".js") {
			return nil
		}
		c, err := countJS(path)
		if err != nil {
			return err
		}
		js.add(c)
		files++
		return nil
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	fmt.Printf("Plugin JavaScript (plugins/**/*.js, %d files)\n", files)
	fmt.Printf("%-28s %8s %8s %8s %8s\n", "", "total", "code", "comment", "blank")
	fmt.Printf("%-28s %8d %8d %8d %8d\n", "plugins", js.total, js.code, js.comment, js.blank)
	return nil
}

// topLevel names the top-level package a file belongs to: cmd/<name> or internal/<name>.
func topLevel(path string) string {
	parts := strings.Split(filepath.ToSlash(path), "/")
	if len(parts) <= 2 {
		return parts[0]
	}
	return parts[0] + "/" + parts[1]
}

func printTable(title string, by map[string]*counts) {
	names := make([]string, 0, len(by))
	for n := range by {
		names = append(names, n)
	}
	sort.Strings(names)
	fmt.Println(title)
	fmt.Printf("%-28s %8s %8s %8s %8s\n", "package", "total", "code", "comment", "blank")
	var sum counts
	for _, n := range names {
		c := by[n]
		sum.add(*c)
		fmt.Printf("%-28s %8d %8d %8d %8d\n", n, c.total, c.code, c.comment, c.blank)
	}
	fmt.Printf("%-28s %8d %8d %8d %8d\n", "TOTAL", sum.total, sum.code, sum.comment, sum.blank)
}

// countGo classifies every line of one Go file with the Go scanner.
func countGo(path string) (counts, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return counts{}, err
	}
	lines := strings.Count(string(src), "\n")
	if len(src) > 0 && src[len(src)-1] != '\n' {
		lines++
	}
	code := make([]bool, lines+2)
	comment := make([]bool, lines+2)
	fset := token.NewFileSet()
	file := fset.AddFile(path, -1, len(src))
	var s scanner.Scanner
	var scanErr error
	s.Init(file, src, func(pos token.Position, msg string) {
		if scanErr == nil {
			scanErr = fmt.Errorf("%s: %s", pos, msg)
		}
	}, scanner.ScanComments)
	for {
		pos, tok, lit := s.Scan()
		if tok == token.EOF {
			break
		}
		if tok == token.SEMICOLON && lit == "\n" {
			continue // an automatically inserted semicolon
		}
		first := fset.Position(pos).Line
		text := lit
		if text == "" {
			text = tok.String()
		}
		last := first + strings.Count(text, "\n")
		mark := code
		if tok == token.COMMENT {
			mark = comment
		}
		for l := first; l <= last && l < len(mark); l++ {
			mark[l] = true
		}
	}
	if scanErr != nil {
		return counts{}, scanErr
	}
	c := counts{total: lines}
	for l := 1; l <= lines; l++ {
		switch {
		case code[l]:
			c.code++
		case comment[l]:
			c.comment++
		default:
			c.blank++
		}
	}
	return c, nil
}

// countJS classifies the lines of one JavaScript file: blank, comment (a line inside a block
// comment, or starting with // or /*), or code. It is a line classifier, not a parser, and the
// same one is used for every count it is compared with.
func countJS(path string) (counts, error) {
	f, err := os.Open(path)
	if err != nil {
		return counts{}, err
	}
	defer f.Close()
	var c counts
	inBlock := false
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		c.total++
		switch {
		case inBlock:
			c.comment++
			if strings.Contains(line, "*/") {
				inBlock = false
			}
		case line == "":
			c.blank++
		case strings.HasPrefix(line, "//"):
			c.comment++
		case strings.HasPrefix(line, "/*"):
			c.comment++
			if !strings.Contains(line[2:], "*/") {
				inBlock = true
			}
		default:
			c.code++
		}
	}
	return c, sc.Err()
}
