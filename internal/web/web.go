package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/NaejEL/opnview/internal/auth"
	"github.com/NaejEL/opnview/internal/broker"
	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/dataset"
	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/plugin"
	"github.com/NaejEL/opnview/internal/secret"
	"github.com/NaejEL/opnview/internal/store"
	"github.com/NaejEL/opnview/internal/store/accounts"
	"github.com/NaejEL/opnview/internal/widget"

	// Linking the built-in widget tree is what makes the built-in widgets exist in the binary
	// and in every test that builds a server.
	"github.com/NaejEL/opnview/internal/widget/request"
	_ "github.com/NaejEL/opnview/internal/widget/widgets"
)

// The paths the core itself knows. They are constants because a template links to them and
// a test enumerates them, and two spellings of one path is a route that exists and a link
// that does not. A path that belongs to one surface only is that surface's constant, in its
// own package.
const (
	// PathRoot sends a reader to whichever surface applies.
	PathRoot = "/"
	// PathSetup is the first-account surface.
	PathSetup = "/setup"
	// PathSignIn is the sign-in surface.
	PathSignIn = "/sign-in"
	// PathSignOut ends a session.
	PathSignOut = "/sign-out"
	// PathSettings is where credentials enter the product, and where a signed-in reader
	// lands.
	PathSettings = "/settings"
	// PathRecover is the password reset authorised by the key file.
	PathRecover = "/recover"
	// PathStylesheet is the one static asset.
	PathStylesheet = "/asset/style.css"
)

// PageSignIn is the page a refusal is drawn on when a route names none: the sign-in page,
// which the account surface provides.
const PageSignIn = "signin"

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

// SessionCookieName is the name of the session cookie, for a test that reads the jar.
const SessionCookieName = cookieSession

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
	// JSON marks a route of the widget API: refused in JSON, never redirected to a page.
	JSON bool
	// Page is the page a refusal by the middleware -- a missing form token, a key file not
	// presented -- is drawn on, so the reader lands on the form they were filling in. Empty
	// is the sign-in page.
	Page string
	// Handler answers the request once the middleware has let it through.
	Handler http.HandlerFunc
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
	// Settings is the live configuration the running service reads. The collection
	// surface loads the configuration again after a save and hands it to this holder,
	// so a saved interval reaches the scheduler without a restart. Nil leaves the
	// surface measuring and saving, with nothing running to tell; the next start
	// reads the rows. A test passes its own holder and asserts on its generation,
	// which is how "without a restart" is checked rather than assumed. cmd/opnview
	// passes the holder its scheduler reads.
	Settings *config.Live
	// Collector is told the configuration after a save, so a saved page size reaches
	// the collectors without a restart. Nil is the same state as a nil Settings, for
	// the same reason. cmd/opnview passes its collector, which is the one the
	// scheduler runs.
	Collector Reconfigurable
	// GeoIP is the MaxMind databases: woken when the licence key or the account ID is
	// saved, and asked which build is in use. Nil, as in a test that does not need it,
	// wakes nothing and reports no build.
	GeoIP GeoIP
	// PublicSuffix is the Public Suffix List's copy in use, asked for its figures
	// when the settings surface is drawn. Nil, as in a test that does not need it,
	// reports no copy held.
	PublicSuffix PublicSuffixList
	// SuffixLists is the Public Suffix List in use, which the widget API groups site names
	// by. Nil reports the grouping unavailable.
	SuffixLists request.SuffixLists
	// Plugins are the plugins the collector was built with: their catalogue strings join the
	// catalogue, and the plugin listing reports them. Nil is the built-in plugins alone, loaded
	// with no data directory, as a test that does not drop a plugin of its own needs.
	Plugins *plugin.Loaded
	// Broker is the firewall broker the collector asks: its learned API catalogue is served, and its
	// mirror's tables join the data catalogue. Nil serves an empty API catalogue.
	Broker *broker.Broker
	// Surfaces are surfaces mounted on this server in addition to the registered ones. The
	// product passes none: every surface linked into the binary registers itself. A test
	// passes the surface it is about, which is how a surface is exercised without
	// registering it for every other server of the process.
	Surfaces []Surface
}

// PublicSuffixList is what the settings surface needs of the Public Suffix List's
// refresh: the copy this process holds, its version, its rule count and when a
// refresh last confirmed it current. It is answered from memory and contacts
// nothing, and it is an interface so this package does not import the refresher.
// The figures never come from the availability row's free-text detail.
type PublicSuffixList interface {
	Held() (version string, rules int, confirmedAt int64, held bool)
}

