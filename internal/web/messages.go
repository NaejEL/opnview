package web

// The catalogue keys a handler may choose.
//
// A HANDLER CHOOSES A KEY, NEVER A STRING. Every one of these is a catalogue key
// and nothing else, so the only place a reader's words exist is locale/en.json. A
// test asserts that each of these keys exists in the catalogue and that every
// catalogue entry is reached either from here or from a template, so an entry
// nobody uses and a key nobody wrote are both failures.
//
// messageKey is its own type so that a plain string cannot be handed to a
// template where a key belongs.
type messageKey string

// The notices: something the reader asked for happened.
const (
	msgSetupDone            messageKey = "notice.setup_done"
	msgSettingsSaved        messageKey = "notice.settings_saved"
	msgPasswordReset        messageKey = "notice.password_reset"
	msgSignedOut            messageKey = "notice.signed_out"
	msgCertificatePresented messageKey = "notice.certificate_presented"
	msgCollectionSaved      messageKey = "notice.collection_saved"
	msgSuggestionFilled     messageKey = "notice.suggestion_filled"
	msgSuggestionNotFilled  messageKey = "notice.suggestion_not_filled"
)

// The refusals: something the reader asked for did not happen, and why.
//
// NONE OF THEM DISCLOSES WHETHER A LOGIN EXISTS. msgSignInRefused is the single
// answer to both a login that does not exist and a password that does not verify,
// which is what makes the two responses indistinguishable.
const (
	msgSetupTokenRefused     messageKey = "error.setup_token_refused"
	msgSetupClosed           messageKey = "error.setup_closed"
	msgLoginRequired         messageKey = "error.login_required"
	msgPasswordRequired      messageKey = "error.password_required"
	msgPasswordMismatch      messageKey = "error.password_mismatch"
	msgPasswordTooShort      messageKey = "error.password_too_short"
	msgSignInRefused         messageKey = "error.sign_in_refused"
	msgSessionExpired        messageKey = "error.session_expired"
	msgFormTokenRefused      messageKey = "error.form_token_refused"
	msgKeyFileRefused        messageKey = "error.key_file_refused"
	msgFirewallURLInvalid    messageKey = "error.firewall_url_invalid"
	msgFingerprintInvalid    messageKey = "error.fingerprint_invalid"
	msgFirewallURLRequired   messageKey = "error.firewall_url_required"
	msgCertificateNotFetched messageKey = "error.certificate_not_fetched"
	msgAPIKeyRequired        messageKey = "error.api_key_required"
	msgThemeUnknown          messageKey = "error.theme_unknown"
	msgInternal              messageKey = "error.internal"

	msgCollectionValueInvalid messageKey = "error.collection_value_invalid"
	msgCollectionKindUnknown  messageKey = "error.collection_kind_unknown"
	// msgCollectionIntervalTooLarge refuses an interval no duration can hold. It is a
	// refusal of its own rather than "not a number", because the figure IS a number
	// and telling the operator otherwise would send them looking for a typo.
	msgCollectionIntervalTooLarge messageKey = "error.collection_interval_too_large"
)

// The collection surface's labels. Every one of them names a figure; none of them
// explains it, because a label that needed a sentence would be the wrong label and
// ROADMAP.md step 7 forbids the sentence either way.
const (
	msgLabelSourceState       messageKey = "collection.label.source_state"
	msgLabelSelection         messageKey = "collection.label.selection"
	msgLabelPageSizeInForce   messageKey = "collection.label.page_size_in_force"
	msgLabelIntervalInForce   messageKey = "collection.label.interval_in_force"
	msgLabelPageCeiling       messageKey = "collection.label.page_ceiling"
	msgLabelPageState         messageKey = "collection.label.page_state"
	msgLabelWindowStart       messageKey = "collection.label.window_start"
	msgLabelWindowEnd         messageKey = "collection.label.window_end"
	msgLabelRetentionHorizon  messageKey = "collection.label.retention_horizon"
	msgLabelCoveredFrom       messageKey = "collection.label.covered_from"
	msgLabelCoveredTo         messageKey = "collection.label.covered_to"
	msgLabelRecordCount       messageKey = "collection.label.record_count"
	msgLabelMeanRateSource    messageKey = "collection.label.mean_rate_source"
	msgLabelMeanRateIngested  messageKey = "collection.label.mean_rate_ingested"
	msgLabelPeakRate          messageKey = "collection.label.peak_rate"
	msgLabelGapCount          messageKey = "collection.label.gap_count"
	msgLabelMissedSeconds     messageKey = "collection.label.missed_seconds"
	msgLabelGapReason         messageKey = "collection.label.gap_reason"
	msgLabelSuggestedPageSize messageKey = "collection.label.suggested_page_size"
	msgLabelSuggestedInterval messageKey = "collection.label.suggested_interval"
	msgLabelSuggestionState   messageKey = "collection.label.suggestion_state"
)

