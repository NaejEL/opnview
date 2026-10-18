package webtest

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
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/web"
	_ "github.com/NaejEL/opnview/internal/web/surfaces"

	"github.com/NaejEL/opnview/internal/auth"
	"github.com/NaejEL/opnview/internal/collect/providertest"
	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/secret"
	"github.com/NaejEL/opnview/internal/store"
	"github.com/NaejEL/opnview/internal/store/storetest"
)

// The Harness every web test runs against.
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
func PasswordParams() auth.Params {
	return auth.Params{
		MemoryKiB:   8 * 1024,
		Iterations:  1,
		Parallelism: 1,
		SaltLength:  16,
		KeyLength:   32,
	}
}

// TestClock is a clock a test moves by hand, so nothing sleeps and both session
// bounds are assertable in microseconds.
type TestClock struct {
	mutex sync.Mutex
	now   time.Time
}

// NewTestClock returns a clock stopped at a fixed, arbitrary instant. The instant is
// a date and carries no meaning; what matters is that it is the same on every
// machine.
func NewTestClock() *TestClock {
	return &TestClock{now: time.Date(2026, 3, 14, 9, 0, 0, 0, time.UTC)}
}

// Now returns the clock's instant.
func (c *TestClock) Now() time.Time {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return c.now
}

// Advance moves the clock forward.
func (c *TestClock) Advance(by time.Duration) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.now = c.now.Add(by)
}

// Harness is one server, its database, its key file and its fake firewall.
type Harness struct {
	T          *testing.T
	DataDir    string
	Store      *store.Store
	Box        *secret.Box
	Clock      *TestClock
	SetupToken *auth.SetupToken
	Creds      *config.FirewallCredentials
	Fake       *providertest.Firewall
	Server     *web.Server
	HTTP       *httptest.Server
	Client     *http.Client
	// Live is the configuration holder the scheduler would read, and collector
	// stands in for the running Collector: together they are what a save on the
	// collection surface has to reach without a restart.
	Live      *config.Live
	Collector *RecordingCollector
	GeoIP     *RecordingGeoIP
}

// RecordingCollector stands in for the running collector and keeps every
// configuration handed to it, in order.
type RecordingCollector struct {
	mutex      sync.Mutex
	configured []config.Config
	reselected []string
}

// Configure records one configuration.
func (c *RecordingCollector) Configure(settings config.Config) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.configured = append(c.configured, settings)
}

// RecordingGeoIP stands in for the MaxMind refresh: it counts the wakes.
type RecordingGeoIP struct {
	mutex sync.Mutex
	wakes int
}

// Wake counts one wake.
func (g *RecordingGeoIP) Wake() {
	g.mutex.Lock()
	defer g.mutex.Unlock()
	g.wakes++
}

// Woken returns how many wakes arrived.
func (g *RecordingGeoIP) Woken() int {
	g.mutex.Lock()
	defer g.mutex.Unlock()
	return g.wakes
}

// Reselect records the kind whose selection was applied.
func (c *RecordingCollector) Reselect(_ context.Context, kind string) error {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.reselected = append(c.reselected, kind)
	return nil
}

// ReselectedKinds returns the kinds whose selection was applied, in order.
func (c *RecordingCollector) ReselectedKinds() []string {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return append([]string(nil), c.reselected...)
}

// Received returns the configurations handed over so far.
func (c *RecordingCollector) Received() []config.Config {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return append([]config.Config(nil), c.configured...)
}

// newHarness builds a server on a fresh data directory.
func New(t *testing.T) *Harness {
	t.Helper()
	return NewIn(t, t.TempDir(), true)
}

// newHarnessIn builds a server on a named data directory, which is what a restart
// test reuses. withKeyFile false is the lost-key-file state.
func NewIn(t *testing.T, dataDir string, withKeyFile bool) *Harness {
	t.Helper()
	return NewOver(t, dataDir, withKeyFile, false)
}

// newTLSHarness builds a server whose fake firewall speaks TLS under a certificate
// it generated for itself, reached through THE PRODUCT'S OWN TRANSPORT.
//
// IT IS THE ONLY HARNESS THAT EXERCISES A TRUST DECISION, and it exists because the
// state it produces was being reported as a different state entirely. A default
// OPNsense serves its API under a self-signed certificate; every other Harness here
// talks plain HTTP to the fake and therefore never makes opnview decide whether to
// believe a certificate. With no fingerprint pinned this firewall is refused, which
// is the certificate-refused state; with the right one pinned it answers.
func NewTLS(t *testing.T) *Harness {
	t.Helper()
	return NewOver(t, t.TempDir(), true, true)
}

