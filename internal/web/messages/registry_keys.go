package messages

import (
	"github.com/NaejEL/opnview/internal/store/reads"
	"github.com/NaejEL/opnview/internal/store/vocab"
)

// The keys composed from a registry row.
//
// A surface that names a source -- the Sources surface, the collection cards -- never spells
// a kind or a provider: the key is composed from the registry row, so no kind is enumerated
// here, a kind or provider with no catalogue entry fails the render rather than showing its
// identifier, and a test holds every kind and provider of a fresh schema to an entry.

// KindLabelKey names a provider kind.
func KindLabelKey(kind string) Key { return Key("collection.kind." + kind) }

// ProviderLabelKey names one registry row: the same product under two kinds is two entries.
func ProviderLabelKey(kind, providerKey string) Key {
	return Key("provider." + kind + "." + providerKey)
}

// GapReasonKey names one value of collection_gap.reason.
func GapReasonKey(reason string) Key { return Key("gap_reason." + reason) }

// The availability of a source, in the values of the Source availability preset
// (docs/widget-catalogue.md, Health and honesty): the conditions are values, only reachable
// reads healthy, and a provider with no availability row reads "state unknown". The Sources
// surface and the collection cards both name a source with these, so a kind reads the same
// on both.
const (
	msgAvailabilityHealthy       Key = "state.availability.healthy"
	msgAvailabilityNotCollecting Key = "state.availability.not_collecting"
	msgAvailabilityUnavailable   Key = "state.availability.unavailable"
	msgAvailabilityNotYetProbed  Key = "state.availability.not_yet_probed"
	msgAvailabilityUnknown       Key = "state.availability.unknown"
)

// availabilityKeys is every key above, for CoreKeys.
func availabilityKeys() []Key {
	return []Key{msgAvailabilityHealthy, msgAvailabilityNotCollecting,
		msgAvailabilityUnavailable, msgAvailabilityNotYetProbed, msgAvailabilityUnknown}
}

// AvailabilityOf names one provider's condition as the preset does, with the class of its
// mark. Only reachable is healthy; a missing row is "state unknown", and a state the schema
// does not allow reads the same rather than as anything better.
func AvailabilityOf(row reads.SourceRow) (Key, string) {
	switch {
	case !row.HasAvailability:
		return msgAvailabilityUnknown, "mark-none"
	case row.NotYetProbed():
		return msgAvailabilityNotYetProbed, "mark-none"
	case row.State == vocab.StateReachable:
		return msgAvailabilityHealthy, "mark-ok"
	case row.State == vocab.StatePresentButDisabled:
		return msgAvailabilityNotCollecting, "mark-off"
	case row.State == vocab.StateUnavailable:
		return msgAvailabilityUnavailable, "mark-warn"
	default:
		return msgAvailabilityUnknown, "mark-none"
	}
}
