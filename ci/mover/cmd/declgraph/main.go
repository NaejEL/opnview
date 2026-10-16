package main

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"
)

func main() {
	dir := os.Args[1]
	fset := token.NewFileSet()
	cfg := &packages.Config{Mode: packages.LoadAllSyntax, Dir: dir, Fset: fset, Tests: false}
	pkgs, err := packages.Load(cfg, ".")
	if err != nil {
		fatalf("%v", err)
	}
	p := pkgs[0]
	// owner of each object: the top-level decl declaring it.
	declOf := map[types.Object]string{}
	fileOf := map[string]string{}
	name := func(d ast.Decl) []string {
		switch d := d.(type) {
		case *ast.FuncDecl:
			n := d.Name.Name
			if d.Recv != nil {
				t := d.Recv.List[0].Type
				if s, ok := t.(*ast.StarExpr); ok {
					t = s.X
				}
				if id, ok := t.(*ast.Ident); ok {
					n = id.Name + "." + n
				}
			}
			return []string{n}
		case *ast.GenDecl:
			var out []string
			for _, s := range d.Specs {
				switch s := s.(type) {
				case *ast.TypeSpec:
					out = append(out, s.Name.Name)
				case *ast.ValueSpec:
					for _, n := range s.Names {
						out = append(out, n.Name)
					}
				}
			}
			return out
		}
		return nil
	}
	type declInfo struct {
		key  string
		decl ast.Decl
	}
	var decls []declInfo
	for _, f := range p.Syntax {
		file := filepath.Base(fset.Position(f.Pos()).Filename)
		for _, d := range f.Decls {
			ns := name(d)
			if len(ns) == 0 {
				continue
			}
			key := strings.Join(ns, ",")
			if len(ns) > 3 {
				key = ns[0] + ",...(" + fmt.Sprint(len(ns)) + ")"
			}
			fileOf[key] = file
			decls = append(decls, declInfo{key, d})
			ast.Inspect(d, func(n ast.Node) bool {
				if id, ok := n.(*ast.Ident); ok {
					if obj := p.TypesInfo.Defs[id]; obj != nil {
						if _, seen := declOf[obj]; !seen {
							declOf[obj] = key
						}
					}
				}
				return true
			})
		}
	}
	for _, di := range decls {
		uses := map[string]bool{}
		ast.Inspect(di.decl, func(n ast.Node) bool {
			if id, ok := n.(*ast.Ident); ok {
				if obj := p.TypesInfo.Uses[id]; obj != nil {
					if k, ok := declOf[obj]; ok && k != di.key {
						uses[k] = true
					}
				}
			}
			return true
		})
		var list []string
		for k := range uses {
			list = append(list, k+"@"+fileOf[k])
		}
		sort.Strings(list)
		fmt.Printf("%s@%s: %s\n", di.key, fileOf[di.key], strings.Join(list, " "))
	}
}

// fatalf reports a failure and ends the command.
func fatalf(format string, arguments ...any) {
	fmt.Fprintf(os.Stderr, "declgraph: "+format+"\n", arguments...)
	os.Exit(1)
}
