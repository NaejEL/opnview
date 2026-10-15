package interfaces

import (
	"context"
	"errors"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"

	"github.com/NaejEL/opnview/internal/store/flows"
	"github.com/NaejEL/opnview/internal/store/reads"
	"github.com/NaejEL/opnview/internal/web"
	"github.com/NaejEL/opnview/internal/web/settingfields"
)

// The Interfaces surface: per interface, its networks and the link-local rule.
//
// THE TERMS ARE OPNSENSE'S. An interface is what Interfaces > Assignments lists, and
// the range its address and prefix length cover is its NETWORK -- "LAN net" in the
// rule editor (https://docs.opnsense.org/manual/aliases.html). Whether a network was
// detected or set by the operator, and the link-local rule, are opnview's own
// (docs/data-model.md, Vocabulary).
//
// WHICH NETWORKS COUNT IS DECISION D1 (specs/SPEC-step-5a-corrections.md), and the
// surface shows it rather than restating it: each network carries its origin,
// whether it is removed, and whether it counts now, as store.InterfaceNetworks
// computes it. A detected network counts until the operator overrides it, and the
// confirm control beside it does not suggest otherwise: confirming makes it the
// operator's, so discovery can no longer take it away. A network that does not count
// says why in the same cell, in a word: removed, or no longer detected -- the two
// ways D1 leaves a network out, since an operator network that is not removed always
// counts.
//
// OVERLAPS ARE READ ON EVERY DRAW, not carried by one response. Each counting network
// of a card is compared with the counting networks of every other interface, so an
// overlap stays on both cards for as long as it exists, and goes when it is resolved.
//
// A SAVED EDIT REDIRECTS BACK TO ITS OWN CARD. The redirect carries the card's anchor
// and the id of the card the edit was made in, so the browser lands on the card and
// the card says the edit was saved; reloading the page does not repeat the edit. A
// refused edit is answered in place, at 400, and its form posts to the same anchor so
// the refusal is in view.
//
// AN EDIT TAKES EFFECT AT THE COLLECTOR'S NEXT DERIVATION. Every edit changes
// store.OnLinkFingerprint, which the collector compares before each derivation; the
// surface says the edit is saved for that derivation, and never that the
// reclassification already happened.
//
// NOTHING HERE WRITES TO THE FIREWALL OR READS FROM IT. The networks and the rule
// are opnview's own rows, and drawing the page contacts no host.

// The form's field names, carried into the template so the page and the handler
// cannot disagree.
const (
	// fieldNetworkAction names what a form asks for.
	fieldNetworkAction = "action"
	// fieldInterfaceID is the interface's row id.
	fieldInterfaceID = "interface"
	// fieldNetwork is the range typed, or the one a row's control is about.
	fieldNetwork = "network"
	// fieldLinkLocal is the link-local rule, yes or no.
	fieldLinkLocal = "link_local"
)

// querySaved names, in the redirect after a saved edit, the card the edit was made in.
const querySaved = "saved"

// The actions a form on this surface can ask for. Each is one store write.
const (
	actionAddNetwork     = "add"
	actionConfirmNetwork = "confirm"
	actionRemoveNetwork  = "remove"
	actionRestoreNetwork = "restore"
	actionLinkLocal      = "link_local"
)

// The two values of the link-local rule as the form submits them.
const (
	linkLocalYes = "yes"
	linkLocalNo  = "no"
)

// interfaceCard is one interface on the surface.
type interfaceCard struct {
	// ID is the row id, as the forms submit it.
	ID string
	// Description, Identifier and Device are discovered data, drawn as data.
	Description string
	Identifier  string
	Device      string
	// Upstream is whether a gateway sits behind the interface; UpstreamKey says so.
	// An upstream card carries no network and no link-local control: on-link
	// evidence on it is not evidence.
	Upstream    bool
	UpstreamKey messageKey
	// Networks are every stored network of both families, in the order
	// store.InterfaceNetworks returns them.
	Networks []networkRow
	// LinkLocal are the rule's two options, the stored one selected.
	LinkLocal []settingfields.ChoiceOption
	// NoticeKey confirms an edit in this card; Overlaps lists every counting network
	// of this card that overlaps a counting network of another interface.
	NoticeKey messageKey
	Overlaps  []overlapRow
	// RefusalKey is a typed network refused in this card, against the add field,
	// whose value Typed keeps.
	RefusalKey messageKey
	Typed      string
}

