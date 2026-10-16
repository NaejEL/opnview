package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/format"
	"go/token"
	"go/types"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/tools/go/ast/astutil"
	"golang.org/x/tools/go/packages"
)

type Plan struct {
	Module   string             `json:"module"`
	Source   string             `json:"source"`
	Packages map[string]PkgPlan `json:"packages"`
	Spread   *Spread            `json:"spread"`
	// Names overrides the exported name of an identifier, keyed "Name" (package-level) or
	// "Type.Name" (method or field).
	Names map[string]string `json:"names"`
	// Functions are types whose methods, where they move away from the type's package, become
	// package functions taking the receiver first.
	Functions []string `json:"functions"`
	// Above are new packages that sit above the source package and may import it.
	Above []string `json:"above"`
}

type PkgPlan struct {
	Name  string   `json:"name"`
	Files []string `json:"files"`
}

type Spread struct {
	Type     string            `json:"type"`
	Base     string            `json:"base"`
	BaseType string            `json:"base_type"`
	Types    map[string]string `json:"types"`
	Fields   map[string]string `json:"fields"`
}

var dryRun bool

var (
	root    string
	plan    Plan
	fset    *token.FileSet
	srcPath string
	// homeOf maps a source-dir file base name to its new dir.
	homeOf   = map[string]string{}
	nameOf   = map[string]string{} // dir -> package name
	problems []string
)

func dirOf(file string) string {
	rel, err := filepath.Rel(root, filepath.Dir(file))
	if err != nil {
		fatalf("%v", err)
	}
	return filepath.ToSlash(rel)
}

// newDir is the dir a file lives in after the move.
func newDir(file string) string {
	d := dirOf(file)
	if d != plan.Source {
		return d
	}
	if h, ok := homeOf[filepath.Base(file)]; ok {
		return h
	}
	return plan.Source
}

func pkgName(dir string) string {
	if n, ok := nameOf[dir]; ok {
		return n
	}
	return filepath.Base(dir)
}

func importPath(dir string) string { return plan.Module + "/" + dir }

func posKey(pos token.Pos) string {
	p := fset.Position(pos)
	return p.Filename + ":" + strconv.Itoa(p.Line) + ":" + strconv.Itoa(p.Column)
}

func capitalize(name string) string {
	r, size := utf8.DecodeRuneInString(name)
	return string(unicode.ToUpper(r)) + name[size:]
}

// srcObj is an object declared in the source package.
type srcObj struct {
	obj  types.Object
	file string
	home string
	// member is "Type.Name" for a method or a field of a named type, "" otherwise.
	member string
	// convert is a method that becomes a package function.
	convert bool
}

var (
	srcByKey  = map[string]*srcObj{}
	renamed   = map[string]string{} // key -> new name
	srcScope  *types.Scope
	spreadObj types.Object
)

func isPackageLevel(obj types.Object) bool {
	return obj.Pkg() != nil && obj.Parent() == obj.Pkg().Scope()
}

// fileEntry is one Go file with the package variant that type-checked it.
type fileEntry struct {
	pkg  *packages.Package
	file *ast.File
	path string
}

