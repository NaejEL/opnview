package settings_test

import "github.com/NaejEL/opnview/internal/web"

// The catalogue keys these tests name, written out here and not imported from the code under
// test: a test that read its keys from the code could not notice the code changing one.
type messageKey = web.MessageKey

const (
	msgCardNotNamed                messageKey = "error.card_not_named"
	msgCredentialAbsent            messageKey = "state.credential.absent"
	msgCredentialReady             messageKey = "state.credential.ready"
	msgCredentialUndecryptable     messageKey = "state.credential.undecryptable"
	msgEditedNotSaved              messageKey = "notice.edited_not_saved"
	msgFieldPollInterval           messageKey = "field.poll_interval"
	msgGeoIPCurrent                messageKey = "state.geoip.current"
	msgGeoIPFailed                 messageKey = "state.geoip.download_failed"
	msgGeoIPKeyUnreadable          messageKey = "state.geoip.licence_key_undecryptable"
	msgGeoIPLimited                messageKey = "state.geoip.download_limit_reached"
	msgGeoIPNoAccountID            messageKey = "state.geoip.no_account_id"
	msgGeoIPNoLicenceKey           messageKey = "state.geoip.no_licence_key"
	msgGeoIPNotDownloaded          messageKey = "state.geoip.not_downloaded"
	msgGeoIPRefused                messageKey = "state.geoip.download_refused"
	msgKindRegistrableDomain       messageKey = "collection.kind.registrable_domain"
	msgLabelGeoIPBuild             messageKey = "settings.label.geoip_build"
	msgLabelGeoIPState             messageKey = "settings.label.geoip_state"
	msgLabelSiteNames              messageKey = "collection.label.site_names"
	msgMaxMindAccountIDBad         messageKey = "error.maxmind_account_id_invalid"
	msgNeverConfirmed              messageKey = "state.public_suffix.never_confirmed"
	msgNo                          messageKey = "state.no"
	msgPublicSuffixDownloaded      messageKey = "state.public_suffix.downloaded"
	msgPublicSuffixFailed          messageKey = "state.public_suffix.failed"
	msgPublicSuffixMalformed       messageKey = "state.public_suffix.malformed"
	msgPublicSuffixNotModified     messageKey = "state.public_suffix.not_modified"
	msgPublicSuffixNotYetRefreshed messageKey = "state.public_suffix.not_yet_refreshed"
	msgSecretNotKept               messageKey = "state.secret_not_kept"
	msgSettingsCardUnknown         messageKey = "error.settings_card_unknown"
	msgSettingsSaved               messageKey = "notice.settings_saved"
	msgSiteNamesShown              messageKey = "state.site_names.shown"
	msgSiteNamesWithheld           messageKey = "state.site_names.withheld"
	msgVerifyCredentialsBad        messageKey = "state.verification.credentials_rejected"
	msgVerifyEndpointNotFound      messageKey = "state.verification.endpoint_not_found"
	msgVerifyNotAttempted          messageKey = "state.verification.not_attempted"
	msgVerifyUnreachable           messageKey = "state.verification.unreachable"
	msgVerifyVerified              messageKey = "state.verification.verified"
	msgYes                         messageKey = "state.yes"
)
