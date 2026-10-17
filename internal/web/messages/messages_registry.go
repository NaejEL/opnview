package messages

// The registry's vocabulary: the kinds and providers the provider table registers, and the
// reasons a collection gap records. A key is composed from a registry row (KindLabelKey,
// ProviderLabelKey, GapReasonKey) by the surfaces that name a source, so these constants are
// the catalogue's bookkeeping: an entry nobody reaches and a key nobody wrote are both failures.

const (
	msgLabelGapReason          Key = "collection.label.gap_reason"
	msgKindFirewallLog         Key = "collection.kind.firewall_log"
	msgKindSecurityEvent       Key = "collection.kind.security_event"
	msgKindDHCPLease           Key = "collection.kind.dhcp_lease"
	msgKindDNSLookup           Key = "collection.kind.dns_lookup"
	msgGapDigestOutsideWindow  Key = "gap_reason.digest_outside_returned_window"
	msgGapEveRotationLost      Key = "gap_reason.eve_rotation_lost"
	msgGapResolverWindow       Key = "gap_reason.resolver_window_not_honoured"
	msgGroupImplementations    Key = "collection.group.implementations"
	msgPageNotAdjustable       Key = "state.sizing.page_not_adjustable"
	msgKindFlowVolume          Key = "collection.kind.flow_volume"
	msgKindIPGeolocation       Key = "collection.kind.ip_geolocation"
	msgKindMeasurementSample   Key = "collection.kind.measurement_sample"
	msgKindRegistrableDomain   Key = "collection.kind.registrable_domain"
	msgKindResolverCache       Key = "collection.kind.resolver_cache"
	msgKindResolverLocalData   Key = "collection.kind.resolver_local_data"
	msgProviderInsightFlow     Key = "provider.flow_volume.insight"
	msgGapDownloadFailed       Key = "gap_reason.download_failed"
	msgLabelRetentionNow       Key = "collection.label.retention_now"
	msgLabelRetentionRequested Key = "collection.label.retention_requested"
	msgEditedNotSaved          Key = "notice.edited_not_saved"
	msgSecretNotKept           Key = "state.secret_not_kept"
	msgOpenInOPNsense          Key = "sources.label.opnsense_page"
	// The push sources' gap reasons (docs/plugins.md, "Push sources").
	msgGapPushBufferOverflow     Key = "gap_reason.push_buffer_overflow"
	msgGapPushStoreFailed        Key = "gap_reason.push_store_failed"
	msgGapPushListenerDown       Key = "gap_reason.push_listener_down"
	msgGapPushScriptFailed       Key = "gap_reason.push_script_failed"
	msgGapPushNotAlive           Key = "gap_reason.push_not_alive"
	msgGapNetflowSequenceGap     Key = "gap_reason.netflow_sequence_gap"
	msgGapNetflowTemplateMissing Key = "gap_reason.netflow_template_missing"
	// The flow_record kind (PS17 of the NetFlow join amendment).
	msgKindFlowRecord Key = "collection.kind.flow_record"
)

// vocabularyKeys is every key above.
func vocabularyKeys() []Key {
	return []Key{
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