func main() {
	planFile := flag.String("plan", "", "plan file")
	dry := flag.Bool("dry", false, "print the renames only")
	apply := flag.Bool("apply", false, "rewrite without the dry pass (internal)")
	flag.StringVar(&root, "root", ".", "the module root")
	flag.Parse()
	absolute, err := filepath.Abs(root)
	check(err)
	root = absolute
	raw, err := os.ReadFile(*planFile)
	if err != nil {
		fatalf("%v", err)
	}
	if err := json.Unmarshal(raw, &plan); err != nil {
		fatalf("%v", err)
	}
	srcPath = importPath(plan.Source)
	for dir, p := range plan.Packages {
		nameOf[dir] = p.Name
		for _, f := range p.Files {
			if prev, dup := homeOf[f]; dup {
				fatalf("%s assigned to %s and %s", f, prev, dir)
			}
			homeOf[f] = dir
		}
	}
	fset = token.NewFileSet()
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedSyntax | packages.NeedTypes |
			packages.NeedTypesInfo | packages.NeedImports | packages.NeedCompiledGoFiles | packages.NeedDeps,
		Tests: true,
		Dir:   root,
		Fset:  fset,
	}
	pkgs, err := packages.Load(cfg, "./...")
	if err != nil {
		fatalf("%v", err)
	}
	loadErrors := 0
	packages.Visit(pkgs, nil, func(p *packages.Package) {
		for _, e := range p.Errors {
			if loadErrors < 20 {
				fmt.Fprintln(os.Stderr, "load error:", e)
			}
			loadErrors++
		}
	})
	if loadErrors > 0 {
		fmt.Fprintf(os.Stderr, "%d load errors\n", loadErrors)
		os.Exit(1)
	}
	// One entry per file, from the variant holding the most files (the test variant).
	files := map[string]*fileEntry{}
	for _, p := range pkgs {
		if strings.HasSuffix(p.ID, ".test") {
			continue
		}
		for i, f := range p.Syntax {
			path := p.CompiledGoFiles[i]
			if !strings.HasPrefix(path, root+"/") {
				continue
			}
			prev, seen := files[path]
			if seen && len(prev.pkg.Syntax) >= len(p.Syntax) {
				continue
			}
			files[path] = &fileEntry{pkg: p, file: f, path: path}
		}
	}
	// The source package: its fullest variant.
	var source *packages.Package
	for _, p := range pkgs {
		if p.PkgPath == srcPath && (source == nil || len(p.Syntax) > len(source.Syntax)) {
			source = p
		}
	}
	if source == nil {
		fatalf("%s", "source package not loaded")
	}
	srcScope = source.Types.Scope()
	if plan.Spread != nil {
		spreadObj = srcScope.Lookup(plan.Spread.Type)
		if spreadObj == nil {
			fatalf("%s", "spread type not found")
		}
	}
	for id, obj := range source.TypesInfo.Defs {
		if obj == nil || obj.Pkg() != source.Types {
			continue
		}
		file := fset.Position(id.Pos()).Filename
		so := &srcObj{obj: obj, file: file, home: newDir(file)}
		switch o := obj.(type) {
		case *types.Func:
			if recv := o.Type().(*types.Signature).Recv(); recv != nil {
				so.member = typeName(recv.Type()) + "." + o.Name()
			} else if !isPackageLevel(obj) {
				continue
			}
		case *types.Var:
			if o.IsField() {
				so.member = fieldOwner(source, o) + "." + o.Name()
			} else if !isPackageLevel(obj) {
				continue
			}
		default:
			if !isPackageLevel(obj) {
				continue
			}
		}
		srcByKey[posKey(obj.Pos())] = so
	}
	for _, typ := range plan.Functions {
		tn := srcScope.Lookup(typ)
		if tn == nil {
			fatalf("functions type not found: %s", typ)
		}
		typeHome := newDir(fset.Position(tn.Pos()).Filename)
		for _, so := range srcByKey {
			if _, isFunc := so.obj.(*types.Func); isFunc && strings.HasPrefix(so.member, typ+".") && so.home != typeHome {
				so.convert = true
			}
		}
	}
	// The spread type's fields move to the base type under their new names.
	if plan.Spread != nil {
		for _, so := range srcByKey {
			if strings.HasPrefix(so.member, plan.Spread.Type+".") {
				if v, ok := so.obj.(*types.Var); ok && v.IsField() {
					n, ok := plan.Spread.Fields[v.Name()]
					if !ok {
						problems = append(problems, "spread field without a new name: "+v.Name())
						continue
					}
					renamed[posKey(v.Pos())] = n
				}
			}
		}
	}
	// What must be exported: an unexported object used from a file of another home.
	sortedPaths := make([]string, 0, len(files))
	for p := range files {
		sortedPaths = append(sortedPaths, p)
	}
	sort.Strings(sortedPaths)
	for _, path := range sortedPaths {
		e := files[path]
		home := newDir(path)
		for id, obj := range e.pkg.TypesInfo.Uses {
			if fset.Position(id.Pos()).Filename != path {
				continue
			}
			so := lookup(obj)
			if so != nil && home != plan.Source && !isAbove(home) && dirOf(path) == plan.Source && so.home == plan.Source &&
				!(plan.Spread != nil && obj == spreadObj) && !spreadMember(so) {
				problems = append(problems, fmt.Sprintf("UP %s uses %s (%s) left in %s", rel(path),
					obj.Name(), so.member, rel(so.file)))
			}
			if so != nil && home != plan.Source && !isAbove(home) && dirOf(path) == plan.Source && spreadMember(so) &&
				so.home == plan.Source {
				if _, isField := so.obj.(*types.Var); !isField {
					problems = append(problems, fmt.Sprintf("UP %s calls %s left in %s", rel(path), so.member,
						rel(so.file)))
				}
			}
			edgeHome := ""
			if so != nil {
				edgeHome = so.home
				if plan.Spread != nil && strings.HasPrefix(so.member, plan.Spread.Type+".") {
					if _, isField := so.obj.(*types.Var); isField {
						edgeHome = plan.Spread.Base
					}
				}
				if plan.Spread != nil && obj == spreadObj {
					edgeHome = home
				}
			}
			if so != nil && edgeHome != home && !isExternalTest(e) {
				if importEdges[home] == nil {
					importEdges[home] = map[string]string{}
				}
				importEdges[home][edgeHome] = rel(path) + " uses " + obj.Name()
			}
			if so == nil || obj.Exported() {
				continue
			}
			if _, done := renamed[posKey(so.obj.Pos())]; done {
				continue
			}
			targetHome := so.home
			if so.member != "" && plan.Spread != nil && strings.HasPrefix(so.member, plan.Spread.Type+".") {
				// A spread method called through the facade or another handle.
				targetHome = so.home
			}
			if targetHome == home && !isExternalTest(e) {
				continue
			}
			n := capitalize(obj.Name())
			k := obj.Name()
			if so.member != "" {
				k = so.member
			}
			if o, ok := plan.Names[k]; ok {
				n = o
			}
			renamed[posKey(so.obj.Pos())] = n
		}
	}
	// Overrides for names that stay unexported but are named in the plan.
	for _, so := range srcByKey {
		k := so.obj.Name()
		if so.member != "" {
			k = so.member
		}
		if o, ok := plan.Names[k]; ok {
			if _, done := renamed[posKey(so.obj.Pos())]; !done && (!so.obj.Exported() || so.convert) {
				renamed[posKey(so.obj.Pos())] = o
			}
		}
	}
	report()
	checkCollisions()
	checkCycles()
	// The rewrite runs dry first: what it finds is a problem too, and nothing is written then.
	dryRun = !*apply
	if dryRun {
		for _, path := range sortedPaths {
			rewrite(files[path])
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		last := ""
		for _, p := range problems {
			if p == last {
				continue
			}
			last = p
			fmt.Println("PROBLEM:", p)
		}
		os.Exit(2)
	}
	if *dry {
		return
	}
	if !*apply {
		// The ASTs are spent by the dry rewrite: apply in a fresh process.
		cmd := exec.Command(os.Args[0], append([]string{"-apply"}, os.Args[1:]...)...)
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		check(cmd.Run())
		return
	}
	for _, path := range sortedPaths {
		rewrite(files[path])
	}
	// Non-Go files.
	for dir, p := range plan.Packages {
		for _, f := range p.Files {
			if strings.HasSuffix(f, ".go") {
				continue
			}
			from := filepath.Join(root, plan.Source, f)
			to := filepath.Join(root, dir, f)
			check(os.MkdirAll(filepath.Dir(to), 0o755))
			check(os.Rename(from, to))
			fmt.Println("moved", rel(from), "->", rel(to))
		}
	}
}

