// Package suffixlist reads a file in the Public Suffix List format and computes a name's
// registrable domain by the list's own formal algorithm (github.com/publicsuffix/list/wiki/Format).
//
// IT IS A FILE FORMAT, NOT A SERVICE. Nothing here downloads anything or names a host: the
// registrable_domain plugin that ships with opnview (plugins/publicsuffix) acquires the list through
// the plugin host's fetch facility and opens its copy with this package, as a host helper
// (internal/plugin/fetch), the way an MMDB file is opened with the MaxMind DB reader. It is in the
// core because parsing and matching thousands of rules in the plugin interpreter on every widget
// request would make every grouping slow, and because the format is the list's own published one,
// which any other source of suffix rules can use too.
//
// THE LIST IS DATA OPNVIEW READS AND NEVER WRITES INTO A NAME. A site name is stored as the
// resolver reported it, and its registrable domain is computed at read time; GroupSites never alters
// a stored site name.
package suffixlist
