// Package publicsuffix is the third and last of the three outbound calls opnview
// makes: the Public Suffix List, downloaded from publicsuffix.org, kept on disk,
// refreshed once a day, and used to group the site names opnview inferred by their
// registrable domain.
//
// IT IS ONE OF EXACTLY THREE PACKAGES ALLOWED TO BUILD AN HTTP REQUEST; the firewall
// client in internal/opnsense and the MaxMind download in internal/maxmind are the
// other two, and a source-policy test in internal/opnsense holds the three. Nothing
// here contacts any host but publicsuffix.org, redirects included, and grouping a
// name contacts nothing: it reads the list held in memory.
//
// THE LIST IS DATA OPNVIEW READS AND NEVER WRITES INTO A NAME. A site name is stored
// as the resolver reported it, and its registrable domain is computed at read time;
// no stored site_name is ever altered by it.
//
// See specs/SPEC-correlation-classification-aggregation.md, scope A8.
package publicsuffix

// The location and the documents, read on 3 October 2026.
const (
	// listHost is the only host this package contacts.
	listHost = "publicsuffix.org"
	// listURL is the canonical location of the list. The list's own header says:
	// "Please pull this list from, and only from
	// https://publicsuffix.org/list/public_suffix_list.dat", and the project's page
	// asks an application to download it "no more than once per day". The server
	// answers with an ETag and a Last-Modified, and answers a conditional request
	// whose copy is current with 304 and no body.
	listURL = "https://publicsuffix.org/list/public_suffix_list.dat"
	// documentation is the project's page for the list, which carries the download
	// guidance above.
	documentation = "https://publicsuffix.org/list/"
	// formatDocumentation is the list's format and its formal algorithm, which this
	// package implements; the project's page links to it.
	formatDocumentation = "https://github.com/publicsuffix/list/wiki/Format"
	// licence is the licence the list is published under, as its own header states:
	// the Mozilla Public License, version 2.0. opnview downloads and reads the file and
	// never modifies or redistributes it.
	licence = "https://mozilla.org/MPL/2.0/"
)

// The registry row this package implements.
const (
	// Kind is the registry kind.
	Kind = "public_suffix"
	// ProviderKey is the registry row of the Public Suffix List.
	ProviderKey = "public_suffix_list"
)
