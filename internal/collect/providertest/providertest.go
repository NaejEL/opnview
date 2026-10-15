package providertest

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/broker"
	"github.com/NaejEL/opnview/internal/collect/provider"
	"github.com/NaejEL/opnview/internal/decode"
	"github.com/NaejEL/opnview/internal/opnsense"
)

// reply is one canned answer.
type reply struct {
	status int
	body   []byte
}

// Recorded is one request the fake received.
type Recorded struct {
	Method      string
	Path        string
	ContentType string
	Query       url.Values
	Body        []byte
	// EndpointPath is the registry path the request resolved to, or the empty string when it
	// resolved to none -- which is a test failure.
	EndpointPath string
	// Arguments are the positional path elements after the command.
	Arguments []string
}

// Firewall answers the registry's paths and records what was asked.
type Firewall struct {
	t      *testing.T
	server *httptest.Server

	mutex    sync.Mutex
	replies  map[string]reply
	streams  map[string]streamReply
	requests []Recorded
	unknown  []string
}

// streamReply is a canned server-sent event stream: the events' data, each written as
// "event: message", "data: <data>" and a blank line, and then either the end of the stream or,
// when forever is set, silence until the reader closes the connection -- which is what the
// firewall's stream does.
type streamReply struct {
	events  [][]byte
	forever bool
}

// NewFirewall starts a fake. Every registry path answers 404 until a reply is configured for it,
// which is the honest default: an endpoint the test did not arrange is a component that is not
// installed.
func NewFirewall(t *testing.T) *Firewall {
	t.Helper()
	fake := &Firewall{t: t, replies: map[string]reply{}}
	fake.server = httptest.NewServer(http.HandlerFunc(fake.serve))
	t.Cleanup(fake.server.Close)
	return fake
}

// URL is the fake's address.
func (f *Firewall) URL() string { return f.server.URL }

// serve routes one request to the longest registry path that prefixes it.
func (f *Firewall) serve(writer http.ResponseWriter, request *http.Request) {
	body := make([]byte, 0)
	if request.Body != nil {
		read, err := io.ReadAll(request.Body)
		if err == nil {
			body = read
		}
	}

	matched := ""
	for _, endpoint := range opnsense.Endpoints() {
		if request.URL.Path == endpoint.Path ||
			strings.HasPrefix(request.URL.Path, endpoint.Path+"/") {
			if len(endpoint.Path) > len(matched) {
				matched = endpoint.Path
			}
		}
	}

	var arguments []string
	if matched != "" && len(request.URL.Path) > len(matched) {
		arguments = strings.Split(strings.TrimPrefix(request.URL.Path, matched+"/"), "/")
	}

	f.mutex.Lock()
	f.requests = append(f.requests, Recorded{
		Method:       request.Method,
		Path:         request.URL.Path,
		ContentType:  request.Header.Get("Content-Type"),
		Query:        request.URL.Query(),
		Body:         body,
		EndpointPath: matched,
		Arguments:    arguments,
	})
	if matched == "" {
		f.unknown = append(f.unknown, request.URL.Path)
	}
	answer, configured := f.replies[matched]
	stream, streams := f.streams[matched]
	f.mutex.Unlock()

	if matched != "" && streams {
		writer.Header().Set("Content-Type", "text/event-stream")
		writer.WriteHeader(http.StatusOK)
		flusher, _ := writer.(http.Flusher)
		for _, event := range stream.events {
			_, _ = writer.Write([]byte("event: message\ndata: " + string(event) + "\n\n"))
		}
		if flusher != nil {
			flusher.Flush()
		}
		if stream.forever {
			<-request.Context().Done()
		}
		return
	}
	if matched == "" || !configured {
		writer.WriteHeader(http.StatusNotFound)
		_, _ = writer.Write([]byte(`{"errorMessage":"Endpoint not found","errorTitle":"Not Found"}`))
		return
	}
	writer.WriteHeader(answer.status)
	_, _ = writer.Write(answer.body)
}

// Answer configures the reply for one endpoint.
func (f *Firewall) Answer(endpoint opnsense.Endpoint, status int, body []byte) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.replies[endpoint.Path] = reply{status: status, body: body}
}

// AnswerStream configures a server-sent event stream for one endpoint.
func (f *Firewall) AnswerStream(endpoint opnsense.Endpoint, events [][]byte, forever bool) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	if f.streams == nil {
		f.streams = map[string]streamReply{}
	}
	f.streams[endpoint.Path] = streamReply{events: events, forever: forever}
}

