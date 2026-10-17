package web

import (
	"context"
	"io/fs"
	"net/http"
	"sync"
	"time"

	"github.com/NaejEL/opnview/internal/web/messages"

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
)

// The surface seam.
//
// A SURFACE IS A DIRECTORY. internal/web/surfaces/<name>/ holds the handlers of one part of
// the interface, its templates, its catalogue keys and a surface.json marker, and registers
// itself from an init() with RegisterSurface. The server owns routing, sessions, CSRF, the
// layout, the themes, the catalogue and the static asset; a surface owns everything that is
// specific to it and receives, through Host, exactly what it may use and nothing else.
//
// ADDING A SURFACE EDITS NO SHIPPED FILE. The surface's directory is linked into the binary
// by the file `go generate` writes in internal/web/surfaces (surfaces_gen.go), a test fails
// when that file is stale, and everything else -- the route table, the top bar's tabs, the
// catalogue, the page templates, the closed list of flash keys -- is built from the
// registered surfaces at start.
//
// A SURFACE IS SELF-CONTAINED. It depends on this package's exported contract (Surface, Host,
// Base, the field helpers) and on nothing in another surface, so the directory can later be
// loaded from outside the binary without recompiling the core.
//
// A BAD SURFACE IS REFUSED, NOT FATAL. A surface whose template is missing or does not
// compile, whose catalogue is malformed or redefines a key another file defines, whose name
// or route is already taken, or that declares no access level, is left out with a named
// reason (Server.RefusedSurfaces, and the console), and the server starts without it.

// MessageKey is a catalogue key. A handler chooses a key, never a string, and a template
// resolves it; its own type keeps a plain string from being handed to a template where a key
// belongs. The keys the core chooses are internal/web/messages'.
type MessageKey = messages.Key

// messageKey is the spelling the core's own files use.
type messageKey = MessageKey

// Page is one page of a surface: a body template drawn inside the layout.
type Page struct {
	// Name is the page's name and the base name of its template, template/<Name>.gohtml.
	// It is unique across the whole product, and the page's class on <main> is
	// "surface-" + Name.
	Name string
	// TitleKey names the page: the browser tab's title and the heading of the surface.
	TitleKey MessageKey
	// Refusal builds the data of the page when the server must draw it with a refusal on it
	// -- a missing form token, a closed setup surface -- at the status the refusal deserves.
	// The base already carries the refusal in Base.Error. Nil draws the page from the base
	// alone, which is right for a page whose template reads only the base.
	Refusal func(host Host, request *http.Request, base Base) (data any, err error)
}

// Tab is a surface's link in the top bar of every signed-in page.
type Tab struct {
	// Page is the page the link leads to; the link carries aria-current while that page is
	// the one being read.
	Page string
	// Path is where it leads.
	Path string
	// TitleKey names the link.
	TitleKey MessageKey
	// Order places it among the other tabs, lowest first; ties are broken by the page name.
	Order int
}

// Indicator is a figure or a state a surface draws in the top bar of every signed-in page,
// as a link.
type Indicator struct {
	// Path is where it links.
	Path string
	// Class is an extra class on the link beyond "outages", or empty.
	Class string
	// TextKey is its text; Figure, when not empty, follows it as a figure.
	TextKey MessageKey
	Figure  string
}

// Surface is everything the server needs to know about one directory of the interface.
type Surface struct {
	// Name is the surface's name, unique across the product. It is the directory's name.
	Name string
	// Files holds template/<page>.gohtml for each page and locale/<language>.json for each
	// language the surface has strings in, both optional. Embed them in the surface's own
	// package.
	Files fs.FS
	// Pages are the surface's pages.
	Pages []Page
	// Routes returns the surface's rows of the route table. It is called once, at start,
	// with the host the surface may use.
	Routes func(host Host) []Route
	// Tab is the surface's link in the top bar; nil for a surface with no tab.
	Tab *Tab
	// Indicator reads the surface's top-bar indicator for a signed-in page. When it returns
	// an error the console is told and IndicatorUnknown is drawn in its place. Nil for a
	// surface with no indicator.
	Indicator        func(ctx context.Context, host Host) (Indicator, error)
	IndicatorUnknown Indicator
	// Notices and Refusals are the catalogue keys one of the surface's redirects may carry in
	// the flash cookie. The list is closed: a value that is on no surface's list is dropped.
	// A notice reports something the reader asked for having happened; a refusal, the
	// opposite.
	Notices  []MessageKey
	Refusals []MessageKey
	// Keys returns every catalogue key the surface's code can choose, so a test can assert
	// that each exists and that no catalogue entry is unreachable. Nil means none.
	Keys func() []MessageKey
}

// SurfaceRefusal is why one surface was left out of a server.
type SurfaceRefusal struct {
	// Surface is the name it declared, possibly empty.
	Surface string
	// Reason is the named reason.
	Reason string
}

// Error names the surface and the reason.
func (r SurfaceRefusal) Error() string {
	return "web: the surface " + quoted(r.Surface) + " was refused: " + r.Reason
}

func quoted(text string) string { return `"` + text + `"` }

