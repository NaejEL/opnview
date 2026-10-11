package web

// The catalogue keys the Interfaces and Sources surfaces, the collection
// surface's settings cards and the Public Suffix List card choose.
//
// They are in a file of their own so this cycle's keys are one list, and
// messages.go's bookkeeping takes them in through surfaceMessageKeys. The rule is
// messages.go's: a handler chooses a key, never a string.

// The page titles, which are also the top bar's links.
const (
	msgTitleInterfaces messageKey = "interfaces.title"
	msgTitleSources    messageKey = "sources.title"
)

// THE AVAILABILITY OF A SOURCE, in the values of the Source availability preset
// (docs/widget-catalogue.md, Health and honesty): the conditions are values, only
// reachable reads healthy, and a provider with no availability row reads "state
// unknown". The Sources surface and the collection cards both name a source with
// these, so a kind reads the same on both.
const (
	msgAvailabilityHealthy       messageKey = "state.availability.healthy"
	msgAvailabilityNotCollecting messageKey = "state.availability.not_collecting"
	msgAvailabilityUnavailable   messageKey = "state.availability.unavailable"
	msgAvailabilityNotYetProbed  messageKey = "state.availability.not_yet_probed"
	msgAvailabilityUnknown       messageKey = "state.availability.unknown"
)

// The answers to a yes-or-no fact: whether a provider is read, whether an interface
// is upstream, whether a network is removed or counts, and the link-local rule.
const (
	msgYes messageKey = "state.yes"
	msgNo  messageKey = "state.no"
)

// The Sources surface's states, each in place of a figure or a datum that is not
// there.
const (
	msgNeverChecked messageKey = "state.source.never_checked"
	msgNoDetail     messageKey = "state.source.no_detail"
)

// The kinds the provider table registers beyond the four the collection surface
// sizes, and the implementations of them. The key is composed from the registry row
// (kindLabelKey, providerLabelKey); these constants are the catalogue's bookkeeping,
// so an entry nobody reaches and a key nobody wrote are both failures. A kind or a
// provider added to the registry adds its entry here and to each catalogue, and a
// test fails until it does.
const (
	msgKindFlowVolume        messageKey = "collection.kind.flow_volume"
	msgKindGeoASN            messageKey = "collection.kind.geo_asn"
	msgKindMeasurementSample messageKey = "collection.kind.measurement_sample"
	msgKindPublicSuffix      messageKey = "collection.kind.public_suffix"
	msgKindResolverCache     messageKey = "collection.kind.resolver_cache"
	msgKindResolverLocalData messageKey = "collection.kind.resolver_local_data"

	msgProviderInsightFlow         messageKey = "provider.flow_volume.insight"
	msgProviderMaxMind             messageKey = "provider.geo_asn.maxmind_geolite2"
	msgProviderInsightSampling     messageKey = "provider.measurement_sample.insight"
	msgProviderPublicSuffixList    messageKey = "provider.public_suffix.public_suffix_list"
	msgProviderUnboundCache        messageKey = "provider.resolver_cache.unbound"
	msgProviderDnsmasqCache        messageKey = "provider.resolver_cache.dnsmasq"
	msgProviderUnboundLocalData    messageKey = "provider.resolver_local_data.unbound"
	msgProviderDnsmasqLocalData    messageKey = "provider.resolver_local_data.dnsmasq"
	msgGapDownloadFailed           messageKey = "gap_reason.download_failed"
	msgFieldPageSize               messageKey = "field.page_size"
	msgInterfaceActionUnknown      messageKey = "error.interface_action_unknown"
	msgLinkLocalInvalid            messageKey = "error.link_local_invalid"
	msgPublicSuffixNotYetRefreshed messageKey = "state.public_suffix.not_yet_refreshed"
)

// The Interfaces surface.
const (
	msgOriginDetected messageKey = "state.origin.detected"
	msgOriginOperator messageKey = "state.origin.operator"
	// msgInterfaceSaved confirms an edit, and says when it takes effect: at the
	// collector's next derivation, never "reclassified".
	msgInterfaceSaved messageKey = "notice.interface_saved"

	msgInterfaceUnknown  messageKey = "error.interface_unknown"
	msgInterfaceUpstream messageKey = "error.interface_upstream"
	msgNetworkUnknown    messageKey = "error.network_unknown"
	// The refusals of a typed network, each its own sentence.
	msgNetworkMalformed  messageKey = "error.network_malformed"
	msgNetworkHostBits   messageKey = "error.network_host_bits"
	msgNetworkIPv4Mapped messageKey = "error.network_ipv4_mapped"
	msgNetworkLinkLocal  messageKey = "error.network_link_local"
)

