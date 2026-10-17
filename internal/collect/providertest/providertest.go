package providertest

import (
	"bytes"
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
	// APIKey and APISecret are the Basic-auth halves the request carried: what a test of a
	// credential change asserts on, since the pass after it must carry the new value.
	APIKey    string
	APISecret string
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
	// fallback, when set, answers every request no reply is configured for, and every request
	// at all while its status is not 200 (NewAnsweringFirewall).
	fallback *reply
	// down makes the fake refuse to answer at all, which is the unreachable-host case: the
	// transport fails rather than a status coming back.
	down bool
	// handler answers every request no reply, stream or feed is configured for (AnswerWith).
	handler func(request *http.Request, body string) (int, string, string)
	// feeds serve a path from a channel, one event per value; open counts the connections held
	// open to each, and refuse answers the next connections to one with a server error.
	feeds  map[string]chan string
	open   map[string]int
	refuse map[string]int
}

// streamReply is a canned server-sent event stream: the events' data, each written as
// "event: message", "data: <data>" and a blank line, and then either the end of the stream or,
// when forever is set, the connection held open until the reader closes it -- which is what the
// firewall's stream does. Held open, it is silent, unless repeatLast is set: then the last event
// is written again every streamRepeatInterval, as the firewall's script goes on printing a reading
// every interval for as long as the connection lasts.
type streamReply struct {
	events     [][]byte
	forever    bool
	repeatLast bool
}

// streamRepeatInterval is how often a held stream with repeatLast writes its last event again.
// The firewall's script prints one reading a second; a test reads at a frozen instant and waits
// on the wall clock, so the fake prints faster, and a read taking the next events after the
// connection's first ones finds them at once rather than at the end of its bound.
const streamRepeatInterval = 50 * time.Millisecond

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

// NewAnsweringFirewall starts a fake that answers every request it holds no reply for with one
// default answer -- an empty search envelope until AnswerAll changes it -- rather than 404: the
// firewall a test of the web surfaces needs, which reads several endpoints and is about none of
// them. get_interface_names answers an empty map, its own shape, as a firewall with nothing
// discovered does. overTLS serves it under a certificate httptest generates, which no system root
// vouches for -- the shape of a firewall as OPNsense ships it.
func NewAnsweringFirewall(t *testing.T, overTLS bool) *Firewall {
	t.Helper()
	fake := &Firewall{t: t, replies: map[string]reply{},
		fallback: &reply{status: http.StatusOK, body: []byte(`{"rows":[],"total":0}`)}}
	fake.replies[opnsense.InterfaceNames.Path] = reply{status: http.StatusOK, body: []byte(`{}`)}
	if overTLS {
		fake.server = httptest.NewTLSServer(http.HandlerFunc(fake.serve))
	} else {
		fake.server = httptest.NewServer(http.HandlerFunc(fake.serve))
	}
	t.Cleanup(fake.server.Close)
	return fake
}

// AnswerAll sets the default answer of an answering fake, and brings a fake that went down back
// up. A status other than 200 answers every request, the replies configured per endpoint included.
func (f *Firewall) AnswerAll(status int, body []byte) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.fallback, f.down = &reply{status: status, body: body}, false
}

// GoDown makes the fake stop answering.
func (f *Firewall) GoDown() {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.down = true
}

// Last returns the most recent request, failing if there is none.
func (f *Firewall) Last() Recorded {
	f.t.Helper()
	received := f.Requests()
	if len(received) == 0 {
		f.t.Fatal("the fake firewall received no request, so this assertion is vacuous")
	}
	return received[len(received)-1]
}

// BaseURL is the fake's address: what a test types into the firewall URL field.
func (f *Firewall) BaseURL() string { return f.server.URL }

// Host is the fake's host:port, chosen by the operating system at run time.
func (f *Firewall) Host() string {
	f.t.Helper()
	target, err := url.Parse(f.server.URL)
	if err != nil {
		f.t.Fatalf("parsing the fake firewall address: %v", err)
	}
	return target.Host
}

// Fingerprint is the SHA-256 fingerprint of the certificate this fake presents, or the empty
// string when it speaks plain HTTP: what an operator reads off OPNsense's own interface.
func (f *Firewall) Fingerprint() string {
	if f.server.TLS == nil || len(f.server.Certificate().Raw) == 0 {
		return ""
	}
	return opnsense.CertificateFingerprint(f.server.Certificate())
}

