package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"sort"

	"golang.org/x/tools/imports"
)

func main() {
	src, dst := os.Args[1], os.Args[2]
	want := map[string]bool{}
	for _, n := range os.Args[3:] {
		want[n] = true
	}
	raw, err := os.ReadFile(src)
	check(err)
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, src, raw, parser.ParseComments)
	check(err)
	type span struct{ from, to int }
	var spans []span
	found := map[string]bool{}
	for _, decl := range file.Decls {
		var name string
		var doc *ast.CommentGroup
		switch d := decl.(type) {
		case *ast.FuncDecl:
			name = d.Name.Name
			if d.Recv != nil && len(d.Recv.List) > 0 {
				t := d.Recv.List[0].Type
				if s, ok := t.(*ast.StarExpr); ok {
					t = s.X
				}
				if id, ok := t.(*ast.Ident); ok {
					name = id.Name + "." + name
				}
			}
			doc = d.Doc
		case *ast.GenDecl:
			if len(d.Specs) == 0 {
				continue
			}
			switch s := d.Specs[0].(type) {
			case *ast.TypeSpec:
				name = s.Name.Name
			case *ast.ValueSpec:
				if len(s.Names) != 1 {
					continue
				}
				name = s.Names[0].Name
			default:
				continue
			}
			doc = d.Doc
		}
		if !want[name] {
			continue
		}
		found[name] = true
		from := decl.Pos()
		if doc != nil {
			from = doc.Pos()
		}
		spans = append(spans, span{fset.Position(from).Offset, fset.Position(decl.End()).Offset})
	}
	for n := range want {
		if !found[n] {
			fmt.Fprintln(os.Stderr, "not found:", n)
			os.Exit(1)
		}
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].from < spans[j].from })
	var moved bytes.Buffer
	var kept bytes.Buffer
	last := 0
	for _, s := range spans {
		kept.Write(raw[last:s.from])
		moved.WriteString("\n")
		moved.Write(raw[s.from:s.to])
		moved.WriteString("\n")
		last = s.to
		// Drop the newline that ended the declaration.
		for last < len(raw) && raw[last] == '\n' {
			last++
			break
		}
	}
	kept.Write(raw[last:])
	target, err := os.ReadFile(dst)
	if os.IsNotExist(err) {
		target = []byte("package " + file.Name.Name + "\n")
	} else {
		check(err)
	}
	target = append(bytes.TrimRight(target, "\n"), '\n')
	target = append(target, moved.Bytes()...)
	write(src, kept.Bytes())
	write(dst, target)
}

func write(path string, content []byte) {
	out, err := imports.Process(path, content, &imports.Options{Comments: true, TabIndent: true, TabWidth: 8, FormatOnly: false})
	if err != nil {
		fmt.Fprintln(os.Stderr, path, err)
		out = content
	}
	// imports.Process may collapse nothing else; keep a single trailing newline.
	out = append(bytes.TrimRight(out, "\n"), '\n')
	check(os.WriteFile(path, out, 0o644))
}

func check(err error) {
	if err != nil {
		fatalf("%v", err)
	}
}

// fatalf reports a failure and ends the command.
func fatalf(format string, arguments ...any) {
	fmt.Fprintf(os.Stderr, "movedecl: "+format+"\n", arguments...)
	os.Exit(1)
}
