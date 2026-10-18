package core

import (
	"context"
	"fmt"
	"time"

	"github.com/NaejEL/opnview/internal/broker"
	"github.com/NaejEL/opnview/internal/broker/schedule"
	"github.com/NaejEL/opnview/internal/collect/provider"
	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/decode"
	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/store/vocab"
)

// Clock is the time the scheduler and the collectors read. It is an interface so
// that a test drives every loop deterministically instead of sleeping.
type Clock interface {
	// Now is the current instant.
	Now() time.Time
	// After returns a channel that delivers once, after d.
	After(d time.Duration) <-chan time.Time
}

// SystemClock is the real clock: the broker's, which is the same wall clock.
type SystemClock = schedule.SystemClock

// Discovery is the snapshot of the firewall's own configuration the collectors join against.
// It is declared in the provider contract, because the measurement sampler is handed it.
type Discovery = provider.Discovery

// NewDiscovery returns an empty snapshot. Empty is the honest starting state: on
// the first pass nothing is discovered, so every join key reports `pending`
// rather than `not_found`.
func NewDiscovery() Discovery {
	return Discovery{
		InterfaceIDByIdentifier: map[string]int64{},
		InterfaceIDByDevice:     map[string]int64{},
		Devices:                 map[string]struct{}{},
		RuleIDByPfLabel:         map[string]int64{},
		Upstream:                map[int64]struct{}{},
	}
}

// Broker is the collector's broker: the one path to the firewall API.
func (c *Collector) Broker() *broker.Broker { return c.APIBroker }

// Vocabulary is the installation's interface identifiers and devices, which mark an object keyed
// by them as value-keyed in the mirror. They are read from discovery, never written into a schema.
func (c *Collector) Vocabulary() []string {
	snapshot := c.Discovery()
	names := make([]string, 0, len(snapshot.InterfaceIDByIdentifier)+len(snapshot.Devices))
	for identifier := range snapshot.InterfaceIDByIdentifier {
		names = append(names, identifier)
	}
	for device := range snapshot.Devices {
		names = append(names, device)
	}
	return names
}

// Configure applies a configuration to a running collector: the page sizes and the
// attribution delay, which are the parts of it the collectors read. The intervals reach the scheduler
// through config.Live, not through here. The next pass of each collector uses the
// new pages; a pass already running finishes with the ones it started with. It is
// what web.Reconfigurable asks of a collector.
func (c *Collector) Configure(settings config.Config) {
	c.Mutex.Lock()
	defer c.Mutex.Unlock()
	c.Cadences = settings
	if settings.AttributionMaxDelaySeconds > 0 {
		c.MaxDelaySeconds = settings.AttributionMaxDelaySeconds
	}
	if settings.CacheAnswerDelaySeconds > 0 {
		c.CacheAnswerDelaySeconds = settings.CacheAnswerDelaySeconds
	}
	if settings.LegPairingWindowSeconds >= 0 {
		c.PairingWindowSeconds = settings.LegPairingWindowSeconds
	}
	if settings.LeaseBackendFailurePassLimit > 0 {
		c.LeaseFailureLimit = settings.LeaseBackendFailurePassLimit
	}
}

// SetSampleAbsence records what one measurement provider's last pass could not read.
func (c *Collector) SetSampleAbsence(providerID int64, absent string) {
	c.Mutex.Lock()
	defer c.Mutex.Unlock()
	if c.SampleAbsences == nil {
		c.SampleAbsences = map[int64]string{}
	}
	if absent == "" {
		delete(c.SampleAbsences, providerID)
		return
	}
	c.SampleAbsences[providerID] = absent
}

// SetReadDetail records what one resolver-record provider's last read said.
//
// The outcome is kept beside the words for the same reason: a probe round restating a
// read's refusal in the detail must not stamp the row `healthy` beside it.
func (c *Collector) SetReadDetail(providerID int64, outcome vocab.AvailabilityOutcome, detail string) {
	c.Mutex.Lock()
	defer c.Mutex.Unlock()
	if c.ReadDetails == nil {
		c.ReadDetails = map[int64]string{}
	}
	if c.ReadOutcomes == nil {
		c.ReadOutcomes = map[int64]vocab.AvailabilityOutcome{}
	}
	c.ReadDetails[providerID] = detail
	c.ReadOutcomes[providerID] = outcome
}

// readDetail is what one resolver-record provider's last read said, or empty.
func (c *Collector) readDetail(providerID int64) string {
	c.Mutex.RLock()
	defer c.Mutex.RUnlock()
	return c.ReadDetails[providerID]
}

// readOutcome is the outcome of one resolver-record provider's last read, or empty.
func (c *Collector) readOutcome(providerID int64) vocab.AvailabilityOutcome {
	c.Mutex.RLock()
	defer c.Mutex.RUnlock()
	return c.ReadOutcomes[providerID]
}

// sampleAbsence is what one provider's last sampling pass could not read, or empty.
func (c *Collector) sampleAbsence(providerID int64) string {
	c.Mutex.RLock()
	defer c.Mutex.RUnlock()
	return c.SampleAbsences[providerID]
}