// The four kinds the collection surface sizes. The KIND is read from the store at
// runtime; the key naming it comes from its registry row, so no template spells a
// kind and a kind with no provider row produces no card at all.
const (
	msgKindFirewallLog   messageKey = "collection.kind.firewall_log"
	msgKindSecurityEvent messageKey = "collection.kind.security_event"
	msgKindDHCPLease     messageKey = "collection.kind.dhcp_lease"
	msgKindDNSLookup     messageKey = "collection.kind.dns_lookup"
)

// The three reasons collection_gap records. The vocabulary is the schema's closed
// one, and these are the keys that name each value to a reader.
const (
	msgGapDigestOutsideWindow messageKey = "gap_reason.digest_outside_returned_window"
	msgGapEveRotationLost     messageKey = "gap_reason.eve_rotation_lost"
	msgGapResolverWindow      messageKey = "gap_reason.resolver_window_not_honoured"
)

// The states the collection surface reports in place of a figure.
//
// EACH IS A SEPARATE SENTENCE BECAUSE EACH IS A SEPARATE FACT. A source the operator
// declined, a source the firewall reports unavailable, a source installed and
// switched off, a window holding no record, a table with no second clock and a span
// too short to divide by are six different conditions. Rendering any of them as a
// rate of nought would say "we looked and there was none" where the truth is "we
// could not look".
const (
	msgSourceReachable   messageKey = "state.sizing.source_reachable"
	msgSourceUnavailable messageKey = "state.sizing.source_unavailable"
	msgSourceDisabled    messageKey = "state.sizing.source_disabled"

	// THE SELECTION IS NOT THE AVAILABILITY, and the two are reported as two rows
	// because they are two facts: what the firewall says, and what the operator has
	// decided. internal/config says so where the selection is defined, and a surface
	// collapsing them would hide an operator's own decision behind a probe result.
	msgSelectionAuto messageKey = "state.sizing.selection_auto"
	msgSelectionOn   messageKey = "state.sizing.selection_on"
	msgSelectionOff  messageKey = "state.sizing.selection_off"

	msgNoRecordsInWindow  messageKey = "state.sizing.no_records_in_window"
	msgNoIngestedClock    messageKey = "state.sizing.no_ingested_clock"
	msgRateNotMeasured    messageKey = "state.sizing.rate_not_measured"
	msgRetentionUnlimited messageKey = "state.sizing.retention_unlimited"

	msgPageNotAdjustable messageKey = "state.sizing.page_not_adjustable"
	msgNoPageCeiling     messageKey = "state.sizing.no_page_ceiling"
	msgPageWithinCeiling messageKey = "state.sizing.page_within_ceiling"
	msgPageAboveCeiling  messageKey = "state.sizing.page_above_ceiling"

	msgSuggestionAvailable          messageKey = "state.sizing.suggestion_available"
	msgSuggestionNotYetMeasured     messageKey = "state.sizing.not_yet_measured"
	msgSuggestionNotCoverable       messageKey = "state.sizing.rate_not_coverable"
	msgSuggestionSourceNotCollected messageKey = "state.sizing.source_not_collected"
)

// The stored-credential states. They are the three config.CredentialState values,
// and they are three separate strings because two of them were going to be
// reported as one: a key file that has been lost or replaced is NOT "no firewall
// is configured", and reporting it as that would send the reader to look for a
// setting that is already there.
const (
	msgCredentialAbsent        messageKey = "state.credential.absent"
	msgCredentialReady         messageKey = "state.credential.ready"
	msgCredentialUndecryptable messageKey = "state.credential.undecryptable"
)

// The MaxMind licence-key states. The key is STORED AND NOT VERIFIED in this
// cycle: verifying it means downloading, the download is cycle 4C, and a
// verification download here would be an outbound call made before it is needed.
const (
	msgLicenceKeyAbsent        messageKey = "state.licence_key.absent"
	msgLicenceKeyStored        messageKey = "state.licence_key.stored"
	msgLicenceKeyUndecryptable messageKey = "state.licence_key.undecryptable"
)

