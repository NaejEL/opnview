package opnsense

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// countingTransport records how many requests actually left. A refusal that happens
// before a request is built leaves this at zero, which is the assertion.
type countingTransport struct {
	requests int
}

// RoundTrip counts a request and answers with an empty object.
func (transport *countingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	transport.requests++
	return nil, errors.New("opnsense_test: this transport must never be reached")
}

// TestClientRefusesAMutatingCommandBeforeTheTransportSeesAnything is the read-only
// rule in its enforceable form.
func TestClientRefusesAMutatingCommandBeforeTheTransportSeesAnything(t *testing.T) {
	for _, command := range mutatingCommands {
		transport := &countingTransport{}
		client := NewClient(Credentials{BaseURL: "https://firewall.invalid"}, transport, time.Second)

		// A hand-built endpoint, which is how a mutating path would arrive in
		// practice: somebody writes the struct rather than adding it to the registry.
		mutating := Endpoint{
			Path:          "/api/example/controller/" + command,
			Method:        http.MethodPost,
			SurveySection: "none",
			UpstreamURL:   "none",
		}
		_, err := client.Call(context.Background(), mutating, RequestOptions{})
		if !errors.Is(err, ErrMutatingCommand) && !errors.Is(err, ErrEndpointNotRegistered) {
			t.Errorf("calling %s returned %v, which is neither refusal", mutating.Path, err)
		}
		if transport.requests != 0 {
			t.Errorf("calling %s reached the transport %d times; it must reach it none",
				mutating.Path, transport.requests)
		}
	}
}

// TestClientRefusesAnEndpointThatIsNotInTheRegistry stops a path nobody justified
// from the survey from ever being issued.
func TestClientRefusesAnEndpointThatIsNotInTheRegistry(t *testing.T) {
	transport := &countingTransport{}
	client := NewClient(Credentials{BaseURL: "https://firewall.invalid"}, transport, time.Second)

	invented := Endpoint{
		Path:          "/api/invented/controller/get",
		Method:        http.MethodGet,
		SurveySection: "none",
		UpstreamURL:   "none",
	}
	_, err := client.Call(context.Background(), invented, RequestOptions{})
	if !errors.Is(err, ErrEndpointNotRegistered) {
		t.Fatalf("calling an invented endpoint returned %v, not the registry refusal", err)
	}
	if transport.requests != 0 {
		t.Fatalf("an invented endpoint reached the transport %d times", transport.requests)
	}
}

// TestClientRefusesAnUnsafePositionalArgument keeps an argument from escaping its
// path path element or opening a query string. The arguments are appended verbatim,
// because the comma in an interface-name list must not be percent-encoded.
func TestClientRefusesAnUnsafePositionalArgument(t *testing.T) {
	transport := &countingTransport{}
	client := NewClient(Credentials{BaseURL: "https://firewall.invalid"}, transport, time.Second)

	for _, argument := range []string{"a/b", "a?b", "a b", "a%2Fb", "a#b"} {
		_, err := client.Call(context.Background(), TrafficTop,
			RequestOptions{Arguments: []string{argument}})
		if !errors.Is(err, ErrUnsafeArgument) {
			t.Errorf("the argument %q returned %v, not the unsafe-argument refusal", argument, err)
		}
	}
	_, err := client.Call(context.Background(), TrafficTop, RequestOptions{Arguments: []string{""}})
	if !errors.Is(err, ErrEmptyArgument) {
		t.Errorf("an empty argument returned %v, not the empty-argument refusal", err)
	}
	if transport.requests != 0 {
		t.Errorf("an unsafe argument reached the transport %d times", transport.requests)
	}
}

// TestClientRefusesToCallWithNoBaseURL is the normal state of this cycle: no
// credentials have been entered, so there is no firewall to reach, and that is a
// refusal rather than a request to nowhere.
func TestClientRefusesToCallWithNoBaseURL(t *testing.T) {
	transport := &countingTransport{}
	client := NewClient(Credentials{}, transport, time.Second)
	_, err := client.Call(context.Background(), IDSStatus, RequestOptions{})
	if !errors.Is(err, ErrNoBaseURL) {
		t.Fatalf("calling with no base URL returned %v", err)
	}
	if transport.requests != 0 {
		t.Fatalf("calling with no base URL reached the transport %d times", transport.requests)
	}
}

