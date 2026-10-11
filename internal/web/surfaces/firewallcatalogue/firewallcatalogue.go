// Package firewallcatalogue mounts the learned API catalogue (A15 of the broker amendment to
// specs/SPEC-backend-completion-plugins.md, docs/widget-api.md, "API catalogue"): what the firewall
// broker learned of every endpoint it called -- parameters and their roles, the nature, key, cursor
// and schema versions of each datum, call costs, floors, streams, readers and counters. It never
// carries a field value, a credential, the base URL or an authorisation header. It draws no page.
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