// AnswerStreamFixture configures a stream from a labelled fixture whose body is
// {"events": [...]}: one event per element, the element as its data, and the stream then held
// open, as the firewall's is.
func (f *Firewall) AnswerStreamFixture(endpoint opnsense.Endpoint, name string) {
	f.t.Helper()
	var body struct {
		Events []json.RawMessage `json:"events"`
	}
	if err := json.Unmarshal(FixtureBody(f.t, name), &body); err != nil {
		f.t.Fatalf("decoding the stream fixture %s: %v", name, err)
	}
	events := make([][]byte, 0, len(body.Events))
	for _, event := range body.Events {
		// One line per event, as the script prints it: a data line cannot hold a newline.
		var compact bytes.Buffer
		if err := json.Compact(&compact, event); err != nil {
			f.t.Fatalf("compacting an event of %s: %v", name, err)
		}
		events = append(events, compact.Bytes())
	}
	f.AnswerStream(endpoint, events, true)
}

// AnswerFixture configures the reply for one endpoint from a labelled fixture.
func (f *Firewall) AnswerFixture(endpoint opnsense.Endpoint, name string) {
	f.t.Helper()
	f.Answer(endpoint, http.StatusOK, FixtureBody(f.t, name))
}

// AnswerJSON configures the reply from a value encoded here. It is used only for bodies a
// fixture would say nothing more about than the value itself -- an empty array, a status object a
// test is varying on purpose -- and the reason is recorded at each call site.
func (f *Firewall) AnswerJSON(endpoint opnsense.Endpoint, value any) {
	f.t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		f.t.Fatalf("encoding a fake answer for %s: %v", endpoint.Path, err)
	}
	f.Answer(endpoint, http.StatusOK, encoded)
}

// RequestsTo returns the requests made to one endpoint.
func (f *Firewall) RequestsTo(endpoint opnsense.Endpoint) []Recorded {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	var matched []Recorded
	for _, request := range f.requests {
		if request.EndpointPath == endpoint.Path {
			matched = append(matched, request)
		}
	}
	return matched
}

// RequestCount returns how many requests the fake received in total.
func (f *Firewall) RequestCount() int {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	return len(f.requests)
}

// AssertEveryPathIsRegistered fails if any request resolved to no registry entry. It is the
// recording half of the registry guarantee: the client refuses an unregistered Endpoint value,
// and this refuses an unregistered PATH, so neither a hand-built struct nor a hand-built URL can
// slip through.
func (f *Firewall) AssertEveryPathIsRegistered() {
	f.t.Helper()
	f.mutex.Lock()
	defer f.mutex.Unlock()
	if len(f.unknown) > 0 {
		f.t.Fatalf("these requested paths are in no registry entry: %v", f.unknown)
	}
}

// firewallOnlyTransport refuses any host but the fake firewall.
//
// It is what makes "one outbound destination" a property a test can fail on. A second
// destination -- a CDN, a telemetry endpoint, a MaxMind download that wandered out of
// internal/maxmind -- does not merely get noticed: the call errors.
type firewallOnlyTransport struct {
	t           *testing.T
	allowedHost string
	inner       http.RoundTripper
}

// RoundTrip refuses a request to any other host.
func (transport *firewallOnlyTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.URL.Host != transport.allowedHost {
		transport.t.Errorf("a request left for %s, which is not the configured firewall %s",
			request.URL.Host, transport.allowedHost)
		return nil, &url.Error{
			Op:  request.Method,
			URL: request.URL.String(),
			Err: os.ErrPermission,
		}
	}
	return transport.inner.RoundTrip(request)
}

// NewClient returns a client pointed at the fake, through a transport that refuses every other
// host, with credentials generated here so none exists in the repository.
func NewClient(t *testing.T, fake *Firewall) *opnsense.Client {
	t.Helper()
	target, err := url.Parse(fake.server.URL)
	if err != nil {
		t.Fatalf("parsing the fake firewall address: %v", err)
	}
	return opnsense.NewClient(
		opnsense.Credentials{
			BaseURL:   fake.server.URL,
			APIKey:    randomCredential(t),
			APISecret: randomCredential(t),
		},
		&firewallOnlyTransport{t: t, allowedHost: target.Host, inner: http.DefaultTransport},
		5*time.Second,
	)
}

// randomCredential generates a credential for one test run.
func randomCredential(t *testing.T) string {
	t.Helper()
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		t.Fatalf("generating a test credential: %v", err)
	}
	return hex.EncodeToString(raw)
}

