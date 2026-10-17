package widget

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"time"

	"github.com/NaejEL/opnview/internal/dataset"
	"github.com/NaejEL/opnview/internal/live"
	"github.com/NaejEL/opnview/internal/store"
	"github.com/NaejEL/opnview/internal/widget/envelope"
	"github.com/NaejEL/opnview/internal/widget/params"
	"github.com/NaejEL/opnview/internal/widget/request"
	"github.com/NaejEL/opnview/internal/widget/script"
)

// PathPrefix is the widget API's path: /api/widget/{type} (decision 3).
const PathPrefix = "/api/widget/"

// ParametersQueryKey is the one query parameter carrying the parameters object as JSON
// (decision 2).
const ParametersQueryKey = "parameters"

// Service serves the registered presets.
type Service struct {
	store   *store.Store
	Now     func() time.Time
	lists   request.SuffixLists
	presets map[string]request.Preset
	types   []string
	// frontOnly are the front-only widgets (renderer.go): listed, never served.
	frontOnly map[string]request.Preset
	refusals  []params.Refusal
	operator  []string
	// datasets is the data catalogue: the core's datasets and the loaded plugins'
	// (internal/widget/datasets/datasets.go).
	datasets *dataset.Catalogue
	// liveInterest is the live-interest lease a request for a live figure renews (live.go).
	liveInterest *live.Interest
}

// Options are what a service needs beside the store.
type Options struct {
	// Now is the clock; nil takes time.Now.
	Now func() time.Time
	// SuffixLists is the Public Suffix List in use, for the registrable-domain grouping;
	// nil reports the grouping unavailable.
	SuffixLists request.SuffixLists
	// OperatorDir is the directory operator widgets are read from, <data-dir>/widgets; empty
	// loads none. A missing directory is not an error. Every widget the loader refuses is
	// reported by Service.Refusals and never stops the service.
	OperatorDir string
	// Datasets is the data catalogue the service answers from; nil is the core's datasets alone.
	Datasets *dataset.Catalogue
	// Plugins are the widgets the loaded plugins carry (source.go): the built-ins' and the data
	// directory's. Nil takes the built-in plugins' alone, as the plugin loader registered them. A
	// refused built-in is an error; a refused third party's widget is reported by Refusals.
	Plugins []Source
	// LiveInterest is the live-interest lease an answer carrying a live figure renews; nil
	// renews none (live.go).
	LiveInterest *live.Interest
}