// newHarnessOver builds a server whose fake firewall speaks TLS or does not.
func NewOver(t *testing.T, dataDir string, withKeyFile, firewallOverTLS bool) *Harness {
	t.Helper()
	return NewAdjusted(t, dataDir, withKeyFile, firewallOverTLS, nil)
}

// newHarnessAdjusted is newHarnessOver with a last say over the server's options, given
// the opened Store: a test that needs the real MaxMind or Public Suffix List refresher
// in place of the recording one builds it there.
func NewAdjusted(t *testing.T, dataDir string, withKeyFile, firewallOverTLS bool,
	adjust func(*web.Options)) *Harness {
	t.Helper()

	database, err := store.Open(context.Background(), databaseFor(t, dataDir))
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

	fake := providertest.NewAnsweringFirewall(t, firewallOverTLS)
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
		providertest.FirewallOnly(t, fake, inner),
		5*time.Second)

	setupToken, err := auth.NewSetupToken()
	if err != nil {
		t.Fatalf("generating a setup token: %v", err)
	}
	clock := NewTestClock()
	// The firewall's clock, from which discovery measures its offset from UTC: a
	// firewall on UTC, in the shape the survey records. It is the REAL time and not
	// the test clock's, because the only collectors these tests run are on the system
	// clock; the quarter-hour rounding absorbs the seconds a test takes.
	fake.Answer(opnsense.SystemTime, http.StatusOK, MustJSON(t, map[string]string{
		"datetime": time.Now().UTC().Format("Mon Jan _2 15:04:05 UTC 2006"),
		"uptime":   "1 day, 00:00:00",
		"loadavg":  "0.10, 0.10, 0.10",
	}))

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
	collector := &RecordingCollector{}
	geoip := &RecordingGeoIP{}

	options := web.Options{
		Store:          database,
		DataDir:        dataDir,
		Box:            box,
		Credentials:    credentials,
		Client:         client,
		SetupToken:     setupToken,
		PasswordParams: PasswordParams(),
		Now:            clock.Now,
		Settings:       live,
		Collector:      collector,
		GeoIP:          geoip,
	}
	if adjust != nil {
		adjust(&options)
	}
	server, err := web.New(options)
	if err != nil {
		t.Fatalf("building the Server: %v", err)
	}

	front := httptest.NewServer(server)
	t.Cleanup(front.Close)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("building a cookie jar: %v", err)
	}
	return &Harness{
		T: t, DataDir: dataDir, Store: database, Box: box, Clock: clock,
		SetupToken: setupToken, Creds: credentials, Fake: fake, Server: server,
		HTTP: front, Live: live, Collector: collector, GeoIP: geoip,
		Client: &http.Client{
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

// Reopen closes this Harness's database and builds a second server over the SAME
// data directory, which is what a restart is.
//
// IT IS HOW AC7 IS ASSERTED ACROSS A RESTART. A setup surface closed by a flag in
// memory would reopen here, and that is the hole the criterion exists to catch.
func (h *Harness) Reopen() *Harness {
	h.T.Helper()
	h.HTTP.Close()
	if err := h.Store.Close(); err != nil {
		h.T.Fatalf("closing the database before the restart: %v", err)
	}
	return NewIn(h.T, h.DataDir, true)
}

// Get issues a GET.
func (h *Harness) Get(path string) *http.Response {
	h.T.Helper()
	request, err := http.NewRequest(http.MethodGet, h.HTTP.URL+path, nil)
	if err != nil {
		h.T.Fatalf("building a GET for %s: %v", path, err)
	}
	answer, err := h.Client.Do(request)
	if err != nil {
		h.T.Fatalf("issuing a GET for %s: %v", path, err)
	}
	return answer
}

// SessionToken reads GET /api/session as the application does at start: the form token this
// reader writes with -- the session's own, or the one in this browser's cookie, which the read
// sets when there is none -- and whether an account exists and the reader is signed in.
func (h *Harness) SessionToken() (token string, accountExists, signedIn bool) {
	h.T.Helper()
	answer := h.Get(PathAPISession)
	defer func() { _ = answer.Body.Close() }()
	body := ReadBody(h.T, answer)
	if answer.StatusCode != http.StatusOK {
		h.T.Fatalf("GET %s answered %d: %s", PathAPISession, answer.StatusCode, body)
	}
	var state struct {
		AccountExists bool   `json:"account_exists"`
		SignedIn      bool   `json:"signed_in"`
		CSRFToken     string `json:"csrf_token"`
	}
	if err := json.Unmarshal([]byte(body), &state); err != nil || state.CSRFToken == "" {
		h.T.Fatalf("GET %s answered no form token: %s", PathAPISession, body)
	}
	return state.CSRFToken, state.AccountExists, state.SignedIn
}

// FormToken is the form token this reader writes with, read from GET /api/session.
func (h *Harness) FormToken() string {
	h.T.Helper()
	token, _, _ := h.SessionToken()
	return token
}

// PostWithToken issues a JSON write carrying this reader's own form token, read as the
// application reads it.
func (h *Harness) PostWithToken(path, body string) *http.Response {
	h.T.Helper()
	return h.PostJSON(path, body, h.FormToken(), "")
}

// CompleteSetup runs the setup end to end through the JSON API and returns the login and
// password it used, both generated here. The session it issues is left in the jar.
func (h *Harness) CompleteSetup() (login, password string) {
	h.T.Helper()
	login = "operator-" + RandomHex(h.T, 4)
	password = RandomHex(h.T, 12)
	answer := h.PostWithToken(PathAPISetup, string(MustJSON(h.T, map[string]string{
		"setup_token":    h.SetupToken.Token(),
		"login":          login,
		"password":       password,
		"password_again": password,
	})))
	defer func() { _ = answer.Body.Close() }()
	if answer.StatusCode != http.StatusOK {
		h.T.Fatalf("setup answered %d: %s", answer.StatusCode, ReadBody(h.T, answer))
	}
	return login, password
}

// SignIn signs in through the JSON API and leaves the session in the Harness's cookie jar.
func (h *Harness) SignIn(login, password string) {
	h.T.Helper()
	answer := h.PostWithToken(PathAPISignIn, string(MustJSON(h.T, map[string]string{
		"login": login, "password": password,
	})))
	defer func() { _ = answer.Body.Close() }()
	if answer.StatusCode != http.StatusOK {
		h.T.Fatalf("signing in answered %d: %s", answer.StatusCode, ReadBody(h.T, answer))
	}
}

// SessionCookie returns the session cookie the jar currently holds, or the empty
// string.
func (h *Harness) SessionCookie() string {
	h.T.Helper()
	target, err := url.Parse(h.HTTP.URL)
	if err != nil {
		h.T.Fatalf("parsing the front-end address: %v", err)
	}
	for _, cookie := range h.Client.Jar.Cookies(target) {
		if cookie.Name == web.SessionCookieName {
			return cookie.Value
		}
	}
	return ""
}

// ReadBody reads a response body to the end.
func ReadBody(t *testing.T, answer *http.Response) string {
	t.Helper()
	raw, err := io.ReadAll(answer.Body)
	if err != nil {
		t.Fatalf("reading a response body: %v", err)
	}
	return string(raw)
}

// AttributeAfter returns what follows marker up to the next double quote.
func AttributeAfter(body, marker string) string {
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

// RandomHex generates a value for one test run, so no credential exists in the
// repository.
func RandomHex(t *testing.T, bytes int) string {
	t.Helper()
	raw := make([]byte, bytes)
	if _, err := rand.Read(raw); err != nil {
		t.Fatalf("generating a test value: %v", err)
	}
	return hex.EncodeToString(raw)
}

// MustJSON encodes a body the fake serves.
func MustJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("encoding a fake answer: %v", err)
	}
	return encoded
}

// databases maps a harness's data directory to its database on the test instance, so a restart
// (Reopen) opens the same database the first server wrote, as a restart of the service does.
var databases sync.Map

// databaseFor returns the database of a data directory, creating it for the test the first time.
func databaseFor(t *testing.T, dataDir string) string {
	t.Helper()
	if dsn, found := databases.Load(dataDir); found {
		return dsn.(string)
	}
	dsn := storetest.Database(t)
	databases.Store(dataDir, dsn)
	t.Cleanup(func() { databases.Delete(dataDir) })
	return dsn
}
