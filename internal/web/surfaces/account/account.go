package account

import "unicode/utf8"

// The credential rule the setup and the reset share.

// minimumPasswordRunes is the shortest password this product accepts.
//
// TWELVE, AND THE REASON IS WRITTEN DOWN RATHER THAN ASSUMED. There is no lockout
// and no delay after repeated failures — the requirement asks for neither, and what
// it does ask for is indistinguishable failures — so the only thing standing
// between a local network and an offline guess is the password itself and the
// Argon2id cost beside it. It is counted in RUNES and not bytes, so a passphrase in
// any script is measured by what somebody typed.
const minimumPasswordRunes = 12

// validateNewCredentials checks a login and a new password, returning the
// catalogue key of the first thing wrong with them.
//
// It is one function because setup and the reset ask for the same three things, and
// two copies of a password rule drift.
func validateNewCredentials(login, password, again string) (messageKey, bool) {
	switch {
	case login == "":
		return msgLoginRequired, false
	case password == "":
		return msgPasswordRequired, false
	case utf8.RuneCountInString(password) < minimumPasswordRunes:
		return msgPasswordTooShort, false
	case password != again:
		return msgPasswordMismatch, false
	}
	return "", true
}