// FirewallOnly is a transport that refuses any host but the fake's, failing the test, in front of
// inner: what makes "one outbound destination" a property a test can fail on.
func FirewallOnly(t *testing.T, fake *Firewall, inner http.RoundTripper) http.RoundTripper {
	return &firewallOnlyTransport{t: t, allowedHost: fake.Host(), inner: inner}
}

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

	apiKey, apiSecret, _ := request.BasicAuth()
	f.mutex.Lock()
	f.requests = append(f.requests, Recorded{
		APIKey:       apiKey,
		APISecret:    apiSecret,
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
	fallback, down, handler := f.fallback, f.down, f.handler
	feed := f.feeds[request.URL.Path]
	refused := feed != nil && f.refuse[request.URL.Path] > 0
	if refused {
		f.refuse[request.URL.Path]--
	} else if feed != nil {
		f.open[request.URL.Path]++
	}
	f.mutex.Unlock()

	if refused {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusServiceUnavailable)
		_, _ = writer.Write([]byte(`{}`))
		return
	}
	if feed != nil {
		f.serveFeed(writer, request, feed)
		return
	}

	if down {
		// Hijack and close without answering, so the client sees no response at all -- which is
		// what an unreachable host looks like from the transport's side.
		hijacker, ok := writer.(http.Hijacker)
		if !ok {
			f.t.Error("the fake cannot simulate an unreachable host on this server")
			return
		}
		if connection, _, err := hijacker.Hijack(); err == nil {
			_ = connection.Close()
		}
		return
	}
	if fallback != nil && (fallback.status != http.StatusOK || matched == "" || !configured) && !streams {
		writer.WriteHeader(fallback.status)
		_, _ = writer.Write(fallback.body)
		return
	}

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
		if !stream.forever {
			return
		}
		if !stream.repeatLast || len(stream.events) == 0 {
			<-request.Context().Done()
			return
		}
		last := stream.events[len(stream.events)-1]
		ticker := time.NewTicker(streamRepeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-request.Context().Done():
				return
			case <-ticker.C:
				if _, err := writer.Write([]byte("event: message\ndata: " + string(last) + "\n\n")); err != nil {
					return
				}
				if flusher != nil {
					flusher.Flush()
				}
			}
		}
	}
	if (matched == "" || !configured) && handler != nil {
		status, contentType, answered := handler(request, string(body))
		writer.Header().Set("Content-Type", contentType)
		writer.WriteHeader(status)
		_, _ = writer.Write([]byte(answered))
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
// open, as the firewall's is, its last event written again every streamRepeatInterval as the
// firewall's script goes on printing readings. The first events of a connection are the
// fixture's, in order, so a read taking the first events of a connection reads exactly them; a
// later read on the same connection reads the repeated last event, which is what a reading after
// the first one looks like.
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
	f.mutex.Lock()
	defer f.mutex.Unlock()
	if f.streams == nil {
		f.streams = map[string]streamReply{}
	}
	f.streams[endpoint.Path] = streamReply{events: events, forever: true, repeatLast: true}
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
// the plugin host's fetch facility -- does not merely get noticed: the call errors.
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
	return opnsense.NewClient(
		opnsense.Credentials{
			BaseURL:   fake.server.URL,
			APIKey:    randomCredential(t),
			APISecret: randomCredential(t),
		},
		FirewallOnly(t, fake, http.DefaultTransport),
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

// serveFeed writes a fed stream: each value of the channel as "data: <value>" and a blank line,
// until the channel closes or the reader goes, the connection counted open meanwhile.
func (f *Firewall) serveFeed(writer http.ResponseWriter, request *http.Request, feed chan string) {
	defer func() {
		f.mutex.Lock()
		f.open[request.URL.Path]--
		f.mutex.Unlock()
	}()
	writer.Header().Set("Content-Type", "text/event-stream")
	writer.WriteHeader(http.StatusOK)
	flusher, _ := writer.(http.Flusher)
	if flusher != nil {
		flusher.Flush()
	}
	for {
		select {
		case event, open := <-feed:
			if !open {
				return
			}
			_, _ = writer.Write([]byte("data: " + event + "\n\n"))
			if flusher != nil {
				flusher.Flush()
			}
		case <-request.Context().Done():
			return
		}
	}
}

// AnswerWith hands every request no reply, stream or feed is configured for to a handler, which
// returns the status, the content type and the body: the fake a test of the broker needs, whose
// paths are the ones it learns rather than the registry's.
func (f *Firewall) AnswerWith(handler func(request *http.Request, body string) (int, string, string)) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.handler = handler
}

// Feed serves a path, exactly, from a channel: each value is one event of a server-sent event
// stream, until the channel closes. A later Feed of the same path serves its next connections.
func (f *Firewall) Feed(path string, events chan string) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.makeFeeds()
	f.feeds[path] = events
}

// RefuseFeed answers the next count connections to a fed path with a server error.
func (f *Firewall) RefuseFeed(path string, count int) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.makeFeeds()
	f.refuse[path] += count
}

// makeFeeds makes the feeds' maps; the caller holds the mutex.
func (f *Firewall) makeFeeds() {
	if f.feeds == nil {
		f.feeds, f.open, f.refuse = map[string]chan string{}, map[string]int{}, map[string]int{}
	}
}

// OpenFeeds is how many connections the fake holds open to a fed path.
func (f *Firewall) OpenFeeds(path string) int {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	return f.open[path]
}

// CountTo is how many requests reached a path, or every path for the empty one.
func (f *Firewall) CountTo(path string) int {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	count := 0
	for _, request := range f.requests {
		if path == "" || request.Path == path {
			count++
		}
	}
	return count
}

// LastTo is the most recent request to a path, or the zero Recorded when none reached it.
func (f *Firewall) LastTo(path string) Recorded {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	for index := len(f.requests) - 1; index >= 0; index-- {
		if f.requests[index].Path == path {
			return f.requests[index]
		}
	}
	return Recorded{}
}
