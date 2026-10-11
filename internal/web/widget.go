package web

import (
	"errors"
	"net/http"

	"github.com/NaejEL/opnview/internal/auth"
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

// The conditions an unauthenticated API request is refused with (decision 4).
const (
	conditionNoSession      = "no_session"
	conditionSessionExpired = "session_expired"
)

// widgetRoutes returns the widget API's rows of the route table.
func (s *Server) widgetRoutes() []Route {
	var routes []Route
	for _, widgetType := range s.widgets.Types() {
		routes = append(routes, Route{Method: http.MethodGet, Pattern: widget.PathPrefix + widgetType,
			Access: AccessAuthenticated, JSON: true, handler: s.handleWidget(widgetType)})
	}
	routes = append(routes, Route{Method: http.MethodGet, Pattern: PathWidgetPattern,
		Access: AccessAuthenticated, JSON: true, handler: s.handleUnknownWidget})
	return routes
}

// handleWidget answers one served type.
func (s *Server) handleWidget(widgetType string) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		s.widgets.ServeType(writer, request, widgetType)
	}
}

// handleUnknownWidget answers a type with no endpoint.
func (s *Server) handleUnknownWidget(writer http.ResponseWriter, request *http.Request) {
	s.widgets.ServeUnknown(writer, request.PathValue("type"))
}

// refuseUnauthenticatedJSON turns away an API request with no live session: 401, and the
// condition saying whether a session expired or there never was one. It serves no data.
func refuseUnauthenticatedJSON(writer http.ResponseWriter, reason error) {
	condition := conditionNoSession
	if errors.Is(reason, auth.ErrSessionExpired) {
		condition = conditionSessionExpired
	}
	widget.WriteJSON(writer, http.StatusUnauthorized, widget.Failure{Condition: condition})
}
