package provider

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"
)

// One record representation, one registry (PA14 of the plugin-API amendment to
// specs/SPEC-backend-completion-plugins.md).
//
// AN IMPLEMENTATION IS CALLED BY FUNCTION NAME AND ANSWERS A RECORD OF ITS KIND'S CONTRACT, checked
// once against the kind file (scripthost.CheckRecord). There is no per-kind interface and no per-kind
// struct between a plugin and the kind's coordinator: the coordinator reads the checked record and
// writes the store's rows from it. Whatever the kind -- a core kind, a service kind, a kind a plugin
// declared for itself -- the registry holds its implementations the same way, keyed by kind and
// provider key.

// Answer is one function's answer, checked against its contract: field names to Go values (text,
// int64, float64, bool, []string, a nested Answer for a record field and []Answer for a records
// field), absent fields absent.
type Answer = map[string]any

// Implementation is one provider of a kind: what answers the functions of the kind's contract.
type Implementation interface {
	// Call runs one function of the kind and returns its answer, checked against the table the
	// function returns, and the result the answer carries. err is the call's failure, the answer's
	// own error, or ErrUnsupportedRead with the answer's reason; on a failure the result is what the
	// availability row records. An answer whose table carries no result has a zero result.
	Call(ctx context.Context, session Session, function string, arguments ...any) (Answer, Result, error)
}

// LiveImplementation is an implementation declaring a live pass (docs/plugins.md, "Live pass").
type LiveImplementation interface {
	Implementation
	// LiveCadence names the setting holding the live cadence, in seconds, its default, its lower
	// bound and the highest freshness floor of the endpoints the live pass reads.
	LiveCadence() (key string, fallback, minimum, floor int64)
	// CallLive runs the live pass, its requests accepting an answer at most maxAge old. It answers
	// what the kind's live answer table holds.
	CallLive(ctx context.Context, session Session, maxAge time.Duration, arguments ...any) (Answer, Result, error)
}

// Registration is one implementation of a kind, with what its manifest says of it.
type Registration struct {
	Kind        string
	Key         string
	DisplayName string
	// Plugin is the plugin declaring it.
	Plugin string
	// Cadence is the declared interval between passes, for a kind whose providers state their own,
	// and MinCadence the lowest an override may set and the least time between two passes however
	// often a trigger fires.
	Cadence    time.Duration
	MinCadence time.Duration
	// Triggers are the core notifications that run a pass besides the cadence, each "channel" or
	// "channel:subject".
	Triggers []string
	// Values are the plain values its script exports beside the functions, by name (the kind's
	// contract names them).
	Values map[string]any
	// Implementation answers its functions.
	Implementation Implementation
}

// Manifest returns the three facts its registry row is made of.
func (r Registration) Manifest() Manifest {
	return Manifest{Kind: r.Kind, Key: r.Key, DisplayName: r.DisplayName}
}

// Probe runs the probe every kind with a firewall source exports.
func (r Registration) Probe(ctx context.Context, session Session) (Result, error) {
	_, result, err := r.Implementation.Call(ctx, session, "probe")
	return result, err
}

// KindAttributes are a kind's attributes as data: a core kind's from its kind file, a plugin's own
// kind's from its manifest's `kinds` entry.
type KindAttributes struct {
	Kind string
	// Plugin is the plugin declaring the kind; empty for a core kind.
	Plugin     string
	Concurrent bool
	Push       bool
	Bindable   bool
}

// AttributesOf are a core kind's attributes.
func (c Contract) AttributesOf() KindAttributes {
	return KindAttributes{Kind: c.Kind, Concurrent: c.Concurrent, Push: c.Push, Bindable: c.Bindable}
}

// Declarer tells the broker every pass asks (a Gate) what one plugin declares of its endpoints.
type Declarer func(gate Gate)

// Set is every kind's implementations for one installation: the built-in plugins and those of a data
// directory, as the plugin host loaded them.
type Set struct {
	mutex         sync.RWMutex
	registrations map[[2]string]Registration
	declaredKinds map[string]KindAttributes
	builtin       map[[2]string]bool
	failures      []error
	declarers     []Declarer
}

// NewSet returns an empty set.
func NewSet() *Set {
	return &Set{registrations: map[[2]string]Registration{}, declaredKinds: map[string]KindAttributes{},
		builtin: map[[2]string]bool{}}
}

// Add registers one implementation. The same kind and key twice is refused, and so is a kind that is
// neither a core kind a plugin may implement nor one a plugin declared.
func (s *Set) Add(registration Registration) error {
	if reason, coreOnly := CoreOnly(registration.Kind); coreOnly {
		return fmt.Errorf("provider: the kind %q is %s", registration.Kind, reason)
	}
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if _, core := ContractOf(registration.Kind); !core {
		if _, declared := s.declaredKinds[registration.Kind]; !declared {
			return fmt.Errorf("provider: the kind %q is neither a core kind nor one a plugin declared", registration.Kind)
		}
	}
	identity := [2]string{registration.Kind, registration.Key}
	if _, duplicate := s.registrations[identity]; duplicate {
		return fmt.Errorf("provider: the %s implementation %q is declared twice", registration.Kind, registration.Key)
	}
	s.registrations[identity] = registration
	return nil
}

