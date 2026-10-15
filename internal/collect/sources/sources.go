package sources

import (
	"fmt"
	"sort"
	"sync"

	"github.com/NaejEL/opnview/internal/broker"
	"github.com/NaejEL/opnview/internal/collect/dhcplease"
	"github.com/NaejEL/opnview/internal/collect/dnslookup"
	"github.com/NaejEL/opnview/internal/collect/firewalllog"
	"github.com/NaejEL/opnview/internal/collect/flowrecord"
	"github.com/NaejEL/opnview/internal/collect/ipgeolocation"
	"github.com/NaejEL/opnview/internal/collect/measurement"
	"github.com/NaejEL/opnview/internal/collect/pluginkind"
	"github.com/NaejEL/opnview/internal/collect/provider"
	"github.com/NaejEL/opnview/internal/collect/reconciledstate"
	"github.com/NaejEL/opnview/internal/collect/registrabledomain"
	"github.com/NaejEL/opnview/internal/collect/resolvercache"
	"github.com/NaejEL/opnview/internal/collect/resolverlocaldata"
	"github.com/NaejEL/opnview/internal/collect/securityevent"
)

// Registry holds the implementations of one kind, keyed by provider key.
type Registry[S any] struct {
	kind  string
	mutex sync.RWMutex
	byKey map[string]S
}

func newRegistry[S any](kind string) *Registry[S] {
	return &Registry[S]{kind: kind, byKey: map[string]S{}}
}

// Kind is the kind the registry holds.
func (r *Registry[S]) Kind() string { return r.kind }

// add registers one implementation; a key registered twice is refused.
func (r *Registry[S]) add(key string, source S) error {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	if _, duplicate := r.byKey[key]; duplicate {
		return fmt.Errorf("sources: the %s implementation %q is registered twice", r.kind, key)
	}
	r.byKey[key] = source
	return nil
}

// Lookup returns the implementation registered under a key.
func (r *Registry[S]) Lookup(key string) (S, bool) {
	r.mutex.RLock()
	defer r.mutex.RUnlock()
	source, found := r.byKey[key]
	return source, found
}

// Sources returns a copy of the registered implementations, by key.
func (r *Registry[S]) Sources() map[string]S {
	r.mutex.RLock()
	defer r.mutex.RUnlock()
	copied := make(map[string]S, len(r.byKey))
	for key, source := range r.byKey {
		copied[key] = source
	}
	return copied
}

// Set is every kind's implementations for one installation.
type Set struct {
	FirewallLog       *Registry[firewalllog.Source]
	SecurityEvent     *Registry[securityevent.Source]
	DHCPLease         *Registry[dhcplease.Source]
	DNSLookup         *Registry[dnslookup.Source]
	ResolverCache     *Registry[resolvercache.Source]
	ResolverLocalData *Registry[resolverlocaldata.Source]
	Measurement       *Registry[measurement.Source]
	ReconciledState   *Registry[reconciledstate.Registration]
	IPGeolocation     *Registry[ipgeolocation.Source]
	RegistrableDomain *Registry[registrabledomain.Source]
	FlowRecord        *Registry[flowrecord.Source]

	mutex         sync.RWMutex
	declared      map[[2]string]provider.Manifest
	declaredKinds map[string]string
	pluginKinds   map[[2]string]pluginkind.Registration
	builtin       map[[2]string]bool
	failures      []error
	// declarers tell a broker what each loaded plugin declares of its endpoints (A17).
	declarers []func(*broker.Broker)
}

// AddDeclarer adds what tells a broker one plugin's endpoint declarations.
func (s *Set) AddDeclarer(declare func(*broker.Broker)) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.declarers = append(s.declarers, declare)
}

// Declarers returns what tells a broker the loaded plugins' endpoint declarations, in load order.
func (s *Set) Declarers() []func(*broker.Broker) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	declarers := make([]func(*broker.Broker), len(s.declarers))
	copy(declarers, s.declarers)
	return declarers
}