func rel(p string) string {
	r, _ := filepath.Rel(root, p)
	return filepath.ToSlash(r)
}

func check(err error) {
	if err != nil {
		fatalf("%v", err)
	}
}

func isExternalTest(e *fileEntry) bool { return strings.HasSuffix(e.file.Name.Name, "_test") }

func typeName(t types.Type) string {
	if p, ok := t.(*types.Pointer); ok {
		t = p.Elem()
	}
	if n, ok := t.(*types.Named); ok {
		return n.Obj().Name()
	}
	return "?"
}

// fieldOwner names the named type whose struct declares a field, or "" for an anonymous struct.
func fieldOwner(p *packages.Package, v *types.Var) string {
	for _, name := range p.Types.Scope().Names() {
		tn, ok := p.Types.Scope().Lookup(name).(*types.TypeName)
		if !ok {
			continue
		}
		st, ok := tn.Type().Underlying().(*types.Struct)
		if !ok {
			continue
		}
		for i := 0; i < st.NumFields(); i++ {
			if st.Field(i) == v {
				return name
			}
		}
	}
	return ""
}

// lookup finds the source object an object is, by position for the source package's own
// variants and by name for a package-level object seen from another package.
func lookup(obj types.Object) *srcObj {
	if obj == nil || obj.Pkg() == nil || obj.Pkg().Path() != srcPath {
		return nil
	}
	if so, ok := srcByKey[posKey(obj.Pos())]; ok {
		return so
	}
	if isPackageLevel(obj) {
		if o := srcScope.Lookup(obj.Name()); o != nil {
			return srcByKey[posKey(o.Pos())]
		}
	}
	return nil
}