// networkRow is one stored network.
type networkRow struct {
	// Network is the range, as data.
	Network string
	// OriginKey and RemovedKey are two of its facts; CountsKey is the third, and
	// ReasonKey, beside a "no", says why it does not count.
	OriginKey  messageKey
	RemovedKey messageKey
	CountsKey  messageKey
	ReasonKey  messageKey
	// The controls the row offers: confirm a detected network not yet confirmed,
	// remove one in place, restore one removed.
	CanConfirm bool
	CanRemove  bool
	CanRestore bool
}

// overlapRow is one network of this card overlapping one of another interface.
type overlapRow struct {
	// Network is this card's network; Other and Description are the other
	// interface's network and that interface's description.
	Network     string
	Other       string
	Description string
}

// interfaceFeedback is what one card has to say.
type interfaceFeedback struct {
	interfaceID int64
	notice      messageKey
	refusal     messageKey
	typed       string
}

// handleInterfacesForm draws the surface. A saved edit lands here, with the card it
// was made in named by the query, and that card says it was saved.
func (s *surface) handleInterfacesForm(writer http.ResponseWriter, request *http.Request) {
	var feedback *interfaceFeedback
	if saved, err := strconv.ParseInt(request.URL.Query().Get(querySaved), 10, 64); err == nil {
		feedback = &interfaceFeedback{interfaceID: saved, notice: msgInterfaceSaved}
	}
	s.renderInterfaces(writer, request, http.StatusOK, feedback, "")
}

// savedLocation is where a saved edit sends the browser: the surface, naming the card
// the edit was made in, at that card's anchor.
func savedLocation(interfaceID int64) string {
	id := strconv.FormatInt(interfaceID, 10)
	return PathInterfaces + "?" + url.Values{querySaved: {id}}.Encode() + "#" + interfaceAnchor(id)
}

// interfaceAnchor is a card's element id, which its forms post to and a saved edit
// returns to.
func interfaceAnchor(id string) string { return "interface_" + id }

// handleInterfacesSubmit applies one edit, and sends the browser back to the card it
// was made in, or redraws the surface with the edit refused against the field that
// caused it.
func (s *surface) handleInterfacesSubmit(writer http.ResponseWriter, request *http.Request) {
	interfaceID, outcome, err := s.applyEdit(request.Context(), request.PostFormValue(fieldInterfaceID),
		request.PostFormValue(fieldNetworkAction), request.PostFormValue(fieldNetwork),
		request.PostFormValue(fieldLinkLocal))
	switch {
	case err != nil:
		s.FailInternal(writer, request, err)
	case outcome.refusal != "" && outcome.inCard:
		s.renderInterfaces(writer, request, http.StatusBadRequest, &interfaceFeedback{
			interfaceID: interfaceID, refusal: outcome.refusal, typed: outcome.typed,
		}, "")
	case outcome.refusal != "":
		s.renderInterfaces(writer, request, http.StatusBadRequest, nil, outcome.refusal)
	default:
		http.Redirect(writer, request, savedLocation(interfaceID), http.StatusSeeOther)
	}
}

// editOutcome is a refused edit: why, and whether the refusal is about what was typed in a card's
// add field (inCard, typed) rather than about the request itself.
type editOutcome struct {
	refusal messageKey
	inCard  bool
	typed   string
}

