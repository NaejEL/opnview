package collection

// The catalogue keys this surface's code chooses. A handler chooses a key, never a string; a
// JSON answer carries its closed code (web.ConditionOf), which the front translates through the
// catalogue.

const (
	msgAggregateModeUnknown      messageKey = "error.aggregate_mode_unknown"
	msgAvailabilityHealthy       messageKey = "state.availability.healthy"
	msgAvailabilityNotCollecting messageKey = "state.availability.not_collecting"
	msgAvailabilityNotYetProbed  messageKey = "state.availability.not_yet_probed"
	msgAvailabilityUnavailable   messageKey = "state.availability.unavailable"
	msgAvailabilityUnknown       messageKey = "state.availability.unknown"
	msgCollectionKindUnknown     messageKey = "error.collection_kind_unknown"
	msgCollectionValueInvalid    messageKey = "error.collection_value_invalid"
	msgNoIngestedClock           messageKey = "state.sizing.no_ingested_clock"
	msgPageAboveCeiling          messageKey = "state.sizing.page_above_ceiling"
	msgPageWithinCeiling         messageKey = "state.sizing.page_within_ceiling"
	msgRateNotMeasured           messageKey = "state.sizing.rate_not_measured"
	// THE SELECTION IS NOT THE AVAILABILITY, and the two are reported as two members
	// because they are two facts: what the firewall says, and what the operator has
	// decided. internal/config says so where the selection is defined.
	msgSelectionAuto                messageKey = "state.sizing.selection_auto"
	msgSelectionOff                 messageKey = "state.sizing.selection_off"
	msgSelectionOn                  messageKey = "state.sizing.selection_on"
	msgSuggestionAvailable          messageKey = "state.sizing.suggestion_available"
	msgSuggestionNotCoverable       messageKey = "state.sizing.rate_not_coverable"
	msgSuggestionNotYetMeasured     messageKey = "state.sizing.not_yet_measured"
	msgSuggestionSourceNotCollected messageKey = "state.sizing.source_not_collected"
	msgValueNotNonNegative          messageKey = "error.value_not_non_negative"
	msgValueOutOfRange              messageKey = "error.value_out_of_range"
)

// keys is every key above, so a test can assert that each exists in the catalogue.
func keys() []messageKey {
	return []messageKey{
		msgAggregateModeUnknown,
		msgAvailabilityHealthy,
		msgAvailabilityNotCollecting,
		msgAvailabilityNotYetProbed,
		msgAvailabilityUnavailable,
		msgAvailabilityUnknown,
		msgCollectionKindUnknown,
		msgCollectionValueInvalid,
		msgNoIngestedClock,
		msgPageAboveCeiling,
		msgPageWithinCeiling,
		msgRateNotMeasured,
		msgSelectionAuto,
		msgSelectionOff,
		msgSelectionOn,
		msgSuggestionAvailable,
		msgSuggestionNotCoverable,
		msgSuggestionNotYetMeasured,
		msgSuggestionSourceNotCollected,
		msgValueNotNonNegative,
		msgValueOutOfRange,
	}
}
