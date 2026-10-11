package collect

import (
	"context"
	"net/netip"
	"sort"
	"sync"
	"time"

	"github.com/NaejEL/opnview/internal/collect/ipgeolocation"
	"github.com/NaejEL/opnview/internal/collect/provider"
	"github.com/NaejEL/opnview/internal/collect/registrabledomain"
	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/store"
)

// The two service kinds: ip_geolocation and registrable_domain (B2 of the backend-completion
// cycle).
//
// THE CORE TALKS ONLY TO THE FIREWALL. A service's data is acquired by its plugin, through the
// plugin host's fetch facility; what is written here is what is true of any such service, whoever
// provides it:
//
//   - ONE ACTIVE PROVIDER PER SERVICE KIND, because a figure must not be placed by two datasets at
//     once. It is the provider the operator turned on, or else the built-in one, or else the first
//     other one not turned off; and it is active only while it holds its data.
//   - The refreshes run at the intervals the settings surface has always set
//     (refresh_interval_geoip_seconds, refresh_interval_public_suffix_list_seconds), each woken early
//     by a save of its credentials; the placing pass runs at poll_interval_geo_lookup_seconds.
//   - Which addresses need placing, never a non-public one, at most maxLookupsPerPass per pass, the
//     answers stored in geo_asn against the provider and the build: every address is placed again
//     when the active provider or its build changes.
//   - A refresh of the list that failed is a collection gap from the instant it was last confirmed.
//   - The widgets group names through RegistrableDomains, which degrades to "no grouping" and never
//     blocks a response.

// maxLookupsPerPass bounds one placing pass, so a first pass over a large history does not hold the
// database for minutes; the next pass carries on where it stopped.
const maxLookupsPerPass = 20000

// lookupBatch is how many addresses one call of a plugin's lookup is handed.
const lookupBatch = 500

// services is what the collector last heard from each service kind's active provider.
type services struct {
	mutex       sync.RWMutex
	geoKey      string
	geo         ipgeolocation.Dataset
	listKey     string
	list        registrabledomain.List
	geoWake     chan struct{}
	geoWakeOnce sync.Once
}

// geoWakeChannel is the channel a save of the MaxMind credentials wakes the refresh with.
func (c *Collector) geoWakeChannel() chan struct{} {
	c.services.geoWakeOnce.Do(func() { c.services.geoWake = make(chan struct{}, 1) })
	return c.services.geoWake
}

// Wake asks for a geolocation refresh now rather than at the next interval. It never blocks: a
// wake already pending is the same request. It is what the settings surface calls when the
// licence key or the account ID is saved.
func (c *Collector) Wake() {
	select {
	case c.geoWakeChannel() <- struct{}{}:
	default:
	}
}

// Ready is the build of the geolocation dataset in use, and whether one is held: what the settings
// surface shows beside the state.
func (c *Collector) Ready() (int64, bool) {
	c.services.mutex.RLock()
	defer c.services.mutex.RUnlock()
	return c.services.geo.Build, c.services.geo.Ready
}

// Held reports the registrable-domain list in use: its version, its rule count, and the last
// instant a refresh confirmed it current. It reads memory and contacts nothing.
func (c *Collector) Held() (version string, rules int, confirmedAt int64, held bool) {
	c.services.mutex.RLock()
	defer c.services.mutex.RUnlock()
	list := c.services.list
	return list.Version, list.Rules, list.ConfirmedAt, list.Ready
}

// chooseService picks the one provider of a service kind opnview reads: the provider turned on,
// or else the built-in, or else the first other not turned off; "" when every one is off.
func (c *Collector) chooseService(ctx context.Context, kind string, keys []string) (string, error) {
	sort.Strings(keys)
	selections := map[string]config.Selection{}
	for _, key := range keys {
		selection, err := config.LoadSourceSelection(ctx, c.store, kind, key)
		if err != nil {
			return "", err
		}
		selections[key] = selection
		if selection == config.SelectionOn {
			return key, nil
		}
	}
	for _, key := range keys {
		if c.sources.IsBuiltin(kind, key) && selections[key] != config.SelectionOff {
			return key, nil
		}
	}
	for _, key := range keys {
		if selections[key] != config.SelectionOff {
			return key, nil
		}
	}
	return "", nil
}

// recordService writes a service provider's availability: the state and outcome its plugin
// reported, the probe word it named, and the detail.
func (c *Collector) recordService(ctx context.Context, kind, key string, ready bool,
	result provider.Result) error {
	providerID, err := c.store.ProviderID(ctx, kind, key)
	if err != nil {
		return err
	}
	state := result.State
	if state == "" {
		state = store.StateUnavailable
		if ready {
			state = store.StateReachable
		}
	}
	outcome := result.Outcome
	if outcome == "" {
		outcome = store.OutcomeRequestNotMade
	}
	probe := result.Probe.Path
	if probe == "" {
		probe = result.Probe.Method
	}
	var detail *string
	if result.Detail != "" {
		text := result.Detail
		detail = &text
	}
	return c.store.SetAvailability(ctx, providerID, state, outcome, probe, detail, c.now())
}

