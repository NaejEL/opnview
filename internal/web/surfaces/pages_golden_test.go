package surfaces_test

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/NaejEL/opnview/internal/store"
	"github.com/NaejEL/opnview/internal/web"
	"github.com/NaejEL/opnview/internal/web/webtest"
)

// The byte-identity record of the Go-rendered pages (AC X3 of the backend-completion cycle).
//
// THE PAGES ARE PINNED BEFORE THE PLUGIN WORK REACHES THEM. testdata/golden_pages.json.gz holds the
// HTML every page surface renders in a fixed set of states -- the collection surface in each state
// its own tests draw it in, the settings, interfaces and sources surfaces signed in, the sign-in
// and setup surfaces signed out -- recorded before the provider strings moved into the plugins and
// before anything else of this cycle touched what the pages read. This test asserts the pages
// render the same bytes, with the values that differ on every run (the form token, the generated
// login, the fake firewall's address, the data directory) replaced by a placeholder first.
//
// To rewrite the record, run this test with OPNVIEW_WRITE_GOLDEN=1. That never happens in the
// suites.

// volatile are the values that differ on every run, each with its placeholder.
var volatile = []struct {
	pattern     *regexp.Regexp
	placeholder string
}{
	{regexp.MustCompile(`name="csrf_token" value="[^"]*"`), `name="csrf_token" value="CSRF"`},
	{regexp.MustCompile(`operator-[0-9a-f]{8}`), "operator-LOGIN"},
	{regexp.MustCompile(`https?://127\.0\.0\.1:[0-9]+`), "FIREWALL"},
}

// normalisePage replaces the volatile values of one page.
func normalisePage(page, dataDir string) string {
	for _, value := range volatile {
		page = value.pattern.ReplaceAllString(page, value.placeholder)
	}
	if dataDir != "" {
		page = strings.ReplaceAll(page, dataDir, "DATA_DIR")
	}
	return page
}

// TestEveryPageRendersByteForByteAsBefore is AC X3.
func TestEveryPageRendersByteForByteAsBefore(t *testing.T) {
	t.Parallel()
	pages := map[string]string{}
	for _, page := range webtest.CollectionPages(t) {
		pages[page.Name] = normalisePage(page.Document, "")
	}

	signedIn := webtest.SignedInHarness(t)
	now := signedIn.Clock.Now().Unix()
	signedIn.SetAvailability("firewall_log", "pf", store.StateReachable)
	signedIn.SetAvailability("security_event", "suricata", store.StatePresentButDisabled)
	stopped := "the service reported a status of stopped"
	signedIn.SetAvailabilityDetail("dns_lookup", "unbound", store.StateUnavailable, "GET /api/unbound/service/status",
		&stopped)
	signedIn.RecordGap("firewall_log", "pf", store.GapDigestOutsideWindow, now-900, now-840)
	signedIn.SeedBundleNetwork(2)
	for _, path := range []string{web.PathSettings, webtest.PathInterfaces, webtest.PathSources} {
		pages["signed in "+path] = normalisePage(webtest.PageSource(t, signedIn, path), signedIn.DataDir)
	}

	fresh := webtest.New(t)
	pages["signed out "+web.PathSetup] = normalisePage(webtest.PageSource(t, fresh, web.PathSetup), fresh.DataDir)
	fresh.CompleteSetup()
	signedOut := fresh.Reopen()
	pages["signed out "+web.PathSignIn] = normalisePage(webtest.PageSource(t, signedOut, web.PathSignIn),
		signedOut.DataDir)

	path := filepath.Join("testdata", "golden_pages.json.gz")
	if os.Getenv("OPNVIEW_WRITE_GOLDEN") == "1" {
		encoded, err := json.Marshal(pages)
		if err != nil {
			t.Fatal(err)
		}
		var compressed bytes.Buffer
		writer := gzip.NewWriter(&compressed)
		if _, err := writer.Write(encoded); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, compressed.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	reader, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	var golden map[string]string
	if err := json.Unmarshal(raw, &golden); err != nil {
		t.Fatal(err)
	}
	for name, want := range golden {
		got, present := pages[name]
		if !present {
			t.Errorf("%s: no longer rendered", name)
			continue
		}
		if got != want {
			t.Errorf("%s: the page changed\n%s", name, firstDifference(want, got))
		}
	}
}

// firstDifference shows where two pages first differ.
func firstDifference(want, got string) string {
	index := 0
	for index < len(want) && index < len(got) && want[index] == got[index] {
		index++
	}
	start := max(0, index-80)
	return "before: ..." + want[start:min(len(want), index+120)] + "\nafter:  ..." + got[start:min(len(got), index+120)]
}
