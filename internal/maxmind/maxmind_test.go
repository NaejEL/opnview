package maxmind

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/store"
)

// The tests stand two local servers in for MaxMind's two hosts: the download host,
// which checks the credentials and redirects, and the host it redirects to, which
// serves the archive. Every credential is generated here and every database is
// written by mmdb_test.go; nothing is read from MaxMind.

// fakeMaxMind is the two hosts.
type fakeMaxMind struct {
	t          *testing.T
	download   *httptest.Server
	redirected *httptest.Server
	accountID  string
	licenceKey string

	mutex        sync.Mutex
	status       int
	modified     map[Edition]time.Time
	archives     map[Edition][]byte
	heads, gets  int
	servedAuth   []string
	elsewhere    string
	redirectedTo string
}

// newFakeMaxMind starts the two hosts, each edition built at the given instant.
func newFakeMaxMind(t *testing.T, built time.Time) *fakeMaxMind {
	t.Helper()
	fake := &fakeMaxMind{
		t: t, status: http.StatusOK,
		accountID:  strconv.Itoa(100000 + int(time.Now().UnixNano()%800000)),
		licenceKey: "test-licence-" + strconv.FormatInt(time.Now().UnixNano(), 36),
		modified:   map[Edition]time.Time{},
		archives:   map[Edition][]byte{},
	}
	fake.redirected = httptest.NewServer(http.HandlerFunc(fake.serveFile))
	t.Cleanup(fake.redirected.Close)
	fake.download = httptest.NewServer(http.HandlerFunc(fake.serveDownload))
	t.Cleanup(fake.download.Close)
	fake.publish(t, built)
	return fake
}

// publish makes a new build of both editions available.
func (f *fakeMaxMind) publish(t *testing.T, built time.Time) {
	t.Helper()
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.modified[EditionCity] = built.Add(time.Hour)
	f.modified[EditionASN] = built.Add(time.Hour)
	f.archives[EditionCity] = archiveOf(t, EditionCity, cityDatabase(t, built))
	f.archives[EditionASN] = archiveOf(t, EditionASN, asnDatabase(t, built))
}

// placed is the network the test databases place, a documentation range.
var placed = netip.MustParsePrefix("203.0.113.0/24")

// cityDatabase answers one network with a country and coordinates.
func cityDatabase(t *testing.T, built time.Time) []byte {
	t.Helper()
	return buildDatabase(t, string(EditionCity), uint64(built.Unix()), []testRecord{{
		network: placed,
		data: map[string]any{
			"country":  map[string]any{"iso_code": "FR", "names": map[string]any{"en": "France"}},
			"location": map[string]any{"latitude": 48.85, "longitude": 2.35},
		},
	}})
}

// asnDatabase answers the same network with an autonomous system.
func asnDatabase(t *testing.T, built time.Time) []byte {
	t.Helper()
	return buildDatabase(t, string(EditionASN), uint64(built.Unix()), []testRecord{{
		network: placed,
		data: map[string]any{
			"autonomous_system_number":       uint32(64500),
			"autonomous_system_organization": "Example Operator",
		},
	}})
}

// serveDownload is the download host: credentials, then a HEAD or a redirect.
func (f *fakeMaxMind) serveDownload(writer http.ResponseWriter, request *http.Request) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	account, key, ok := request.BasicAuth()
	if !ok || account != f.accountID || key != f.licenceKey {
		writer.WriteHeader(http.StatusUnauthorized)
		return
	}
	if f.status != http.StatusOK {
		writer.WriteHeader(f.status)
		return
	}
	parts := strings.Split(strings.Trim(request.URL.Path, "/"), "/")
	if len(parts) != 4 || parts[0] != "geoip" || parts[1] != "databases" || parts[3] != "download" ||
		request.URL.Query().Get("suffix") != "tar.gz" {
		writer.WriteHeader(http.StatusNotFound)
		return
	}
	edition := Edition(parts[2])
	modified, known := f.modified[edition]
	if !known {
		writer.WriteHeader(http.StatusNotFound)
		return
	}
	writer.Header().Set("Last-Modified", modified.UTC().Format(http.TimeFormat))
	if request.Method == http.MethodHead {
		f.heads++
		writer.WriteHeader(http.StatusOK)
		return
	}
	f.gets++
	target := f.redirected.URL + "/files/" + string(edition)
	if f.elsewhere != "" {
		target = f.elsewhere
	}
	http.Redirect(writer, request, target, http.StatusFound)
}

