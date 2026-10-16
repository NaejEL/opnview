package main

import (
	"flag"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/tools/go/packages"
)

// target is one identifier to rename: a package-level object, a method or field of a named type,
// or a local declared at a line of a file.
type target struct {
	dir, typeName, name, newName string
	// file and line place a local's declaration: the file relative to the root.
	file string
	line int
	// declared is the position of the object's declaration, which identifies it in every variant
	// of its package (the package itself and its test variants).
	declared string
}

func main() {
	root := flag.String("root", ".", "the module root")
	qualify := flag.String("qualify", "", "a package name: a renamed package-level identifier is used as name.New outside -home")
	home := flag.String("home", "", "with -qualify, the file the renamed declarations are about to move with, relative to the root")
	loose := flag.Bool("loose", false, "rename even where the module does not type-check, to repair a rename that broke it")
	flag.Parse()
	absolute, err := filepath.Abs(*root)
	if err != nil {
		fatalf("%v", err)
	}
	var targets []*target
	for _, argument := range flag.Args() {
		targets = append(targets, parseTarget(argument))
	}
	if len(targets) == 0 {
		fatalf("no identifier to rename")
	}
	fset := token.NewFileSet()
	config := &packages.Config{Mode: packages.LoadAllSyntax, Tests: true, Dir: absolute, Fset: fset}
	loaded, err := packages.Load(config, "./...")
	if err != nil {
		fatalf("%v", err)
	}
	failures := 0
	packages.Visit(loaded, nil, func(p *packages.Package) {
		for _, e := range p.Errors {
			fmt.Fprintln(os.Stderr, "load error:", e)
			failures++
		}
	})
	if failures > 0 && !*loose {
		os.Exit(1)
	}
	module := modulePath(absolute)
	byDeclaration := map[string]*target{}
	for _, t := range targets {
		var object types.Object
		if t.file != "" {
			object = resolveLocal(loaded, fset, filepath.Join(absolute, filepath.FromSlash(t.file)), t)
		} else {
			object = resolve(loaded, module+"/"+t.dir, t)
		}
		if object == nil {
			fatalf("%s not found", t.qualified())
		}
		t.declared = fset.Position(object.Pos()).String()
		byDeclaration[t.declared] = t
	}
	// Every identifier, in every variant, that declares or uses a target; one edit per position.
	type edit struct {
		target      *target
		replacement string
	}
	edits := map[string]map[int]edit{}
	homeFile := ""
	if *home != "" {
		homeFile = filepath.Join(absolute, filepath.FromSlash(*home))
	}
	for _, p := range loaded {
		if p.TypesInfo == nil {
			continue
		}
		note := func(id *ast.Ident, object types.Object, use bool) {
			if object == nil {
				return
			}
			t := byDeclaration[fset.Position(object.Pos()).String()]
			if t == nil {
				return
			}
			if v, isVar := object.(*types.Var); isVar && v.Embedded() {
				fatalf("%s is an embedded field, named by its type: rename the type", t.qualified())
			}
			position := fset.Position(id.Pos())
			if edits[position.Filename] == nil {
				edits[position.Filename] = map[int]edit{}
			}
			replacement := t.newName
			if use && *qualify != "" && t.typeName == "" && t.file == "" && position.Filename != homeFile {
				replacement = *qualify + "." + t.newName
			}
			edits[position.Filename][position.Offset] = edit{t, replacement}
		}
		for id, object := range p.TypesInfo.Defs {
			note(id, object, false)
		}
		for id, object := range p.TypesInfo.Uses {
			note(id, object, true)
		}
	}
	var files []string
	for file := range edits {
		files = append(files, file)
	}
	sort.Strings(files)
	for _, file := range files {
		raw, err := os.ReadFile(file)
		if err != nil {
			fatalf("%v", err)
		}
		var offsets []int
		for offset := range edits[file] {
			offsets = append(offsets, offset)
		}
		sort.Sort(sort.Reverse(sort.IntSlice(offsets)))
		for _, offset := range offsets {
			e := edits[file][offset]
			if !strings.HasPrefix(string(raw[offset:]), e.target.name) {
				fatalf("%s:%d does not read %s", file, offset, e.target.name)
			}
			raw = append(raw[:offset], append([]byte(e.replacement), raw[offset+len(e.target.name):]...)...)
		}
		if err := os.WriteFile(file, raw, 0o644); err != nil {
			fatalf("%v", err)
		}
		relative, _ := filepath.Rel(absolute, file)
		fmt.Printf("renamed %d in %s\n", len(offsets), filepath.ToSlash(relative))
	}
}

