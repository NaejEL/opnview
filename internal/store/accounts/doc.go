// Package accounts is the part of the store holding the operator's accounts, their sessions, the
// encrypted credentials and the setting rows (account.go). internal/store composes it into Store.
//
// Nothing here returns a password, a salt or a session token to a caller that did not already
// have it.
package accounts
