package settings

import (
	"embed"
	"time"

	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/secret"
	"github.com/NaejEL/opnview/internal/store"
	"github.com/NaejEL/opnview/internal/web"
)

// The surface's files: its catalogue file and its marker.
//
//go:embed locale surface.json
var files embed.FS

type messageKey = web.MessageKey

func init() {
	web.RegisterSurface(web.Surface{
		Name:  "settings",
		Files: files,
		Routes: func(host web.Host) []web.Route {
			return newSurface(host).apiRoutes()
		},
		Keys: func() []web.MessageKey { return keys() },
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
