// Package hello is the worked example of docs/extending.md, "Adding a surface": one page,
// /hello, that says how many accounts the installation has. It is a complete surface -- a
// marker, a template, a catalogue file and a handler -- and it is exercised by
// internal/web/modularity_test.go, which imports it for its effect exactly as
// internal/web/surfaces imports the shipped ones. It lives here, and not below
// internal/web/surfaces, so that it is tested but never linked into the product.
package hello

import (
	"embed"
	"net/http"
	"strconv"

	"github.com/NaejEL/opnview/internal/web"
)

//go:embed template locale surface.json
var files embed.FS

// PathHello is where the page lives.
const PathHello = "/hello"

// The page and the catalogue keys its code chooses. The words are in locale/en.json.
const (
	pageHello                    = "hello"
	msgTitleHello web.MessageKey = "hello.title"
)

func init() {
	web.RegisterSurface(web.Surface{
		Name:  "hello",
		Files: files,
		Pages: []web.Page{{Name: pageHello, TitleKey: msgTitleHello}},
		Routes: func(host web.Host) []web.Route {
			s := &surface{host}
			return []web.Route{
				{Method: http.MethodGet, Pattern: PathHello, Access: web.AccessAuthenticated, Handler: s.handle},
			}
		},
		Tab:  &web.Tab{Page: pageHello, Path: PathHello, TitleKey: msgTitleHello, Order: 90},
		Keys: func() []web.MessageKey { return []web.MessageKey{msgTitleHello} },
	})
}

// surface is the handlers, over what the host grants.
type surface struct{ web.Host }

// view is everything the template may read: the common part, and the one figure.
type view struct {
	web.Base
	Accounts string
}

// handle draws the page.
func (s *surface) handle(writer http.ResponseWriter, request *http.Request) {
	base, err := s.NewBase(writer, request, web.SessionFrom(request), msgTitleHello)
	if err != nil {
		s.FailInternal(writer, request, err)
		return
	}
	count, err := s.Store().CountAccounts(request.Context())
	if err != nil {
		s.FailInternal(writer, request, err)
		return
	}
	built := view{Base: base, Accounts: strconv.Itoa(count)}
	if err := s.Render(writer, http.StatusOK, pageHello, &built); err != nil {
		s.FailInternal(writer, request, err)
	}
}
