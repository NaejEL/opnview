package account

// The catalogue keys this surface's code chooses. A handler chooses a key, never a string; a
// JSON answer carries its closed code (web.ConditionOf), which the front translates through the
// catalogue (locale/en.json beside this file, or the core's when another surface uses the key).

const (
	msgKeyFileRefused    messageKey = "error.key_file_refused"
	msgLoginRequired     messageKey = "error.login_required"
	msgPasswordMismatch  messageKey = "error.password_mismatch"
	msgPasswordRequired  messageKey = "error.password_required"
	msgPasswordTooShort  messageKey = "error.password_too_short"
	msgSetupTokenRefused messageKey = "error.setup_token_refused"
	msgSignInRefused     messageKey = "error.sign_in_refused"
)

// keys is every key above, so a test can assert that each exists in the catalogue.
func keys() []messageKey {
	return []messageKey{
		msgKeyFileRefused,
		msgLoginRequired,
		msgPasswordMismatch,
		msgPasswordRequired,
		msgPasswordTooShort,
		msgSetupTokenRefused,
		msgSignInRefused,
	}
}
