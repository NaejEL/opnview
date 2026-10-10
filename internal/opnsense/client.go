package opnsense

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// InterfaceName is an OPNsense interface identifier — the configuration key,
// the token `interfaces_info` returns as `identifier`.
//
// It is a type of its own for one measured reason: /api/diagnostics/traffic/top
// takes these, and a NETWORK DEVICE NAME — the token the filter log reports in
// its `interface` field, and the key `get_interface_names` maps from — returns an
// empty array with HTTP 200, which is indistinguishable from an absence of
// traffic. Survey, "Verified against a live firewall, 2026-09-27".
//
// There is deliberately no matching type for a device name. A second type would
// only be checked where somebody remembered to write it, and the check that
// actually holds is the one in internal/collect: the names about to be sent are
// compared against the device set discovery read from the firewall, so a device
// where an identifier belongs is refused whatever its Go type.
type InterfaceName string

// Credentials are what the firewall needs to answer, and they never have a
// default: they are entered in the interface, which is cycle 4B. Nothing in
// this repository ships a value for any of these fields.
type Credentials struct {
	// BaseURL is the firewall's HTTPS base URL, with no trailing slash.
	BaseURL string
	// APIKey is the key half of the pair, sent as the HTTP Basic username.
	APIKey string
	// APISecret is the secret half, sent as the HTTP Basic password.
	APISecret string
	// Fingerprint is the SHA-256 fingerprint of the certificate the firewall is
	// expected to present, lower-case hex with no separators, or empty.
	//
	// EMPTY MEANS THE STANDARD VERIFICATION, NOT AN ABSENT ONE. A fingerprint is
	// what an operator pins when OPNsense serves the certificate it generated for
	// itself, which is how the product ships; an installation whose certificate a
	// certificate authority vouches for leaves this empty and is verified against
	// the system roots. See trust.go.
	Fingerprint string
}

// The refusals. Both are programming errors rather than conditions of the
// firewall, and both happen before a request is built.
var (
	// ErrMutatingCommand is returned when a path names a command that could
	// write. opnview uses the OPNsense API strictly read-only, and this is the
	// enforceable form of that rule.
	ErrMutatingCommand = errors.New("opnsense: refusing to issue a mutating command")
	// ErrEndpointNotRegistered is returned for a path that is not in Registry().
	// An endpoint nobody justified from the survey cannot be called.
	ErrEndpointNotRegistered = errors.New("opnsense: endpoint is not in the registry")
	// ErrNoBaseURL is returned when the client has no firewall to talk to, which
	// is the normal state until credentials are entered.
	ErrNoBaseURL = errors.New("opnsense: no firewall base URL is configured")
	// ErrStreamEndpoint is returned when Call is asked for an endpoint that streams,
	// or Stream for one that does not. A stream has no end for Call to read to, and
	// an ordinary answer has no events for Stream to count.
	ErrStreamEndpoint = errors.New("opnsense: the endpoint's answer is not read this way")
	// ErrEmptyArgument is returned when a positional path argument is empty. An
	// empty path element would silently change which command the router reaches.
	ErrEmptyArgument = errors.New("opnsense: positional argument is empty")
	// ErrUnsafeArgument is returned for a positional argument outside the safe
	// character class. The arguments are appended to the path VERBATIM rather than
	// percent-encoded, because the one endpoint that takes a list takes it as a
	// comma-separated path element and percent-encoding the comma would change which
	// interfaces the firewall answers for — and a wrong argument there returns an
	// empty array with HTTP 200, indistinguishable from no traffic. Refusing an
	// argument that would need encoding is safer than encoding it.
	ErrUnsafeArgument = errors.New("opnsense: positional argument is outside the safe character class")
)

// mutatingCommands is the list docs/opnsense-api-survey.md names under "GET
// versus POST, and the read-only guarantee": the commands opnview never calls.
//
// `dnsbl` and `reconfigure_general` were added by the resolver-cache cycle, which
// read every action of two Unbound controllers at opnsense/core 26.7.3,
// DiagnosticsController.php and ServiceController.php (survey, "Resolver cache and
// local data, read from source for the resolver-cache cycle"). The other Unbound
// controllers, SettingsController.php and OverviewController.php, were not audited
// for this list, and this list does not claim to cover them; opnview calls none of
// their paths, because the endpoint registry admits none.
// Unbound/Api/ServiceController.php's dnsblAction runs `unbound dnsbl`, which
// rebuilds the blocklists, and reconfigureGeneralAction reloads DNS and restarts
// DHCP. Neither is the plain `reconfigure` the list already held, so neither was
// refused. Unbound/Api/DiagnosticsController.php has no mutating action at that tag:
// stats, dumpcache, dumpinfra, listlocaldata, listlocalzones and listinsecure read,
// and testBlocklist only matches a name against the lists. The cache is flushed by the
// configd action `unbound cache`, which no API action reaches.
var mutatingCommands = []string{
	"set", "add", "del", "toggle", "reconfigure",
	"start", "stop", "restart", "clear", "drop_alert_log", "del_lease",
	"dnsbl", "reconfigure_general",
}

