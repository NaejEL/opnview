package filterlog

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/NaejEL/opnview/internal/collect/core"
	"github.com/NaejEL/opnview/internal/collect/derive"
	"github.com/NaejEL/opnview/internal/collect/firewalllog"
	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/store/push"
	"github.com/NaejEL/opnview/internal/store/vocab"
)

// A core kind fed by two paths: an exclusive source per instant, and nothing counted twice (PS9 of
// the push amendment to specs/SPEC-backend-completion-plugins.md).
//
// ONE PROVIDER, TWO PATHS. A provider of firewall_log may deliver its records by polling the filter
// log AND through a push entry -- the firewall's filterlog lines received over TCP syslog -- which
// declares that it replaces the polled endpoint. The two paths' record identities differ (a pushed
// line carries a header written for the push target, so it cannot reproduce the pulled digest;
// survey, Push sources, S4), so no identity could deduplicate them once stored. What keeps a
// connection from counting twice in flow, the pairing and every aggregate family is that EVERY
// INSTANT BELONGS TO ONE PATH: the provider's history is cut into source periods that never
// overlap (provider_source_period), and a record is stored only when its event instant lies in a
// period of the path that delivered it. A record of the other path is superseded: counted, not
// stored.
//
// FROM PULL TO PUSH. When the push entry becomes alive, polling goes on and the pushed records are
// held: the two paths overlap. During the overlap a pull pass stores nothing of the newest second its
// page returned, which the firewall may still have been writing. At the end of each pass the boundary
// is the second before that newest one: every second up to it was read completely by polling. Once
// the push path has been alive since before the boundary -- its first held record no later than it --
// the pull period is closed at the boundary, a push period opens after it, the held records after
// the boundary are stored and those up to it superseded, and polling stops (the pass is skipped:
// zero calls to the replaced endpoint).
//
// FROM PUSH TO PULL. When the push entry stops being alive, the boundary is the last second it
// delivered a record of; the push period closes there and polling resumes at once. Pulled records up
// to the boundary are superseded, and a gap the pull path then detects is clipped by the push
// period: no gap is recorded over an interval whose records were stored.
//
// RESIDUAL RISK, NAMED (Risk 41). The firewall writes a second's lines over the whole second, and
// the push path's last second is taken as delivered whole: a line of that second the push path lost
// in flight, as the connection died, is in neither path's period. TCP delivers in order, so only the
// very end of a connection can lose such a line.

// PushedDigestPrefix begins the identity of every record a push path delivered: no pulled digest
// (32 hexadecimal digits) can carry it, so flow.log_digest UNIQUE holds across the two paths.
const PushedDigestPrefix = "push:"

// maxHeldPushed bounds the pushed records held during an overlap; past it the oldest are dropped
// and recorded as a push_buffer_overflow gap.
const maxHeldPushed = 50000

// load reads a provider's periods once. A provider whose open period is push when opnview starts is
// returned to pull at the last instant the push path stored: whether the push source is alive is
// this run's to discover, and polling meanwhile is what loses nothing.
func (c *FilterLogCoordinator) loadPushPath(ctx context.Context, path *core.PushPath, providerID int64) error {
	if path.Loaded {
		return nil
	}
	periods, err := c.Store.SourcePeriods(ctx, providerID)
	if err != nil {
		return err
	}
	path.Periods, path.Mode, path.Loaded = periods, core.ModePull, true
	if len(periods) > 0 && periods[len(periods)-1].ToAt == nil && periods[len(periods)-1].Path == push.PathPush {
		boundary := periods[len(periods)-1].FromAt
		if newest, found, err := c.Store.NewestPushedFlowObservedAt(ctx, PushedDigestPrefix); err != nil {
			return err
		} else if found && newest > boundary {
			boundary = newest
		}
		if err := c.Store.OpenSourcePeriod(ctx, providerID, push.PathPull, boundary); err != nil {
			return err
		}
		if path.Periods, err = c.Store.SourcePeriods(ctx, providerID); err != nil {
			return err
		}
	}
	return nil
}

// openPeriod records a boundary and reloads the periods.
func (c *FilterLogCoordinator) openPeriod(ctx context.Context, path *core.PushPath, providerID int64, opened string,
	boundary int64) error {
	if err := c.Store.OpenSourcePeriod(ctx, providerID, opened, boundary); err != nil {
		return err
	}
	periods, err := c.Store.SourcePeriods(ctx, providerID)
	if err != nil {
		return err
	}
	path.Periods = periods
	return nil
}

