// Command mkdoc gives every Go package under the roots named a doc.go holding its package comment:
// a package comment found on another file moves there, and a package with none is listed as
// MISSING. Without -write it only reports.
//
// Usage, inside the development container:
//
//	go run ./cmd/mkdoc -write|-list ROOT...
package main
