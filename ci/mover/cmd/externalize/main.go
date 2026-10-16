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
	"strings"

	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/imports"
)

func main() {
	root := flag.String("root", ".", "the module root")
	dry := flag.Bool("dry", false, "report what would stop the conversion, and write nothing")
	keep := flag.String("keep", "", "comma-separated test files that stay internal tests")
	flag.Parse()
	if flag.NArg() != 1 {
		fatalf("usage: externalize [-root DIR] [-dry] PACKAGE_DIR")
	}
	absolute, err := filepath.Abs(*root)
	if err != nil {
		fatalf("%v", err)
	}
	directory := filepath.Join(absolute, filepath.FromSlash(flag.Arg(0)))
	fset := token.NewFileSet()
	config := &packages.Config{Mode: packages.LoadAllSyntax, Tests: true, Dir: directory, Fset: fset}
	loaded, err := packages.Load(config, ".")
	if err != nil {
		fatalf("%v", err)
	}
	// The internal test variant: the package's files and its internal test files, type-checked
	// together.
	var variant *packages.Package
	for _, p := range loaded {
		if p.ID != p.PkgPath && strings.HasSuffix(p.ID, ".test]") && !strings.HasSuffix(p.Name, "_test") {
			variant = p
		}
		for _, e := range p.Errors {
			fatalf("load error: %v", e)
		}
	}
	if variant == nil {
		fatalf("%s has no internal test files", flag.Arg(0))
	}
	kept := map[string]bool{}
	for _, file := range strings.Split(*keep, ",") {
		if file != "" {
			kept[file] = true
		}
	}
	name := variant.Name
	scope := variant.Types.Scope()
	// An identifier a converted test file declares stays its own; one a kept file declares does not:
	// the kept file stays in the package.
	isTestFile := func(pos token.Pos) bool {
		filename := fset.Position(pos).Filename
		return strings.HasSuffix(filename, "_test.go") && !kept[filepath.Base(filename)]
	}
	type edit struct {
		offset, length int
		text           string
	}
	edits := map[string][]edit{}
	var problems []string
	problem := func(pos token.Pos, format string, arguments ...any) {
		problems = append(problems, fmt.Sprintf("%s: %s", relative(absolute, fset.Position(pos)),
			fmt.Sprintf(format, arguments...)))
	}
	for _, file := range variant.Syntax {
		filename := fset.Position(file.Pos()).Filename
		if !strings.HasSuffix(filename, "_test.go") || kept[filepath.Base(filename)] {
			continue
		}
		clause := fset.Position(file.Name.Pos())
		edits[filename] = append(edits[filename], edit{clause.Offset, len(file.Name.Name), name + "_test"})
		ast.Inspect(file, func(node ast.Node) bool {
			switch n := node.(type) {
			case *ast.FuncDecl:
				if n.Recv != nil {
					if named := receiverType(variant.TypesInfo, n); named != nil && !isTestFile(named.Obj().Pos()) {
						problem(n.Pos(), "a method of %s, a type of the package, is declared in a test file", named.Obj().Name())
					}
				}
			case *ast.SelectorExpr:
				selection := variant.TypesInfo.Selections[n]
				if selection != nil && !selection.Obj().Exported() && selection.Obj().Pkg() == variant.Types &&
					!isTestFile(selection.Obj().Pos()) {
					problem(n.Sel.Pos(), "uses %s, unexported, of the package", n.Sel.Name)
				}
			case *ast.KeyValueExpr:
				if key, isIdent := n.Key.(*ast.Ident); isIdent {
					if field, isVar := variant.TypesInfo.Uses[key].(*types.Var); isVar && field.IsField() &&
						!field.Exported() && field.Pkg() == variant.Types && !isTestFile(field.Pos()) {
						problem(key.Pos(), "sets %s, an unexported field of the package", key.Name)
					}
				}
			case *ast.Ident:
				object := variant.TypesInfo.Uses[n]
				if object == nil || object.Pkg() != variant.Types || object.Parent() != scope ||
					isTestFile(object.Pos()) {
					return true
				}
				if !object.Exported() {
					problem(n.Pos(), "uses %s, unexported, of the package", n.Name)
					return true
				}
				// A local of the test that is spelled like the package hides it here.
				if inner := variant.Types.Scope().Innermost(n.Pos()); inner != nil {
					if _, shadow := inner.LookupParent(name, n.Pos()); shadow != nil && shadow.Parent() != variant.Types.Scope() &&
						shadow.Parent() != types.Universe {
						if _, isPackage := shadow.(*types.PkgName); !isPackage {
							problem(n.Pos(), "a local named %s hides the package where %s is used", name, n.Name)
						}
					}
				}
				position := fset.Position(n.Pos())
				edits[filename] = append(edits[filename], edit{position.Offset, len(n.Name), name + "." + n.Name})
			}
			return true
		})
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		for _, p := range problems {
			fmt.Println("PROBLEM:", p)
		}
		os.Exit(2)
	}
	if *dry {
		return
	}
	importLine := fmt.Sprintf("%q", variant.PkgPath)
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
		list := edits[file]
		sort.Slice(list, func(i, j int) bool { return list[i].offset > list[j].offset })
		for _, e := range list {
			raw = append(raw[:e.offset], append([]byte(e.text), raw[e.offset+e.length:]...)...)
		}
		text := string(raw)
		if strings.Contains(text, name+".") && !strings.Contains(text, importLine) {
			if at := strings.Index(text, "import ("); at >= 0 {
				text = text[:at+len("import (")] + "\n\t" + importLine + text[at+len("import ("):]
			} else if at := strings.Index(text, "\nimport "); at >= 0 {
				text = text[:at] + "\nimport " + importLine + text[at:]
			} else {
				clause := strings.Index(text, "package "+name+"_test")
				end := clause + len("package "+name+"_test")
				text = text[:end] + "\n\nimport " + importLine + "\n" + text[end:]
			}
		}
		formatted, err := imports.Process(file, []byte(text), &imports.Options{Comments: true, TabIndent: true, TabWidth: 8, FormatOnly: true})
		if err != nil {
			fatalf("%s: %v", file, err)
		}
		if err := os.WriteFile(file, formatted, 0o644); err != nil {
			fatalf("%v", err)
		}
		fmt.Printf("externalized %s\n", relative(absolute, token.Position{Filename: file}))
	}
}

// receiverType is the named type a method is declared on.
func receiverType(info *types.Info, declaration *ast.FuncDecl) *types.Named {
	function, isFunc := info.Defs[declaration.Name].(*types.Func)
	if !isFunc {
		return nil
	}
	receiver := function.Type().(*types.Signature).Recv()
	if receiver == nil {
		return nil
	}
	t := receiver.Type()
	if pointer, isPointer := t.(*types.Pointer); isPointer {
		t = pointer.Elem()
	}
	named, _ := t.(*types.Named)
	return named
}

func relative(root string, position token.Position) string {
	path, err := filepath.Rel(root, position.Filename)
	if err != nil {
		path = position.Filename
	}
	if position.Line > 0 {
		return fmt.Sprintf("%s:%d", filepath.ToSlash(path), position.Line)
	}
	return filepath.ToSlash(path)
}

// fatalf reports a failure and ends the command.
func fatalf(format string, arguments ...any) {
	fmt.Fprintf(os.Stderr, "externalize: "+format+"\n", arguments...)
	os.Exit(1)
}
