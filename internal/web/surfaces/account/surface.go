package account

import (
	"embed"
	"time"

	"github.com/NaejEL/opnview/internal/auth"
	"github.com/NaejEL/opnview/internal/store"
	"github.com/NaejEL/opnview/internal/store/accounts"
	"github.com/NaejEL/opnview/internal/web"
)

// The surface's files: its catalogue file and its marker.
//
//go:embed locale surface.json
var files embed.FS

type messageKey = web.MessageKey

func init() {
	web.RegisterSurface(web.Surface{
		Name:  "account",
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
	store         *store.Store
	now           func() time.Time
	sessions      *auth.Sessions
	setupToken    *auth.SetupToken
	params        auth.Params
	decoyPassword accounts.PasswordHash
}

// newSurface binds the handlers to a host.
func newSurface(host web.Host) *surface {
	return &surface{Host: host, store: host.Store(), now: host.Now, sessions: host.Sessions(),
		setupToken: host.SetupToken(), params: host.PasswordParams(), decoyPassword: host.DecoyPassword()}
}
