package people

import (
	"embed"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/NaejEL/opnview/internal/store/owners"
	"github.com/NaejEL/opnview/internal/web"
)

// The surface's marker.
//
//go:embed surface.json
var files embed.FS

func init() {
	web.RegisterSurface(web.Surface{
		Name:  "people",
		Files: files,
		Routes: func(host web.Host) []web.Route {
			return (&surface{Host: host}).routes()
		},
	})
}

// The paths.
const (
	// PathOwners lists the people (GET) and creates one (POST).
	PathOwners = "/api/owners"
	// PathOwnerRenamePattern renames one person.
	PathOwnerRenamePattern = "/api/owners/{id}/rename"
	// PathOwnerDeletePattern deletes one person and answers the assignments it cleared.
	PathOwnerDeletePattern = "/api/owners/{id}/delete"
	// PathOwnerRestore undoes a deletion.
	PathOwnerRestore = "/api/owners/restore"
	// PathOwnerAssignPattern assigns clients to one person.
	PathOwnerAssignPattern = "/api/owners/{id}/assign"
	// PathClients lists every client.
	PathClients = "/api/clients"
	// PathClientsUnassign unassigns clients.
	PathClientsUnassign = "/api/clients/unassign"
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
		route(http.MethodGet, PathOwners, s.handleOwners),
		route(http.MethodPost, PathOwners, s.handleCreate),
		route(http.MethodPost, PathOwnerRenamePattern, s.handleRename),
		route(http.MethodPost, PathOwnerDeletePattern, s.handleDelete),
		route(http.MethodPost, PathOwnerRestore, s.handleRestore),
		route(http.MethodPost, PathOwnerAssignPattern, s.handleAssign),
		route(http.MethodGet, PathClients, s.handleClients),
		route(http.MethodPost, PathClientsUnassign, s.handleUnassign),
	}
}

// Refusal is a refused request: its closed code, and the client it is about when one client is
// the reason.
type Refusal struct {
	Condition string `json:"condition"`
	Client    int64  `json:"client,omitempty"`
}

// Owner is one person as the API answers it.
type Owner struct {
	ID          int64  `json:"id"`
	DisplayName string `json:"display_name"`
	CreatedAt   int64  `json:"created_at"`
	UpdatedAt   int64  `json:"updated_at"`
	ClientCount int64  `json:"client_count"`
}

// OwnersAnswer is the listing of the people.
type OwnersAnswer struct {
	Owners []Owner `json:"owners"`
}

// OwnerAnswer is one person, created or renamed.
type OwnerAnswer struct {
	Owner Owner `json:"owner"`
}

// Assignment is one machine's assignment: the client and when it was assigned.
type Assignment struct {
	Client          int64 `json:"client"`
	OwnerAssignedAt int64 `json:"owner_assigned_at"`
}

// DeletedAnswer is a deletion: the person and every assignment it cleared, which is exactly the
// body an undo takes back (RestoreRequest).
type DeletedAnswer struct {
	ID          int64        `json:"id"`
	DisplayName string       `json:"display_name"`
	Cleared     []Assignment `json:"cleared"`
}

// NotRestored is one assignment an undo left as it is, and why: assigned_elsewhere, naming the
// person it is assigned to now, or client_not_found.
type NotRestored struct {
	Client  int64  `json:"client"`
	Reason  string `json:"reason"`
	OwnerID *int64 `json:"owner_id"`
}

// RestoredAnswer is an undone deletion.
type RestoredAnswer struct {
	Owner       Owner         `json:"owner"`
	Restored    []Assignment  `json:"restored"`
	NotRestored []NotRestored `json:"not_restored"`
}

// AssignmentChange is one client an assignment named, as it now stands; changed is false when it
// already stood as asked.
type AssignmentChange struct {
	Client          int64  `json:"client"`
	OwnerID         *int64 `json:"owner_id"`
	OwnerAssignedAt *int64 `json:"owner_assigned_at"`
	Changed         bool   `json:"changed"`
}

// AssignmentsAnswer is an assignment or an unassignment.
type AssignmentsAnswer struct {
	Assignments []AssignmentChange `json:"assignments"`
}

// ClientInterface is the interface a client was seen behind.
type ClientInterface struct {
	ID          int64   `json:"id"`
	Identifier  *string `json:"identifier"`
	Description *string `json:"description"`
	UserLabel   *string `json:"user_label"`
}

// ClientOwner is the person a client is assigned to.
type ClientOwner struct {
	ID          int64   `json:"id"`
	DisplayName *string `json:"display_name"`
}

// Client is one machine of the listing.
type Client struct {
	ID               int64            `json:"id"`
	IdentityKind     string           `json:"identity_kind"`
	IdentityKey      string           `json:"identity_key"`
	Hostname         *string          `json:"hostname"`
	MAC              *string          `json:"mac"`
	LastAddress      *string          `json:"last_address"`
	Interface        *ClientInterface `json:"interface"`
	UnstableIdentity bool             `json:"unstable_identity"`
	Owner            *ClientOwner     `json:"owner"`
	OwnerAssignedAt  *int64           `json:"owner_assigned_at"`
	FirstSeenAt      int64            `json:"first_seen_at"`
	LastSeenAt       int64            `json:"last_seen_at"`
}

