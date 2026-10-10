package opnsense

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The tests of the resolver-cache cycle's three endpoints and of the bounded stream read.

// streamTestBound is the bound these tests read with: short, so a test that waits for
// it costs a fraction of a second, and long enough that a server answering at once is
// never cut off by it.
var streamTestBound = StreamBound{Events: 2, Within: 400 * time.Millisecond}

// TestStreamReturnsWithinItsBoundWhenTheServerStreamsForever is the guarantee AC18 asks
// for in its first form: a stream that never ends is read up to the event count and
// closed, rather than waited on until the client's overall timeout.
func TestStreamReturnsWithinItsBoundWhenTheServerStreamsForever(t *testing.T) {
	served := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		defer close(served)
		if request.Header.Get("Accept") != "text/event-stream" {
			t.Errorf("the stream was requested with Accept %q", request.Header.Get("Accept"))
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := writer.(http.Flusher)
		for index := 0; ; index++ {
			if _, err := fmt.Fprintf(writer, "event: message\ndata: {\"total\":%d,\"idle\":%d}\n\n",
				index, 100-index); err != nil {
				return
			}
			flusher.Flush()
			select {
			case <-request.Context().Done():
				return
			case <-time.After(5 * time.Millisecond):
			}
		}
	}))
	defer server.Close()

	client := NewClient(Credentials{BaseURL: server.URL}, nil, 30*time.Second)
	started := time.Now()
	response, err := client.Stream(context.Background(), CPUUsageStream,
		StreamBound{Events: 2, Within: 10 * time.Second})
	elapsed := time.Since(started)
	if err != nil {
		t.Fatalf("the stream read failed: %v", err)
	}
	if response.EndedBy != StreamEndedAtEventLimit || len(response.Events) != 2 {
		t.Fatalf("the read ended by %q with %d events, want the event limit and 2",
			response.EndedBy, len(response.Events))
	}
	if elapsed > 2*time.Second {
		t.Fatalf("the read took %v; a stream that never ends must be closed at the event limit", elapsed)
	}
	if string(response.Events[1]) != `{"total":1,"idle":99}` {
		t.Fatalf("the second event's data is %q", response.Events[1])
	}
	select {
	case <-served:
	case <-time.After(2 * time.Second):
		t.Fatal("the server was still streaming two seconds after the read: the stream was not closed")
	}
}

// TestStreamReturnsWithinItsBoundWhenTheServerSendsNothing is the second form: a stream
// that opens and stays silent ends at the time bound, with no event, and is not
// reported as a transport failure.
func TestStreamReturnsWithinItsBoundWhenTheServerSendsNothing(t *testing.T) {
	for _, headerFirst := range []bool{true, false} {
		name := "headers sent, then silence"
		if !headerFirst {
			name = "no header at all"
		}
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if headerFirst {
					writer.Header().Set("Content-Type", "text/event-stream")
					writer.WriteHeader(http.StatusOK)
					writer.(http.Flusher).Flush()
				}
				<-request.Context().Done()
			}))
			defer server.Close()

			client := NewClient(Credentials{BaseURL: server.URL}, nil, 30*time.Second)
			started := time.Now()
			response, err := client.Stream(context.Background(), CPUUsageStream, streamTestBound)
			elapsed := time.Since(started)
			if err != nil {
				t.Fatalf("a silent stream returned an error, %v; it is no event in time", err)
			}
			if response.EndedBy != StreamEndedAtTimeLimit || len(response.Events) != 0 {
				t.Fatalf("the read ended by %q with %d events, want the time limit and none",
					response.EndedBy, len(response.Events))
			}
			if elapsed > streamTestBound.Within+time.Second {
				t.Fatalf("the read took %v, beyond its bound of %v", elapsed, streamTestBound.Within)
			}
		})
	}
}

