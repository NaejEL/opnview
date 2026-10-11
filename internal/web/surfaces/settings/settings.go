package settings

import (
	"context"
	"errors"
	"fmt"
	"github.com/NaejEL/opnview/internal/web"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/NaejEL/opnview/internal/collect/ipgeolocation"
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

// ONE FORM FOR THE WHOLE PAGE, ONE SAVE PER CARD. Every card's fields belong to one
// form, and each card's Save button sends that card's name in fieldSettingsCard. A
// submission therefore carries what is typed in EVERY card, and the handler stores
// the named card's fields and nothing else: the other cards are drawn again with what
// was posted in them, marked as edited and not saved, so saving one card never
// silently discards what was typed in another. A secret typed in another card is the
// one thing that cannot come back -- a secret field is never filled -- so the field
// says that it was not kept.
//
// A SUBMISSION NAMING NO CARD STORES NOTHING. The form's default button, which the
// Enter key presses, names none; such a submission is refused at 400 with every
// typed value kept, rather than storing every card at once.
//
// A SAVE REDIRECTS when nothing typed elsewhere would be lost by it: 303 to the
// surface, naming the card saved and, for the firewall, the outcome of the
// verification, at the card's anchor -- so a reload does not post again. When another
// card holds an unsaved edit, the save is answered in place instead, because a
// redirect would discard that edit. A refusal is always answered in place, at 400.

// handleSettingsForm draws the settings surface. A save lands here, with the card it
// was made in named by the query, and that card says it was saved.
func (s *surface) handleSettingsForm(writer http.ResponseWriter, request *http.Request) {
	built, err := s.newView(writer, request, web.SessionFrom(request), msgTitleSettings)
	if err != nil {
		s.FailInternal(writer, request, err)
		return
	}
	// NOT ATTEMPTED, not "verified": drawing the form makes no call to the firewall.
	// Reporting a stale success here would be reporting a call that did not happen; the
	// one exception is the outcome of the verification the save just made, carried by
	// the redirect and accepted only as one of the verification states.
	query := request.URL.Query()
	saved := settingsCardNamed(query.Get(querySaved))
	verification := msgVerifyNotAttempted
	if saved == settingsCardFirewall {
		if carried, known := verificationNamed(query.Get(queryVerified)); known {
			verification = carried
		}
	}
	if err := s.fillSettings(request, &built, verification); err != nil {
		s.FailInternal(writer, request, err)
		return
	}
	if saved != "" {
		built.Notice = msgSettingsSaved
		built.setCard(saved)
	}
	if err := s.Render(writer, http.StatusOK, pageSettings, &built); err != nil {
		s.FailInternal(writer, request, err)
	}
}

// The settings surface's cards. A submission names its card in fieldSettingsCard,
// which is the name of each card's Save button, and only that card's fields are read
// and stored.
const (
	fieldSettingsCard = "settings_card"

	settingsCardFirewall     = "firewall"
	settingsCardGeolocation  = "geolocation"
	settingsCardPublicSuffix = "public_suffix"
	settingsCardAppearance   = "appearance"
)

// queryVerified carries, in the redirect after a firewall save, the outcome of the
// verification that save made.
const queryVerified = "verified"

// settingsCards are the cards a submission may name.
func settingsCards() []string {
	return []string{settingsCardFirewall, settingsCardGeolocation, settingsCardPublicSuffix,
		settingsCardAppearance}
}

// settingsCardNamed is the card a value names, or the empty string for none.
func settingsCardNamed(named string) string {
	for _, card := range settingsCards() {
		if card == named {
			return card
		}
	}
	return ""
}

// submittedSettingsCard is the card a submission names, or the empty string for none
// or for a name that is not a card. The fetch control sits in the firewall card and
// is about it.
func submittedSettingsCard(request *http.Request) string {
	if request.PostFormValue(fieldFetchCertificate) != "" {
		return settingsCardFirewall
	}
	return settingsCardNamed(request.PostFormValue(fieldSettingsCard))
}

// fieldFetchCertificate is the fetch control's name.
const fieldFetchCertificate = "fetch_certificate"

// verificationNamed accepts a carried verification outcome only when it is one of the
// verification states.
func verificationNamed(value string) (messageKey, bool) {
	for _, key := range []messageKey{msgVerifyNotAttempted, msgVerifyVerified,
		msgVerifyCredentialsBad, msgVerifyEndpointNotFound, msgVerifyUnreachable,
		msgVerifyCertificateRefused, msgVerifyUnexpectedAnswer, msgVerifyNoURL} {
		if string(key) == value {
			return key, true
		}
	}
	return "", false
}

// settingsSavedLocation is where a save sends the browser: the surface, naming the card
// saved and, for the firewall, the verification's outcome, at the card's anchor.
func settingsSavedLocation(card string, verification messageKey) string {
	query := url.Values{querySaved: {card}}
	if card == settingsCardFirewall {
		query.Set(queryVerified, string(verification))
	}
	return web.PathSettings + "?" + query.Encode() + "#settings_" + card
}

// settingsOutcome is what one submission has to say, and to which card.
type settingsOutcome struct {
	// card is the card the submission named; saved says its fields were stored, and
	// are therefore drawn as stored rather than as typed.
	card  string
	saved bool
	// notice and refusal are said in that card, or at the head of the page when the
	// submission named none.
	notice  messageKey
	refusal messageKey
	// verification is the firewall verification's outcome.
	verification messageKey
	// suffixRefusal refuses the Public Suffix List's interval against its field.
	suffixRefusal messageKey
	// presented is the fingerprint the firewall presented, put in its field.
	presented string
}

// handleSettingsSubmit stores what was typed on the card the submission names, makes
// it live, and verifies the firewall's credentials when they were among it.
func (s *surface) handleSettingsSubmit(writer http.ResponseWriter, request *http.Request) {
	ctx := request.Context()
	now := s.now().UTC().Unix()

	card := submittedSettingsCard(request)
	if card == "" {
		// The default button names no card, and a name that is not a card came from
		// somewhere other than the page. Nothing is stored either way.
		refusal := msgCardNotNamed
		if request.PostFormValue(fieldSettingsCard) != "" {
			refusal = msgSettingsCardUnknown
		}
		s.renderSettingsOutcome(writer, request, http.StatusBadRequest, settingsOutcome{refusal: refusal})
		return
	}
	refuse := func(key messageKey) {
		s.renderSettingsOutcome(writer, request, http.StatusBadRequest,
			settingsOutcome{card: card, refusal: key})
	}

	firewallURL := strings.TrimRight(strings.TrimSpace(request.PostFormValue("firewall_url")), "/")
	apiKey := strings.TrimSpace(request.PostFormValue("api_key"))
	apiSecret := request.PostFormValue("api_secret")
	fingerprintTyped := request.PostFormValue("certificate_fingerprint")
	licenceKey := strings.TrimSpace(request.PostFormValue("maxmind_licence_key"))
	accountID := strings.TrimSpace(request.PostFormValue("maxmind_account_id"))

	verification := msgVerifyNotAttempted
	switch card {
	case settingsCardFirewall:
		if firewallURL != "" {
			if err := validateFirewallURL(firewallURL); err != nil {
				refuse(msgFirewallURLInvalid)
				return
			}
		}
		// THE FETCH BUTTON STORES NOTHING. It connects, shows the fingerprint of the
		// certificate the firewall presents, and redraws the form with it in the field;
		// accepting it is the save that follows, which is a second deliberate act. It
		// is handled before anything is parsed or stored, so pressing it with a
		// half-typed form loses nothing and writes nothing. The URL it connects to is
		// the one typed, already checked above.
		if request.PostFormValue(fieldFetchCertificate) != "" {
			s.showPresentedCertificate(writer, request, firewallURL)
			return
		}
		// Normalised rather than refused for its punctuation: OPNsense displays a
		// fingerprint colon-separated and upper-case, and that is what a reader pastes.
		fingerprint, err := opnsense.ParseFingerprint(fingerprintTyped)
		if err != nil {
			refuse(msgFingerprintInvalid)
			return
		}
		// A secret with no key beside it cannot authenticate anything, and storing one
		// would leave the surface reporting a readable credential that cannot be used.
		if apiSecret != "" && apiKey == "" {
			refuse(msgAPIKeyRequired)
			return
		}
		for _, write := range []struct{ key, value string }{
			{config.KeyOPNsenseBaseURL, firewallURL},
			{config.KeyOPNsenseAPIKey, apiKey},
			{config.KeyOPNsenseCertificateFingerprint, fingerprint},
		} {
			if err := s.writeSetting(ctx, write.key, write.value, now); err != nil {
				s.FailInternal(writer, request, err)
				return
			}
		}
		if err := s.storeSecret(ctx, store.CredentialOPNsenseAPISecret, apiSecret, now); err != nil {
			s.FailInternal(writer, request, err)
			return
		}
		// THE LIVE HOLDER, through the same path start-up uses. This is what makes
		// the change reach the running collectors.
		credentials, _, err := config.LoadFirewallCredentials(ctx, s.store, s.unsealer())
		if err != nil {
			s.FailInternal(writer, request, err)
			return
		}
		s.credentials.Set(credentials)
		// The pooled connections were shaken hands over the PREVIOUS fingerprint, and
		// the certificate is checked once per handshake. Without this, correcting a pin
		// would leave a connection open that the new pin does not accept, and the
		// verification below would report a firewall that answers on a trust decision
		// that is no longer in force.
		s.client.CloseIdleConnections()
		// One call to the firewall, and the only one this package makes. It is the
		// first of the permitted outbound calls and not another; see verify.go.
		verification = s.verify(ctx)

	case settingsCardGeolocation:
		// MaxMind account IDs are numbers; anything else would only be refused by
		// MaxMind, after spending a request.
		if accountID != "" {
			if number, err := strconv.ParseInt(accountID, 10, 64); err != nil || number <= 0 {
				refuse(msgMaxMindAccountIDBad)
				return
			}
		}
		previousAccountID, _, err := s.store.Setting(ctx, config.KeyMaxMindAccountID)
		if err != nil {
			s.FailInternal(writer, request, err)
			return
		}
		if err := s.storeSecret(ctx, store.CredentialMaxMindLicenceKey, licenceKey, now); err != nil {
			s.FailInternal(writer, request, err)
			return
		}
		if err := s.writeSetting(ctx, config.KeyMaxMindAccountID, accountID, now); err != nil {
			s.FailInternal(writer, request, err)
			return
		}
		// A new licence key or a different account ID is fetched with now, not at the
		// next daily check. The refresh runs in its own loop: nothing here waits for
		// it, and this package makes no call to MaxMind.
		if s.geoip != nil && (licenceKey != "" || accountID != previousAccountID) {
			s.geoip.Wake()
		}

	case settingsCardPublicSuffix:
		// Read and refused before anything is stored, against its own field.
		seconds, changed, refusal := s.typedPublicSuffixInterval(request)
		if refusal != "" {
			s.renderSettingsOutcome(writer, request, http.StatusBadRequest,
				settingsOutcome{card: card, suffixRefusal: refusal})
			return
		}
		// The refresh interval reaches the running loop the way the collection
		// surface's intervals do: the configuration is loaded again and handed to the
		// live holder, which the loop asks before every wait.
		if changed {
			if err := s.store.SetSetting(ctx, config.KeyPublicSuffixRefreshInterval,
				seconds, now); err != nil {
				s.FailInternal(writer, request, err)
				return
			}
			if err := s.ApplyConfiguration(ctx); err != nil {
				s.FailInternal(writer, request, err)
				return
			}
		}

	case settingsCardAppearance:
		parsedTheme, err := web.ParseTheme(request.PostFormValue("theme"))
		if err != nil {
			refuse(msgThemeUnknown)
			return
		}
		if err := s.store.SetSetting(ctx, config.KeyTheme, string(parsedTheme), now); err != nil {
			s.FailInternal(writer, request, err)
			return
		}
	}

	outcome := settingsOutcome{card: card, saved: true, notice: msgSettingsSaved,
		verification: verification}
	built, err := s.settingsView(writer, request, outcome)
	if err != nil {
		s.FailInternal(writer, request, err)
		return
	}
	// Nothing typed elsewhere would be lost: the browser is sent back, so a reload
	// does not post the save again.
	if !built.hasUnsavedEdits() {
		http.Redirect(writer, request, settingsSavedLocation(card, verification), http.StatusSeeOther)
		return
	}
	if err := s.Render(writer, http.StatusOK, pageSettings, &built); err != nil {
		s.FailInternal(writer, request, err)
	}
}

// showPresentedCertificate connects to the firewall at the typed URL and redraws the
// settings surface with the fingerprint of the certificate it presented in the field,
// and everything else as it was typed. It stores nothing and verifies nothing: the
// connection reads one certificate and closes, and nothing it read is trusted until
// the operator saves it. See opnsense.FetchCertificateFingerprint.
func (s *surface) showPresentedCertificate(writer http.ResponseWriter, request *http.Request,
	firewallURL string) {
	if firewallURL == "" {
		s.renderSettingsOutcome(writer, request, http.StatusBadRequest,
			settingsOutcome{card: settingsCardFirewall, refusal: msgFirewallURLRequired})
		return
	}
	// The one call this button makes, a TLS handshake and nothing after it.
	presented, err := opnsense.FetchCertificateFingerprint(request.Context(), firewallURL)
	if err != nil {
		// The detail names the address and why; it goes to the console, not the page.
		fmt.Fprintf(os.Stderr, "opnview: web: fetching the firewall certificate: %v\n", err)
		s.renderSettingsOutcome(writer, request, http.StatusBadRequest,
			settingsOutcome{card: settingsCardFirewall, refusal: msgCertificateNotFetched})
		return
	}
	s.renderSettingsOutcome(writer, request, http.StatusOK, settingsOutcome{
		card: settingsCardFirewall, notice: msgCertificatePresented, presented: presented})
}

// renderSettingsOutcome draws the settings surface in answer to a submission, at a
// status, with every value typed in it kept.
func (s *surface) renderSettingsOutcome(writer http.ResponseWriter, request *http.Request,
	status int, outcome settingsOutcome) {
	built, err := s.settingsView(writer, request, outcome)
	if err != nil {
		s.FailInternal(writer, request, err)
		return
	}
	if err := s.Render(writer, status, pageSettings, &built); err != nil {
		s.FailInternal(writer, request, err)
	}
}

// settingsView builds the settings surface in answer to a submission: what is stored,
// with what was typed put back in every card except one whose fields were just saved,
// and the outcome said in the card it belongs to.
func (s *surface) settingsView(writer http.ResponseWriter, request *http.Request,
	outcome settingsOutcome) (view, error) {
	built, err := s.newView(writer, request, web.SessionFrom(request), msgTitleSettings)
	if err != nil {
		return view{}, err
	}
	verification := outcome.verification
	if verification == "" {
		verification = msgVerifyNotAttempted
	}
	if err := s.fillSettings(request, &built, verification); err != nil {
		return view{}, err
	}
	except := ""
	if outcome.saved {
		except = outcome.card
	}
	if err := s.applySettingsDraft(request, &built, except); err != nil {
		return view{}, err
	}
	built.setCard(outcome.card)
	built.Notice = outcome.notice
	built.Error = outcome.refusal
	if outcome.suffixRefusal != "" {
		built.PublicSuffix.IntervalField.RefusalKey = outcome.suffixRefusal
	}
	if outcome.presented != "" {
		built.CertificateFingerprint = outcome.presented
	}
	return built, nil
}

// applySettingsDraft puts what a submission carried back into every card but one, and
// marks each card whose fields differ from what is stored as edited and not saved. A
// field the submission did not carry keeps what is stored.
//
// A SECRET IS NEVER PUT BACK. A secret typed and not stored is reported by a state
// beside its field instead, so a reader knows to type it again.
func (s *surface) applySettingsDraft(request *http.Request, built *view, except string) error {
	posted := func(name string) (string, bool) {
		values, sent := request.PostForm[name]
		if !sent || len(values) == 0 {
			return "", false
		}
		return values[0], true
	}
	edited := map[string]bool{}

	if except != settingsCardFirewall {
		if typed, sent := posted("firewall_url"); sent {
			if strings.TrimRight(strings.TrimSpace(typed), "/") != built.FirewallURL {
				edited[settingsCardFirewall] = true
			}
			built.FirewallURL = typed
		}
		if typed, sent := posted("api_key"); sent {
			if strings.TrimSpace(typed) != built.APIKey {
				edited[settingsCardFirewall] = true
			}
			built.APIKey = typed
		}
		if typed, sent := posted("certificate_fingerprint"); sent {
			normalised, err := opnsense.ParseFingerprint(typed)
			if err != nil {
				normalised = typed
			}
			if normalised != built.CertificateFingerprint {
				edited[settingsCardFirewall] = true
			}
			built.CertificateFingerprint = typed
		}
		if typed, _ := posted("api_secret"); typed != "" {
			edited[settingsCardFirewall] = true
			built.APISecretNotKept = true
		}
	}
	if except != settingsCardGeolocation {
		if typed, sent := posted("maxmind_account_id"); sent {
			if strings.TrimSpace(typed) != built.MaxMindAccountID {
				edited[settingsCardGeolocation] = true
			}
			built.MaxMindAccountID = typed
		}
		if typed, _ := posted("maxmind_licence_key"); strings.TrimSpace(typed) != "" {
			edited[settingsCardGeolocation] = true
			built.LicenceKeyNotKept = true
		}
	}
	if except != settingsCardPublicSuffix {
		if figure, sent := posted(fieldPublicSuffixInterval); sent {
			unit, _ := posted(web.UnitFieldName(fieldPublicSuffixInterval))
			settings, err := s.Configuration(request.Context())
			if err != nil {
				return err
			}
			inForce := int64(settings.PublicSuffixRefreshInterval / time.Second)
			field := web.DurationField(fieldPublicSuffixInterval, built.PublicSuffix.IntervalField.ID,
				msgFieldPublicSuffixInterval, inForce, map[string]string{
					fieldPublicSuffixInterval: figure, web.UnitFieldName(fieldPublicSuffixInterval): unit})
			built.PublicSuffix.IntervalField = field
			if web.DurationDiffers(figure, unit, inForce) {
				edited[settingsCardPublicSuffix] = true
			}
		}
	}
	if except != settingsCardAppearance {
		if typed, sent := posted("theme"); sent {
			if chosen, err := web.ParseTheme(typed); err == nil {
				for index := range built.Themes {
					option := &built.Themes[index]
					if option.Selected && option.Value != chosen {
						edited[settingsCardAppearance] = true
					}
					option.Selected = option.Value == chosen
				}
			}
		}
	}
	built.SettingsEdited = edited
	return nil
}

// fillSettings puts the current configuration and the three states into a view.
//
// IT READS NO SECRET INTO THE VIEW. What it reads is whether each one is there and
// whether it can be opened, which are states, and it reports them as catalogue keys.
func (s *surface) fillSettings(request *http.Request, built *view,
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

	accountID, _, err := s.store.Setting(ctx, config.KeyMaxMindAccountID)
	if err != nil {
		return err
	}
	built.MaxMindAccountID = accountID
	if built.GeoIPState, err = s.geoIPState(ctx); err != nil {
		return err
	}
	if s.geoip != nil {
		if build, ready := s.geoip.Ready(); ready {
			built.GeoIPBuild = web.Instant(build)
		}
	}
	if err := s.fillPublicSuffix(ctx, built); err != nil {
		return err
	}

	current := s.ReadTheme(ctx)
	for _, candidate := range web.ThemeChoices() {
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
// IT MAKES NO CALL TO MAXMIND. Nothing in this package does: whether MaxMind accepts
// the key is what a download finds out, and geoIPState reports what it found.
func (s *surface) licenceKeyState(ctx context.Context) (messageKey, error) {
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

// geoIPState says where the MaxMind databases stand, from the availability row the
// refresh writes. It reads the row and contacts nothing.
//
// A probe it does not know reads as the failure state rather than as success.
// "not_yet_probed" is what the schema writes on a fresh database, before any
// refresh ran.
func (s *surface) geoIPState(ctx context.Context) (messageKey, error) {
	providerID, err := s.store.ProviderID(ctx, ipgeolocation.Kind, ipgeolocation.BuiltinKey)
	if err != nil {
		return "", err
	}
	_, probe, _, err := s.store.Availability(ctx, providerID)
	if err != nil {
		return "", err
	}
	switch probe {
	case ipgeolocation.ProbeCurrent:
		return msgGeoIPCurrent, nil
	case ipgeolocation.ProbeNoLicenceKey:
		return msgGeoIPNoLicenceKey, nil
	case ipgeolocation.ProbeLicenceKeyUndecryptable:
		return msgGeoIPKeyUnreadable, nil
	case ipgeolocation.ProbeNoAccountID:
		return msgGeoIPNoAccountID, nil
	case ipgeolocation.ProbeRefused:
		return msgGeoIPRefused, nil
	case ipgeolocation.ProbeLimited:
		return msgGeoIPLimited, nil
	case "not_yet_probed":
		return msgGeoIPNotDownloaded, nil
	default:
		return msgGeoIPFailed, nil
	}
}
