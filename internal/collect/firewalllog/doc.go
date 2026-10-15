// Package firewalllog is the firewall_log kind: a source of records of packets the firewall
// allowed or denied.
//
// WHAT THE KIND PRODUCES. A page of Records, newest first, each decoded and normalised by its
// implementation and not yet joined to anything: the timestamp is already a UTC epoch and the
// action and direction are already the schema's vocabulary, because both are the implementation's
// business -- the shapes a timestamp arrives in and the words a product uses for an action are
// facts about that product. Resolving the device and the rule, composing a client identity and
// detecting a gap are the kind's business, written once in internal/collect (firewalllog.go).
//
// WHAT AN IMPLEMENTATION IS. A directory internal/collect/firewalllog/<product>/ holding a
// provider.json whose kind is "firewall_log", a Go package whose init() calls Register with the
// embedded manifest and a Source, its fixtures and its tests. See docs/extending.md for a worked
// example. An implementation reports; it never writes, never reads the store and never chooses
// whether it is the active one.
//
// THERE IS NO SERVER-SIDE RESUME POINT for this kind on OPNsense: the filter log's `digest` is a
// de-duplication key and not a cursor. The coordinator therefore pulls the most recent page of
// the size the operator configured and declares a gap when the oldest record returned is newer
// than the newest record stored.
package firewalllog
