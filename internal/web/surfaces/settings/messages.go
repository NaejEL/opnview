package settings

// The catalogue keys this surface's code chooses. A handler chooses a key, never a string; a
// JSON answer carries its closed code (web.ConditionOf), which the front translates through the
// catalogue.

const (
	msgAPIKeyRequired              messageKey = "error.api_key_required"
	msgCertificateNotFetched       messageKey = "error.certificate_not_fetched"
	msgCollectionValueInvalid      messageKey = "error.collection_value_invalid"
	msgCredentialAbsent            messageKey = "state.credential.absent"
	msgCredentialReady             messageKey = "state.credential.ready"
	msgCredentialUndecryptable     messageKey = "state.credential.undecryptable"
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
	msgPublicSuffixDownloaded      messageKey = "state.public_suffix.downloaded"
	msgPublicSuffixFailed          messageKey = "state.public_suffix.failed"
	msgPublicSuffixMalformed       messageKey = "state.public_suffix.malformed"
	msgPublicSuffixNotModified     messageKey = "state.public_suffix.not_modified"
	msgPublicSuffixNotYetRefreshed messageKey = "state.public_suffix.not_yet_refreshed"
	msgVerifyCertificateRefused    messageKey = "state.verification.certificate_refused"
	msgVerifyCredentialsBad        messageKey = "state.verification.credentials_rejected"
	msgVerifyEndpointNotFound      messageKey = "state.verification.endpoint_not_found"
	msgVerifyNoURL                 messageKey = "state.verification.no_url"
	msgVerifyNotAttempted          messageKey = "state.verification.not_attempted"
	msgVerifyUnexpectedAnswer      messageKey = "state.verification.unexpected_answer"
	msgVerifyUnreachable           messageKey = "state.verification.unreachable"
	msgVerifyVerified              messageKey = "state.verification.verified"
)

// keys is every key above, so a test can assert that each exists in the catalogue.
func keys() []messageKey {
	return []messageKey{
		msgAPIKeyRequired,
		msgCertificateNotFetched,
		msgCollectionValueInvalid,
		msgCredentialAbsent,
		msgCredentialReady,
		msgCredentialUndecryptable,
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
		msgPublicSuffixDownloaded,
		msgPublicSuffixFailed,
		msgPublicSuffixMalformed,
		msgPublicSuffixNotModified,
		msgPublicSuffixNotYetRefreshed,
		msgVerifyCertificateRefused,
		msgVerifyCredentialsBad,
		msgVerifyEndpointNotFound,
		msgVerifyNoURL,
		msgVerifyNotAttempted,
		msgVerifyUnexpectedAnswer,
		msgVerifyUnreachable,
		msgVerifyVerified,
	}
}