// serveFile is the host a download is redirected to.
func (f *fakeMaxMind) serveFile(writer http.ResponseWriter, request *http.Request) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.servedAuth = append(f.servedAuth, request.Header.Get("Authorization"))
	archive, known := f.archives[Edition(strings.TrimPrefix(request.URL.Path, "/files/"))]
	if !known {
		writer.WriteHeader(http.StatusNotFound)
		return
	}
	_, _ = writer.Write(archive)
}

// counts returns how many HEAD and GET requests the download host answered.
func (f *fakeMaxMind) counts() (int, int) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	return f.heads, f.gets
}

// hostOf is a server's host:port.
func hostOf(t *testing.T, server *httptest.Server) string {
	t.Helper()
	parsed, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parsing %s: %v", server.URL, err)
	}
	return parsed.Host
}

// client returns a client allowed exactly the two fake hosts.
func (f *fakeMaxMind) client(t *testing.T) *Client {
	t.Helper()
	return newClient(f.download.URL, hostOf(t, f.download),
		[]string{hostOf(t, f.download), hostOf(t, f.redirected)}, &http.Transport{}, 10*time.Second)
}

// harness is a store, a dataset directory, the fake and a refresher over them.
type harness struct {
	fake      *fakeMaxMind
	store     *store.Store
	dataset   *Dataset
	refresher *Refresher
	locator   *Locator
	now       time.Time
	creds     config.MaxMindCredentials
	state     config.CredentialState
}

// newHarness builds the harness with valid credentials.
func newHarness(t *testing.T) *harness {
	t.Helper()
	ctx := context.Background()
	built := time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)
	fake := newFakeMaxMind(t, built)
	database, err := store.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatalf("opening the database: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	dataset, err := OpenDataset(filepath.Join(t.TempDir(), "geoip"))
	if err != nil {
		t.Fatalf("opening the dataset: %v", err)
	}
	t.Cleanup(func() { _ = dataset.Close() })
	h := &harness{
		fake: fake, store: database, dataset: dataset,
		now:   time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC),
		creds: config.MaxMindCredentials{AccountID: fake.accountID, LicenceKey: fake.licenceKey},
		state: config.CredentialStateReady,
	}
	clock := func() time.Time { return h.now }
	h.refresher = NewRefresher(fake.client(t), dataset, database,
		func(context.Context) (config.MaxMindCredentials, config.CredentialState, error) {
			return h.creds, h.state, nil
		}, clock)
	h.locator = NewLocator(dataset, database, clock)
	return h
}

// availability reads the provider's row.
func (h *harness) availability(t *testing.T) (store.AvailabilityState, string) {
	t.Helper()
	ctx := context.Background()
	id, err := h.store.ProviderID(ctx, Kind, ProviderKey)
	if err != nil {
		t.Fatalf("looking up the provider: %v", err)
	}
	state, probe, _, err := h.store.Availability(ctx, id)
	if err != nil {
		t.Fatalf("reading the availability: %v", err)
	}
	return state, probe
}

// active says whether the provider is active.
func (h *harness) active(t *testing.T) bool {
	t.Helper()
	_, active, err := h.store.ActiveProviderID(context.Background(), Kind)
	if err != nil {
		t.Fatalf("reading the active provider: %v", err)
	}
	return active
}

// TestNothingIsRequestedWithoutALicenceKeyOrAnAccountID is AC1.
func TestNothingIsRequestedWithoutALicenceKeyOrAnAccountID(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	for _, missing := range []struct {
		name  string
		creds config.MaxMindCredentials
		state config.CredentialState
		probe string
	}{
		{"no licence key", config.MaxMindCredentials{AccountID: h.fake.accountID},
			config.CredentialStateAbsent, ProbeNoLicenceKey},
		{"an unreadable licence key", config.MaxMindCredentials{AccountID: h.fake.accountID},
			config.CredentialStateUndecryptable, ProbeLicenceKeyUndecryptable},
		{"no account ID", config.MaxMindCredentials{LicenceKey: h.fake.licenceKey},
			config.CredentialStateReady, ProbeNoAccountID},
	} {
		h.creds, h.state = missing.creds, missing.state
		if err := h.refresher.Refresh(ctx); err != nil {
			t.Fatalf("%s: refreshing: %v", missing.name, err)
		}
		if state, probe := h.availability(t); state != store.StateUnavailable || probe != missing.probe {
			t.Errorf("%s: the dataset reads %s / %s, want unavailable / %s",
				missing.name, state, probe, missing.probe)
		}
	}
	if heads, gets := h.fake.counts(); heads+gets != 0 {
		t.Errorf("%d requests left for MaxMind with nothing to authenticate them", heads+gets)
	}
	if h.active(t) {
		t.Error("the provider is active with no dataset")
	}
}

