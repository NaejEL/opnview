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
// the interface, its catalogue keys and a surface.json marker, and registers itself from an
// init() with RegisterSurface. The server owns routing, sessions, CSRF and the catalogue; a
// surface owns everything that is specific to it and receives, through Host, exactly what it
// may use and nothing else. Every surface answers JSON: the interface is the front
// (specs/SPEC-step-7-frontend.md), served by the frontend surface, and no surface draws a page.
//
// ADDING A SURFACE EDITS NO SHIPPED FILE. The surface's directory is linked into the binary
// by the file `go generate` writes in internal/web/surfaces (surfaces_gen.go), a test fails
// when that file is stale, and everything else -- the route table and the catalogue -- is
// built from the registered surfaces at start.
//
// A SURFACE IS SELF-CONTAINED. It depends on this package's exported contract (Surface, Host,
// the JSON helpers) and on nothing in another surface, so the directory can later be loaded
// from outside the binary without recompiling the core.
//
// A BAD SURFACE IS REFUSED, NOT FATAL. A surface whose catalogue is malformed or redefines a
// key another file defines, whose name or route is already taken, or that declares no access
// level, is left out with a named reason (Server.RefusedSurfaces, and the console), and the
// server starts without it.

// MessageKey is a catalogue key. A handler chooses a key, never a string; a JSON answer
// carries its closed code (ConditionOf), which the front translates through the catalogue.
// The keys the core chooses are internal/web/messages'.
type MessageKey = messages.Key

// messageKey is the spelling the core's own files use.
type messageKey = MessageKey

// Surface is everything the server needs to know about one directory of the interface.
type Surface struct {
	// Name is the surface's name, unique across the product. It is the directory's name.
	Name string
	// Files holds locale/<language>.json for each language the surface has strings in,
	// optional. Embed them in the surface's own package.
	Files fs.FS
	// Routes returns the surface's rows of the route table. It is called once, at start,
	// with the host the surface may use.
	Routes func(host Host) []Route
	// Keys returns every catalogue key the surface's code can choose, so a test can assert
	// that each exists. Nil means none.
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
// server was built with, and the few services that must be the same for every surface (the
// form token, the configuration in force, the catalogue). It is deliberately an interface: a
// surface cannot reach the server's route table or its sessions' internals, only what is
// listed here.
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
	// GeoIP is woken by a save of the MaxMind credentials; may be nil.
	GeoIP() GeoIP
	// PublicSuffix answers for the Public Suffix List's copy in use; may be nil.
	PublicSuffix() PublicSuffixList
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
	// CatalogueStrings is the core's, the mounted surfaces' and the plugins' strings of one
	// language, merged: what the front's string catalogue is built from.
	CatalogueStrings(language string) map[string]string

	// FormToken is the form token the request's reader writes with -- the session's own, or the
	// one in the browser's own cookie, set when there is none -- for a JSON answer to carry.
	FormToken(writer http.ResponseWriter, request *http.Request) (string, error)
}

// AllHandlerKeys is every catalogue key the Go code of the core and of every registered
// surface can choose, and every key a built-in plugin carries -- reached through the registry
// rows and the settings its manifest declares, which the loader confines its keys to -- so a
// test can assert that each exists.
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
	for _, key := range dataset.LabelKeys() {
		keys = append(keys, MessageKey(key))
	}
	return keys
}