// applyEdit applies one edit of an interface, as the page's forms and the JSON route both carry
// it: the interface's id, the action, the network the action is about and the link-local rule.
// It returns the interface's id, or a refusal; an error is a failure of the database.
func (s *surface) applyEdit(ctx context.Context, idText, action, typed, linkLocal string) (int64, editOutcome, error) {
	now := s.now().UTC().Unix()
	interfaceID, err := strconv.ParseInt(idText, 10, 64)
	if err != nil {
		// The id is the form's own hidden field: a value that is not one came from
		// somewhere other than a card, and there is no card to say it in.
		return 0, editOutcome{refusal: msgInterfaceUnknown}, nil
	}
	row, found, err := s.interfaceRow(ctx, interfaceID)
	if err != nil {
		return 0, editOutcome{}, err
	}
	if !found {
		return 0, editOutcome{refusal: msgInterfaceUnknown}, nil
	}
	// AN UPSTREAM INTERFACE HAS NO CONTROL ON THIS PAGE, so an edit of one came from
	// somewhere other than its card. It is refused at the page, whichever edit it
	// is: on-link evidence on an upstream interface is not evidence.
	if row.IsUpstream {
		return 0, editOutcome{refusal: msgInterfaceUpstream}, nil
	}

	switch action {
	case actionAddNetwork:
		if _, err := s.store.AddInterfaceNetwork(ctx, interfaceID, typed, now); err != nil {
			if refusal := networkRefusal(typed, err); refusal != "" {
				return interfaceID, editOutcome{refusal: refusal, inCard: true, typed: typed}, nil
			}
			return storeRefusal(interfaceID, err)
		}
	case actionConfirmNetwork, actionRestoreNetwork:
		// Restoring a removed network is confirming it: the store's confirmation
		// makes it the operator's and clears the removal, so it counts again.
		if _, err := s.store.ConfirmInterfaceNetwork(ctx, interfaceID, typed, now); err != nil {
			return storeRefusal(interfaceID, err)
		}
	case actionRemoveNetwork:
		if _, err := s.store.RemoveInterfaceNetwork(ctx, interfaceID, typed, now); err != nil {
			return storeRefusal(interfaceID, err)
		}
	case actionLinkLocal:
		var evidence bool
		switch linkLocal {
		case linkLocalYes:
			evidence = true
		case linkLocalNo:
		default:
			return interfaceID, editOutcome{refusal: msgLinkLocalInvalid}, nil
		}
		if err := s.store.SetLinkLocalEvidence(ctx, interfaceID, evidence); err != nil {
			return storeRefusal(interfaceID, err)
		}
	default:
		return interfaceID, editOutcome{refusal: msgInterfaceActionUnknown}, nil
	}
	return interfaceID, editOutcome{}, nil
}

// storeRefusal names a store refusal, or passes a failure on.
//
// These refusals come from the controls of a row -- confirm, remove, restore, the
// link-local rule -- whose values the page wrote itself, so a refusal means the
// request did not come from the page as drawn, or the row changed in between. There
// is no field to draw it against; the page says it at the top.
func storeRefusal(interfaceID int64, err error) (int64, editOutcome, error) {
	switch {
	case errors.Is(err, flows.ErrUnknownInterface):
		return interfaceID, editOutcome{refusal: msgInterfaceUnknown}, nil
	case errors.Is(err, flows.ErrUpstreamInterface):
		return interfaceID, editOutcome{refusal: msgInterfaceUpstream}, nil
	case errors.Is(err, flows.ErrUnknownNetwork):
		return interfaceID, editOutcome{refusal: msgNetworkUnknown}, nil
	case errors.Is(err, flows.ErrMalformedNetwork), errors.Is(err, flows.ErrLinkLocalNetwork):
		return interfaceID, editOutcome{refusal: msgNetworkMalformed}, nil
	}
	return interfaceID, editOutcome{}, err
}

// networkRefusal names why a typed network was refused, each reason its own
// sentence, or returns the empty key for a refusal that is not about what was
// typed. The store decides; this only chooses the words, and says the more precise
// thing where the typed text shows which of the store's malformed cases it is.
func networkRefusal(typed string, err error) messageKey {
	switch {
	case errors.Is(err, flows.ErrLinkLocalNetwork):
		return msgNetworkLinkLocal
	case errors.Is(err, flows.ErrMalformedNetwork):
		prefix, parseErr := netip.ParsePrefix(strings.TrimSpace(typed))
		switch {
		case parseErr != nil:
			return msgNetworkMalformed
		case prefix.Addr().Is4In6():
			return msgNetworkIPv4Mapped
		case prefix != prefix.Masked():
			return msgNetworkHostBits
		}
		return msgNetworkMalformed
	}
	return ""
}

// interfaceRow reads one interface, and whether it exists.
func (s *surface) interfaceRow(ctx context.Context, id int64) (reads.InterfaceRow, bool, error) {
	rows, err := s.store.Interfaces(ctx)
	if err != nil {
		return reads.InterfaceRow{}, false, err
	}
	for _, row := range rows {
		if row.ID == id {
			return row, true, nil
		}
	}
	return reads.InterfaceRow{}, false, nil
}

