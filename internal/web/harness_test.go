package web

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/auth"
	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/secret"
	"github.com/NaejEL/opnview/internal/store"
)

// The harness every web test runs against.
//
// NO CREDENTIAL, ADDRESS, HOST NAME OR PORT OF ANY REAL NETWORK EXISTS HERE. Every
// credential is generated per test run, and the only host any test reaches is a
// loopback fake whose address the operating system chose at run time. That is the
// zero-hardcoded-configuration rule, kept in the tests as well as in the product,
// and it has been broken before.
//
// THE TRANSPORT BEHIND THE OPNSENSE CLIENT FAILS ANY HOST BUT THE FAKE, and it fails
// the test as well as the call. So a second outbound destination — a MaxMind
// download that wandered into this cycle, a CDN, a telemetry beacon, a version check
// — does not merely get noticed: the test fails.

// testPasswordParams are Argon2id parameters low enough that hashing does not
// dominate a test run.
//
// NOTHING IN THE PRODUCT LOWERS THEM. They exist to prove the OPPOSITE of what a
// weak parameter set usually proves: a record written under these still verifies
// after the product's defaults are raised, because verification reads the parameters
// from the record. That is AC6, and it is only assertable if a test can write a
// record under parameters other than the defaults.
func testPasswordParams() auth.Params {
	return auth.Params{
		MemoryKiB:   8 * 1024,
		Iterations:  1,
		Parallelism: 1,
		SaltLength:  16,
		KeyLength:   32,
	}
}

// testClock is a clock a test moves by hand, so nothing sleeps and both session
// bounds are assertable in microseconds.
type testClock struct {
	mutex sync.Mutex
	now   time.Time
}

// newTestClock returns a clock stopped at a fixed, arbitrary instant. The instant is
// a date and carries no meaning; what matters is that it is the same on every
// machine.
func newTestClock() *testClock {
	return &testClock{now: time.Date(2026, 3, 14, 9, 0, 0, 0, time.UTC)}
}

// Now returns the clock's instant.
func (c *testClock) Now() time.Time {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return c.now
}

// advance moves the clock forward.
func (c *testClock) advance(by time.Duration) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.now = c.now.Add(by)
}

// harness is one server, its database, its key file and its fake firewall.
type harness struct {
	t          *testing.T
	dataDir    string
	store      *store.Store
	box        *secret.Box
	clock      *testClock
	setupToken *auth.SetupToken
	creds      *config.FirewallCredentials
	fake       *fakeFirewall
	server     *Server
	http       *httptest.Server
	client     *http.Client
	// live is the configuration holder the scheduler would read, and collector
	// stands in for the running collector: together they are what a save on the
	// collection surface has to reach without a restart.
	live      *config.Live
	collector *recordingCollector
}

// recordingCollector stands in for the running collector and keeps every
// configuration handed to it, in order.
type recordingCollector struct {
	mutex      sync.Mutex
	configured []config.Config
}

// Configure records one configuration.
func (c *recordingCollector) Configure(settings config.Config) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.configured = append(c.configured, settings)
}

// received returns the configurations handed over so far.
func (c *recordingCollector) received() []config.Config {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return append([]config.Config(nil), c.configured...)
}

// newHarness builds a server on a fresh data directory.
func newHarness(t *testing.T) *harness {
	t.Helper()
	return newHarnessIn(t, t.TempDir(), true)
}

// newHarnessIn builds a server on a named data directory, which is what a restart
// test reuses. withKeyFile false is the lost-key-file state.
func newHarnessIn(t *testing.T, dataDir string, withKeyFile bool) *harness {
	t.Helper()
	return newHarnessOver(t, dataDir, withKeyFile, false)
}

// newTLSHarness builds a server whose fake firewall speaks TLS under a certificate
// it generated for itself, reached through THE PRODUCT'S OWN TRANSPORT.
//
// IT IS THE ONLY HARNESS THAT EXERCISES A TRUST DECISION, and it exists because the
// state it produces was being reported as a different state entirely. A default
// OPNsense serves its API under a self-signed certificate; every other harness here
// talks plain HTTP to the fake and therefore never makes opnview decide whether to
// believe a certificate. With no fingerprint pinned this firewall is refused, which
// is the certificate-refused state; with the right one pinned it answers.
func newTLSHarness(t *testing.T) *harness {
	t.Helper()
	return newHarnessOver(t, t.TempDir(), true, true)
}

