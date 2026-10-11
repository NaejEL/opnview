package settings

import (
	"context"
	"net/http"
	"net/http/httptest"

	"github.com/NaejEL/opnview/internal/web"
)

// What the external tests of this surface need of its internals.

// SettingsCards are the cards a submission may name.
var SettingsCards = settingsCards

// VerificationEndpoint is the one endpoint a credential check calls.
var VerificationEndpoint = verificationEndpoint

// PageFigures are every figure the settings page draws, by name, read through the page's own
// fill: what the JSON answer must carry (C-AC9). A state is its catalogue key; a duration its
// figure and its unit.
func PageFigures(ctx context.Context, host web.Host) (map[string]string, error) {
	request := httptest.NewRequest(http.MethodGet, web.PathSettings, nil).WithContext(ctx)
	var built view
	if err := newSurface(host).fillSettings(request, &built, msgVerifyNotAttempted); err != nil {
		return nil, err
	}
	card := built.PublicSuffix
	unit := ""
	for _, option := range card.IntervalField.Units {
		if option.Selected {
			unit = option.Value
		}
	}
	figures := map[string]string{
		"firewall_url":                  built.FirewallURL,
		"api_key":                       built.APIKey,
		"certificate_fingerprint":       built.CertificateFingerprint,
		"credential_state":              string(built.CredentialState),
		"verification":                  string(built.VerificationState),
		"licence_key_state":             string(built.LicenceKeyState),
		"account_id":                    built.MaxMindAccountID,
		"geoip_state":                   string(built.GeoIPState),
		"geoip_build":                   built.GeoIPBuild,
		"public_suffix.state":           string(card.StateKey),
		"public_suffix.in_use":          string(card.InUseKey),
		"public_suffix.version":         card.Version,
		"public_suffix.version_state":   string(card.VersionState),
		"public_suffix.rules":           card.Rules,
		"public_suffix.confirmed":       card.Confirmed,
		"public_suffix.confirmed_state": string(card.ConfirmedState),
		"public_suffix.interval":        card.IntervalField.Value + " " + unit,
	}
	for _, option := range built.Themes {
		if option.Selected {
			figures["theme"] = string(option.Value)
		}
	}
	return figures, nil
}
