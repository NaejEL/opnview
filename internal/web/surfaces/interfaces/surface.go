// Package interfaces is the Interfaces surface: per interface, its networks and the
// link-local rule. It writes opnview's own interface_network and interface rows, and
// nothing on the firewall.
package interfaces

import (
	"embed"
	"net/http"
	"time"

	"github.com/NaejEL/opnview/internal/store"
	"github.com/NaejEL/opnview/internal/web"
)

// The surface's files: its page template, its catalogue file and its marker.
//
//go:embed template locale surface.json
var files embed.FS

type messageKey = web.MessageKey

// PathInterfaces is where the operator sets an interface's networks and its link-local
// rule. It writes opnview's own rows and nothing on the firewall.
const PathInterfaces = "/interfaces"

// pageInterfaces is the surface's one page.
const pageInterfaces = "interfaces"

func init() {
	web.RegisterSurface(web.Surface{
		Name:  "interfaces",
		Files: files,
		Pages: []web.Page{{Name: pageInterfaces, TitleKey: msgTitleInterfaces, Refusal: refusalView}},
		Routes: func(host web.Host) []web.Route {
			s := newSurface(host)
			return append([]web.Route{
				// The Interfaces surface writes opnview's own interface_network and interface
				// rows, and nothing on the firewall.
				{Method: http.MethodGet, Pattern: PathInterfaces, Access: web.AccessAuthenticated,
					Handler: s.handleInterfacesForm},
				{Method: http.MethodPost, Pattern: PathInterfaces, Access: web.AccessAuthenticated,
					Handler: s.handleInterfacesSubmit},
			}, s.apiRoutes()...)
		},
		Tab:  &web.Tab{Page: pageInterfaces, Path: PathInterfaces, TitleKey: msgTitleInterfaces, Order: 30},
		Keys: func() []web.MessageKey { return keys() },
	})
}

// surface is the surface's handlers over what the host grants.
type surface struct {
	web.Host
	store *store.Store
	now   func() time.Time
}

// newSurface binds the handlers to a host.
func newSurface(host web.Host) *surface {
	return &surface{Host: host, store: host.Store(), now: host.Now}
}

// view is everything the surface's template may read.
type view struct {
	web.Base
	// InterfacesPath is where the surface lives, for each card's forms.
	InterfacesPath string
	// Interfaces are the surface's cards, one per interface row, and the Field* names are
	// its forms' field names.
	Interfaces         []interfaceCard
	FieldNetworkAction string
	FieldInterfaceID   string
	FieldNetwork       string
	FieldLinkLocal     string
}

// newView returns a view with everything common to every page filled in.
func (s *surface) newView(writer http.ResponseWriter, request *http.Request,
	session *store.Session, title messageKey) (view, error) {
	base, err := s.NewBase(writer, request, session, title)
	if err != nil {
		return view{}, err
	}
	return view{Base: base, InterfacesPath: PathInterfaces}, nil
}

// refusalView is the data of the page when the core draws it with a refusal on it.
func refusalView(host web.Host, request *http.Request, base web.Base) (any, error) {
	built := view{Base: base, InterfacesPath: PathInterfaces}
	if err := newSurface(host).fillInterfaces(request.Context(), &built, nil); err != nil {
		return nil, err
	}
	return &built, nil
}