// New returns the service over a store.
func New(database *store.Store, options Options) (*Service, error) {
	if database == nil {
		return nil, errors.New("widget: a store is required")
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	service := &Service{store: database, Now: now, lists: options.SuffixLists, presets: map[string]request.Preset{},
		liveInterest: options.LiveInterest}
	service.datasets = options.Datasets
	if service.datasets == nil {
		catalogue, err := dataset.NewCatalogue(nil)
		if err != nil {
			return nil, err
		}
		service.datasets = catalogue
	}
	if err := script.HostAreaFailures(); err != nil {
		return nil, err
	}
	sources := options.Plugins
	if sources == nil {
		sources = defaultSources()
	}
	fromPlugins, refused, err := loadPluginWidgets(context.Background(), database, sources, service.presets)
	if err != nil {
		return nil, err
	}
	service.refusals = append(service.refusals, refused...)
	for _, preset := range fromPlugins {
		service.presets[preset.Type] = preset
		if preset.Origin == request.OriginOperator {
			service.operator = append(service.operator, preset.Type)
		}
	}
	if options.OperatorDir != "" {
		loaded, refusals := loadOperatorWidgets(context.Background(), database, options.OperatorDir, service.presets)
		service.refusals = append(service.refusals, refusals...)
		for _, preset := range loaded {
			service.presets[preset.Type] = preset
			service.operator = append(service.operator, preset.Type)
		}
	}
	sort.Strings(service.operator)
	sort.Slice(service.refusals, func(i, j int) bool { return service.refusals[i].Type < service.refusals[j].Type })
	// A front-only widget is known and never served: it leaves the served presets for a map of its
	// own, so no route is built for it and Respond answers it as an unknown type.
	service.frontOnly = map[string]request.Preset{}
	for widgetType, preset := range service.presets {
		if preset.FrontOnly {
			service.frontOnly[widgetType] = preset
			delete(service.presets, widgetType)
			continue
		}
		service.types = append(service.types, widgetType)
	}
	sort.Strings(service.types)
	return service, nil
}

// FrontOnlyTypes returns the front-only widgets that loaded, sorted: each has a renderer and
// neither a query nor a script, and nothing on the server answers it.
func (s *Service) FrontOnlyTypes() []string {
	types := make([]string, 0, len(s.frontOnly))
	for widgetType := range s.frontOnly {
		types = append(types, widgetType)
	}
	sort.Strings(types)
	return types
}

// Known returns the declaration of a loaded widget, served or front-only.
func (s *Service) Known(widgetType string) (request.Preset, bool) {
	if preset, found := s.presets[widgetType]; found {
		return preset, true
	}
	preset, found := s.frontOnly[widgetType]
	return preset, found
}

// Refusals returns the operator widgets the loader refused, each with its named reason. The
// service is running without them.
func (s *Service) Refusals() []params.Refusal {
	return append([]params.Refusal(nil), s.refusals...)
}

// OperatorTypes returns the operator widgets that loaded, sorted.
func (s *Service) OperatorTypes() []string {
	return append([]string(nil), s.operator...)
}

// Types returns every served type, sorted. The HTTP routes are built from it.
func (s *Service) Types() []string {
	return append([]string(nil), s.types...)
}

// Preset returns a served type's declaration.
func (s *Service) Preset(widgetType string) (request.Preset, bool) {
	preset, found := s.presets[widgetType]
	return preset, found
}

// Failure is the JSON a request is refused with: a condition code and, where one
// applies, the reason.
type Failure struct {
	Condition string `json:"condition"`
	Type      string `json:"type,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

// ServeType answers one request for a served type: 200 with the envelope, or 400 with a
// JSON condition when the parameters cannot be parsed at all.
func (s *Service) ServeType(writer http.ResponseWriter, httpRequest *http.Request, widgetType string) {
	response, err := s.Respond(httpRequest.Context(), widgetType, httpRequest.URL.Query().Get(ParametersQueryKey))
	var (
		malformed params.ErrMalformed
		failed    *request.ScriptError
	)
	switch {
	case errors.As(err, &failed):
		// A widget script that threw, ran past its time bound or answered something that is not
		// an answer: its own closed condition, and the detail in the log, never in the answer.
		if httpRequest.Context().Err() != nil {
			return
		}
		script.ScriptLogger(failed.Plugin, failed.Error())
		WriteJSON(writer, http.StatusInternalServerError, Failure{Condition: request.ConditionScriptFailed, Type: widgetType})
	case errors.As(err, &malformed):
		WriteJSON(writer, http.StatusBadRequest, Failure{Condition: envelope.ConditionMalformedParameters,
			Type: widgetType, Reason: malformed.Reason})
	case errors.Is(err, ErrUnknownType):
		s.ServeUnknown(writer, widgetType)
	case err != nil:
		// The error is the store's: a cancelled request or a failed read. Its text names
		// statements and is not shown; the condition says what happened.
		if httpRequest.Context().Err() != nil {
			return
		}
		WriteJSON(writer, http.StatusInternalServerError, Failure{Condition: "read_failed", Type: widgetType})
	default:
		WriteJSON(writer, http.StatusOK, response)
	}
}

// ServeUnknown answers a type with no endpoint: 404, JSON, unknown_widget_type (A16) -- or
// front_only_widget for a front-only widget, which is known and answered by nothing on the server.
func (s *Service) ServeUnknown(writer http.ResponseWriter, widgetType string) {
	if _, frontOnly := s.frontOnly[widgetType]; frontOnly {
		WriteJSON(writer, http.StatusNotFound, Failure{Condition: ConditionFrontOnlyWidget, Type: widgetType})
		return
	}
	WriteJSON(writer, http.StatusNotFound, Failure{Condition: envelope.ConditionUnknownWidgetType, Type: widgetType})
}

// WriteJSON writes a JSON body with its status. It sets no CORS header, deliberately: the
// API answers its own origin only (A13), and is not cached, because every answer is about
// a window ending now.
func WriteJSON(writer http.ResponseWriter, status int, body any) {
	encoded, err := json.Marshal(body)
	if err != nil {
		status = http.StatusInternalServerError
		encoded = []byte(`{"condition":"encoding_failed"}`)
	}
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.WriteHeader(status)
	_, _ = writer.Write(encoded)
}

var ErrUnknownType = errors.New("widget: unknown type")

// Respond computes one response. It is what ServeType writes, and what the response-time
// measurement of the pre-deployment command times.
func (s *Service) Respond(ctx context.Context, widgetType, rawParameters string) (envelope.Response, error) {
	preset, found := s.presets[widgetType]
	if !found {
		return envelope.Response{}, ErrUnknownType
	}
	parsed, err := params.ParseParametersWith(rawParameters, preset.Params, s.datasets)
	if err != nil {
		return envelope.Response{}, err
	}
	widgetRequest := &request.Request{
		Ctx:            ctx,
		Db:             s.store,
		Lists:          s.lists,
		Now:            s.Now().Unix(),
		Preset:         preset,
		Params:         parsed,
		Refs:           map[string][]params.Resolved{},
		Entries:        map[int]map[string]params.Resolved{},
		Failed:         map[int]bool{},
		WithheldSeries: map[int]bool{},
		Datasets:       s.datasets,
		LiveInterest:   s.liveInterest,
		Response: &envelope.Response{
			Type: widgetType, Parameters: parsed.Effective, UnknownParameters: parsed.Unknown,
			InvalidParameters: parsed.Invalid, MissingParameters: parsed.Missing,
			UnresolvedReferences: []params.UnresolvedReference{}, Gaps: []envelope.Gap{},
			Availability: []envelope.ProviderAvailability{}, Freshness: []envelope.Freshness{}, Withheld: []string{},
			States: []string{},
		},
	}
	if widgetRequest.Response.InvalidParameters == nil {
		widgetRequest.Response.InvalidParameters = []params.InvalidValue{}
	}
	if widgetRequest.Response.MissingParameters == nil {
		widgetRequest.Response.MissingParameters = []string{}
	}
	if err := widgetRequest.Prepare(); err != nil {
		return envelope.Response{}, err
	}
	response := widgetRequest.Response

	topUnresolved, topWithheld := false, false
	for _, unresolvedReference := range response.UnresolvedReferences {
		if request.IsSeriesPath(unresolvedReference.Path) {
			continue
		}
		if unresolvedReference.Reason == params.UnresolvedWithheld {
			topWithheld = true
		} else {
			topUnresolved = true
		}
	}
	condition := ""
	switch {
	case topUnresolved:
		condition = envelope.ConditionUnresolvedReference
	case topWithheld:
		condition = envelope.ConditionWithheldByAggregateMode
	case len(response.MissingParameters) > 0:
		condition = envelope.ConditionMissingRequiredParameter
	case preset.WithheldWholeUnderNoDomains && widgetRequest.NoDomains():
		condition = envelope.ConditionWithheldByAggregateMode
		response.Withheld = append(response.Withheld, "data")
	}
	if condition != "" {
		response.Condition = condition
		return *response, nil
	}

	serve := preset.Serve
	if preset.Volumes {
		serve = func(r *request.Request) (any, bool, error) { return r.ServeMeasured(preset.Serve) }
	}
	data, hasRows, err := serve(widgetRequest)
	if errors.Is(err, script.ErrWithheldUnderNoDomains) {
		response.Condition = envelope.ConditionWithheldByAggregateMode
		response.Data = nil
		response.Withheld = append(response.Withheld, "data")
		sort.Strings(response.States)
		sort.Strings(response.Withheld)
		return *response, nil
	}
	if err != nil {
		return envelope.Response{}, err
	}
	response.Condition = widgetRequest.Condition(hasRows)
	if response.Condition != envelope.ConditionReachable && response.Condition != envelope.ConditionReachableNoRows &&
		!hasRows {
		// A degraded source with nothing stored: no data at all, never a zero.
		response.Data = nil
	} else {
		response.Data = data
	}
	if widgetRequest.Override != "" {
		response.Condition = widgetRequest.Override
		if widgetRequest.Override == envelope.ConditionNoOwnerExists {
			response.Data = nil
		}
	}
	sort.Strings(response.States)
	sort.Strings(response.Withheld)
	return *response, nil
}

// SetLiveInterest gives the service the live-interest lease its live figures renew. It is called
// once, before the service serves anything.
func (s *Service) SetLiveInterest(interest *live.Interest) { s.liveInterest = interest }

// LiveInterest is the lease the service's live figures renew; nil when none was given.
func (s *Service) LiveInterest() *live.Interest { return s.liveInterest }

// Store is the database the service reads, for a surface serving a live figure beside it.
func (s *Service) Store() *store.Store { return s.store }