func newNameOf(so *srcObj) string {
	// An embedded field is named by its type.
	if v, ok := so.obj.(*types.Var); ok && v.Embedded() {
		t := v.Type()
		if p, ok := t.(*types.Pointer); ok {
			t = p.Elem()
		}
		if n, ok := t.(*types.Named); ok {
			if ts := lookup(n.Obj()); ts != nil {
				return newNameOf(ts)
			}
		}
	}
	if n, ok := renamed[posKey(so.obj.Pos())]; ok {
		return n
	}
	return so.obj.Name()
}

func report() {
	var lines []string
	for k, n := range renamed {
		so := srcByKey[k]
		what := so.obj.Name()
		if so.member != "" {
			what = so.member
		}
		lines = append(lines, fmt.Sprintf("EXPORT %s: %s -> %s (%s)", so.home, what, n, rel(so.file)))
	}
	sort.Strings(lines)
	for _, l := range lines {
		fmt.Println(l)
	}
}

// checkCollisions reports two objects that would share a name in one package scope or one type.
func checkCollisions() {
	seen := map[string]string{}
	for _, so := range srcByKey {
		n := newNameOf(so)
		var k string
		home := so.home
		if plan.Spread != nil && strings.HasPrefix(so.member, plan.Spread.Type+".") {
			if _, isField := so.obj.(*types.Var); isField {
				home = plan.Spread.Base
			}
		}
		if so.member != "" && !so.convert {
			typ := so.member[:strings.Index(so.member, ".")]
			if typ == "" || typ == "?" {
				continue
			}
			k = home + "|" + typ + "." + n
		} else {
			if so.obj.Name() == "init" || so.obj.Name() == "_" {
				continue
			}
			// Test files of a package share its scope.
			k = home + "|" + n
		}
		if prev, dup := seen[k]; dup {
			problems = append(problems, fmt.Sprintf("collision %s: %s and %s", k, prev, rel(so.file)))
		}
		seen[k] = rel(so.file)
	}
}

