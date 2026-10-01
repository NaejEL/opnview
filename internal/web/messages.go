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
	msgSetupDone     messageKey = "notice.setup_done"
	msgSettingsSaved messageKey = "notice.settings_saved"
	msgPasswordReset messageKey = "notice.password_reset"
	msgSignedOut     messageKey = "notice.signed_out"
)

// The refusals: something the reader asked for did not happen, and why.
//
// NONE OF THEM DISCLOSES WHETHER A LOGIN EXISTS. msgSignInRefused is the single
// answer to both a login that does not exist and a password that does not verify,
// which is what makes the two responses indistinguishable.
const (
	msgSetupTokenRefused  messageKey = "error.setup_token_refused"
	msgSetupClosed        messageKey = "error.setup_closed"
	msgLoginRequired      messageKey = "error.login_required"
	msgPasswordRequired   messageKey = "error.password_required"
	msgPasswordMismatch   messageKey = "error.password_mismatch"
	msgPasswordTooShort   messageKey = "error.password_too_short"
	msgSignInRefused      messageKey = "error.sign_in_refused"
	msgSessionExpired     messageKey = "error.session_expired"
	msgFormTokenRefused   messageKey = "error.form_token_refused"
	msgKeyFileRefused     messageKey = "error.key_file_refused"
	msgFirewallURLInvalid messageKey = "error.firewall_url_invalid"
	msgAPIKeyRequired     messageKey = "error.api_key_required"
	msgThemeUnknown       messageKey = "error.theme_unknown"
	msgInternal           messageKey = "error.internal"
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
	msgVerifyNotAttempted     messageKey = "state.verification.not_attempted"
	msgVerifyVerified         messageKey = "state.verification.verified"
	msgVerifyCredentialsBad   messageKey = "state.verification.credentials_rejected"
	msgVerifyEndpointNotFound messageKey = "state.verification.endpoint_not_found"
	msgVerifyUnreachable      messageKey = "state.verification.unreachable"
	msgVerifyUnexpectedAnswer messageKey = "state.verification.unexpected_answer"
	msgVerifyNoURL            messageKey = "state.verification.no_url"
)

// The page titles. They are keys like every other user-visible string: a title is
// what the browser tab and the heading of a surface say.
const (
	msgTitleSetup    messageKey = "setup.title"
	msgTitleSignIn   messageKey = "sign_in.title"
	msgTitleSettings messageKey = "settings.title"
	msgTitleRecover  messageKey = "recover.title"
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
func handlerMessageKeys() []messageKey {
	return []messageKey{
		msgSetupDone, msgSettingsSaved, msgPasswordReset, msgSignedOut,

		msgSetupTokenRefused, msgSetupClosed, msgLoginRequired, msgPasswordRequired,
		msgPasswordMismatch, msgPasswordTooShort, msgSignInRefused, msgSessionExpired,
		msgFormTokenRefused, msgKeyFileRefused, msgFirewallURLInvalid,
		msgAPIKeyRequired, msgThemeUnknown, msgInternal,

		msgCredentialAbsent, msgCredentialReady, msgCredentialUndecryptable,

		msgLicenceKeyAbsent, msgLicenceKeyStored, msgLicenceKeyUndecryptable,

		msgVerifyNotAttempted, msgVerifyVerified, msgVerifyCredentialsBad,
		msgVerifyEndpointNotFound, msgVerifyUnreachable, msgVerifyUnexpectedAnswer,
		msgVerifyNoURL,

		msgTitleSetup, msgTitleSignIn, msgTitleSettings, msgTitleRecover,

		msgThemeSystem, msgThemeLight, msgThemeDark,
	}
}