// New returns an empty set.
func New() *Set {
	return &Set{
		FirewallLog:       newRegistry[firewalllog.Source](firewalllog.Kind),
		SecurityEvent:     newRegistry[securityevent.Source](securityevent.Kind),
		DHCPLease:         newRegistry[dhcplease.Source](dhcplease.Kind),
		DNSLookup:         newRegistry[dnslookup.Source](dnslookup.Kind),
		ResolverCache:     newRegistry[resolvercache.Source](resolvercache.Kind),
		ResolverLocalData: newRegistry[resolverlocaldata.Source](resolverlocaldata.Kind),
		Measurement:       newRegistry[measurement.Source](measurement.Kind),
		ReconciledState:   newRegistry[reconciledstate.Registration](reconciledstate.Kind),
		IPGeolocation:     newRegistry[ipgeolocation.Source](ipgeolocation.Kind),
		RegistrableDomain: newRegistry[registrabledomain.Source](registrabledomain.Kind),
		FlowRecord:        newRegistry[flowrecord.Source](flowrecord.Kind),
		builtin:           map[[2]string]bool{},
		declared:          map[[2]string]provider.Manifest{},
		declaredKinds:     map[string]string{},
		pluginKinds:       map[[2]string]pluginkind.Registration{},
	}
}

// declare records a registry row; the same kind and key twice is refused.
func (s *Set) declare(manifest provider.Manifest) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	identity := [2]string{manifest.Kind, manifest.Key}
	if _, duplicate := s.declared[identity]; duplicate {
		return fmt.Errorf("sources: the %s implementation %q is declared twice", manifest.Kind, manifest.Key)
	}
	s.declared[identity] = manifest
	return nil
}

// undeclare withdraws a registry row whose implementation was not added after all.
func (s *Set) undeclare(manifest provider.Manifest) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	delete(s.declared, [2]string{manifest.Kind, manifest.Key})
}

// Taken reports whether a kind and key are already declared.
func (s *Set) Taken(kind, key string) bool {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	_, taken := s.declared[[2]string{kind, key}]
	return taken
}

// register declares a row and adds its implementation to a registry, as one step.
func register[S any](s *Set, registry *Registry[S], manifest provider.Manifest, source S) error {
	if manifest.Kind != registry.Kind() {
		return fmt.Errorf("sources: the implementation %q declares the kind %q and was added to %s",
			manifest.Key, manifest.Kind, registry.Kind())
	}
	if err := s.declare(manifest); err != nil {
		return err
	}
	if err := registry.add(manifest.Key, source); err != nil {
		s.undeclare(manifest)
		return err
	}
	return nil
}

// AddFirewallLog adds a firewall_log implementation.
func (s *Set) AddFirewallLog(manifest provider.Manifest, source firewalllog.Source) error {
	return register(s, s.FirewallLog, manifest, source)
}

// AddSecurityEvent adds a security_event implementation.
func (s *Set) AddSecurityEvent(manifest provider.Manifest, source securityevent.Source) error {
	return register(s, s.SecurityEvent, manifest, source)
}

// AddDHCPLease adds a dhcp_lease implementation.
func (s *Set) AddDHCPLease(manifest provider.Manifest, source dhcplease.Source) error {
	return register(s, s.DHCPLease, manifest, source)
}

// AddDNSLookup adds a dns_lookup implementation.
func (s *Set) AddDNSLookup(manifest provider.Manifest, source dnslookup.Source) error {
	return register(s, s.DNSLookup, manifest, source)
}

// AddResolverCache adds a resolver_cache implementation.
func (s *Set) AddResolverCache(manifest provider.Manifest, source resolvercache.Source) error {
	return register(s, s.ResolverCache, manifest, source)
}

// AddResolverLocalData adds a resolver_local_data implementation.
func (s *Set) AddResolverLocalData(manifest provider.Manifest, source resolverlocaldata.Source) error {
	return register(s, s.ResolverLocalData, manifest, source)
}

// AddMeasurement adds a measurement_sample implementation.
func (s *Set) AddMeasurement(manifest provider.Manifest, source measurement.Source) error {
	return register(s, s.Measurement, manifest, source)
}

// AddFlowRecord adds a flow_record implementation.
func (s *Set) AddFlowRecord(manifest provider.Manifest, source flowrecord.Source) error {
	return register(s, s.FlowRecord, manifest, source)
}

// AddReconciledState adds a reconciled_state implementation and the cadence it declared.
func (s *Set) AddReconciledState(manifest provider.Manifest, registration reconciledstate.Registration) error {
	registration.Key = manifest.Key
	return register(s, s.ReconciledState, manifest, registration)
}