// The collection surface's settings cards.
const (
	msgCardPairing       messageKey = "collection.card.pairing"
	msgCardLeaseBackends messageKey = "collection.card.lease_backends"
	msgCardAttribution   messageKey = "collection.card.attribution"
	msgCardStorage       messageKey = "collection.card.storage"

	msgLabelPairingWindow     messageKey = "collection.label.pairing_window"
	msgLabelLeaseFailureLimit messageKey = "collection.label.lease_failure_limit"
	msgLabelAttributionDelay  messageKey = "collection.label.attribution_delay"
	msgLabelCacheAnswerCap    messageKey = "collection.label.cache_answer_cap"
	msgLabelRetention         messageKey = "collection.label.retention"
	msgLabelSiteNames         messageKey = "collection.label.site_names"

	// The site-name mode, named by what it does: shown or withheld. Neither rewrites
	// anything stored.
	msgSiteNamesShown    messageKey = "state.site_names.shown"
	msgSiteNamesWithheld messageKey = "state.site_names.withheld"

	// msgRetentionConfirm is a shorter retention horizon waiting for its
	// confirmation: the next purge deletes what is older, and that cannot be undone.
	msgRetentionConfirm messageKey = "notice.retention_confirm"

	msgValueNotNonNegative  messageKey = "error.value_not_non_negative"
	msgAggregateModeUnknown messageKey = "error.aggregate_mode_unknown"
)

// The Public Suffix List card: the last refresh's outcome, one key per probe, and
// the states that stand in for a figure.
const (
	msgPublicSuffixDownloaded  messageKey = "state.public_suffix.downloaded"
	msgPublicSuffixNotModified messageKey = "state.public_suffix.not_modified"
	msgPublicSuffixFailed      messageKey = "state.public_suffix.failed"
	msgPublicSuffixMalformed   messageKey = "state.public_suffix.malformed"
	msgNeverConfirmed          messageKey = "state.public_suffix.never_confirmed"
	msgVersionNotStated        messageKey = "state.public_suffix.version_not_stated"
)

// The units a duration is spelled and typed in, a count of one and any other count.
const (
	msgUnitDay     messageKey = "unit.day.one"
	msgUnitDays    messageKey = "unit.day.other"
	msgUnitHour    messageKey = "unit.hour.one"
	msgUnitHours   messageKey = "unit.hour.other"
	msgUnitMinute  messageKey = "unit.minute.one"
	msgUnitMinutes messageKey = "unit.minute.other"
	msgUnitSecond  messageKey = "unit.second.one"
	msgUnitSeconds messageKey = "unit.second.other"
	// msgDurationUnitUnknown refuses a unit that is not one of the four.
	msgDurationUnitUnknown messageKey = "error.duration_unit_unknown"
	// msgFieldPollInterval names a polled kind's interval field, which takes a unit.
	msgFieldPollInterval messageKey = "field.poll_interval"
)

// The outages the indicator counts, named on the Sources surface: a kind with no
// healthy implementation, or one implementation turned on and not healthy.
const (
	msgOutageKind     messageKey = "state.outage.kind"
	msgOutageProvider messageKey = "state.outage.provider"
)

// Why a network does not count now, beside the "no": the operator removed it, or
// discovery no longer reports it (decision D1).
const (
	msgCountsNoRemoved        messageKey = "state.counts.removed"
	msgCountsNoLongerDetected messageKey = "state.counts.no_longer_detected"
)

// A shorter retention waiting for its confirmation: the horizon in force and the one
// asked for.
const (
	msgLabelRetentionNow       messageKey = "collection.label.retention_now"
	msgLabelRetentionRequested messageKey = "collection.label.retention_requested"
)

// The refusals that state what is accepted, the settings surface's card refusal, and
// the Public Suffix List's interval field.
const (
	// msgValueOutOfRange refuses a value outside the range config.Load accepts, which
	// is stated beside it; msgRangeUnbounded is a range with no upper bound.
	msgValueOutOfRange messageKey = "error.value_out_of_range"
	msgRangeUnbounded  messageKey = "range.unbounded"
	// msgSettingsCardUnknown refuses a submission naming no card of the surface.
	msgSettingsCardUnknown       messageKey = "error.settings_card_unknown"
	msgFieldPublicSuffixInterval messageKey = "field.public_suffix_interval"
)