// rewrite rewrites one file and writes it to its new place.
func rewrite(e *fileEntry) {
	info := e.pkg.TypesInfo
	home := newDir(e.path)
	external := isExternalTest(e)
	bareHome := home
	if external {
		bareHome = ""
	}
	needs := map[string]bool{}
	quals := map[string][]*ast.Ident{}
	shadowed := map[string]bool{}
	// qual is the qualifier of a new package at a position: noted, so a file in which the
	// package's name is shadowed anywhere imports it under an alias.
	qual := func(dir string, pos token.Pos, id *ast.Ident) *ast.Ident {
		name := pkgName(dir)
		if id == nil {
			id = &ast.Ident{Name: name, NamePos: pos}
		} else {
			id.Name = name
		}
		if scope := e.pkg.Types.Scope().Innermost(pos); scope != nil {
			if _, obj := scope.LookupParent(name, pos); obj != nil {
				if pn, ok := obj.(*types.PkgName); !ok || pn.Imported().Path() != importPath(dir) {
					shadowed[dir] = true
				}
			}
		}
		quals[dir] = append(quals[dir], id)
		needs[dir] = true
		return id
	}
	changed := false
	inSource := dirOf(e.path) == plan.Source
	spreadHome := func() string {
		if plan.Spread == nil || home == plan.Source || external {
			return ""
		}
		if home == plan.Spread.Base {
			return plan.Spread.BaseType
		}
		return plan.Spread.Types[home]
	}()
	calledConverted := map[*ast.SelectorExpr]bool{}
	astutil.Apply(e.file, func(c *astutil.Cursor) bool {
		switch n := c.Node().(type) {
		case *ast.FuncDecl:
			// A method that becomes a package function: its receiver becomes its first parameter.
			if n.Recv != nil && len(n.Recv.List) == 1 {
				if so := lookup(info.Defs[n.Name]); so != nil && so.convert {
					field := n.Recv.List[0]
					if len(field.Names) == 0 {
						problems = append(problems, "unnamed receiver: "+so.member)
						return true
					}
					n.Type.Params.List = append([]*ast.Field{field}, n.Type.Params.List...)
					n.Recv = nil
					changed = true
				}
			}
		case *ast.CallExpr:
			sel, ok := n.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			s := info.Selections[sel]
			if s == nil || s.Kind() != types.MethodVal {
				return true
			}
			so := lookup(s.Obj())
			if so == nil || !so.convert {
				return true
			}
			if len(s.Index()) > 1 {
				problems = append(problems, fmt.Sprintf("%s: %s called through an embedded field", rel(e.path),
					so.member))
				return true
			}
			calledConverted[sel] = true
			receiver := sel.X
			if _, isPointer := info.TypeOf(sel.X).(*types.Pointer); !isPointer {
				receiver = &ast.UnaryExpr{OpPos: sel.X.Pos(), Op: token.AND, X: sel.X}
			}
			nn := newNameOf(so)
			var fn ast.Expr = &ast.Ident{Name: nn, NamePos: sel.Pos()}
			if so.home != bareHome {
				fn = &ast.SelectorExpr{X: qual(so.home, sel.Pos(), nil), Sel: &ast.Ident{Name: nn, NamePos: sel.Pos()}}
			}
			n.Fun = fn
			n.Args = append([]ast.Expr{receiver}, n.Args...)
			changed = true
			return true
		case *ast.SelectorExpr:
			if s := info.Selections[n]; s != nil && !calledConverted[n] {
				if so := lookup(s.Obj()); so != nil && so.convert {
					problems = append(problems, fmt.Sprintf("%s: %s used as a value", rel(e.path), so.member))
				}
			}
			if x, ok := n.X.(*ast.Ident); ok {
				if pn, ok := info.Uses[x].(*types.PkgName); ok && pn.Imported().Path() == srcPath {
					so := lookup(info.Uses[n.Sel])
					if so == nil {
						return true
					}
					nn := newNameOf(so)
					if so.home == bareHome {
						c.Replace(&ast.Ident{Name: nn, NamePos: n.Pos()})
						changed = true
						return false
					}
					if so.home != plan.Source || nn != n.Sel.Name {
						changed = true
					}
					if so.home != plan.Source {
						qual(so.home, x.Pos(), x)
					}
					n.Sel.Name = nn
					return false
				}
			}
			// A spread method of another new package, called on this package's handle.
			if plan.Spread != nil && spreadHome != "" {
				sel := info.Selections[n]
				if sel != nil && sel.Kind() == types.MethodVal && typeName(sel.Recv()) == plan.Spread.Type {
					so := lookup(sel.Obj())
					if so != nil && so.home != home && so.home != plan.Spread.Base {
						if home == plan.Spread.Base || so.home == plan.Source {
							problems = append(problems, fmt.Sprintf("%s calls %s of %s",
								rel(e.path), so.member, so.home))
							return true
						}
						n.X = &ast.CallExpr{
							Fun: &ast.SelectorExpr{X: qual(so.home, n.Pos(), nil), Sel: &ast.Ident{Name: "On", NamePos: n.Pos()}}, Lparen: n.X.Pos(), Rparen: n.X.End(),
							Args: []ast.Expr{&ast.SelectorExpr{X: n.X, Sel: &ast.Ident{Name: plan.Spread.BaseType, NamePos: n.Pos()}}},
						}
						needs[so.home] = true
						changed = true
					}
				}
			}
		case *ast.Ident:
			if obj, ok := info.Defs[n]; ok && obj != nil && !isEmbedded(obj) {
				if so := lookup(obj); so != nil {
					if nn := newNameOf(so); nn != n.Name {
						n.Name = nn
						changed = true
					}
				}
				return true
			}
			obj := info.Uses[n]
			so := lookup(obj)
			if so == nil {
				return true
			}
			nn := newNameOf(so)
			if plan.Spread != nil && obj == spreadObj && spreadHome != "" {
				n.Name = spreadHome
				changed = true
				return true
			}
			if so.member == "" && inSource || so.member == "" && dirOf(e.path) == plan.Source {
				if so.home != bareHome {
					c.Replace(&ast.SelectorExpr{X: qual(so.home, n.Pos(), nil), Sel: &ast.Ident{Name: nn, NamePos: n.Pos()}})
					changed = true
					return false
				}
			}
			if nn != n.Name {
				n.Name = nn
				changed = true
			}
		}
		return true
	}, nil)
	newPath := filepath.Join(root, home, filepath.Base(e.path))
	if newPath != e.path {
		changed = true
		name := pkgName(home)
		if external {
			name += "_test"
		}
		e.file.Name.Name = name
	}
	if !changed {
		return
	}
	for dir := range needs {
		if dir == home && !external {
			continue
		}
		if shadowed[dir] {
			alias := pkgName(dir) + "pkg"
			for _, id := range quals[dir] {
				id.Name = alias
			}
			astutil.AddNamedImport(fset, e.file, alias, importPath(dir))
			fmt.Println("ALIAS", rel(e.path), alias)
			continue
		}
		astutil.AddImport(fset, e.file, importPath(dir))
	}
	// Drop the source import when nothing uses it any more, and a self import.
	if !usesImport(e.file, srcPath) {
		astutil.DeleteImport(fset, e.file, srcPath)
	}
	if !external {
		astutil.DeleteImport(fset, e.file, importPath(home))
	}
	if dryRun {
		return
	}
	var buf bytes.Buffer
	if err := format.Node(&buf, fset, e.file); err != nil {
		fatalf("%s: %v", e.path, err)
	}
	out, err := format.Source(buf.Bytes())
	if err != nil {
		out = buf.Bytes()
		fmt.Println("WARNING: not reformatted:", rel(newPath), err)
	}
	out = alignDocComments(out)
	check(os.MkdirAll(filepath.Dir(newPath), 0o755))
	check(os.WriteFile(newPath, out, 0o644))
	if newPath != e.path {
		check(os.Remove(e.path))
		fmt.Println("moved", rel(e.path), "->", rel(newPath))
	}
}

