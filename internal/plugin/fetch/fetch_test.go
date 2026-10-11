package fetch

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// recordingTransport answers each URL with an arranged reply and records what reached it, with
// the Authorization header each request carried. Nothing leaves the process.
type recordingTransport struct {
	mutex    sync.Mutex
	replies  map[string]*http.Response
	bodies   map[string]string
	received []string
}

func newRecordingTransport() *recordingTransport {
	return &recordingTransport{replies: map[string]*http.Response{}, bodies: map[string]string{}}
}

func (r *recordingTransport) arrange(target string, status int, headers map[string]string, body string) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	header := http.Header{}
	for name, value := range headers {
		header.Set(name, value)
	}
	r.replies[target] = &http.Response{StatusCode: status, Header: header}
	r.bodies[target] = body
}

func (r *recordingTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	r.received = append(r.received, request.Method+" "+request.URL.String()+" auth="+
		request.Header.Get("Authorization")+" inm="+request.Header.Get("If-None-Match"))
	reply, arranged := r.replies[request.URL.String()]
	if !arranged {
		return &http.Response{StatusCode: http.StatusNotFound, Header: http.Header{},
			Body: io.NopCloser(bytes.NewReader(nil)), Request: request}, nil
	}
	if etag := reply.Header.Get("ETag"); etag != "" && request.Header.Get("If-None-Match") == etag {
		return &http.Response{StatusCode: http.StatusNotModified, Header: reply.Header.Clone(),
			Body: io.NopCloser(bytes.NewReader(nil)), Request: request}, nil
	}
	return &http.Response{StatusCode: reply.StatusCode, Header: reply.Header.Clone(),
		Body: io.NopCloser(strings.NewReader(r.bodies[request.URL.String()])), Request: request}, nil
}

func (r *recordingTransport) requests() []string {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	return append([]string(nil), r.received...)
}

func newFacility(t *testing.T, transport http.RoundTripper, destinations ...string) *Facility {
	t.Helper()
	return New("example", filepath.Join(t.TempDir(), "plugin-data", "example"), destinations, 5*time.Second, transport)
}

// TestARequestToAnUndeclaredHostIsRefusedBeforeAnythingIsSent is the guard: a host the manifest
// does not declare is refused before any connection, and so is a redirect to one.
func TestARequestToAnUndeclaredHostIsRefusedBeforeAnythingIsSent(t *testing.T) {
	transport := newRecordingTransport()
	facility := newFacility(t, transport, "https://list.example")
	ctx := context.Background()

	result := facility.Fetch(ctx, Request{URL: "https://elsewhere.example/list", File: "list.dat", MaxBytes: 1024})
	if result.Outcome != OutcomeHostRefused {
		t.Errorf("an undeclared host answered %q", result.Outcome)
	}
	transport.arrange("https://list.example/list", http.StatusFound,
		map[string]string{"Location": "https://elsewhere.example/list"}, "")
	transport.arrange("https://elsewhere.example/list", http.StatusOK, nil, "never served")
	result = facility.Fetch(ctx, Request{URL: "https://list.example/list", File: "list.dat", MaxBytes: 1024})
	if result.Outcome != OutcomeHostRefused {
		t.Errorf("a redirect to an undeclared host answered %q", result.Outcome)
	}
	for _, request := range transport.requests() {
		if strings.Contains(request, "elsewhere.example") {
			t.Errorf("a request reached an undeclared host: %s", request)
		}
	}
	if facility.Exists("list.dat") {
		t.Error("a refused fetch left a file")
	}
}

// TestHeadersGoToTheHostAskedAndNeverToTheOneARedirectLeadsTo is the credential rule MaxMind's
// redirect to its storage host depends on.
func TestHeadersGoToTheHostAskedAndNeverToTheOneARedirectLeadsTo(t *testing.T) {
	transport := newRecordingTransport()
	facility := newFacility(t, transport, "https://download.example", "https://storage.example")
	transport.arrange("https://download.example/db", http.StatusFound,
		map[string]string{"Location": "https://storage.example/db.tar.gz"}, "")
	transport.arrange("https://storage.example/db.tar.gz", http.StatusOK,
		map[string]string{"Last-Modified": "Thu, 01 Oct 2026 10:00:00 GMT"}, "archive")
	result := facility.Fetch(context.Background(), Request{URL: "https://download.example/db",
		Headers: map[string]string{"Authorization": "Basic example"}, File: "db.tar.gz", MaxBytes: 1024})
	if result.Outcome != OutcomeFetched {
		t.Fatalf("the fetch answered %+v", result)
	}
	requests := transport.requests()
	if len(requests) != 2 {
		t.Fatalf("the requests were %v", requests)
	}
	if !strings.Contains(requests[0], "auth=Basic example") {
		t.Errorf("the host asked did not receive the headers: %s", requests[0])
	}
	if !strings.Contains(requests[1], "auth= ") {
		t.Errorf("the host a redirect led to received the headers: %s", requests[1])
	}
	if text, err := facility.ReadText("db.tar.gz"); err != nil || text != "archive" {
		t.Errorf("the body kept is %q (%v)", text, err)
	}
}

