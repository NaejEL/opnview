// Command split splits one Go package of a module into several, rewriting every reference in the
// module. It is the tool Phase D's layout moves were made with, kept so the next split is made the
// same way rather than by hand.
//
// It moves the files a plan assigns to new packages, qualifies every reference that now crosses a
// package boundary, exports what a split forces (printing each as an EXPORT line), and rewrites the
// methods of one "spread" type -- a type whose methods were spread over the moved files -- onto one
// handle type per new package, each embedding a base handle.
//
// Usage, from ci/mover inside the development container, -root naming the module being split:
//
//	go run ./cmd/split -root ../.. -plan plans/example.json [-dry]
//
// The plan is JSON; plans/example.json is the one example kept. Its fields:
//
//   - module: the module path; source: the directory of the package being split;
//   - packages: per new directory, its package name and the files of the source it takes;
//   - names: an exported name chosen for an identifier, keyed "Name" or "Type.Name";
//   - functions: types whose methods, where they leave the type's package, become functions;
//   - above: new packages that sit above the source package and may import it;
//   - spread: the spread type, its base package and handle types (optional).
//
// The rewrite runs dry first: anything it would get wrong -- an import cycle between the new
// packages, a name collision, a reference that would point up -- is printed as a PROBLEM line and
// nothing is written. Files move by rename, so git sees each as a move and history is preserved.
package main
