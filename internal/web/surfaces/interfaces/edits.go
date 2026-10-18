package interfaces

import (
	"context"
	"errors"
	"net/netip"
	"strconv"
	"strings"

	"github.com/NaejEL/opnview/internal/store/flows"
	"github.com/NaejEL/opnview/internal/store/reads"
)

// The Interfaces surface: per interface, its networks and the link-local rule, in JSON (api.go).
//
// THE TERMS ARE OPNSENSE'S. An interface is what Interfaces > Assignments lists, and the range its
// address and prefix length cover is its NETWORK -- "LAN net" in the rule editor
// (https://docs.opnsense.org/manual/aliases.html). Whether a network was detected or set by the
// operator, and the link-local rule, are opnview's own (docs/data-model.md, Vocabulary).
//
// WHICH NETWORKS COUNT IS DECISION D1 (specs/SPEC-step-5a-corrections.md), and the answer shows it
// rather than restating it: each network carries its origin, whether it is removed, and whether it
// counts now, as store.InterfaceNetworks computes it. A network that does not count says why, in a
// code: removed, or no longer detected.
//
// OVERLAPS ARE READ ON EVERY ANSWER, so an overlap is reported for as long as it exists.
//
// AN EDIT TAKES EFFECT AT THE COLLECTOR'S NEXT DERIVATION. Every edit changes
// store.OnLinkFingerprint, which the collector compares before each derivation.
//
// NOTHING HERE WRITES TO THE FIREWALL OR READS FROM IT. The networks and the rule are opnview's
// own rows.

// The actions an edit can ask for. Each is one store write.
const (
	actionAddNetwork     = "add"
	actionConfirmNetwork = "confirm"
	actionRemoveNetwork  = "remove"
	actionRestoreNetwork = "restore"
	actionLinkLocal      = "link_local"
)

// The two values of the link-local rule as an edit carries them once read.
const (
	linkLocalYes = "yes"
	linkLocalNo  = "no"
)

// editOutcome is a refused edit: why, and whether the refusal is about what was typed in a card's
// add field (inCard, typed) rather than about the request itself.
type editOutcome struct {
	refusal messageKey
	inCard  bool
	typed   string
}

// applyEdit applies one edit of an interface, as the JSON route carries it: the interface's id, the action, the network the action is about and the link-local rule.
// It returns the interface's id, or a refusal; an error is a failure of the database.
func (s *surface) applyEdit(ctx context.Context, idText, action, typed, linkLocal string) (int64, editOutcome, error) {
	now := s.now().UTC().Unix()
	interfaceID, err := strconv.ParseInt(idText, 10, 64)
	if err != nil {
		return 0, editOutcome{refusal: msgInterfaceUnknown}, nil
	}
	row, found, err := s.interfaceRow(ctx, interfaceID)
	if err != nil {
		return 0, editOutcome{}, err
	}
	if !found {
		return 0, editOutcome{refusal: msgInterfaceUnknown}, nil
	}
	// AN UPSTREAM INTERFACE TAKES NO EDIT, whichever edit it is: on-link evidence on an
	// upstream interface is not evidence.
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
// These refusals are about a row the edit names -- confirm, remove, restore, the link-local
// rule -- that does not stand as the edit assumed: the row changed in between, or never existed.
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
// code, or returns the empty key for a refusal that is not about what was
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

// originKey names an interface network's origin.
func originKey(origin string) messageKey {
	if origin == flows.NetworkOriginOperator {
		return msgOriginOperator
	}
	return msgOriginDetected
}
