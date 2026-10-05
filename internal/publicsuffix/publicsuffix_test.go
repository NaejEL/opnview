package publicsuffix

import (
	"bufio"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/store"
)

// AC6 and AC7.

// excerpt is the list's own rules the test vectors exercise.
func excerpt(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "public_suffix_list_excerpt.dat"))
	if err != nil {
		t.Fatalf("reading the excerpt: %v", err)
	}
	return raw
}

// vectorLine is one line of the list's own test file.
var vectorLine = regexp.MustCompile(`^checkPublicSuffix\((null|'[^']*'), (null|'[^']*')\);$`)

// TestTheRegistrableDomainPassesTheListsOwnTestVectors is AC7.
func TestTheRegistrableDomainPassesTheListsOwnTestVectors(t *testing.T) {
	list, err := parse(excerpt(t), 1)
	if err != nil {
		t.Fatalf("parsing the excerpt: %v", err)
	}
	file, err := os.Open(filepath.Join("testdata", "test_psl.txt"))
	if err != nil {
		t.Fatalf("opening the vectors: %v", err)
	}
	defer func() { _ = file.Close() }()
	scanner := bufio.NewScanner(file)
	vectors := 0
	categories := map[string]bool{}
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		match := vectorLine.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		vectors++
		input, expected := match[1], match[2]
		if input == "null" {
			continue
		}
		input = strings.Trim(input, "'")
		got, ok := list.RegistrableDomain(input)
		switch {
		case expected == "null" && ok:
			t.Errorf("%s has no registrable domain, and the function gave %s", input, got)
		case expected != "null" && !ok:
			t.Errorf("%s: the function gave none, the vector expects %s", input, expected)
		case expected != "null" && got != strings.Trim(expected, "'"):
			t.Errorf("%s: the function gave %s, the vector expects %s", input, got, expected)
		}
		switch {
		case strings.HasSuffix(input, ".ck") || strings.HasSuffix(input, ".mm"):
			categories["wildcard"] = true
		}
		if strings.Contains(input, "kobe.jp") || input == "www.ck" || input == "www.www.ck" {
			categories["exception"] = true
		}
		if strings.Contains(input, "k12.ak.us") || strings.Contains(input, "ide.kyoto.jp") {
			categories["multi-label"] = true
		}
	}
	if vectors < 75 {
		t.Fatalf("only %d vectors were read", vectors)
	}
	for _, category := range []string{"wildcard", "exception", "multi-label"} {
		if !categories[category] {
			t.Errorf("no %s rule was exercised", category)
		}
	}

	// The grouping never alters a site name: each group carries the stored names verbatim,
	// whatever the case, the trailing dot or the script they were written in, and the totals
	// it was handed are left as they were. The names are chosen so that any normalisation --
	// lower-casing, stripping the dot, punycode -- would change at least one of them.
	stored := []store.SiteTotal{
		{SiteName: "WWW.Example.co.uk", Bytes: 10, Connections: 1},
		{SiteName: "shop.example.co.uk.", Bytes: 20, Connections: 2},
		{SiteName: "bücher.example.de", Bytes: 30, Connections: 3},
		{SiteName: "co.uk", Bytes: 40, Connections: 4},
	}
	handed := append([]store.SiteTotal(nil), stored...)
	var carried []string
	for _, group := range GroupSites(list, handed) {
		carried = append(carried, group.SiteNames...)
	}
	sort.Strings(carried)
	var want []string
	for _, site := range stored {
		want = append(want, site.SiteName)
	}
	sort.Strings(want)
	if strings.Join(carried, "|") != strings.Join(want, "|") {
		t.Errorf("the groups carry the site names %q, not the stored %q verbatim", carried, want)
	}
	for index := range stored {
		if handed[index] != stored[index] {
			t.Errorf("grouping altered the site total it was handed: %+v became %+v", stored[index], handed[index])
		}
	}
	if got, ok := list.RegistrableDomain("www.example.com."); !ok || got != "example.com." {
		t.Errorf("a trailing dot gave %q", got)
	}
}

// TestPunycodeEncodesTheRFCExample is the encoder against RFC 3492's own sample.
func TestPunycodeEncodesTheRFCExample(t *testing.T) {
	for input, want := range map[string]string{
		"bücher": "bcher-kva",
		"中国":     "fiqs8s",
		"公司":     "55qx5d",
		"食狮":     "85x722f",
	} {
		got, ok := punycodeEncode([]rune(input))
		if !ok || got != want {
			t.Errorf("%s encodes to %s, not %s", input, got, want)
		}
	}
}