// PageSize returns the page a core kind's paged read asks for now: the setting its kind file names, as
// the collection surface last saved it, or the file's default. The setting rows are the truth; this is
// what the collectors were last told.
func (c *Collector) PageSize(kind string) int { return c.intervals().PageSize(kind) }

// Discovery returns the current snapshot.
func (c *Collector) Discovery() Discovery {
	c.Mutex.RLock()
	defer c.Mutex.RUnlock()
	return c.DiscoverySnapshot
}

// SetDiscovery replaces the snapshot.
func (c *Collector) SetDiscovery(snapshot Discovery) {
	c.Mutex.Lock()
	defer c.Mutex.Unlock()
	c.DiscoverySnapshot = snapshot
}

// MeasureNormaliser returns the timestamp normaliser for this instant, carrying the
// reference time the year-less filter-log shape needs and the firewall's offset
// from UTC.
//
// NO ZONE-LESS TIMESTAMP IS READ BEFORE THE OFFSET IS KNOWN. At start the collectors
// and discovery run side by side, so a collector that gets here first measures the
// offset itself; if that fails, its pass fails and is retried, rather than storing
// records at a guessed time that nothing would later correct.
func (c *Collector) MeasureNormaliser(ctx context.Context) (decode.Normaliser, error) {
	c.Mutex.RLock()
	offset, measured := c.OffsetSeconds, c.OffsetMeasured
	c.Mutex.RUnlock()
	if !measured {
		if err := c.DiscoverClock(ctx); err != nil {
			return decode.Normaliser{}, fmt.Errorf("collect: the firewall's offset from UTC "+
				"is not known yet, so no zone-less timestamp is read: %w", err)
		}
		offset = c.FirewallOffsetSeconds()
	}
	return decode.Normaliser{Reference: c.Clock.Now(), OffsetSeconds: offset}, nil
}

// FirewallOffsetSeconds is the firewall's offset from UTC as discovery last measured
// it, and zero before it has.
func (c *Collector) FirewallOffsetSeconds() int {
	c.Mutex.RLock()
	defer c.Mutex.RUnlock()
	return c.OffsetSeconds
}

// Now is the current instant as the UTC epoch every column stores.
func (c *Collector) Now() int64 { return c.Clock.Now().UTC().Unix() }

// WriteAvailability records a state determined by one probe endpoint, with the outcome
// code that names why. An empty outcome is refused rather than defaulted: a code nobody
// chose is a code that would have to be guessed back from the detail.
func (c *Collector) WriteAvailability(ctx context.Context, providerID int64,
	state vocab.AvailabilityState, outcome vocab.AvailabilityOutcome, probe opnsense.Endpoint,
	detail string) error {
	if outcome == "" {
		return fmt.Errorf("collect: the availability of provider %d (%s %s) was written with no "+
			"outcome code", providerID, probe.Method, probe.Path)
	}
	var detailValue *string
	if detail != "" {
		detailValue = &detail
	}
	probeName := probe.Method + " " + probe.Path
	if err := c.Store.SetAvailability(ctx, providerID, state, outcome, probeName, detailValue,
		c.Now()); err != nil {
		return fmt.Errorf("collect: recording availability: %w", err)
	}
	return nil
}

// JoinErrors folds several failures into one error without losing any of them.
//
// It stayed in this package when the decoding layer moved out: a pass that reads five kinds
// reports every failure it met rather than the first, and that is a fact about how collection
// is organised, not about how a field is read.
func JoinErrors(errs []error) error {
	if len(errs) == 0 {
		return nil
	}
	joined := errs[0]
	for _, err := range errs[1:] {
		joined = fmt.Errorf("%w; %w", joined, err)
	}
	return joined
}

// Sources returns the implementations this collector reads.
func (c *Collector) Sources() *provider.Set { return c.SourceSet }

// DiscoverClock measures the firewall's offset from UTC from its own clock, survey
// gap 7, and puts it in force for every timestamp read after it. A failure leaves
// the last measurement in force rather than falling back to zero: a zone does not
// change because one request failed.
func (c *Collector) DiscoverClock(ctx context.Context) error {
	readAt := c.Clock.Now()
	body, ok, err := c.ReadObject(ctx, opnsense.SystemTime)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("collect: %s did not answer, so the firewall's offset from UTC "+
			"stays at %ds", opnsense.SystemTime.Path, c.FirewallOffsetSeconds())
	}
	datetime, present := decode.String(body, "datetime")
	if !present {
		return fmt.Errorf("collect: %s carries no datetime, so the firewall's offset from UTC "+
			"stays at %ds", opnsense.SystemTime.Path, c.FirewallOffsetSeconds())
	}
	offset, err := decode.FirewallOffsetSeconds(datetime, readAt)
	if err != nil {
		return err
	}
	c.Mutex.Lock()
	c.OffsetSeconds, c.OffsetMeasured = offset, true
	c.Mutex.Unlock()
	return nil
}

// Storing marks the start of a pass's store-and-derive: it waits for a purge in
// progress, and keeps the next one waiting until the returned function is called,
// once the pass's derivation has ended.
func (c *Collector) Storing() func() {
	c.PurgeMutex.RLock()
	return c.PurgeMutex.RUnlock
}
