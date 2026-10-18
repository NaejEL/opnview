package collect_test

// The netflow plugin's words, as its script and manifest name them (plugins/netflow): what the collector's tests
// of the join read in the plugin's tables. They are the plugin's vocabulary, restated here for the tests alone.
const (
	roleInterface            = "interface"
	roleNoReceivingInterface = "no_receiving_interface"
	roleLocalDelivery        = "local_delivery"
	roleUnmapped             = "unmapped"

	joinPending   = "pending"
	joinJoined    = "joined"
	joinCopy      = "copy"
	joinUnmatched = "unmatched"

	copyTranslated = "translated_copy"
	copyParent     = "parent_copy"

	unmatchedNoLoggedLeg        = "no_logged_leg"
	unmatchedAmbiguous          = "ambiguous"
	unmatchedBeyondFlowHorizon  = "beyond_flow_horizon"
	unmatchedIndexUnmapped      = "interface_index_unmapped"
	unmatchedReverseUnconfirmed = "reverse_unconfirmed"
	unmatchedFilterLogGap       = "filter_log_gap"

	orientationForward = "forward"
	orientationReverse = "reverse"

	volumeMeasured               = "measured"
	volumeInProgress             = "in_progress"
	volumeAwaitingExport         = "awaiting_export"
	volumeMissing                = "missing"
	volumeExportGap              = "export_gap"
	volumeNotExportedWhenRefused = "not_exported_when_refused"
	volumeForwardingUnsettled    = "forwarding_export_unsettled"
	volumeIngressNotCaptured     = "ingress_not_captured"
	volumeParentCopiesOnly       = "parent_copies_only"
	volumeInterfaceNotExported   = "interface_not_exported"
	volumeInterfaceNotCaptured   = "interface_not_captured"
	volumeCaptureUnknown         = "capture_configuration_unknown"
	volumeAmbiguousRecords       = "ambiguous_records"
	volumeNotExpectedWhenRefused = "not_expected_when_refused"

	exportedYes     = "yes"
	exportedUnknown = "unknown"
)

// coveredState reports whether a direction in a state is covered, as the netflow measure declares it.
func coveredState(state string) bool {
	switch state {
	case volumeMeasured, volumeInProgress, volumeAwaitingExport, volumeMissing, volumeNotExpectedWhenRefused:
		return true
	}
	return false
}
