// Package resolvercache is the resolver_cache kind: a source of the records a resolver holds in
// its cache, with the seconds each has left. "Cache" is Unbound's own word (unbound-control(8),
// dump_cache).
//
// WHAT THE KIND PRODUCES. One recorddump.Dump per read: every record the cache holds now. What is
// true of any resolver's cache is written once in internal/collect (resolvercache.go and
// resourcerecord.go): the A, AAAA and CNAME records are stored as observations whose coverage
// interval a later poll extends, every other type is counted, and the flows whose attribution the
// new records can change are decided again.
//
// THE CACHE READ AND THE QUERY REPORT ARE TWO KINDS WITH TWO AVAILABILITY ROWS, and neither ever
// writes the other's. The kind admits several active providers, since every record it stores
// names the provider that read it.
//
// WHAT AN IMPLEMENTATION IS. A directory internal/collect/resolvercache/<product>/ holding a
// provider.json whose kind is "resolver_cache", a Go package whose init() calls Register with the
// embedded manifest and a Source, its fixtures and its tests. A resolver with no cache read
// (Dnsmasq) is registered and probed and returns provider.ErrUnsupportedRead with the reason.
package resolvercache
