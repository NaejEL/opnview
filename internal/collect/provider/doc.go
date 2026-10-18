// Package provider is the contract every provider implementation is written against, and the
// host that runs one.
//
// THERE IS NOT ONE COLLECTOR PER SOURCE. THERE ARE KINDS, AND SEVERAL OF THEM ALREADY HAVE
// MORE THAN ONE IMPLEMENTATION. `dhcp_lease` holds Kea, Dnsmasq and the end-of-life ISC plugin, and
// `dns_lookup` holds Unbound and Dnsmasq. On the firewall the survey probed, Dnsmasq serves DHCP and
// Kea is disabled, while Unbound AND Dnsmasq are both running as resolvers. Somebody else runs Kea.
// So the seam is the kind, and it is required by data that exists today.
//
// THE LAYOUT IS THE SEAM, AND A KIND IS DATA (PA2 and PA14 of the plugin-API amendment to
// specs/SPEC-backend-completion-plugins.md):
//
//	internal/collect/provider/kinds/<kind>.json   one KIND: its contract and its attributes
//	plugins/<plugin>/                             one IMPLEMENTATION: its manifest, its script, its
//	                                              recorded answers and its tests
//	internal/collect/kinds/<coordinator>/         what the core makes of a kind's records
//
// Adding a core kind's contract is adding a file, and collecting a core kind is adding its coordinator
// directory, which registers its pass; adding an implementation is adding a plugin directory. An
// implementation is called by function name and answers records checked once against its kind's
// contract (Implementation, Answer); every implementation of every kind is held in one Set. Nothing
// selects an implementation at compile time, nothing branches on a product name, and detection
// chooses among them at runtime from the firewall's own configuration.
//
// WHAT AN IMPLEMENTATION IS GIVEN, AND WHAT IT RETURNS. The contract has two directions. The kind's
// file says what a connector must PRODUCE: the records it yields, the result it reports, the
// functions the kind may call. Session, in this package, says what it may CONSUME to produce it, and
// it is deliberately the shorter list: what the port withholds -- the store, the discovery snapshot,
// availability, activation -- is what makes the seam a boundary rather than a naming convention.
//
// WHEN CONNECTORS MOVE OUT OF PROCESS THIS IS THE PROTOCOL. Each Session method becomes a request the
// connector sends to its host and each function of a kind's contract one the host sends to the
// connector. That is why the list is short and why it stays short.
package provider