// The registry of the surfaces linked into the binary.
var (
	registryMutex sync.Mutex
	registered    []Surface
)

// RegisterSurface adds a surface to the product. It is called from the init() of the
// surface's package. It never fails: a surface that cannot be served is refused when a
// server is built, with a reason, rather than stopping the process at start-up.
func RegisterSurface(surface Surface) {
	registryMutex.Lock()
	defer registryMutex.Unlock()
	registered = append(registered, surface)
}

// RegisteredSurfaces returns the surfaces linked into this binary, in the order they
// registered.
func RegisteredSurfaces() []Surface {
	registryMutex.Lock()
	defer registryMutex.Unlock()
	return append([]Surface(nil), registered...)
}

// Host is what a surface may ask of the server it is mounted on: the collaborators the
// server was built with, and the few services that must be the same for every surface
// (drawing, refusing, redirecting, the configuration in force). It is deliberately an
// interface: a surface cannot reach the server's route table, its renderer or its
// sessions' internals, only what is listed here.
type Host interface {
	// Store is the database.
	Store() *store.Store
	// Now is the clock.
	Now() time.Time
	// DataDir is where the database and the key file live.
	DataDir() string
	// Box opens and seals stored credentials; nil when the key file is lost.
	Box() *secret.Box
	// Credentials is the live holder the OPNsense client reads.
	Credentials() *config.FirewallCredentials
	// Client is the OPNsense client used to verify what was typed.
	Client() *opnsense.Client
	// SetupToken is the one-time token printed at start-up.
	SetupToken() *auth.SetupToken
	// Sessions issues, validates and revokes sessions.
	Sessions() *auth.Sessions
	// PasswordParams are the Argon2id parameters a new record is written with.
	PasswordParams() auth.Params
	// DecoyPassword is a record nobody's password matches.
	DecoyPassword() accounts.PasswordHash
	// LiveSettings is the configuration holder the scheduler reads; may be nil.
	LiveSettings() *config.Live
	// Collector is told the configuration after a save; may be nil.
	Collector() Reconfigurable
	// GeoIP is woken by a save of a geolocation plugin's settings; may be nil.
	GeoIP() GeoIP
	// Widgets is the widget API's service.
	Widgets() *widget.Service
	// Plugins are the loaded plugins and those refused.
	Plugins() *plugin.Loaded
	// Broker is the firewall broker, nil where none was given.
	Broker() *broker.Broker

	// Configuration loads the configuration in force, through the path start-up uses.
	Configuration(ctx context.Context) (config.Config, error)
	// ApplyConfiguration loads the configuration again and hands it to the live holder and
	// to the running collector, so a saved value takes effect without a restart.
	ApplyConfiguration(ctx context.Context) error
	// AccountExists reports whether the installation has an account.
	AccountExists(ctx context.Context) (bool, error)
	// ReadTheme reads the stored theme.
	ReadTheme(ctx context.Context) Theme

	// NewBase returns the part of a page's data common to every page: the language, the
	// theme, the form token, the carried notice or refusal, the paths, the tabs and the
	// indicators.
	NewBase(writer http.ResponseWriter, request *http.Request, session *accounts.Session,
		title MessageKey) (Base, error)
	// Render writes one page, buffered, with the security headers. data must be a pointer
	// to a struct embedding Base.
	Render(writer http.ResponseWriter, status int, page string, data any) error
	// RenderRefusal draws a page with a refusal on it at the given status.
	RenderRefusal(writer http.ResponseWriter, request *http.Request, status int, page string,
		key MessageKey)
	// FailInternal answers 500 with a catalogue string, and the console gets the detail.
	FailInternal(writer http.ResponseWriter, request *http.Request, err error)
	// FormToken is the form token the request's reader writes with -- the session's own, or the
	// one in the browser's own cookie, set when there is none -- for a JSON answer to carry.
	FormToken(writer http.ResponseWriter, request *http.Request) (string, error)
	// RedirectWithError and RedirectWithNotice send the reader elsewhere, carrying a key.
	RedirectWithError(writer http.ResponseWriter, request *http.Request, path string, key MessageKey)
	RedirectWithNotice(writer http.ResponseWriter, request *http.Request, path string, key MessageKey)
	// RefuseSetupClosed turns away a request to the setup surface after an account exists.
	RefuseSetupClosed(writer http.ResponseWriter, request *http.Request)
}

// AllHandlerKeys is every catalogue key the Go code of the core and of every registered
// surface can choose, and every key a built-in plugin carries -- reached through the registry
// rows and the settings its manifest declares, which the loader confines its keys to -- so a
// test can assert that each exists and that no catalogue entry is unreachable.
func AllHandlerKeys() []MessageKey {
	keys := messages.CoreKeys()
	for _, surface := range RegisteredSurfaces() {
		if surface.Keys != nil {
			keys = append(keys, surface.Keys()...)
		}
	}
	for key := range plugin.BuiltinsLoaded().Strings(SourceLanguage) {
		keys = append(keys, MessageKey(key))
	}
	core, _ := dataset.Core()
	for _, declaration := range core {
		keys = append(keys, MessageKey(declaration.Label))
	}
	return keys
}
