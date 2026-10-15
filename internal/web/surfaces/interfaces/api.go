package interfaces

import (
	"context"
	"net/http"

	"github.com/NaejEL/opnview/internal/store/flows"
	"github.com/NaejEL/opnview/internal/web"
)

// The Interfaces surface's JSON equivalents (docs/widget-api.md, "Settings API"): per interface
// its networks, with their origin, removal, whether each counts and why not, the overlaps, and the
// link-local rule; and every edit the page's forms make, applied by the function the page uses
// (applyEdit), so the two refuse the same inputs and make the same change.

// The JSON path, for the listing and for an edit.
const PathAPIInterfaceNetworks = "/api/interface-networks"

// apiRoutes are the surface's JSON rows of the route table.
func (s *surface) apiRoutes() []web.Route {
	return []web.Route{
		{Method: http.MethodGet, Pattern: PathAPIInterfaceNetworks, Access: web.AccessAuthenticated, JSON: true,
			Handler: s.handleAPIInterfaceNetworks},
		{Method: http.MethodPost, Pattern: PathAPIInterfaceNetworks, Access: web.AccessAuthenticated, JSON: true,
			Handler: s.handleAPIEdit},
	}
}

// InterfacesAnswer is every figure of the page.
type InterfacesAnswer struct {
	Interfaces []InterfaceState `json:"interfaces"`
}

// InterfaceState is one interface's card.
type InterfaceState struct {
	ID int64 `json:"id"`
	// Description, Identifier and Device are discovered data.
	Description string `json:"description"`
	Identifier  string `json:"identifier"`
	Device      string `json:"device"`
	// Upstream is whether a gateway sits behind the interface; an upstream interface carries no
	// network and takes no edit.
	Upstream bool `json:"upstream"`
	// LinkLocal is the link-local rule.
	LinkLocal bool           `json:"link_local"`
	Networks  []NetworkState `json:"networks"`
	Overlaps  []OverlapState `json:"overlaps"`
}

// NetworkState is one stored network.
type NetworkState struct {
	Network string `json:"network"`
	// Origin is detected or operator.
	Origin  string `json:"origin"`
	Removed bool   `json:"removed"`
	Counts  bool   `json:"counts"`
	// NotCountingReason is removed or no_longer_detected beside a network that does not count,
	// null otherwise.
	NotCountingReason *string `json:"not_counting_reason"`
}

// OverlapState is one counting network of the interface overlapping one of another.
type OverlapState struct {
	Network          string `json:"network"`
	Other            string `json:"other"`
	OtherInterfaceID int64  `json:"other_interface_id"`
	OtherDescription string `json:"other_description"`
}

// interfacesAnswer reads every figure of the page from the reads the page draws from.
func (s *surface) interfacesAnswer(ctx context.Context) (InterfacesAnswer, error) {
	answer := InterfacesAnswer{Interfaces: []InterfaceState{}}
	rows, err := s.store.Interfaces(ctx)
	if err != nil {
		return answer, err
	}
	networks, err := s.store.InterfaceNetworks(ctx)
	if err != nil {
		return answer, err
	}
	descriptions := map[int64]string{}
	for _, row := range rows {
		descriptions[row.ID] = row.Description
	}
	for _, row := range rows {
		state := InterfaceState{ID: row.ID, Description: row.Description, Identifier: row.Identifier,
			Device: row.Device, Upstream: row.IsUpstream, LinkLocal: row.LinkLocalEvidence,
			Networks: []NetworkState{}, Overlaps: []OverlapState{}}
		if !row.IsUpstream {
			for _, network := range networks {
				if network.InterfaceID != row.ID {
					continue
				}
				drawn := NetworkState{Network: network.Network.String(),
					Origin: web.ConditionOf(originKey(network.Origin)), Removed: network.Removed,
					Counts: network.Counts}
				if reason := notCountingReason(network); reason != "" {
					code := web.ConditionOf(reason)
					drawn.NotCountingReason = &code
				}
				state.Networks = append(state.Networks, drawn)
			}
			state.Overlaps = overlapStates(row.ID, networks, descriptions)
		}
		answer.Interfaces = append(answer.Interfaces, state)
	}
	return answer, nil
}

// overlapStates are overlapsOf's rows, with the other interface named by its id.
func overlapStates(interfaceID int64, networks []flows.InterfaceNetwork,
	descriptions map[int64]string) []OverlapState {
	overlaps := []OverlapState{}
	for _, own := range networks {
		if own.InterfaceID != interfaceID || !own.Counts {
			continue
		}
		for _, other := range networks {
			if other.InterfaceID == interfaceID || !other.Counts || !other.Network.Overlaps(own.Network) {
				continue
			}
			overlaps = append(overlaps, OverlapState{Network: own.Network.String(), Other: other.Network.String(),
				OtherInterfaceID: other.InterfaceID, OtherDescription: descriptions[other.InterfaceID]})
		}
	}
	return overlaps
}

func (s *surface) handleAPIInterfaceNetworks(writer http.ResponseWriter, request *http.Request) {
	s.answerInterfaces(writer, request)
}

// answerInterfaces answers with every figure of the page as it now stands.
func (s *surface) answerInterfaces(writer http.ResponseWriter, request *http.Request) {
	answer, err := s.interfacesAnswer(request.Context())
	if err != nil {
		web.FailJSON(writer, request, err)
		return
	}
	web.WriteJSON(writer, http.StatusOK, answer)
}

// handleAPIEdit applies one edit: {interface, action, network, link_local}, the action one of add,
// confirm, remove, restore and link_local, and link_local a boolean.
func (s *surface) handleAPIEdit(writer http.ResponseWriter, request *http.Request) {
	body, ok := web.DecodeJSONObject(request.Body)
	if !ok {
		web.RefuseJSON(writer, http.StatusBadRequest, web.ConditionMalformedBody, "")
		return
	}
	idText, _ := web.JSONText(body, "interface")
	action, _ := web.JSONText(body, "action")
	network, _ := web.JSONText(body, "network")
	linkLocal, _ := web.JSONText(body, "link_local")
	switch linkLocal {
	case "true":
		linkLocal = linkLocalYes
	case "false":
		linkLocal = linkLocalNo
	}
	_, outcome, err := s.applyEdit(request.Context(), idText, action, network, linkLocal)
	switch {
	case err != nil:
		web.FailJSON(writer, request, err)
	case outcome.refusal != "":
		web.RefuseJSON(writer, http.StatusBadRequest, web.ConditionOf(outcome.refusal), refusalField(outcome))
	default:
		s.answerInterfaces(writer, request)
	}
}

// refusalField is the body member a refused edit is about.
func refusalField(outcome editOutcome) string {
	switch {
	case outcome.inCard:
		return "network"
	case outcome.refusal == msgInterfaceUnknown || outcome.refusal == msgInterfaceUpstream:
		return "interface"
	case outcome.refusal == msgLinkLocalInvalid:
		return "link_local"
	case outcome.refusal == msgInterfaceActionUnknown:
		return "action"
	}
	return "network"
}
