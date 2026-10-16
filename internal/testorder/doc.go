// Package testorder starts a package's longest tests first.
//
// go test starts a package's tests in source order: its test files sorted by name, and the
// functions of each file in the order they are declared. A parallel test is released, once every
// top-level test has reached its t.Parallel, in about that same order, so a test that takes many
// times longer than its neighbours but sits in a file late in the alphabet starts last and decides
// the package's wall time on its own. Measured on 16 October 2026 on 16 CPUs, internal/collect's
// longest test, 16 s alone, started 7.7 s into a 24 s run.
//
// StartFirst moves the tests a package names to the front of that order, from the package's
// TestMain, before m.Run. Nothing about a test changes but when it starts: no file is renamed,
// no test is renamed or merged, and -run selects exactly what it selected before.
//
// The testing package offers no exported way to order tests, so StartFirst reorders the slice
// testing.M holds them in, through reflection. Every assumption that takes is checked, and a
// mismatch -- the field renamed or retyped by a later Go release, or a name the package does not
// declare -- is an error the package's TestMain reports by failing, never a silent no-op.
package testorder