// MutatingCommand returns the mutating command a path names, or the empty
// string. The OPNsense router maps {base}/api/<module>/<controller>/<command>,
// so the command is the fourth path element; path elements after it are positional
// parameters and are caller data rather than a command.
func MutatingCommand(path string) string {
	elements := strings.Split(strings.Trim(path, "/"), "/")
	if len(elements) < 4 {
		return ""
	}
	command := elements[3]
	for _, forbidden := range mutatingCommands {
		if command == forbidden {
			return command
		}
	}
	return ""
}

// Outcome is what a call to the firewall turned into.
//
// NONE OF THESE VALUES IS "no data", and that is the point of the type. Every
// source returns an empty list both when it is disabled and when it is quiet
// (survey, gap 11), so the collector has to be able to tell a healthy empty
// answer from five different failures. An empty body, a failed result, a 404
// and a 401/403 each mean something different, and each is a state opnview
// records rather than a zero it draws.
type Outcome string

// The outcomes, and no sixth.
const (
	// OutcomeOK is HTTP 200 with a body that is not empty and does not report a
	// failed result. The body may still describe nothing at all — an empty array
	// from a healthy source is this outcome, and what it means is the
	// collector's decision, not the transport's.
	OutcomeOK Outcome = "ok"
	// OutcomeEmptyBody is HTTP 200 with an empty body. The survey names this as
	// a backend failure or a wrong verb, never as an absence of data.
	OutcomeEmptyBody Outcome = "empty_body"
	// OutcomeResultFailed is HTTP 200 with {"result":"failed"}. Model validation
	// failures come back this way, so HTTP 200 is not proof of success.
	OutcomeResultFailed Outcome = "result_failed"
	// OutcomeNotFound is HTTP 404: an unknown module, controller or command. It
	// is the normal signal that an optional component is not installed.
	OutcomeNotFound Outcome = "not_found"
	// OutcomeForbidden is HTTP 401 or 403: the credentials failed, or the key
	// owner's ACL does not cover the URI.
	OutcomeForbidden Outcome = "forbidden"
	// OutcomeServerError is any other unsuccessful status, HTTP 500 included.
	OutcomeServerError Outcome = "server_error"
	// OutcomeTransportFailure is no answer at all: the firewall was not reached.
	OutcomeTransportFailure Outcome = "transport_failure"
	// OutcomeCertificateRefused is the firewall answering the handshake with a
	// certificate opnview does not accept.
	//
	// IT IS NOT OutcomeTransportFailure, and separating the two is the reason this
	// value exists. A refused certificate means the host IS reachable and proved an
	// identity that is not the pinned one; reporting it as an unreachable host sends
	// the operator to the network when the answer is in the fingerprint field. See
	// trust.go.
	OutcomeCertificateRefused Outcome = "certificate_refused"
)

// Response is one answer from the firewall.
type Response struct {
	// Endpoint is the registry entry that produced it.
	Endpoint Endpoint
	// Outcome is what the call turned into.
	Outcome Outcome
	// StatusCode is the HTTP status, or 0 when the firewall was not reached.
	StatusCode int
	// Body is the raw body, kept raw so a collector decodes it into its own
	// shape rather than a shared one.
	Body []byte
	// Detail is a short human-readable account of the outcome, suitable for
	// source_availability.detail. It never carries the base URL or a credential.
	Detail string
}

// OK reports whether the call reached the firewall and got a usable body.
func (r Response) OK() bool { return r.Outcome == OutcomeOK }

