package collect

import (
	"context"
	"fmt"
	"strconv"

	"github.com/NaejEL/opnview/internal/store"
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

// CollectSecurityEvent runs one pass of the active implementation of the security_event kind.
func (c *Collector) CollectSecurityEvent(ctx context.Context) error {
	providerKey, providerID, active, err := c.activeSourceKey(ctx, KindSecurityEvent)
	if err != nil {
		return err
	}
	if !active {
		return nil
	}
	source, registered := securityEventSources[providerKey]
	if !registered {
		return fmt.Errorf("collect: the active security_event provider %q has no implementation",
			providerKey)
	}

	view, rotationResult, err := source.rotation(ctx, c)
	if err != nil {
		return err
	}
	if !view.known {
		// The rotation view is unavailable, so a lost file cannot be told from a present one.
		// The state is recorded and nothing is concluded.
		if writeErr := c.writeAvailability(ctx, providerID, rotationResult.state,
			rotationResult.probe, rotationResult.detail); writeErr != nil {
			return writeErr
		}
	}

	now := c.now()
	watermarks, err := c.store.EveWatermarks(ctx)
	if err != nil {
		return err
	}

	if view.known {
		if err := c.recordLostRotations(ctx, providerID, view, watermarks, now); err != nil {
			return err
		}
	}

	records, result, readErr := source.alerts(ctx, c)
	if writeErr := c.writeAvailability(ctx, providerID, result.state,
		result.probe, result.detail); writeErr != nil {
		return writeErr
	}
	if readErr != nil {
		return readErr
	}

	highestPerFile := make(map[string]int64)
	for _, record := range records {
		if watermark, seen := watermarks[record.FileID]; seen &&
			record.ByteOffset <= watermark.ByteOffset {
			continue
		}
		if record.ByteOffset > highestPerFile[record.FileID] {
			highestPerFile[record.FileID] = record.ByteOffset
		}

		event, err := c.buildSecurityEvent(ctx, record, now)
		if err != nil {
			return err
		}
		if err := c.store.InsertSecurityEvent(ctx, providerID, event); err != nil {
			return err
		}
	}

	for fileID, offset := range highestPerFile {
		state := "rotated"
		var sequence *int64
		if parsed, err := strconv.ParseInt(fileID, 10, 64); err == nil {
			sequence = &parsed
			if view.highestSequence != nil && parsed == *view.highestSequence {
				state = "current"
			}
		}
		if err := c.store.UpsertEveCursor(ctx, store.EveCursor{
			FileID:        fileID,
			ByteOffset:    offset,
			FileSequence:  sequence,
			RotationState: state,
			ObservedAt:    now,
		}); err != nil {
			return err
		}
	}
	return nil
}

// recordLostRotations marks every watermarked file the feed no longer carries as lost, and
// writes one gap row for it.
//
// A file already marked lost is skipped: one loss is one row, not an unbounded stream of
// identical rows every minute.
func (c *Collector) recordLostRotations(ctx context.Context, providerID int64, view rotationView,
	watermarks map[string]store.EveWatermark, now int64) error {
	for fileID, watermark := range watermarks {
		if _, still := view.present[fileID]; still {
			continue
		}
		if watermark.RotationState == "lost" {
			continue
		}
		if err := c.store.MarkEveFileLost(ctx, fileID, now); err != nil {
			return err
		}
		detail := fmt.Sprintf(
			"rotation discarded the event file %s, which the reader had a watermark in at byte "+
				"%d; the records after that offset are gone and no endpoint can return them",
			fileID, watermark.ByteOffset)
		if err := c.store.RecordCollectionGap(ctx, store.CollectionGap{
			ProviderID:      providerID,
			IntervalStartAt: watermark.ObservedAt,
			IntervalEndAt:   now,
			Reason:          store.GapEveRotationLost,
			Detail:          &detail,
			DetectedAt:      now,
		}); err != nil {
			return err
		}
	}
	return nil
}

// buildSecurityEvent turns one record into a row, composing the key the de-duplication rests
// on and resolving the source machine.
func (c *Collector) buildSecurityEvent(ctx context.Context, record alertRecord, now int64) (
	store.SecurityEvent, error) {
	srcClientID, srcInterfaceID, err := c.clientForAddress(ctx, nil, record.SrcAddress, now)
	if err != nil {
		return store.SecurityEvent{}, err
	}

	return store.SecurityEvent{
		// The key is composed here, from the file and the offset inside it, because that is the
		// pair the provider guarantees stable. The ingestion coordinate itself is not a column
		// on this table: it lives in the cursor, which answers a different question.
		ProviderEventKey:   record.FileID + ":" + strconv.FormatInt(record.ByteOffset, 10),
		OccurredAt:         record.OccurredAt,
		IngestedAt:         now,
		RuleIdentity:       record.RuleIdentity,
		Signature:          record.Signature,
		EventAction:        record.EventAction,
		NormalisedSeverity: record.NormalisedSeverity,
		SrcAddress:         record.SrcAddress,
		SrcPort:            record.SrcPort,
		DstAddress:         record.DstAddress,
		DstPort:            record.DstPort,
		Protocol:           record.Protocol,
		InInterfaceDevice:  record.InInterfaceDevice,
		SrcClientID:        srcClientID,
		SrcInterfaceID:     srcInterfaceID,
	}, nil
}
