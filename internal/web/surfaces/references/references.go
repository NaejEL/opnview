package references

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/web"
	"github.com/NaejEL/opnview/internal/widget/params"
	"github.com/NaejEL/opnview/internal/widget/request"
)

// The surface's marker.
//
//go:embed surface.json
var files embed.FS

func init() {
	web.RegisterSurface(web.Surface{
		Name:  "references",
		Files: files,
		Routes: func(host web.Host) []web.Route {
			return (&surface{Host: host}).routes()
		},
	})
}

// The paths.
const (
	// PathCandidatesPattern lists the candidates of one reference kind.
	PathCandidatesPattern = "/api/references/{kind}"
	// PathResolve resolves a list of reference objects.
	PathResolve = "/api/references/resolve"
	// QueryFilter is the listing's filter parameter.
	QueryFilter = "q"
	// QueryReferences is the resolution's parameter: a JSON array of reference objects.
	QueryReferences = "references"
)

// MaxResolved is the most reference objects one resolution takes.
const MaxResolved = 200

// The closed codes these routes answer beside the resolver's own.
const (
	// ConditionUnknownKind is a listing for a kind outside the reference vocabulary (404).
	ConditionUnknownKind = "unknown_kind"
	// ConditionMalformedParameters is a resolution whose references parameter is not a JSON array.
	ConditionMalformedParameters = "malformed_parameters"
	// ConditionTooManyReferences is a resolution of more than MaxResolved references.
	ConditionTooManyReferences = "too_many_references"
	// ConditionReadFailed is a read the store could not complete.
	ConditionReadFailed = "read_failed"
)

// The reasons a references parameter is malformed, as the widget API names them.
const (
	reasonNotJSON      = "not_json"
	reasonNotAnArray   = "not_an_array"
	reasonTrailingData = "trailing_data"
)

type surface struct {
	web.Host
}

func (s *surface) routes() []web.Route {
	return []web.Route{
		{Method: http.MethodGet, Pattern: PathResolve, Access: web.AccessAuthenticated, JSON: true,
			Handler: s.handleResolve},
		{Method: http.MethodGet, Pattern: PathCandidatesPattern, Access: web.AccessAuthenticated, JSON: true,
			Handler: s.handleCandidates},
	}
}