// RefreshGeolocation refreshes the active ip_geolocation provider's dataset and records where it
// stands; the provider is active exactly when its dataset is ready.
func (c *Collector) RefreshGeolocation(ctx context.Context) error {
	if err := c.ensureProviders(ctx); err != nil {
		return err
	}
	key, err := c.chooseService(ctx, ipgeolocation.Kind, sortedKeys(c.sources.IPGeolocation.Sources()))
	if err != nil || key == "" {
		if err == nil {
			err = c.store.SetActiveProviders(ctx, ipgeolocation.Kind)
		}
		return err
	}
	source, _ := c.sources.IPGeolocation.Lookup(key)
	dataset, result, refreshErr := source.Refresh(ctx, c)
	if err := c.recordService(ctx, ipgeolocation.Kind, key, dataset.Ready, result); err != nil {
		return err
	}
	c.services.mutex.Lock()
	c.services.geoKey, c.services.geo = key, dataset
	c.services.mutex.Unlock()
	if dataset.Ready {
		if err := c.store.SetActiveProviders(ctx, ipgeolocation.Kind, key); err != nil {
			return err
		}
	} else if err := c.store.SetActiveProviders(ctx, ipgeolocation.Kind); err != nil {
		return err
	}
	return refreshErr
}

// LoadServices reads, without acquiring anything, what each service kind's chosen provider already
// holds on disk: what a start does before the first refresh, so the dataset and the list a previous
// run downloaded are in use at once.
func (c *Collector) LoadServices(ctx context.Context) error {
	var failures []error
	if key, err := c.chooseService(ctx, ipgeolocation.Kind, sortedKeys(c.sources.IPGeolocation.Sources())); err != nil {
		failures = append(failures, err)
	} else if key != "" {
		source, _ := c.sources.IPGeolocation.Lookup(key)
		dataset, _, err := source.Status(ctx, c)
		if err != nil {
			failures = append(failures, err)
		}
		c.services.mutex.Lock()
		c.services.geoKey, c.services.geo = key, dataset
		c.services.mutex.Unlock()
	}
	if key, err := c.chooseService(ctx, registrabledomain.Kind,
		sortedKeys(c.sources.RegistrableDomain.Sources())); err != nil {
		failures = append(failures, err)
	} else if key != "" {
		source, _ := c.sources.RegistrableDomain.Lookup(key)
		list, _, err := source.Status(ctx, c)
		if err != nil {
			failures = append(failures, err)
		}
		c.services.mutex.Lock()
		c.services.listKey, c.services.list = key, list
		c.services.mutex.Unlock()
	}
	return joinErrors(failures)
}

// LocateAddresses places every public address the stored flows carry that has no answer from the
// active provider's current build. With no dataset ready nothing is written: an unreachable
// dataset produces neither answers nor misses. An address that is not public is never looked up
// and never written, because no dataset places it and asking would record a miss that says nothing.
func (c *Collector) LocateAddresses(ctx context.Context) error {
	c.services.mutex.RLock()
	key, dataset := c.services.geoKey, c.services.geo
	c.services.mutex.RUnlock()
	if key == "" || !dataset.Ready {
		return nil
	}
	source, found := c.sources.IPGeolocation.Lookup(key)
	if !found {
		return nil
	}
	providerID, err := c.store.ProviderID(ctx, ipgeolocation.Kind, key)
	if err != nil {
		return err
	}
	addresses, err := c.store.AddressesToGeolocate(ctx, providerID, dataset.Build)
	if err != nil {
		return err
	}
	var public []string
	for _, text := range addresses {
		if len(public) >= maxLookupsPerPass || ctx.Err() != nil {
			break
		}
		address, err := netip.ParseAddr(text)
		if err != nil || !isPublic(address) {
			continue
		}
		public = append(public, text)
	}
	for start := 0; start < len(public); start += lookupBatch {
		end := min(start+lookupBatch, len(public))
		build, answers, err := source.Lookup(ctx, c, public[start:end])
		if err != nil {
			return err
		}
		asked := map[string]bool{}
		for _, address := range public[start:end] {
			asked[address] = true
		}
		now := c.now()
		for _, answer := range answers {
			if !asked[answer.Address] {
				continue
			}
			row := store.GeoASN{
				Address: answer.Address, ProviderID: providerID, LookupState: store.GeoMiss,
				CountryCode: answer.CountryCode, CountryName: answer.CountryName,
				Latitude: answer.Latitude, Longitude: answer.Longitude,
				ASN: answer.ASN, Operator: answer.Operator,
				DatasetBuildAt: build, LookedUpAt: now,
			}
			if answer.CountryCode != nil {
				row.LookupState = store.GeoResolved
			}
			if err := c.store.UpsertGeoASN(ctx, row); err != nil {
				return err
			}
		}
	}
	return nil
}

