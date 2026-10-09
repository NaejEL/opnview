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

	records, result, readErr := source.records(ctx, c, c.pages().FirewallLog)
	if writeErr := c.writeAvailability(ctx, providerID, result.state,
		result.probe, result.detail); writeErr != nil {
		return writeErr
	}
	if readErr != nil {
		return readErr
	}
	if len(records) == 0 {
		// The refresh runs after every pass, an empty one included: the current slot
		// of each period is rewritten on every pass, because it is still filling.
		return c.derive(ctx, derivation{})
	}

	newestStored, hasStored, err := c.store.NewestFlowObservedAt(ctx)
	if err != nil {
		return err
	}

	snapshot := c.Discovery()
	// No retention purge runs from here until the derivation has ended, so none can
	// move these rows into the purged part before a refresh has read them (purge.go).
	defer c.storing()()
	// The instant every row of this pass is stamped with stays in flight until the
	// rows are written, so no refresh running meanwhile moves its watermark past it.
	now, ingest := c.beginIngest()
	defer c.endIngest(ingest)
	var stored derivation
	passErr := c.storeFirewallLog(ctx, records, snapshot, now, providerID, newestStored, hasStored,
		&stored)
	if c.hooks.afterStore != nil {
		c.hooks.afterStore()
	}

	// Every record of the page that was stored -- all of them, or those stored before
	// the pass failed -- can be placed from everything held, its flows attributed, and
	// the slots refreshed; a failure is still returned.
	c.endIngest(ingest)
	return c.deriveAfter(ctx, stored, passErr)
}

// storeFirewallLog stores the records of one page and runs the gap test, recording
// in stored every address and instant it wrote, so the derivation that follows
// reaches them even when this returns an error part-way. A record the retention purge
// has already removed is not stored again: store.InsertFlow refuses it inside the
// insert itself, so a purge committing at any moment of this loop is seen.
func (c *Collector) storeFirewallLog(ctx context.Context, records []logRecord, snapshot Discovery,
	now, providerID, newestStored int64, hasStored bool, stored *derivation) error {
	oldestReturned := int64(0)
	for index, record := range records {
		if index == 0 || record.ObservedAt < oldestReturned {
			oldestReturned = record.ObservedAt
		}

		seen, err := c.store.HasFlowDigest(ctx, record.Digest)
		if err != nil {
			return err
		}
		if seen {
			// The endpoint echoes back records already seen, and the schema's uniqueness would
			// reject them anyway. Skipping here keeps the pass from doing work for a row that
			// cannot land.
			continue
		}

		flow := buildFlow(record, snapshot, now)
		if err := c.store.InsertFlow(ctx, flow); err != nil {
			return err
		}
		stored.addresses = append(stored.addresses, record.SrcAddress, record.DstAddress)
		stored.widen(record.ObservedAt, record.ObservedAt)
		// Its other leg may be stored already, by this pass or an earlier one.
		stored.widenPairing(record.ObservedAt, record.ObservedAt)
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
//
// NEITHER END IS PLACED HERE. Which interface an address belongs to, and which client stands
// behind it, is decided from on-link evidence once the whole page is stored (derive.go and
// internal/store/classify.go), because the evidence for one record can arrive in a later one.
// Placing an end from the record alone is how a remote address used to become a client row on
// whatever interface happened to log it first. Until the derivation runs the row is
// north-south with no interface on either end, which is what the schema's CHECK requires of
// an unplaced row.
func buildFlow(record logRecord, snapshot Discovery, now int64) store.Flow {
	interfaceState := store.LookupNotFound
	switch {
	case snapshot.RefreshedAt == 0:
		// Discovery has not run yet, so the key has not been looked up rather than looked up
		// and missed. The two are different facts.
		interfaceState = store.LookupPending
	default:
		if _, present := snapshot.InterfaceIDByDevice[record.Device]; present {
			interfaceState = store.LookupResolved
		}
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
		IPID:                 record.IPID,
		TCPSeq:               record.TCPSeq,
	}
}
