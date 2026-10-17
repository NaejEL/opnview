package hello

import (
	"embed"
	"net/http"

	"github.com/NaejEL/opnview/internal/web"
)

//go:embed locale surface.json
var files embed.FS

// PathHello is where the answer lives.
const PathHello = "/api/hello"

// The catalogue keys the front uses to name the figure. The words are in locale/en.json.
const (
	msgTitleHello    web.MessageKey = "hello.title"
	msgGreetingHello web.MessageKey = "hello.greeting"
)

func init() {
	web.RegisterSurface(web.Surface{
		Name:  "hello",
		Files: files,
		Routes: func(host web.Host) []web.Route {
			s := &surface{host}
			return []web.Route{
				{Method: http.MethodGet, Pattern: PathHello, Access: web.AccessAuthenticated, JSON: true,
					Handler: s.handle},
			}
		},
		Keys: func() []web.MessageKey { return []web.MessageKey{msgTitleHello, msgGreetingHello} },
	})
}

// surface is the handlers, over what the host grants.
type surface struct{ web.Host }

// Answer is the route's answer: the one figure.
type Answer struct {
	Accounts int `json:"accounts"`
}

// handle answers the figure.
func (s *surface) handle(writer http.ResponseWriter, request *http.Request) {
	count, err := s.Store().CountAccounts(request.Context())
	if err != nil {
		web.FailJSON(writer, request, err)
		return
	}
	web.WriteJSON(writer, http.StatusOK, Answer{Accounts: count})
}
