// Package securityevent is the security_event kind: a source of intrusion-detection events.
//
// WHAT THE KIND PRODUCES. Alerts, newest first, each decoded and normalised by its
// implementation, plus a view of the rotation of the files the feed is written to. The
// ingestion coordinate (FileID, ByteOffset) is what this kind's cursor is keyed on; it is not a
// column of the event. The kind admits several concurrently active providers, because people run
// Suricata, CrowdSec and Zenarmor together: every event row says which provider reported it.
//
// WHAT AN IMPLEMENTATION IS. A directory internal/collect/securityevent/<product>/ holding a
// provider.json whose kind is "security_event", a Go package whose init() calls Register with the
// embedded manifest and a Source, its fixtures and its tests. See docs/extending.md. Keeping the
// watermark, detecting a lost rotation and resolving a client are the kind's business, written
// once in internal/collect (securityevent.go).
package securityevent

import (
	"context"

	"github.com/NaejEL/opnview/internal/collect/provider"
)

// Kind is the registry kind this package answers for.
const Kind = "security_event"

// Alert is one security event, decoded and normalised by its implementation.
type Alert struct {
	// FileID and ByteOffset are the ingestion coordinate this kind's cursor is keyed on.
	// They are not columns on the event: they answer where the reader stopped, which is a
	// different question from whether an event is already stored.
	FileID             string
	ByteOffset         int64
	OccurredAt         int64
	RuleIdentity       string
	Signature          string
	EventAction        string
	NormalisedSeverity *string
	SrcAddress         string
	DstAddress         string
	SrcPort            *int64
	DstPort            *int64
	Protocol           *string
	InInterfaceDevice  *string
}

// Rotation is what an implementation knows about the files its feed rotates through.
type Rotation struct {
	// Known is false when the implementation could not ask. A watermarked file that is
	// merely unlisted because the call failed must not be declared lost.
	Known bool
	// Present holds the file ids the feed still carries.
	Present map[string]struct{}
	// HighestSequence names the current file, or is nil when the implementation does not
	// report a sequence.
	HighestSequence *int64
}

// Source is one implementation of the security_event kind.
type Source interface {
	// Probe reports what the firewall says about this implementation.
	Probe(ctx context.Context, host provider.Session) (provider.Result, error)
	// Rotation reports which of the feed's files still exist, which is how a permanent
	// loss is detected.
	Rotation(ctx context.Context, host provider.Session) (Rotation, provider.Result, error)
	// Alerts returns the most recent events, newest first.
	Alerts(ctx context.Context, host provider.Session, pageSize int) ([]Alert, provider.Result, error)
}
