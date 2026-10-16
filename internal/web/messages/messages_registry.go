package web

// The registry's vocabulary: the kinds and providers the provider table registers, and the
// reasons a collection gap records. A key is composed from a registry row (KindLabelKey,
// ProviderLabelKey, GapReasonKey) by the surfaces that name a source, so these constants are
// the catalogue's bookkeeping: an entry nobody reaches and a key nobody wrote are both failures.

const (
	msgLabelGapReason          messageKey = "collection.label.gap_reason"
	msgKindFirewallLog         messageKey = "collection.kind.firewall_log"
	msgKindSecurityEvent       messageKey = "collection.kind.security_event"
	msgKindDHCPLease           messageKey = "collection.kind.dhcp_lease"
	msgKindDNSLookup           messageKey = "collection.kind.dns_lookup"
	msgGapDigestOutsideWindow  messageKey = "gap_reason.digest_outside_returned_window"
	msgGapEveRotationLost      messageKey = "gap_reason.eve_rotation_lost"
	msgGapResolverWindow       messageKey = "gap_reason.resolver_window_not_honoured"
	msgGroupImplementations    messageKey = "collection.group.implementations"
	msgPageNotAdjustable       messageKey = "state.sizing.page_not_adjustable"
	msgLabelGeoIPState         messageKey = "settings.label.geoip_state"
	msgLabelGeoIPBuild         messageKey = "settings.label.geoip_build"
	msgKindFlowVolume          messageKey = "collection.kind.flow_volume"
	msgKindIPGeolocation       messageKey = "collection.kind.ip_geolocation"
	msgKindMeasurementSample   messageKey = "collection.kind.measurement_sample"
	msgKindRegistrableDomain   messageKey = "collection.kind.registrable_domain"
	msgKindResolverCache       messageKey = "collection.kind.resolver_cache"
	msgKindResolverLocalData   messageKey = "collection.kind.resolver_local_data"
	msgProviderInsightFlow     messageKey = "provider.flow_volume.insight"
	msgGapDownloadFailed       messageKey = "gap_reason.download_failed"
	msgLabelRetentionNow       messageKey = "collection.label.retention_now"
	msgLabelRetentionRequested messageKey = "collection.label.retention_requested"
	msgEditedNotSaved          messageKey = "notice.edited_not_saved"
	msgSecretNotKept           messageKey = "state.secret_not_kept"
	msgOpenInOPNsense          messageKey = "sources.label.opnsense_page"
	// The push sources' gap reasons (docs/plugins.md, "Push sources").
	msgGapPushBufferOverflow     messageKey = "gap_reason.push_buffer_overflow"
	msgGapPushStoreFailed        messageKey = "gap_reason.push_store_failed"
	msgGapPushListenerDown       messageKey = "gap_reason.push_listener_down"
	msgGapPushScriptFailed       messageKey = "gap_reason.push_script_failed"
	msgGapPushNotAlive           messageKey = "gap_reason.push_not_alive"
	msgGapNetflowSequenceGap     messageKey = "gap_reason.netflow_sequence_gap"
	msgGapNetflowTemplateMissing messageKey = "gap_reason.netflow_template_missing"
	// The flow_record kind (PS17 of the NetFlow join amendment).
	msgKindFlowRecord messageKey = "collection.kind.flow_record"
)

// vocabularyKeys is every key above.
func vocabularyKeys() []MessageKey {
	return []MessageKey{
		msgLabelGapReason,
		msgKindFirewallLog,
		msgKindSecurityEvent,
		msgKindDHCPLease,
		msgKindDNSLookup,
		msgGapDigestOutsideWindow,
		msgGapEveRotationLost,
		msgGapResolverWindow,
		msgGroupImplementations,
		msgPageNotAdjustable,
		msgLabelGeoIPState,
		msgLabelGeoIPBuild,
		msgKindFlowVolume,
		msgKindIPGeolocation,
		msgKindMeasurementSample,
		msgKindRegistrableDomain,
		msgKindResolverCache,
		msgKindResolverLocalData,
		msgProviderInsightFlow,
		msgGapDownloadFailed,
		msgLabelRetentionNow,
		msgLabelRetentionRequested,
		msgEditedNotSaved,
		msgSecretNotKept,
		msgOpenInOPNsense,
		msgGapPushBufferOverflow,
		msgGapPushStoreFailed,
		msgGapPushListenerDown,
		msgGapPushScriptFailed,
		msgGapPushNotAlive,
		msgGapNetflowSequenceGap,
		msgGapNetflowTemplateMissing,
		msgKindFlowRecord,
	}
}
