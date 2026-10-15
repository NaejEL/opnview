// Package sources is a set of provider implementations: every kind's implementations, by
// provider key, as one installation has them.
//
// A SET, NOT A PROCESS-WIDE REGISTRY. The plugin host (internal/plugin) loads the built-in plugins
// and the plugins of one data directory into a set, and a collector reads that set and nothing
// else. Two collectors over two data directories -- as the tests build them, each with plugins
// of its own dropped into a temporary directory -- therefore never see each other's plugins, and
// the binary has no list of implementations anywhere: what a set holds is what was found.
//
// The set records, beside the implementations, the registry rows they declare (Declared), the
// kinds the plugins declared for themselves (DeclaredKinds), and the refusals met while it was
// filled, which the probe round reports.
package sources