// TestADownloadAuthenticatesAtMaxMindOnlyAndFollowsTheRedirect is AC2 and AC7.
func TestADownloadAuthenticatesAtMaxMindOnlyAndFollowsTheRedirect(t *testing.T) {
	h := newHarness(t)
	if err := h.refresher.Refresh(context.Background()); err != nil {
		t.Fatalf("refreshing: %v", err)
	}
	if _, ready := h.dataset.Ready(); !ready {
		t.Fatal("the dataset is not ready after a successful refresh")
	}
	if state, probe := h.availability(t); state != store.StateReachable || probe != ProbeCurrent {
		t.Errorf("the dataset reads %s / %s, want reachable / %s", state, probe, ProbeCurrent)
	}
	if !h.active(t) {
		t.Error("the provider is not active with the dataset ready")
	}
	h.fake.mutex.Lock()
	defer h.fake.mutex.Unlock()
	if len(h.fake.servedAuth) != 2 {
		t.Fatalf("the redirected host served %d archives, want 2", len(h.fake.servedAuth))
	}
	for _, header := range h.fake.servedAuth {
		if header != "" {
			t.Error("the credentials reached the host the download was redirected to")
		}
	}
}

// TestAnyOtherHostIsRefusedRedirectsIncluded is AC3.
func TestAnyOtherHostIsRefusedRedirectsIncluded(t *testing.T) {
	h := newHarness(t)
	reached := false
	elsewhere := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		reached = true
	}))
	t.Cleanup(elsewhere.Close)
	h.fake.mutex.Lock()
	h.fake.elsewhere = elsewhere.URL + "/files/" + string(EditionCity)
	h.fake.mutex.Unlock()

	err := h.refresher.Refresh(context.Background())
	if !errors.Is(err, ErrHostRefused) {
		t.Errorf("a redirect to a third host answered %v, want the refused-host error", err)
	}
	if reached {
		t.Error("a request reached a host that is neither of MaxMind's two")
	}
	if _, probe := h.availability(t); probe != ProbeFailed {
		t.Errorf("a refused host reads %s, want %s", probe, ProbeFailed)
	}

	// And a client asked straight for another host refuses before sending.
	refusing := newClient(elsewhere.URL, hostOf(t, h.fake.download), []string{hostOf(t, h.fake.download)},
		&http.Transport{}, time.Second)
	if _, err := refusing.LastModified(context.Background(), h.creds, EditionCity); !errors.Is(err, ErrHostRefused) {
		t.Errorf("a request for another host answered %v", err)
	}
}

// TestAnUnchangedBuildIsNotDownloadedAgain is AC4.
func TestAnUnchangedBuildIsNotDownloadedAgain(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	if err := h.refresher.Refresh(ctx); err != nil {
		t.Fatalf("refreshing: %v", err)
	}
	headsBefore, getsBefore := h.fake.counts()
	if err := h.refresher.Refresh(ctx); err != nil {
		t.Fatalf("refreshing again: %v", err)
	}
	heads, gets := h.fake.counts()
	if gets != getsBefore {
		t.Errorf("an unchanged build was downloaded again (%d downloads)", gets-getsBefore)
	}
	if heads != headsBefore+2 {
		t.Errorf("the check made %d HEAD requests, want one per edition", heads-headsBefore)
	}

	// A newer build is downloaded.
	h.fake.publish(t, time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC))
	if err := h.refresher.Refresh(ctx); err != nil {
		t.Fatalf("refreshing after a new build: %v", err)
	}
	if build, _ := h.dataset.Build(EditionCity); build != time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC).Unix() {
		t.Error("a newer build was not installed")
	}
}

