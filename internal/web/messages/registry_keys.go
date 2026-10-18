package messages

import (
	"github.com/NaejEL/opnview/internal/store/reads"
	"github.com/NaejEL/opnview/internal/store/vocab"
)

// The availability of a source, in the values of the Source availability preset
// (docs/widget-catalogue.md, Health and honesty): the conditions are values, only reachable
// reads healthy, and a provider with no availability row reads "state unknown". The Sources
// surface and the collection cards both name a source with these, so a kind reads the same
// on both.
const (
	MsgAvailabilityHealthy       Key = "state.availability.healthy"
	MsgAvailabilityNotCollecting Key = "state.availability.not_collecting"
	MsgAvailabilityUnavailable   Key = "state.availability.unavailable"
	MsgAvailabilityNotYetProbed  Key = "state.availability.not_yet_probed"
	MsgAvailabilityUnknown       Key = "state.availability.unknown"
)

// AvailabilityOf names one provider's condition as the preset does, with the class of its
// mark. Only reachable is healthy; a missing row is "state unknown", and a state the schema
// does not allow reads the same rather than as anything better.
func AvailabilityOf(row reads.SourceRow) (Key, string) {
	switch {
	case !row.HasAvailability:
		return MsgAvailabilityUnknown, "mark-none"
	case row.NotYetProbed():
		return MsgAvailabilityNotYetProbed, "mark-none"
	case row.State == vocab.StateReachable:
		return MsgAvailabilityHealthy, "mark-ok"
	case row.State == vocab.StatePresentButDisabled:
		return MsgAvailabilityNotCollecting, "mark-off"
	case row.State == vocab.StateUnavailable:
		return MsgAvailabilityUnavailable, "mark-warn"
	default:
		return MsgAvailabilityUnknown, "mark-none"
	}
}
