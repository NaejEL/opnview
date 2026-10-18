// Package hello is the worked example of docs/extending.md, "Adding a surface": one JSON route,
// /api/hello, that answers how many accounts the installation has. It is a complete surface -- a
// marker, a catalogue file and a handler -- and it is exercised by internal/web/modularity_test.go,
// which imports it for its effect exactly as internal/web/surfaces imports the shipped ones. It
// lives here, and not below internal/web/surfaces, so that it is tested but never linked into the
// product.
package hello
