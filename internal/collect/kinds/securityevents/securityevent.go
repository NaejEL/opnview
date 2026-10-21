package securityevents

import (
	"context"
	"fmt"
	"strconv"

	"github.com/NaejEL/opnview/internal/collect/core"
	"github.com/NaejEL/opnview/internal/collect/derive"
	"github.com/NaejEL/opnview/internal/collect/provider"
	"github.com/NaejEL/opnview/internal/store/flows"
	"github.com/NaejEL/opnview/internal/store/vocab"
)

// The security_event kind.
//
// What is true of any source of security events, and is therefore here: the durable cursor,
// the rotation detection, the client resolution, and the gap row a permanent loss earns.
//
// THE CURSOR IS A WATERMARK AND NOT A PAGE POSITION. Paging over this kind's material counts
// offsets from the end of the file, so any newly appended record shifts every offset and a
// page number cannot be resumed across polls. The durable resume point is (file, offset),
// stored per file.
//
// A ROTATION THAT DISCARDED A WATERMARKED FILE IS A PERMANENT LOSS. It is recorded twice — as
// a rotation state on the watermark, and as a gap row — and the watermark is NOT reset,
// because a reset would make the loss look like a successful read of nothing.

// CollectSecurityEvent runs one pass of every active implementation of the security_event kind.
//
// SEVERAL ARE NORMAL HERE, and this kind is the reason the one-active-provider-per-kind rule
// went: people run Suricata, CrowdSec and Zenarmor together, and the how-to corpus is people
// stacking them. Event identity is (provider_id, provider_event_key), so two sources reporting
// the same intrusion are two attributed rows rather than one doubled count, and a screen can
// show them per provider. One failing source does not stop the others.
func (c *SecurityEventCoordinator) CollectSecurityEvent(ctx context.Context) error {
	sources, err := c.ActiveSources(ctx, Kind)
	if err != nil {
		return err
	}
	var failures []error
	for _, active := range sources {
		stored, err := c.collectSecurityEventFrom(ctx, active)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		// After the pass, and only after one that stored an event: the rules its events name are
		// resolved. Nothing that call meets fails the pass (resolveRuleInfo).
		if stored > 0 {
			c.resolveRuleInfo(ctx, active)
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("collect: the security-event pass was incomplete: %w",
			core.JoinErrors(failures))
	}
	return nil
}

// collectSecurityEventFrom runs one pass of one implementation.
//
// The ingestion watermark it reads is NOT keyed by provider, and that is the recorded
// provisional shape rather than an oversight: eve_ingest_cursor names a file format, and the
// schema says in as many words that it is rewritten when a second INGESTING provider is
// surveyed. Today exactly one implementation of this kind exists, so the watermarks belong to
// it; a second one that also resumed from a file offset would need the cursor keyed by provider
// first, and surveying its shape is what that cycle would begin with.
func (c *SecurityEventCoordinator) collectSecurityEventFrom(ctx context.Context, active core.ActiveSource) (int, error) {
	providerKey, providerID := active.ProviderKey, active.ProviderID
	source, registered := c.SourceSet.Lookup(Kind, providerKey)
	if !registered {
		return 0, fmt.Errorf("collect: the active security_event provider %q has no implementation",
			providerKey)
	}

	rotation, rotationResult, err := source.Implementation.Call(ctx, c, "rotation")
	if err != nil {
		return 0, err
	}
	known := provider.Flag(rotation, "known", false)
	if !known {
		// The rotation view is unavailable, so a lost file cannot be told from a present one.
		// The state is recorded and nothing is concluded.
		if writeErr := c.WriteAvailability(ctx, providerID, rotationResult.State,
			rotationResult.Outcome, rotationResult.Probe, rotationResult.Detail); writeErr != nil {
			return 0, writeErr
		}
	}

	now := c.Now()
	watermarks, err := c.Store.EveWatermarks(ctx)
	if err != nil {
		return 0, err
	}

	if known {
		if err := c.recordLostRotations(ctx, providerID, provider.Texts(rotation, "present"), watermarks, now); err != nil {
			return 0, err
		}
	}

	alerts, result, readErr := source.Implementation.Call(ctx, c, "alerts", c.PageSize(Kind))
	if writeErr := c.WriteAvailability(ctx, providerID, result.State,
		result.Outcome, result.Probe, result.Detail); writeErr != nil {
		return 0, writeErr
	}
	if readErr != nil {
		return 0, readErr
	}

	// No retention purge runs between this pass storing an event and the end of its
	// derivation (purge.go).
	defer c.Storing()()
	highestPerFile := make(map[string]int64)
	// stored are the events this pass wrote, and addresses both ends of each: the destination end
	// is placed exactly as the source end is (gap G7, closed in step 6).
	var stored, addresses []string
	for _, record := range provider.Records(alerts, "alerts") {
		fileID, offset := provider.Text(record, "file_id"), provider.Integer(record, "byte_offset")
		if watermark, seen := watermarks[fileID]; seen && offset <= watermark.ByteOffset {
			continue
		}
		if offset > highestPerFile[fileID] {
			highestPerFile[fileID] = offset
		}

		if err := c.Store.InsertSecurityEvent(ctx, providerID, buildSecurityEvent(record, now)); err != nil {
			// The events stored before this one are placed all the same.
			return len(stored), derive.On(c.Collector).DeriveAfter(ctx, derive.Derivation{}, err)
		}
		stored = append(stored, fileID)
		addresses = append(addresses, provider.Text(record, "src_address"), provider.Text(record, "dst_address"))
	}

	for fileID, offset := range highestPerFile {
		state := "rotated"
		var sequence *int64
		if parsed, err := strconv.ParseInt(fileID, 10, 64); err == nil {
			sequence = &parsed
			if highest := provider.IntegerPointer(rotation, "highest_sequence"); highest != nil && parsed == *highest {
				state = "current"
			}
		}
		if err := c.Store.UpsertEveCursor(ctx, vocab.EveCursor{
			FileID:        fileID,
			ByteOffset:    offset,
			FileSequence:  sequence,
			RotationState: state,
			ObservedAt:    now,
		}); err != nil {
			return len(stored), derive.On(c.Collector).DeriveAfter(ctx, derive.Derivation{}, err)
		}
	}
	if len(stored) == 0 {
		return 0, nil
	}
	// Both ends are placed after the pass, by the classification that places a flow's ends.
	return len(stored), derive.On(c.Collector).Derive(ctx, derive.Derivation{})
}

// The names of the kind's bounds on its rule-info call (kinds/security_event.json, "bounds").
const (
	boundRuleInfoPerPass = "rule_info_per_pass"
	boundRuleInfoRecheck = "rule_info_recheck_seconds"
)

// resolveRuleInfo asks one provider about the rules its events name that the cache holds no
// answer for, or an answer older than the kind's re-check age, at most the kind's per-pass bound
// of them, and writes its answers into the provider-neutral cache (provider_rule_info).
//
// A FAILED RULE-INFO CALL FAILS NOTHING ELSE. It never fails or undoes the alert pass that ran
// before it, and never writes the provider's availability, which is the alerts read's: the call
// is logged with the provider and what it answered, and the identities it did not answer stay
// unfetched -- no row -- and are asked again at the next pass that stores an event.
func (c *SecurityEventCoordinator) resolveRuleInfo(ctx context.Context, active core.ActiveSource) {
	providerKey, providerID := active.ProviderKey, active.ProviderID
	source, registered := c.SourceSet.Lookup(Kind, providerKey)
	contract, known := provider.ContractOf(Kind)
	if !registered || !known {
		return
	}
	perPass, hasBound := contract.BoundOf(boundRuleInfoPerPass)
	recheck, hasRecheck := contract.BoundOf(boundRuleInfoRecheck)
	if !hasBound || !hasRecheck {
		c.Note("collect: the security_event contract declares no rule-info bounds; %s was not asked", providerKey)
		return
	}
	now := c.Now()
	identities, err := c.Store.RuleIdentitiesToFetch(ctx, providerID, now-recheck.Default, perPass.Default)
	if err != nil {
		c.Note("collect: rule info of the security_event provider %s: reading what to ask: %v", providerKey, err)
		return
	}
	if len(identities) == 0 {
		return
	}
	asked := make(map[string]bool, len(identities))
	list := make([]any, 0, len(identities))
	for _, identity := range identities {
		asked[identity] = true
		list = append(list, identity)
	}
	answer, result, err := source.Implementation.Call(ctx, c, "rule_info", list)
	if err != nil {
		c.Note("collect: rule info of the security_event provider %s: outcome %s: %v; the %d rule identities "+
			"asked stay not fetched and are asked again", providerKey, result.Outcome, err, len(identities))
		return
	}
	if failure := provider.Text(answer, "error"); failure != "" {
		c.Note("collect: rule info of the security_event provider %s: outcome %s: %s (%s); the rule identities "+
			"it did not answer stay not fetched and are asked again", providerKey, result.Outcome, failure, result.Detail)
	}
	for _, entry := range provider.Records(answer, "rules") {
		identity := provider.Text(entry, "rule_identity")
		if !asked[identity] {
			// An answer to a question not asked names no event of this pass's question; it is not stored.
			continue
		}
		info := flows.RuleInfo{RuleIdentity: identity, LookupState: provider.Text(entry, "lookup_state"),
			FetchedAt: now}
		if info.LookupState == flows.RuleFound {
			info.NormalisedSeverity = provider.TextPointer(entry, "normalised_severity")
			info.ProviderSeverity = provider.TextPointer(entry, "provider_severity")
			info.Category = provider.TextPointer(entry, "category")
			info.RuleSource = provider.TextPointer(entry, "rule_source")
		}
		if err := c.Store.UpsertRuleInfo(ctx, providerID, info); err != nil {
			c.Note("collect: rule info of the security_event provider %s: %v", providerKey, err)
			return
		}
		delete(asked, identity)
	}
	if len(asked) > 0 && provider.Text(answer, "error") == "" {
		c.Note("collect: rule info of the security_event provider %s: outcome %s: %d of the %d rule identities "+
			"asked were not answered; they stay not fetched and are asked again", providerKey, result.Outcome,
			len(asked), len(identities))
	}
}

// recordLostRotations marks every watermarked file the feed no longer carries as lost, and
// writes one gap row for it.
//
// A file already marked lost is skipped: one loss is one row, not an unbounded stream of
// identical rows every minute.
func (c *SecurityEventCoordinator) recordLostRotations(ctx context.Context, providerID int64, present []string,
	watermarks map[string]flows.EveWatermark, now int64) error {
	carried := map[string]bool{}
	for _, fileID := range present {
		carried[fileID] = true
	}
	for fileID, watermark := range watermarks {
		if carried[fileID] {
			continue
		}
		if watermark.RotationState == "lost" {
			continue
		}
		if err := c.Store.MarkEveFileLost(ctx, fileID, now); err != nil {
			return err
		}
		detail := fmt.Sprintf(
			"rotation discarded the event file %s, which the reader had a watermark in at byte "+
				"%d; the records after that offset are gone and no endpoint can return them",
			fileID, watermark.ByteOffset)
		if err := c.Store.RecordCollectionGap(ctx, vocab.CollectionGap{
			ProviderID:      providerID,
			IntervalStartAt: watermark.ObservedAt,
			IntervalEndAt:   now,
			Reason:          vocab.GapEveRotationLost,
			Detail:          &detail,
			DetectedAt:      now,
		}); err != nil {
			return err
		}
	}
	return nil
}

// buildSecurityEvent turns one record into a row, composing the key the de-duplication rests
// on. The source machine is not resolved here: the event's source end is placed by the same
// classification as a flow's, after the pass, so an attacker's address outside never becomes a
// client row.
func buildSecurityEvent(record provider.Answer, now int64) vocab.SecurityEvent {
	return vocab.SecurityEvent{
		// The key is composed here, from the file and the offset inside it, because that is the
		// pair the provider guarantees stable. The ingestion coordinate itself is not a column
		// on this table: it lives in the cursor, which answers a different question.
		ProviderEventKey: provider.Text(record, "file_id") + ":" +
			strconv.FormatInt(provider.Integer(record, "byte_offset"), 10),
		OccurredAt:         provider.Integer(record, "occurred_at"),
		IngestedAt:         now,
		RuleIdentity:       provider.Text(record, "rule_identity"),
		Signature:          provider.Text(record, "signature"),
		EventAction:        provider.Text(record, "event_action"),
		NormalisedSeverity: provider.TextPointer(record, "normalised_severity"),
		SrcAddress:         provider.Text(record, "src_address"),
		SrcPort:            provider.IntegerPointer(record, "src_port"),
		DstAddress:         provider.Text(record, "dst_address"),
		DstPort:            provider.IntegerPointer(record, "dst_port"),
		Protocol:           provider.TextPointer(record, "protocol"),
		InInterfaceDevice:  provider.TextPointer(record, "in_interface_device"),
	}
}

// Kind is the kind this coordinator collects.
const Kind = "security_event"
