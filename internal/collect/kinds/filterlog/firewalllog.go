package filterlog

import (
	"context"
	"fmt"

	"github.com/NaejEL/opnview/internal/collect/core"
	"github.com/NaejEL/opnview/internal/collect/derive"
	"github.com/NaejEL/opnview/internal/collect/provider"
	"github.com/NaejEL/opnview/internal/store/vocab"
)

// The firewall_log kind.
//
// Everything here is true of any source of packet-level records, whichever product produced
// them: the two first-class join keys are resolved, a client identity is composed, and a gap
// is detected. The product-specific half — which endpoint, which field names, which words for
// an action — is in that implementation's own directory.
//
// THE GAP DETECTION IS THE POINT OF THIS FILE. There is no server-side resume point for this
// kind on OPNsense: the filter log's `digest` is a de-duplication key and not a cursor,
// measured twice. So what works instead is to pull the most recent page, discard what the
// digest says has already been seen — the uniqueness the schema already has — and DECLARE A
// GAP when the oldest record returned is newer than the newest record stored. The gap is then
// detected rather than assumed, and the poll has to be sized per installation rather than
// fixed.

// CollectFirewallLog runs one pass of the active implementation of the firewall_log kind.
func (c *FilterLogCoordinator) CollectFirewallLog(ctx context.Context) error {
	providerKey, providerID, active, err := c.ActiveSourceKey(ctx, core.KindFirewallLog)
	if err != nil {
		return err
	}
	if !active {
		// No implementation of this kind is active, which the probe round decided and recorded.
		// Collecting anyway would be reading a source opnview has just said it cannot read.
		return nil
	}
	source, registered := c.SourceSet.Lookup(Kind, providerKey)
	if !registered {
		return fmt.Errorf("collect: the active firewall_log provider %q has no implementation",
			providerKey)
	}
	// While a push entry that replaces this provider's polling is alive and past its overlap, the
	// pass reads nothing: the push path answers (PS9, PS10; pushpaths.go).
	if skip, err := c.planPull(ctx, providerKey, providerID, nil); err != nil {
		return err
	} else if skip.skip {
		if err := c.WriteAvailability(ctx, providerID, vocab.StateReachable, vocab.OutcomeHealthy,
			scriptlessProbe(providerKey), pushAnswers(providerKey)); err != nil {
			return err
		}
		return derive.On(c.Collector).Derive(ctx, derive.Derivation{})
	}

	// Every record the firewall logged before the page was asked for is in it, or in the gap the
	// pass records: the filter log's read-through instant, which the NetFlow join waits for
	// (internal/collect/derive/flowrecord.go).
	readStarted := c.Now()
	answer, result, readErr := source.Implementation.Call(ctx, c, "records", c.Pages().FirewallLog)
	records := make([]vocab.Flow, 0, len(provider.Records(answer, "records")))
	for _, record := range provider.Records(answer, "records") {
		records = append(records, reportedFlow(record))
	}
	if writeErr := c.WriteAvailability(ctx, providerID, result.State,
		result.Outcome, result.Probe, result.Detail); writeErr != nil {
		return writeErr
	}
	if readErr != nil {
		return readErr
	}
	if len(records) == 0 {
		// The refresh runs after every pass, an empty one included: the current slot
		// of each period is rewritten on every pass, because it is still filling.
		derive.On(c.Collector).SetFilterLogReadThrough(readStarted)
		return derive.On(c.Collector).Derive(ctx, derive.Derivation{})
	}

	newestStored, hasStored, err := c.Store.NewestFlowObservedAt(ctx)
	if err != nil {
		return err
	}
	plan, err := c.planPull(ctx, providerKey, providerID, records)
	if err != nil {
		return err
	}

	snapshot := c.Discovery()
	// No retention purge runs from here until the derivation has ended, so none can
	// move these rows into the purged part before a refresh has read them (purge.go).
	defer c.Storing()()
	// The instant every row of this pass is stamped with stays in flight until the
	// rows are written, so no refresh running meanwhile moves its watermark past it.
	now, ingest := derive.On(c.Collector).BeginIngest()
	defer derive.On(c.Collector).EndIngest(ingest)
	var stored derive.Derivation
	passErr := c.storeFirewallLog(ctx, records, plan, snapshot, now, providerID, newestStored, hasStored,
		&stored)
	if passErr == nil && plan.path != nil {
		// An overlap ends once polling has read up to a second the push path covers.
		accepted, err := c.handOver(ctx, providerID, plan.path, records)
		if err != nil {
			passErr = err
		} else if len(accepted) > 0 {
			passErr = c.storeRecords(ctx, accepted, snapshot, now, &stored)
		}
	}
	if c.Hooks.AfterStore != nil {
		c.Hooks.AfterStore()
	}

	if passErr == nil {
		derive.On(c.Collector).SetFilterLogReadThrough(readStarted)
	}
	// Every record of the page that was stored -- all of them, or those stored before
	// the pass failed -- can be placed from everything held, its flows attributed, and
	// the slots refreshed; a failure is still returned.
	derive.On(c.Collector).EndIngest(ingest)
	return derive.On(c.Collector).DeriveAfter(ctx, stored, passErr)
}

