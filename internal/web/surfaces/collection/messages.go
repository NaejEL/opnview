package collection

// The catalogue keys this surface's code chooses. A handler chooses a key, never a string;
// the words are in locale/en.json beside this file, or in the core's locale when another
// surface uses the key too.

const (
	msgAggregateModeUnknown      messageKey = "error.aggregate_mode_unknown"
	msgAvailabilityHealthy       messageKey = "state.availability.healthy"
	msgAvailabilityNotCollecting messageKey = "state.availability.not_collecting"
	msgAvailabilityNotYetProbed  messageKey = "state.availability.not_yet_probed"
	msgAvailabilityUnavailable   messageKey = "state.availability.unavailable"
	msgAvailabilityUnknown       messageKey = "state.availability.unknown"
	msgCardAttribution           messageKey = "collection.card.attribution"
	msgCardLeaseBackends         messageKey = "collection.card.lease_backends"
	// msgCardNotNamed refuses a submission that named no card -- the Enter key's --
	// and stores nothing.
	msgCardNotNamed messageKey = "error.card_not_named"
	msgCardPairing  messageKey = "collection.card.pairing"
	msgCardStorage  messageKey = "collection.card.storage"
	// msgCollectionIntervalTooLarge refuses an interval no duration can hold. It is a
	// refusal of its own rather than "not a number", because the figure IS a number
	// and telling the operator otherwise would send them looking for a typo.
	msgCollectionIntervalTooLarge messageKey = "error.collection_interval_too_large"
	msgCollectionKindUnknown      messageKey = "error.collection_kind_unknown"
	msgCollectionSaved            messageKey = "notice.collection_saved"
	msgCollectionValueInvalid     messageKey = "error.collection_value_invalid"
	msgFieldPageSize              messageKey = "field.page_size"
	// msgFieldPollInterval names a polled kind's interval field, which takes a unit.
	msgFieldPollInterval      messageKey = "field.poll_interval"
	msgLabelAttributionDelay  messageKey = "collection.label.attribution_delay"
	msgLabelCacheAnswerCap    messageKey = "collection.label.cache_answer_cap"
	msgLabelCoveredFrom       messageKey = "collection.label.covered_from"
	msgLabelCoveredTo         messageKey = "collection.label.covered_to"
	msgLabelGapCount          messageKey = "collection.label.gap_count"
	msgLabelIntervalInForce   messageKey = "collection.label.interval_in_force"
	msgLabelLeaseFailureLimit messageKey = "collection.label.lease_failure_limit"
	msgLabelMeanRateIngested  messageKey = "collection.label.mean_rate_ingested"
	msgLabelMeanRateSource    messageKey = "collection.label.mean_rate_source"
	msgLabelMissedSeconds     messageKey = "collection.label.missed_seconds"
	msgLabelPageCeiling       messageKey = "collection.label.page_ceiling"
	msgLabelPageSizeInForce   messageKey = "collection.label.page_size_in_force"
	msgLabelPageState         messageKey = "collection.label.page_state"
	msgLabelPairingWindow     messageKey = "collection.label.pairing_window"
	msgLabelPeakRate          messageKey = "collection.label.peak_rate"
	msgLabelRecordCount       messageKey = "collection.label.record_count"
	msgLabelRetention         messageKey = "collection.label.retention"
	msgLabelRetentionHorizon  messageKey = "collection.label.retention_horizon"
	msgLabelSelection         messageKey = "collection.label.selection"
	msgLabelSiteNames         messageKey = "collection.label.site_names"
	msgLabelSourceState       messageKey = "collection.label.source_state"
	msgLabelSuggestedInterval messageKey = "collection.label.suggested_interval"
	msgLabelSuggestedPageSize messageKey = "collection.label.suggested_page_size"
	msgLabelSuggestionState   messageKey = "collection.label.suggestion_state"
	msgLabelWindowEnd         messageKey = "collection.label.window_end"
	msgLabelWindowStart       messageKey = "collection.label.window_start"
	msgNoIngestedClock        messageKey = "state.sizing.no_ingested_clock"
	msgNoPageCeiling          messageKey = "state.sizing.no_page_ceiling"
	msgNoRecordsInWindow      messageKey = "state.sizing.no_records_in_window"
	msgPageAboveCeiling       messageKey = "state.sizing.page_above_ceiling"
	msgPageWithinCeiling      messageKey = "state.sizing.page_within_ceiling"
	msgRateNotMeasured        messageKey = "state.sizing.rate_not_measured"
	// msgRetentionConfirm is a shorter retention horizon waiting for its
	// confirmation: the next purge deletes what is older, and that cannot be undone.
	msgRetentionConfirm   messageKey = "notice.retention_confirm"
	msgRetentionUnlimited messageKey = "state.sizing.retention_unlimited"
	// THE SELECTION IS NOT THE AVAILABILITY, and the two are reported as two rows
	// because they are two facts: what the firewall says, and what the operator has
	// decided. internal/config says so where the selection is defined, and a surface
	// collapsing them would hide an operator's own decision behind a probe result.
	msgSelectionAuto messageKey = "state.sizing.selection_auto"
	msgSelectionOff  messageKey = "state.sizing.selection_off"
	msgSelectionOn   messageKey = "state.sizing.selection_on"
	msgSettingsSaved messageKey = "notice.settings_saved"
	// The site-name mode, named by what it does: shown or withheld. Neither rewrites
	// anything stored.
	msgSiteNamesShown               messageKey = "state.site_names.shown"
	msgSiteNamesWithheld            messageKey = "state.site_names.withheld"
	msgSuggestionAvailable          messageKey = "state.sizing.suggestion_available"
	msgSuggestionFilled             messageKey = "notice.suggestion_filled"
	msgSuggestionNotCoverable       messageKey = "state.sizing.rate_not_coverable"
	msgSuggestionNotFilled          messageKey = "notice.suggestion_not_filled"
	msgSuggestionNotYetMeasured     messageKey = "state.sizing.not_yet_measured"
	msgSuggestionSourceNotCollected messageKey = "state.sizing.source_not_collected"
	// msgTitleCollection names the collection surface, which is a route of its own
	// rather than a fourth card on the settings page.
	msgTitleCollection     messageKey = "collection.title"
	msgValueNotNonNegative messageKey = "error.value_not_non_negative"
	// msgValueOutOfRange refuses a value outside the range config.Load accepts, which
	// is stated beside it.
	msgValueOutOfRange messageKey = "error.value_out_of_range"
)

