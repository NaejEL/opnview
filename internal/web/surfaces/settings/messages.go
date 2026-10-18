package settings

// The catalogue keys this surface's code chooses. A handler chooses a key, never a string; a
// JSON answer carries its closed code (web.ConditionOf), which the front translates through the
// catalogue.

const (
	msgAPIKeyRequired           messageKey = "error.api_key_required"
	msgCertificateNotFetched    messageKey = "error.certificate_not_fetched"
	msgCredentialAbsent         messageKey = "state.credential.absent"
	msgCredentialReady          messageKey = "state.credential.ready"
	msgCredentialUndecryptable  messageKey = "state.credential.undecryptable"
	msgFingerprintInvalid       messageKey = "error.fingerprint_invalid"
	msgFirewallURLInvalid       messageKey = "error.firewall_url_invalid"
	msgFirewallURLRequired      messageKey = "error.firewall_url_required"
	msgVerifyCertificateRefused messageKey = "state.verification.certificate_refused"
	msgVerifyCredentialsBad     messageKey = "state.verification.credentials_rejected"
	msgVerifyEndpointNotFound   messageKey = "state.verification.endpoint_not_found"
	msgVerifyNoURL              messageKey = "state.verification.no_url"
	msgVerifyNotAttempted       messageKey = "state.verification.not_attempted"
	msgVerifyUnexpectedAnswer   messageKey = "state.verification.unexpected_answer"
	msgVerifyUnreachable        messageKey = "state.verification.unreachable"
	msgVerifyVerified           messageKey = "state.verification.verified"
)

// keys is every key above, so a test can assert that each exists in the catalogue.
func keys() []messageKey {
	return []messageKey{
		msgAPIKeyRequired,
		msgCertificateNotFetched,
		msgCredentialAbsent,
		msgCredentialReady,
		msgCredentialUndecryptable,
		msgFingerprintInvalid,
		msgFirewallURLInvalid,
		msgFirewallURLRequired,
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
