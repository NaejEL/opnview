package services

import (
	"context"
	"net/netip"
	"sort"
	"time"

	"github.com/NaejEL/opnview/internal/collect/core"
	"github.com/NaejEL/opnview/internal/collect/provider"
	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/store/geo"
	"github.com/NaejEL/opnview/internal/store/vocab"
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

// The two service kinds this coordinator serves.
const (
	geolocationKind       = "ip_geolocation"
	registrableDomainKind = "registrable_domain"
)

// maxLookupsPerPass bounds one placing pass, so a first pass over a large history does not hold the
// database for minutes; the next pass carries on where it stopped.
const maxLookupsPerPass = 20000

// lookupBatch is how many addresses one call of a plugin's lookup is handed.
const lookupBatch = 500

// geoWakeChannel is the channel a save of the MaxMind credentials wakes the refresh with.
func (c *ServiceCoordinator) geoWakeChannel() chan struct{} {
	c.Services.GeoWakeOnce.Do(func() { c.Services.GeoWake = make(chan struct{}, 1) })
	return c.Services.GeoWake
}

// Wake asks for a geolocation refresh now rather than at the next interval. It never blocks: a
// wake already pending is the same request. It is what the settings surface calls when the
// licence key or the account ID is saved.
func (c *ServiceCoordinator) Wake() {
	select {
	case c.geoWakeChannel() <- struct{}{}:
	default:
	}
}

// Ready is the build of the geolocation dataset in use, and whether one is held: what the settings
// surface shows beside the state.
func (c *ServiceCoordinator) Ready() (int64, bool) {
	c.Services.Mutex.RLock()
	defer c.Services.Mutex.RUnlock()
	return provider.Integer(c.Services.Geo, "build"), provider.Flag(c.Services.Geo, "ready", false)
}

// Held reports the registrable-domain list in use: its version, its rule count, and the last
// instant a refresh confirmed it current. It reads memory and contacts nothing.
func (c *ServiceCoordinator) Held() (version string, rules int, confirmedAt int64, held bool) {
	c.Services.Mutex.RLock()
	defer c.Services.Mutex.RUnlock()
	list := c.Services.List
	return provider.Text(list, "version"), int(provider.Integer(list, "rules")), provider.Integer(list, "confirmed_at"),
		provider.Flag(list, "ready", false)
}

// keysOf are the provider keys of a kind's implementations, sorted.
func (c *ServiceCoordinator) keysOf(kind string) []string {
	var keys []string
	for _, registration := range c.SourceSet.Of(kind) {
		keys = append(keys, registration.Key)
	}
	return keys
}

// chooseService picks the one provider of a service kind opnview reads: the provider turned on,
// or else the built-in, or else the first other not turned off; "" when every one is off.
func (c *ServiceCoordinator) chooseService(ctx context.Context, kind string, keys []string) (string, error) {
	sort.Strings(keys)
	selections := map[string]config.Selection{}
	for _, key := range keys {
		selection, err := config.LoadSourceSelection(ctx, c.Store, kind, key)
		if err != nil {
			return "", err
		}
		selections[key] = selection
		if selection == config.SelectionOn {
			return key, nil
		}
	}
	for _, key := range keys {
		if c.SourceSet.IsBuiltin(kind, key) && selections[key] != config.SelectionOff {
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
func (c *ServiceCoordinator) recordService(ctx context.Context, kind, key string, ready bool,
	result provider.Result) error {
	providerID, err := c.Store.ProviderID(ctx, kind, key)
	if err != nil {
		return err
	}
	state := result.State
	if state == "" {
		state = vocab.StateUnavailable
		if ready {
			state = vocab.StateReachable
		}
	}
	outcome := result.Outcome
	if outcome == "" {
		outcome = vocab.OutcomeRequestNotMade
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
	return c.Store.SetAvailability(ctx, providerID, state, outcome, probe, detail, c.Now())
}

// RefreshGeolocation refreshes the active ip_geolocation provider's dataset and records where it
// stands; the provider is active exactly when its dataset is ready.
func (c *ServiceCoordinator) RefreshGeolocation(ctx context.Context) error {
	if err := c.EnsureProviders(ctx); err != nil {
		return err
	}
	key, err := c.chooseService(ctx, geolocationKind, c.keysOf(geolocationKind))
	if err != nil || key == "" {
		if err == nil {
			err = c.Store.SetActiveProviders(ctx, geolocationKind)
		}
		return err
	}
	source, _ := c.SourceSet.Lookup(geolocationKind, key)
	dataset, result, refreshErr := source.Implementation.Call(ctx, c, "refresh")
	ready := provider.Flag(dataset, "ready", false)
	if err := c.recordService(ctx, geolocationKind, key, ready, result); err != nil {
		return err
	}
	c.Services.Mutex.Lock()
	c.Services.GeoKey, c.Services.Geo = key, dataset
	c.Services.Mutex.Unlock()
	if ready {
		if err := c.Store.SetActiveProviders(ctx, geolocationKind, key); err != nil {
			return err
		}
	} else if err := c.Store.SetActiveProviders(ctx, geolocationKind); err != nil {
		return err
	}
	return refreshErr
}

// LoadServices reads, without acquiring anything, what each service kind's chosen provider already
// holds on disk: what a start does before the first refresh, so the dataset and the list a previous
// run downloaded are in use at once.
func (c *ServiceCoordinator) LoadServices(ctx context.Context) error {
	var failures []error
	if key, err := c.chooseService(ctx, geolocationKind, c.keysOf(geolocationKind)); err != nil {
		failures = append(failures, err)
	} else if key != "" {
		source, _ := c.SourceSet.Lookup(geolocationKind, key)
		dataset, _, err := source.Implementation.Call(ctx, c, "status")
		if err != nil {
			failures = append(failures, err)
		}
		c.Services.Mutex.Lock()
		c.Services.GeoKey, c.Services.Geo = key, dataset
		c.Services.Mutex.Unlock()
	}
	if key, err := c.chooseService(ctx, registrableDomainKind,
		c.keysOf(registrableDomainKind)); err != nil {
		failures = append(failures, err)
	} else if key != "" {
		source, _ := c.SourceSet.Lookup(registrableDomainKind, key)
		list, _, err := source.Implementation.Call(ctx, c, "status")
		if err != nil {
			failures = append(failures, err)
		}
		c.Services.Mutex.Lock()
		c.Services.ListKey, c.Services.List = key, list
		c.Services.Mutex.Unlock()
	}
	return core.JoinErrors(failures)
}

// LocateAddresses places every public address the stored flows carry that has no answer from the
// active provider's current build. With no dataset ready nothing is written: an unreachable
// dataset produces neither answers nor misses. An address that is not public is never looked up
// and never written, because no dataset places it and asking would record a miss that says nothing.
func (c *ServiceCoordinator) LocateAddresses(ctx context.Context) error {
	c.Services.Mutex.RLock()
	key, dataset := c.Services.GeoKey, c.Services.Geo
	c.Services.Mutex.RUnlock()
	if key == "" || !provider.Flag(dataset, "ready", false) {
		return nil
	}
	source, found := c.SourceSet.Lookup(geolocationKind, key)
	if !found {
		return nil
	}
	providerID, err := c.Store.ProviderID(ctx, geolocationKind, key)
	if err != nil {
		return err
	}
	addresses, err := c.Store.AddressesToGeolocate(ctx, providerID, provider.Integer(dataset, "build"))
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
		list := make([]any, 0, end-start)
		for _, address := range public[start:end] {
			list = append(list, address)
		}
		answer, _, err := source.Implementation.Call(ctx, c, "lookup", list)
		if err != nil {
			return err
		}
		build := provider.Integer(answer, "build")
		asked := map[string]bool{}
		for _, address := range public[start:end] {
			asked[address] = true
		}
		now := c.Now()
		for _, placed := range provider.Records(answer, "answers") {
			address := provider.Text(placed, "address")
			if !asked[address] {
				continue
			}
			row := geo.GeoASN{
				Address: address, ProviderID: providerID, LookupState: geo.GeoMiss,
				CountryCode: provider.TextPointer(placed, "country_code"),
				CountryName: provider.TextPointer(placed, "country_name"),
				Latitude:    provider.NumberPointer(placed, "latitude"),
				Longitude:   provider.NumberPointer(placed, "longitude"),
				ASN: provider.IntegerPointer(placed, "asn"), Operator: provider.TextPointer(placed, "operator"),
				DatasetBuildAt: build, LookedUpAt: now,
			}
			if row.CountryCode != nil {
				row.LookupState = geo.GeoResolved
			}
			if err := c.Store.UpsertGeoASN(ctx, row); err != nil {
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
func (c *ServiceCoordinator) RefreshRegistrableDomains(ctx context.Context) error {
	if err := c.EnsureProviders(ctx); err != nil {
		return err
	}
	key, err := c.chooseService(ctx, registrableDomainKind, c.keysOf(registrableDomainKind))
	if err != nil || key == "" {
		if err == nil {
			err = c.Store.SetActiveProviders(ctx, registrableDomainKind)
		}
		return err
	}
	source, _ := c.SourceSet.Lookup(registrableDomainKind, key)
	list, result, refreshErr := source.Implementation.Call(ctx, c, "refresh")
	now := c.Now()
	ready := provider.Flag(list, "ready", false)
	if provider.Flag(list, "failed", false) {
		providerID, err := c.Store.ProviderID(ctx, registrableDomainKind, key)
		if err != nil {
			return err
		}
		start := now
		if confirmed := provider.Integer(list, "confirmed_at"); confirmed > 0 && confirmed < now {
			start = confirmed
		}
		var detail *string
		if refreshErr != nil {
			text := refreshErr.Error()
			detail = &text
		}
		if err := c.Store.RecordCollectionGap(ctx, vocab.CollectionGap{ProviderID: providerID,
			IntervalStartAt: start, IntervalEndAt: now, Reason: vocab.GapDownloadFailed, Detail: detail,
			DetectedAt: now}); err != nil {
			return err
		}
	}
	if err := c.recordService(ctx, registrableDomainKind, key, ready, result); err != nil {
		return err
	}
	c.Services.Mutex.Lock()
	c.Services.ListKey, c.Services.List = key, list
	c.Services.Mutex.Unlock()
	if ready {
		if err := c.Store.SetActiveProviders(ctx, registrableDomainKind, key); err != nil {
			return err
		}
	} else if err := c.Store.SetActiveProviders(ctx, registrableDomainKind); err != nil {
		return err
	}
	return refreshErr
}

// RegistrableDomains returns each name's registrable domain, from the active registrable_domain
// provider, and false when no list is held or the provider failed: the widgets then offer no
// grouping, named, rather than a name grouped by a guess.
func (c *ServiceCoordinator) RegistrableDomains(ctx context.Context, names []string) (map[string]string, bool) {
	c.Services.Mutex.RLock()
	key, list := c.Services.ListKey, c.Services.List
	c.Services.Mutex.RUnlock()
	if key == "" || !provider.Flag(list, "ready", false) {
		return nil, false
	}
	source, found := c.SourceSet.Lookup(registrableDomainKind, key)
	if !found {
		return nil, false
	}
	ctx, cancel := context.WithTimeout(ctx, registrableGroupingBound)
	defer cancel()
	asked := make([]any, 0, len(names))
	for _, name := range names {
		asked = append(asked, name)
	}
	answer, _, err := source.Implementation.Call(ctx, c, "group", asked)
	if err != nil {
		return nil, false
	}
	domains := map[string]string{}
	for _, grouping := range provider.Records(answer, "groups") {
		if domain := provider.Text(grouping, "registrable_domain"); domain != "" {
			domains[provider.Text(grouping, "name")] = domain
		}
	}
	return domains, true
}

// registrableGroupingBound bounds the grouping a widget response waits for. A slow or failing
// provider degrades the response to "no grouping" rather than blocking it.
const registrableGroupingBound = 5 * time.Second

// ServiceTasks are the loops of the two service kinds.
func (c *ServiceCoordinator) ServiceTasks(settings *config.Live) []core.Task {
	return []core.Task{
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
