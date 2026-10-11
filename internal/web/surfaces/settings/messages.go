package settings

// The catalogue keys this surface's code chooses. A handler chooses a key, never a string;
// the words are in locale/en.json beside this file, or in the core's locale when another
// surface uses the key too.

const (
	msgAPIKeyRequired messageKey = "error.api_key_required"
	// msgCardNotNamed refuses a submission that named no card -- the Enter key's --
	// and stores nothing.
	msgCardNotNamed                messageKey = "error.card_not_named"
	msgCertificateNotFetched       messageKey = "error.certificate_not_fetched"
	msgCertificatePresented        messageKey = "notice.certificate_presented"
	msgCollectionValueInvalid      messageKey = "error.collection_value_invalid"
	msgCredentialAbsent            messageKey = "state.credential.absent"
	msgCredentialReady             messageKey = "state.credential.ready"
	msgCredentialUndecryptable     messageKey = "state.credential.undecryptable"
	msgFieldPublicSuffixInterval   messageKey = "field.public_suffix_interval"
	msgFingerprintInvalid          messageKey = "error.fingerprint_invalid"
	msgFirewallURLInvalid          messageKey = "error.firewall_url_invalid"
	msgFirewallURLRequired         messageKey = "error.firewall_url_required"
	msgGeoIPCurrent                messageKey = "state.geoip.current"
	msgGeoIPFailed                 messageKey = "state.geoip.download_failed"
	msgGeoIPKeyUnreadable          messageKey = "state.geoip.licence_key_undecryptable"
	msgGeoIPLimited                messageKey = "state.geoip.download_limit_reached"
	msgGeoIPNoAccountID            messageKey = "state.geoip.no_account_id"
	msgGeoIPNoLicenceKey           messageKey = "state.geoip.no_licence_key"
	msgGeoIPNotDownloaded          messageKey = "state.geoip.not_downloaded"
	msgGeoIPRefused                messageKey = "state.geoip.download_refused"
	msgLicenceKeyAbsent            messageKey = "state.licence_key.absent"
	msgLicenceKeyStored            messageKey = "state.licence_key.stored"
	msgLicenceKeyUndecryptable     messageKey = "state.licence_key.undecryptable"
	msgMaxMindAccountIDBad         messageKey = "error.maxmind_account_id_invalid"
	msgNeverConfirmed              messageKey = "state.public_suffix.never_confirmed"
	msgNo                          messageKey = "state.no"
	msgPublicSuffixDownloaded      messageKey = "state.public_suffix.downloaded"
	msgPublicSuffixFailed          messageKey = "state.public_suffix.failed"
	msgPublicSuffixMalformed       messageKey = "state.public_suffix.malformed"
	msgPublicSuffixNotModified     messageKey = "state.public_suffix.not_modified"
	msgPublicSuffixNotYetRefreshed messageKey = "state.public_suffix.not_yet_refreshed"
	// msgSettingsCardUnknown refuses a submission naming no card of the surface.
	msgSettingsCardUnknown      messageKey = "error.settings_card_unknown"
	msgSettingsSaved            messageKey = "notice.settings_saved"
	msgThemeUnknown             messageKey = "error.theme_unknown"
	msgTitleSettings            messageKey = "settings.title"
	msgVerifyCertificateRefused messageKey = "state.verification.certificate_refused"
	msgVerifyCredentialsBad     messageKey = "state.verification.credentials_rejected"
	msgVerifyEndpointNotFound   messageKey = "state.verification.endpoint_not_found"
	msgVerifyNoURL              messageKey = "state.verification.no_url"
	msgVerifyNotAttempted       messageKey = "state.verification.not_attempted"
	msgVerifyUnexpectedAnswer   messageKey = "state.verification.unexpected_answer"
	msgVerifyUnreachable        messageKey = "state.verification.unreachable"
	msgVerifyVerified           messageKey = "state.verification.verified"
	msgVersionNotStated         messageKey = "state.public_suffix.version_not_stated"
	msgYes                      messageKey = "state.yes"
)

// keys is every key above, so a test can assert that each exists in the catalogue and that
// no catalogue entry is unreachable.
func keys() []messageKey {
	return []messageKey{
		msgAPIKeyRequired,
		msgCardNotNamed,
		msgCertificateNotFetched,
		msgCertificatePresented,
		msgCollectionValueInvalid,
		msgCredentialAbsent,
		msgCredentialReady,
		msgCredentialUndecryptable,
		msgFieldPublicSuffixInterval,
		msgFingerprintInvalid,
		msgFirewallURLInvalid,
		msgFirewallURLRequired,
		msgGeoIPCurrent,
		msgGeoIPFailed,
		msgGeoIPKeyUnreadable,
		msgGeoIPLimited,
		msgGeoIPNoAccountID,
		msgGeoIPNoLicenceKey,
		msgGeoIPNotDownloaded,
		msgGeoIPRefused,
		msgLicenceKeyAbsent,
		msgLicenceKeyStored,
		msgLicenceKeyUndecryptable,
		msgMaxMindAccountIDBad,
		msgNeverConfirmed,
		msgNo,
		msgPublicSuffixDownloaded,
		msgPublicSuffixFailed,
		msgPublicSuffixMalformed,
		msgPublicSuffixNotModified,
		msgPublicSuffixNotYetRefreshed,
		msgSettingsCardUnknown,
		msgSettingsSaved,
		msgThemeUnknown,
		msgTitleSettings,
		msgVerifyCertificateRefused,
		msgVerifyCredentialsBad,
		msgVerifyEndpointNotFound,
		msgVerifyNoURL,
		msgVerifyNotAttempted,
		msgVerifyUnexpectedAnswer,
		msgVerifyUnreachable,
		msgVerifyVerified,
		msgVersionNotStated,
		msgYes,
	}
}