// PushLiveness is told when a push entry that replaces a polled endpoint becomes alive or stops
// being alive (PS10).
func (c *FilterLogCoordinator) PushLiveness(ctx context.Context, kind, key string, alive bool, at int64) error {
	if kind != core.KindFirewallLog {
		return nil
	}
	providerID, err := c.Store.ProviderID(ctx, kind, key)
	if err != nil {
		return err
	}
	path := c.PushPaths.Of(kind, key)
	path.Mutex.Lock()
	defer path.Mutex.Unlock()
	if err := c.loadPushPath(ctx, path, providerID); err != nil {
		return err
	}
	switch {
	case alive && path.Mode == core.ModePull:
		path.Mode, path.Held = core.ModeOverlap, nil
	case !alive && path.Mode == core.ModeOverlap:
		path.Mode, path.Held = core.ModePull, nil
	case !alive && path.Mode == core.ModePush:
		boundary := path.LastPushed
		if boundary == 0 && len(path.Periods) > 0 {
			boundary = path.Periods[len(path.Periods)-1].FromAt
		}
		if err := c.openPeriod(ctx, path, providerID, push.PathPull, boundary); err != nil {
			return err
		}
		path.Mode, path.LastPushed = core.ModePull, 0
	}
	return nil
}

// ReceivePushedFirewallLog takes the records a push entry delivered for a firewall_log provider. In
// an overlap they are held; in a push period they are stored as a pull pass stores its own and
// derived at once; otherwise they are superseded.
func (c *FilterLogCoordinator) ReceivePushedFirewallLog(ctx context.Context, key string, records []firewalllog.Record) error {
	providerID, err := c.Store.ProviderID(ctx, core.KindFirewallLog, key)
	if err != nil {
		return err
	}
	path := c.PushPaths.Of(core.KindFirewallLog, key)
	path.Mutex.Lock()
	if err := c.loadPushPath(ctx, path, providerID); err != nil {
		path.Mutex.Unlock()
		return err
	}
	var kept []firewalllog.Record
	var overflowFrom, overflowTo int64
	switch path.Mode {
	case core.ModeOverlap:
		path.Held = append(path.Held, records...)
		if excess := len(path.Held) - maxHeldPushed; excess > 0 {
			overflowFrom, overflowTo = path.Held[0].ObservedAt, path.Held[excess-1].ObservedAt
			path.Held = append([]firewalllog.Record(nil), path.Held[excess:]...)
			path.Dropped += int64(excess)
		}
	case core.ModePush:
		for _, record := range records {
			if path.PathAt(record.ObservedAt) != push.PathPush {
				path.Superseded++
				continue
			}
			kept = append(kept, record)
			if record.ObservedAt > path.LastPushed {
				path.LastPushed = record.ObservedAt
			}
		}
	default:
		for _, record := range records {
			if path.PathAt(record.ObservedAt) == push.PathPush {
				kept = append(kept, record)
				continue
			}
			path.Superseded++
		}
	}
	path.Mutex.Unlock()
	if overflowTo != 0 {
		detail := "pushed records held for the handover from polling exceeded the bound and the oldest were dropped"
		if err := c.Store.RecordPushGap(ctx, providerID, vocab.GapPushBufferOverflow, overflowFrom, overflowTo, detail,
			c.Now()); err != nil {
			return err
		}
	}
	return c.storePushed(ctx, providerID, kept)
}

// storePushed stores pushed records and derives from them, as a pull pass does.
func (c *FilterLogCoordinator) storePushed(ctx context.Context, providerID int64, records []firewalllog.Record) error {
	if len(records) == 0 {
		return nil
	}
	snapshot := c.Discovery()
	defer c.Storing()()
	now, ingest := derive.On(c.Collector).BeginIngest()
	defer derive.On(c.Collector).EndIngest(ingest)
	var stored derive.Derivation
	passErr := c.storeRecords(ctx, records, snapshot, now, &stored)
	derive.On(c.Collector).EndIngest(ingest)
	if passErr == nil {
		// A TCP push path delivers in order: every line logged before the newest one delivered is in.
		newest := int64(0)
		for _, record := range records {
			newest = max(newest, record.ObservedAt)
		}
		derive.On(c.Collector).SetFilterLogReadThrough(newest)
	}
	return derive.On(c.Collector).DeriveAfter(ctx, stored, passErr)
}

// storeRecords stores records that are not stored yet, recording what it wrote in stored.
func (c *FilterLogCoordinator) storeRecords(ctx context.Context, records []firewalllog.Record, snapshot core.Discovery, now int64,
	stored *derive.Derivation) error {
	for _, record := range records {
		seen, err := c.Store.HasFlowDigest(ctx, record.Digest)
		if err != nil {
			return err
		}
		if seen {
			continue
		}
		if err := c.Store.InsertFlow(ctx, buildFlow(record, snapshot, now)); err != nil {
			return err
		}
		stored.Addresses = append(stored.Addresses, record.SrcAddress, record.DstAddress)
		stored.Widen(record.ObservedAt, record.ObservedAt)
		stored.WidenPairing(record.ObservedAt, record.ObservedAt)
	}
	return nil
}

// pullPlan is what the source periods decide of one pull pass.
type pullPlan struct {
	// skip is set while a push period is open: the pass reads nothing.
	skip bool
	// keep is the pulled records stored; superseded how many are not.
	keep       []firewalllog.Record
	superseded int64
	// pushFloor is the end of the newest closed push period, which clips a gap the pull path detects.
	pushFloor int64
	path      *core.PushPath
}