// TestEachFailureShapeProducesADistinctOutcomeAndNoneIsNoData is the table AC15
// asks for: four different failures, four different outcomes, and not one of them
// meaning "there is no data".
func TestEachFailureShapeProducesADistinctOutcomeAndNoneIsNoData(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		body    string
		want    Outcome
		wantOK  bool
		because string
	}{
		{
			name: "HTTP 200 with an empty body", status: http.StatusOK, body: "",
			want: OutcomeEmptyBody,
			because: "the survey names an empty body as a backend failure or a wrong verb, " +
				"never as an absence of data",
		},
		{
			name: "HTTP 200 with a failed result", status: http.StatusOK,
			body: `{"result":"failed","validations":{"example":"example"}}`,
			want: OutcomeResultFailed,
			because: "model validation failures come back as HTTP 200, so HTTP 200 is not " +
				"proof of success",
		},
		{
			name: "HTTP 404", status: http.StatusNotFound,
			body: `{"errorMessage":"Endpoint not found"}`, want: OutcomeNotFound,
			because: "a 404 on a module endpoint is the normal signal that an optional " +
				"component is not installed",
		},
		{
			name: "HTTP 401", status: http.StatusUnauthorized,
			body: `{"errorMessage":"Authentication Failed"}`, want: OutcomeForbidden,
			because: "the credentials failed",
		},
		{
			name: "HTTP 403", status: http.StatusForbidden,
			body: `{"errorMessage":"Forbidden"}`, want: OutcomeForbidden,
			because: "the key owner's ACL does not cover the path",
		},
		{
			name: "HTTP 500", status: http.StatusInternalServerError, body: `{}`,
			want: OutcomeServerError, because: "an uncaught error on the firewall",
		},
		{
			name: "HTTP 200 with an empty array", status: http.StatusOK, body: `[]`,
			want: OutcomeOK, wantOK: true,
			because: "a healthy source with nothing to say is a normal state, and what an " +
				"empty collection means is the collector's decision rather than the transport's",
		},
	}

	seen := map[Outcome]string{}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(
				func(writer http.ResponseWriter, _ *http.Request) {
					writer.WriteHeader(testCase.status)
					_, _ = writer.Write([]byte(testCase.body))
				}))
			defer server.Close()

			client := NewClient(Credentials{BaseURL: server.URL}, nil, time.Second)
			response, err := client.Call(context.Background(), IDSStatus, RequestOptions{})
			if err != nil {
				t.Fatalf("the call failed outright: %v", err)
			}
			if response.Outcome != testCase.want {
				t.Fatalf("got outcome %q, want %q (%s)", response.Outcome, testCase.want, testCase.because)
			}
			if response.OK() != testCase.wantOK {
				t.Fatalf("OK() is %v, want %v", response.OK(), testCase.wantOK)
			}
			if !testCase.wantOK && response.Detail == "" {
				t.Fatalf("a %q outcome carries no detail, so nothing could be recorded about it",
					response.Outcome)
			}
			if !testCase.wantOK && len(response.Body) > 0 && response.OK() {
				t.Fatalf("a %q response reports itself as usable", response.Outcome)
			}
		})
		seen[testCase.want] = testCase.name
	}

	// Every outcome the type declares, except the transport failure exercised below,
	// is reachable and distinct. A collapsed pair would mean two different firewall
	// conditions rendering as one state.
	for _, outcome := range []Outcome{OutcomeOK, OutcomeEmptyBody, OutcomeResultFailed,
		OutcomeNotFound, OutcomeForbidden, OutcomeServerError} {
		if _, reached := seen[outcome]; !reached {
			t.Errorf("no case in this table produces the outcome %q", outcome)
		}
	}
}

// TestATransportFailureIsItsOwnOutcome separates "the firewall did not answer" from
// every answer it could have given.
func TestATransportFailureIsItsOwnOutcome(t *testing.T) {
	client := NewClient(Credentials{BaseURL: "https://firewall.invalid"},
		&countingTransport{}, time.Second)
	response, err := client.Call(context.Background(), IDSStatus, RequestOptions{})
	if err == nil {
		t.Fatal("a transport failure returned no error")
	}
	if response.Outcome != OutcomeTransportFailure {
		t.Fatalf("got outcome %q, want %q", response.Outcome, OutcomeTransportFailure)
	}
	if response.StatusCode != 0 {
		t.Fatalf("a transport failure reported HTTP %d; there was no answer to have a status",
			response.StatusCode)
	}
}

