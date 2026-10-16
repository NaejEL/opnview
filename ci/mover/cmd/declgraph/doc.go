// Command declgraph prints, for one package, each top-level declaration (Name, or Recv.Method) with
// its file and the other top-level declarations of the package it references. It is how a split is
// planned: a group of declarations that reference nothing outside themselves is a package.
//
// Usage, inside the development container:
//
//	go run ./cmd/declgraph DIR
package main