// ClientsAnswer is the listing of every client, with or without traffic.
type ClientsAnswer struct {
	Clients []Client `json:"clients"`
}

func ownerOf(owner owners.Owner) Owner {
	return Owner{ID: owner.ID, DisplayName: owner.DisplayName, CreatedAt: owner.CreatedAt, UpdatedAt: owner.UpdatedAt,
		ClientCount: owner.ClientCount}
}

// refuse answers a store refusal with its code: 404 for a person named by the path that does not
// exist, 400 otherwise. Any other failure is a 500 whose detail goes to the console.
func refuse(writer http.ResponseWriter, request *http.Request, err error) {
	var refusal *owners.UserInputRefusal
	if !errors.As(err, &refusal) {
		web.FailJSON(writer, request, err)
		return
	}
	status := http.StatusBadRequest
	if refusal.Code == owners.RefusalUnknownOwner {
		status = http.StatusNotFound
	}
	web.WriteJSON(writer, status, Refusal{Condition: refusal.Code, Client: refusal.ClientID})
}

// malformed answers a body that does not have the shape the route takes.
func malformed(writer http.ResponseWriter) {
	web.WriteJSON(writer, http.StatusBadRequest, Refusal{Condition: web.ConditionMalformedBody})
}

// pathID reads the {id} of the path; a value that is not a positive whole number names nobody.
func pathID(writer http.ResponseWriter, request *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(request.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		web.WriteJSON(writer, http.StatusNotFound, Refusal{Condition: owners.RefusalUnknownOwner})
		return 0, false
	}
	return id, true
}

// object reads the body as one JSON object holding exactly the keys allowed.
func object(writer http.ResponseWriter, request *http.Request, allowed ...string) (map[string]any, bool) {
	decoded, ok := web.DecodeJSONObject(request.Body)
	if !ok {
		malformed(writer)
		return nil, false
	}
	for key := range decoded {
		known := false
		for _, candidate := range allowed {
			known = known || candidate == key
		}
		if !known {
			malformed(writer)
			return nil, false
		}
	}
	return decoded, true
}

// text reads a member that must be a string.
func text(decoded map[string]any, key string) (string, bool) {
	value, ok := decoded[key].(string)
	return value, ok
}

// integer reads a value that must be a whole number.
func integer(value any) (int64, bool) {
	number, ok := value.(json.Number)
	if !ok {
		return 0, false
	}
	parsed, err := strconv.ParseInt(number.String(), 10, 64)
	return parsed, err == nil
}

// clientList reads the "clients" member: a list of client ids.
func clientList(decoded map[string]any) ([]int64, bool) {
	listed, ok := decoded["clients"].([]any)
	if !ok {
		return nil, false
	}
	ids := make([]int64, 0, len(listed))
	for _, entry := range listed {
		id, ok := integer(entry)
		if !ok {
			return nil, false
		}
		ids = append(ids, id)
	}
	return ids, true
}

func (s *surface) handleOwners(writer http.ResponseWriter, request *http.Request) {
	owners, err := s.Store().Owners(request.Context())
	if err != nil {
		web.FailJSON(writer, request, err)
		return
	}
	answer := OwnersAnswer{Owners: make([]Owner, 0, len(owners))}
	for _, owner := range owners {
		answer.Owners = append(answer.Owners, ownerOf(owner))
	}
	web.WriteJSON(writer, http.StatusOK, answer)
}

func (s *surface) handleCreate(writer http.ResponseWriter, request *http.Request) {
	decoded, ok := object(writer, request, "display_name")
	if !ok {
		return
	}
	name, ok := text(decoded, "display_name")
	if !ok {
		malformed(writer)
		return
	}
	created, err := s.Store().CreateOwner(request.Context(), name, s.Now().Unix())
	if err != nil {
		refuse(writer, request, err)
		return
	}
	web.WriteJSON(writer, http.StatusOK, OwnerAnswer{Owner: ownerOf(created)})
}

func (s *surface) handleRename(writer http.ResponseWriter, request *http.Request) {
	id, ok := pathID(writer, request)
	if !ok {
		return
	}
	decoded, ok := object(writer, request, "display_name")
	if !ok {
		return
	}
	name, ok := text(decoded, "display_name")
	if !ok {
		malformed(writer)
		return
	}
	renamed, err := s.Store().RenameOwner(request.Context(), id, name, s.Now().Unix())
	if err != nil {
		refuse(writer, request, err)
		return
	}
	web.WriteJSON(writer, http.StatusOK, OwnerAnswer{Owner: ownerOf(renamed)})
}

