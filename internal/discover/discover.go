package discover

import (
	"bufio"
	"bytes"
	"errors"
	"flag"
	"fmt"
	"go/format"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// DirectivePrefix begins the //go:generate line CheckDirective reads.
const DirectivePrefix = "//go:generate go run github.com/NaejEL/opnview/ci/discover"

// Spec describes one generated aggregation file. Every path is slash-separated and relative to
// the repository root, the directory holding go.mod.
type Spec struct {
	// Root is the directory whose subtree is walked.
	Root string
	// Marker is the file name that makes a directory an implementation.
	Marker string
	// MinDepth and MaxDepth bound how far below Root a marker may sit; Root itself is depth 0.
	// A MaxDepth of 0 means no upper bound.
	MinDepth, MaxDepth int
	// RequireGo keeps only directories holding at least one non-test .go file: a directory
	// that is pure data has nothing to import.
	RequireGo bool
	// Output is the generated file.
	Output string
	// Package is the generated file's package clause.
	Package string
	// What is one sentence naming what the family is, for the file's comment.
	What string
}

// ParseFlags builds a Spec from the generator's command-line flags.
func ParseFlags(arguments []string) (Spec, error) {
	var spec Spec
	flags := flag.NewFlagSet("discover", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&spec.Root, "root", "", "directory whose subtree is walked")
	flags.StringVar(&spec.Marker, "marker", "", "file name that marks an implementation directory")
	flags.IntVar(&spec.MinDepth, "min-depth", 1, "shallowest depth below root")
	flags.IntVar(&spec.MaxDepth, "max-depth", 0, "deepest depth below root (0: unbounded)")
	flags.BoolVar(&spec.RequireGo, "require-go", true, "keep only directories with non-test Go files")
	flags.StringVar(&spec.Output, "out", "", "generated file")
	flags.StringVar(&spec.Package, "package", "", "package clause of the generated file")
	flags.StringVar(&spec.What, "what", "the implementations", "what the family is")
	if err := flags.Parse(arguments); err != nil {
		return spec, fmt.Errorf("discover: %w", err)
	}
	if spec.Root == "" || spec.Marker == "" || spec.Output == "" || spec.Package == "" {
		return spec, errors.New("discover: -root, -marker, -out and -package are required")
	}
	return spec, nil
}

// RepositoryRoot returns the nearest ancestor of dir holding a go.mod, and the module path.
func RepositoryRoot(dir string) (string, string, error) {
	current, err := filepath.Abs(dir)
	if err != nil {
		return "", "", err
	}
	for {
		raw, err := os.ReadFile(filepath.Join(current, "go.mod"))
		if err == nil {
			scanner := bufio.NewScanner(bytes.NewReader(raw))
			for scanner.Scan() {
				if line := strings.TrimSpace(scanner.Text()); strings.HasPrefix(line, "module ") {
					return current, strings.TrimSpace(strings.TrimPrefix(line, "module ")), nil
				}
			}
			return "", "", fmt.Errorf("discover: %s/go.mod declares no module", current)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", "", errors.New("discover: no go.mod above " + dir)
		}
		current = parent
	}
}

// Directories returns the implementation directories of a spec, relative to the repository
// root, sorted.
func Directories(repoRoot string, spec Spec) ([]string, error) {
	base := filepath.Join(repoRoot, filepath.FromSlash(spec.Root))
	var found []string
	err := filepath.WalkDir(base, func(current string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Name() != spec.Marker {
			return nil
		}
		directory := filepath.Dir(current)
		relative, err := filepath.Rel(base, directory)
		if err != nil {
			return err
		}
		depth := 0
		if relative != "." {
			depth = strings.Count(filepath.ToSlash(relative), "/") + 1
		}
		if depth < spec.MinDepth || (spec.MaxDepth > 0 && depth > spec.MaxDepth) {
			return nil
		}
		if spec.RequireGo {
			hasGo, err := holdsGoCode(directory)
			if err != nil {
				return err
			}
			if !hasGo {
				return nil
			}
		}
		repoRelative, err := filepath.Rel(repoRoot, directory)
		if err != nil {
			return err
		}
		found = append(found, filepath.ToSlash(repoRelative))
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("discover: walking %s: %w", spec.Root, err)
	}
	sort.Strings(found)
	return found, nil
}

// holdsGoCode reports whether a directory has a non-test .go file.
func holdsGoCode(directory string) (bool, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() && strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go") {
			return true, nil
		}
	}
	return false, nil
}

