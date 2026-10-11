// Package datasets mounts the data catalogue (P8 of specs/SPEC-backend-completion-plugins.md,
// docs/widget-api.md, "Data catalogue"). It draws no page: it answers JSON only, for the step-7
// application and for whoever builds a chart on the data.
//
//   - GET /api/datasets lists every dataset the core and every loaded plugin expose, each with its
//     columns, their roles and units, and the aggregations they admit.
//   - GET /api/datasets/{name}/query runs one query against one dataset, carried as the widget
//     API carries a widget's parameters, and answers in its envelope.
//
// No SQL text is accepted from the browser: a query names columns, aggregations, operators and
// values, and internal/dataset compiles it from the dataset's declaration.
package datasets

import (
	"embed"
	"net/http"

	"github.com/NaejEL/opnview/internal/broker"
	"github.com/NaejEL/opnview/internal/dataset"
	"github.com/NaejEL/opnview/internal/web"
	"github.com/NaejEL/opnview/internal/widget"
)

// The surface's marker.
//
//go:embed surface.json locale
var files embed.FS

func init() {
	web.RegisterSurface(web.Surface{
		Name:  "datasets",
		Files: files,
		// The label of every dataset the broker learned of the firewall API.
		Keys: func() []web.MessageKey { return []web.MessageKey{broker.LabelMirrorDataset} },
		Routes: func(host web.Host) []web.Route {
			return (&surface{Host: host}).routes()
		},
	})
}

// The paths.
const (
	// PathDatasets is the listing.
	PathDatasets = "/api/datasets"
	// PathDatasetQueryPattern is one dataset's query.
	PathDatasetQueryPattern = "/api/datasets/{name}/query"
)

// ConditionReadFailed is a query the store could not complete; its detail names statements and is
// not served.
const ConditionReadFailed = "read_failed"

type surface struct {
	web.Host
}

func (s *surface) routes() []web.Route {
	return []web.Route{
		{Method: http.MethodGet, Pattern: PathDatasets, Access: web.AccessAuthenticated, JSON: true,
			Handler: s.handleListing},
		{Method: http.MethodGet, Pattern: PathDatasetQueryPattern, Access: web.AccessAuthenticated, JSON: true,
			Handler: s.handleQuery},
	}
}

// failure is a refused or failed request.
type failure struct {
	Condition string `json:"condition"`
	Dataset   string `json:"dataset,omitempty"`
	Path      string `json:"path,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

func (s *surface) handleListing(writer http.ResponseWriter, request *http.Request) {
	listing, err := s.Widgets().ListDatasets(request.Context())
	if err != nil {
		if request.Context().Err() != nil {
			return
		}
		widget.WriteJSON(writer, http.StatusInternalServerError, failure{Condition: ConditionReadFailed})
		return
	}
	widget.WriteJSON(writer, http.StatusOK, listing)
}

func (s *surface) handleQuery(writer http.ResponseWriter, request *http.Request) {
	name := request.PathValue("name")
	response, refusal, err := s.Widgets().QueryDataset(request.Context(), name,
		request.URL.Query().Get(widget.ParametersQueryKey))
	switch {
	case refusal != nil && refusal.Code == dataset.RefusalUnknownDataset:
		widget.WriteJSON(writer, http.StatusNotFound, failure{Condition: refusal.Code, Dataset: name})
	case refusal != nil:
		widget.WriteJSON(writer, http.StatusBadRequest, failure{Condition: refusal.Code, Dataset: name,
			Path: refusal.Path, Reason: refusal.Reason})
	case err != nil:
		if request.Context().Err() != nil {
			return
		}
		widget.WriteJSON(writer, http.StatusInternalServerError, failure{Condition: ConditionReadFailed,
			Dataset: name})
	default:
		widget.WriteJSON(writer, http.StatusOK, response)
	}
}