// The verification outcomes, one per thing the firewall's answer can mean. They
// come from the opnsense.Outcome vocabulary, which already separates 401/403 from
// 404 from unreachable, and NONE of them is reported as success.
const (
	msgVerifyNotAttempted       messageKey = "state.verification.not_attempted"
	msgVerifyVerified           messageKey = "state.verification.verified"
	msgVerifyCredentialsBad     messageKey = "state.verification.credentials_rejected"
	msgVerifyEndpointNotFound   messageKey = "state.verification.endpoint_not_found"
	msgVerifyUnreachable        messageKey = "state.verification.unreachable"
	msgVerifyCertificateRefused messageKey = "state.verification.certificate_refused"
	msgVerifyUnexpectedAnswer   messageKey = "state.verification.unexpected_answer"
	msgVerifyNoURL              messageKey = "state.verification.no_url"
)

// The page titles. They are keys like every other user-visible string: a title is
// what the browser tab and the heading of a surface say.
const (
	msgTitleSetup    messageKey = "setup.title"
	msgTitleSignIn   messageKey = "sign_in.title"
	msgTitleSettings messageKey = "settings.title"
	msgTitleRecover  messageKey = "recover.title"
	// msgTitleCollection names the collection surface, which is a route of its own
	// rather than a fourth card on the settings page.
	msgTitleCollection messageKey = "collection.title"
)

// The theme names. The theme is one setting row for the installation, so these
// are the three values that row may hold.
const (
	msgThemeSystem messageKey = "theme.system"
	msgThemeLight  messageKey = "theme.light"
	msgThemeDark   messageKey = "theme.dark"
)

// handlerMessageKeys is every key above, in one place, so a test can assert that
// each exists in the catalogue and that no catalogue entry is unreachable.
//
// It is a function rather than a variable so nothing can append to it at run time.
// A key declared above and left out of this list fails that test, which is the
// point of keeping both in one file.
func handlerMessageKeys() []messageKey {
	return []messageKey{
		msgSetupDone, msgSettingsSaved, msgPasswordReset, msgSignedOut,
		msgCertificatePresented,

		msgSetupTokenRefused, msgSetupClosed, msgLoginRequired, msgPasswordRequired,
		msgPasswordMismatch, msgPasswordTooShort, msgSignInRefused, msgSessionExpired,
		msgFormTokenRefused, msgKeyFileRefused, msgFirewallURLInvalid,
		msgFingerprintInvalid, msgFirewallURLRequired, msgCertificateNotFetched,
		msgAPIKeyRequired, msgThemeUnknown, msgInternal,

		msgCredentialAbsent, msgCredentialReady, msgCredentialUndecryptable,

		msgLicenceKeyAbsent, msgLicenceKeyStored, msgLicenceKeyUndecryptable,

		msgVerifyNotAttempted, msgVerifyVerified, msgVerifyCredentialsBad,
		msgVerifyEndpointNotFound, msgVerifyUnreachable, msgVerifyCertificateRefused,
		msgVerifyUnexpectedAnswer, msgVerifyNoURL,

		msgTitleSetup, msgTitleSignIn, msgTitleSettings, msgTitleRecover,
		msgTitleCollection,

		msgThemeSystem, msgThemeLight, msgThemeDark,

		msgCollectionSaved, msgSuggestionFilled, msgSuggestionNotFilled,
		msgCollectionValueInvalid, msgCollectionKindUnknown, msgCollectionIntervalTooLarge,

		msgLabelSourceState, msgLabelPageSizeInForce, msgLabelIntervalInForce,
		msgLabelPageCeiling, msgLabelPageState, msgLabelWindowStart, msgLabelWindowEnd,
		msgLabelRetentionHorizon, msgLabelCoveredFrom, msgLabelCoveredTo,
		msgLabelRecordCount, msgLabelMeanRateSource, msgLabelMeanRateIngested,
		msgLabelPeakRate, msgLabelGapCount, msgLabelMissedSeconds, msgLabelGapReason,
		msgLabelSuggestedPageSize, msgLabelSuggestedInterval, msgLabelSuggestionState,

		msgKindFirewallLog, msgKindSecurityEvent, msgKindDHCPLease, msgKindDNSLookup,

		msgGapDigestOutsideWindow, msgGapEveRotationLost, msgGapResolverWindow,

		msgSourceReachable, msgSourceUnavailable, msgSourceDisabled,
		msgSelectionAuto, msgSelectionOn, msgSelectionOff,
		msgLabelSelection, msgNoRecordsInWindow, msgNoIngestedClock,
		msgRateNotMeasured, msgRetentionUnlimited, msgPageNotAdjustable,
		msgNoPageCeiling, msgPageWithinCeiling, msgPageAboveCeiling,
		msgSuggestionAvailable, msgSuggestionNotYetMeasured, msgSuggestionNotCoverable,
		msgSuggestionSourceNotCollected,
	}
}
