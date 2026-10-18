package collect

import (
	"context"
	"fmt"
	"os"

	"github.com/NaejEL/opnview/internal/broker"
	"github.com/NaejEL/opnview/internal/broker/mirror"
	"github.com/NaejEL/opnview/internal/collect/core"
	"github.com/NaejEL/opnview/internal/collect/derive"
	"github.com/NaejEL/opnview/internal/collect/discovery"
	"github.com/NaejEL/opnview/internal/collect/kinds/declared"
	"github.com/NaejEL/opnview/internal/collect/kinds/filterlog"
	"github.com/NaejEL/opnview/internal/collect/kinds/leases"
	"github.com/NaejEL/opnview/internal/collect/kinds/lookups"
	"github.com/NaejEL/opnview/internal/collect/kinds/measurements"
	"github.com/NaejEL/opnview/internal/collect/kinds/neighbours"
	"github.com/NaejEL/opnview/internal/collect/kinds/resolverrecords"
	"github.com/NaejEL/opnview/internal/collect/kinds/securityevents"
	"github.com/NaejEL/opnview/internal/collect/provider"
	"github.com/NaejEL/opnview/internal/collect/services"
	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/plugin"
	"github.com/NaejEL/opnview/internal/store"
)

// Collector is the collector: the state every part shares (internal/collect/core), and the parts,
// each a package named by what it coordinates (doc.go). A part's methods are the Collector's, so a
// caller holds one Collector and never names a part.
type Collector struct {
	*core.Collector
	*derive.Deriver
	*discovery.Discoverer
	*services.ServiceCoordinator
	*leases.LeaseCoordinator
	*neighbours.NeighbourCoordinator
	*lookups.LookupCoordinator
	*filterlog.FilterLogCoordinator
	*securityevents.SecurityEventCoordinator
	*resolverrecords.ResolverRecordCoordinator
	*measurements.MeasurementCoordinator
	*declared.DeclaredKindCoordinator
}

// compose is the collector whose parts all share state.
func compose(state *core.Collector) *Collector {
	return &Collector{
		Collector:                 state,
		Deriver:                   derive.On(state),
		Discoverer:                discovery.On(state),
		ServiceCoordinator:        services.On(state),
		LeaseCoordinator:          leases.On(state),
		NeighbourCoordinator:      neighbours.On(state),
		LookupCoordinator:         lookups.On(state),
		FilterLogCoordinator:      filterlog.On(state),
		SecurityEventCoordinator:  securityevents.On(state),
		ResolverRecordCoordinator: resolverrecords.On(state),
		MeasurementCoordinator:    measurements.On(state),
		DeclaredKindCoordinator:   declared.On(state),
	}
}

// NewWithSources returns a collector reading the implementations of one source set: the built-in
// plugins and those of a data directory, as plugin.Load returns them.
func NewWithSources(client *opnsense.Client, database *store.Store, clock core.Clock, set *provider.Set) *Collector {
	if set == nil {
		set = plugin.Builtins()
	}
	if clock == nil {
		clock = core.SystemClock{}
	}
	c := &core.Collector{
		SourceSet:         set,
		Client:            client,
		Store:             database,
		Clock:             clock,
		MaxDelaySeconds:   config.DefaultAttributionMaxDelaySeconds,
		DiscoverySnapshot: core.NewDiscovery(),
		Probed:            map[string][]core.ProbedSource{},

		CacheAnswerDelaySeconds: config.DefaultCacheAnswerDelaySeconds,
		ReadDetails:             map[int64]string{},

		PairingWindowSeconds: config.DefaultLegPairingWindowSeconds,
		LeaseFailureLimit:    config.DefaultLeaseBackendFailurePassLimit,
		LeaseFailures:        map[int64]int64{},
		Cadences:             config.Defaults(),
	}
	var mirrored *mirror.Mirror
	if database != nil {
		mirrored = mirror.New(database, c.Now)
	}
	// THE BROKER IS THE ONE PATH TO THE FIREWALL (A9 of the broker amendment): every pass, the
	// probe round and discovery ask it, and it answers from its mirror or from one call.
	var settingReader broker.SettingReader
	if database != nil {
		settingReader = database.Setting
	}
	c.APIBroker = broker.New(client, mirrored, broker.Options{Clock: clock, Firewall: client.BaseURL,
		Normaliser: c.KnownNormaliser, Vocabulary: c.Vocabulary, Setting: settingReader})
	if settingReader != nil {
		settings, refused := broker.LoadSettings(context.Background(), settingReader)
		for _, refusal := range refused {
			fmt.Fprintf(os.Stderr, "opnview: %v\n", refusal)
		}
		c.APIBroker.Scheduler().SetSettings(settings)
	}
	for _, loaded := range set.Declarers() {
		loaded(c.APIBroker)
	}
	c.DeclareCoreEndpoints()
	c.Host = provider.NewHost(c.APIBroker, c.MeasureNormaliser, c.Now)
	return compose(c)
}