// TestABrokenDownloadLeavesTheDatabaseInUse is AC5.
func TestABrokenDownloadLeavesTheDatabaseInUse(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	if err := h.refresher.Refresh(ctx); err != nil {
		t.Fatalf("refreshing: %v", err)
	}
	before, _ := h.dataset.Build(EditionCity)

	newer := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	for _, broken := range []struct {
		name    string
		archive []byte
	}{
		{"not an archive", []byte("not a gzip stream")},
		{"a truncated archive", archiveOf(t, EditionCity, cityDatabase(t, newer))[:200]},
		{"another edition under the City name", archiveOf(t, EditionCity, asnDatabase(t, newer))},
		{"an archive with no database", archiveOf(t, EditionASN, cityDatabase(t, newer))},
	} {
		h.fake.publish(t, newer)
		h.fake.mutex.Lock()
		h.fake.archives[EditionCity] = broken.archive
		h.fake.mutex.Unlock()

		if err := h.refresher.Refresh(ctx); err == nil {
			t.Errorf("%s was installed without an error", broken.name)
		}
		if build, present := h.dataset.Build(EditionCity); !present || build != before {
			t.Errorf("%s replaced the database in use", broken.name)
		}
		if state, probe := h.availability(t); state != store.StateReachable || probe != ProbeFailed {
			t.Errorf("%s reads %s / %s, want reachable / %s", broken.name, state, probe, ProbeFailed)
		}
		if answer, ok, err := h.dataset.Lookup(netip.MustParseAddr("203.0.113.9")); err != nil || !ok ||
			answer.CountryCode == nil {
			t.Errorf("%s left a dataset that no longer answers", broken.name)
		}
	}
	leftovers, err := filepath.Glob(filepath.Join(h.dataset.dir, ".*"))
	if err != nil {
		t.Fatalf("listing the dataset directory: %v", err)
	}
	if len(leftovers) != 0 {
		t.Errorf("a failed download left files behind: %v", leftovers)
	}
}

// TestARefusalAndASpentLimitAreTheirOwnStates is AC6.
func TestARefusalAndASpentLimitAreTheirOwnStates(t *testing.T) {
	for _, answer := range []struct {
		status int
		probe  string
		err    error
	}{
		{http.StatusUnauthorized, ProbeRefused, ErrRefused},
		{http.StatusForbidden, ProbeRefused, ErrRefused},
		{http.StatusTooManyRequests, ProbeLimited, ErrLimited},
		{http.StatusInternalServerError, ProbeFailed, nil},
	} {
		h := newHarness(t)
		h.fake.mutex.Lock()
		h.fake.status = answer.status
		h.fake.mutex.Unlock()
		err := h.refresher.Refresh(context.Background())
		if err == nil || (answer.err != nil && !errors.Is(err, answer.err)) {
			t.Errorf("a %d answered %v", answer.status, err)
		}
		if state, probe := h.availability(t); state != store.StateUnavailable || probe != answer.probe {
			t.Errorf("a %d reads %s / %s, want unavailable / %s", answer.status, state, probe, answer.probe)
		}
		if _, gets := h.fake.counts(); gets != 0 {
			t.Errorf("a %d was followed by a download", answer.status)
		}
	}
}

// storeFlow stores one synthesised flow between two addresses. Nothing here is an
// address read off a network: the public one is a documentation range.
func storeFlow(t *testing.T, database *store.Store, index int, source, destination string) {
	t.Helper()
	if err := database.InsertFlow(context.Background(), store.Flow{
		LogDigest: "geo-test-" + strconv.Itoa(index), ObservedAt: 1790000000 + int64(index),
		IngestedAt: 1790000005 + int64(index), InterfaceDevice: "example-device",
		InterfaceLookupState: store.LookupResolved, SrcAddress: source, DstAddress: destination,
		Protocol: "tcp", IPVersion: 4, Action: "pass", Direction: "out", PacketBytes: 100,
		RuleLookupState: store.LookupPending,
	}); err != nil {
		t.Fatalf("storing a flow: %v", err)
	}
}

// geoRow reads one geo_asn row: its state, country, ASN and build, and whether it
// exists.
func geoRow(t *testing.T, database *store.Store, address string) (string, string, int64, int64, bool) {
	t.Helper()
	var state string
	var country *string
	var asn, build *int64
	err := database.DB().QueryRow(
		`SELECT lookup_state, country_code, asn, dataset_build_at FROM geo_asn WHERE address = ?`,
		address).Scan(&state, &country, &asn, &build)
	if err != nil {
		return "", "", 0, 0, false
	}
	deref := func(value *int64) int64 {
		if value == nil {
			return 0
		}
		return *value
	}
	code := ""
	if country != nil {
		code = *country
	}
	return state, code, deref(asn), deref(build), true
}

