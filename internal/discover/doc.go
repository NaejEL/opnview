// Package discover generates, and checks, the one file that links a family of
// implementations into the binary.
//
// WHY A GENERATED FILE EXISTS AT ALL. Go links packages, not directories: an implementation
// directory that nothing imports is not in the binary, whatever it contains. The modular rule of
// CLAUDE.md -- adding an implementation is adding a directory and touching nothing else -- is met
// by making that one import file a build product instead of a hand-kept list:
//
//   - a family of implementations is every directory below a root that holds a marker file
//     (surface.json for a web surface, the one family the core still links this way);
//   - `go generate` walks the tree and writes one file of blank imports, sorted, headed "Code
//     generated ... DO NOT EDIT.";
//   - a test in the consuming package re-renders the file in memory and fails when the file on
//     disk differs, so a directory added without regenerating is a red test that names the
//     command to run, and a hand edit is the same red test.
//
// The generation parameters are written ONCE, in the //go:generate directive of the consuming
// package, and the staleness test reads them from there (CheckDirective), so the directive and
// the test cannot drift apart.
package discover
