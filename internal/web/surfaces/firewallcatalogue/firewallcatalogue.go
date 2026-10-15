package firewallcatalogue

import (
	"embed"
	"net/http"

	"github.com/NaejEL/opnview/internal/broker"
	"github.com/NaejEL/opnview/internal/web"
	"github.com/NaejEL/opnview/internal/widget"
)

// The surface's marker.
//
//go:embed surface.json
var files embed.FS

func init() {
	web.RegisterSurface(web.Surface{
		Name:  "firewallcatalogue",
		Files: files,
		Routes: func(host web.Host) []web.Route {
			return []web.Route{{Method: http.MethodGet, Pattern: PathCatalogue, Access: web.AccessAuthenticated,
				JSON: true, Handler: func(writer http.ResponseWriter, request *http.Request) {
					serve(host, writer, request)
				}}}
		},
	})
}

// PathCatalogue is the catalogue.
const PathCatalogue = "/api/firewall/catalogue"

// failure is a catalogue that could not be read.
type failure struct {
	Condition string `json:"condition"`
}

func serve(host web.Host, writer http.ResponseWriter, request *http.Request) {
	gate := host.Broker()
	if gate == nil {
		widget.WriteJSON(writer, http.StatusOK, broker.Catalogue{Endpoints: []broker.CatalogueEndpoint{},
			Subscriptions: []broker.SubscriptionState{}})
		return
	}
	catalogue, err := gate.Catalogue(request.Context())
	if err != nil {
		if request.Context().Err() != nil {
			return
		}
		widget.WriteJSON(writer, http.StatusInternalServerError, failure{Condition: "read_failed"})
		return
	}
	widget.WriteJSON(writer, http.StatusOK, catalogue)
}