// GeoIP is what the settings surface needs of the MaxMind refresh: to be told that
// the credentials changed, so the databases are fetched now rather than at the next
// daily check, and the build of the databases in use. It is an interface so that
// this package does not import the downloader.
type GeoIP interface {
	Wake()
	Ready() (build int64, ready bool)
}

// Reconfigurable is what the collection surface needs of a running collector: to be
// told the configuration that is now in force, and to apply the operator's selection
// of one kind's implementations at once. It is an interface so that this package
// does not import the collectors, and a test can count what reached it.
type Reconfigurable interface {
	Configure(settings config.Config)
	Reselect(ctx context.Context, kind string) error
}

// Server is the HTTP core. One per process: it holds the routes, the renderer
// and the session store, and nothing a request can change.
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
	decoyPassword accounts.PasswordHash

	// settings and collector are told about a saved configuration. Either may be
	// nil; see Options.
	settings  *config.Live
	collector Reconfigurable
	// geoip is woken by a save of the MaxMind credentials; it may be nil.
	geoip GeoIP
	// publicSuffix answers for the Public Suffix List's copy in use; it may be nil.
	publicSuffix PublicSuffixList
	// plugins are the loaded plugins.
	plugins *plugin.Loaded
	// broker is the firewall broker, nil where none was given.
	broker *broker.Broker
	// widgets is the widget API.
	widgets *widget.Service

	// surfaces are the accepted surfaces, in name order, and refused the others with why.
	surfaces []vetted
	refused  []SurfaceRefusal
	// pages are the pages of the accepted surfaces by name.
	pages map[string]Page
	// tabs and indicators are the top bar's, built from the accepted surfaces.
	tabs       []Tab
	indicators []indicatorSource
	// notices and refusals are the closed list of keys a redirect may carry.
	notices  map[MessageKey]struct{}
	refusals map[MessageKey]struct{}

	routes []Route
	mux    *http.ServeMux
}

// indicatorSource is one surface's top-bar indicator.
type indicatorSource struct {
	surface string
	read    func(ctx context.Context, host Host) (Indicator, error)
	unknown Indicator
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
		params:        params,
		now:           clock,
		decoyPassword: decoy,
		settings:      options.Settings,
		collector:     options.Collector,
		geoip:         options.GeoIP,
		publicSuffix:  options.PublicSuffix,
		plugins:       options.Plugins,
		broker:        options.Broker,
	}
	if server.plugins == nil {
		server.plugins = plugin.BuiltinsLoaded()
	}
	// The data catalogue: the core's datasets and every table of a loaded plugin.
	datasets, err := dataset.NewCatalogue(server.plugins.Datasets())
	if err != nil {
		return nil, err
	}
	if options.Broker != nil {
		gate := options.Broker
		datasets.SetDynamic(func() []dataset.Declaration { return gate.Datasets(context.Background()) })
	}
	server.widgets, err = widget.New(options.Store, widget.Options{Now: clock, SuffixLists: options.SuffixLists,
		Datasets: datasets,
		// The widgets the loaded plugins carry: the built-ins' and the data directory's.
		Plugins: server.plugins.WidgetSources(),
		// Operator widgets are read from <data-dir>/widgets; a refused one is reported by
		// Widgets().Refusals() and never stops the service.
		OperatorDir: filepath.Join(options.DataDir, "widgets")})
	if err != nil {
		return nil, err
	}

	candidates := append(RegisteredSurfaces(), options.Surfaces...)
	accepted, refused, contributed := vetSurfaces(server, coreRoutes(server), candidates)
	server.surfaces, server.refused = accepted, refused
	for _, refusal := range refused {
		fmt.Fprintf(os.Stderr, "opnview: web: %v\n", refusal)
	}

	catalogue, err := buildCatalogue(SourceLanguage, accepted, withDatasetStrings(SourceLanguage,
		server.plugins.Strings(SourceLanguage)))
	if err != nil {
		return nil, err
	}
	server.renderer, err = newRenderer(catalogue, accepted)
	if err != nil {
		return nil, err
	}
	server.assemble(contributed)
	return server, nil
}

// coreRoutes are the rows the core itself serves.
func coreRoutes(s *Server) []Route {
	return []Route{
		// The root is authenticated, not public: it serves no content to a reader
		// with no session, it redirects them to the surface that applies.
		{Method: http.MethodGet, Pattern: PathRoot, Access: AccessAuthenticated,
			Handler: s.handleRoot},
		{Method: http.MethodGet, Pattern: PathStylesheet, Access: AccessPublic,
			Handler: s.handleStylesheet},
	}
}

