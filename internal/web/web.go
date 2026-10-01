// Package web is opnview's HTTP surface: the server, the routing, the session and
// CSRF middleware, the three surfaces this cycle ships, the embedded templates and
// stylesheet, and the string catalogue.
//
// WHAT THE SEAM IS. The server owns routing, sessions and the static-asset story;
// a feature owns its handlers and registers them. Step 5 adds widget endpoints by
// adding files and route rows, not by rewriting cmd/opnview/main.go. That is why
// the routes are a TABLE with a declared access level per row rather than a
// sequence of mux calls: a route added later carries a decision about who may
// reach it, and a test enumerates the table and fails on a route that carries
// none.
//
// WHAT IT SERVES, AND NOTHING MORE. Setup, sign-in and sign-out, settings, and one
// stylesheet. There is no widget endpoint, no canvas, no dashboard format and no
// language picker here: those are steps 5 and 7.
//
// NO CONTROL ON ANY PAGE CAN CHANGE A FIREWALL SETTING, and none suggests that it
// can. The only call this package makes to the firewall is the credential
// verification in verify.go, which is a read of one registry endpoint.
//
// NOTHING IS LOADED FROM OFF-HOST. Every byte a page needs is embedded in this
// package and served by this binary. No font, no script, no icon set, no favicon,
// no analytics, no version check.
package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/NaejEL/opnview/internal/auth"
	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/secret"
	"github.com/NaejEL/opnview/internal/store"
)

// The paths. They are constants because a template links to them and a test
// enumerates them, and two spellings of one path is a route that exists and a link
// that does not.
const (
	// PathRoot sends a reader to whichever surface applies.
	PathRoot = "/"
	// PathSetup is the first-account surface.
	PathSetup = "/setup"
	// PathSignIn is the sign-in surface.
	PathSignIn = "/sign-in"
	// PathSignOut ends a session.
	PathSignOut = "/sign-out"
	// PathSettings is where credentials enter the product.
	PathSettings = "/settings"
	// PathRecover is the password reset authorised by the key file.
	PathRecover = "/recover"
	// PathStylesheet is the one static asset.
	PathStylesheet = "/asset/style.css"
)

// The cookies.
//
// NONE OF THEM CARRIES THE Secure FLAG, and the reason is recorded in README.md's
// limitations section rather than hidden here: Secure makes sign-in impossible over
// plain HTTP, and there is no TLS story before step 8. SameSite=Lax and a CSRF
// token are what stand in for it, and the CSRF token is built now because setup and
// settings are mutating forms and step 5's mutating endpoints would otherwise
// inherit nothing.
const (
	cookieSession = "opnview_session"
	cookieCSRF    = "opnview_csrf"
	cookieNotice  = "opnview_notice"
)

// Access is who may reach a route. Every route declares one, and a test asserts
// that the membership of each set is the one somebody decided on.
type Access string

// The three access levels, and no fourth.
const (
	// AccessPublic is reachable by anybody. The set is deliberately small: the
	// setup surface, the sign-in surface, the reset form and the stylesheet.
	AccessPublic Access = "public"
	// AccessAuthenticated refuses a request carrying no live session.
	AccessAuthenticated Access = "authenticated"
	// AccessKeyFile refuses a request that does not present the contents of the
	// key file beside the database. It is one route: the password reset, which is
	// recoverable by possession of that file and by nothing else.
	AccessKeyFile Access = "key_file"
)

// Route is one registered route.
type Route struct {
	// Method is the HTTP method. A route is registered per method, so a POST to a
	// path that only answers GET is a 405 from the router rather than a handler
	// that has to remember to check.
	Method string
	// Pattern is the path.
	Pattern string
	// Access is who may reach it.
	Access Access
	// RequiresNoAccount marks the setup surface.
	//
	// IT IS THE STATE THAT CLOSES SETUP PERMANENTLY, it is evaluated in the
	// middleware rather than in a handler, and it is a COUNT AGAINST THE DATABASE
	// rather than a flag. A flag in memory is reset by a restart; a check written
	// into the GET handler only leaves the POST open. Both of those are the classic
	// first-run hole, and this cycle's stated top risk is exactly that hole.
	RequiresNoAccount bool

	handler http.HandlerFunc
}