// keys is every key above, so a test can assert that each exists in the catalogue and that
// no catalogue entry is unreachable.
func keys() []messageKey {
	return []messageKey{
		msgAggregateModeUnknown,
		msgAvailabilityHealthy,
		msgAvailabilityNotCollecting,
		msgAvailabilityNotYetProbed,
		msgAvailabilityUnavailable,
		msgAvailabilityUnknown,
		msgCardAttribution,
		msgCardLeaseBackends,
		msgCardNotNamed,
		msgCardPairing,
		msgCardStorage,
		msgCollectionIntervalTooLarge,
		msgCollectionKindUnknown,
		msgCollectionSaved,
		msgCollectionValueInvalid,
		msgFieldPageSize,
		msgFieldPollInterval,
		msgLabelAttributionDelay,
		msgLabelCacheAnswerCap,
		msgLabelCoveredFrom,
		msgLabelCoveredTo,
		msgLabelGapCount,
		msgLabelIntervalInForce,
		msgLabelLeaseFailureLimit,
		msgLabelMeanRateIngested,
		msgLabelMeanRateSource,
		msgLabelMissedSeconds,
		msgLabelPageCeiling,
		msgLabelPageSizeInForce,
		msgLabelPageState,
		msgLabelPairingWindow,
		msgLabelPeakRate,
		msgLabelRecordCount,
		msgLabelRetention,
		msgLabelRetentionHorizon,
		msgLabelSelection,
		msgLabelSiteNames,
		msgLabelSourceState,
		msgLabelSuggestedInterval,
		msgLabelSuggestedPageSize,
		msgLabelSuggestionState,
		msgLabelWindowEnd,
		msgLabelWindowStart,
		msgNoIngestedClock,
		msgNoPageCeiling,
		msgNoRecordsInWindow,
		msgPageAboveCeiling,
		msgPageWithinCeiling,
		msgRateNotMeasured,
		msgRetentionConfirm,
		msgRetentionUnlimited,
		msgSelectionAuto,
		msgSelectionOff,
		msgSelectionOn,
		msgSettingsSaved,
		msgSiteNamesShown,
		msgSiteNamesWithheld,
		msgSuggestionAvailable,
		msgSuggestionFilled,
		msgSuggestionNotCoverable,
		msgSuggestionNotFilled,
		msgSuggestionNotYetMeasured,
		msgSuggestionSourceNotCollected,
		msgTitleCollection,
		msgValueNotNonNegative,
		msgValueOutOfRange,
	}
}