// CredentialSource yields the credentials the NEXT request is made with.
//
// IT IS A SEAM, AND IT IS THE ONLY ONE THIS PACKAGE HAS. Cycle 4A built the
// client once at start-up with an empty Credentials value, which meant a
// credential typed into the settings surface could not reach a running collector:
// a typo cost a restart, and so did the first correct entry. A source is read per
// request instead, so a change made through the interface takes effect on the
// next collection pass in the same process.
//
// It is an interface rather than a setter on Client for the reason every other
// narrow interface in this repository is one: the thing that OWNS the credentials
// — internal/config, which knows where they are stored and how they are
// decrypted — must not be something this package imports.
type CredentialSource interface {
	// Credentials returns the credentials to use now. An implementation must be
	// safe to call from several goroutines: the collector loops run concurrently.
	Credentials() Credentials
}

// staticCredentials is a CredentialSource that never changes. It is what
// NewClient wraps its argument in, so a caller with one fixed firewall — every
// test in this repository, and nothing in the product — needs no source of its
// own.
type staticCredentials struct{ credentials Credentials }

// Credentials returns the fixed value.
func (s staticCredentials) Credentials() Credentials { return s.credentials }

// Static returns a CredentialSource over one fixed value.
func Static(credentials Credentials) CredentialSource {
	return staticCredentials{credentials: credentials}
}

// Client is the single point at which opnview builds an HTTP request. Nothing
// else in cmd/ or internal/ constructs one, which is what makes "one outbound
// destination" an enforceable property rather than an intention.
type Client struct {
	source CredentialSource
	http   *http.Client
}

// NewClient returns a client for one firewall whose credentials never change.
// transport may be nil, in which case a transport with no shared state is used;
// tests pass a transport that fails any host but the fake firewall.
func NewClient(credentials Credentials, transport http.RoundTripper, timeout time.Duration) *Client {
	return NewClientFromSource(Static(credentials), transport, timeout)
}

