package collect

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/NaejEL/opnview/internal/collect/firewalllog"
	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/store"
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
// INSTANT BELONGS TO ONE PATH: the provider's history is cut into source segments that never
// overlap (provider_source_segment), and a record is stored only when its event instant lies in a
// segment of the path that delivered it. A record of the other path is superseded: counted, not
// stored.
//
// FROM PULL TO PUSH. When the push entry becomes alive, polling goes on and the pushed records are
// held: the two paths overlap. During the overlap a pull pass stores nothing of the newest second its
// page returned, which the firewall may still have been writing. At the end of each pass the boundary
// is the second before that newest one: every second up to it was read completely by polling. Once
// the push path has been alive since before the boundary -- its first held record no later than it --
// the pull segment is closed at the boundary, a push segment opens after it, the held records after
// the boundary are stored and those up to it superseded, and polling stops (the pass is skipped:
// zero calls to the replaced endpoint).
//
// FROM PUSH TO PULL. When the push entry stops being alive, the boundary is the last second it
// delivered a record of; the push segment closes there and polling resumes at once. Pulled records up
// to the boundary are superseded, and a gap the pull path then detects is clipped by the push
// segment: no gap is recorded over an interval whose records were stored.
//
// RESIDUAL RISK, NAMED (Risk 41). The firewall writes a second's lines over the whole second, and
// the push path's last second is taken as delivered whole: a line of that second the push path lost
// in flight, as the connection died, is in neither path's segment. TCP delivers in order, so only the
// very end of a connection can lose such a line.

// PushedDigestPrefix begins the identity of every record a push path delivered: no pulled digest
// (32 hexadecimal digits) can carry it, so flow.log_digest UNIQUE holds across the two paths.
const PushedDigestPrefix = "push:"

// maxHeldPushed bounds the pushed records held during an overlap; past it the oldest are dropped
// and recorded as a push_buffer_overflow gap.
const maxHeldPushed = 50000

// The modes of a provider's paths.
const (
	modePull    = "pull"
	modeOverlap = "overlap"
	modePush    = "push"
)

// pushPaths is the state of every provider fed by two paths.
type pushPaths struct {
	mutex     sync.Mutex
	providers map[string]*pushPath
}

// pushPath is one provider's.
type pushPath struct {
	mutex    sync.Mutex
	loaded   bool
	mode     string
	segments []store.SourceSegment
	held     []firewalllog.Record
	// lastPushed is the newest instant the push path delivered in its current segment.
	lastPushed int64
	superseded int64
	dropped    int64
}

func pathKey(kind, key string) string { return kind + "/" + key }

func (p *pushPaths) of(kind, key string) *pushPath {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	if p.providers == nil {
		p.providers = map[string]*pushPath{}
	}
	path, found := p.providers[pathKey(kind, key)]
	if !found {
		path = &pushPath{mode: modePull}
		p.providers[pathKey(kind, key)] = path
	}
	return path
}

// known reports whether a provider has ever been fed by two paths in this run or before.
func (p *pushPaths) known(kind, key string) (*pushPath, bool) {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	path, found := p.providers[pathKey(kind, key)]
	return path, found
}

// load reads a provider's segments once. A provider whose open segment is push when opnview starts is
// returned to pull at the last instant the push path stored: whether the push source is alive is
// this run's to discover, and polling meanwhile is what loses nothing.
func (c *Collector) loadPushPath(ctx context.Context, path *pushPath, providerID int64) error {
	if path.loaded {
		return nil
	}
	segments, err := c.store.SourceSegments(ctx, providerID)
	if err != nil {
		return err
	}
	path.segments, path.mode, path.loaded = segments, modePull, true
	if len(segments) > 0 && segments[len(segments)-1].ToAt == nil && segments[len(segments)-1].Path == store.PathPush {
		boundary := segments[len(segments)-1].FromAt
		if newest, found, err := c.store.NewestPushedFlowObservedAt(ctx, PushedDigestPrefix); err != nil {
			return err
		} else if found && newest > boundary {
			boundary = newest
		}
		if err := c.store.OpenSourceSegment(ctx, providerID, store.PathPull, boundary); err != nil {
			return err
		}
		if path.segments, err = c.store.SourceSegments(ctx, providerID); err != nil {
			return err
		}
	}
	return nil
}

// pathAt is the path whose segment holds an instant; pull when no segment exists.
func (p *pushPath) pathAt(at int64) string {
	for index := len(p.segments) - 1; index >= 0; index-- {
		segment := p.segments[index]
		if at >= segment.FromAt && (segment.ToAt == nil || at <= *segment.ToAt) {
			return segment.Path
		}
	}
	return store.PathPull
}

