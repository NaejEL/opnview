package settings_test

import "github.com/NaejEL/opnview/internal/web"

// The catalogue keys these tests name, written out here and not imported from the code under
// test: a test that read its keys from the code could not notice the code changing one. A JSON
// answer carries each as its closed code (web.ConditionOf).
type messageKey = web.MessageKey

const (
	msgCredentialAbsent            messageKey = "state.credential.absent"
	msgCredentialReady             messageKey = "state.credential.ready"
	msgCredentialUndecryptable     messageKey = "state.credential.undecryptable"
	msgGeoIPCurrent                messageKey = "state.geoip.current"
	msgGeoIPFailed                 messageKey = "state.geoip.download_failed"
	msgGeoIPKeyUnreadable          messageKey = "state.geoip.licence_key_undecryptable"
	msgGeoIPLimited                messageKey = "state.geoip.download_limit_reached"
	msgGeoIPNoAccountID            messageKey = "state.geoip.no_account_id"
	msgGeoIPNoLicenceKey           messageKey = "state.geoip.no_licence_key"
	msgGeoIPNotDownloaded          messageKey = "state.geoip.not_downloaded"
	msgGeoIPRefused                messageKey = "state.geoip.download_refused"
	msgPublicSuffixDownloaded      messageKey = "state.public_suffix.downloaded"
	msgPublicSuffixFailed          messageKey = "state.public_suffix.failed"
	msgPublicSuffixMalformed       messageKey = "state.public_suffix.malformed"
	msgPublicSuffixNotModified     messageKey = "state.public_suffix.not_modified"
	msgPublicSuffixNotYetRefreshed messageKey = "state.public_suffix.not_yet_refreshed"
	msgVerifyCredentialsBad        messageKey = "state.verification.credentials_rejected"
	msgVerifyEndpointNotFound      messageKey = "state.verification.endpoint_not_found"
	msgVerifyUnreachable           messageKey = "state.verification.unreachable"
	msgVerifyVerified              messageKey = "state.verification.verified"
)