// NewClientFromSource returns a client that reads its credentials for every
// request. It is what the product uses, so that the settings surface can change
// them without a restart.
func NewClientFromSource(source CredentialSource, transport http.RoundTripper,
	timeout time.Duration) *Client {
	if source == nil {
		source = Static(Credentials{})
	}
	if transport == nil {
		// The product's transport: standard verification, or the pinned fingerprint
		// when one is configured. A caller that passes its own transport — every
		// test does — gets exactly that one and no trust decision of ours.
		transport = NewTrustingTransport(source)
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &Client{
		source: source,
		http:   &http.Client{Transport: transport, Timeout: timeout},
	}
}

// CloseIdleConnections drops the pooled connections.
//
// IT IS WHAT MAKES A CHANGED FINGERPRINT TAKE EFFECT WITHOUT A RESTART. The
// certificate is checked once per handshake, not once per request, so a connection
// opened under the previous pin would be reused and would keep answering after the
// pin changed to one it does not match. Whatever replaces the credentials calls
// this, which is the settings surface.
func (c *Client) CloseIdleConnections() { c.http.CloseIdleConnections() }

// RequestOptions are the parameters of one call.
type RequestOptions struct {
	// Arguments are the positional path path elements appended after the command.
	Arguments []string
	// Query is appended as a query string. It is only ever used where the
	// survey establishes a query parameter.
	Query url.Values
	// Form is the form-encoded body of a grid search. Every value arrives at
	// the controller as a string, which is what a grid search expects.
	Form url.Values
	// JSON is marshalled as an application/json body. It is the only encoding
	// that delivers a native integer, which one endpoint requires.
	JSON any
}

// Call issues one request, or refuses to.
//
// The order of the checks is deliberate: registration, then the mutating
// command, then the base URL, then the arguments. Every refusal happens before
// a request object exists, so a transport can assert it saw nothing.
func (c *Client) Call(ctx context.Context, ep Endpoint, opt RequestOptions) (Response, error) {
	response := Response{Endpoint: ep}

	// The credentials are read ONCE PER CALL, from the source, so a change made
	// through the settings surface reaches the next pass without a restart. Read
	// once and held in a local, so one call cannot be built from two halves of two
	// different credential sets.
	credentials := c.source.Credentials()

	if !registered(ep) {
		return response, fmt.Errorf("%w: %s", ErrEndpointNotRegistered, ep.Path)
	}
	if command := MutatingCommand(ep.Path); command != "" {
		return response, fmt.Errorf("%w: %s in %s", ErrMutatingCommand, command, ep.Path)
	}
	if ep.Stream {
		return response, fmt.Errorf("%w: %s streams, and is read with Stream", ErrStreamEndpoint, ep.Path)
	}
	if credentials.BaseURL == "" {
		return response, ErrNoBaseURL
	}
	for _, argument := range opt.Arguments {
		if argument == "" {
			return response, fmt.Errorf("%w: %s", ErrEmptyArgument, ep.Path)
		}
		if !safeArgument(argument) {
			return response, fmt.Errorf("%w: %q in %s", ErrUnsafeArgument, argument, ep.Path)
		}
	}

	target := strings.TrimRight(credentials.BaseURL, "/") + ep.Path
	for _, argument := range opt.Arguments {
		target += "/" + argument
	}
	if len(opt.Query) > 0 {
		target += "?" + opt.Query.Encode()
	}

	var body io.Reader
	contentType := ""
	switch ep.Encoding {
	case FormBody:
		if len(opt.Form) > 0 {
			body = strings.NewReader(opt.Form.Encode())
			contentType = "application/x-www-form-urlencoded"
		}
	case JSONBody:
		encoded, err := json.Marshal(opt.JSON)
		if err != nil {
			return response, fmt.Errorf("opnsense: encoding the body for %s: %w", ep.Path, err)
		}
		body = bytes.NewReader(encoded)
		contentType = "application/json"
	case NoBody:
	}

	request, err := http.NewRequestWithContext(ctx, ep.Method, target, body)
	if err != nil {
		return response, fmt.Errorf("opnsense: building a request for %s: %w", ep.Path, err)
	}
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	request.Header.Set("Accept", "application/json")
	request.SetBasicAuth(credentials.APIKey, credentials.APISecret)

	answer, err := c.http.Do(request)
	if err != nil {
		// THE TWO FAILURES ARE TWO FAILURES. A refused certificate is the firewall
		// answering with an identity opnview does not accept, and an unreachable host
		// is no answer at all; they are acted on in two different places.
		if certificateRefused(err) {
			response.Outcome = OutcomeCertificateRefused
			response.Detail = "the firewall's certificate was refused"
		} else {
			response.Outcome = OutcomeTransportFailure
			response.Detail = "the firewall did not answer"
		}
		return response, fmt.Errorf("opnsense: calling %s: %w", ep.Path, err)
	}
	defer func() { _ = answer.Body.Close() }()

	raw, err := io.ReadAll(answer.Body)
	if err != nil {
		response.Outcome = OutcomeTransportFailure
		response.StatusCode = answer.StatusCode
		response.Detail = "the answer could not be read to the end"
		return response, fmt.Errorf("opnsense: reading %s: %w", ep.Path, err)
	}

	response.StatusCode = answer.StatusCode
	response.Body = raw
	response.Outcome, response.Detail = classify(answer.StatusCode, raw)
	return response, nil
}

// safeArgument reports whether a positional argument can be appended to a path
// verbatim. The class is the one OPNsense's own identifiers and the comma that
// separates a list use, and nothing else: no slash, no percent, no space, no
// question mark, so an argument can neither escape its path element nor open a query
// string.
func safeArgument(argument string) bool {
	for index := 0; index < len(argument); index++ {
		character := argument[index]
		switch {
		case character >= 'a' && character <= 'z':
		case character >= 'A' && character <= 'Z':
		case character >= '0' && character <= '9':
		case character == '_' || character == '-' || character == '.' || character == ',':
		default:
			return false
		}
	}
	return true
}

// classify turns a status code and a body into an outcome.
//
// HTTP 200 is not proof of success: a model validation failure and a wrong verb
// both come back as 200, the first with {"result":"failed"} and the second with
// an empty body. HTTP 405 is never emitted. Survey, "Status codes and error
// bodies".
func classify(status int, body []byte) (Outcome, string) {
	switch {
	case status == http.StatusNotFound:
		return OutcomeNotFound, "the endpoint is not present on this firewall"
	case status == http.StatusUnauthorized:
		return OutcomeForbidden, "authentication failed"
	case status == http.StatusForbidden:
		return OutcomeForbidden, "the key owner's ACL does not cover this path"
	case status < 200 || status > 299:
		return OutcomeServerError, fmt.Sprintf("the firewall answered HTTP %d", status)
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return OutcomeEmptyBody, "HTTP 200 with an empty body, which is a backend failure and not an absence of data"
	}
	if failedResult(body) {
		return OutcomeResultFailed, "HTTP 200 with a failed result"
	}
	return OutcomeOK, ""
}

// failedResult reports whether the body is an OPNsense failure envelope. It
// decodes only the one key, so a body of any other shape is left alone.
func failedResult(body []byte) bool {
	var envelope struct {
		Result string `json:"result"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return false
	}
	return envelope.Result == "failed"
}
