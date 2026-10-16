// Command rename renames identifiers across a module, by their type-checked identity rather than
// their spelling: a package-level object, a method or field of a named type, or a local, and every
// use of it in every package and test of the module. It is how a name a split exported is made
// unexported again once its last caller outside the package is gone, and how a local that shadows
// a package name is renamed so the package needs no import alias.
//
// Usage, from ci/mover inside the development container, -root naming the module:
//
//	go run ./cmd/rename -root ../.. DIR:Name=new DIR:Type.Member=new @FILE:LINE:name=new ...
//
// With -qualify NAME -home FILE, a renamed package-level identifier is written NAME.New wherever it
// is used outside FILE: the declarations are about to move, with FILE, into package NAME.
//
// DIR is the package's directory relative to the root; @FILE:LINE:name names a local by the line
// that declares it, FILE relative to the root. An embedded field is named by its type and
// is refused: rename the type. The result is not reformatted; gofmt -w realigns what moved, and the
// compiler judges a collision.
package main
