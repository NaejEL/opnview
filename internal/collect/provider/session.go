package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/NaejEL/opnview/internal/decode"
	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/store"
)

// Session is THE PORT: everything an implementation may ask of the host that runs it, and
// nothing else.
//
// It is declared as an interface rather than passed as the collector for one reason, and it is
// not testability: the collector is the whole layer. It holds the store, the discovery snapshot,
// the availability writer, the identity cascade and every kind's coordinator, and an
// implementation handed it could write a row, mint a client identity or re-run a probe round.
// None of those is a connector's business, and none of them can cross a process boundary,
// which is where this seam is going. So the boundary is stated in code, where the compiler
// keeps it, instead of in a comment asking implementations to behave.
//
// WHAT IT DELIBERATELY DOES NOT GRANT, because each absence is the thing that makes the port
// worth having:
//
//   - THE STORE. No implementation reads or writes a row. It returns records; the kind
//     decides what becomes of them. This is what lets a connector run somewhere with no
//     database at all.
//   - DISCOVERY. The firewall's own configuration is the kind's to join against. The one
//     implementation that needs a snapshot -- the sampler -- is HANDED it as a parameter of
//     Sample, so what it may see is decided per call by the coordinator rather than fetched
//     at will.
//   - AVAILABILITY. An implementation REPORTS a Result; it never writes source availability.
//     The distinction is what keeps a connector from declaring itself healthy.
//   - ACTIVATION. Nothing here answers which implementation of a kind is active. That is
//     read from the firewall's configuration by the probe round, and an implementation that
//     could ask would be one step from preferring itself.
//   - THE CLOCK. Now yields the instant as the epoch every column stores, and that is all.
//     No implementation schedules, sleeps or waits.
//
// AND NOTHING THAT SERVES ONE KIND. Reading a page of a lease table is common to the readable
// DHCP backends, so it is written once -- as a FREE FUNCTION over this port, in the dhcplease
// package, not as a method on it. A port that gains a method whenever two implementations share
// code ends up as wide as the layer it was meant to narrow, and every one of those methods is a
// verb somebody has to implement on the far side of a wire.
type Session interface {
	// Call issues one request to the firewall API and returns what it answered. It goes through
	// the broker, which answers from its mirror or from one call shared by every requester, and
	// the client that performs the call is not exposed: an implementation states an endpoint and
	// reads a response. The refusal to write to the firewall is the broker's read-only rule, with
	// internal/opnsense's refusal of mutating commands checked first.
	Call(ctx context.Context, endpoint opnsense.Endpoint, options opnsense.RequestOptions) (
		opnsense.Response, error)

	// ServiceState probes an OPNsense service status endpoint. Every implementation whose
	// product is an OPNsense service asks the same question of the same shape of answer, so
	// the host answers it once.
	ServiceState(ctx context.Context, endpoint opnsense.Endpoint) (
		running bool, state store.AvailabilityState, outcome store.AvailabilityOutcome, detail string,
		err error)

	// ReadObject calls an endpoint and decodes a JSON object, reporting whether that
	// worked. An unreadable answer is not an error: it is an availability state, and the
	// caller says so.
	ReadObject(ctx context.Context, endpoint opnsense.Endpoint) (decode.Object, bool, error)

	// ReadCollection calls an endpoint and returns its rows, whatever collection shape it
	// used, looking under the wrapper keys the caller names.
	ReadCollection(ctx context.Context, endpoint opnsense.Endpoint, wrapperKeys ...string) (
		[]decode.Object, bool, error)

	// Stream reads a server-sent event stream within a bound and closes it. It is the
	// second outbound capability, and it exists because one endpoint answers with a
	// stream that never ends: Call reads a body to its end and this body has none
	// (survey, "Processor stream, read from source for the resolver-cache cycle").
	Stream(ctx context.Context, endpoint opnsense.Endpoint, bound opnsense.StreamBound) (
		opnsense.StreamResponse, error)

	// Normaliser returns the timestamp normaliser for this instant. It carries the
	// reference time the one year-less timestamp shape needs, which an implementation
	// cannot compose for itself without reading a clock, and the firewall's offset
	// from UTC, which the host measures first if nothing has yet.
	Normaliser(ctx context.Context) (decode.Normaliser, error)

	// Now is the current instant as the UTC epoch every column stores.
	Now() int64
}