// openSegment records a boundary and reloads the segments.
func (c *Collector) openSegment(ctx context.Context, path *pushPath, providerID int64, opened string,
	boundary int64) error {
	if err := c.store.OpenSourceSegment(ctx, providerID, opened, boundary); err != nil {
		return err
	}
	segments, err := c.store.SourceSegments(ctx, providerID)
	if err != nil {
		return err
	}
	path.segments = segments
	return nil
}

// PushLiveness is told when a push entry that replaces a polled endpoint becomes alive or stops
// being alive (PS10).
func (c *Collector) PushLiveness(ctx context.Context, kind, key string, alive bool, at int64) error {
	if kind != KindFirewallLog {
		return nil
	}
	providerID, err := c.store.ProviderID(ctx, kind, key)
	if err != nil {
		return err
	}
	path := c.pushPaths.of(kind, key)
	path.mutex.Lock()
	defer path.mutex.Unlock()
	if err := c.loadPushPath(ctx, path, providerID); err != nil {
		return err
	}
	switch {
	case alive && path.mode == modePull:
		path.mode, path.held = modeOverlap, nil
	case !alive && path.mode == modeOverlap:
		path.mode, path.held = modePull, nil
	case !alive && path.mode == modePush:
		boundary := path.lastPushed
		if boundary == 0 && len(path.segments) > 0 {
			boundary = path.segments[len(path.segments)-1].FromAt
		}
		if err := c.openSegment(ctx, path, providerID, store.PathPull, boundary); err != nil {
			return err
		}
		path.mode, path.lastPushed = modePull, 0
	}
	return nil
}

// ReceivePushedFirewallLog takes the records a push entry delivered for a firewall_log provider. In
// an overlap they are held; in a push segment they are stored as a pull pass stores its own and
// derived at once; otherwise they are superseded.
func (c *Collector) ReceivePushedFirewallLog(ctx context.Context, key string, records []firewalllog.Record) error {
	providerID, err := c.store.ProviderID(ctx, KindFirewallLog, key)
	if err != nil {
		return err
	}
	path := c.pushPaths.of(KindFirewallLog, key)
	path.mutex.Lock()
	if err := c.loadPushPath(ctx, path, providerID); err != nil {
		path.mutex.Unlock()
		return err
	}
	var kept []firewalllog.Record
	var overflowFrom, overflowTo int64
	switch path.mode {
	case modeOverlap:
		path.held = append(path.held, records...)
		if excess := len(path.held) - maxHeldPushed; excess > 0 {
			overflowFrom, overflowTo = path.held[0].ObservedAt, path.held[excess-1].ObservedAt
			path.held = append([]firewalllog.Record(nil), path.held[excess:]...)
			path.dropped += int64(excess)
		}
	case modePush:
		for _, record := range records {
			if path.pathAt(record.ObservedAt) != store.PathPush {
				path.superseded++
				continue
			}
			kept = append(kept, record)
			if record.ObservedAt > path.lastPushed {
				path.lastPushed = record.ObservedAt
			}
		}
	default:
		for _, record := range records {
			if path.pathAt(record.ObservedAt) == store.PathPush {
				kept = append(kept, record)
				continue
			}
			path.superseded++
		}
	}
	path.mutex.Unlock()
	if overflowTo != 0 {
		detail := "pushed records held for the handover from polling exceeded the bound and the oldest were dropped"
		if err := c.store.RecordPushGap(ctx, providerID, store.GapPushBufferOverflow, overflowFrom, overflowTo, detail,
			c.now()); err != nil {
			return err
		}
	}
	return c.storePushed(ctx, providerID, kept)
}

// storePushed stores pushed records and derives from them, as a pull pass does.
func (c *Collector) storePushed(ctx context.Context, providerID int64, records []firewalllog.Record) error {
	if len(records) == 0 {
		return nil
	}
	snapshot := c.Discovery()
	defer c.storing()()
	now, ingest := c.beginIngest()
	defer c.endIngest(ingest)
	var stored derivation
	passErr := c.storeRecords(ctx, records, snapshot, now, &stored)
	c.endIngest(ingest)
	if passErr == nil {
		// A TCP push path delivers in order: every line logged before the newest one delivered is in.
		newest := int64(0)
		for _, record := range records {
			newest = max(newest, record.ObservedAt)
		}
		c.setFilterLogReadThrough(newest)
	}
	return c.deriveAfter(ctx, stored, passErr)
}

// storeRecords stores records that are not stored yet, recording what it wrote in stored.
func (c *Collector) storeRecords(ctx context.Context, records []firewalllog.Record, snapshot Discovery, now int64,
	stored *derivation) error {
	for _, record := range records {
		seen, err := c.store.HasFlowDigest(ctx, record.Digest)
		if err != nil {
			return err
		}
		if seen {
			continue
		}
		if err := c.store.InsertFlow(ctx, buildFlow(record, snapshot, now)); err != nil {
			return err
		}
		stored.addresses = append(stored.addresses, record.SrcAddress, record.DstAddress)
		stored.widen(record.ObservedAt, record.ObservedAt)
		stored.widenPairing(record.ObservedAt, record.ObservedAt)
	}
	return nil
}