// NewSession returns the provider.Session an implementation is handed in production, over the
// fake: the same Host code the collector embeds, with a firewall on UTC (offset zero) and a clock
// stopped at the given instant. It is what an implementation's own tests call its Probe and its
// read methods with.
func NewSession(t *testing.T, fake *Firewall, at time.Time) provider.Session {
	t.Helper()
	gate := broker.New(NewClient(t, fake), nil, broker.Options{})
	t.Cleanup(gate.Close)
	return provider.NewHost(gate,
		func(context.Context) (decode.Normaliser, error) {
			return decode.Normaliser{Reference: at}, nil
		},
		func() int64 { return at.UTC().Unix() })
}

// FixtureFile is the on-disk shape of a labelled fixture: the provenance, and the response body
// the fake serves.
type FixtureFile struct {
	Provenance struct {
		SynthesisedFrom       string `json:"synthesised_from"`
		CapturedFromAFirewall bool   `json:"captured_from_a_firewall"`
		Statement             string `json:"statement"`
		SurveySection         string `json:"survey_section"`
		FieldListFrom         string `json:"field_list_from"`
		WhyTheseValues        string `json:"why_these_values"`
		ExampleValues         string `json:"example_values_are_not_a_network"`
	} `json:"__provenance__"`
	Body json.RawMessage `json:"body"`
}

var (
	fixtureOnce  sync.Once
	fixtureIndex map[string][]string
	fixtureErr   error
)

// fixtureRoots returns the directories fixtures live below -- internal/collect, and plugins/, where
// each built-in plugin keeps the fixtures of its own answers -- found from the working directory
// of the test.
func fixtureRoots() ([]string, error) {
	current, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	for {
		if _, err := os.Stat(filepath.Join(current, "go.mod")); err == nil {
			return []string{filepath.Join(current, "internal", "collect"), filepath.Join(current, "plugins")}, nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return nil, fmt.Errorf("providertest: no go.mod above the working directory")
		}
		current = parent
	}
}

// indexFixtures walks every testdata directory below the fixture roots once.
func indexFixtures() {
	roots, err := fixtureRoots()
	if err != nil {
		fixtureErr = err
		return
	}
	fixtureIndex = map[string][]string{}
	for _, root := range roots {
		if fixtureErr = walkFixtures(root); fixtureErr != nil {
			return
		}
	}
}

// walkFixtures indexes the fixtures below one root.
func walkFixtures(root string) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			return nil
		}
		if filepath.Base(filepath.Dir(path)) != "testdata" {
			return nil
		}
		fixtureIndex[entry.Name()] = append(fixtureIndex[entry.Name()], path)
		return nil
	})
}

// FixturePath resolves a fixture by file name to its path.
func FixturePath(t *testing.T, name string) string {
	t.Helper()
	fixtureOnce.Do(indexFixtures)
	if fixtureErr != nil {
		t.Fatalf("listing the fixtures: %v", fixtureErr)
	}
	paths := fixtureIndex[name]
	switch len(paths) {
	case 0:
		t.Fatalf("the fixture %s is in no testdata directory below internal/collect", name)
	case 1:
		return paths[0]
	}
	t.Fatalf("the fixture name %s exists in several testdata directories (%v); a name must be unique "+
		"so a test never has to guess which one it means", name, paths)
	return ""
}

// FixtureBody returns one fixture's response body.
func FixtureBody(t *testing.T, name string) []byte {
	t.Helper()
	return ReadFixture(t, name).Body
}

// ReadFixture reads and decodes one fixture.
func ReadFixture(t *testing.T, name string) FixtureFile {
	t.Helper()
	raw, err := os.ReadFile(FixturePath(t, name))
	if err != nil {
		t.Fatalf("reading the fixture %s: %v", name, err)
	}
	var decoded FixtureFile
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decoding the fixture %s: %v", name, err)
	}
	return decoded
}

// FixtureNames lists every fixture file below internal/collect, sorted.
func FixtureNames(t *testing.T) []string {
	t.Helper()
	fixtureOnce.Do(indexFixtures)
	if fixtureErr != nil {
		t.Fatalf("listing the fixtures: %v", fixtureErr)
	}
	names := make([]string, 0, len(fixtureIndex))
	for name := range fixtureIndex {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Requests returns a snapshot of every request the fake received, in order.
func (f *Firewall) Requests() []Recorded {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	return append([]Recorded(nil), f.requests...)
}

// RemoveStream drops the stream configured for an endpoint, so a plain answer can replace it.
func (f *Firewall) RemoveStream(endpoint opnsense.Endpoint) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	delete(f.streams, endpoint.Path)
}