// listServer stands in for publicsuffix.org.
type listServer struct {
	mutex       sync.Mutex
	status      int
	body        []byte
	etag        string
	requests    int
	conditional int
	sent        int
}

func (s *listServer) handle(writer http.ResponseWriter, request *http.Request) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.requests++
	if s.etag != "" && request.Header.Get("If-None-Match") == s.etag {
		s.conditional++
		writer.WriteHeader(http.StatusNotModified)
		return
	}
	if s.status != 0 && s.status != http.StatusOK {
		writer.WriteHeader(s.status)
		return
	}
	if s.etag != "" {
		writer.Header().Set("ETag", s.etag)
	}
	writer.Header().Set("Last-Modified", "Thu, 01 Oct 2026 23:03:02 GMT")
	s.sent++
	_, _ = writer.Write(s.body)
}

func (s *listServer) set(status int, body []byte, etag string) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.status, s.body, s.etag = status, body, etag
}

// harness is a refresher against a local server, with a store to record into.
type harness struct {
	server    *listServer
	refresher *Refresher
	database  *store.Store
	dir       string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	server := &listServer{}
	httpServer := httptest.NewServer(http.HandlerFunc(server.handle))
	t.Cleanup(httpServer.Close)
	target, err := url.Parse(httpServer.URL)
	if err != nil {
		t.Fatalf("parsing the server address: %v", err)
	}
	database, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("opening the store: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	dir := filepath.Join(t.TempDir(), "publicsuffix")
	client := newClient(httpServer.URL+"/list/public_suffix_list.dat", []string{target.Host},
		http.DefaultTransport, 5*time.Second)
	refresher, err := NewRefresher(client, dir, database, func() time.Time {
		return time.Unix(1790000000, 0)
	})
	if err != nil {
		t.Fatalf("creating the refresher: %v", err)
	}
	return &harness{server: server, refresher: refresher, database: database, dir: dir}
}

func (h *harness) availability(t *testing.T) (store.AvailabilityState, string) {
	t.Helper()
	providerID, err := h.database.ProviderID(context.Background(), Kind, ProviderKey)
	if err != nil {
		t.Fatalf("looking up the provider: %v", err)
	}
	state, probe, _, err := h.database.Availability(context.Background(), providerID)
	if err != nil {
		t.Fatalf("reading the availability: %v", err)
	}
	return state, probe
}

func (h *harness) gaps(t *testing.T) int {
	t.Helper()
	var count int
	if err := h.database.DB().QueryRow(`SELECT count(*) FROM collection_gap g
		JOIN provider p ON p.id = g.provider_id WHERE p.kind = ? AND g.reason = 'download_failed'`,
		Kind).Scan(&count); err != nil {
		t.Fatalf("counting the gaps: %v", err)
	}
	return count
}

func (h *harness) active(t *testing.T) bool {
	t.Helper()
	var active bool
	if err := h.database.DB().QueryRow("SELECT is_active FROM provider WHERE kind = ? AND provider_key = ?",
		Kind, ProviderKey).Scan(&active); err != nil {
		t.Fatalf("reading the activation: %v", err)
	}
	return active
}

// fullEnough is a list that clears the parser's floor: the excerpt's rules and a few more,
// in the list's own sections.
func fullEnough(t *testing.T) []byte {
	t.Helper()
	text := string(excerpt(t))
	return []byte(strings.Replace(text, "// ===END ICANN DOMAINS===",
		"example-one\nexample-two\nexample-three\n\n// ===END ICANN DOMAINS===", 1))
}

// TestTheListIsDownloadedKeptAndRefreshedOnlyWhenItChanged is AC6.
func TestTheListIsDownloadedKeptAndRefreshedOnlyWhenItChanged(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	sites := []store.SiteTotal{{SiteName: "www.example.com", Bytes: 10}, {SiteName: "api.example.com", Bytes: 5}}

	// With no copy at all, the grouping says so.
	if _, state := h.refresher.Group(sites); state != GroupingUnavailable {
		t.Errorf("with no copy the grouping is %s", state)
	}
	if state, _ := h.availability(t); state != store.StateUnavailable {
		t.Errorf("with no copy the provider is %s", state)
	}

	// A failed first download keeps nothing, records a gap and stays unavailable.
	h.server.set(http.StatusServiceUnavailable, nil, "")
	if err := h.refresher.Refresh(ctx); err == nil {
		t.Error("a failed download reported no error")
	}
	if state, probe := h.availability(t); state != store.StateUnavailable || probe != ProbeFailed {
		t.Errorf("after a failed first download the provider is %s, %s", state, probe)
	}
	if h.gaps(t) != 1 {
		t.Errorf("a failed download recorded %d gaps", h.gaps(t))
	}
	if _, state := h.refresher.Group(sites); state != GroupingUnavailable {
		t.Errorf("after a failed first download the grouping is %s", state)
	}

	// A successful download is stored and used.
	h.server.set(http.StatusOK, fullEnough(t), `"example-etag-1"`)
	if err := h.refresher.Refresh(ctx); err != nil {
		t.Fatalf("downloading: %v", err)
	}
	if state, probe := h.availability(t); state != store.StateReachable || probe != ProbeDownloaded || !h.active(t) {
		t.Errorf("after a download the provider is %s, %s, active %t", state, probe, h.active(t))
	}
	groups, state := h.refresher.Group(sites)
	if state != GroupingAvailable || len(groups) != 1 || groups[0].RegistrableDomain != "example.com" ||
		groups[0].Bytes != 15 || len(groups[0].SiteNames) != 2 {
		t.Errorf("the grouping is %s %+v", state, groups)
	}
	if _, err := os.Stat(filepath.Join(h.dir, listFile)); err != nil {
		t.Errorf("the copy was not kept on disk: %v", err)
	}

	// An unchanged list is not downloaded again where the server signals it.
	sentBefore := h.server.sent
	if err := h.refresher.Refresh(ctx); err != nil {
		t.Fatalf("refreshing: %v", err)
	}
	if h.server.sent != sentBefore || h.server.conditional != 1 {
		t.Errorf("an unchanged list was sent %d more times, %d conditional answers",
			h.server.sent-sentBefore, h.server.conditional)
	}
	if _, probe := h.availability(t); probe != ProbeNotModified {
		t.Errorf("an unchanged list left the probe %s", probe)
	}

	// A malformed download keeps the previous copy, records a gap and the state.
	h.server.set(http.StatusOK, []byte("<html>not the list</html>"), `"example-etag-2"`)
	if err := h.refresher.Refresh(ctx); !errors.Is(err, ErrMalformed) {
		t.Errorf("a malformed download gave %v", err)
	}
	if state, probe := h.availability(t); state != store.StateReachable || probe != ProbeMalformed {
		t.Errorf("after a malformed download the provider is %s, %s", state, probe)
	}
	if h.gaps(t) != 2 {
		t.Errorf("%d gaps after two failures", h.gaps(t))
	}
	if groups, state := h.refresher.Group(sites); state != GroupingAvailable || len(groups) != 1 {
		t.Error("a malformed download replaced the copy in use")
	}
	onDisk, err := os.ReadFile(filepath.Join(h.dir, listFile))
	if err != nil || !strings.Contains(string(onDisk), icannBegin) {
		t.Error("a malformed download replaced the copy on disk")
	}

	// A failed download keeps the previous copy too.
	h.server.set(http.StatusInternalServerError, nil, "")
	if err := h.refresher.Refresh(ctx); err == nil {
		t.Error("a failed download reported no error")
	}
	if state, probe := h.availability(t); state != store.StateReachable || probe != ProbeFailed {
		t.Errorf("after a failed refresh the provider is %s, %s", state, probe)
	}

	// A second process start reads the kept copy before any download.
	again, err := NewRefresher(h.refresher.client, h.dir, h.database, h.refresher.now)
	if err != nil {
		t.Fatalf("reopening: %v", err)
	}
	if again.List() == nil {
		t.Error("the kept copy was not read back at start")
	}
}

// TestNoHostButTheListHostIsContacted refuses a request elsewhere, redirects included.
func TestNoHostButTheListHostIsContacted(t *testing.T) {
	elsewhere := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte("elsewhere"))
	}))
	defer elsewhere.Close()
	redirecting := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, elsewhere.URL+"/list", http.StatusFound)
	}))
	defer redirecting.Close()
	target, _ := url.Parse(redirecting.URL)
	client := newClient(redirecting.URL+"/list", []string{target.Host}, http.DefaultTransport, 5*time.Second)
	if _, err := client.Fetch(context.Background(), Validators{}); !errors.Is(err, ErrHostRefused) {
		t.Errorf("a redirect to another host gave %v", err)
	}
	other := newClient(elsewhere.URL+"/list", []string{target.Host}, http.DefaultTransport, 5*time.Second)
	if _, err := other.Fetch(context.Background(), Validators{}); !errors.Is(err, ErrHostRefused) {
		t.Errorf("a request to another host gave %v", err)
	}
	if listHost != "publicsuffix.org" || !strings.HasPrefix(listURL, "https://"+listHost+"/") {
		t.Error("the list is not fetched from its canonical host")
	}
}