// handleRoot sends a signed-in reader to the settings surface.
//
// There is nothing else to send them to yet: canvases are step 7. The middleware
// has already turned away a reader with no session, so reaching here means there is
// one.
func (s *Server) handleRoot(writer http.ResponseWriter, request *http.Request) {
	http.Redirect(writer, request, PathSettings, http.StatusSeeOther)
}

// assemble builds the route table, the pages, the tabs, the indicators and the flash lists
// from the accepted surfaces, and the router from the table.
//
// THE TABLE IS THE WHOLE LIST. Nothing else calls mux.Handle, so a route that is
// not here does not exist, and a route that is here carries an access level.
func (s *Server) assemble(contributed map[string][]Route) {
	s.routes = coreRoutes(s)
	s.pages = map[string]Page{}
	s.notices = map[MessageKey]struct{}{}
	s.refusals = map[MessageKey]struct{}{
		msgSetupClosed: {}, msgSessionExpired: {}, msgKeyFileRefused: {},
	}
	for _, accepted := range s.surfaces {
		surface := accepted.surface
		s.routes = append(s.routes, contributed[surface.Name]...)
		for _, page := range surface.Pages {
			s.pages[page.Name] = page
		}
		if surface.Tab != nil {
			s.tabs = append(s.tabs, *surface.Tab)
		}
		if surface.Indicator != nil {
			s.indicators = append(s.indicators, indicatorSource{surface: surface.Name,
				read: surface.Indicator, unknown: surface.IndicatorUnknown})
		}
		for _, key := range surface.Notices {
			s.notices[key] = struct{}{}
		}
		for _, key := range surface.Refusals {
			s.refusals[key] = struct{}{}
		}
	}
	sort.SliceStable(s.tabs, func(i, j int) bool {
		if s.tabs[i].Order != s.tabs[j].Order {
			return s.tabs[i].Order < s.tabs[j].Order
		}
		return s.tabs[i].Page < s.tabs[j].Page
	})

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

// RefusedSurfaces returns the surfaces left out of this server, each with its reason.
func (s *Server) RefusedSurfaces() []SurfaceRefusal {
	return append([]SurfaceRefusal(nil), s.refused...)
}

// MountedSurfaces returns the names of the surfaces this server serves, sorted.
func (s *Server) MountedSurfaces() []string {
	names := make([]string, 0, len(s.surfaces))
	for _, accepted := range s.surfaces {
		names = append(names, accepted.surface.Name)
	}
	return names
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

// The Host interface, answered by the server.

// Store is the database.
func (s *Server) Store() *store.Store { return s.store }

// Now is the clock.
func (s *Server) Now() time.Time { return s.now() }

// DataDir is where the database and the key file live.
func (s *Server) DataDir() string { return s.dataDir }

// Box opens and seals stored credentials; nil when the key file is lost.
func (s *Server) Box() *secret.Box { return s.box }

// Credentials is the live holder the OPNsense client reads.
func (s *Server) Credentials() *config.FirewallCredentials { return s.credentials }

// Client is the OPNsense client used to verify what was typed.
func (s *Server) Client() *opnsense.Client { return s.client }

// SetupToken is the one-time token printed at start-up.
func (s *Server) SetupToken() *auth.SetupToken { return s.setupToken }

// Sessions issues, validates and revokes sessions.
func (s *Server) Sessions() *auth.Sessions { return s.sessions }

// PasswordParams are the Argon2id parameters a new record is written with.
func (s *Server) PasswordParams() auth.Params { return s.params }

// DecoyPassword is a record nobody's password matches.
func (s *Server) DecoyPassword() accounts.PasswordHash { return s.decoyPassword }

// LiveSettings is the configuration holder the scheduler reads; may be nil.
func (s *Server) LiveSettings() *config.Live { return s.settings }

// Collector is told the configuration after a save; may be nil.
func (s *Server) Collector() Reconfigurable { return s.collector }

// GeoIP is woken by a save of the MaxMind credentials; may be nil.
func (s *Server) GeoIP() GeoIP { return s.geoip }

// PublicSuffix answers for the Public Suffix List's copy in use; may be nil.
func (s *Server) PublicSuffix() PublicSuffixList { return s.publicSuffix }

// Plugins are the loaded plugins.
func (s *Server) Plugins() *plugin.Loaded { return s.plugins }

// Broker is the firewall broker, nil where none was given.
func (s *Server) Broker() *broker.Broker { return s.broker }

// Widgets is the widget API's service.
func (s *Server) Widgets() *widget.Service { return s.widgets }

var _ Host = (*Server)(nil)
