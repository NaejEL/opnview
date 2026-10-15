// Package collect reads the five sources, decides which implementation of each
// kind opnview reads, records what it could not read, and writes the result.
//
// THE DIRECTORY TREE IS THE SEAM, AND THE LISTING IS THE DOCUMENTATION. There is not one
// collector per source here: there are KINDS, and several of them already have more than one
// implementation. So every directory below this one is named for exactly one of these things,
// or for a part of the collector it holds (below), and `ls` is enough to tell which:
//
//	<kind>/                  one KIND'S CONTRACT: its doc.go, its record types, its Source
//	                         interface and its registry (firewalllog, securityevent, dhcplease,
//	                         dnslookup, resolvercache, resolverlocaldata, measurement)
//	<kind>/<product>/        one IMPLEMENTATION of that kind: provider.json, its code, its
//	                         fixtures and its tests, holding only facts about that product
//	provider/                the contract every kind shares: the session port, the probe result,
//	                         the manifest and the registry primitive
//	kinds/<kind>/            one kind's COORDINATOR: its cursor, its gap handling, everything
//	                         true of any implementation of it, which needs the store and the
//	                         discovery snapshot an implementation is never handed
//
// The machinery that genuinely spans the kinds is in the parts named for it: the state every
// part shares (core/), the derivation every pass ends with (derive/), runtime discovery
// (discovery/), the identity cascade (identity/) and the service kinds (services/). What stays in
// this package is the Collector that composes them, the probe round, the purge's ordering and the
// scheduler's task list.
//
// The rule that layout has to keep passing -- ROADMAP.md, "Rules that apply to every step":
// ADDING AN IMPLEMENTATION IS ADDING A DIRECTORY, and touches nothing else. A Kea equivalent for
// another firewall is `dhcplease/<product>/` with a provider.json; no file here changes, because
// nothing selects an implementation at compile time, nothing branches on a product name outside
// that product's own directory, and the one file linking the directories into the binary
// (providers_gen.go) is generated from the tree. See docs/extending.md.
//
// Three further properties are worth stating before the code, because each one is
// the answer to something measured rather than a style choice.
//
// AN EMPTY SUCCESS IS A CLAIM, AND A CLAIM IS CHECKED. Every source returns an
// empty list both when it is disabled and when it is quiet, so nothing here ever
// concludes "no data" from an empty answer. What it concludes is recorded in
// source_availability, with the probe that determined it, and when a window was
// genuinely missed it is recorded in collection_gap.
//
// NOTHING IS WRITTEN TO THE FIREWALL. internal/opnsense refuses a mutating
// command before a request exists, and where a firewall setting has to be turned
// on for a source to exist — the resolver query log, the Suricata event types —
// opnview reads the flag and reports it. It never sets it.
//
// NOTHING IS CLASSIFIED BY ITS NAME. No interface identifier, description, VLAN
// name, address or CIDR is a literal here, and no branch reads the TEXT of a
// name, a description or a label. Where a classification cannot be made from a
// field whose meaning the survey establishes, it is left unclassified and said
// so, rather than guessed from a token.
package collect