// usesImport reports whether a file still names the package an import path imports.
func usesImport(f *ast.File, path string) bool {
	local := ""
	for _, spec := range f.Imports {
		p, _ := strconv.Unquote(spec.Path.Value)
		if p != path {
			continue
		}
		if spec.Name != nil {
			local = spec.Name.Name
		} else {
			local = filepath.Base(path)
		}
	}
	if local == "" {
		return false
	}
	if local == "_" || local == "." {
		return true
	}
	used := false
	ast.Inspect(f, func(n ast.Node) bool {
		if s, ok := n.(*ast.SelectorExpr); ok {
			if x, ok := s.X.(*ast.Ident); ok && x.Name == local && x.Obj == nil {
				used = true
			}
		}
		return !used
	})
	return used
}

// importEdges are, per new home, the homes it references, with one example.
var importEdges = map[string]map[string]string{}

// checkCycles reports an import cycle among the new homes.
func checkCycles() {
	state := map[string]int{}
	var stack []string
	var visit func(string)
	visit = func(n string) {
		state[n] = 1
		stack = append(stack, n)
		var next []string
		for m := range importEdges[n] {
			next = append(next, m)
		}
		sort.Strings(next)
		for _, m := range next {
			if state[m] == 1 {
				var cycle []string
				for i := len(stack) - 1; i >= 0; i-- {
					cycle = append([]string{stack[i]}, cycle...)
					if stack[i] == m {
						break
					}
				}
				var why []string
				for i, a := range cycle {
					b := cycle[(i+1)%len(cycle)]
					why = append(why, importEdges[a][b])
				}
				problems = append(problems, "CYCLE "+strings.Join(cycle, " -> ")+": "+strings.Join(why, "; "))
				continue
			}
			if state[m] == 0 {
				visit(m)
			}
		}
		stack = stack[:len(stack)-1]
		state[n] = 2
	}
	var nodes []string
	for n := range importEdges {
		nodes = append(nodes, n)
	}
	sort.Strings(nodes)
	for _, n := range nodes {
		if state[n] == 0 {
			visit(n)
		}
	}
}

// isEmbedded reports an embedded field, whose identifier is also a use of the type it embeds.
func isEmbedded(obj types.Object) bool {
	v, ok := obj.(*types.Var)
	return ok && v.Embedded()
}

// spreadMember reports a method or field of the spread type.
func spreadMember(so *srcObj) bool {
	return plan.Spread != nil && strings.HasPrefix(so.member, plan.Spread.Type+".")
}

// isAbove reports a new package that sits above the source package.
func isAbove(dir string) bool {
	for _, a := range plan.Above {
		if a == dir {
			return true
		}
	}
	return false
}

// fatalf reports a failure and ends the command.
func fatalf(format string, arguments ...any) {
	fmt.Fprintf(os.Stderr, "split: "+format+"\n", arguments...)
	os.Exit(1)
}
