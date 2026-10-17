package settings

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/web"
)

// The settings surface's JSON routes (docs/widget-api.md, "Settings API"): every figure of the
// settings, and every save of its cards. The MaxMind settings are the maxmind
// plugin's declared settings, written through POST /api/plugins/maxmind/settings, which stores
// the same rows under the same keys; this surface reports their state and does not write them a
// second way.

// The JSON paths.
const (
	// PathAPISettings is every figure of the settings.
	PathAPISettings = "/api/settings"
	// PathAPIFirewall saves the firewall card and verifies it.
	PathAPIFirewall = "/api/settings/firewall"
	// PathAPIFirewallCertificate reads the fingerprint of the certificate a firewall presents,
	// and stores nothing.
	PathAPIFirewallCertificate = "/api/settings/firewall/certificate"
	// PathAPIPublicSuffix saves the Public Suffix List's refresh interval.
	PathAPIPublicSuffix = "/api/settings/public-suffix"
)

// GeolocationPlugin is the plugin whose declared settings are the MaxMind card's.
const GeolocationPlugin = "maxmind"

// apiRoutes are the surface's JSON rows of the route table.
func (s *surface) apiRoutes() []web.Route {
	return []web.Route{
		{Method: http.MethodGet, Pattern: PathAPISettings, Access: web.AccessAuthenticated, JSON: true,
			Handler: s.handleAPISettings},
		{Method: http.MethodPost, Pattern: PathAPIFirewall, Access: web.AccessAuthenticated, JSON: true,
			Handler: s.handleAPIFirewall},
		{Method: http.MethodPost, Pattern: PathAPIFirewallCertificate, Access: web.AccessAuthenticated,
			JSON: true, Handler: s.handleAPIFirewallCertificate},
		{Method: http.MethodPost, Pattern: PathAPIPublicSuffix, Access: web.AccessAuthenticated, JSON: true,
			Handler: s.handleAPIPublicSuffix},
	}
}

// SettingsAnswer is every figure of the settings answer.
type SettingsAnswer struct {
	Firewall     FirewallState     `json:"firewall"`
	Geolocation  GeolocationState  `json:"geolocation"`
	PublicSuffix PublicSuffixState `json:"public_suffix"`
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

// GeolocationState is the MaxMind card, whose settings are the maxmind plugin's.
type GeolocationState struct {
	// Plugin and SettingsPath are where the card's settings are written.
	Plugin       string `json:"plugin"`
	SettingsPath string `json:"settings_path"`
	// AccountID is the MaxMind account ID, null when not set.
	AccountID *string `json:"account_id"`
	// LicenceKeyState is absent, stored or undecryptable.
	LicenceKeyState string `json:"licence_key_state"`
	// DownloadState is where the databases stand, from the refresh's availability row.
	DownloadState string `json:"download_state"`
	// BuildAt is the build of the databases in use, null when none is.
	BuildAt *int64 `json:"build_at"`
}

// PublicSuffixState is the Public Suffix List card.
type PublicSuffixState struct {
	// State is the last refresh's outcome.
	State string `json:"state"`
	// InUse says whether a copy is in use; Version, VersionStated and Rules describe it.
	InUse         bool    `json:"in_use"`
	Version       *string `json:"version"`
	VersionStated bool    `json:"version_stated"`
	Rules         *int    `json:"rules"`
	// ConfirmedAt is when a refresh last confirmed the copy current, null when none has.
	ConfirmedAt *int64 `json:"confirmed_at"`
	// RefreshIntervalSeconds is the refresh interval in force.
	RefreshIntervalSeconds int64 `json:"refresh_interval_seconds"`
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

	answer.Geolocation = GeolocationState{Plugin: GeolocationPlugin,
		SettingsPath: "/api/plugins/" + GeolocationPlugin + "/settings"}
	if answer.Geolocation.AccountID, err = text(config.KeyMaxMindAccountID); err != nil {
		return SettingsAnswer{}, err
	}
	licenceState, err := s.licenceKeyState(ctx)
	if err != nil {
		return SettingsAnswer{}, err
	}
	answer.Geolocation.LicenceKeyState = web.ConditionOf(licenceState)
	geoIPState, err := s.geoIPState(ctx)
	if err != nil {
		return SettingsAnswer{}, err
	}
	answer.Geolocation.DownloadState = web.ConditionOf(geoIPState)
	if s.geoip != nil {
		if build, ready := s.geoip.Ready(); ready {
			answer.Geolocation.BuildAt = &build
		}
	}

	suffixState, err := s.publicSuffixState(ctx)
	if err != nil {
		return SettingsAnswer{}, err
	}
	answer.PublicSuffix = PublicSuffixState{State: web.ConditionOf(suffixState)}
	if s.publicSuffix != nil {
		version, rules, confirmedAt, held := s.publicSuffix.Held()
		if held {
			answer.PublicSuffix.InUse = true
			answer.PublicSuffix.Rules = &rules
			answer.PublicSuffix.Version = nullable(version)
			answer.PublicSuffix.VersionStated = version != ""
		}
		if confirmedAt > 0 {
			answer.PublicSuffix.ConfirmedAt = &confirmedAt
		}
	}
	settings, err := s.Configuration(ctx)
	if err != nil {
		return SettingsAnswer{}, err
	}
	answer.PublicSuffix.RefreshIntervalSeconds = int64(settings.PublicSuffixRefreshInterval / time.Second)
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

// handleAPIPublicSuffix saves the refresh interval, in seconds. An absent interval changes
// nothing, as a form that does not carry the field.
func (s *surface) handleAPIPublicSuffix(writer http.ResponseWriter, request *http.Request) {
	body, ok := readBody(writer, request)
	if !ok {
		return
	}
	secondsText, sent := web.JSONText(body, "refresh_interval_seconds")
	if sent {
		seconds, changed, refusal := s.publicSuffixInterval(request.Context(), secondsText)
		if refusal != "" {
			web.RefuseJSON(writer, http.StatusBadRequest, web.ConditionOf(refusal), "refresh_interval_seconds")
			return
		}
		if changed {
			if err := s.savePublicSuffixInterval(request.Context(), seconds, s.now().UTC().Unix()); err != nil {
				web.FailJSON(writer, request, err)
				return
			}
		}
	}
	s.answerSaved(writer, request, msgVerifyNotAttempted)
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
