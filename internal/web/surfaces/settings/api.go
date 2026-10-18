package settings

import (
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/web"
)

// The settings surface's JSON routes (docs/widget-api.md, "Settings API"): every figure of the
// settings, and every save of its cards. A plugin's settings -- the MaxMind and Public Suffix List
// ones included -- are the plugin's own, served and written by /api/plugins/{name}/settings; the
// appearance is the frontend surface's.

// The JSON paths.
const (
	// PathAPISettings is every figure of the settings.
	PathAPISettings = "/api/settings"
	// PathAPIFirewall saves the firewall card and verifies it.
	PathAPIFirewall = "/api/settings/firewall"
	// PathAPIFirewallCertificate reads the fingerprint of the certificate a firewall presents,
	// and stores nothing.
	PathAPIFirewallCertificate = "/api/settings/firewall/certificate"
)

// apiRoutes are the surface's JSON rows of the route table.
func (s *surface) apiRoutes() []web.Route {
	return []web.Route{
		web.JSONRoute(http.MethodGet, PathAPISettings, s.handleAPISettings),
		web.JSONRoute(http.MethodPost, PathAPIFirewall, s.handleAPIFirewall),
		web.JSONRoute(http.MethodPost, PathAPIFirewallCertificate, s.handleAPIFirewallCertificate),
	}
}

// SettingsAnswer is every figure of the settings answer.
type SettingsAnswer struct {
	Firewall FirewallState `json:"firewall"`
}

// FirewallState is the firewall card. No secret is ever in it: the API secret is reported by the
// credential state alone.
type FirewallState struct {
	// URL, APIKey and CertificateFingerprint are configuration, null when not set.
	URL                    *string `json:"url"`
	APIKey                 *string `json:"api_key"`
	CertificateFingerprint *string `json:"certificate_fingerprint"`
	// CredentialState is absent, ready or undecryptable.
	CredentialState string `json:"credential_state"`
	// Verification is the outcome of the credential check: not_attempted unless the answer is a
	// save's, which makes one.
	Verification string `json:"verification"`
}

// CertificateAnswer is the fingerprint a firewall presented.
type CertificateAnswer struct {
	PresentedFingerprint string `json:"presented_fingerprint"`
}

// nullable is a text, null when empty.
func nullable(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

// settingsAnswer reads every figure of the answer.
//
// IT READS NO SECRET INTO THE ANSWER. What it reads is whether each one is there and whether it
// can be opened, which are states, and it reports them as closed codes.
func (s *surface) settingsAnswer(request *http.Request, verification messageKey) (SettingsAnswer, error) {
	ctx := request.Context()
	text := func(key string) (*string, error) {
		value, _, err := s.store.Setting(ctx, key)
		return nullable(value), err
	}
	var answer SettingsAnswer
	var err error
	if answer.Firewall.URL, err = text(config.KeyOPNsenseBaseURL); err != nil {
		return SettingsAnswer{}, err
	}
	if answer.Firewall.APIKey, err = text(config.KeyOPNsenseAPIKey); err != nil {
		return SettingsAnswer{}, err
	}
	if answer.Firewall.CertificateFingerprint, err = text(config.KeyOPNsenseCertificateFingerprint); err != nil {
		return SettingsAnswer{}, err
	}
	_, state, err := config.LoadFirewallCredentials(ctx, s.store, s.unsealer())
	if err != nil {
		return SettingsAnswer{}, err
	}
	answer.Firewall.CredentialState = web.ConditionOf(credentialStateKey(state))
	answer.Firewall.Verification = web.ConditionOf(verification)

	return answer, nil
}

func (s *surface) handleAPISettings(writer http.ResponseWriter, request *http.Request) {
	answer, err := s.settingsAnswer(request, msgVerifyNotAttempted)
	if err != nil {
		web.FailJSON(writer, request, err)
		return
	}
	web.WriteJSON(writer, http.StatusOK, answer)
}

// readBody decodes a write's body, or refuses it.
func readBody(writer http.ResponseWriter, request *http.Request) (map[string]any, bool) {
	body, ok := web.DecodeJSONObject(request.Body)
	if !ok {
		web.RefuseJSON(writer, http.StatusBadRequest, web.ConditionMalformedBody, "")
	}
	return body, ok
}

// member is one member of a body as the form field would carry it.
func member(body map[string]any, key string) string {
	value, _ := web.JSONText(body, key)
	return value
}

// handleAPIFirewall saves the firewall card: the body is the card's whole content, as one card -- an absent or empty url, api_key or certificate_fingerprint removes that
// setting, and an absent or empty api_secret keeps the one stored.
func (s *surface) handleAPIFirewall(writer http.ResponseWriter, request *http.Request) {
	body, ok := readBody(writer, request)
	if !ok {
		return
	}
	firewallURL := strings.TrimRight(strings.TrimSpace(member(body, "url")), "/")
	apiKey := strings.TrimSpace(member(body, "api_key"))
	apiSecret := member(body, "api_secret")
	if refusal := firewallURLRefusal(firewallURL); refusal != "" {
		web.RefuseJSON(writer, http.StatusBadRequest, web.ConditionOf(refusal), "url")
		return
	}
	fingerprint, refusal := firewallCredentialRefusal(apiKey, apiSecret, member(body, "certificate_fingerprint"))
	if refusal != "" {
		field := "certificate_fingerprint"
		if refusal == msgAPIKeyRequired {
			field = "api_key"
		}
		web.RefuseJSON(writer, http.StatusBadRequest, web.ConditionOf(refusal), field)
		return
	}
	verification, err := s.saveFirewall(request.Context(), firewallURL, apiKey, apiSecret, fingerprint,
		s.now().UTC().Unix())
	if err != nil {
		web.FailJSON(writer, request, err)
		return
	}
	s.answerSaved(writer, request, verification)
}

// handleAPIFirewallCertificate connects to the firewall at the given URL and answers the
// fingerprint of the certificate it presents. It stores nothing and verifies nothing, as a fetch control.
func (s *surface) handleAPIFirewallCertificate(writer http.ResponseWriter, request *http.Request) {
	body, ok := readBody(writer, request)
	if !ok {
		return
	}
	firewallURL := strings.TrimRight(strings.TrimSpace(member(body, "url")), "/")
	if refusal := firewallURLRefusal(firewallURL); refusal != "" {
		web.RefuseJSON(writer, http.StatusBadRequest, web.ConditionOf(refusal), "url")
		return
	}
	if firewallURL == "" {
		web.RefuseJSON(writer, http.StatusBadRequest, web.ConditionOf(msgFirewallURLRequired), "url")
		return
	}
	presented, err := opnsense.FetchCertificateFingerprint(request.Context(), firewallURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "opnview: web: fetching the firewall certificate: %v\n", err)
		web.RefuseJSON(writer, http.StatusBadRequest, web.ConditionOf(msgCertificateNotFetched), "url")
		return
	}
	web.WriteJSON(writer, http.StatusOK, CertificateAnswer{PresentedFingerprint: presented})
}

// answerSaved answers a save with every figure as it now stands.
func (s *surface) answerSaved(writer http.ResponseWriter, request *http.Request, verification messageKey) {
	answer, err := s.settingsAnswer(request, verification)
	if err != nil {
		web.FailJSON(writer, request, err)
		return
	}
	web.WriteJSON(writer, http.StatusOK, answer)
}