// One form per page, one Save per card (settings.go, collection_settings.go).
const (
	// msgCardNotNamed refuses a submission that named no card -- the Enter key's --
	// and stores nothing.
	msgCardNotNamed messageKey = "error.card_not_named"
	// msgEditedNotSaved marks a card drawn with edits another card's save did not
	// store; msgSecretNotKept is a secret typed and not stored, to be typed again.
	msgEditedNotSaved messageKey = "notice.edited_not_saved"
	msgSecretNotKept  messageKey = "state.secret_not_kept"
	// msgOpenInOPNsense heads the OPNsense pages that turn an installed provider on.
	msgOpenInOPNsense messageKey = "sources.label.opnsense_page"
)

// The OPNsense pages that turn an installed provider on, named as OPNsense's own
// breadcrumb names them (opnsense_pages.go).
const (
	msgPageIDSAdministration messageKey = "opnsense_page.ids_administration"
	msgPageKeaDHCPv4         messageKey = "opnsense_page.kea_dhcpv4"
	msgPageDnsmasqDHCPRanges messageKey = "opnsense_page.dnsmasq_dhcp_ranges"
	msgPageUnboundGeneral    messageKey = "opnsense_page.unbound_general"
	msgPageUnboundReporting  messageKey = "opnsense_page.unbound_reporting"
	msgPageNetFlow           messageKey = "opnsense_page.netflow"
)

// surfaceMessageKeys is every key above, for messages.go's bookkeeping.
func surfaceMessageKeys() []messageKey {
	return []messageKey{
		msgValueOutOfRange, msgRangeUnbounded, msgSettingsCardUnknown, msgFieldPublicSuffixInterval,
		msgCardNotNamed, msgEditedNotSaved, msgSecretNotKept, msgOpenInOPNsense,
		msgPageIDSAdministration, msgPageKeaDHCPv4, msgPageDnsmasqDHCPRanges, msgPageUnboundGeneral,
		msgPageUnboundReporting, msgPageNetFlow,
		msgUnitDay, msgUnitDays, msgUnitHour, msgUnitHours, msgUnitMinute, msgUnitMinutes,
		msgUnitSecond, msgUnitSeconds, msgDurationUnitUnknown, msgFieldPollInterval,
		msgOutageKind, msgOutageProvider, msgCountsNoRemoved, msgCountsNoLongerDetected,
		msgLabelRetentionNow, msgLabelRetentionRequested,

		msgTitleInterfaces, msgTitleSources,

		msgAvailabilityHealthy, msgAvailabilityNotCollecting, msgAvailabilityUnavailable,
		msgAvailabilityNotYetProbed, msgAvailabilityUnknown,
		msgYes, msgNo, msgNeverChecked, msgNoDetail,

		msgKindFlowVolume, msgKindGeoASN, msgKindMeasurementSample, msgKindPublicSuffix,
		msgKindResolverCache, msgKindResolverLocalData,
		msgProviderInsightFlow, msgProviderMaxMind, msgProviderInsightSampling,
		msgProviderPublicSuffixList, msgProviderUnboundCache, msgProviderDnsmasqCache,
		msgProviderUnboundLocalData, msgProviderDnsmasqLocalData,
		msgGapDownloadFailed, msgFieldPageSize, msgInterfaceActionUnknown, msgLinkLocalInvalid,
		msgPublicSuffixNotYetRefreshed,

		msgOriginDetected, msgOriginOperator, msgInterfaceSaved,
		msgInterfaceUnknown, msgInterfaceUpstream, msgNetworkUnknown,
		msgNetworkMalformed, msgNetworkHostBits, msgNetworkIPv4Mapped, msgNetworkLinkLocal,

		msgCardPairing, msgCardLeaseBackends, msgCardAttribution, msgCardStorage,
		msgLabelPairingWindow, msgLabelLeaseFailureLimit, msgLabelAttributionDelay,
		msgLabelCacheAnswerCap, msgLabelRetention, msgLabelSiteNames,
		msgSiteNamesShown, msgSiteNamesWithheld, msgRetentionConfirm,
		msgValueNotNonNegative, msgAggregateModeUnknown,

		msgPublicSuffixDownloaded, msgPublicSuffixNotModified, msgPublicSuffixFailed,
		msgPublicSuffixMalformed, msgNeverConfirmed, msgVersionNotStated,
	}
}
