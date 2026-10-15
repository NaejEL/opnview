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
