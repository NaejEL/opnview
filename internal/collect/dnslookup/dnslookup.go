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
// returns provider.ErrUnsupportedRead with the reason. See docs/extending.md.
package dnslookup

import (
	"context"

	"github.com/NaejEL/opnview/internal/collect/provider"
	"github.com/NaejEL/opnview/internal/decode"
)

// Kind is the registry kind this package answers for.
const Kind = "dns_lookup"

// Record is one resolver lookup, decoded and normalised by its implementation.
type Record struct {
	// DeduplicationKey is composed by the implementation, because what can serve as a key
	// is a fact about the source. Unbound's own row identifier is null on every row a real
	// firewall returns, so its key is composed from the row's content.
	DeduplicationKey string
	ClientAddress    string
	// ClientFamily is the family of the querying address, 4 or 6, where the source reports
	// one -- even when ClientAddress is the host name its reverse lookup returned; nil
	// otherwise. It is what decides a host name the local data holds under one IPv4 and
	// one IPv6 address (store.ResolveLoggedHostname).
	ClientFamily *int64
	Domain       string
	Action       string
	AnswerSource *string
	Rcode        *string
	DNSSECStatus *string
	// BlocklistName is empty when the source named no list, which for a blocked lookup is
	// its own state rather than an absence.
	BlocklistName string
	LookedUpAt    int64
}

// Source is one implementation of the dns_lookup kind.
type Source interface {
	// Probe reports what the firewall says about this implementation.
	Probe(ctx context.Context, host provider.Session) (provider.Result, error)
	// Lookups returns one page, 1-based, or provider.ErrUnsupportedRead.
	Lookups(ctx context.Context, host provider.Session, page int) ([]Record, provider.Result, error)
	// PageBound is how many pages one pass may walk. It is the implementation's to state:
	// Unbound's endpoint caps its buffer, so walking further re-reads what was already seen.
	PageBound() int
	// RequestedSpanSeconds is the window the implementation asks for. Whether the source
	// honours it is a separate matter, and the kind never presents the answer as coverage
	// of it.
	RequestedSpanSeconds() int64
}

// NormaliseAction maps a resolver's action onto the closed vocabulary the schema constrains.
// Anything else is `unknown`: a lookup whose verdict opnview cannot name must not be counted as
// allowed.
func NormaliseAction(raw string) string {
	switch decode.LowerASCII(raw) {
	case "pass", "passed", "allowed":
		return "pass"
	case "block", "blocked":
		return "block"
	case "drop", "dropped":
		return "drop"
	default:
		return "unknown"
	}
}