// Refusal is a refused request.
type Refusal struct {
	Condition string `json:"condition"`
	Kind      string `json:"kind,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

// CandidatesAnswer is GET /api/references/{kind}.
type CandidatesAnswer struct {
	// Kind is the kind listed, and By its accepted keys, each of which every candidate carries.
	Kind string   `json:"kind"`
	By   []string `json:"by"`
	// Q is the filter applied, and Limit the most candidates answered.
	Q     string `json:"q"`
	Limit int    `json:"limit"`
	// Condition is withheld_by_aggregate_mode for a site under no_domains, with no candidate;
	// absent otherwise.
	Condition  string             `json:"condition,omitempty"`
	Candidates []params.Candidate `json:"candidates"`
	// Remainder is how many candidates the filter keeps past Limit.
	Remainder int `json:"remainder"`
}

// ResolveAnswer is GET /api/references/resolve.
type ResolveAnswer struct {
	// AggregateMode is the mode the references were resolved under.
	AggregateMode string `json:"aggregate_mode"`
	// References are the answers, in the order of the request.
	References []ResolvedReference `json:"references"`
}

// ResolvedReference is one reference object's answer.
type ResolvedReference struct {
	Index int `json:"index"`
	// Kind, By, Value and Label echo the object; under no_domains a site's value and label are
	// removed, as the widget envelope removes them. All four are absent for a value that is not a
	// reference object.
	Kind  string `json:"kind,omitempty"`
	By    string `json:"by,omitempty"`
	Value any    `json:"value,omitempty"`
	Label any    `json:"label,omitempty"`
	// State is resolved, or the reason it is not: no_match, ambiguous, unknown_kind, unknown_by,
	// withheld_by_aggregate_mode, or not_a_reference_object.
	State string `json:"state"`
}

// aggregateMode reads the aggregate mode in force, as a widget request reads it: full when no row
// holds it.
func (s *surface) aggregateMode(httpRequest *http.Request) (string, error) {
	value, present, err := s.Store().Setting(httpRequest.Context(), config.KeyAggregateMode)
	if err != nil {
		return "", err
	}
	if !present {
		return request.AggregateModeFull, nil
	}
	return value, nil
}

func (s *surface) handleCandidates(writer http.ResponseWriter, httpRequest *http.Request) {
	kind := httpRequest.PathValue("kind")
	accepted, known := params.ReferenceByKind[kind]
	if !known {
		web.WriteJSON(writer, http.StatusNotFound, Refusal{Condition: ConditionUnknownKind, Kind: kind})
		return
	}
	mode, err := s.aggregateMode(httpRequest)
	if err != nil {
		web.WriteJSON(writer, http.StatusInternalServerError, Refusal{Condition: ConditionReadFailed})
		return
	}
	answer := CandidatesAnswer{Kind: kind, By: append([]string{}, accepted...),
		Q: httpRequest.URL.Query().Get(QueryFilter), Limit: params.CandidateLimit, Candidates: []params.Candidate{}}
	if kind == "site" && mode == request.AggregateModeNoDomains {
		// A domain name selects nothing under no_domains, and listing the names would disclose them.
		answer.Condition = params.UnresolvedWithheld
		web.WriteJSON(writer, http.StatusOK, answer)
		return
	}
	listed, err := params.Candidates(httpRequest.Context(), s.Store(), kind, answer.Q)
	if err != nil {
		if httpRequest.Context().Err() != nil {
			return
		}
		web.WriteJSON(writer, http.StatusInternalServerError, Refusal{Condition: ConditionReadFailed})
		return
	}
	answer.Candidates, answer.Remainder = listed.Candidates, listed.Remainder
	web.WriteJSON(writer, http.StatusOK, answer)
}

// decodeList reads the references parameter: absent is the empty list.
func decodeList(raw string) ([]json.RawMessage, string) {
	if strings.TrimSpace(raw) == "" {
		return []json.RawMessage{}, ""
	}
	decoder := json.NewDecoder(bytes.NewReader([]byte(raw)))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, reasonNotJSON
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, reasonTrailingData
	}
	if _, isList := value.([]any); !isList {
		return nil, reasonNotAnArray
	}
	var list []json.RawMessage
	if err := json.Unmarshal([]byte(raw), &list); err != nil {
		return nil, reasonNotAnArray
	}
	return list, ""
}

func (s *surface) handleResolve(writer http.ResponseWriter, httpRequest *http.Request) {
	list, reason := decodeList(httpRequest.URL.Query().Get(QueryReferences))
	if reason != "" {
		web.WriteJSON(writer, http.StatusBadRequest, Refusal{Condition: ConditionMalformedParameters, Reason: reason})
		return
	}
	if len(list) > MaxResolved {
		web.WriteJSON(writer, http.StatusBadRequest, Refusal{Condition: ConditionTooManyReferences})
		return
	}
	mode, err := s.aggregateMode(httpRequest)
	if err != nil {
		web.WriteJSON(writer, http.StatusInternalServerError, Refusal{Condition: ConditionReadFailed})
		return
	}
	noDomains := mode == request.AggregateModeNoDomains
	answer := ResolveAnswer{AggregateMode: mode, References: make([]ResolvedReference, 0, len(list))}
	for index, raw := range list {
		reference, state, ok, err := params.ResolveObject(httpRequest.Context(), s.Store(), raw, noDomains)
		if err != nil {
			if httpRequest.Context().Err() != nil {
				return
			}
			web.WriteJSON(writer, http.StatusInternalServerError, Refusal{Condition: ConditionReadFailed})
			return
		}
		entry := ResolvedReference{Index: index, State: state}
		if ok {
			entry.Kind, entry.By, entry.Value, entry.Label = reference.Kind, reference.By, reference.Value, reference.Label
			if noDomains && reference.Kind == "site" {
				// No answer carries a domain name under no_domains, not even the one the request sent.
				entry.Value, entry.Label = nil, nil
			}
		}
		answer.References = append(answer.References, entry)
	}
	web.WriteJSON(writer, http.StatusOK, answer)
}