// storeFirewallLog stores the records of one page and runs the gap test, recording
// in stored every address and instant it wrote, so the derivation that follows
// reaches them even when this returns an error part-way. A record the retention purge
// has already removed is not stored again: store.InsertFlow refuses it inside the
// insert itself, so a purge committing at any moment of this loop is seen.
//
// THE SOURCE PERIODS DECIDE WHICH RECORDS ARE KEPT (plan, pushpaths.go): a record of an instant a
// push path covers is superseded, and a gap is clipped by what the push path stored.
func (c *FilterLogCoordinator) storeFirewallLog(ctx context.Context, records []vocab.Flow, plan pullPlan,
	snapshot core.Discovery, now, providerID, newestStored int64, hasStored bool, stored *derive.Derivation) error {
	oldestReturned := int64(0)
	for index, record := range records {
		if index == 0 || record.ObservedAt < oldestReturned {
			oldestReturned = record.ObservedAt
		}
	}
	if err := c.storeRecords(ctx, plan.keep, snapshot, now, stored); err != nil {
		return err
	}

	// The gap test, clipped by the newest push period: an interval whose records the push path
	// stored is never a gap.
	if plan.pushFloor > newestStored {
		newestStored, hasStored = plan.pushFloor, true
	}
	if hasStored && oldestReturned > newestStored {
		detail := fmt.Sprintf(
			"the page of %d records began after the newest record stored, so the interval between "+
				"them was not read; this kind has no cursor on OPNsense and the page size has to "+
				"be sized for this installation", len(records))
		if err := c.Store.RecordCollectionGap(ctx, vocab.CollectionGap{
			ProviderID:      providerID,
			IntervalStartAt: newestStored,
			IntervalEndAt:   oldestReturned,
			Reason:          vocab.GapDigestOutsideWindow,
			Detail:          &detail,
			DetectedAt:      now,
		}); err != nil {
			return err
		}
	}
	return nil
}

// reportedFlow is a checked firewall_log record as a row holding what the source reported; buildFlow
// adds what the core derives.
func reportedFlow(record provider.Answer) vocab.Flow {
	return vocab.Flow{
		LogDigest: provider.Text(record, "digest"), ObservedAt: provider.Integer(record, "observed_at"),
		InterfaceDevice: provider.Text(record, "device"), SrcAddress: provider.Text(record, "src_address"),
		DstAddress: provider.Text(record, "dst_address"), SrcPort: provider.IntegerPointer(record, "src_port"),
		DstPort: provider.IntegerPointer(record, "dst_port"), Protocol: provider.Text(record, "protocol"),
		IPVersion: provider.Integer(record, "ip_version"), Action: provider.Text(record, "action"),
		Direction: provider.Text(record, "direction"), LogReason: provider.TextPointer(record, "log_reason"),
		PacketBytes: provider.Integer(record, "packet_bytes"), Rid: provider.TextPointer(record, "rid"),
		IPID: provider.IntegerPointer(record, "ip_id"), TCPSeq: provider.IntegerPointer(record, "tcp_seq"),
	}
}

// buildFlow turns one record into a row, resolving the two join keys.
//
// Neither failure drops the row: a device name absent from the map is `not_found`, a rid
// matching no rule is `not_found`, and both are states the screens render. A dropped row would
// lose a packet that really crossed the firewall, which is the opposite of what this product
// is for.
//
// NEITHER END IS PLACED HERE. Which interface an address belongs to, and which client stands behind
// it, is decided from on-link evidence once the whole page is stored
// (internal/collect/derive/derive.go and internal/store/flows/classify.go), because the evidence
// for one record can arrive in a later one. Placing an end from the record alone is how a remote
// address used to become a client row on whatever interface happened to log it first. Until the
// derivation runs the row is north-south with no interface on either end, which is what the
// schema's CHECK requires of an unplaced row.
func buildFlow(record vocab.Flow, snapshot core.Discovery, now int64) vocab.Flow {
	interfaceState := vocab.LookupNotFound
	switch {
	case snapshot.RefreshedAt == 0:
		// Discovery has not run yet, so the key has not been looked up rather than looked up
		// and missed. The two are different facts.
		interfaceState = vocab.LookupPending
	default:
		if _, present := snapshot.InterfaceIDByDevice[record.InterfaceDevice]; present {
			interfaceState = vocab.LookupResolved
		}
	}

	ruleState := vocab.LookupNotFound
	var ruleID *int64
	switch {
	case snapshot.RefreshedAt == 0:
		ruleState = vocab.LookupPending
	case record.Rid == nil:
		// The record named no rule at all, so there is nothing to look up. That is not the same
		// as a rid that resolved to nothing.
		ruleState = vocab.LookupPending
	default:
		if id, present := snapshot.RuleIDByPfLabel[*record.Rid]; present {
			ruleState = vocab.LookupResolved
			ruleID = &id
		}
	}

	flow := record
	flow.IngestedAt = now
	flow.InterfaceLookupState = interfaceState
	flow.RuleID, flow.RuleLookupState = ruleID, ruleState
	return flow
}

// Kind is the kind this coordinator collects.
const Kind = "firewall_log"

func init() {
	core.RegisterPushHandler(Kind, core.PushHandler{
		Receive: func(ctx context.Context, c *core.Collector, key string, records []provider.Answer) error {
			return On(c).receivePushed(ctx, key, records)
		},
		Liveness: func(ctx context.Context, c *core.Collector, key string, alive bool, at int64) error {
			return On(c).pushLiveness(ctx, Kind, key, alive, at)
		},
	})
}