// DeclareKind records a kind a plugin declared for itself. A core kind, or a kind another plugin
// declared, is refused.
func (s *Set) DeclareKind(kind, plugin string) error {
	if provider.IsCoreKind(kind) {
		return fmt.Errorf("sources: %q is a core kind, which a plugin implements and never declares", kind)
	}
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if owner, taken := s.declaredKinds[kind]; taken && owner != plugin {
		return fmt.Errorf("sources: the kind %q is already declared by the plugin %s", kind, owner)
	}
	s.declaredKinds[kind] = plugin
	return nil
}

// AddDeclared adds an implementation of a kind its plugin declared.
func (s *Set) AddDeclared(manifest provider.Manifest, registration pluginkind.Registration) error {
	s.mutex.RLock()
	owner, declared := s.declaredKinds[manifest.Kind]
	s.mutex.RUnlock()
	if !declared || owner != registration.Plugin {
		return fmt.Errorf("sources: the kind %q is not declared by the plugin %s", manifest.Kind, registration.Plugin)
	}
	if err := s.declare(manifest); err != nil {
		return err
	}
	registration.Kind, registration.Key = manifest.Kind, manifest.Key
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.pluginKinds[[2]string{manifest.Kind, manifest.Key}] = registration
	return nil
}

// Declared returns every registry row the set's implementations declare, ordered by kind and key.
func (s *Set) Declared() []provider.Manifest {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	manifests := make([]provider.Manifest, 0, len(s.declared))
	for _, manifest := range s.declared {
		manifests = append(manifests, manifest)
	}
	sort.Slice(manifests, func(i, j int) bool {
		if manifests[i].Kind != manifests[j].Kind {
			return manifests[i].Kind < manifests[j].Kind
		}
		return manifests[i].Key < manifests[j].Key
	})
	return manifests
}

// DeclaredKinds returns the kinds the set's plugins declared for themselves, sorted.
func (s *Set) DeclaredKinds() []string {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	kinds := make([]string, 0, len(s.declaredKinds))
	for kind := range s.declaredKinds {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	return kinds
}

// Kinds returns every kind the set knows, sorted: the core kinds and the declared ones. It is
// derived, never listed by hand: the widget manifests are validated against it and the
// source_availability widget covers it.
func (s *Set) Kinds() []string {
	kinds := append(provider.CoreKinds(), s.DeclaredKinds()...)
	sort.Strings(kinds)
	return kinds
}

// PluginKindRegistrations returns every implementation of a declared kind, ordered by kind and key.
func (s *Set) PluginKindRegistrations() []pluginkind.Registration {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	all := make([]pluginkind.Registration, 0, len(s.pluginKinds))
	for _, registration := range s.pluginKinds {
		all = append(all, registration)
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].Kind != all[j].Kind {
			return all[i].Kind < all[j].Kind
		}
		return all[i].Key < all[j].Key
	})
	return all
}

// PluginKind returns one implementation of a declared kind.
func (s *Set) PluginKind(kind, key string) (pluginkind.Registration, bool) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	registration, found := s.pluginKinds[[2]string{kind, key}]
	return registration, found
}

// NoteFailure keeps a refusal met while the set was filled, for the probe round to report.
func (s *Set) NoteFailure(err error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.failures = append(s.failures, err)
}

// Failures returns the refusals met while the set was filled.
func (s *Set) Failures() []error {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	return append([]error(nil), s.failures...)
}

// DeclaredKindOwner returns the plugin that declared a kind, when one did.
func (s *Set) DeclaredKindOwner(kind string) (string, bool) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	owner, found := s.declaredKinds[kind]
	return owner, found
}

// AddIPGeolocation adds an ip_geolocation implementation.
func (s *Set) AddIPGeolocation(manifest provider.Manifest, source ipgeolocation.Source) error {
	return register(s, s.IPGeolocation, manifest, source)
}

// AddRegistrableDomain adds a registrable_domain implementation.
func (s *Set) AddRegistrableDomain(manifest provider.Manifest, source registrabledomain.Source) error {
	return register(s, s.RegistrableDomain, manifest, source)
}

// MarkBuiltin records that an implementation is a built-in plugin's: the default of a kind with
// one active provider, when the operator chose none.
func (s *Set) MarkBuiltin(kind, key string) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.builtin[[2]string{kind, key}] = true
}

// IsBuiltin reports whether an implementation is a built-in plugin's.
func (s *Set) IsBuiltin(kind, key string) bool {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	return s.builtin[[2]string{kind, key}]
}