// isPublic is whether an address is worth asking a dataset about: a global unicast address that
// is not in a private range. It is the standard library's classification, not a list kept here.
func isPublic(address netip.Addr) bool {
	address = address.Unmap()
	return address.IsValid() && address.IsGlobalUnicast() && !address.IsPrivate()
}

// RefreshRegistrableDomains refreshes the active registrable_domain provider's list, records where
// it stands, and records a failed refresh as a collection gap from the instant the list was last
// confirmed current.
func (c *Collector) RefreshRegistrableDomains(ctx context.Context) error {
	if err := c.ensureProviders(ctx); err != nil {
		return err
	}
	key, err := c.chooseService(ctx, registrabledomain.Kind, sortedKeys(c.sources.RegistrableDomain.Sources()))
	if err != nil || key == "" {
		if err == nil {
			err = c.store.SetActiveProviders(ctx, registrabledomain.Kind)
		}
		return err
	}
	source, _ := c.sources.RegistrableDomain.Lookup(key)
	list, result, refreshErr := source.Refresh(ctx, c)
	now := c.now()
	if list.Failed {
		providerID, err := c.store.ProviderID(ctx, registrabledomain.Kind, key)
		if err != nil {
			return err
		}
		start := now
		if list.ConfirmedAt > 0 && list.ConfirmedAt < now {
			start = list.ConfirmedAt
		}
		var detail *string
		if refreshErr != nil {
			text := refreshErr.Error()
			detail = &text
		}
		if err := c.store.RecordCollectionGap(ctx, store.CollectionGap{ProviderID: providerID,
			IntervalStartAt: start, IntervalEndAt: now, Reason: store.GapDownloadFailed, Detail: detail,
			DetectedAt: now}); err != nil {
			return err
		}
	}
	if err := c.recordService(ctx, registrabledomain.Kind, key, list.Ready, result); err != nil {
		return err
	}
	c.services.mutex.Lock()
	c.services.listKey, c.services.list = key, list
	c.services.mutex.Unlock()
	if list.Ready {
		if err := c.store.SetActiveProviders(ctx, registrabledomain.Kind, key); err != nil {
			return err
		}
	} else if err := c.store.SetActiveProviders(ctx, registrabledomain.Kind); err != nil {
		return err
	}
	return refreshErr
}

// RegistrableDomains returns each name's registrable domain, from the active registrable_domain
// provider, and false when no list is held or the provider failed: the widgets then offer no
// grouping, named, rather than a name grouped by a guess.
func (c *Collector) RegistrableDomains(ctx context.Context, names []string) (map[string]string, bool) {
	c.services.mutex.RLock()
	key, list := c.services.listKey, c.services.list
	c.services.mutex.RUnlock()
	if key == "" || !list.Ready {
		return nil, false
	}
	source, found := c.sources.RegistrableDomain.Lookup(key)
	if !found {
		return nil, false
	}
	ctx, cancel := context.WithTimeout(ctx, registrableGroupingBound)
	defer cancel()
	domains, err := source.Group(ctx, c, names)
	if err != nil {
		return nil, false
	}
	return domains, true
}

// registrableGroupingBound bounds the grouping a widget response waits for. A slow or failing
// provider degrades the response to "no grouping" rather than blocking it.
const registrableGroupingBound = 5 * time.Second

// ServiceTasks are the loops of the two service kinds.
func (c *Collector) ServiceTasks(settings *config.Live) []Task {
	return []Task{
		{
			Name:       "IP geolocation refresh",
			Interval:   settings.Interval(func(s config.Config) time.Duration { return s.GeoIPRefreshInterval }),
			Run:        c.RefreshGeolocation,
			RunAtStart: true,
			Wake:       c.geoWakeChannel(),
		},
		{
			Name:       "geolocation lookups",
			Interval:   settings.Interval(func(s config.Config) time.Duration { return s.GeoLookupInterval }),
			Run:        c.LocateAddresses,
			RunAtStart: true,
		},
		{
			Name: "registrable domain list refresh",
			Interval: settings.Interval(func(s config.Config) time.Duration {
				return s.PublicSuffixRefreshInterval
			}),
			Run:        c.RefreshRegistrableDomains,
			RunAtStart: true,
		},
	}
}
