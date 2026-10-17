package settings_test

import "github.com/NaejEL/opnview/internal/web"

// The catalogue keys these tests name, written out here and not imported from the code under
// test: a test that read its keys from the code could not notice the code changing one.
type messageKey = web.MessageKey

const (
	msgCardNotNamed            messageKey = "error.card_not_named"
	msgCredentialAbsent        messageKey = "state.credential.absent"
	msgCredentialReady         messageKey = "state.credential.ready"
	msgCredentialUndecryptable messageKey = "state.credential.undecryptable"
	msgEditedNotSaved          messageKey = "notice.edited_not_saved"
	msgFieldPollInterval       messageKey = "field.poll_interval"
	msgLabelSiteNames          messageKey = "collection.label.site_names"
	msgSecretNotKept           messageKey = "state.secret_not_kept"
	msgSettingsCardUnknown     messageKey = "error.settings_card_unknown"
	msgSettingsSaved           messageKey = "notice.settings_saved"
	msgSiteNamesShown          messageKey = "state.site_names.shown"
	msgSiteNamesWithheld       messageKey = "state.site_names.withheld"
	msgVerifyCredentialsBad    messageKey = "state.verification.credentials_rejected"
	msgVerifyEndpointNotFound  messageKey = "state.verification.endpoint_not_found"
	msgVerifyNotAttempted      messageKey = "state.verification.not_attempted"
	msgVerifyUnreachable       messageKey = "state.verification.unreachable"
	msgVerifyVerified          messageKey = "state.verification.verified"
)