func (s *surface) handleDelete(writer http.ResponseWriter, request *http.Request) {
	id, ok := pathID(writer, request)
	if !ok {
		return
	}
	if _, ok := object(writer, request); !ok {
		return
	}
	deleted, err := s.Store().DeleteOwner(request.Context(), id, s.Now().Unix())
	if err != nil {
		refuse(writer, request, err)
		return
	}
	answer := DeletedAnswer{ID: deleted.ID, DisplayName: deleted.DisplayName, Cleared: []Assignment{}}
	for _, cleared := range deleted.Cleared {
		answer.Cleared = append(answer.Cleared, Assignment{Client: cleared.ClientID,
			OwnerAssignedAt: cleared.OwnerAssignedAt})
	}
	web.WriteJSON(writer, http.StatusOK, answer)
}

func (s *surface) handleRestore(writer http.ResponseWriter, request *http.Request) {
	decoded, ok := object(writer, request, "display_name", "cleared")
	if !ok {
		return
	}
	name, ok := text(decoded, "display_name")
	listed, isList := decoded["cleared"].([]any)
	if !ok || !isList {
		malformed(writer)
		return
	}
	assignments := make([]owners.Assignment, 0, len(listed))
	for _, entry := range listed {
		member, isObject := entry.(map[string]any)
		if !isObject || len(member) != 2 {
			malformed(writer)
			return
		}
		client, clientOK := integer(member["client"])
		at, atOK := integer(member["owner_assigned_at"])
		if !clientOK || !atOK {
			malformed(writer)
			return
		}
		assignments = append(assignments, owners.Assignment{ClientID: client, OwnerAssignedAt: at})
	}
	restored, err := s.Store().RestoreOwner(request.Context(), name, assignments, s.Now().Unix())
	if err != nil {
		refuse(writer, request, err)
		return
	}
	answer := RestoredAnswer{Owner: ownerOf(restored.Owner), Restored: []Assignment{}, NotRestored: []NotRestored{}}
	for _, assignment := range restored.Restored {
		answer.Restored = append(answer.Restored, Assignment{Client: assignment.ClientID,
			OwnerAssignedAt: assignment.OwnerAssignedAt})
	}
	for _, left := range restored.NotRestored {
		answer.NotRestored = append(answer.NotRestored, NotRestored{Client: left.ClientID, Reason: left.Reason,
			OwnerID: left.OwnerID})
	}
	web.WriteJSON(writer, http.StatusOK, answer)
}

func (s *surface) handleAssign(writer http.ResponseWriter, request *http.Request) {
	id, ok := pathID(writer, request)
	if !ok {
		return
	}
	decoded, ok := object(writer, request, "clients")
	if !ok {
		return
	}
	clients, ok := clientList(decoded)
	if !ok {
		malformed(writer)
		return
	}
	changes, err := s.Store().AssignClients(request.Context(), id, clients, s.Now().Unix())
	if err != nil {
		refuse(writer, request, err)
		return
	}
	web.WriteJSON(writer, http.StatusOK, assignmentsAnswer(changes))
}

func (s *surface) handleUnassign(writer http.ResponseWriter, request *http.Request) {
	decoded, ok := object(writer, request, "clients")
	if !ok {
		return
	}
	clients, ok := clientList(decoded)
	if !ok {
		malformed(writer)
		return
	}
	changes, err := s.Store().UnassignClients(request.Context(), clients, s.Now().Unix())
	if err != nil {
		refuse(writer, request, err)
		return
	}
	web.WriteJSON(writer, http.StatusOK, assignmentsAnswer(changes))
}

func assignmentsAnswer(changes []owners.AssignmentChange) AssignmentsAnswer {
	answer := AssignmentsAnswer{Assignments: make([]AssignmentChange, 0, len(changes))}
	for _, change := range changes {
		answer.Assignments = append(answer.Assignments, AssignmentChange{Client: change.ClientID,
			OwnerID: change.OwnerID, OwnerAssignedAt: change.OwnerAssignedAt, Changed: change.Changed})
	}
	return answer
}

func (s *surface) handleClients(writer http.ResponseWriter, request *http.Request) {
	clients, err := s.Store().Clients(request.Context())
	if err != nil {
		web.FailJSON(writer, request, err)
		return
	}
	answer := ClientsAnswer{Clients: make([]Client, 0, len(clients))}
	for _, client := range clients {
		listed := Client{ID: client.ID, IdentityKind: client.IdentityKind, IdentityKey: client.IdentityKey,
			Hostname: client.Hostname, MAC: client.MAC, LastAddress: client.LastAddress,
			UnstableIdentity: client.UnstableIdentity, OwnerAssignedAt: client.OwnerAssignedAt,
			FirstSeenAt: client.FirstSeenAt, LastSeenAt: client.LastSeenAt}
		if client.InterfaceID != nil {
			listed.Interface = &ClientInterface{ID: *client.InterfaceID, Identifier: client.InterfaceIdentifier,
				Description: client.InterfaceDescription, UserLabel: client.InterfaceUserLabel}
		}
		if client.OwnerID != nil {
			listed.Owner = &ClientOwner{ID: *client.OwnerID, DisplayName: client.OwnerDisplayName}
		}
		answer.Clients = append(answer.Clients, listed)
	}
	web.WriteJSON(writer, http.StatusOK, answer)
}