// TestPublicAddressesArePlacedAndOthersAreNeverLookedUp is AC8, AC9, and the
// no-dataset half of the data model's rule.
func TestPublicAddressesArePlacedAndOthersAreNeverLookedUp(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	storeFlow(t, h.store, 1, "192.168.10.20", "203.0.113.9")
	storeFlow(t, h.store, 2, "192.168.10.20", "198.51.100.7")
	storeFlow(t, h.store, 3, "fe80::1", "127.0.0.1")

	if err := h.locator.Locate(ctx); err != nil {
		t.Fatalf("locating with no dataset: %v", err)
	}
	if _, _, _, _, present := geoRow(t, h.store, "203.0.113.9"); present {
		t.Fatal("an address was written with no dataset to answer it")
	}

	if err := h.refresher.Refresh(ctx); err != nil {
		t.Fatalf("refreshing: %v", err)
	}
	if err := h.locator.Locate(ctx); err != nil {
		t.Fatalf("locating: %v", err)
	}
	first := time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC).Unix()
	if state, country, asn, build, _ := geoRow(t, h.store, "203.0.113.9"); state != "resolved" ||
		country != "FR" || asn != 64500 || build != first {
		t.Errorf("the placed address reads %s %s %d %d", state, country, asn, build)
	}
	if state, _, _, build, present := geoRow(t, h.store, "198.51.100.7"); !present || state != "miss" ||
		build != first {
		t.Errorf("an address the dataset does not hold reads %s (present %v)", state, present)
	}
	for _, private := range []string{"192.168.10.20", "fe80::1", "127.0.0.1"} {
		if _, _, _, _, present := geoRow(t, h.store, private); present {
			t.Errorf("the non-public address %s was looked up", private)
		}
	}

	// A newer build answers every address again.
	second := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	h.fake.publish(t, second)
	if err := h.refresher.Refresh(ctx); err != nil {
		t.Fatalf("refreshing to the newer build: %v", err)
	}
	if err := h.locator.Locate(ctx); err != nil {
		t.Fatalf("locating again: %v", err)
	}
	for _, address := range []string{"203.0.113.9", "198.51.100.7"} {
		if _, _, _, build, _ := geoRow(t, h.store, address); build != second.Unix() {
			t.Errorf("%s still carries the older build after a new one", address)
		}
	}
}

// TestADatasetOnDiskIsOpenedAtStart: the databases a previous run downloaded are in
// use from the first second of the next one.
func TestADatasetOnDiskIsOpenedAtStart(t *testing.T) {
	h := newHarness(t)
	if err := h.refresher.Refresh(context.Background()); err != nil {
		t.Fatalf("refreshing: %v", err)
	}
	reopened, err := OpenDataset(h.dataset.dir)
	if err != nil {
		t.Fatalf("reopening the dataset: %v", err)
	}
	defer func() { _ = reopened.Close() }()
	if _, ready := reopened.Ready(); !ready {
		t.Error("the databases on disk were not opened at start")
	}
	if _, err := os.Stat(filepath.Join(h.dataset.dir, string(EditionCity)+".last-modified")); err != nil {
		t.Errorf("the download date was not kept beside the database: %v", err)
	}

	// A database on disk that does not open is reported and left out; it does not
	// stop the rest from opening, and the next refresh downloads it again.
	if err := os.WriteFile(filepath.Join(h.dataset.dir, string(EditionASN)+".mmdb"),
		[]byte("not a database"), 0o640); err != nil {
		t.Fatalf("corrupting the ASN database: %v", err)
	}
	damaged, err := OpenDataset(h.dataset.dir)
	if damaged == nil || err == nil {
		t.Fatalf("a corrupt database opened as %v with error %v", damaged, err)
	}
	defer func() { _ = damaged.Close() }()
	if _, present := damaged.Build(EditionCity); !present {
		t.Error("a corrupt ASN database stopped the City database from opening")
	}
	if _, present := damaged.Build(EditionASN); present {
		t.Error("a corrupt database was opened")
	}
	_, getsBefore := h.fake.counts()
	again := NewRefresher(h.fake.client(t), damaged, h.store,
		func(context.Context) (config.MaxMindCredentials, config.CredentialState, error) {
			return h.creds, h.state, nil
		}, func() time.Time { return h.now })
	if err := again.Refresh(context.Background()); err != nil {
		t.Fatalf("refreshing over a corrupt database: %v", err)
	}
	if _, gets := h.fake.counts(); gets != getsBefore+1 {
		t.Errorf("the corrupt edition was downloaded %d times, want once", gets-getsBefore)
	}
	if _, ready := damaged.Ready(); !ready {
		t.Error("the corrupt database was not replaced")
	}
}