// TestStreamBoundStaysBelowTheClientTimeout is risk 5 of the spec: the read's own
// deadline, and not the client's overall timeout, ends a silent stream, so the end is
// "no event in time" rather than a transport failure.
func TestStreamBoundStaysBelowTheClientTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusOK)
		writer.(http.Flusher).Flush()
		<-request.Context().Done()
	}))
	defer server.Close()

	client := NewClient(Credentials{BaseURL: server.URL}, nil, 300*time.Millisecond)
	response, err := client.Stream(context.Background(), CPUUsageStream,
		StreamBound{Events: 2, Within: 10 * time.Second})
	if err != nil {
		t.Fatalf("a bound longer than the client's timeout produced %v; the bound must be clamped", err)
	}
	if response.Outcome != OutcomeOK || response.EndedBy != StreamEndedAtTimeLimit {
		t.Fatalf("got %q ended by %q, want an opened stream ended at the time limit",
			response.Outcome, response.EndedBy)
	}
}

// TestStreamRefusalsAreTheirOwnOutcomes: a denial and a 404 are each their own outcome,
// and neither carries an event.
func TestStreamRefusalsAreTheirOwnOutcomes(t *testing.T) {
	for _, testCase := range []struct {
		status int
		want   Outcome
	}{
		{http.StatusUnauthorized, OutcomeForbidden},
		{http.StatusForbidden, OutcomeForbidden},
		{http.StatusNotFound, OutcomeNotFound},
		{http.StatusInternalServerError, OutcomeServerError},
	} {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.WriteHeader(testCase.status)
			_, _ = writer.Write([]byte(`{"errorMessage":"example"}`))
		}))
		client := NewClient(Credentials{BaseURL: server.URL}, nil, time.Second)
		response, err := client.Stream(context.Background(), CPUUsageStream, streamTestBound)
		server.Close()
		if err != nil {
			t.Fatalf("HTTP %d returned an error, %v, rather than an outcome", testCase.status, err)
		}
		if response.Outcome != testCase.want || len(response.Events) != 0 || response.Detail == "" {
			t.Errorf("HTTP %d gave %q with %d events and detail %q", testCase.status,
				response.Outcome, len(response.Events), response.Detail)
		}
	}
}

