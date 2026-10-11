// Package settings is the settings surface: where credentials enter the product. Its cards
// are the OPNsense credentials, the MaxMind geolocation key, the Public Suffix List's
// refresh and the appearance (theme).
package settings

import (
	"embed"
	"net/http"
	"time"

	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/secret"
	"github.com/NaejEL/opnview/internal/store"
	"github.com/NaejEL/opnview/internal/web"
)

// The surface's files: its page template, its catalogue file and its marker.
//
//go:embed template locale surface.json
var files embed.FS

type messageKey = web.MessageKey

// querySaved names, in the redirect after a saved edit, the card the edit was made in.
const querySaved = "saved"

// pageSettings is the surface's one page.
const pageSettings = "settings"

func init() {
	web.RegisterSurface(web.Surface{
		Name:  "settings",
		Files: files,
		Pages: []web.Page{{Name: pageSettings, TitleKey: msgTitleSettings, Refusal: refusalView}},
		Routes: func(host web.Host) []web.Route {
			s := newSurface(host)
			return []web.Route{
				{Method: http.MethodGet, Pattern: web.PathSettings, Access: web.AccessAuthenticated,
					Handler: s.handleSettingsForm},
				{Method: http.MethodPost, Pattern: web.PathSettings, Access: web.AccessAuthenticated,
					Page: pageSettings, Handler: s.handleSettingsSubmit},
			}
		},
		Tab:     &web.Tab{Page: pageSettings, Path: web.PathSettings, TitleKey: msgTitleSettings, Order: 10},
		Notices: []web.MessageKey{msgSettingsSaved},
		Keys:    func() []web.MessageKey { return keys() },
	})
}

// surface is the surface's handlers over what the host grants.
type surface struct {
	web.Host
	store        *store.Store
	now          func() time.Time
	box          *secret.Box
	credentials  *config.FirewallCredentials
	client       *opnsense.Client
	geoip        web.GeoIP
	publicSuffix web.PublicSuffixList
}

// newSurface binds the handlers to a host.
func newSurface(host web.Host) *surface {
	return &surface{Host: host, store: host.Store(), now: host.Now, box: host.Box(),
		credentials: host.Credentials(), client: host.Client(), geoip: host.GeoIP(),
		publicSuffix: host.PublicSuffix()}
}

// view is everything the surface's template may read.
//
// IT CARRIES NO SECRET. There is no field here for an API secret, a MaxMind key, a
// password, a salt or a key file, and a test asserts that no response body contains any of
// them.
type view struct {
	web.Base

	// FirewallURL and APIKey are configuration rather than secrets, so they are
	// pre-filled: a reader correcting a typo has to be able to see the typo.
	FirewallURL string
	APIKey      string
	// CertificateFingerprint is the pinned SHA-256 fingerprint, pre-filled for the
	// same reason: it is a hash of a certificate the firewall shows to anybody, so
	// it is configuration and not a secret.
	CertificateFingerprint string
	// The three states the surface reports. Each is a key.
	CredentialState   messageKey
	VerificationState messageKey
	LicenceKeyState   messageKey
	// MaxMindAccountID is pre-filled: it identifies an account and grants nothing
	// without the licence key.
	MaxMindAccountID string
	// GeoIPState says where the MaxMind databases stand, and GeoIPBuild is the
	// build of the ones in use, empty when there are none.
	GeoIPState messageKey
	GeoIPBuild string
	// Themes are the options the selector offers.
	Themes []themeOption

	// SettingsCard names the settings card a submission was about; its notice and
	// its refusal are drawn in that card rather than at the head of the page.
	SettingsCard string
	// SettingsEdited marks, by card, the cards drawn with values a submission carried that are
	// not stored: edited, and not saved. APISecretNotKept and LicenceKeyNotKept say a secret
	// was typed and not stored, which a field never pre-filled cannot show any other way.
	SettingsEdited    map[string]bool
	APISecretNotKept  bool
	LicenceKeyNotKept bool

	// PublicSuffix is the Public Suffix List card.
	PublicSuffix publicSuffixCard
}

// themeOption is one row of the theme selector.
type themeOption struct {
	// Value is what the form submits and what the setting row holds.
	Value web.Theme
	// Label is the catalogue key naming it.
	Label messageKey
	// Selected marks the one in force.
	Selected bool
}

// setCard names the card a submission was about and tells the layout the notice and the
// refusal are drawn inside it.
func (built *view) setCard(card string) {
	built.SettingsCard = card
	built.InlineNotices = card != ""
}

// hasUnsavedEdits says whether any card of the page is drawn with an edit that is not
// saved: one a redirect would discard.
func (built view) hasUnsavedEdits() bool {
	for _, edited := range built.SettingsEdited {
		if edited {
			return true
		}
	}
	return false
}

// newView returns a view with everything common to every page filled in.
func (s *surface) newView(writer http.ResponseWriter, request *http.Request,
	session *store.Session, title messageKey) (view, error) {
	base, err := s.NewBase(writer, request, session, title)
	if err != nil {
		return view{}, err
	}
	return view{Base: base}, nil
}

// refusalView is the data of the page when the core draws it with a refusal on it.
func refusalView(host web.Host, request *http.Request, base web.Base) (any, error) {
	built := view{Base: base}
	if err := newSurface(host).fillSettings(request, &built, msgVerifyNotAttempted); err != nil {
		return nil, err
	}
	return &built, nil
}
