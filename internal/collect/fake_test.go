package collect

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/store"
)

// The fake firewall every collector test runs against.
//
// IT IS BUILT FROM A DOCUMENT, AND THAT IS THE LIMIT OF WHAT THESE TESTS PROVE.
// Cycle 4A cannot reach an OPNsense: credentials are entered in opnview's own
// interface, which is cycle 4B. So every response below comes from a fixture
// synthesised by reading docs/opnsense-api-survey.md, each fixture says so in its
// own file, and if the survey is wrong about a field, an envelope or a status code
// these tests pass and the collection is still wrong. Only the end of 4B closes
// that.
//
// What the fake does enforce, and what a document cannot get wrong:
//
//   - every path ever requested is a registry entry, so an invented endpoint fails
//     the test rather than reaching a firewall;
//   - the transport refuses any host but the fake, so a second outbound destination
//     fails the test rather than being noticed later;
//   - the credentials are generated per test, so no key or secret exists in the
//     repository to leak.

// fixtureRoot is where the labelled response fixtures live.
const fixtureRoot = "testdata"

// reply is one canned answer.
type reply struct {
	status int
	body   []byte
}

// recorded is one request the fake received.
type recorded struct {
	method      string
	path        string
	contentType string
	query       url.Values
	body        []byte
	// endpointPath is the registry path the request resolved to, or the empty
	// string when it resolved to none — which is a test failure.
	endpointPath string
	// arguments are the positional path elements after the command.
	arguments []string
}

// fakeFirewall answers the registry's paths and records what was asked.
type fakeFirewall struct {
	t      *testing.T
	server *httptest.Server

	mutex    sync.Mutex
	replies  map[string]reply
	requests []recorded
	unknown  []string
}

// newFakeFirewall starts a fake. Every registry path answers 404 until a reply is
// configured for it, which is the honest default: an endpoint the test did not
// arrange is a component that is not installed.
func newFakeFirewall(t *testing.T) *fakeFirewall {
	t.Helper()
	fake := &fakeFirewall{t: t, replies: map[string]reply{}}
	fake.server = httptest.NewServer(http.HandlerFunc(fake.serve))
	t.Cleanup(fake.server.Close)
	return fake
}

// serve routes one request to the longest registry path that prefixes it.
func (f *fakeFirewall) serve(writer http.ResponseWriter, request *http.Request) {
	body := make([]byte, 0)
	if request.Body != nil {
		read, err := io.ReadAll(request.Body)
		if err == nil {
			body = read
		}
	}

	matched := ""
	for _, endpoint := range opnsense.Registry() {
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
	f.requests = append(f.requests, recorded{
		method:       request.Method,
		path:         request.URL.Path,
		contentType:  request.Header.Get("Content-Type"),
		query:        request.URL.Query(),
		body:         body,
		endpointPath: matched,
		arguments:    arguments,
	})
	if matched == "" {
		f.unknown = append(f.unknown, request.URL.Path)
	}
	answer, configured := f.replies[matched]
	f.mutex.Unlock()

	if matched == "" || !configured {
		writer.WriteHeader(http.StatusNotFound)
		_, _ = writer.Write([]byte(`{"errorMessage":"Endpoint not found","errorTitle":"Not Found"}`))
		return
	}
	writer.WriteHeader(answer.status)
	_, _ = writer.Write(answer.body)
}

// answer configures the reply for one endpoint.
func (f *fakeFirewall) answer(endpoint opnsense.Endpoint, status int, body []byte) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.replies[endpoint.Path] = reply{status: status, body: body}
}

// answerFixture configures the reply for one endpoint from a labelled fixture.
func (f *fakeFirewall) answerFixture(endpoint opnsense.Endpoint, name string) {
	f.answer(endpoint, http.StatusOK, fixtureBody(f.t, name))
}

// answerJSON configures the reply from a value encoded here. It is used only for
// bodies a fixture would say nothing more about than the value itself — an empty
// array, a status object a test is varying on purpose — and the reason is recorded
// at each call site.
func (f *fakeFirewall) answerJSON(endpoint opnsense.Endpoint, value any) {
	f.t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		f.t.Fatalf("encoding a fake answer for %s: %v", endpoint.Path, err)
	}
	f.answer(endpoint, http.StatusOK, encoded)
}

// requestsTo returns the requests made to one endpoint.
func (f *fakeFirewall) requestsTo(endpoint opnsense.Endpoint) []recorded {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	var matched []recorded
	for _, request := range f.requests {
		if request.endpointPath == endpoint.Path {
			matched = append(matched, request)
		}
	}
	return matched
}

// requestCount returns how many requests the fake received in total.
func (f *fakeFirewall) requestCount() int {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	return len(f.requests)
}

// assertEveryPathIsRegistered fails if any request resolved to no registry entry.
// It is the recording half of the registry guarantee: the client refuses an
// unregistered Endpoint value, and this refuses an unregistered PATH, so neither a
// hand-built struct nor a hand-built URL can slip through.
func (f *fakeFirewall) assertEveryPathIsRegistered() {
	f.t.Helper()
	f.mutex.Lock()
	defer f.mutex.Unlock()
	if len(f.unknown) > 0 {
		f.t.Fatalf("these requested paths are in no registry entry: %v", f.unknown)
	}
}

