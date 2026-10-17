package collection_test

import "github.com/NaejEL/opnview/internal/web"

// The catalogue keys these tests name, written out here and not imported from the code under
// test: a test that read its keys from the code could not notice the code changing one. A JSON
// answer carries each as its closed code (web.ConditionOf).
type messageKey = web.MessageKey

const (
	msgAvailabilityHealthy          messageKey = "state.availability.healthy"
	msgAvailabilityNotYetProbed     messageKey = "state.availability.not_yet_probed"
	msgAvailabilityUnavailable      messageKey = "state.availability.unavailable"
	msgNoIngestedClock              messageKey = "state.sizing.no_ingested_clock"
	msgPageAboveCeiling             messageKey = "state.sizing.page_above_ceiling"
	msgPageWithinCeiling            messageKey = "state.sizing.page_within_ceiling"
	msgRateNotMeasured              messageKey = "state.sizing.rate_not_measured"
	msgSuggestionAvailable          messageKey = "state.sizing.suggestion_available"
	msgSuggestionSourceNotCollected messageKey = "state.sizing.source_not_collected"
)
