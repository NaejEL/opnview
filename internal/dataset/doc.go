// Package dataset is the data catalogue (P8 of specs/SPEC-backend-completion-plugins.md): every
// dataset the core and every loaded plugin expose, each declared as data, and the compiler that
// turns a query against one into a single parameterised SELECT.
//
// A DATASET IS A DECLARATION. Its columns, each with a type, a role (time, dimension or measure),
// a unit, whether it is nullable and whether it carries a domain name, and the aggregations it
// admits. The core's datasets are directories under core/, discovered at start (core.go); every
// table a loaded plugin declares is a dataset too, declared by the plugin's manifest. Adding a
// dataset is adding a directory, and nothing else.
//
// NO SQL TEXT COMES FROM THE BROWSER. A query names a dataset, columns, aggregations, operators and
// values; the compiler takes every identifier from the dataset's declaration, quoted, and binds
// every value. Anything a query names that the declaration does not hold is refused with a closed
// code before any statement is built (query.go, compile.go).
package dataset