// Gate is the broker as the host sees it: the one path to the firewall API (A9 of the broker
// amendment to specs/SPEC-backend-completion-plugins.md). A call names who asks and how old an
// answer may be; the broker answers from its mirror or from one call shared by every requester,
// and applies its read-only rule. The host reads every answer whole, as a body.
type Gate interface {
	GateCall(ctx context.Context, requester string, endpoint opnsense.Endpoint, options opnsense.RequestOptions,
		maxAge time.Duration) (opnsense.Response, error)
	GateStream(ctx context.Context, requester string, endpoint opnsense.Endpoint, bound opnsense.StreamBound) (
		opnsense.StreamResponse, error)
}

// Host is the Session over the broker. The collector embeds one; the fake-firewall helper of the
// tests builds one, so the code behind each port method is written once.
type Host struct {
	gate      Gate
	normalise func(ctx context.Context) (decode.Normaliser, error)
	now       func() int64
	// requester is who the host asks on behalf of, and maxAge how old an answer may be.
	requester string
	maxAge    time.Duration
}

var _ Session = (*Host)(nil)

// NewHost returns a host over the broker. normalise yields the timestamp normaliser (the collector
// measures the firewall's offset from UTC before it answers); now yields the current UTC epoch.
func NewHost(gate Gate, normalise func(ctx context.Context) (decode.Normaliser, error), now func() int64) *Host {
	return &Host{gate: gate, normalise: normalise, now: now, requester: "core:collector"}
}

// For returns the host asking on behalf of one requester, at one max age.
func (h *Host) For(requester string, maxAge time.Duration) *Host {
	scoped := *h
	scoped.requester, scoped.maxAge = requester, maxAge
	return &scoped
}

// Requester is who the host asks on behalf of.
func (h *Host) Requester() string { return h.requester }

// MaxAge is how old an answer the host accepts.
func (h *Host) MaxAge() time.Duration { return h.maxAge }

// Call issues one request to the firewall API. It exists so that the port an implementation is
// handed can grant ONE OUTBOUND CALL rather than the client that makes it: handing the client
// across the seam would hand over its construction and its credentials, handing this method hands
// over a request and a response and nothing else. It is also the shape that survives connectors
// moving out of process, where the host performs the call and the connector never holds a
// client at all.
func (h *Host) Call(ctx context.Context, endpoint opnsense.Endpoint,
	options opnsense.RequestOptions) (opnsense.Response, error) {
	return h.gate.GateCall(ctx, h.requester, endpoint, options, h.maxAge)
}

// Normaliser returns the timestamp normaliser for this instant.
func (h *Host) Normaliser(ctx context.Context) (decode.Normaliser, error) {
	return h.normalise(ctx)
}

// Now is the current instant as the UTC epoch every column stores.
func (h *Host) Now() int64 { return h.now() }

// Stream reads one server-sent event stream within a bound. It is the port's second outbound
// capability, and it hands over a bounded read and its events, nothing more.
func (h *Host) Stream(ctx context.Context, endpoint opnsense.Endpoint,
	bound opnsense.StreamBound) (opnsense.StreamResponse, error) {
	return h.gate.GateStream(ctx, h.requester, endpoint, bound)
}

// ServiceState probes a service status endpoint and reports whether it is running, along with
// the availability state and detail its answer implies. Every implementation whose product
// exposes an OPNsense service uses it, which is why it lives here rather than in one of them.
func (h *Host) ServiceState(ctx context.Context, endpoint opnsense.Endpoint) (
	bool, store.AvailabilityState, store.AvailabilityOutcome, string, error) {
	response, err := h.gate.GateCall(ctx, h.requester, endpoint, opnsense.RequestOptions{}, h.maxAge)
	if err != nil && response.Outcome == "" {
		return false, store.StateUnavailable, store.OutcomeRequestNotMade, "", err
	}
	if !response.OK() {
		return false, store.StateUnavailable, ResponseOutcome(response), response.Detail, nil
	}
	var body decode.Object
	if err := json.Unmarshal(response.Body, &body); err != nil {
		return false, store.StateUnavailable, store.OutcomeUndecodable,
			"the status body could not be read", nil
	}
	status, present := decode.String(body, "status")
	if !present {
		return false, store.StateUnavailable, store.OutcomeUndecodable,
			"the status body reported no status", nil
	}
	if decode.LowerASCII(status) == "running" {
		return true, store.StateReachable, store.OutcomeHealthy, "", nil
	}
	return false, store.StatePresentButDisabled, store.OutcomeServiceNotRunning,
		"the service reported " + QuoteForDetail(status), nil
}

