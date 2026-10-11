package widgetapi

import (
	"net/http"

	"github.com/NaejEL/opnview/internal/web"
	"github.com/NaejEL/opnview/internal/widget"
)

// The widget API's routes (step 5B, specs/SPEC-step-5b-widget-api.md): one authenticated
// GET route per served preset, built from the widget registry, so a preset added there is
// routed here without an edit; and one route for every other type under the same prefix,
// which answers unknown_widget_type in JSON rather than an HTML page. Every one of them
// reads the local database and nothing else, and answers JSON only: an unauthenticated
// request gets a 401 with a JSON condition (decision 4), never a redirect to a page.

// PathWidgetPattern is the pattern of a type the registry does not serve.
const PathWidgetPattern = widget.PathPrefix + "{type}"

// widgetRoutes returns the widget API's rows of the route table.
func (s *surface) widgetRoutes() []web.Route {
	var routes []web.Route
	for _, widgetType := range s.Widgets().Types() {
		routes = append(routes, web.Route{Method: http.MethodGet, Pattern: widget.PathPrefix + widgetType,
			Access: web.AccessAuthenticated, JSON: true, Handler: s.handleWidget(widgetType)})
	}
	routes = append(routes, web.Route{Method: http.MethodGet, Pattern: PathWidgetPattern,
		Access: web.AccessAuthenticated, JSON: true, Handler: s.handleUnknownWidget})
	return routes
}

// handleWidget answers one served type.
func (s *surface) handleWidget(widgetType string) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		s.Widgets().ServeType(writer, request, widgetType)
	}
}

// handleUnknownWidget answers a type with no endpoint.
func (s *surface) handleUnknownWidget(writer http.ResponseWriter, request *http.Request) {
	s.Widgets().ServeUnknown(writer, request.PathValue("type"))
}
