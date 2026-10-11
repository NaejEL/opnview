package settings

import (
	"context"

	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/store"
	"github.com/NaejEL/opnview/internal/web"
)

// What a save of a card checks and stores, written once for the page and its JSON equivalent
// (api.go), so the two refuse the same inputs and make the same change.

// firewallURLRefusal refuses a typed firewall URL that is not the address of a firewall API. An
// empty URL is accepted: it removes the setting.
func firewallURLRefusal(firewallURL string) messageKey {
	if firewallURL != "" {
		if err := validateFirewallURL(firewallURL); err != nil {
			return msgFirewallURLInvalid
		}
	}
	return ""
}

// firewallCredentialRefusal checks the rest of the firewall card and returns the fingerprint
// normalised.
func firewallCredentialRefusal(apiKey, apiSecret, fingerprintTyped string) (string, messageKey) {
	// Normalised rather than refused for its punctuation: OPNsense displays a fingerprint
	// colon-separated and upper-case, and that is what a reader pastes.
	fingerprint, err := opnsense.ParseFingerprint(fingerprintTyped)
	if err != nil {
		return "", msgFingerprintInvalid
	}
	// A secret with no key beside it cannot authenticate anything, and storing one would leave
	// the surface reporting a readable credential that cannot be used.
	if apiSecret != "" && apiKey == "" {
		return "", msgAPIKeyRequired
	}
	return fingerprint, ""
}

// saveFirewall stores the firewall card, makes it live and verifies it, returning the
// verification's outcome.
func (s *surface) saveFirewall(ctx context.Context, firewallURL, apiKey, apiSecret, fingerprint string,
	now int64) (messageKey, error) {
	for _, write := range []struct{ key, value string }{
		{config.KeyOPNsenseBaseURL, firewallURL},
		{config.KeyOPNsenseAPIKey, apiKey},
		{config.KeyOPNsenseCertificateFingerprint, fingerprint},
	} {
		if err := s.writeSetting(ctx, write.key, write.value, now); err != nil {
			return "", err
		}
	}
	if err := s.storeSecret(ctx, store.CredentialOPNsenseAPISecret, apiSecret, now); err != nil {
		return "", err
	}
	// THE LIVE HOLDER, through the same path start-up uses. This is what makes the change reach
	// the running collectors.
	credentials, _, err := config.LoadFirewallCredentials(ctx, s.store, s.unsealer())
	if err != nil {
		return "", err
	}
	s.credentials.Set(credentials)
	// The pooled connections were shaken hands over the PREVIOUS fingerprint, and the
	// certificate is checked once per handshake. Without this, correcting a pin would leave a
	// connection open that the new pin does not accept, and the verification below would report
	// a firewall that answers on a trust decision that is no longer in force.
	s.client.CloseIdleConnections()
	// One call to the firewall, and the only one this package makes. It is the first of the
	// permitted outbound calls and not another; see verify.go.
	return s.verify(ctx), nil
}

// saveTheme stores a theme, or refuses one this build does not offer.
func (s *surface) saveTheme(ctx context.Context, value string, now int64) (messageKey, error) {
	parsedTheme, err := web.ParseTheme(value)
	if err != nil {
		return msgThemeUnknown, nil
	}
	return "", s.store.SetSetting(ctx, config.KeyTheme, string(parsedTheme), now)
}