// ReadObject calls an endpoint and decodes its body as a JSON object, reporting whether that
// worked. A failure is not an error here: the caller turns it into an availability state,
// which is what an unreadable answer is.
func (h *Host) ReadObject(ctx context.Context, endpoint opnsense.Endpoint) (
	decode.Object, bool, error) {
	response, err := h.gate.GateCall(ctx, h.requester, endpoint, opnsense.RequestOptions{}, h.maxAge)
	if err != nil && response.Outcome == "" {
		return nil, false, err
	}
	if !response.OK() {
		return nil, false, nil
	}
	var body decode.Object
	if err := json.Unmarshal(response.Body, &body); err != nil {
		return nil, false, nil
	}
	return body, true, nil
}

// ReadCollection calls an endpoint and returns its rows, whatever collection shape it used: a
// bare array, a grid-search envelope, a map keyed by something, or a list under one of the
// wrapper keys the caller names.
//
// The wrapper keys are an UNVERIFIED assumption, like the telemetry field names: the survey
// establishes that those endpoints answer and not how they shape their answers. A body whose
// shape none of them matches yields no rows, which the caller records as an absent reading
// rather than as a zero.
func (h *Host) ReadCollection(ctx context.Context, endpoint opnsense.Endpoint,
	wrapperKeys ...string) ([]decode.Object, bool, error) {
	response, err := h.gate.GateCall(ctx, h.requester, endpoint, opnsense.RequestOptions{}, h.maxAge)
	if err != nil && response.Outcome == "" {
		return nil, false, err
	}
	if !response.OK() {
		return nil, false, nil
	}

	var decoded any
	if err := json.Unmarshal(response.Body, &decoded); err != nil {
		return nil, false, nil
	}
	rows, ok := CollectionRows(decoded, response.Body, wrapperKeys...)
	return rows, ok, nil
}

// CollectionRows finds an answer's rows, whatever collection shape it used: under one of the
// wrapper keys, in a grid-search envelope, as a bare array, or as a map keyed by something.
func CollectionRows(decoded any, body []byte, wrapperKeys ...string) ([]decode.Object, bool) {
	if object, isObject := decoded.(decode.Object); isObject {
		for _, key := range wrapperKeys {
			if nested, present := object[key]; present {
				if rows := decode.Objects(nested); len(rows) > 0 {
					return rows, true
				}
			}
		}
	}
	rows, err := decode.Rows(body)
	if err != nil {
		return nil, false
	}
	return rows, true
}

// ResponseOutcome names what one answer from the firewall turned into, from the closed
// set. It reads the outcome and the status code the client classified, never the
// detail: 401 and 403 are one opnsense outcome and two availability outcomes, because
// the first is a key that was refused and the second a key whose user lacks a privilege,
// and the user acts on them in two different places.
func ResponseOutcome(response opnsense.Response) store.AvailabilityOutcome {
	switch response.Outcome {
	case opnsense.OutcomeOK:
		return store.OutcomeHealthy
	case opnsense.OutcomeNotFound:
		return store.OutcomeNotFound
	case opnsense.OutcomeForbidden:
		if response.StatusCode == http.StatusUnauthorized {
			return store.OutcomeAuthenticationFailed
		}
		return store.OutcomeDenied
	case opnsense.OutcomeEmptyBody:
		return store.OutcomeEmptyBody
	case opnsense.OutcomeResultFailed:
		return store.OutcomeResultFailed
	case opnsense.OutcomeServerError:
		return store.OutcomeServerError
	case opnsense.OutcomeTransportFailure:
		return store.OutcomeTransportFailure
	case opnsense.OutcomeCertificateRefused:
		return store.OutcomeCertificateRefused
	}
	return store.OutcomeRequestNotMade
}

// QuoteForDetail renders a value the firewall reported for an availability detail, so a reader
// can tell an empty answer from a missing one.
func QuoteForDetail(value string) string {
	if value == "" {
		return "an empty status"
	}
	return "a status of " + value
}

// Gate is the broker the host asks.
func (h *Host) Gate() Gate { return h.gate }
