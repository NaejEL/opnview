package opnsense

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Reading a server-sent event stream that never ends.
//
// One endpoint opnview reads answers this way: /api/diagnostics/cpu_usage/stream,
// whose controller streams the configd action `system cpu stream` with
// Content-Type: text/event-stream, and whose script prints an event about every
// second until the connection is closed (survey, "Processor stream, read from source
// for the resolver-cache cycle"). Call cannot read it: Call reads a body to its end,
// and this body has none, so Call would wait for the client's overall timeout and
// return nothing. Stream reads a bounded number of events within a bounded time,
// closes the connection, and says which of the two bounds ended the read.
//
// THE BOUNDS ARE THE CALLER'S, AND THE TIME BOUND NEVER FIGHTS THE CLIENT'S TIMEOUT.
// The read runs under its own deadline, and that deadline is clamped below the
// client's overall timeout, so a stream is always ended by Stream's own bound and
// never by the transport giving up -- which would be reported as a transport failure
// rather than as "no event in time".

// StreamBound is how much of a stream one read takes.
type StreamBound struct {
	// Events is how many events are read before the connection is closed.
	Events int
	// Within is how long the read may take, from the request to the last event.
	Within time.Duration
}

// StreamEnd says what ended a stream read.
type StreamEnd string

// The three ends of a read that reached the stream.
const (
	// StreamEndedAtEventLimit: the bound's number of events was read.
	StreamEndedAtEventLimit StreamEnd = "event_limit"
	// StreamEndedAtTimeLimit: the bound's time ran out first.
	StreamEndedAtTimeLimit StreamEnd = "time_limit"
	// StreamEndedByServer: the firewall closed the stream before either bound.
	StreamEndedByServer StreamEnd = "closed_by_server"
)

// StreamResponse is what one bounded read of a stream returned.
type StreamResponse struct {
	// Endpoint is the registry entry read.
	Endpoint Endpoint
	// Outcome is what the call turned into. OutcomeOK means HTTP 200: the stream was
	// opened, which says nothing about whether an event arrived -- Events does.
	Outcome Outcome
	// StatusCode is the HTTP status, or 0 when the firewall was not reached.
	StatusCode int
	// Events are the `data:` payloads of the events read, in order, each with the
	// lines of one event joined by a newline as the event-stream format specifies.
	Events [][]byte
	// EndedBy says which bound ended the read, when the stream was opened.
	EndedBy StreamEnd
	// Detail is a short account of the outcome, carrying no credential.
	Detail string
}

// Stream reads one server-sent event stream, within a bound, and closes it.
//
// The refusals are Call's, in Call's order, plus one: an endpoint that does not
// stream is refused, and so is a bound that reads nothing.
func (c *Client) Stream(ctx context.Context, ep Endpoint, bound StreamBound) (StreamResponse, error) {
	response := StreamResponse{Endpoint: ep}
	credentials := c.source.Credentials()

	if !registered(ep) {
		return response, fmt.Errorf("%w: %s", ErrEndpointNotRegistered, ep.Path)
	}
	if command := MutatingCommand(ep.Path); command != "" {
		return response, fmt.Errorf("%w: %s in %s", ErrMutatingCommand, command, ep.Path)
	}
	if !ep.Stream {
		return response, fmt.Errorf("%w: %s does not stream, and is read with Call", ErrStreamEndpoint, ep.Path)
	}
	if credentials.BaseURL == "" {
		return response, ErrNoBaseURL
	}
	if bound.Events <= 0 || bound.Within <= 0 {
		return response, fmt.Errorf("opnsense: a stream read of %s needs a positive event count and time", ep.Path)
	}

	// The read's own deadline, kept below the client's overall timeout so that this
	// bound, and not the transport's, is what ends a silent stream.
	within := bound.Within
	if overall := c.http.Timeout; overall > 0 && within >= overall {
		within = overall - overall/10
	}
	readCtx, cancel := context.WithTimeout(ctx, within)
	defer cancel()

	request, err := http.NewRequestWithContext(readCtx, ep.Method,
		strings.TrimRight(credentials.BaseURL, "/")+ep.Path, nil)
	if err != nil {
		return response, fmt.Errorf("opnsense: building a request for %s: %w", ep.Path, err)
	}
	request.Header.Set("Accept", "text/event-stream")
	request.SetBasicAuth(credentials.APIKey, credentials.APISecret)

	answer, err := c.http.Do(request)
	if err != nil {
		if errors.Is(readCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil {
			// The firewall accepted the connection and sent no header within the bound:
			// the stream never opened, which is an event that did not arrive in time.
			response.Outcome = OutcomeOK
			response.EndedBy = StreamEndedAtTimeLimit
			response.Detail = "the stream did not open within the bound"
			return response, nil
		}
		if certificateRefused(err) {
			response.Outcome = OutcomeCertificateRefused
			response.Detail = "the firewall's certificate was refused"
		} else {
			response.Outcome = OutcomeTransportFailure
			response.Detail = "the firewall did not answer"
		}
		return response, fmt.Errorf("opnsense: calling %s: %w", ep.Path, err)
	}
	// Closing the body is what closes the stream: the firewall's script ends when its
	// connection does.
	defer func() { _ = answer.Body.Close() }()

	response.StatusCode = answer.StatusCode
	if answer.StatusCode < 200 || answer.StatusCode > 299 {
		// The status decides, exactly as for Call; the body of a refusal is short and
		// is not an event stream, so it is not read.
		response.Outcome, response.Detail = classify(answer.StatusCode, nil)
		return response, nil
	}
	response.Outcome = OutcomeOK

	events, ended := readEvents(readCtx, answer, bound.Events)
	response.Events = events
	response.EndedBy = ended
	if ended == StreamEndedAtTimeLimit && ctx.Err() != nil {
		// The caller's own context ended the read, not the bound.
		return response, fmt.Errorf("opnsense: reading %s: %w", ep.Path, ctx.Err())
	}
	return response, nil
}

// readEvents reads at most limit events from an event stream, until the body ends or
// the context's deadline closes it.
//
// The format is the WHATWG HTML Living Standard's, section "Server-sent events",
// "Interpreting an event stream": an event is the lines up to a blank line; a `data:`
// line appends its value, one leading space removed, and the lines of one event are
// joined by a newline; a line starting with a colon is a comment; any other field
// (`event:`, `id:`, `retry:`) does not change the data.
func readEvents(ctx context.Context, answer *http.Response, limit int) ([][]byte, StreamEnd) {
	var (
		events  [][]byte
		data    []string
		hasData bool
	)
	scanner := bufio.NewScanner(answer.Body)
	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		switch {
		case line == "":
			if hasData {
				events = append(events, []byte(strings.Join(data, "\n")))
				data, hasData = nil, false
				if len(events) >= limit {
					return events, StreamEndedAtEventLimit
				}
			}
		case strings.HasPrefix(line, ":"):
			// A comment.
		case line == "data" || strings.HasPrefix(line, "data:"):
			value := strings.TrimPrefix(strings.TrimPrefix(line, "data"), ":")
			data = append(data, strings.TrimPrefix(value, " "))
			hasData = true
		}
	}
	if ctx.Err() != nil {
		return events, StreamEndedAtTimeLimit
	}
	return events, StreamEndedByServer
}
