package web

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/secret"
	"github.com/NaejEL/opnview/internal/store"
)

// The settings surface: where credentials enter the product.
//
// WHAT MAKES A TYPO RECOVERABLE WITHOUT A RESTART. Storing a credential is only
// half of it. The other half is the last step of the submit handler: the stored
// value is read back through exactly the path start-up uses, and written into the
// live holder the OPNsense client reads on every call. So the next collection pass —
// in the same process, with no restart — uses what was just typed. Cycle 4A built
// the client once at start-up from an empty value, and that is the defect this
// closes.
//
// A SECRET FIELD IS NEVER PRE-FILLED AND AN EMPTY SUBMISSION NEVER CLEARS ONE.
// Otherwise saving the theme would destroy the credentials. What is reported instead
// is a STATE beside the field, which is the only thing about a secret this surface
// may say.

// handleSettingsForm draws the settings surface.
func (s *Server) handleSettingsForm(writer http.ResponseWriter, request *http.Request) {
	built, err := s.newView(writer, request, sessionFrom(request), titleKeyFor(pageSettings))
	if err != nil {
		s.failInternal(writer, request, err)
		return
	}
	// NOT ATTEMPTED, not "verified": drawing the form makes no call to the firewall.
	// Reporting a stale success here would be reporting a call that did not happen.
	if err := s.fillSettings(request, &built, msgVerifyNotAttempted); err != nil {
		s.failInternal(writer, request, err)
		return
	}
	if err := s.renderer.render(writer, http.StatusOK, pageSettings, built); err != nil {
		s.failInternal(writer, request, err)
	}
}

// handleSettingsSubmit stores what was typed, makes it live, and verifies it.
func (s *Server) handleSettingsSubmit(writer http.ResponseWriter, request *http.Request) {
	ctx := request.Context()
	now := s.now().UTC().Unix()

	firewallURL := strings.TrimRight(strings.TrimSpace(request.PostFormValue("firewall_url")), "/")
	apiKey := strings.TrimSpace(request.PostFormValue("api_key"))
	apiSecret := request.PostFormValue("api_secret")
	fingerprintTyped := request.PostFormValue("certificate_fingerprint")
	licenceKey := strings.TrimSpace(request.PostFormValue("maxmind_licence_key"))
	theme := request.PostFormValue("theme")

	if firewallURL != "" {
		if err := validateFirewallURL(firewallURL); err != nil {
			s.renderSettingsRefusal(writer, request, msgFirewallURLInvalid)
			return
		}
	}
	// Normalised rather than refused for its punctuation: OPNsense displays a
	// fingerprint colon-separated and upper-case, and that is what a reader pastes.
	fingerprint, err := opnsense.ParseFingerprint(fingerprintTyped)
	if err != nil {
		s.renderSettingsRefusal(writer, request, msgFingerprintInvalid)
		return
	}
	parsedTheme, err := ParseTheme(theme)
	if err != nil {
		s.renderSettingsRefusal(writer, request, msgThemeUnknown)
		return
	}
	// A secret with no key beside it cannot authenticate anything, and storing one
	// would leave the surface reporting a readable credential that cannot be used.
	if apiSecret != "" && apiKey == "" {
		s.renderSettingsRefusal(writer, request, msgAPIKeyRequired)
		return
	}

	if err := s.writeSetting(ctx, config.KeyOPNsenseBaseURL, firewallURL, now); err != nil {
		s.failInternal(writer, request, err)
		return
	}
	if err := s.writeSetting(ctx, config.KeyOPNsenseAPIKey, apiKey, now); err != nil {
		s.failInternal(writer, request, err)
		return
	}
	if err := s.writeSetting(ctx, config.KeyOPNsenseCertificateFingerprint,
		fingerprint, now); err != nil {
		s.failInternal(writer, request, err)
		return
	}
	if err := s.store.SetSetting(ctx, config.KeyTheme, string(parsedTheme), now); err != nil {
		s.failInternal(writer, request, err)
		return
	}
	if err := s.storeSecret(ctx, store.CredentialOPNsenseAPISecret, apiSecret, now); err != nil {
		s.failInternal(writer, request, err)
		return
	}
	if err := s.storeSecret(ctx, store.CredentialMaxMindLicenceKey, licenceKey, now); err != nil {
		s.failInternal(writer, request, err)
		return
	}

	// THE LIVE HOLDER, through the same path start-up uses. This is what makes the
	// change reach the running collectors.
	credentials, _, err := config.LoadFirewallCredentials(ctx, s.store, s.unsealer())
	if err != nil {
		s.failInternal(writer, request, err)
		return
	}
	s.credentials.Set(credentials)
	// The pooled connections were shaken hands over the PREVIOUS fingerprint, and
	// the certificate is checked once per handshake. Without this, correcting a pin
	// would leave a connection open that the new pin does not accept, and the
	// verification below would report a firewall that answers on a trust decision
	// that is no longer in force.
	s.client.CloseIdleConnections()

	// One call to the firewall, and the only one this package makes. It is the first
	// of the two permitted outbound calls and not a third; see verify.go.
	verification := s.verify(ctx)

	built, err := s.newView(writer, request, sessionFrom(request), titleKeyFor(pageSettings))
	if err != nil {
		s.failInternal(writer, request, err)
		return
	}
	built.Notice = msgSettingsSaved
	if err := s.fillSettings(request, &built, verification); err != nil {
		s.failInternal(writer, request, err)
		return
	}
	if err := s.renderer.render(writer, http.StatusOK, pageSettings, built); err != nil {
		s.failInternal(writer, request, err)
	}
}

