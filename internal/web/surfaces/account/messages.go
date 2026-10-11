package account

// The catalogue keys this surface's code chooses. A handler chooses a key, never a string;
// the words are in locale/en.json beside this file, or in the core's locale when another
// surface uses the key too.

const (
	msgKeyFileRefused    messageKey = "error.key_file_refused"
	msgLoginRequired     messageKey = "error.login_required"
	msgPasswordMismatch  messageKey = "error.password_mismatch"
	msgPasswordRequired  messageKey = "error.password_required"
	msgPasswordReset     messageKey = "notice.password_reset"
	msgPasswordTooShort  messageKey = "error.password_too_short"
	msgSetupDone         messageKey = "notice.setup_done"
	msgSetupTokenRefused messageKey = "error.setup_token_refused"
	msgSignInRefused     messageKey = "error.sign_in_refused"
	msgSignedOut         messageKey = "notice.signed_out"
	msgTitleRecover      messageKey = "recover.title"
	msgTitleSetup        messageKey = "setup.title"
	msgTitleSignIn       messageKey = "sign_in.title"
)

// keys is every key above, so a test can assert that each exists in the catalogue and that
// no catalogue entry is unreachable.
func keys() []messageKey {
	return []messageKey{
		msgKeyFileRefused,
		msgLoginRequired,
		msgPasswordMismatch,
		msgPasswordRequired,
		msgPasswordReset,
		msgPasswordTooShort,
		msgSetupDone,
		msgSetupTokenRefused,
		msgSignInRefused,
		msgSignedOut,
		msgTitleRecover,
		msgTitleSetup,
		msgTitleSignIn,
	}
}
