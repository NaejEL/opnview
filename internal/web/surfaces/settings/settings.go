package settings

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/NaejEL/opnview/internal/store/accounts"
	"github.com/NaejEL/opnview/internal/web"

	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/opnsense"
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

	settingsCardFirewall   = "firewall"
	settingsCardAppearance = "appearance"
)

// queryVerified carries, in the redirect after a firewall save, the outcome of the
// verification that save made.
const queryVerified = "verified"

// settingsCards are the cards a submission may name.
func settingsCards() []string {
	return []string{settingsCardFirewall, settingsCardAppearance}
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

	verification := msgVerifyNotAttempted
	switch card {
	case settingsCardFirewall:
		if refusal := firewallURLRefusal(firewallURL); refusal != "" {
			refuse(refusal)
			return
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
		fingerprint, refusal := firewallCredentialRefusal(apiKey, apiSecret, fingerprintTyped)
		if refusal != "" {
			refuse(refusal)
			return
		}
		var err error
		verification, err = s.saveFirewall(ctx, firewallURL, apiKey, apiSecret, fingerprint, now)
		if err != nil {
			s.FailInternal(writer, request, err)
			return
		}

	case settingsCardAppearance:
		refusal, err := s.saveTheme(ctx, request.PostFormValue("theme"), now)
		if err != nil {
			s.FailInternal(writer, request, err)
			return
		}
		if refusal != "" {
			refuse(refusal)
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

// fillSettings puts the current configuration and the two firewall states into a view.
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
