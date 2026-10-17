// Package dnslookup is the dns_lookup kind: a source of the lookups a resolver answered, per
// querying client.
//
// WHAT THE KIND PRODUCES. A page of Records, 1-based, each decoded and normalised by its
// implementation. What is true of any source of resolver lookups is written once in
// internal/collect (dnslookup.go): resolving the querying machine, resolving the list that
// refused a lookup, walking the pages, and detecting a gap. What is a fact about a product is the
// implementation's: which resolver, how its window behaves (PageBound, RequestedSpanSeconds), and
// what can serve as a de-duplication key (Record.DeduplicationKey).
//
// THE KIND NEVER PRESENTS WHAT IT READ AS COVERAGE OF A WINDOW. The one implementation that can
// be read is a ring buffer of the most recent lookups whose time bounds are ignored outright,
// measured on a live firewall, so nothing returns, stores or reports a covered interval: the
// availability detail says in words what was read and what it is not.
//
// WHAT AN IMPLEMENTATION IS. A directory internal/collect/dnslookup/<product>/ holding a
// provider.json whose kind is "dns_lookup", a Go package whose init() calls Register with the
// embedded manifest and a Source, its fixtures and its tests. A resolver whose lookups cannot be
// read (Dnsmasq: free-text log under an unrecorded grammar) is still registered and probed, and
// returns provider.ErrUnsupportedRead with the reason. See docs/plugins.md.
package dnslookup
