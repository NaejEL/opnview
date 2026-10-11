// Package provider is the contract every provider implementation is written against, and the
// host that runs one.
//
// THERE IS NOT ONE COLLECTOR PER SOURCE. THERE ARE KINDS, AND SEVERAL OF THEM ALREADY HAVE
// MORE THAN ONE IMPLEMENTATION. The schema's registry says so: `dhcp_lease` holds Kea, Dnsmasq
// and the end-of-life ISC plugin, and `dns_lookup` holds Unbound and Dnsmasq. On the firewall the
// survey probed, Dnsmasq serves DHCP and Kea is disabled, while Unbound AND Dnsmasq are both
// running as resolvers. Somebody else runs Kea. So the seam is the kind, and it is required by
// data that exists today rather than by a step nobody has specified.
//
// THE LAYOUT IS THE SEAM:
//
//	internal/collect/provider                  this package: what every kind shares
//	internal/collect/<kind>/                   one KIND: its contract (doc.go), its record
//	                                           types, its Source interface and its registry
//	internal/collect/<kind>/<product>/         one IMPLEMENTATION: provider.json, its code,
//	                                           its fixtures (testdata/) and its tests
//
// Adding an implementation is adding its directory. Nothing selects an implementation at
// compile time, nothing branches on a product name outside that product's own directory, and
// detection chooses among them at runtime from the firewall's own configuration. The one file
// that links the directories into the binary, internal/collect/providers_gen.go, is generated
// from the tree by `go generate` and checked by a test, never edited (internal/discover).
//
// WHAT AN IMPLEMENTATION IS GIVEN, AND WHAT IT RETURNS. The contract has two directions. The
// kind's Source interface says what a connector must PRODUCE: the records it yields, the Result
// it reports, the questions the kind may ask it. Session, in this package, says what it may
// CONSUME to produce it, and it is deliberately the shorter list: what the port withholds -- the
// store, the discovery snapshot, availability, activation -- is what makes the seam a boundary
// rather than a naming convention.
//
// WHEN CONNECTORS MOVE OUT OF PROCESS THIS IS THE PROTOCOL. Each Session method becomes a request
// the connector sends to its host and each Source method becomes one the host sends to the
// connector. That is why the list is short and why it stays short: a method added here is a verb
// added to a protocol, and the cost of removing one later is a compatibility break rather than a
// rename. How an implementation announces itself (provider.json and the kind's Register) is the
// transport and is in-process today; the contract beside it should not have to move for that.
//
// WHY THERE IS NO SOURCE FOR THE geo_asn AND public_suffix KINDS HERE. Their one implementation
// each is an acquisition -- the MaxMind dataset, the Public Suffix List -- that internal/maxmind
// and internal/publicsuffix download and read; neither is a firewall source, and the port below,
// whose only outbound capability is the firewall, could not serve it. They still DECLARE
// themselves, with a Manifest, so their registry rows are created the same way.
package provider
