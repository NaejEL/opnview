// Package registrabledomain is the registrable_domain kind: a list that gives a site name its
// registrable domain, so the widgets can group names at read time. "Registrable domain" is the
// Public Suffix List's and the URL Standard's term; the built-in implementation is the publicsuffix
// plugin.
//
// WHAT THE KIND PRODUCES. A list's state -- held or not, its version, its rule count, when a refresh
// last confirmed it -- and, for a batch of names, each name's registrable domain. What is true of
// any such list is written once in internal/collect (services.go): one active provider at a time,
// chosen by the operator's selection and defaulting to the built-in; a failed refresh recorded as a
// collection gap; the widget-side grouping, which never alters a stored name and degrades to the
// named state registrable_domain_grouping_unavailable rather than blocking a response.
package registrabledomain
