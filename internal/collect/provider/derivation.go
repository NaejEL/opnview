package provider

import (
	"context"
	"time"

	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/store/vocab"
)

// A derivation (PA4 of the plugin-API amendment to specs/SPEC-backend-completion-plugins.md): a function of
// a plugin's script the collector runs after the collection passes it names, after the batches its own push
// entries store, and on its cadence, reading what the core and the plugin stored and writing its outputs in
// one transaction. "Derivation" is opnview's own term (docs/data-model.md, *Vocabulary*).
//
// THE COLLECTOR RUNS EACH ONE ON ITS OWN LOOP, one run at a time, and hands it what changed since its last
// successful run -- the passes, the batches, the cadence ticks -- never "everything". A run that fails keeps
// what it was handed for the next, writes nothing, and its closed outcome is recorded in the availability of
// its plugin's provider.

// The triggers a change can come from.
const (
	TriggerPass    = "pass"
	TriggerPush    = "push"
	TriggerCadence = "cadence"
)

// DerivationChange is one change a run is handed.
type DerivationChange struct {
	// Trigger is pass, push or cadence.
	Trigger string
	// Kind and Provider are a pass's.
	Kind, Provider string
	// Entry is a push batch's entry; Rows how many rows it stored; From and To the reception instants it
	// spans.
	Entry    string
	Rows     int
	From, To int64
	// At is when the change was noted, UTC epoch seconds.
	At int64
}

// DerivationOutcome is what one run did.
type DerivationOutcome struct {
	// Ran is false when nothing was pending: no script ran.
	Ran bool
	// Written is how many rows the run changed, and Instants the start instants of the connections whose
	// measure rows changed: the aggregate slots to refresh.
	Written  int64
	Instants []int64
}

// DerivationFailure is a run that failed: it wrote nothing, and its outcome is recorded in the availability
// of the provider named.
type DerivationFailure struct {
	Outcome      vocab.AvailabilityOutcome
	Detail       string
	ProviderKind string
	ProviderKey  string
	// Probe is what the availability row names as having decided it: the plugin's script.
	Probe opnsense.Endpoint
}

func (f *DerivationFailure) Error() string { return f.Detail }

// Derivation is one declared derivation, as the collector runs it.
type Derivation interface {
	// Name is <plugin>.<derivation>.
	Name() string
	// AfterPasses are the passes that run it: a kind, or a kind and a provider ("kind/provider").
	AfterPasses() []string
	// Cadence is its cadence; zero runs it on its other triggers alone.
	Cadence() time.Duration
	// Note records a change; Wake delivers once while a change is pending.
	Note(change DerivationChange)
	Wake() <-chan struct{}
	// Run runs it over what was noted since its last successful run. It is never run concurrently with
	// itself: a second call waits for the first.
	Run(ctx context.Context, session Session, now int64) (DerivationOutcome, error)
}

// AddDerivation adds a plugin's derivation.
func (s *Set) AddDerivation(derivation Derivation) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.derivations = append(s.derivations, derivation)
}

// Derivations returns every derivation, in load order.
func (s *Set) Derivations() []Derivation {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	return append([]Derivation(nil), s.derivations...)
}
