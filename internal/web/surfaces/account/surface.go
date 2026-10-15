package account

import (
	"embed"
	"net/http"
	"time"

	"github.com/NaejEL/opnview/internal/auth"
	"github.com/NaejEL/opnview/internal/store"
	"github.com/NaejEL/opnview/internal/store/accounts"
	"github.com/NaejEL/opnview/internal/web"
)

// The surface's files: its three page templates, its catalogue file and its marker.
//
//go:embed template locale surface.json
var files embed.FS

type messageKey = web.MessageKey

// The pages. The sign-in page's name is the core's (web.PageSignIn).
const (
	pageSetup   = "setup"
	pageSignIn  = web.PageSignIn
	pageRecover = "recover"
)

func init() {
	web.RegisterSurface(web.Surface{
		Name:  "account",
		Files: files,
		Pages: []web.Page{
			{Name: pageSetup, TitleKey: msgTitleSetup, Refusal: refusalView},
			{Name: pageSignIn, TitleKey: msgTitleSignIn, Refusal: refusalView},
			{Name: pageRecover, TitleKey: msgTitleRecover, Refusal: refusalView},
		},
		Routes: func(host web.Host) []web.Route {
			s := newSurface(host)
			return append(s.apiRoutes(), []web.Route{
				{Method: http.MethodGet, Pattern: web.PathSetup, Access: web.AccessPublic,
					RequiresNoAccount: true, Page: pageSetup, Handler: s.handleSetupForm},
				{Method: http.MethodPost, Pattern: web.PathSetup, Access: web.AccessPublic,
					RequiresNoAccount: true, Page: pageSetup, Handler: s.handleSetupSubmit},

				{Method: http.MethodGet, Pattern: web.PathSignIn, Access: web.AccessPublic,
					Handler: s.handleSignInForm},
				{Method: http.MethodPost, Pattern: web.PathSignIn, Access: web.AccessPublic,
					Handler: s.handleSignInSubmit},
				{Method: http.MethodPost, Pattern: web.PathSignOut, Access: web.AccessAuthenticated,
					Handler: s.handleSignOut},

				// The reset form discloses nothing -- it names no login and lists nothing -- so it
				// is reachable by somebody who has forgotten their password, which is the only
				// person who needs it. The SUBMISSION is the guarded half.
				{Method: http.MethodGet, Pattern: web.PathRecover, Access: web.AccessPublic,
					Page: pageRecover, Handler: s.handleRecoverForm},
				{Method: http.MethodPost, Pattern: web.PathRecover, Access: web.AccessKeyFile,
					Page: pageRecover, Handler: s.handleRecoverSubmit},
			}...)
		},
		Notices:  []web.MessageKey{msgSetupDone, msgPasswordReset, msgSignedOut},
		Refusals: []web.MessageKey{msgSignInRefused},
		Keys:     func() []web.MessageKey { return keys() },
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

// view is everything the surface's templates may read.
type view struct {
	web.Base
	// Login is what the reader typed, put back so a refusal does not make them type it
	// again. A password is never put back.
	Login string
}

// newView returns a view with everything common to every page filled in.
func (s *surface) newView(writer http.ResponseWriter, request *http.Request,
	session *accounts.Session, title messageKey) (view, error) {
	base, err := s.NewBase(writer, request, session, title)
	if err != nil {
		return view{}, err
	}
	return view{Base: base}, nil
}

// refusalView is the data of a page the core draws with a refusal on it: the page's view
// over the base, which already carries the refusal.
func refusalView(_ web.Host, _ *http.Request, base web.Base) (any, error) {
	return &view{Base: base}, nil
}