// renderInterfaces draws the surface at a status, with one card's feedback or a
// page-level refusal.
func (s *surface) renderInterfaces(writer http.ResponseWriter, request *http.Request, status int,
	feedback *interfaceFeedback, pageRefusal messageKey) {
	built, err := s.newView(writer, request, web.SessionFrom(request), msgTitleInterfaces)
	if err != nil {
		s.FailInternal(writer, request, err)
		return
	}
	if pageRefusal != "" {
		built.Error = pageRefusal
	}
	if err := s.fillInterfaces(request.Context(), &built, feedback); err != nil {
		s.FailInternal(writer, request, err)
		return
	}
	if err := s.Render(writer, status, pageInterfaces, &built); err != nil {
		s.FailInternal(writer, request, err)
	}
}

// fillInterfaces puts one card per interface row into a view.
func (s *surface) fillInterfaces(ctx context.Context, built *view, feedback *interfaceFeedback) error {
	rows, err := s.store.Interfaces(ctx)
	if err != nil {
		return err
	}
	networks, err := s.store.InterfaceNetworks(ctx)
	if err != nil {
		return err
	}
	descriptions := map[int64]string{}
	for _, row := range rows {
		descriptions[row.ID] = row.Description
	}

	built.FieldNetworkAction = fieldNetworkAction
	built.FieldInterfaceID = fieldInterfaceID
	built.FieldNetwork = fieldNetwork
	built.FieldLinkLocal = fieldLinkLocal
	for _, row := range rows {
		card := interfaceCard{
			ID:          strconv.FormatInt(row.ID, 10),
			Description: row.Description,
			Identifier:  row.Identifier,
			Device:      row.Device,
			Upstream:    row.IsUpstream,
			UpstreamKey: yesNo(row.IsUpstream),
			LinkLocal: []settingfields.ChoiceOption{
				{Value: linkLocalNo, LabelKey: msgNo, Selected: !row.LinkLocalEvidence},
				{Value: linkLocalYes, LabelKey: msgYes, Selected: row.LinkLocalEvidence},
			},
		}
		if !row.IsUpstream {
			for _, network := range networks {
				if network.InterfaceID != row.ID {
					continue
				}
				card.Networks = append(card.Networks, networkRow{
					Network:    network.Network.String(),
					OriginKey:  originKey(network.Origin),
					RemovedKey: yesNo(network.Removed),
					CountsKey:  yesNo(network.Counts),
					ReasonKey:  notCountingReason(network),
					CanConfirm: !network.Removed && network.Origin == flows.NetworkOriginDetected,
					CanRemove:  !network.Removed,
					CanRestore: network.Removed,
				})
			}
			card.Overlaps = overlapsOf(row.ID, networks, descriptions)
		}
		if feedback != nil && feedback.interfaceID == row.ID {
			card.NoticeKey = feedback.notice
			card.RefusalKey = feedback.refusal
			card.Typed = feedback.typed
		}
		built.Interfaces = append(built.Interfaces, card)
	}
	return nil
}

// notCountingReason says why a network does not count now, or nothing when it does.
// D1 leaves a network out in two ways: the operator removed it, or it is a detection
// discovery no longer reports. An operator network that is not removed always counts.
func notCountingReason(network flows.InterfaceNetwork) messageKey {
	switch {
	case network.Counts:
		return ""
	case network.Removed:
		return msgCountsNoRemoved
	case network.Origin == flows.NetworkOriginDetected:
		return msgCountsNoLongerDetected
	default:
		return ""
	}
}

// overlapsOf lists every counting network of one interface that overlaps a counting
// network of another, in the order the store returns them. It is the same comparison
// the store reports after an edit (store.NetworkEdit.Overlaps), made for every
// counting network of the card on every draw, so an overlap is shown for as long as it
// exists rather than on the one response that created it.
func overlapsOf(interfaceID int64, networks []flows.InterfaceNetwork,
	descriptions map[int64]string) []overlapRow {
	var overlaps []overlapRow
	for _, own := range networks {
		if own.InterfaceID != interfaceID || !own.Counts {
			continue
		}
		for _, other := range networks {
			if other.InterfaceID == interfaceID || !other.Counts || !other.Network.Overlaps(own.Network) {
				continue
			}
			overlaps = append(overlaps, overlapRow{
				Network:     own.Network.String(),
				Other:       other.Network.String(),
				Description: descriptions[other.InterfaceID],
			})
		}
	}
	return overlaps
}

// originKey names an interface network's origin.
func originKey(origin string) messageKey {
	if origin == flows.NetworkOriginOperator {
		return msgOriginOperator
	}
	return msgOriginDetected
}

// yesNo names a stored boolean.
func yesNo(value bool) messageKey {
	if value {
		return msgYes
	}
	return msgNo
}
