// Package ipgeolocation is the ip_geolocation kind: a dataset that places public addresses --
// their country, coordinates, autonomous system and operator. "IP geolocation" is MaxMind's own term
// for its GeoLite2 products; the built-in implementation is the maxmind plugin, and a third party
// may drop another (DB-IP, IPinfo: both publish the MMDB format the plugin host reads).
//
// WHAT THE KIND PRODUCES. A dataset's state -- held or not, and its build -- and, for a batch of
// public addresses, what the dataset says of each. What is true of any such dataset is written once
// in internal/collect (services.go): which addresses need placing, never a non-public one; at most
// 20 000 per pass; the answers stored in geo_asn against the provider and the build; every address
// placed again when the active provider or its build changes; one active provider at a time, chosen
// by the operator's selection and defaulting to the built-in.
package ipgeolocation

import (
	"context"

	"github.com/NaejEL/opnview/internal/collect/provider"
)

// Kind is the registry kind this package answers for.
const Kind = "ip_geolocation"

// Dataset is what an implementation holds: whether it answers lookups, and the build it answers
// from.
type Dataset struct {
	Ready bool
	Build int64
}

// Answer is what a dataset says about one address. A field is nil when the dataset gave no value;
// no country is a miss.
type Answer struct {
	Address     string
	CountryCode *string
	CountryName *string
	Latitude    *float64
	Longitude   *float64
	ASN         *int64
	Operator    *string
}

// Source is one implementation of the ip_geolocation kind.
type Source interface {
	// Status reports the dataset held, from what is on disk, without acquiring anything.
	Status(ctx context.Context, host provider.Session) (Dataset, provider.Result, error)
	// Refresh acquires or updates the dataset.
	Refresh(ctx context.Context, host provider.Session) (Dataset, provider.Result, error)
	// Lookup places a batch of public addresses, returning the build the answers come from.
	Lookup(ctx context.Context, host provider.Session, addresses []string) (int64, []Answer, error)
}

// The built-in implementation's registry key and the probe words it records, which the settings
// surface draws its MaxMind card from until the step-7 application replaces that page.
const (
	BuiltinKey                   = "maxmind_geolite2"
	ProbeCurrent                 = "dataset_current"
	ProbeNoLicenceKey            = "no_licence_key"
	ProbeLicenceKeyUndecryptable = "licence_key_undecryptable"
	ProbeNoAccountID             = "no_account_id"
	ProbeRefused                 = "download_refused"
	ProbeLimited                 = "download_limit_reached"
	ProbeFailed                  = "download_failed"
)