// parseTarget reads DIR:Name=New, DIR:Type.Member=New or @FILE:LINE:name=new.
func parseTarget(argument string) *target {
	if strings.HasPrefix(argument, "@") {
		parts := strings.SplitN(strings.TrimPrefix(argument, "@"), ":", 3)
		equals := strings.LastIndex(argument, "=")
		if len(parts) != 3 || equals < 0 {
			fatalf("%q is not @FILE:LINE:name=new", argument)
		}
		line, err := strconv.Atoi(parts[1])
		if err != nil {
			fatalf("%q: %v", argument, err)
		}
		name := parts[2][:strings.LastIndex(parts[2], "=")]
		return &target{file: parts[0], line: line, name: name, newName: argument[equals+1:]}
	}
	colon := strings.LastIndex(argument, ":")
	equals := strings.LastIndex(argument, "=")
	if colon < 0 || equals < colon {
		fatalf("%q is not DIR:Name=New or DIR:Type.Member=New", argument)
	}
	t := &target{dir: strings.TrimSuffix(argument[:colon], "/"), newName: argument[equals+1:]}
	name := argument[colon+1 : equals]
	if dot := strings.Index(name, "."); dot >= 0 {
		t.typeName, t.name = name[:dot], name[dot+1:]
	} else {
		t.name = name
	}
	return t
}

// resolveLocal finds the object an identifier declares at a line of a file.
func resolveLocal(loaded []*packages.Package, fset *token.FileSet, file string, t *target) types.Object {
	for _, p := range loaded {
		if p.TypesInfo == nil {
			continue
		}
		for id, object := range p.TypesInfo.Defs {
			if object == nil || id.Name != t.name {
				continue
			}
			position := fset.Position(id.Pos())
			if position.Filename == file && position.Line == t.line {
				return object
			}
		}
	}
	return nil
}

func (t *target) qualified() string {
	if t.file != "" {
		return fmt.Sprintf("%s:%d:%s", t.file, t.line, t.name)
	}
	if t.typeName != "" {
		return t.typeName + "." + t.name
	}
	return t.name
}

// resolve finds a target's object in the package itself, or in its test variant for an object a
// test file declares, its external test package included.
func resolve(loaded []*packages.Package, path string, t *target) types.Object {
	if object := resolveIn(loaded, path, t, true); object != nil {
		return object
	}
	return resolveIn(loaded, path, t, false)
}

func resolveIn(loaded []*packages.Package, path string, t *target, plain bool) types.Object {
	for _, p := range loaded {
		if (p.PkgPath != path && p.PkgPath != path+"_test") || p.Types == nil || (p.ID == p.PkgPath) != plain {
			continue
		}
		scope := p.Types.Scope()
		if t.typeName == "" {
			if object := scope.Lookup(t.name); object != nil {
				return object
			}
			continue
		}
		named, ok := scope.Lookup(t.typeName).(*types.TypeName)
		if !ok {
			continue
		}
		object, index, _ := types.LookupFieldOrMethod(named.Type(), true, p.Types, t.name)
		if object != nil && len(index) == 1 {
			return object
		}
	}
	return nil
}

// modulePath reads the module path of the root's go.mod.
func modulePath(root string) string {
	raw, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		fatalf("%v", err)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module "))
		}
	}
	fatalf("%s/go.mod declares no module", root)
	return ""
}

// fatalf reports a failure and ends the command.
func fatalf(format string, arguments ...any) {
	fmt.Fprintf(os.Stderr, "rename: "+format+"\n", arguments...)
	os.Exit(1)
}