// Render returns the content the generated file must have.
func Render(repoRoot, module string, spec Spec) ([]byte, error) {
	directories, err := Directories(repoRoot, spec)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	out.WriteString("// Code generated by `go generate`; DO NOT EDIT.\n//\n")
	fmt.Fprintf(&out, "// This file links every directory below %s holding a %s\n", spec.Root, spec.Marker)
	fmt.Fprintf(&out, "// into the binary: %s.\n", spec.What)
	out.WriteString("// It is rewritten from the directory tree, never edited, and a test fails when it is\n")
	out.WriteString("// stale. Adding an implementation is adding its directory and running\n")
	out.WriteString("// `go generate ./...`; no line below is ever written by hand.\n")
	fmt.Fprintf(&out, "package %s\n\n", spec.Package)
	if len(directories) > 0 {
		out.WriteString("import (\n")
		for _, directory := range directories {
			fmt.Fprintf(&out, "\t_ %q\n", path.Join(module, directory))
		}
		out.WriteString(")\n")
	}
	formatted, err := format.Source(out.Bytes())
	if err != nil {
		return nil, fmt.Errorf("discover: formatting the generated file: %w", err)
	}
	return formatted, nil
}

// Generate writes the generated file of a spec, starting from startDir to find the repository.
func Generate(startDir string, spec Spec) error {
	repoRoot, module, err := RepositoryRoot(startDir)
	if err != nil {
		return err
	}
	content, err := Render(repoRoot, module, spec)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(repoRoot, filepath.FromSlash(spec.Output)), content, 0o644)
}

// Check reports an error when the generated file on disk differs from what Render produces,
// naming the command that fixes it.
func Check(startDir string, spec Spec) error {
	repoRoot, module, err := RepositoryRoot(startDir)
	if err != nil {
		return err
	}
	want, err := Render(repoRoot, module, spec)
	if err != nil {
		return err
	}
	got, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(spec.Output)))
	if err != nil {
		return fmt.Errorf("discover: %s cannot be read (%v); run `go generate ./...`", spec.Output, err)
	}
	if !bytes.Equal(got, want) {
		return fmt.Errorf("discover: %s is stale: it does not match the directories below %s; "+
			"run `go generate ./...` and keep the result", spec.Output, spec.Root)
	}
	return nil
}

// SpecFromDirective reads the //go:generate directive of a Go source file and returns the Spec
// it carries. It is how the staleness test of a consuming package learns the parameters
// without a second copy of them.
func SpecFromDirective(goFile string) (Spec, error) {
	raw, err := os.ReadFile(goFile)
	if err != nil {
		return Spec{}, err
	}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, DirectivePrefix) {
			continue
		}
		return ParseFlags(splitArguments(strings.TrimPrefix(line, DirectivePrefix)))
	}
	return Spec{}, fmt.Errorf("discover: %s carries no %q directive", goFile, DirectivePrefix)
}

// CheckDirective checks the generated file named by the directive in goFile (a path relative to
// the calling package's directory, which is a test's working directory).
func CheckDirective(goFile string) error {
	spec, err := SpecFromDirective(goFile)
	if err != nil {
		return err
	}
	return Check(filepath.Dir(goFile), spec)
}

// splitArguments splits a directive's argument text the way the shell `go generate` hands it
// to the command does for the cases a directive uses: whitespace separates arguments, and a
// double-quoted run is one argument with its quotes removed.
func splitArguments(text string) []string {
	var arguments []string
	var current strings.Builder
	quoted, started := false, false
	for _, r := range text {
		switch {
		case r == '"':
			quoted = !quoted
			started = true
		case (r == ' ' || r == '\t') && !quoted:
			if started {
				arguments = append(arguments, current.String())
				current.Reset()
				started = false
			}
		default:
			current.WriteRune(r)
			started = true
		}
	}
	if started {
		arguments = append(arguments, current.String())
	}
	return arguments
}
