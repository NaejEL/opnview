package settings

// The catalogue keys this surface's code chooses. A handler chooses a key, never a string;
// the words are in locale/en.json beside this file, or in the core's locale when another
// surface uses the key too.

const (
	msgAPIKeyRequired messageKey = "error.api_key_required"
	// msgCardNotNamed refuses a submission that named no card -- the Enter key's --
	// and stores nothing.
	msgCardNotNamed            messageKey = "error.card_not_named"
	msgCertificateNotFetched   messageKey = "error.certificate_not_fetched"
	msgCertificatePresented    messageKey = "notice.certificate_presented"
	msgCredentialAbsent        messageKey = "state.credential.absent"
	msgCredentialReady         messageKey = "state.credential.ready"
	msgCredentialUndecryptable messageKey = "state.credential.undecryptable"
	msgFingerprintInvalid      messageKey = "error.fingerprint_invalid"
	msgFirewallURLInvalid      messageKey = "error.firewall_url_invalid"
	msgFirewallURLRequired     messageKey = "error.firewall_url_required"
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
)

// keys is every key above, so a test can assert that each exists in the catalogue and that
// no catalogue entry is unreachable.
func keys() []messageKey {
	return []messageKey{
		msgAPIKeyRequired,
		msgCardNotNamed,
		msgCertificateNotFetched,
		msgCertificatePresented,
		msgCredentialAbsent,
		msgCredentialReady,
		msgCredentialUndecryptable,
		msgFingerprintInvalid,
		msgFirewallURLInvalid,
		msgFirewallURLRequired,
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
	}
}
