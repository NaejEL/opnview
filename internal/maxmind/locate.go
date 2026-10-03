package maxmind

import (
	"context"
	"net/netip"
	"time"

	"github.com/NaejEL/opnview/internal/collect"
	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/store"
)

// Locator places the addresses the stored flows carry, against the dataset on disk.
// It contacts nothing.
type Locator struct {
	dataset *Dataset
	store   *store.Store
	now     func() time.Time
}

// NewLocator returns a locator.
func NewLocator(dataset *Dataset, database *store.Store, now func() time.Time) *Locator {
	return &Locator{dataset: dataset, store: database, now: now}
}

// maxLookupsPerPass bounds one pass, so a first pass over a large history does not
// hold the database for minutes; the next pass carries on where it stopped.
const maxLookupsPerPass = 20000

// Locate looks up every public address with no answer from the build on disk.
//
// WITH NO DATASET, NOTHING IS WRITTEN: an unreachable dataset produces neither
// resolved rows nor misses. An address that is not public — private, loopback,
// link-local, multicast, unspecified — is never looked up and never written, because
// no dataset places it and asking would record a miss that says nothing.
func (l *Locator) Locate(ctx context.Context) error {
	build, ready := l.dataset.Ready()
	if !ready {
		return nil
	}
	providerID, err := l.store.ProviderID(ctx, Kind, ProviderKey)
	if err != nil {
		return err
	}
	addresses, err := l.store.AddressesToGeolocate(ctx, build)
	if err != nil {
		return err
	}
	looked := 0
	for _, text := range addresses {
		if looked >= maxLookupsPerPass || ctx.Err() != nil {
			break
		}
		address, err := netip.ParseAddr(text)
		if err != nil || !isPublic(address) {
			continue
		}
		looked++
		answer, ok, err := l.dataset.Lookup(address.Unmap())
		if err != nil {
			return err
		}
		if !ok {
			// The dataset went away between the check above and this lookup.
			return nil
		}
		row := store.GeoASN{
			Address: text, ProviderID: providerID, LookupState: store.GeoMiss,
			CountryCode: answer.CountryCode, CountryName: answer.CountryName,
			Latitude: answer.Latitude, Longitude: answer.Longitude,
			ASN: answer.ASN, Operator: answer.Operator,
			DatasetBuildAt: build, LookedUpAt: l.now().UTC().Unix(),
		}
		if answer.CountryCode != nil {
			row.LookupState = store.GeoResolved
		}
		if err := l.store.UpsertGeoASN(ctx, row); err != nil {
			return err
		}
	}
	return nil
}

// isPublic is whether an address is worth asking a dataset about: a global unicast
// address that is not in a private range. It is the standard library's
// classification, not a list kept here.
func isPublic(address netip.Addr) bool {
	address = address.Unmap()
	return address.IsValid() && address.IsGlobalUnicast() && !address.IsPrivate()
}

// Tasks returns the two loops: the refresh, woken early by a save on the settings
// surface, and the lookups. Both read their interval from the live configuration
// before every wait.
func Tasks(settings *config.Live, refresher *Refresher, locator *Locator) []collect.Task {
	return []collect.Task{
		{
			Name:       "MaxMind database refresh",
			Interval:   settings.Interval(func(s config.Config) time.Duration { return s.GeoIPRefreshInterval }),
			Run:        refresher.Refresh,
			RunAtStart: true,
			Wake:       refresher.Woken(),
		},
		{
			Name:       "geolocation lookups",
			Interval:   settings.Interval(func(s config.Config) time.Duration { return s.GeoLookupInterval }),
			Run:        locator.Locate,
			RunAtStart: true,
		},
	}
}