// planPull applies the periods to a pull pass of a firewall_log provider. A provider never fed by
// two paths keeps every record.
func (c *FilterLogCoordinator) planPull(ctx context.Context, key string, providerID int64, records []firewalllog.Record) (
	pullPlan, error) {
	path, known := c.PushPaths.Known(core.KindFirewallLog, key)
	if !known {
		return pullPlan{keep: records}, nil
	}
	path.Mutex.Lock()
	defer path.Mutex.Unlock()
	if err := c.loadPushPath(ctx, path, providerID); err != nil {
		return pullPlan{}, err
	}
	plan := pullPlan{path: path}
	for _, period := range path.Periods {
		if period.Path == push.PathPush && period.ToAt != nil && *period.ToAt > plan.pushFloor {
			plan.pushFloor = *period.ToAt
		}
	}
	if path.Mode == core.ModePush {
		plan.skip = true
		return plan, nil
	}
	newest := int64(0)
	for _, record := range records {
		if record.ObservedAt > newest {
			newest = record.ObservedAt
		}
	}
	for _, record := range records {
		switch {
		case path.Mode == core.ModeOverlap && record.ObservedAt >= newest:
			// The newest second may still have been written to: read again next pass.
			continue
		case path.PathAt(record.ObservedAt) != push.PathPull:
			plan.superseded++
			continue
		}
		plan.keep = append(plan.keep, record)
	}
	path.Superseded += plan.superseded
	return plan, nil
}

// handOver ends an overlap after a pull pass that read up to newest: the boundary is the second
// before it, once the push path has been alive since before the boundary.
func (c *FilterLogCoordinator) handOver(ctx context.Context, providerID int64, path *core.PushPath, records []firewalllog.Record) (
	[]firewalllog.Record, error) {
	path.Mutex.Lock()
	defer path.Mutex.Unlock()
	if path.Mode != core.ModeOverlap || len(path.Held) == 0 || len(records) == 0 {
		return nil, nil
	}
	newest := int64(0)
	for _, record := range records {
		if record.ObservedAt > newest {
			newest = record.ObservedAt
		}
	}
	firstPushed := path.Held[0].ObservedAt
	for _, record := range path.Held {
		if record.ObservedAt < firstPushed {
			firstPushed = record.ObservedAt
		}
	}
	boundary := newest - 1
	if boundary < firstPushed {
		return nil, nil
	}
	if open := path.Periods; len(open) > 0 && open[len(open)-1].ToAt == nil && boundary < open[len(open)-1].FromAt {
		return nil, nil
	}
	if err := c.openPeriod(ctx, path, providerID, push.PathPush, boundary); err != nil {
		return nil, err
	}
	var accepted []firewalllog.Record
	for _, record := range path.Held {
		if record.ObservedAt > boundary {
			accepted = append(accepted, record)
			if record.ObservedAt > path.LastPushed {
				path.LastPushed = record.ObservedAt
			}
			continue
		}
		path.Superseded++
	}
	path.Mode, path.Held = core.ModePush, nil
	return accepted, nil
}

// PushPathState is a provider's paths as the API catalogue lists them: its mode, its periods and
// its counters.
type PushPathState struct {
	Mode       string              `json:"mode"`
	Periods    []push.SourcePeriod `json:"source_periods"`
	Held       int                 `json:"held"`
	Superseded int64               `json:"superseded"`
	Dropped    int64               `json:"dropped"`
}

// PushPathOf reports a provider's paths; ok is false for one never fed by two paths.
func (c *FilterLogCoordinator) PushPathOf(kind, key string) (PushPathState, bool) {
	path, known := c.PushPaths.Known(kind, key)
	if !known {
		return PushPathState{}, false
	}
	path.Mutex.Lock()
	defer path.Mutex.Unlock()
	state := PushPathState{Mode: path.Mode, Periods: append([]push.SourcePeriod{}, path.Periods...),
		Held: len(path.Held), Superseded: path.Superseded, Dropped: path.Dropped}
	sort.Slice(state.Periods, func(i, j int) bool { return state.Periods[i].FromAt < state.Periods[j].FromAt })
	return state, true
}

// scriptlessProbe is what a skipped pass's availability names as its probe: the push path, not an
// endpoint.
func scriptlessProbe(key string) opnsense.Endpoint {
	return opnsense.Endpoint{Method: "push", Path: key}
}

// pushAnswers is the detail a skipped pull pass records: the plugin is told which path answered.
func pushAnswers(key string) string {
	return fmt.Sprintf("the %s push source answers: its push entry replaces polling while it is alive, so this "+
		"pass read nothing", key)
}

// ZonelessInstant reads a timestamp that carries no zone -- an RFC 3164 header's, or an ISO one
// without an offset -- with the firewall's measured offset from UTC and the year-less rule of the
// normaliser (PS5): the same function the pulled filter log's timestamps go through.
func (c *FilterLogCoordinator) ZonelessInstant(ctx context.Context, text string) (time.Time, error) {
	normaliser, err := c.MeasureNormaliser(ctx)
	if err != nil {
		return time.Time{}, err
	}
	epoch, err := normaliser.NormaliseFilterLogTimestamp(text)
	if err != nil {
		return time.Time{}, err
	}
	return time.Unix(epoch, 0).UTC(), nil
}
