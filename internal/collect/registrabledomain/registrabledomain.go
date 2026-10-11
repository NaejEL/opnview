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

import (
	"context"

	"github.com/NaejEL/opnview/internal/collect/provider"
)

// Kind is the registry kind this package answers for.
const Kind = "registrable_domain"

// List is what an implementation holds.
type List struct {
	Ready       bool
	Version     string
	Rules       int
	ConfirmedAt int64
	// Failed says the refresh that reported this failed, which the kind records as a gap.
	Failed bool
}

// Source is one implementation of the registrable_domain kind.
type Source interface {
	// Status reports the list held, from what is on disk, without acquiring anything.
	Status(ctx context.Context, host provider.Session) (List, provider.Result, error)
	// Refresh acquires or updates the list.
	Refresh(ctx context.Context, host provider.Session) (List, provider.Result, error)
	// Group returns each name's registrable domain; a name with none is absent from the map.
	Group(ctx context.Context, host provider.Session, names []string) (map[string]string, error)
}

// The built-in implementation's registry key and the probe words it records, which the settings
// surface draws its Public Suffix List card from until the step-7 application replaces that page.
const (
	BuiltinKey       = "public_suffix_list"
	ProbeDownloaded  = "list_downloaded"
	ProbeNotModified = "list_not_modified"
	ProbeFailed      = "download_failed"
	ProbeMalformed   = "download_malformed"
)