// TestStreamReadsTheEventStreamFormat holds the parser to the format: a comment is
// skipped, `event:` does not change the data, and a multi-line data field is joined
// with a newline.
func TestStreamReadsTheEventStreamFormat(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(": a comment\r\nevent: message\r\ndata: first\r\ndata:second\r\n\r\n" +
			"\n\nevent: message\ndata: {\"total\":1}\n\n"))
	}))
	defer server.Close()
	client := NewClient(Credentials{BaseURL: server.URL}, nil, time.Second)
	response, err := client.Stream(context.Background(), CPUUsageStream, StreamBound{Events: 5, Within: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if response.EndedBy != StreamEndedByServer || len(response.Events) != 2 {
		t.Fatalf("ended by %q with %d events", response.EndedBy, len(response.Events))
	}
	if string(response.Events[0]) != "first\nsecond" || string(response.Events[1]) != `{"total":1}` {
		t.Fatalf("the events read are %q and %q", response.Events[0], response.Events[1])
	}
}

// TestCallRefusesAStreamAndStreamRefusesAnOrdinaryEndpoint keeps the two reads apart:
// Call on a stream would wait for the client's timeout, and Stream on an ordinary
// answer would count no event.
func TestCallRefusesAStreamAndStreamRefusesAnOrdinaryEndpoint(t *testing.T) {
	transport := &countingTransport{}
	client := NewClient(Credentials{BaseURL: "https://firewall.invalid"}, transport, time.Second)
	if _, err := client.Call(context.Background(), CPUUsageStream, RequestOptions{}); !errors.Is(err, ErrStreamEndpoint) {
		t.Errorf("Call on the stream returned %v", err)
	}
	if _, err := client.Stream(context.Background(), SystemTime, streamTestBound); !errors.Is(err, ErrStreamEndpoint) {
		t.Errorf("Stream on an ordinary endpoint returned %v", err)
	}
	invented := CPUUsageStream
	invented.Path = "/api/invented/controller/stream"
	if _, err := client.Stream(context.Background(), invented, streamTestBound); !errors.Is(err, ErrEndpointNotRegistered) {
		t.Errorf("Stream on an unregistered path returned %v", err)
	}
	if transport.requests != 0 {
		t.Fatalf("a refused read reached the transport %d times", transport.requests)
	}
}

// TestOnlyTheProcessorEndpointStreams pins the one endpoint read with Stream.
func TestOnlyTheProcessorEndpointStreams(t *testing.T) {
	for _, endpoint := range Registry() {
		if endpoint.Stream != (endpoint.Path == CPUUsageStream.Path) {
			t.Errorf("%s declares Stream %v", endpoint.Path, endpoint.Stream)
		}
	}
}

// TestTheRegistryAddsExactlyTheResolverCacheCycleEndpoints is AC3's registry half: the
// three endpoints this cycle adds are registered, GET, and cite their 26.7.3 controller,
// configd action file, script and ACL page; and getActivity, which carries no processor
// figure, is removed (AC3 as amended by specs/SPEC-resolver-cache-closing.md).
func TestTheRegistryAddsExactlyTheResolverCacheCycleEndpoints(t *testing.T) {
	added := map[string]Endpoint{}
	for _, endpoint := range Registry() {
		added[endpoint.Path] = endpoint
	}
	for _, want := range []struct {
		path      string
		citations []string
	}{
		{"/api/unbound/diagnostics/dumpcache", []string{
			"26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Unbound/Api/DiagnosticsController.php",
			"actions_unbound.conf", "scripts/unbound/wrapper.py", "OPNsense/Unbound/ACL/ACL.xml"}},
		{"/api/unbound/diagnostics/listlocaldata", []string{
			"26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Unbound/Api/DiagnosticsController.php",
			"actions_unbound.conf", "scripts/unbound/wrapper.py", "OPNsense/Unbound/ACL/ACL.xml"}},
		{"/api/diagnostics/cpu_usage/stream", []string{
			"26.7.3/src/opnsense/mvc/app/controllers/OPNsense/Diagnostics/Api/CpuUsageController.php",
			"actions_system.conf", "scripts/system/cpu.py", "OPNsense/Core/ACL/ACL.xml"}},
	} {
		endpoint, present := added[want.path]
		if !present {
			t.Errorf("%s is not registered", want.path)
			continue
		}
		if endpoint.Method != http.MethodGet {
			t.Errorf("%s is %s; the controller action takes a GET", want.path, endpoint.Method)
		}
		cited := endpoint.UpstreamURL + " " + endpoint.Note
		for _, citation := range want.citations {
			if !strings.Contains(cited, citation) {
				t.Errorf("%s does not cite %s", want.path, citation)
			}
		}
	}
	if got := len(Registry()); got != 34 {
		t.Errorf("the registry holds %d endpoints; 32 before this cycle, the three it adds and the "+
			"getActivity entry it removes make 34", got)
	}
}

// TestTheUnboundControllersMutatingActionsAreRefused is AC2's mutating half. Every
// action of Unbound/Api/DiagnosticsController.php at 26.7.3 is listed with what it
// does, and so are the two Unbound/Api/ServiceController.php actions whose names the
// generic list did not hold. The diagnostics actions read and must pass; the two
// service actions write and must be refused.
func TestTheUnboundControllersMutatingActionsAreRefused(t *testing.T) {
	for _, action := range []struct {
		path     string
		mutating bool
	}{
		{"/api/unbound/diagnostics/stats", false},
		{"/api/unbound/diagnostics/dumpcache", false},
		{"/api/unbound/diagnostics/dumpinfra", false},
		{"/api/unbound/diagnostics/listlocaldata", false},
		{"/api/unbound/diagnostics/listlocalzones", false},
		{"/api/unbound/diagnostics/listinsecure", false},
		{"/api/unbound/diagnostics/test_blocklist", false},
		{"/api/unbound/service/dnsbl", true},
		{"/api/unbound/service/reconfigure_general", true},
		{"/api/unbound/service/reconfigure", true},
		{"/api/unbound/service/restart", true},
	} {
		got := MutatingCommand(action.path) != ""
		if got != action.mutating {
			t.Errorf("%s: mutating %v, want %v", action.path, got, action.mutating)
		}
	}
}
