// Command movedecl moves top-level declarations, with their doc comments, from one file to the end
// of another (created with the source's package clause when absent), and fixes both files' imports.
// It prepares a split, by gathering what one new package takes into the files the plan names.
//
// Usage, inside the development container:
//
//	go run ./cmd/movedecl SRC DST Name [Type.Method ...]
package main