// pullPlan is what the source segments decide of one pull pass.
type pullPlan struct {
	// skip is set while a push segment is open: the pass reads nothing.
	skip bool
	// keep is the pulled records stored; superseded how many are not.
	keep       []firewalllog.Record
	superseded int64
	// pushFloor is the end of the newest closed push segment, which clips a gap the pull path detects.
	pushFloor int64
	path      *pushPath
}

// planPull applies the segments to a pull pass of a firewall_log provider. A provider never fed by
// two paths keeps every record.
func (c *Collector) planPull(ctx context.Context, key string, providerID int64, records []firewalllog.Record) (
	pullPlan, error) {
	path, known := c.pushPaths.known(KindFirewallLog, key)
	if !known {
		return pullPlan{keep: records}, nil
	}
	path.mutex.Lock()
	defer path.mutex.Unlock()
	if err := c.loadPushPath(ctx, path, providerID); err != nil {
		return pullPlan{}, err
	}
	plan := pullPlan{path: path}
	for _, segment := range path.segments {
		if segment.Path == store.PathPush && segment.ToAt != nil && *segment.ToAt > plan.pushFloor {
			plan.pushFloor = *segment.ToAt
		}
	}
	if path.mode == modePush {
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
		case path.mode == modeOverlap && record.ObservedAt >= newest:
			// The newest second may still have been written to: read again next pass.
			continue
		case path.pathAt(record.ObservedAt) != store.PathPull:
			plan.superseded++
			continue
		}
		plan.keep = append(plan.keep, record)
	}
	path.superseded += plan.superseded
	return plan, nil
}

// handOver ends an overlap after a pull pass that read up to newest: the boundary is the second
// before it, once the push path has been alive since before the boundary.
func (c *Collector) handOver(ctx context.Context, providerID int64, path *pushPath, records []firewalllog.Record) (
	[]firewalllog.Record, error) {
	path.mutex.Lock()
	defer path.mutex.Unlock()
	if path.mode != modeOverlap || len(path.held) == 0 || len(records) == 0 {
		return nil, nil
	}
	newest := int64(0)
	for _, record := range records {
		if record.ObservedAt > newest {
			newest = record.ObservedAt
		}
	}
	firstPushed := path.held[0].ObservedAt
	for _, record := range path.held {
		if record.ObservedAt < firstPushed {
			firstPushed = record.ObservedAt
		}
	}
	boundary := newest - 1
	if boundary < firstPushed {
		return nil, nil
	}
	if open := path.segments; len(open) > 0 && open[len(open)-1].ToAt == nil && boundary < open[len(open)-1].FromAt {
		return nil, nil
	}
	if err := c.openSegment(ctx, path, providerID, store.PathPush, boundary); err != nil {
		return nil, err
	}
	var accepted []firewalllog.Record
	for _, record := range path.held {
		if record.ObservedAt > boundary {
			accepted = append(accepted, record)
			if record.ObservedAt > path.lastPushed {
				path.lastPushed = record.ObservedAt
			}
			continue
		}
		path.superseded++
	}
	path.mode, path.held = modePush, nil
	return accepted, nil
}

// PushPathState is a provider's paths as the API catalogue lists them: its mode, its segments and
// its counters.
type PushPathState struct {
	Mode       string                `json:"mode"`
	Segments   []store.SourceSegment `json:"segments"`
	Held       int                   `json:"held"`
	Superseded int64                 `json:"superseded"`
	Dropped    int64                 `json:"dropped"`
}

// PushPathOf reports a provider's paths; ok is false for one never fed by two paths.
func (c *Collector) PushPathOf(kind, key string) (PushPathState, bool) {
	path, known := c.pushPaths.known(kind, key)
	if !known {
		return PushPathState{}, false
	}
	path.mutex.Lock()
	defer path.mutex.Unlock()
	state := PushPathState{Mode: path.mode, Segments: append([]store.SourceSegment{}, path.segments...),
		Held: len(path.held), Superseded: path.superseded, Dropped: path.dropped}
	sort.Slice(state.Segments, func(i, j int) bool { return state.Segments[i].FromAt < state.Segments[j].FromAt })
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
func (c *Collector) ZonelessInstant(ctx context.Context, text string) (time.Time, error) {
	normaliser, err := c.measureNormaliser(ctx)
	if err != nil {
		return time.Time{}, err
	}
	epoch, err := normaliser.NormaliseFilterLogTimestamp(text)
	if err != nil {
		return time.Time{}, err
	}
	return time.Unix(epoch, 0).UTC(), nil
}
