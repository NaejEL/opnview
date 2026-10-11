// Package labels mounts the JSON API of the labels the user gives interfaces and the purposes the
// user gives blocklists (C1 of the backend-completion cycle, docs/widget-api.md, "Write endpoints",
// *Labels and purposes*). It draws no page and carries no catalogue: it answers JSON only, for the
// step-7 application.
//
// Both are user input that nothing infers: a label is never derived from a description, a purpose
// never from a list's name (docs/data-model.md, "interface", "blocklist"). Discovery never writes a
// label and the purge never removes a purpose. Every route is authenticated; every write is a JSON
// POST the middleware asks for the session's form token, and no GET changes anything.
package labels

import (
	"embed"
	"errors"
	"net/http"
	"strconv"

	"github.com/NaejEL/opnview/internal/store"
	"github.com/NaejEL/opnview/internal/web"
)

// The surface's marker.
//
//go:embed surface.json
var files embed.FS

func init() {
	web.RegisterSurface(web.Surface{
		Name:  "labels",
		Files: files,
		Routes: func(host web.Host) []web.Route {
			return (&surface{Host: host}).routes()
		},
	})
}

// The paths.
const (
	// PathInterfaceLabelPattern sets or clears one interface's label.
	PathInterfaceLabelPattern = "/api/interfaces/{id}/label"
	// PathBlocklists lists every blocklist with its purpose, and the purpose vocabulary.
	PathBlocklists = "/api/blocklists"
	// PathBlocklistPurposePattern sets or clears one blocklist's purpose.
	PathBlocklistPurposePattern = "/api/blocklists/{id}/purpose"
)

// surface is the surface's handlers over what the host grants.
type surface struct {
	web.Host
}

func (s *surface) routes() []web.Route {
	route := func(method, pattern string, handler http.HandlerFunc) web.Route {
		return web.Route{Method: method, Pattern: pattern, Access: web.AccessAuthenticated, JSON: true,
			Handler: handler}
	}
	return []web.Route{
		route(http.MethodPost, PathInterfaceLabelPattern, s.handleLabel),
		route(http.MethodGet, PathBlocklists, s.handleBlocklists),
		route(http.MethodPost, PathBlocklistPurposePattern, s.handlePurpose),
	}
}

// Refusal is a refused request's closed code.
type Refusal struct {
	Condition string `json:"condition"`
}

// Interface is an interface as a label write leaves it.
type Interface struct {
	ID          int64   `json:"id"`
	Identifier  string  `json:"identifier"`
	Description string  `json:"description"`
	UserLabel   *string `json:"user_label"`
}

// InterfaceAnswer is a label write's answer.
type InterfaceAnswer struct {
	Interface Interface `json:"interface"`
}

// Blocklist is one list with its purpose.
type Blocklist struct {
	ID                int64   `json:"id"`
	Name              string  `json:"name"`
	Purpose           *string `json:"purpose"`
	PurposeAssignedAt *int64  `json:"purpose_assigned_at"`
	FirstSeenAt       int64   `json:"first_seen_at"`
	LastSeenAt        int64   `json:"last_seen_at"`
}

// BlocklistsAnswer is the listing: every list, and the closed vocabulary a purpose is chosen from.
type BlocklistsAnswer struct {
	Blocklists []Blocklist `json:"blocklists"`
	Purposes   []string    `json:"purposes"`
}

// BlocklistAnswer is a purpose write's answer.
type BlocklistAnswer struct {
	Blocklist Blocklist `json:"blocklist"`
}

func blocklistOf(list store.Blocklist) Blocklist {
	return Blocklist{ID: list.ID, Name: list.Name, Purpose: list.Purpose, PurposeAssignedAt: list.PurposeAssignedAt,
		FirstSeenAt: list.FirstSeenAt, LastSeenAt: list.LastSeenAt}
}

// refuse answers a store refusal with its code: 404 for a row named by the path that does not
// exist, 400 otherwise; any other failure is a 500 whose detail goes to the console.
func refuse(writer http.ResponseWriter, request *http.Request, err error) {
	var refusal *store.UserInputRefusal
	if !errors.As(err, &refusal) {
		web.FailJSON(writer, request, err)
		return
	}
	status := http.StatusBadRequest
	if refusal.Code == store.RefusalUnknownInterface || refusal.Code == store.RefusalUnknownBlocklist {
		status = http.StatusNotFound
	}
	web.WriteJSON(writer, status, Refusal{Condition: refusal.Code})
}

// pathID reads the {id} of the path; a value that is not a positive whole number names nothing.
func pathID(writer http.ResponseWriter, request *http.Request, unknown string) (int64, bool) {
	id, err := strconv.ParseInt(request.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		web.WriteJSON(writer, http.StatusNotFound, Refusal{Condition: unknown})
		return 0, false
	}
	return id, true
}

// nullableText reads a body that is one object holding exactly one key, whose value is a string
// or null.
func nullableText(writer http.ResponseWriter, request *http.Request, key string) (*string, bool) {
	decoded, ok := web.DecodeJSONObject(request.Body)
	if ok && len(decoded) == 1 {
		value, present := decoded[key]
		if present && value == nil {
			return nil, true
		}
		if text, isText := value.(string); present && isText {
			return &text, true
		}
	}
	web.WriteJSON(writer, http.StatusBadRequest, Refusal{Condition: web.ConditionMalformedBody})
	return nil, false
}

func (s *surface) handleLabel(writer http.ResponseWriter, request *http.Request) {
	id, ok := pathID(writer, request, store.RefusalUnknownInterface)
	if !ok {
		return
	}
	label, ok := nullableText(writer, request, "user_label")
	if !ok {
		return
	}
	labelled, err := s.Store().SetInterfaceUserLabel(request.Context(), id, label)
	if err != nil {
		refuse(writer, request, err)
		return
	}
	web.WriteJSON(writer, http.StatusOK, InterfaceAnswer{Interface: Interface{ID: labelled.ID,
		Identifier: labelled.Identifier, Description: labelled.Description, UserLabel: labelled.UserLabel}})
}

func (s *surface) handleBlocklists(writer http.ResponseWriter, request *http.Request) {
	lists, err := s.Store().Blocklists(request.Context())
	if err != nil {
		web.FailJSON(writer, request, err)
		return
	}
	answer := BlocklistsAnswer{Blocklists: make([]Blocklist, 0, len(lists)),
		Purposes: append([]string(nil), store.BlocklistPurposes...)}
	for _, list := range lists {
		answer.Blocklists = append(answer.Blocklists, blocklistOf(list))
	}
	web.WriteJSON(writer, http.StatusOK, answer)
}

func (s *surface) handlePurpose(writer http.ResponseWriter, request *http.Request) {
	id, ok := pathID(writer, request, store.RefusalUnknownBlocklist)
	if !ok {
		return
	}
	purpose, ok := nullableText(writer, request, "purpose")
	if !ok {
		return
	}
	list, err := s.Store().SetBlocklistPurpose(request.Context(), id, purpose, s.Now().Unix())
	if err != nil {
		refuse(writer, request, err)
		return
	}
	web.WriteJSON(writer, http.StatusOK, BlocklistAnswer{Blocklist: blocklistOf(list)})
}
