// Command externalize turns a package's internal test files (package p) into external ones
// (package p_test): every use of one of the package's own identifiers is qualified, p.Name, and
// the package is imported. An external test may import what imports the package, so its shared
// fixtures can live in an importable package of their own, and a part's tests can move beside the
// part. What an external test cannot do -- use an unexported identifier of the package, set an
// unexported field, declare a method of a package type -- is printed as a PROBLEM line, and
// nothing is written until there is none.
//
// Usage, from ci/mover inside the development container, -root naming the module:
//
//	go run ./cmd/externalize -root ../.. [-dry] [-keep FILE,...] PACKAGE_DIR
//
// A kept file stays an internal test, beside the external ones.
package main
