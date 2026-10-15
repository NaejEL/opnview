// Package buildinfo carries the few facts the program knows about itself.
//
// It exists so that `go build ./...` and `go test ./...` compile and exercise
// real code from the very first cycle, rather than matching no package at all.
// It makes no decision belonging to a later step: no data model, no storage,
// no OPNsense client.
package buildinfo