// TestAJSONBodyCarriesIntegersAndTheRightContentType is the call-form guarantee the
// resolver query depends on, checked at the transport boundary.
func TestAJSONBodyCarriesIntegersAndTheRightContentType(t *testing.T) {
	var (
		gotMethod      string
		gotContentType string
		gotBody        string
	)
	server := httptest.NewServer(http.HandlerFunc(
		func(writer http.ResponseWriter, request *http.Request) {
			gotMethod = request.Method
			gotContentType = request.Header.Get("Content-Type")
			buffer := make([]byte, 512)
			read, _ := request.Body.Read(buffer)
			gotBody = string(buffer[:read])
			_, _ = writer.Write([]byte(`{"rows":[]}`))
		}))
	defer server.Close()

	client := NewClient(Credentials{BaseURL: server.URL}, nil, time.Second)
	_, err := client.Call(context.Background(), SearchQueries, RequestOptions{
		JSON: map[string]any{"timeStart": int64(1749989000), "timeEnd": int64(1749990000)},
	})
	if err != nil {
		t.Fatalf("the call failed: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("the request was %s, not POST", gotMethod)
	}
	if gotContentType != "application/json" {
		t.Errorf("the request carried Content-Type %q, not application/json", gotContentType)
	}
	// The bounds must be JSON numbers. Quoted, they would reach the controller as PHP
	// strings, is_int() would fail, and the call would silently degrade.
	for _, quoted := range []string{`"timeStart":"`, `"timeEnd":"`} {
		if strings.Contains(gotBody, quoted) {
			t.Errorf("the body quoted a bound: %s", gotBody)
		}
	}
	for _, want := range []string{`"timeStart":1749989000`, `"timeEnd":1749990000`} {
		if !strings.Contains(gotBody, want) {
			t.Errorf("the body %s does not carry %s as a JSON integer", gotBody, want)
		}
	}
}

// TestAFormBodyIsSentForAGridSearch keeps the grid searches on the encoding the
// survey says both search helpers accept.
func TestAFormBodyIsSentForAGridSearch(t *testing.T) {
	var gotContentType, gotBody string
	server := httptest.NewServer(http.HandlerFunc(
		func(writer http.ResponseWriter, request *http.Request) {
			gotContentType = request.Header.Get("Content-Type")
			buffer := make([]byte, 512)
			read, _ := request.Body.Read(buffer)
			gotBody = string(buffer[:read])
			_, _ = writer.Write([]byte(`{"rows":[]}`))
		}))
	defer server.Close()

	client := NewClient(Credentials{BaseURL: server.URL}, nil, time.Second)
	if _, err := client.Call(context.Background(), SearchRule,
		RequestOptions{Form: Pagination(1, -1)}); err != nil {
		t.Fatalf("the call failed: %v", err)
	}
	if gotContentType != "application/x-www-form-urlencoded" {
		t.Errorf("the grid search carried Content-Type %q", gotContentType)
	}
	parsed, err := url.ParseQuery(gotBody)
	if err != nil {
		t.Fatalf("the form body did not parse: %v", err)
	}
	if parsed.Get("rowCount") != "-1" {
		t.Errorf("the form body asked for rowCount %q, not -1", parsed.Get("rowCount"))
	}
}

// TestPositionalArgumentsAreAppendedVerbatim proves the comma in an interface-name
// list reaches the firewall as a comma. Percent-encoded, it would change which
// interfaces the firewall answers for, and a wrong argument there returns an empty
// array with HTTP 200 — indistinguishable from no traffic.
func TestPositionalArgumentsAreAppendedVerbatim(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(
		func(writer http.ResponseWriter, request *http.Request) {
			gotPath = request.URL.Path
			_, _ = writer.Write([]byte(`{}`))
		}))
	defer server.Close()

	client := NewClient(Credentials{BaseURL: server.URL}, nil, time.Second)
	if _, err := client.Call(context.Background(), TrafficTop,
		RequestOptions{Arguments: []string{"example_if_a,example_if_b"}}); err != nil {
		t.Fatalf("the call failed: %v", err)
	}
	want := TrafficTop.Path + "/example_if_a,example_if_b"
	if gotPath != want {
		t.Fatalf("the request path was %q, want %q", gotPath, want)
	}
}
