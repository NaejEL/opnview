package settings

import (
	"context"
	"errors"
	"net/url"

	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/store/accounts"
)

// The states the settings answer reports, and the writes its saves share.
//
// A SECRET IS NEVER ANSWERED AND AN EMPTY SUBMISSION NEVER CLEARS ONE. What is reported
// instead is a STATE, which is the only thing about a secret this surface may say.
//
// WHAT MAKES A TYPO RECOVERABLE WITHOUT A RESTART. Storing a credential is only half of it.
// The other half is the last step of the firewall save (save.go): the stored value is read
// back through exactly the path start-up uses, and written into the live holder the OPNsense
// client reads on every call, so the next collection pass uses what was just typed.

// credentialStateKey names one config.CredentialState.
//
// THE THREE ARE THREE DIFFERENT SENTENCES, and the middle one is the reason this
// function exists rather than a boolean: a key file that has been lost or replaced
// is not "no firewall is configured". The firewall IS configured; what is broken is
// opnview's ability to read the secret, and re-entering it is what fixes it.
// Reporting it as absence would send the reader to look for a setting that is
// already there.
func credentialStateKey(state config.CredentialState) messageKey {
	switch state {
	case config.CredentialStateReady:
		return msgCredentialReady
	case config.CredentialStateUndecryptable:
		return msgCredentialUndecryptable
	default:
		return msgCredentialAbsent
	}
}

// unsealer hands config the box, or nil when there is none.
//
// The nil is the load-bearing case: it is the lost-or-replaced key file, and it
// makes LoadFirewallCredentials report the undecryptable state rather than
// pretending nothing is configured.
func (s *surface) unsealer() config.Unsealer {
	if s.box == nil {
		return nil
	}
	return s.box
}

// writeSetting writes a setting row, or removes it when the value is empty.
//
// AN EMPTY VALUE IS AN ABSENT ROW, not a row holding the empty string. Two ways of
// saying "not configured" is one more than the interface can report.
func (s *surface) writeSetting(ctx context.Context, key, value string, now int64) error {
	if value == "" {
		return s.store.DeleteSetting(ctx, key)
	}
	return s.store.SetSetting(ctx, key, value, now)
}

// storeSecret seals one credential and writes it.
//
// AN EMPTY SUBMISSION LEAVES WHAT IS STORED ALONE. The field renders empty whatever
// is stored, so an empty submission is "I did not retype it" and not "delete it" —
// and if empty meant delete, saving the theme would destroy both credentials.
// Clearing one is a separate act, which this cycle does not offer and which the next
// cycle to need it can add as its own control.
//
// WITH NO KEY FILE, STORING IS REFUSED rather than done in the clear. A credential
// written unsealed because the key was missing is a credential silently unprotected.
func (s *surface) storeSecret(ctx context.Context, name, plaintext string, now int64) error {
	if plaintext == "" {
		return nil
	}
	if s.box == nil {
		return errors.New("web: there is no key file, so a credential cannot be sealed")
	}
	sealed, err := s.box.Seal([]byte(plaintext))
	if err != nil {
		return err
	}
	return s.store.SetCredential(ctx, accounts.EncryptedCredential{
		Name:       name,
		Algorithm:  sealed.Algorithm,
		KeyID:      sealed.KeyID,
		Nonce:      sealed.Nonce,
		Ciphertext: sealed.Bytes,
		UpdatedAt:  now,
	})
}

// validateFirewallURL refuses something that is not an address of a firewall API.
//
// IT CHECKS A SHAPE AND CARRIES NO VALUE. There is no default here, no placeholder,
// no example and no host name: the scheme must be http or https and there must be a
// host, and what that host is belongs entirely to the person installing the product.
// A query or a fragment is refused because the client appends its own, and a path is
// refused because the client appends the endpoint's.
func validateFirewallURL(value string) error {
	parsed, err := url.Parse(value)
	if err != nil {
		return err
	}
	switch {
	case parsed.Scheme != "http" && parsed.Scheme != "https":
		return errors.New("web: the firewall URL has no http or https scheme")
	case parsed.Host == "":
		return errors.New("web: the firewall URL has no host")
	case parsed.Path != "":
		return errors.New("web: the firewall URL carries a path")
	case parsed.RawQuery != "", parsed.Fragment != "":
		return errors.New("web: the firewall URL carries a query or a fragment")
	case parsed.User != nil:
		return errors.New("web: the firewall URL carries credentials in it")
	}
	return nil
}