// Options are what a server needs.
type Options struct {
	// Store is the database. Required.
	Store *store.Store
	// DataDir is where the database and the key file live. Required.
	DataDir string
	// Box opens and seals the stored credentials. It may be nil, which is the
	// lost-or-replaced key file state: the server still starts, still signs people
	// in, and reports that the stored credentials cannot be read.
	Box *secret.Box
	// Credentials is the live holder the OPNsense client reads. Required.
	Credentials *config.FirewallCredentials
	// Client is the OPNsense client used to verify what was typed. Required.
	Client *opnsense.Client
	// SetupToken is the one-time token printed at start-up. Required.
	SetupToken *auth.SetupToken
	// PasswordParams are the Argon2id parameters a NEW record is written with. The
	// zero value takes auth.DefaultParams. A test lowers them so that hashing does
	// not dominate a test run; nothing in the product lowers them.
	PasswordParams auth.Params
	// Now is the clock. Nil takes time.Now, and a test drives both session bounds
	// through it rather than sleeping.
	Now func() time.Time
}

// Server is the HTTP surface.
type Server struct {
	store       *store.Store
	dataDir     string
	box         *secret.Box
	credentials *config.FirewallCredentials
	client      *opnsense.Client
	setupToken  *auth.SetupToken
	sessions    *auth.Sessions
	renderer    *renderer
	params      auth.Params
	now         func() time.Time

	// decoyPassword is a password record nobody's password matches.
	//
	// IT IS WHAT MAKES THE TWO SIGN-IN FAILURES INDISTINGUISHABLE. A login that
	// does not exist would otherwise answer without doing any Argon2id work at all,
	// and the difference between "instant refusal" and "sixty milliseconds then
	// refusal" tells an attacker which logins exist. A refusal for an unknown login
	// verifies against this record, so both paths cost the same.
	decoyPassword store.PasswordHash

	routes []Route
	mux    *http.ServeMux
}

// New returns a server with every route registered.
func New(options Options) (*Server, error) {
	switch {
	case options.Store == nil:
		return nil, errors.New("web: a store is required")
	case options.DataDir == "":
		return nil, errors.New("web: a data directory is required")
	case options.Credentials == nil:
		return nil, errors.New("web: a credential holder is required")
	case options.Client == nil:
		return nil, errors.New("web: an OPNsense client is required")
	case options.SetupToken == nil:
		return nil, errors.New("web: a setup token is required")
	}

	catalogue, err := LoadCatalogue(SourceLanguage)
	if err != nil {
		return nil, err
	}
	built, err := newRenderer(catalogue)
	if err != nil {
		return nil, err
	}

	params := options.PasswordParams
	if params.MemoryKiB == 0 {
		params = auth.DefaultParams
	}
	clock := options.Now
	if clock == nil {
		clock = time.Now
	}

	decoyPassword, err := auth.RandomToken()
	if err != nil {
		return nil, err
	}
	decoy, err := auth.HashPassword(decoyPassword, params)
	if err != nil {
		return nil, err
	}

	server := &Server{
		store:         options.Store,
		dataDir:       options.DataDir,
		box:           options.Box,
		credentials:   options.Credentials,
		client:        options.Client,
		setupToken:    options.SetupToken,
		sessions:      auth.NewSessions(options.Store, auth.DefaultLifetimes()),
		renderer:      built,
		params:        params,
		now:           clock,
		decoyPassword: decoy,
	}
	server.register()
	return server, nil
}

