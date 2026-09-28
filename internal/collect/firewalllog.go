package collect

import (
	"context"
	"fmt"

	"github.com/NaejEL/opnview/internal/store"
)

// The firewall_log kind.
//
// Everything here is true of any source of packet-level records, whichever product produced
// them: the two first-class join keys are resolved, a client identity is composed, and a gap
// is detected. The product-specific half — which endpoint, which field names, which words for
// an action — is in that implementation's own file.
//
// THE GAP DETECTION IS THE POINT OF THIS FILE. There is no server-side resume point for this
// kind on OPNsense: the filter log's `digest` is a de-duplication key and not a cursor,
// measured twice. So what works instead is to pull the most recent page, discard what the
// digest says has already been seen — the uniqueness the schema already has — and DECLARE A
// GAP when the oldest record returned is newer than the newest record stored. The gap is then
// detected rather than assumed, and the poll has to be sized per installation rather than
// fixed.

// CollectFirewallLog runs one pass of the active implementation of the firewall_log kind.
func (c *Collector) CollectFirewallLog(ctx context.Context) error {
	providerKey, providerID, active, err := c.activeSourceKey(ctx, KindFirewallLog)
	if err != nil {
		return err
	}
	if !active {
		// No implementation of this kind is active, which the probe round decided and recorded.
		// Collecting anyway would be reading a source opnview has just said it cannot read.
		return nil
	}
	source, registered := firewallLogSources[providerKey]
	if !registered {
		return fmt.Errorf("collect: the active firewall_log provider %q has no implementation",
			providerKey)
	}

	records, result, readErr := source.records(ctx, c)
	if writeErr := c.writeAvailability(ctx, providerID, result.state,
		result.probe, result.detail); writeErr != nil {
		return writeErr
	}
	if readErr != nil {
		return readErr
	}
	if len(records) == 0 {
		return nil
	}

	newestStored, hasStored, err := c.store.NewestFlowObservedAt(ctx)
	if err != nil {
		return err
	}

	snapshot := c.Discovery()
	now := c.now()
	oldestReturned := int64(0)

	for index, record := range records {
		if index == 0 || record.ObservedAt < oldestReturned {
			oldestReturned = record.ObservedAt
		}

		stored, err := c.store.HasFlowDigest(ctx, record.Digest)
		if err != nil {
			return err
		}
		if stored {
			// The endpoint echoes back records already seen, and the schema's uniqueness would
			// reject them anyway. Skipping here keeps the pass from doing work for a row that
			// cannot land.
			continue
		}

		flow, err := c.buildFlow(ctx, record, snapshot, now)
		if err != nil {
			return err
		}
		if err := c.store.InsertFlow(ctx, flow); err != nil {
			return err
		}
	}

	// The gap test.
	if hasStored && oldestReturned > newestStored {
		detail := fmt.Sprintf(
			"the page of %d records began after the newest record stored, so the interval between "+
				"them was not read; this kind has no cursor on OPNsense and the page size has to "+
				"be sized for this installation", len(records))
		if err := c.store.RecordCollectionGap(ctx, store.CollectionGap{
			ProviderID:      providerID,
			IntervalStartAt: newestStored,
			IntervalEndAt:   oldestReturned,
			Reason:          store.GapDigestOutsideWindow,
			Detail:          &detail,
			DetectedAt:      now,
		}); err != nil {
			return err
		}
	}
	return nil
}

// buildFlow turns one record into a row, resolving the two join keys.
//
// Neither failure drops the row: a device name absent from the map is `not_found`, a rid
// matching no rule is `not_found`, and both are states the screens render. A dropped row would
// lose a packet that really crossed the firewall, which is the opposite of what this product
// is for.
func (c *Collector) buildFlow(ctx context.Context, record logRecord, snapshot Discovery,
	now int64) (store.Flow, error) {
	interfaceState := store.LookupNotFound
	var loggedInterfaceID *int64
	switch {
	case snapshot.RefreshedAt == 0:
		// Discovery has not run yet, so the key has not been looked up rather than looked up
		// and missed. The two are different facts.
		interfaceState = store.LookupPending
	default:
		if id, present := snapshot.InterfaceIDByDevice[record.Device]; present {
			interfaceState = store.LookupResolved
			loggedInterfaceID = &id
		}
	}

	// The record names ONE interface: the one the packet crossed. Which end of the conversation
	// that is comes from the direction, whose meaning the survey establishes. Everything else
	// comes from the machines known at the two addresses, each of which carries its own
	// interface. traffic_scope then derives from interface membership alone, which is the only
	// thing it is ever allowed to derive from.
	var srcHint, dstHint *int64
	switch record.Direction {
	case "in":
		srcHint = loggedInterfaceID
	case "out":
		dstHint = loggedInterfaceID
	}

	srcClientID, srcInterfaceID, err := c.clientForAddress(ctx, srcHint, record.SrcAddress, now)
	if err != nil {
		return store.Flow{}, err
	}
	dstClientID, dstInterfaceID, err := c.clientForAddress(ctx, dstHint, record.DstAddress, now)
	if err != nil {
		return store.Flow{}, err
	}

	ruleState := store.LookupNotFound
	var ruleID *int64
	switch {
	case snapshot.RefreshedAt == 0:
		ruleState = store.LookupPending
	case record.Rid == nil:
		// The record named no rule at all, so there is nothing to look up. That is not the same
		// as a rid that resolved to nothing.
		ruleState = store.LookupPending
	default:
		if id, present := snapshot.RuleIDByPfLabel[*record.Rid]; present {
			ruleState = store.LookupResolved
			ruleID = &id
		}
	}

	return store.Flow{
		LogDigest:            record.Digest,
		ObservedAt:           record.ObservedAt,
		IngestedAt:           now,
		InterfaceDevice:      record.Device,
		InterfaceLookupState: interfaceState,
		SrcInterfaceID:       srcInterfaceID,
		DstInterfaceID:       dstInterfaceID,
		SrcClientID:          srcClientID,
		DstClientID:          dstClientID,
		SrcAddress:           record.SrcAddress,
		DstAddress:           record.DstAddress,
		SrcPort:              record.SrcPort,
		DstPort:              record.DstPort,
		Protocol:             record.Protocol,
		IPVersion:            record.IPVersion,
		Action:               record.Action,
		Direction:            record.Direction,
		LogReason:            record.LogReason,
		PacketBytes:          record.PacketBytes,
		Rid:                  record.Rid,
		RuleID:               ruleID,
		RuleLookupState:      ruleState,
	}, nil
}
