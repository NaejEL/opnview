package web

import (
	"context"
	"fmt"
	"os"

	"github.com/NaejEL/opnview/internal/opnsense"
)

// Verifying what the user typed.
//
// THIS IS THE FIRST OF THE TWO PERMITTED OUTBOUND CALLS, NOT A THIRD. Same
// destination, same registry, same read-only guarantee as every collector: the
// firewall API on the local network. ROADMAP.md permits exactly two outbound calls
// and this is inside the first of them.
//
// THE MAXMIND KEY IS NOT VERIFIED HERE. A download verifies it, and downloading is
// internal/maxmind's alone; a save wakes that refresh, and the settings surface
// reports the state it recorded.
//
// THE FOUR ANSWERS ARE FOUR ANSWERS. 401/403 is a credential failure, 404 is an
// absent endpoint, a refused certificate is a host that answered and proved an
// identity opnview does not accept, and no answer at all is an unreachable host.
// None of the four is worked around, retried against another path, or reported as
// success — which is the failure mode this whole vocabulary exists to prevent,
// because on this API an HTTP 200 is not proof of success either.
//
// THE FOURTH IS HERE BECAUSE IT WAS REPORTED AS THE LAST ONE. OPNsense serves its
// API under a self-signed certificate as it ships, Go's transport refused it, and
// the refusal arrived as a transport error that this surface reported as "the host
// was not reached" — the opposite of what had happened. opnsense.OutcomeCertificateRefused
// and internal/opnsense/trust.go are the two halves of that fix.

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
		// THE ERROR GOES TO THE CONSOLE, and it used to go nowhere. The outcome below
		// is a word the reader can act on; the error is the detail that says WHICH
		// certificate was presented, or which host refused the connection, and
		// dropping it left the operator with a one-line state and no way to find out
		// more. It is written where every other opnview diagnostic is written, and
		// not onto the page: it can name a host and a path.
		fmt.Fprintf(os.Stderr, "opnview: web: verifying the firewall credentials: %v\n", err)
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
	case opnsense.OutcomeCertificateRefused:
		// THE HOST WAS REACHED. It answered the handshake and presented a certificate
		// opnview does not accept, which is the fingerprint field's business and not
		// the network's. Reporting this as an unreachable host is the defect this
		// case exists to close.
		return msgVerifyCertificateRefused
	case opnsense.OutcomeEmptyBody, opnsense.OutcomeResultFailed, opnsense.OutcomeServerError:
		// The firewall answered and the answer cannot be used. It is neither a
		// credential failure nor an absence, and reporting it as either would send
		// the reader to change something that is not wrong.
		return msgVerifyUnexpectedAnswer
	default:
		return msgVerifyUnexpectedAnswer
	}
}