// firewallOnlyTransport refuses any host but the fake firewall.
//
// It is what makes "one outbound destination" a property a test can fail on. A
// second destination — a CDN, a telemetry endpoint, a MaxMind download that
// wandered into this cycle — does not merely get noticed: the call errors.
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

// newFakeClient returns a client pointed at the fake, through a transport that
// refuses every other host, with credentials generated here so none exists in the
// repository.
func newFakeClient(t *testing.T, fake *fakeFirewall) *opnsense.Client {
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

// fixtureFile is the on-disk shape of a labelled fixture: the provenance, and the
// response body the fake serves.
type fixtureFile struct {
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

// fixtureBody returns one fixture's response body.
func fixtureBody(t *testing.T, name string) []byte {
	t.Helper()
	return readFixture(t, name).Body
}

// readFixture reads and decodes one fixture.
func readFixture(t *testing.T, name string) fixtureFile {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(fixtureRoot, name))
	if err != nil {
		t.Fatalf("reading the fixture %s: %v", name, err)
	}
	var decoded fixtureFile
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decoding the fixture %s: %v", name, err)
	}
	return decoded
}

// fixtureNames lists every fixture file.
func fixtureNames(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(fixtureRoot)
	if err != nil {
		t.Fatalf("listing the fixtures: %v", err)
	}
	var names []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	return names
}

// fixedClock is a clock a test drives. It never advances on its own, so nothing in a
// test sleeps and nothing depends on how fast the machine is.
//
// Each call to After hands out ITS OWN channel, and that matters: a single shared
// channel would let whichever loop re-selects first take every release, and a test
// built on it would report that the other loops had stalled when they had merely not
// been woken. One channel per waiter makes "release every waiting loop once" a thing a
// test can actually do.
type fixedClock struct {
	mutex   sync.Mutex
	now     time.Time
	waiters []chan time.Time
}

// newFixedClock returns a clock stopped at an instant.
func newFixedClock(at time.Time) *fixedClock {
	return &fixedClock{now: at}
}

// Now returns the clock's instant.
func (c *fixedClock) Now() time.Time {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return c.now
}

// After registers a waiter and returns its channel.
func (c *fixedClock) After(time.Duration) <-chan time.Time {
	channel := make(chan time.Time, 1)
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.waiters = append(c.waiters, channel)
	return channel
}

// waiting reports how many loops are currently waiting for a tick.
func (c *fixedClock) waiting() int {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return len(c.waiters)
}

// releaseAll wakes every loop that is currently waiting, exactly once each, and returns
// how many it woke. The channels are buffered, so it never blocks on a loop that has
// already stopped.
func (c *fixedClock) releaseAll() int {
	c.mutex.Lock()
	waiters := c.waiters
	c.waiters = nil
	instant := c.now
	c.mutex.Unlock()

	for _, channel := range waiters {
		channel <- instant
	}
	return len(waiters)
}

// awaitWaiters blocks until at least count loops are waiting for a tick, or fails the
// test. It is how a test synchronises with the scheduler without sleeping for a guessed
// duration.
func (c *fixedClock) awaitWaiters(t *testing.T, count int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for c.waiting() < count {
		if time.Now().After(deadline) {
			t.Fatalf("only %d of %d loops are waiting for a tick after five seconds",
				c.waiting(), count)
		}
		runtime.Gosched()
	}
}

// advance moves the clock forward.
func (c *fixedClock) advance(by time.Duration) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.now = c.now.Add(by)
}

// newTestStore opens a database under t.TempDir, so no test ever writes a database,
// a -wal or a -shm file into the working tree.
func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	database, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("opening the test database: %v", err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("closing the test database: %v", err)
		}
	})
	return database
}

// countRows counts a table, failing the test on an error.
func countRows(t *testing.T, database *store.Store, table string) int {
	t.Helper()
	count, err := database.CountRows(context.Background(), table)
	if err != nil {
		t.Fatalf("counting %s: %v", table, err)
	}
	return count
}

// arrangeDiscoverableFirewall configures the fake with the discovery responses and
// runs one discovery pass, which is the precondition of every collector.
func arrangeDiscoverableFirewall(t *testing.T, fake *fakeFirewall, collector *Collector) {
	t.Helper()
	fake.answerFixture(opnsense.InterfacesInfo, "interfaces_info.json")
	fake.answerFixture(opnsense.InterfaceNames, "get_interface_names.json")
	fake.answerFixture(opnsense.SearchRule, "search_rule.json")
	if err := collector.RefreshDiscovery(context.Background()); err != nil {
		t.Fatalf("runtime discovery: %v", err)
	}
}

// referenceInstant is the clock every collector test runs at.
//
// It is the day after the instants the fixtures carry, so a year-less filter-log
// timestamp resolves against a reference that is a few minutes ahead of it rather than
// a year away — which is what a real poll looks like, and what makes the year inference
// meaningful here rather than trivially satisfied.
func referenceInstant() time.Time {
	return time.Date(2026, time.September, 27, 0, 0, 0, 0, time.UTC)
}

// referenceEpoch is referenceInstant as the UTC epoch every column stores.
func referenceEpoch() int64 { return referenceInstant().Unix() }