// TestEachAnswerIsItsOwnOutcome: a refusal, a spent limit, another status and a HEAD.
func TestEachAnswerIsItsOwnOutcome(t *testing.T) {
	for _, answer := range []struct {
		status  int
		method  string
		outcome string
	}{
		{http.StatusUnauthorized, http.MethodGet, OutcomeRefused},
		{http.StatusForbidden, http.MethodGet, OutcomeRefused},
		{http.StatusTooManyRequests, http.MethodGet, OutcomeLimited},
		{http.StatusInternalServerError, http.MethodGet, OutcomeFailed},
		{http.StatusOK, http.MethodHead, OutcomeAnswered},
		{http.StatusOK, http.MethodGet, OutcomeFetched},
	} {
		transport := newRecordingTransport()
		facility := newFacility(t, transport, "https://list.example")
		transport.arrange("https://list.example/list", answer.status, nil, "body")
		result := facility.Fetch(context.Background(), Request{URL: "https://list.example/list",
			Method: answer.method, File: "list.dat", MaxBytes: 1024})
		if result.Outcome != answer.outcome {
			t.Errorf("a %s answered %d gave %q, want %q", answer.method, answer.status, result.Outcome, answer.outcome)
		}
		if answer.outcome != OutcomeFetched && facility.Exists("list.dat") {
			t.Errorf("a %d left a file", answer.status)
		}
	}
}

// TestAConditionalFetchKeepsTheCopyAndAFailedOneLeavesItInUse: an unchanged copy is not sent
// again, and neither a body the check refuses, nor one too large, replaces the copy held.
func TestAConditionalFetchKeepsTheCopyAndAFailedOneLeavesItInUse(t *testing.T) {
	transport := newRecordingTransport()
	facility := newFacility(t, transport, "https://list.example")
	ctx := context.Background()
	request := Request{URL: "https://list.example/list", File: "list.dat", MaxBytes: 64, Conditional: true,
		Check: func(temporary string) (bool, string) {
			text, err := facility.ReadText(temporary)
			if err != nil || !strings.HasPrefix(text, "list") {
				return false, "not the list"
			}
			return true, ""
		}}

	transport.arrange("https://list.example/list", http.StatusOK, map[string]string{"ETag": `"one"`}, "list one")
	if result := facility.Fetch(ctx, request); result.Outcome != OutcomeFetched {
		t.Fatalf("the first fetch answered %+v", result)
	}
	if result := facility.Fetch(ctx, request); result.Outcome != OutcomeNotModified || result.ETag != `"one"` {
		t.Errorf("an unchanged copy answered %+v", result)
	}
	if requests := transport.requests(); !strings.Contains(requests[1], `inm="one"`) {
		t.Errorf("the second request was not conditional: %s", requests[1])
	}

	transport.arrange("https://list.example/list", http.StatusOK, map[string]string{"ETag": `"two"`}, "<html>")
	if result := facility.Fetch(ctx, request); result.Outcome != OutcomeMalformed {
		t.Errorf("a body the check refused answered %+v", result)
	}
	transport.arrange("https://list.example/list", http.StatusOK, map[string]string{"ETag": `"three"`},
		"list "+strings.Repeat("x", 100))
	if result := facility.Fetch(ctx, request); result.Outcome != OutcomeFailed {
		t.Errorf("a body larger than its bound answered %+v", result)
	}
	if text, err := facility.ReadText("list.dat"); err != nil || text != "list one" {
		t.Errorf("a failed fetch replaced the copy in use: %q (%v)", text, err)
	}
	entries, err := os.ReadDir(facility.Directory())
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			t.Errorf("a failed fetch left %s behind", entry.Name())
		}
	}
	if err := facility.Remove("list.dat"); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(facility.Directory()); len(entries) != 0 {
		t.Errorf("removing a fetched file left %v", entries)
	}
}

// TestNoNameReachesOutsideTheDataDirectory: a name is a plain file name.
func TestNoNameReachesOutsideTheDataDirectory(t *testing.T) {
	facility := newFacility(t, newRecordingTransport(), "https://list.example")
	for _, name := range []string{"", ".", "..", "../escape", "a/b", `a\b`, ".hidden"} {
		if _, err := facility.Path(name); err == nil {
			t.Errorf("%q was accepted as a file name", name)
		}
	}
}