// register builds the route table and the router from it.
//
// THE TABLE IS THE WHOLE LIST. Nothing else calls mux.Handle, so a route that is
// not here does not exist, and a route that is here carries an access level.
func (s *Server) register() {
	s.routes = []Route{
		// The root is authenticated, not public: it serves no content to a reader
		// with no session, it redirects them to the surface that applies.
		{Method: http.MethodGet, Pattern: PathRoot, Access: AccessAuthenticated,
			handler: s.handleRoot},

		{Method: http.MethodGet, Pattern: PathSetup, Access: AccessPublic,
			RequiresNoAccount: true, handler: s.handleSetupForm},
		{Method: http.MethodPost, Pattern: PathSetup, Access: AccessPublic,
			RequiresNoAccount: true, handler: s.handleSetupSubmit},

		{Method: http.MethodGet, Pattern: PathSignIn, Access: AccessPublic,
			handler: s.handleSignInForm},
		{Method: http.MethodPost, Pattern: PathSignIn, Access: AccessPublic,
			handler: s.handleSignInSubmit},
		{Method: http.MethodPost, Pattern: PathSignOut, Access: AccessAuthenticated,
			handler: s.handleSignOut},

		{Method: http.MethodGet, Pattern: PathSettings, Access: AccessAuthenticated,
			handler: s.handleSettingsForm},
		{Method: http.MethodPost, Pattern: PathSettings, Access: AccessAuthenticated,
			handler: s.handleSettingsSubmit},

		// The reset form discloses nothing — it names no login and lists nothing —
		// so it is reachable by somebody who has forgotten their password, which is
		// the only person who needs it. The SUBMISSION is the guarded half.
		{Method: http.MethodGet, Pattern: PathRecover, Access: AccessPublic,
			handler: s.handleRecoverForm},
		{Method: http.MethodPost, Pattern: PathRecover, Access: AccessKeyFile,
			handler: s.handleRecoverSubmit},

		{Method: http.MethodGet, Pattern: PathStylesheet, Access: AccessPublic,
			handler: s.handleStylesheet},
	}

	s.mux = http.NewServeMux()
	for _, route := range s.routes {
		s.mux.Handle(route.Method+" "+route.Pattern, s.guard(route))
	}
}

// Routes returns the registered routes. A test enumerates them and asserts that
// each falls in exactly one access set, so a route added later without a decision
// fails the test rather than shipping open.
func (s *Server) Routes() []Route {
	copied := make([]Route, len(s.routes))
	copy(copied, s.routes)
	return copied
}

// ServeHTTP lets the server be used as a handler directly, which is what a test
// does through httptest.
func (s *Server) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	s.mux.ServeHTTP(writer, request)
}

// CollectExpiredSessions deletes every session past either bound. cmd/opnview
// calls it on start-up and the caller may schedule it; a session past a bound is
// refused whether or not it has run.
func (s *Server) CollectExpiredSessions(ctx context.Context) error {
	return s.sessions.CollectExpired(ctx, s.now())
}

// AccountExists reports whether the installation has an account. cmd/opnview reads
// it to decide whether to print a setup token, and it is the same query the setup
// middleware runs.
func (s *Server) AccountExists(ctx context.Context) (bool, error) {
	count, err := s.store.CountAccounts(ctx)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// ListenAndServe listens on address and serves until ctx is cancelled, then shuts
// down within grace.
//
// THE CONTEXT IS WHAT STOPS IT, so the existing signal path in cmd/opnview stops
// the HTTP server and the collection scheduler through one cancellation.
//
// address has NO DEFAULT anywhere in opnview, for the same reason --data-dir has
// none: a default would pre-empt what the installer of step 8 chooses, and no
// address of any real network belongs in this repository.
func (s *Server) ListenAndServe(ctx context.Context, address string, grace time.Duration) error {
	if address == "" {
		return errors.New("web: no listen address was given")
	}
	server := &http.Server{
		Addr:              address,
		Handler:           s.mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	stopped := make(chan error, 1)
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), grace)
		defer cancel()
		stopped <- server.Shutdown(shutdown)
	}()

	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("web: serving on %s: %w", address, err)
	}
	if err := <-stopped; err != nil {
		return fmt.Errorf("web: shutting down: %w", err)
	}
	return nil
}