// newHarnessOver builds a server whose fake firewall speaks TLS or does not.
func newHarnessOver(t *testing.T, dataDir string, withKeyFile, firewallOverTLS bool) *harness {
	t.Helper()

	database, err := store.Open(context.Background(), dataDir)
	if err != nil {
		t.Fatalf("opening the database: %v", err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("closing the database: %v", err)
		}
	})

	var box *secret.Box
	if withKeyFile {
		box, err = secret.OpenOrCreate(dataDir)
		if err != nil {
			t.Fatalf("opening the key file: %v", err)
		}
	}

	fake := newFakeFirewall(t, firewallOverTLS)
	credentials := config.NewFirewallCredentials()
	// OVER TLS THE INNER TRANSPORT IS THE PRODUCT'S, so what decides whether the
	// fake's certificate is acceptable is the code that decides it in production, not
	// a decision the test made. The guard that refuses any other host stays in front
	// of it either way.
	var inner http.RoundTripper = http.DefaultTransport
	if firewallOverTLS {
		inner = opnsense.NewTrustingTransport(credentials)
	}
	client := opnsense.NewClientFromSource(credentials,
		&firewallOnlyTransport{t: t, allowedHost: fake.host(), inner: inner},
		5*time.Second)

	setupToken, err := auth.NewSetupToken()
	if err != nil {
		t.Fatalf("generating a setup token: %v", err)
	}
	clock := newTestClock()

	// The live holder is primed exactly as cmd/opnview primes it, so a restart test
	// exercises the same path the product takes.
	var unsealer config.Unsealer
	if box != nil {
		unsealer = box
	}
	initial, _, err := config.LoadFirewallCredentials(context.Background(), database, unsealer)
	if err != nil {
		t.Fatalf("loading the stored credentials: %v", err)
	}
	credentials.Set(initial)
	settings, err := config.Load(context.Background(), database)
	if err != nil {
		t.Fatalf("loading the stored configuration: %v", err)
	}
	live := config.NewLive(settings)
	collector := &recordingCollector{}

	server, err := New(Options{
		Store:          database,
		DataDir:        dataDir,
		Box:            box,
		Credentials:    credentials,
		Client:         client,
		SetupToken:     setupToken,
		PasswordParams: testPasswordParams(),
		Now:            clock.Now,
		Settings:       live,
		Collector:      collector,
	})
	if err != nil {
		t.Fatalf("building the server: %v", err)
	}

	front := httptest.NewServer(server)
	t.Cleanup(front.Close)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("building a cookie jar: %v", err)
	}
	return &harness{
		t: t, dataDir: dataDir, store: database, box: box, clock: clock,
		setupToken: setupToken, creds: credentials, fake: fake, server: server,
		http: front, live: live, collector: collector,
		client: &http.Client{
			Jar: jar,
			// A redirect is a fact a test asserts on, so it is never followed
			// automatically: following one hides the status and the Location that say
			// what the server decided.
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

// reopen closes this harness's database and builds a second server over the SAME
// data directory, which is what a restart is.
//
// IT IS HOW AC7 IS ASSERTED ACROSS A RESTART. A setup surface closed by a flag in
// memory would reopen here, and that is the hole the criterion exists to catch.
func (h *harness) reopen() *harness {
	h.t.Helper()
	h.http.Close()
	if err := h.store.Close(); err != nil {
		h.t.Fatalf("closing the database before the restart: %v", err)
	}
	return newHarnessIn(h.t, h.dataDir, true)
}

// get issues a GET.
func (h *harness) get(path string) *http.Response {
	h.t.Helper()
	request, err := http.NewRequest(http.MethodGet, h.http.URL+path, nil)
	if err != nil {
		h.t.Fatalf("building a GET for %s: %v", path, err)
	}
	answer, err := h.client.Do(request)
	if err != nil {
		h.t.Fatalf("issuing a GET for %s: %v", path, err)
	}
	return answer
}

// post issues a POST carrying the form token the page it names was rendered with.
func (h *harness) post(path string, form url.Values) *http.Response {
	h.t.Helper()
	if _, present := form["csrf_token"]; !present {
		form.Set("csrf_token", h.formToken(path))
	}
	return h.postRaw(path, form)
}

// postRaw issues a POST exactly as given, token included or omitted.
func (h *harness) postRaw(path string, form url.Values) *http.Response {
	h.t.Helper()
	request, err := http.NewRequest(http.MethodPost, h.http.URL+path,
		strings.NewReader(form.Encode()))
	if err != nil {
		h.t.Fatalf("building a POST for %s: %v", path, err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	answer, err := h.client.Do(request)
	if err != nil {
		h.t.Fatalf("issuing a POST for %s: %v", path, err)
	}
	return answer
}

// formToken fetches a page and reads the form token out of it, which is what a
// browser does.
func (h *harness) formToken(path string) string {
	h.t.Helper()
	source := path
	if path == PathSignOut {
		// The sign-out control lives in the bar of whatever page is open.
		source = PathSettings
	}
	answer := h.get(source)
	defer func() { _ = answer.Body.Close() }()
	body := readBody(h.t, answer)
	token := attributeAfter(body, `name="csrf_token" value="`)
	if token == "" {
		h.t.Fatalf("no form token was rendered on %s (status %d)", source, answer.StatusCode)
	}
	return token
}

// completeSetup runs the setup surface end to end and returns the login and
// password it used, both generated here.
func (h *harness) completeSetup() (login, password string) {
	h.t.Helper()
	login = "operator-" + randomHex(h.t, 4)
	password = randomHex(h.t, 12)
	answer := h.post(PathSetup, url.Values{
		"setup_token":    {h.setupToken.Token()},
		"login":          {login},
		"password":       {password},
		"password_again": {password},
	})
	defer func() { _ = answer.Body.Close() }()
	if answer.StatusCode != http.StatusSeeOther {
		h.t.Fatalf("setup answered %d rather than a redirect: %s",
			answer.StatusCode, readBody(h.t, answer))
	}
	return login, password
}

// signIn signs in and leaves the session in the harness's cookie jar.
func (h *harness) signIn(login, password string) {
	h.t.Helper()
	answer := h.post(PathSignIn, url.Values{"login": {login}, "password": {password}})
	defer func() { _ = answer.Body.Close() }()
	if answer.StatusCode != http.StatusSeeOther {
		h.t.Fatalf("signing in answered %d rather than a redirect", answer.StatusCode)
	}
}

// sessionCookie returns the session cookie the jar currently holds, or the empty
// string.
func (h *harness) sessionCookie() string {
	h.t.Helper()
	target, err := url.Parse(h.http.URL)
	if err != nil {
		h.t.Fatalf("parsing the front-end address: %v", err)
	}
	for _, cookie := range h.client.Jar.Cookies(target) {
		if cookie.Name == cookieSession {
			return cookie.Value
		}
	}
	return ""
}

// readBody reads a response body to the end.
func readBody(t *testing.T, answer *http.Response) string {
	t.Helper()
	raw, err := io.ReadAll(answer.Body)
	if err != nil {
		t.Fatalf("reading a response body: %v", err)
	}
	return string(raw)
}

// attributeAfter returns what follows marker up to the next double quote.
func attributeAfter(body, marker string) string {
	start := strings.Index(body, marker)
	if start < 0 {
		return ""
	}
	rest := body[start+len(marker):]
	end := strings.Index(rest, `"`)
	if end < 0 {
		return ""
	}
	return rest[:end]
}

// randomHex generates a value for one test run, so no credential exists in the
// repository.
func randomHex(t *testing.T, bytes int) string {
	t.Helper()
	raw := make([]byte, bytes)
	if _, err := rand.Read(raw); err != nil {
		t.Fatalf("generating a test value: %v", err)
	}
	return hex.EncodeToString(raw)
}

// fakeFirewall answers the registry's paths, records what was asked and with which
// credentials, and refuses everything else.
type fakeFirewall struct {
	t      *testing.T
	server *httptest.Server

	mutex  sync.Mutex
	status int
	body   []byte
	// perPath answers one path with a shape of its own. Runtime discovery reads three
	// endpoints whose envelopes differ, and a collection pass that fell over on a body
	// it could not decode would prove nothing about the credentials it carried.
	perPath  map[string][]byte
	requests []fakeRequest
	// down makes the fake refuse to answer at all, which is the unreachable-host
	// case: the transport fails rather than a status coming back.
	down bool
}

// fakeRequest is one request the fake received.
type fakeRequest struct {
	// path is what was asked for.
	path string
	// method is how.
	method string
	// apiKey and apiSecret are the Basic-auth halves the request carried. They are
	// what AC16 asserts on: the pass after a credential change must carry the new
	// value.
	apiKey    string
	apiSecret string
}

// newFakeFirewall starts a fake answering the verification endpoint. overTLS serves
// it under a certificate httptest generates, which no system root vouches for —
// which is the shape of a firewall as OPNsense ships it.
func newFakeFirewall(t *testing.T, overTLS bool) *fakeFirewall {
	t.Helper()
	fake := &fakeFirewall{t: t, status: http.StatusOK, perPath: map[string][]byte{}}
	fake.body = mustJSON(t, map[string]any{"rows": []any{}, "total": 0})
	// get_interface_names answers a flat map from device name to description, not the
	// search envelope every other endpoint uses. An empty map is a firewall with
	// nothing discovered, which is a state rather than a failure.
	fake.perPath[opnsense.InterfaceNames.Path] = mustJSON(t, map[string]string{})
	if overTLS {
		fake.server = httptest.NewTLSServer(http.HandlerFunc(fake.serve))
	} else {
		fake.server = httptest.NewServer(http.HandlerFunc(fake.serve))
	}
	t.Cleanup(fake.server.Close)
	return fake
}

// serve records one request and answers it.
func (f *fakeFirewall) serve(writer http.ResponseWriter, request *http.Request) {
	apiKey, apiSecret, _ := request.BasicAuth()

	f.mutex.Lock()
	f.requests = append(f.requests, fakeRequest{
		path: request.URL.Path, method: request.Method,
		apiKey: apiKey, apiSecret: apiSecret,
	})
	status, body, down := f.status, f.body, f.down
	if override, present := f.perPath[request.URL.Path]; present && status == http.StatusOK {
		body = override
	}
	f.mutex.Unlock()

	if down {
		// Hijack and close without answering, so the client sees no response at all —
		// which is what an unreachable host looks like from the transport's side.
		hijacker, ok := writer.(http.Hijacker)
		if !ok {
			f.t.Fatal("the fake cannot simulate an unreachable host on this server")
		}
		connection, _, err := hijacker.Hijack()
		if err != nil {
			f.t.Fatalf("hijacking to simulate an unreachable host: %v", err)
		}
		_ = connection.Close()
		return
	}
	writer.WriteHeader(status)
	_, _ = writer.Write(body)
}

// host is the fake's host:port, chosen by the operating system at run time.
func (f *fakeFirewall) host() string {
	f.t.Helper()
	target, err := url.Parse(f.server.URL)
	if err != nil {
		f.t.Fatalf("parsing the fake firewall address: %v", err)
	}
	return target.Host
}

// baseURL is what a test types into the firewall URL field.
func (f *fakeFirewall) baseURL() string { return f.server.URL }

// fingerprint is the SHA-256 fingerprint of the certificate this fake presents, or
// the empty string when it speaks plain HTTP. It is what an operator would read off
// OPNsense's own interface and type into the settings surface.
func (f *fakeFirewall) fingerprint() string {
	f.t.Helper()
	if f.server.TLS == nil || len(f.server.Certificate().Raw) == 0 {
		return ""
	}
	return opnsense.CertificateFingerprint(f.server.Certificate())
}

// answer sets what the fake replies with.
func (f *fakeFirewall) answer(status int, body []byte) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.status, f.body, f.down = status, body, false
}

// goDown makes the fake stop answering.
func (f *fakeFirewall) goDown() {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.down = true
}

// received returns the requests the fake got.
func (f *fakeFirewall) received() []fakeRequest {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	copied := make([]fakeRequest, len(f.requests))
	copy(copied, f.requests)
	return copied
}

// last returns the most recent request, failing if there is none.
func (f *fakeFirewall) last() fakeRequest {
	f.t.Helper()
	received := f.received()
	if len(received) == 0 {
		f.t.Fatal("the fake firewall received no request, so this assertion is vacuous")
	}
	return received[len(received)-1]
}

// mustJSON encodes a body the fake serves.
func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("encoding a fake answer: %v", err)
	}
	return encoded
}

// firewallOnlyTransport refuses any host but the fake firewall.
//
// It is what makes "one outbound destination" a property a test can fail on rather
// than a claim. It is the same guard internal/collect's own fake carries, and it is
// duplicated rather than shared because a test helper exported across packages would
// be product surface.
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
		return nil, &url.Error{Op: request.Method, URL: request.URL.String(), Err: os.ErrPermission}
	}
	return transport.inner.RoundTrip(request)
}
