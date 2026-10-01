package web

import (
	"context"

	"github.com/NaejEL/opnview/internal/opnsense"
)

// Verifying what the user typed.
//
// THIS IS THE FIRST OF THE TWO PERMITTED OUTBOUND CALLS, NOT A THIRD. Same
// destination, same registry, same read-only guarantee as every collector: the
// firewall API on the local network. ROADMAP.md permits exactly two outbound calls
// and this is inside the first of them.
//
// THE MAXMIND KEY IS NOT VERIFIED IN THIS CYCLE. Verifying it means downloading,
// downloading is cycle 4C, and a verification download here would be a call made
// before it is needed. The key is stored and reported as stored, and nothing else.
//
// THE THREE ANSWERS ARE THREE ANSWERS. 401/403 is a credential failure, 404 is an
// absent endpoint, and no answer at all is an unreachable host. None of the three is
// worked around, retried against another path, or reported as success — which is the
// failure mode this whole vocabulary exists to prevent, because on this API an HTTP
// 200 is not proof of success either.

// verificationEndpoint is the one endpoint a credential check calls.
//
// It is opnsense.InterfacesInfo — /api/interfaces/overview/interfaces_info — and it
// is already in opnsense.Registry(), so nothing new can be reached from here.
//
// WHY THIS ONE, cited against docs/opnsense-api-survey.md: the survey establishes
// it under "Runtime discovery" as item (i), the interfaces with their descriptions,
// devices, link state and VLAN tags. It is core OPNsense rather than a plugin, so a
// 404 from it means the base URL is not an OPNsense API rather than that an optional
// module is missing; it is a GET with no positional argument and no body, so nothing
// about the call form can be got wrong; and it is the endpoint runtime discovery
// calls first anyway, so a credential that verifies here is a credential collection
// can actually use. It is read-only: the survey tabulates it under "GET versus POST,
// and the read-only guarantee", and internal/opnsense refuses a mutating command
// before a request exists.
func verificationEndpoint() opnsense.Endpoint { return opnsense.InterfacesInfo }

// verify calls the firewall once and says what the answer means.
//
// It returns a catalogue key, because that is the only thing the settings surface
// may show, and the mapping below is the whole of the translation from the
// opnsense.Outcome vocabulary to something a reader can act on.
func (s *Server) verify(ctx context.Context) messageKey {
	if s.credentials.Credentials().BaseURL == "" {
		return msgVerifyNoURL
	}

	// The endpoint is the registry entry named above, and the citation for it is in
	// the comment on verificationEndpoint: docs/opnsense-api-survey.md, "Runtime
	// discovery", item (i).
	response, err := s.client.Call(ctx, verificationEndpoint(), opnsense.RequestOptions{})
	if err != nil {
		// A transport failure carries an error as well as an outcome; every other
		// outcome arrives with a nil error. Falling through to the switch on the
		// outcome keeps one mapping rather than two.
		if response.Outcome == "" {
			return msgVerifyUnreachable
		}
	}

	switch response.Outcome {
	case opnsense.OutcomeOK:
		return msgVerifyVerified
	case opnsense.OutcomeForbidden:
		return msgVerifyCredentialsBad
	case opnsense.OutcomeNotFound:
		return msgVerifyEndpointNotFound
	case opnsense.OutcomeTransportFailure:
		return msgVerifyUnreachable
	case opnsense.OutcomeEmptyBody, opnsense.OutcomeResultFailed, opnsense.OutcomeServerError:
		// The firewall answered and the answer cannot be used. It is neither a
		// credential failure nor an absence, and reporting it as either would send
		// the reader to change something that is not wrong.
		return msgVerifyUnexpectedAnswer
	default:
		return msgVerifyUnexpectedAnswer
	}
}
