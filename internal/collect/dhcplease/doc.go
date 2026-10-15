// Package dhcplease is the dhcp_lease kind: a source of the lease table of a DHCP server.
//
// WHAT THE KIND PRODUCES. A page of Observations, each one lease generation, with the field-name
// differences between backends already normalised away by the implementation that read it. What
// is true of any lease source is here or in internal/collect (dhcplease.go): the identity
// cascade, resolving the interface the backend named, reading one page of the standard envelope
// (ReadLeaseRows), normalising the field names (DecodeLease), and writing one row per lease
// generation.
//
// THIS IS THE KIND THAT MADE THE SEAM NECESSARY RATHER THAN NOTIONAL. Kea, Dnsmasq and the
// end-of-life ISC plugin are implementations of it; on the firewall the survey probed, Dnsmasq
// serves DHCP and Kea is disabled, and somebody else's firewall is the other way round. The kind
// admits several concurrently active providers: a lease is keyed by (address, generation_key,
// provider_id).
//
// THE VALIDITY START IS REPORTED BY KEA AND BY NO OTHER BACKEND, and the model says so instead of
// substituting: StartsAt is a pointer, nil meaning "this backend cannot say". What makes a
// re-poll idempotent is dhcp_lease.generation_key, composed by store.GenerationKeyOf.
//
// WHAT AN IMPLEMENTATION IS. A directory internal/collect/dhcplease/<product>/ holding a
// provider.json whose kind is "dhcp_lease", a Go package whose init() calls Register with the
// embedded manifest and a Source, its fixtures and its tests. An implementation that can never be
// read returns provider.ErrUnsupportedRead from Leases and says why. See docs/extending.md for a
// complete worked example.
package dhcplease
