// Package sizing turns a measured record rate into a suggested pair — a page
// size and a poll interval — for each paged read.
//
// A PAGE AND AN INTERVAL ARE ONE DECISION. A poll loses records when its source
// writes more between two polls than one page holds, and the API offers no way
// to recover them. So the question this package answers is not "how often" or
// "how much" but the two together: given the busiest minute this installation
// has actually produced, which pair keeps a page ahead of it with room to spare.
//
// The input is a store.RecordRateMeasurement, taken on this installation's own
// rows; nothing here is a figure from somebody else's network. The output is a
// suggestion the operator may apply or ignore on the collection surface: the
// package writes nothing and decides nothing.
//
// Rebuilt after the loss of 2 October 2026 from the compiled package of that
// evening: the declarations, the registry and the derivation are the compiled
// ones; the comments are rewritten.
package sizing