// Taken reports whether a kind and key are already registered.
func (s *Set) Taken(kind, key string) bool {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	_, taken := s.registrations[[2]string{kind, key}]
	return taken
}

// Lookup returns the implementation registered under a kind and key.
func (s *Set) Lookup(kind, key string) (Registration, bool) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	registration, found := s.registrations[[2]string{kind, key}]
	return registration, found
}

// Of returns the implementations of one kind, ordered by key.
func (s *Set) Of(kind string) []Registration {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	var registrations []Registration
	for identity, registration := range s.registrations {
		if identity[0] == kind {
			registrations = append(registrations, registration)
		}
	}
	sort.Slice(registrations, func(i, j int) bool { return registrations[i].Key < registrations[j].Key })
	return registrations
}

// All returns every implementation, ordered by kind and key.
func (s *Set) All() []Registration {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	registrations := make([]Registration, 0, len(s.registrations))
	for _, registration := range s.registrations {
		registrations = append(registrations, registration)
	}
	sort.Slice(registrations, func(i, j int) bool {
		if registrations[i].Kind != registrations[j].Kind {
			return registrations[i].Kind < registrations[j].Kind
		}
		return registrations[i].Key < registrations[j].Key
	})
	return registrations
}

// Declared returns every registry row the set's implementations declare, ordered by kind and key.
func (s *Set) Declared() []Manifest {
	var manifests []Manifest
	for _, registration := range s.All() {
		manifests = append(manifests, registration.Manifest())
	}
	return manifests
}

// DeclareKind records a kind a plugin declared for itself, with its attributes. A core kind, or a
// kind another plugin declared, is refused.
func (s *Set) DeclareKind(attributes KindAttributes) error {
	if IsCoreKind(attributes.Kind) {
		return fmt.Errorf("provider: %q is a core kind, which a plugin implements and never declares", attributes.Kind)
	}
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if held, taken := s.declaredKinds[attributes.Kind]; taken && held.Plugin != attributes.Plugin {
		return fmt.Errorf("provider: the kind %q is already declared by the plugin %s", attributes.Kind, held.Plugin)
	}
	s.declaredKinds[attributes.Kind] = attributes
	return nil
}

// DeclaredKindOwner returns the plugin that declared a kind, when one did.
func (s *Set) DeclaredKindOwner(kind string) (string, bool) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	attributes, found := s.declaredKinds[kind]
	return attributes.Plugin, found
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

// Kinds returns every kind the set knows, sorted: the core kinds and the declared ones. It is derived,
// never listed by hand.
func (s *Set) Kinds() []string {
	kinds := append(CoreKinds(), s.DeclaredKinds()...)
	sort.Strings(kinds)
	return kinds
}

// Attributes returns a kind's attributes: a core kind's from its file, a declared kind's from its
// manifest.
func (s *Set) Attributes(kind string) (KindAttributes, bool) {
	if contract, core := KindOf(kind); core {
		return contract.AttributesOf(), true
	}
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	attributes, found := s.declaredKinds[kind]
	return attributes, found
}

// AllAttributes returns every kind's attributes, ordered by kind: what the database's provider_kind
// rows are written from.
func (s *Set) AllAttributes() []KindAttributes {
	var all []KindAttributes
	for _, kind := range s.Kinds() {
		if attributes, found := s.Attributes(kind); found {
			all = append(all, attributes)
		}
	}
	return all
}

// Concurrent reports whether several providers of a kind may be active at once.
func (s *Set) Concurrent(kind string) bool {
	attributes, _ := s.Attributes(kind)
	return attributes.Concurrent
}

// Pushable reports whether a push entry may deliver records of a kind.
func (s *Set) Pushable(kind string) bool {
	attributes, _ := s.Attributes(kind)
	return attributes.Push
}

// BindableKinds returns the kinds a widget manifest may bind, sorted.
func (s *Set) BindableKinds() []string {
	var kinds []string
	for _, attributes := range s.AllAttributes() {
		if attributes.Bindable {
			kinds = append(kinds, attributes.Kind)
		}
	}
	return kinds
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

// MarkBuiltin records that an implementation is a built-in plugin's: the default of a kind with one
// active provider, when the operator chose none.
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

// AddDeclarer adds what tells a broker one plugin's endpoint declarations.
func (s *Set) AddDeclarer(declare Declarer) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.declarers = append(s.declarers, declare)
}

// Declarers returns what tells a broker the loaded plugins' endpoint declarations, in load order.
func (s *Set) Declarers() []Declarer {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	return append([]Declarer(nil), s.declarers...)
}

// CadenceSettingKey is the setting a provider's cadence is overridden by, namespaced by its plugin:
// <plugin>_cadence__<kind>__<provider key>. A declared setting key never holds a double underscore,
// so no setting a plugin declares can collide with it. The override is never below the provider's
// declared lower bound.
func CadenceSettingKey(plugin, kind, key string) string {
	return plugin + "_" + CadenceSettingName(kind, key)
}

// CadenceSettingName is the override's key among the plugin's own settings.
func CadenceSettingName(kind, key string) string { return "cadence__" + kind + "__" + key }