// renderSettingsRefusal redraws the settings surface with a refusal and nothing
// stored.
func (s *Server) renderSettingsRefusal(writer http.ResponseWriter, request *http.Request,
	key messageKey) {
	s.renderRefusal(writer, request, http.StatusBadRequest, pageSettings, key)
}

// fillSettings puts the current configuration and the three states into a view.
//
// IT READS NO SECRET INTO THE VIEW. What it reads is whether each one is there and
// whether it can be opened, which are states, and it reports them as catalogue keys.
func (s *Server) fillSettings(request *http.Request, built *view,
	verification messageKey) error {
	ctx := request.Context()

	firewallURL, _, err := s.store.Setting(ctx, config.KeyOPNsenseBaseURL)
	if err != nil {
		return err
	}
	apiKey, _, err := s.store.Setting(ctx, config.KeyOPNsenseAPIKey)
	if err != nil {
		return err
	}
	fingerprint, _, err := s.store.Setting(ctx, config.KeyOPNsenseCertificateFingerprint)
	if err != nil {
		return err
	}
	built.FirewallURL = firewallURL
	built.APIKey = apiKey
	built.CertificateFingerprint = fingerprint

	_, state, err := config.LoadFirewallCredentials(ctx, s.store, s.unsealer())
	if err != nil {
		return err
	}
	built.CredentialState = credentialStateKey(state)
	built.VerificationState = verification

	licenceState, err := s.licenceKeyState(ctx)
	if err != nil {
		return err
	}
	built.LicenceKeyState = licenceState

	current := s.readTheme(ctx)
	for _, candidate := range themes() {
		built.Themes = append(built.Themes, themeOption{
			Value:    candidate.Value,
			Label:    candidate.Label,
			Selected: candidate.Value == current,
		})
	}
	return nil
}

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

// licenceKeyState says whether the MaxMind licence key is there and openable.
//
// IT MAKES NO CALL TO MAXMIND. Nothing in this cycle does: the key is stored and
// not verified, because verifying it means downloading and the download is cycle 4C.
func (s *Server) licenceKeyState(ctx context.Context) (messageKey, error) {
	sealed, stored, err := s.store.Credential(ctx, store.CredentialMaxMindLicenceKey)
	if err != nil {
		return "", err
	}
	if !stored {
		return msgLicenceKeyAbsent, nil
	}
	if s.box == nil {
		return msgLicenceKeyUndecryptable, nil
	}
	if _, err := s.box.Unseal(secret.Ciphertext{
		Algorithm: sealed.Algorithm,
		KeyID:     sealed.KeyID,
		Nonce:     sealed.Nonce,
		Bytes:     sealed.Ciphertext,
	}); err != nil {
		return msgLicenceKeyUndecryptable, nil
	}
	return msgLicenceKeyStored, nil
}

// unsealer hands config the box, or nil when there is none.
//
// The nil is the load-bearing case: it is the lost-or-replaced key file, and it
// makes LoadFirewallCredentials report the undecryptable state rather than
// pretending nothing is configured.
func (s *Server) unsealer() config.Unsealer {
	if s.box == nil {
		return nil
	}
	return s.box
}

// writeSetting writes a setting row, or removes it when the value is empty.
//
// AN EMPTY VALUE IS AN ABSENT ROW, not a row holding the empty string. Two ways of
// saying "not configured" is one more than the interface can report.
func (s *Server) writeSetting(ctx context.Context, key, value string, now int64) error {
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
func (s *Server) storeSecret(ctx context.Context, name, plaintext string, now int64) error {
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
	return s.store.SetCredential(ctx, store.EncryptedCredential{
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
